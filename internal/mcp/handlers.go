package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/imtaebin/code-context-graph/internal/analysis/changes"
	"github.com/imtaebin/code-context-graph/internal/analysis/community"
	"github.com/imtaebin/code-context-graph/internal/analysis/deadcode"
	"github.com/imtaebin/code-context-graph/internal/analysis/incremental"
	"github.com/imtaebin/code-context-graph/internal/model"
	"github.com/imtaebin/code-context-graph/internal/ragindex"
)

type handlers struct {
	deps  *Deps
	cache *Cache
}

// mustJSON serializes v to a JSON string for use as a cache key.
func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func (h *handlers) logger() *slog.Logger {
	if h.deps.Logger != nil {
		return h.deps.Logger
	}
	return slog.Default()
}

func (h *handlers) parseProject(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	log := h.logger()

	dirPath, err := request.RequireString("path")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("missing parameter: %v", err)), nil
	}

	log.Info("parse_project called", "path", dirPath)

	entries, err := os.ReadDir(dirPath)
	if err != nil {
		log.Error("failed to read directory", "path", dirPath, "error", err)
		return mcp.NewToolResultError(fmt.Sprintf("read dir: %v", err)), nil
	}

	var parsed, errCount int
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		ext := filepath.Ext(entry.Name())
		if ext != ".go" && ext != ".py" && ext != ".ts" && ext != ".java" && ext != ".rb" {
			continue
		}

		fp := filepath.Join(dirPath, entry.Name())
		content, err := os.ReadFile(fp)
		if err != nil {
			log.Warn("failed to read file", "file", fp, "error", err)
			errCount++
			continue
		}

		nodes, edges, err := h.deps.Parser.Parse(fp, content)
		if err != nil {
			log.Warn("failed to parse file", "file", fp, "error", err)
			errCount++
			continue
		}

		log.Debug("parsed file", "file", fp, "nodes", len(nodes), "edges", len(edges))

		if len(nodes) > 0 {
			if err := h.deps.Store.UpsertNodes(ctx, nodes); err != nil {
				log.Error("failed to upsert nodes", "file", fp, "error", err)
				return mcp.NewToolResultError(fmt.Sprintf("upsert nodes: %v", err)), nil
			}
		}
		if len(edges) > 0 {
			if err := h.deps.Store.UpsertEdges(ctx, edges); err != nil {
				log.Error("failed to upsert edges", "file", fp, "error", err)
				return mcp.NewToolResultError(fmt.Sprintf("upsert edges: %v", err)), nil
			}
		}
		parsed++
	}

	log.Info("parse_project completed", "parsed", parsed, "errors", errCount)
	return mcp.NewToolResultText(fmt.Sprintf(`{"parsed":%d,"errors":%d}`, parsed, errCount)), nil
}

func (h *handlers) getNode(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	log := h.logger()

	qn, err := request.RequireString("qualified_name")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("missing parameter: %v", err)), nil
	}

	log.Info("get_node called", "qualified_name", qn)

	key := "get_node:" + mustJSON(map[string]any{"qualified_name": qn})
	if h.cache != nil {
		if cached, ok := h.cache.Get(key); ok {
			log.Debug("get_node cache hit", "qualified_name", qn)
			return mcp.NewToolResultText(cached), nil
		}
	}

	node, err := h.deps.Store.GetNode(ctx, qn)
	if err != nil {
		log.Error("store error", "tool", "get_node", "error", err)
		return mcp.NewToolResultError(fmt.Sprintf("store error: %v", err)), nil
	}
	if node == nil {
		log.Warn("node not found", "qualified_name", qn)
		return mcp.NewToolResultError(fmt.Sprintf("node %q not found", qn)), nil
	}

	data := map[string]any{
		"id":             node.ID,
		"qualified_name": node.QualifiedName,
		"kind":           node.Kind,
		"name":           node.Name,
		"file_path":      node.FilePath,
		"start_line":     node.StartLine,
		"end_line":       node.EndLine,
		"language":       node.Language,
	}
	b, _ := json.Marshal(data)
	result := string(b)
	if h.cache != nil {
		h.cache.Set(key, result)
	}
	return mcp.NewToolResultText(result), nil
}

func (h *handlers) getImpactRadius(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	log := h.logger()

	qn, err := request.RequireString("qualified_name")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("missing parameter: %v", err)), nil
	}
	depth := request.GetInt("depth", 1)

	log.Info("get_impact_radius called", "qualified_name", qn, "depth", depth)

	key := "get_impact_radius:" + mustJSON(map[string]any{"qualified_name": qn, "depth": depth})
	if h.cache != nil {
		if cached, ok := h.cache.Get(key); ok {
			log.Debug("get_impact_radius cache hit", "qualified_name", qn)
			return mcp.NewToolResultText(cached), nil
		}
	}

	node, err := h.deps.Store.GetNode(ctx, qn)
	if err != nil {
		log.Error("store error", "tool", "get_impact_radius", "error", err)
		return mcp.NewToolResultError(fmt.Sprintf("store error: %v", err)), nil
	}
	if node == nil {
		log.Warn("node not found", "qualified_name", qn)
		return mcp.NewToolResultError(fmt.Sprintf("node %q not found", qn)), nil
	}

	nodes, err := h.deps.ImpactAnalyzer.ImpactRadius(ctx, node.ID, depth)
	if err != nil {
		log.Error("impact analysis error", "node_id", node.ID, "error", err)
		return mcp.NewToolResultError(fmt.Sprintf("impact analysis error: %v", err)), nil
	}

	log.Info("get_impact_radius completed", "qualified_name", qn, "result_count", len(nodes))

	impactResult := make([]map[string]any, len(nodes))
	for i, n := range nodes {
		impactResult[i] = map[string]any{
			"id":             n.ID,
			"qualified_name": n.QualifiedName,
			"kind":           n.Kind,
			"name":           n.Name,
			"file_path":      n.FilePath,
		}
	}
	b, _ := json.Marshal(impactResult)
	result := string(b)
	if h.cache != nil {
		h.cache.Set(key, result)
	}
	return mcp.NewToolResultText(result), nil
}

func (h *handlers) search(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	log := h.logger()

	query, err := request.RequireString("query")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("missing parameter: %v", err)), nil
	}
	limit := request.GetInt("limit", 10)
	pathPrefix := request.GetString("path", "")

	log.Info("search called", "query", query, "limit", limit, "path", pathPrefix)

	key := "search:" + mustJSON(map[string]any{"query": query, "limit": limit, "path": pathPrefix})
	if h.cache != nil {
		if cached, ok := h.cache.Get(key); ok {
			log.Debug("search cache hit", "query", query)
			return mcp.NewToolResultText(cached), nil
		}
	}

	// When path filtering is active, fetch more results from FTS so
	// that after filtering we still have up to 'limit' results.
	fetchLimit := limit
	if pathPrefix != "" {
		fetchLimit = limit * 5
		if fetchLimit < 50 {
			fetchLimit = 50
		}
	}

	nodes, err := h.deps.SearchBackend.Query(ctx, h.deps.DB, query, fetchLimit)
	if err != nil {
		log.Error("search error", "query", query, "error", err)
		return mcp.NewToolResultError(fmt.Sprintf("search error: %v", err)), nil
	}

	if pathPrefix != "" {
		filtered := nodes[:0]
		for _, n := range nodes {
			if strings.HasPrefix(n.FilePath, pathPrefix) {
				filtered = append(filtered, n)
			}
		}
		nodes = filtered
		if len(nodes) > limit {
			nodes = nodes[:limit]
		}
	}

	log.Info("search completed", "query", query, "result_count", len(nodes))

	searchResult := make([]map[string]any, len(nodes))
	for i, n := range nodes {
		searchResult[i] = map[string]any{
			"id":             n.ID,
			"qualified_name": n.QualifiedName,
			"kind":           n.Kind,
			"name":           n.Name,
			"file_path":      n.FilePath,
		}
	}
	b, _ := json.Marshal(searchResult)
	result := string(b)
	if h.cache != nil {
		h.cache.Set(key, result)
	}
	return mcp.NewToolResultText(result), nil
}

func (h *handlers) getAnnotation(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	log := h.logger()

	qn, err := request.RequireString("qualified_name")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("missing parameter: %v", err)), nil
	}

	log.Info("get_annotation called", "qualified_name", qn)

	key := "get_annotation:" + mustJSON(map[string]any{"qualified_name": qn})
	if h.cache != nil {
		if cached, ok := h.cache.Get(key); ok {
			log.Debug("get_annotation cache hit", "qualified_name", qn)
			return mcp.NewToolResultText(cached), nil
		}
	}

	node, err := h.deps.Store.GetNode(ctx, qn)
	if err != nil {
		log.Error("store error", "tool", "get_annotation", "error", err)
		return mcp.NewToolResultError(fmt.Sprintf("store error: %v", err)), nil
	}
	if node == nil {
		log.Warn("node not found", "qualified_name", qn)
		return mcp.NewToolResultError(fmt.Sprintf("node %q not found", qn)), nil
	}

	ann, err := h.deps.Store.GetAnnotation(ctx, node.ID)
	if err != nil {
		log.Error("annotation error", "node_id", node.ID, "error", err)
		return mcp.NewToolResultError(fmt.Sprintf("annotation error: %v", err)), nil
	}
	if ann == nil {
		log.Warn("annotation not found", "qualified_name", qn)
		return mcp.NewToolResultError(fmt.Sprintf("no annotation for %q", qn)), nil
	}

	tags := make([]map[string]any, len(ann.Tags))
	for i, tag := range ann.Tags {
		tags[i] = map[string]any{
			"kind":    tag.Kind,
			"name":    tag.Name,
			"value":   tag.Value,
			"ordinal": tag.Ordinal,
		}
	}

	data := map[string]any{
		"summary": ann.Summary,
		"context": ann.Context,
		"tags":    tags,
	}
	b, _ := json.Marshal(data)
	result := string(b)
	if h.cache != nil {
		h.cache.Set(key, result)
	}
	return mcp.NewToolResultText(result), nil
}

func (h *handlers) traceFlow(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	log := h.logger()

	qn, err := request.RequireString("qualified_name")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("missing parameter: %v", err)), nil
	}

	log.Info("trace_flow called", "qualified_name", qn)

	key := "trace_flow:" + mustJSON(map[string]any{"qualified_name": qn})
	if h.cache != nil {
		if cached, ok := h.cache.Get(key); ok {
			log.Debug("trace_flow cache hit", "qualified_name", qn)
			return mcp.NewToolResultText(cached), nil
		}
	}

	node, err := h.deps.Store.GetNode(ctx, qn)
	if err != nil {
		log.Error("store error", "tool", "trace_flow", "error", err)
		return mcp.NewToolResultError(fmt.Sprintf("store error: %v", err)), nil
	}
	if node == nil {
		log.Warn("node not found", "qualified_name", qn)
		return mcp.NewToolResultError(fmt.Sprintf("node %q not found", qn)), nil
	}

	flow, err := h.deps.FlowTracer.TraceFlow(ctx, node.ID)
	if err != nil {
		log.Error("trace error", "node_id", node.ID, "error", err)
		return mcp.NewToolResultError(fmt.Sprintf("trace error: %v", err)), nil
	}

	log.Info("trace_flow completed", "qualified_name", qn, "members", len(flow.Members))

	members := make([]map[string]any, len(flow.Members))
	for i, m := range flow.Members {
		members[i] = map[string]any{
			"node_id": m.NodeID,
			"ordinal": m.Ordinal,
		}
	}

	data := map[string]any{
		"name":    flow.Name,
		"members": members,
	}
	b, _ := json.Marshal(data)
	result := string(b)
	if h.cache != nil {
		h.cache.Set(key, result)
	}
	return mcp.NewToolResultText(result), nil
}

func (h *handlers) buildOrUpdateGraph(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	log := h.logger()

	dirPath, err := request.RequireString("path")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("missing parameter: %v", err)), nil
	}

	fullRebuild := true
	if v, err := request.RequireString("full_rebuild"); err == nil {
		fullRebuild = v == "true"
	} else {
		// try bool from arguments
		if args, ok := request.Params.Arguments.(map[string]any); ok {
			if fb, ok := args["full_rebuild"]; ok {
				switch val := fb.(type) {
				case bool:
					fullRebuild = val
				case string:
					fullRebuild = val == "true"
				}
			}
		}
	}

	postprocess := "full"
	if args, ok := request.Params.Arguments.(map[string]any); ok {
		if pp, ok := args["postprocess"].(string); ok && pp != "" {
			postprocess = pp
		}
	}

	log.Info("build_or_update_graph called", "path", dirPath, "full_rebuild", fullRebuild, "postprocess", postprocess)

	start := time.Now()
	var nodeCount, edgeCount, fileCount int

	if fullRebuild || h.deps.Incremental == nil {
		// 전체 파싱: 디렉토리를 재귀 탐색
		err := filepath.Walk(dirPath, func(fp string, info os.FileInfo, err error) error {
			if err != nil {
				return nil // skip errors
			}
			if info.IsDir() {
				return nil
			}
			ext := filepath.Ext(fp)
			if ext != ".go" && ext != ".py" && ext != ".ts" && ext != ".java" && ext != ".rb" {
				return nil
			}

			content, err := os.ReadFile(fp)
			if err != nil {
				log.Warn("failed to read file", "file", fp, "error", err)
				return nil
			}

			nodes, edges, err := h.deps.Parser.Parse(fp, content)
			if err != nil {
				log.Warn("failed to parse file", "file", fp, "error", err)
				return nil
			}

			if len(nodes) > 0 {
				if err := h.deps.Store.UpsertNodes(ctx, nodes); err != nil {
					return fmt.Errorf("upsert nodes: %w", err)
				}
				nodeCount += len(nodes)
			}
			if len(edges) > 0 {
				if err := h.deps.Store.UpsertEdges(ctx, edges); err != nil {
					return fmt.Errorf("upsert edges: %w", err)
				}
				edgeCount += len(edges)
			}
			fileCount++
			return nil
		})
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("walk error: %v", err)), nil
		}
	} else {
		// 증분 빌드
		files := map[string]incremental.FileInfo{}
		err := filepath.Walk(dirPath, func(fp string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if info.IsDir() {
				return nil
			}
			ext := filepath.Ext(fp)
			if ext != ".go" && ext != ".py" && ext != ".ts" && ext != ".java" && ext != ".rb" {
				return nil
			}
			content, err := os.ReadFile(fp)
			if err != nil {
				return nil
			}
			hash := sha256.Sum256(content)
			files[fp] = incremental.FileInfo{
				Hash:    hex.EncodeToString(hash[:]),
				Content: content,
			}
			return nil
		})
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("walk error: %v", err)), nil
		}

		stats, err := h.deps.Incremental.Sync(ctx, files)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("incremental sync error: %v", err)), nil
		}
		fileCount = stats.Added + stats.Modified
	}

	// 후처리
	switch postprocess {
	case "full":
		// flows 재빌드 (FlowTracer는 노드별이므로 스킵 — 전체 flow는 별도)
		// community 재빌드
		if h.deps.CommunityBuilder != nil {
			_, err := h.deps.CommunityBuilder.Rebuild(ctx, community.Config{Depth: 2})
			if err != nil {
				log.Warn("community rebuild failed", "error", err)
			}
		}
		// search 재빌드
		if h.deps.SearchBackend != nil {
			if err := h.deps.SearchBackend.Rebuild(ctx, h.deps.DB); err != nil {
				log.Warn("search rebuild failed", "error", err)
			}
		}
	case "minimal":
		// search만 재빌드
		if h.deps.SearchBackend != nil {
			if err := h.deps.SearchBackend.Rebuild(ctx, h.deps.DB); err != nil {
				log.Warn("search rebuild failed", "error", err)
			}
		}
	case "none":
		// 스킵
	}

	elapsed := time.Since(start).Milliseconds()

	result := map[string]any{
		"status":        "ok",
		"files_parsed":  fileCount,
		"nodes_created": nodeCount,
		"edges_created": edgeCount,
		"elapsed_ms":    elapsed,
	}
	b, _ := json.Marshal(result)
	if h.cache != nil {
		h.cache.Flush()
	}
	return mcp.NewToolResultText(string(b)), nil
}

func (h *handlers) runPostprocess(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	log := h.logger()

	doFlows := getBool(request, "flows", true)
	doCommunities := getBool(request, "communities", true)
	doFTS := getBool(request, "fts", true)
	communityDepth := request.GetInt("community_depth", 2)

	log.Info("run_postprocess called", "flows", doFlows, "communities", doCommunities, "fts", doFTS)

	var flowsCount, communitiesCount, ftsIndexed int

	if doFlows {
		// FlowTracer operates per-node; for bulk rebuild we skip or iterate
		// For now, flows count stays 0 unless we have a bulk flow builder
	}

	if doCommunities && h.deps.CommunityBuilder != nil {
		stats, err := h.deps.CommunityBuilder.Rebuild(ctx, community.Config{Depth: communityDepth})
		if err != nil {
			log.Warn("community rebuild failed", "error", err)
		} else {
			communitiesCount = len(stats)
		}
	}

	if doFTS && h.deps.SearchBackend != nil {
		if err := h.deps.SearchBackend.Rebuild(ctx, h.deps.DB); err != nil {
			log.Warn("search rebuild failed", "error", err)
		} else {
			ftsIndexed = 1 // at least one rebuild happened
		}
	}

	result := map[string]any{
		"status":            "ok",
		"flows_count":       flowsCount,
		"communities_count": communitiesCount,
		"fts_indexed":       ftsIndexed,
	}
	b, _ := json.Marshal(result)
	if h.cache != nil {
		h.cache.Flush()
	}
	return mcp.NewToolResultText(string(b)), nil
}

func (h *handlers) queryGraph(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	log := h.logger()

	pattern, err := request.RequireString("pattern")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("missing parameter: %v", err)), nil
	}
	target, err := request.RequireString("target")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("missing parameter: %v", err)), nil
	}

	log.Info("query_graph called", "pattern", pattern, "target", target)

	key := "query_graph:" + mustJSON(map[string]any{"pattern": pattern, "target": target})
	if h.cache != nil {
		if cached, ok := h.cache.Get(key); ok {
			log.Debug("query_graph cache hit", "pattern", pattern, "target", target)
			return mcp.NewToolResultText(cached), nil
		}
	}

	// 패턴 유효성 검사
	validPatterns := map[string]bool{
		"callers_of": true, "callees_of": true, "imports_of": true,
		"importers_of": true, "children_of": true, "tests_for": true,
		"inheritors_of": true, "file_summary": true,
	}
	if !validPatterns[pattern] {
		return mcp.NewToolResultError(fmt.Sprintf("unknown pattern: %q", pattern)), nil
	}

	// file_summary는 노드 조회가 불필요
	if pattern == "file_summary" {
		if h.deps.QueryService == nil {
			return mcp.NewToolResultError("QueryService not configured"), nil
		}
		summary, err := h.deps.QueryService.FileSummaryOf(ctx, target)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("file summary error: %v", err)), nil
		}
		fsData := map[string]any{
			"pattern": pattern,
			"target":  target,
			"results": summary,
		}
		b, _ := json.Marshal(fsData)
		result := string(b)
		if h.cache != nil {
			h.cache.Set(key, result)
		}
		return mcp.NewToolResultText(result), nil
	}

	// 나머지 패턴은 노드를 먼저 조회
	node, err := h.deps.Store.GetNode(ctx, target)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("store error: %v", err)), nil
	}
	if node == nil {
		return mcp.NewToolResultError(fmt.Sprintf("node %q not found", target)), nil
	}

	if h.deps.QueryService == nil {
		return mcp.NewToolResultError("QueryService not configured"), nil
	}

	var nodes []model.Node
	switch pattern {
	case "callers_of":
		nodes, err = h.deps.QueryService.CallersOf(ctx, node.ID)
	case "callees_of":
		nodes, err = h.deps.QueryService.CalleesOf(ctx, node.ID)
	case "imports_of":
		nodes, err = h.deps.QueryService.ImportsOf(ctx, node.ID)
	case "importers_of":
		nodes, err = h.deps.QueryService.ImportersOf(ctx, node.ID)
	case "children_of":
		nodes, err = h.deps.QueryService.ChildrenOf(ctx, node.ID)
	case "tests_for":
		nodes, err = h.deps.QueryService.TestsFor(ctx, node.ID)
	case "inheritors_of":
		nodes, err = h.deps.QueryService.InheritorsOf(ctx, node.ID)
	}

	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("query error: %v", err)), nil
	}

	qgResults := make([]map[string]any, len(nodes))
	for i, n := range nodes {
		qgResults[i] = map[string]any{
			"id":             n.ID,
			"qualified_name": n.QualifiedName,
			"kind":           n.Kind,
			"name":           n.Name,
			"file_path":      n.FilePath,
		}
	}

	resp := map[string]any{
		"pattern": pattern,
		"target":  target,
		"results": qgResults,
	}
	b, _ := json.Marshal(resp)
	result := string(b)
	if h.cache != nil {
		h.cache.Set(key, result)
	}
	return mcp.NewToolResultText(result), nil
}

func (h *handlers) listGraphStats(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	log := h.logger()
	log.Info("list_graph_stats called")

	key := "list_graph_stats:" + mustJSON(map[string]any{})
	if h.cache != nil {
		if cached, ok := h.cache.Get(key); ok {
			log.Debug("list_graph_stats cache hit")
			return mcp.NewToolResultText(cached), nil
		}
	}

	var nodeCount, edgeCount int64
	h.deps.DB.WithContext(ctx).Model(&model.Node{}).Count(&nodeCount)
	h.deps.DB.WithContext(ctx).Model(&model.Edge{}).Count(&edgeCount)

	type kindCount struct {
		Kind  string
		Count int64
	}

	var nodesByKind []kindCount
	h.deps.DB.WithContext(ctx).Model(&model.Node{}).
		Select("kind, COUNT(*) as count").
		Group("kind").Scan(&nodesByKind)

	var nodesByLang []kindCount
	h.deps.DB.WithContext(ctx).Model(&model.Node{}).
		Select("language as kind, COUNT(*) as count").
		Where("language != ''").
		Group("language").Scan(&nodesByLang)

	var edgesByKind []kindCount
	h.deps.DB.WithContext(ctx).Model(&model.Edge{}).
		Select("kind, COUNT(*) as count").
		Group("kind").Scan(&edgesByKind)

	nbk := map[string]int64{}
	for _, k := range nodesByKind {
		nbk[k.Kind] = k.Count
	}
	nbl := map[string]int64{}
	for _, k := range nodesByLang {
		nbl[k.Kind] = k.Count
	}
	ebk := map[string]int64{}
	for _, k := range edgesByKind {
		ebk[k.Kind] = k.Count
	}

	statsData := map[string]any{
		"total_nodes":       nodeCount,
		"total_edges":       edgeCount,
		"nodes_by_kind":     nbk,
		"nodes_by_language": nbl,
		"edges_by_kind":     ebk,
	}
	b, _ := json.Marshal(statsData)
	result := string(b)
	if h.cache != nil {
		h.cache.Set(key, result)
	}
	return mcp.NewToolResultText(result), nil
}

func (h *handlers) findLargeFunctions(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	log := h.logger()

	minLines := request.GetInt("min_lines", 50)
	limit := request.GetInt("limit", 50)
	pathPrefix := request.GetString("path", "")

	log.Info("find_large_functions called", "min_lines", minLines, "limit", limit, "path", pathPrefix)

	key := "find_large_functions:" + mustJSON(map[string]any{"min_lines": minLines, "limit": limit, "path": pathPrefix})
	if h.cache != nil {
		if cached, ok := h.cache.Get(key); ok {
			log.Debug("find_large_functions cache hit")
			return mcp.NewToolResultText(cached), nil
		}
	}

	if h.deps.LargefuncAnalyzer == nil {
		return mcp.NewToolResultError("LargefuncAnalyzer not configured"), nil
	}

	nodes, err := h.deps.LargefuncAnalyzer.Find(ctx, minLines)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("largefunc error: %v", err)), nil
	}

	if pathPrefix != "" {
		filtered := nodes[:0]
		for _, n := range nodes {
			if strings.HasPrefix(n.FilePath, pathPrefix) {
				filtered = append(filtered, n)
			}
		}
		nodes = filtered
	}

	if len(nodes) > limit {
		nodes = nodes[:limit]
	}

	lfResults := make([]map[string]any, len(nodes))
	for i, n := range nodes {
		lines := n.EndLine - n.StartLine + 1
		lfResults[i] = map[string]any{
			"name":  n.QualifiedName,
			"file":  n.FilePath,
			"lines": lines,
		}
	}

	resp := map[string]any{
		"results": lfResults,
		"count":   len(lfResults),
	}
	b, _ := json.Marshal(resp)
	result := string(b)
	if h.cache != nil {
		h.cache.Set(key, result)
	}
	return mcp.NewToolResultText(result), nil
}

func (h *handlers) detectChanges(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	log := h.logger()

	repoRoot, err := request.RequireString("repo_root")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("missing parameter: %v", err)), nil
	}
	base := request.GetString("base", "HEAD~1")

	log.Info("detect_changes called", "repo_root", repoRoot, "base", base)

	key := "detect_changes:" + mustJSON(map[string]any{"repo_root": repoRoot, "base": base})
	if h.cache != nil {
		if cached, ok := h.cache.Get(key); ok {
			log.Debug("detect_changes cache hit", "repo_root", repoRoot, "base", base)
			return mcp.NewToolResultText(cached), nil
		}
	}

	if h.deps.ChangesGitClient == nil {
		return mcp.NewToolResultError("ChangesGitClient not configured"), nil
	}

	chSvc := changes.New(h.deps.DB, h.deps.ChangesGitClient)
	risks, err := chSvc.Analyze(ctx, repoRoot, base)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("changes analyze error: %v", err)), nil
	}

	entries := make([]map[string]any, len(risks))
	for i, r := range risks {
		entries[i] = map[string]any{
			"name":       r.Node.QualifiedName,
			"file":       r.Node.FilePath,
			"hunk_count": r.HunkCount,
			"risk_score": r.RiskScore,
		}
	}

	resp := map[string]any{
		"base":    base,
		"entries": entries,
	}
	b, _ := json.Marshal(resp)
	result := string(b)
	if h.cache != nil {
		h.cache.Set(key, result)
	}
	return mcp.NewToolResultText(result), nil
}

func (h *handlers) getAffectedFlows(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	log := h.logger()

	repoRoot, err := request.RequireString("repo_root")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("missing parameter: %v", err)), nil
	}
	base := request.GetString("base", "HEAD~1")

	log.Info("get_affected_flows called", "repo_root", repoRoot, "base", base)

	key := "get_affected_flows:" + mustJSON(map[string]any{"repo_root": repoRoot, "base": base})
	if h.cache != nil {
		if cached, ok := h.cache.Get(key); ok {
			log.Debug("get_affected_flows cache hit", "repo_root", repoRoot, "base", base)
			return mcp.NewToolResultText(cached), nil
		}
	}

	if h.deps.ChangesGitClient == nil {
		return mcp.NewToolResultError("ChangesGitClient not configured"), nil
	}

	chSvc := changes.New(h.deps.DB, h.deps.ChangesGitClient)
	risks, err := chSvc.Analyze(ctx, repoRoot, base)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("changes analyze error: %v", err)), nil
	}

	if len(risks) == 0 {
		emptyResp := map[string]any{"affected_flows": []any{}}
		b, _ := json.Marshal(emptyResp)
		result := string(b)
		if h.cache != nil {
			h.cache.Set(key, result)
		}
		return mcp.NewToolResultText(result), nil
	}

	// 변경된 노드 ID 수집
	changedNodeIDs := make([]uint, 0, len(risks))
	for _, r := range risks {
		changedNodeIDs = append(changedNodeIDs, r.Node.ID)
	}

	// FlowMembership 조회
	var memberships []model.FlowMembership
	h.deps.DB.WithContext(ctx).Where("node_id IN ?", changedNodeIDs).Find(&memberships)

	// Flow별로 영향받는 노드 그룹화
	flowNodes := map[uint][]uint{}
	for _, m := range memberships {
		flowNodes[m.FlowID] = append(flowNodes[m.FlowID], m.NodeID)
	}

	if len(flowNodes) == 0 {
		emptyResp := map[string]any{"affected_flows": []any{}}
		b, _ := json.Marshal(emptyResp)
		result := string(b)
		if h.cache != nil {
			h.cache.Set(key, result)
		}
		return mcp.NewToolResultText(result), nil
	}

	flowIDs := make([]uint, 0, len(flowNodes))
	for fid := range flowNodes {
		flowIDs = append(flowIDs, fid)
	}

	var flowList []model.Flow
	h.deps.DB.WithContext(ctx).Where("id IN ?", flowIDs).Find(&flowList)

	affected := make([]map[string]any, len(flowList))
	for i, f := range flowList {
		affected[i] = map[string]any{
			"id":             f.ID,
			"name":           f.Name,
			"affected_nodes": flowNodes[f.ID],
		}
	}

	resp := map[string]any{"affected_flows": affected}
	b, _ := json.Marshal(resp)
	result := string(b)
	if h.cache != nil {
		h.cache.Set(key, result)
	}
	return mcp.NewToolResultText(result), nil
}

func (h *handlers) listFlows(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	log := h.logger()

	sortBy := request.GetString("sort_by", "name")
	limit := request.GetInt("limit", 50)

	log.Info("list_flows called", "sort_by", sortBy, "limit", limit)

	key := "list_flows:" + mustJSON(map[string]any{"sort_by": sortBy, "limit": limit})
	if h.cache != nil {
		if cached, ok := h.cache.Get(key); ok {
			log.Debug("list_flows cache hit")
			return mcp.NewToolResultText(cached), nil
		}
	}

	var flowList []model.Flow
	h.deps.DB.WithContext(ctx).Find(&flowList)

	type flowInfo struct {
		ID          uint   `json:"id"`
		Name        string `json:"name"`
		Description string `json:"description"`
		NodeCount   int    `json:"node_count"`
	}

	infos := make([]flowInfo, len(flowList))
	for i, f := range flowList {
		var memberCount int64
		h.deps.DB.WithContext(ctx).Model(&model.FlowMembership{}).
			Where("flow_id = ?", f.ID).Count(&memberCount)
		infos[i] = flowInfo{
			ID:          f.ID,
			Name:        f.Name,
			Description: f.Description,
			NodeCount:   int(memberCount),
		}
	}

	// 정렬
	switch sortBy {
	case "node_count":
		for i := 0; i < len(infos)-1; i++ {
			for j := i + 1; j < len(infos); j++ {
				if infos[j].NodeCount > infos[i].NodeCount {
					infos[i], infos[j] = infos[j], infos[i]
				}
			}
		}
	default: // "name"
		for i := 0; i < len(infos)-1; i++ {
			for j := i + 1; j < len(infos); j++ {
				if infos[j].Name < infos[i].Name {
					infos[i], infos[j] = infos[j], infos[i]
				}
			}
		}
	}

	if len(infos) > limit {
		infos = infos[:limit]
	}

	resp := map[string]any{"flows": infos}
	b, _ := json.Marshal(resp)
	result := string(b)
	if h.cache != nil {
		h.cache.Set(key, result)
	}
	return mcp.NewToolResultText(result), nil
}

func (h *handlers) listCommunities(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	log := h.logger()

	sortBy := request.GetString("sort_by", "size")
	minSize := request.GetInt("min_size", 0)

	log.Info("list_communities called", "sort_by", sortBy, "min_size", minSize)

	key := "list_communities:" + mustJSON(map[string]any{"sort_by": sortBy, "min_size": minSize})
	if h.cache != nil {
		if cached, ok := h.cache.Get(key); ok {
			log.Debug("list_communities cache hit")
			return mcp.NewToolResultText(cached), nil
		}
	}

	var communities []model.Community
	h.deps.DB.WithContext(ctx).Find(&communities)

	type commInfo struct {
		ID        uint    `json:"id"`
		Label     string  `json:"label"`
		NodeCount int     `json:"node_count"`
		Cohesion  float64 `json:"cohesion"`
	}

	infos := make([]commInfo, 0)
	for _, c := range communities {
		var memberCount int64
		h.deps.DB.WithContext(ctx).Model(&model.CommunityMembership{}).
			Where("community_id = ?", c.ID).Count(&memberCount)

		if int(memberCount) < minSize {
			continue
		}

		infos = append(infos, commInfo{
			ID:        c.ID,
			Label:     c.Label,
			NodeCount: int(memberCount),
		})
	}

	// 정렬
	switch sortBy {
	case "name":
		for i := 0; i < len(infos)-1; i++ {
			for j := i + 1; j < len(infos); j++ {
				if infos[j].Label < infos[i].Label {
					infos[i], infos[j] = infos[j], infos[i]
				}
			}
		}
	default: // "size"
		for i := 0; i < len(infos)-1; i++ {
			for j := i + 1; j < len(infos); j++ {
				if infos[j].NodeCount > infos[i].NodeCount {
					infos[i], infos[j] = infos[j], infos[i]
				}
			}
		}
	}

	resp := map[string]any{"communities": infos}
	b, _ := json.Marshal(resp)
	commResult := string(b)
	if h.cache != nil {
		h.cache.Set(key, commResult)
	}
	return mcp.NewToolResultText(commResult), nil
}

func (h *handlers) getCommunity(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	log := h.logger()

	communityID := request.GetInt("community_id", 0)
	if communityID == 0 {
		return mcp.NewToolResultError("missing parameter: community_id"), nil
	}
	includeMembers := getBool(request, "include_members", false)

	log.Info("get_community called", "community_id", communityID, "include_members", includeMembers)

	key := "get_community:" + mustJSON(map[string]any{"community_id": communityID, "include_members": includeMembers})
	if h.cache != nil {
		if cached, ok := h.cache.Get(key); ok {
			log.Debug("get_community cache hit", "community_id", communityID)
			return mcp.NewToolResultText(cached), nil
		}
	}

	var comm model.Community
	if err := h.deps.DB.WithContext(ctx).First(&comm, communityID).Error; err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("community %d not found", communityID)), nil
	}

	var memberCount int64
	h.deps.DB.WithContext(ctx).Model(&model.CommunityMembership{}).
		Where("community_id = ?", comm.ID).Count(&memberCount)

	gcData := map[string]any{
		"id":         comm.ID,
		"label":      comm.Label,
		"node_count": memberCount,
	}

	// 커버리지 조회
	if h.deps.CoverageAnalyzer != nil {
		cc, err := h.deps.CoverageAnalyzer.ByCommunity(ctx, comm.ID)
		if err == nil && cc != nil {
			gcData["coverage"] = cc.Ratio
		}
	}

	if includeMembers {
		var memberships []model.CommunityMembership
		h.deps.DB.WithContext(ctx).Where("community_id = ?", comm.ID).Find(&memberships)

		nodeIDs := make([]uint, len(memberships))
		for i, m := range memberships {
			nodeIDs[i] = m.NodeID
		}

		var nodes []model.Node
		if len(nodeIDs) > 0 {
			h.deps.DB.WithContext(ctx).Where("id IN ?", nodeIDs).Find(&nodes)
		}

		members := make([]map[string]any, len(nodes))
		for i, n := range nodes {
			members[i] = map[string]any{
				"id":             n.ID,
				"qualified_name": n.QualifiedName,
				"kind":           n.Kind,
				"name":           n.Name,
				"file_path":      n.FilePath,
			}
		}
		gcData["members"] = members
	}

	b, _ := json.Marshal(gcData)
	result := string(b)
	if h.cache != nil {
		h.cache.Set(key, result)
	}
	return mcp.NewToolResultText(result), nil
}

func (h *handlers) getArchitectureOverview(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	log := h.logger()
	log.Info("get_architecture_overview called")

	key := "get_architecture_overview:" + mustJSON(map[string]any{})
	if h.cache != nil {
		if cached, ok := h.cache.Get(key); ok {
			log.Debug("get_architecture_overview cache hit")
			return mcp.NewToolResultText(cached), nil
		}
	}

	var communities []model.Community
	h.deps.DB.WithContext(ctx).Find(&communities)

	if len(communities) == 0 {
		emptyResp := map[string]any{
			"communities": []any{},
			"coupling":    []any{},
			"warnings":    []string{"No communities found. Run community rebuild first."},
		}
		b, _ := json.Marshal(emptyResp)
		result := string(b)
		if h.cache != nil {
			h.cache.Set(key, result)
		}
		return mcp.NewToolResultText(result), nil
	}

	commInfos := make([]map[string]any, len(communities))
	for i, c := range communities {
		var memberCount int64
		h.deps.DB.WithContext(ctx).Model(&model.CommunityMembership{}).
			Where("community_id = ?", c.ID).Count(&memberCount)
		commInfos[i] = map[string]any{
			"id":         c.ID,
			"label":      c.Label,
			"node_count": memberCount,
		}
	}

	var couplingPairs []map[string]any
	var warnings []string

	if h.deps.CouplingAnalyzer != nil {
		pairs, err := h.deps.CouplingAnalyzer.Analyze(ctx)
		if err == nil {
			for _, cp := range pairs {
				couplingPairs = append(couplingPairs, map[string]any{
					"from":       cp.FromCommunity,
					"to":         cp.ToCommunity,
					"edge_count": cp.EdgeCount,
					"strength":   cp.Strength,
				})
				if cp.Strength > 0.8 {
					warnings = append(warnings, fmt.Sprintf("High coupling between %s and %s (strength: %.2f)", cp.FromCommunity, cp.ToCommunity, cp.Strength))
				}
			}
		}
	}

	if couplingPairs == nil {
		couplingPairs = []map[string]any{}
	}
	if warnings == nil {
		warnings = []string{}
	}

	resp := map[string]any{
		"communities": commInfos,
		"coupling":    couplingPairs,
		"warnings":    warnings,
	}
	b, _ := json.Marshal(resp)
	result := string(b)
	if h.cache != nil {
		h.cache.Set(key, result)
	}
	return mcp.NewToolResultText(result), nil
}

func (h *handlers) findDeadCode(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	log := h.logger()
	log.Info("find_dead_code called")

	opts := deadcode.Options{}
	var pathPrefix string
	var kinds []string

	// kinds 파라미터 처리
	if args, ok := request.Params.Arguments.(map[string]any); ok {
		if kindsRaw, ok := args["kinds"]; ok {
			if kindsArr, ok := kindsRaw.([]any); ok {
				for _, k := range kindsArr {
					if ks, ok := k.(string); ok {
						opts.Kinds = append(opts.Kinds, model.NodeKind(ks))
						kinds = append(kinds, ks)
					}
				}
			}
		}
		if fp, ok := args["path"].(string); ok {
			opts.FilePattern = fp
			pathPrefix = fp
		}
	}

	key := "find_dead_code:" + mustJSON(map[string]any{"path": pathPrefix, "kinds": kinds})
	if h.cache != nil {
		if cached, ok := h.cache.Get(key); ok {
			log.Debug("find_dead_code cache hit")
			return mcp.NewToolResultText(cached), nil
		}
	}

	if h.deps.DeadcodeAnalyzer == nil {
		return mcp.NewToolResultError("DeadcodeAnalyzer not configured"), nil
	}

	nodes, err := h.deps.DeadcodeAnalyzer.Find(ctx, opts)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("deadcode error: %v", err)), nil
	}

	dcResults := make([]map[string]any, len(nodes))
	for i, n := range nodes {
		dcResults[i] = map[string]any{
			"name":       n.QualifiedName,
			"kind":       n.Kind,
			"file":       n.FilePath,
			"start_line": n.StartLine,
		}
	}

	resp := map[string]any{
		"dead_code": dcResults,
		"count":     len(dcResults),
	}
	b, _ := json.Marshal(resp)
	result := string(b)
	if h.cache != nil {
		h.cache.Set(key, result)
	}
	return mcp.NewToolResultText(result), nil
}

// ragIndexPath는 doc-index.json의 실효 경로를 반환한다.
// deps.RagIndexDir이 비어 있으면 ".ccg"를 기본값으로 사용한다.
func (h *handlers) ragIndexPath() string {
	dir := h.deps.RagIndexDir
	if dir == "" {
		dir = ".ccg"
	}
	return filepath.Join(dir, "doc-index.json")
}

func (h *handlers) buildRagIndex(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	outDir := request.GetString("out_dir", "")
	indexDir := request.GetString("index_dir", "")

	// Fall back to deps defaults
	if indexDir == "" {
		indexDir = h.deps.RagIndexDir
	}

	b := &ragindex.Builder{
		DB:          h.deps.DB,
		OutDir:      outDir,   // empty string → Builder uses "docs" default
		IndexDir:    indexDir, // empty string → Builder uses ".ccg" default
		ProjectDesc: h.deps.RagProjectDesc,
	}
	communities, files, err := b.Build()
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("build rag index: %v", err)), nil
	}
	if h.cache != nil {
		h.cache.Flush()
	}
	return mcp.NewToolResultText(fmt.Sprintf("Built doc-index: %d communities, %d files", communities, files)), nil
}

func (h *handlers) getRagTree(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	communityID := request.GetString("community_id", "")
	depth := int(request.GetFloat("depth", 0))

	key := "get_rag_tree:" + mustJSON(map[string]any{"community_id": communityID, "depth": depth})
	if h.cache != nil {
		if cached, ok := h.cache.Get(key); ok {
			return mcp.NewToolResultText(cached), nil
		}
	}

	idx, err := ragindex.LoadIndex(h.ragIndexPath())
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("load doc-index: %v", err)), nil
	}

	var node *ragindex.TreeNode
	if communityID == "" {
		node = idx.Root
	} else {
		node = ragindex.FindNode(idx.Root, communityID)
		if node == nil {
			return mcp.NewToolResultError(fmt.Sprintf("community_id %q not found", communityID)), nil
		}
	}

	if depth > 0 {
		node = ragindex.PruneTree(node, depth)
	}

	b, err := json.Marshal(node)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("marshal tree: %v", err)), nil
	}
	result := string(b)
	if h.cache != nil {
		h.cache.Set(key, result)
	}
	return mcp.NewToolResultText(result), nil
}

func (h *handlers) getDocContent(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	filePath, err := request.RequireString("file_path")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("missing parameter: %v", err)), nil
	}

	// Path traversal protection
	clean := filepath.Clean(filePath)
	if filepath.IsAbs(clean) || strings.HasPrefix(clean, "..") {
		return mcp.NewToolResultError("invalid file_path: path traversal not allowed"), nil
	}

	const maxDocFileSizeBytes = 1 << 20 // 1 MB

	// Include mtime in cache key to detect file changes; also enforce size limit
	var mtime int64
	if stat, statErr := os.Stat(clean); statErr == nil {
		if stat.Size() > maxDocFileSizeBytes {
			return mcp.NewToolResultError(fmt.Sprintf("file %q exceeds 1 MB size limit (%d bytes)", filePath, stat.Size())), nil
		}
		mtime = stat.ModTime().UnixNano()
	}

	key := "get_doc_content:" + mustJSON(map[string]any{"file_path": filePath, "mtime": mtime})
	if h.cache != nil {
		if cached, ok := h.cache.Get(key); ok {
			return mcp.NewToolResultText(cached), nil
		}
	}

	content, err := os.ReadFile(clean)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("read file %q: %v. Run 'ccg docs' to generate documentation files.", filePath, err)), nil
	}
	result := string(content)
	if h.cache != nil {
		h.cache.Set(key, result)
	}
	return mcp.NewToolResultText(result), nil
}

func (h *handlers) searchDocs(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	query, err := request.RequireString("query")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("missing parameter: %v", err)), nil
	}
	limit := request.GetInt("limit", 10)
	if limit <= 0 {
		limit = 10
	}

	key := "search_docs:" + mustJSON(map[string]any{"query": query, "limit": limit})
	if h.cache != nil {
		if cached, ok := h.cache.Get(key); ok {
			return mcp.NewToolResultText(cached), nil
		}
	}

	idx, err := ragindex.LoadIndex(h.ragIndexPath())
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("load doc-index: %v", err)), nil
	}

	results := ragindex.Search(idx.Root, query, limit)

	b, err := json.Marshal(results)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("marshal results: %v", err)), nil
	}
	resultStr := string(b)
	if h.cache != nil {
		h.cache.Set(key, resultStr)
	}
	return mcp.NewToolResultText(resultStr), nil
}

func getBool(request mcp.CallToolRequest, name string, defaultVal bool) bool {
	if args, ok := request.Params.Arguments.(map[string]any); ok {
		if v, ok := args[name]; ok {
			switch val := v.(type) {
			case bool:
				return val
			case string:
				return val == "true"
			}
		}
	}
	return defaultVal
}
