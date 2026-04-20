package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/tae2089/trace"

	"github.com/tae2089/code-context-graph/internal/ctxns"
	"github.com/tae2089/code-context-graph/internal/model"
)

// getNode returns detailed metadata for a graph node by qualified name.
// @intent 정규화 이름으로 노드를 조회해 위치와 종류 등 기본 식별 정보를 제공한다.
// @param request qualified_name은 조회할 노드의 전체 이름이다.
// @requires 대상 노드가 그래프 저장소에 존재해야 한다.
// @ensures 성공 시 노드 메타데이터를 JSON으로 반환한다.
// @see mcp.handlers.getAnnotation
func (h *handlers) getNode(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ctx = h.applyWorkspace(ctx, request)
	log := h.logger()

	qn, err := request.RequireString("qualified_name")
	if err != nil {
		return missingParamResult(err)
	}

	log.Info("get_node called", "qualified_name", qn)

	return finalizeToolResult(h.cachedExecute("get_node:", map[string]any{"qualified_name": qn, "workspace": request.GetString("workspace", "")}, func() (string, error) {
		node, err := h.deps.Store.GetNode(ctx, qn)
		if err != nil {
			log.Error("store error", "tool", "get_node", trace.SlogError(err))
			return "", trace.Wrap(err, "store error")
		}
		if node == nil {
			log.Warn("node not found", "qualified_name", qn)
			return "", nodeNotFoundErr(qn)
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
		result, err := marshalJSON(data)
		if err != nil {
			return "", trace.Wrap(err, "marshal result")
		}
		return result, nil
	}))
}

// search performs full-text search over indexed graph nodes.
// @intent 키워드와 경로 접두사로 코드 그래프 노드를 효율적으로 탐색하게 한다.
// @param request path가 주어지면 결과를 해당 파일 경로 접두사로 후처리 필터링한다.
// @requires SearchBackend가 구성되어 있어야 한다.
// @ensures 성공 시 최대 limit개의 노드 요약 목록을 반환한다.
// @see mcp.handlers.getNode
func (h *handlers) search(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ctx = h.applyWorkspace(ctx, request)
	log := h.logger()

	query, err := request.RequireString("query")
	if err != nil {
		return missingParamResult(err)
	}
	limit := request.GetInt("limit", 10)
	pathPrefix := request.GetString("path", "")

	log.Info("search called", "query", query, "limit", limit, "path", pathPrefix)

	if h.deps.SearchBackend == nil {
		return mcp.NewToolResultError("SearchBackend not configured"), nil
	}

	return finalizeToolResult(h.cachedExecute("search:", map[string]any{"query": query, "limit": limit, "path": pathPrefix, "workspace": request.GetString("workspace", "")}, func() (string, error) {
		// When path filtering is active, fetch more results from FTS so
		// that after filtering we still have up to 'limit' results.
		fetchLimit := limit
		if pathPrefix != "" {
			fetchLimit = max(limit*5, 50)
		}

		nodes, err := h.deps.SearchBackend.Query(ctx, h.deps.DB, query, fetchLimit)
		if err != nil {
			log.Error("search error", "query", query, trace.SlogError(err))
			return "", trace.Wrap(err, "search error")
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
			searchResult[i] = nodeToBasicMap(n)
		}
		result, err := marshalJSON(searchResult)
		if err != nil {
			return "", trace.Wrap(err, "marshal result")
		}
		return result, nil
	}))
}

// getAnnotation returns stored annotation metadata for a graph node.
// @intent 노드에 연결된 요약과 태그를 조회해 비즈니스 문맥 검색 결과를 보강한다.
// @param request qualified_name은 주석을 조회할 노드의 전체 이름이다.
// @requires 대상 노드와 annotation 레코드가 존재해야 한다.
// @ensures 성공 시 summary, context, tags를 포함한 응답을 반환한다.
// @see mcp.handlers.getNode
func (h *handlers) getAnnotation(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ctx = h.applyWorkspace(ctx, request)
	log := h.logger()

	qn, err := request.RequireString("qualified_name")
	if err != nil {
		return missingParamResult(err)
	}

	log.Info("get_annotation called", "qualified_name", qn)

	return finalizeToolResult(h.cachedExecute("get_annotation:", map[string]any{"qualified_name": qn, "workspace": request.GetString("workspace", "")}, func() (string, error) {
		node, err := h.deps.Store.GetNode(ctx, qn)
		if err != nil {
			log.Error("store error", "tool", "get_annotation", trace.SlogError(err))
			return "", trace.Wrap(err, "store error")
		}
		if node == nil {
			log.Warn("node not found", "qualified_name", qn)
			return "", nodeNotFoundErr(qn)
		}

		ann, err := h.deps.Store.GetAnnotation(ctx, node.ID)
		if err != nil {
			log.Error("annotation error", "node_id", node.ID, trace.SlogError(err))
			return "", trace.Wrap(err, "annotation error")
		}
		if ann == nil {
			log.Warn("annotation not found", "qualified_name", qn)
			return "", newToolResultErr(fmt.Sprintf("no annotation for %q", qn))
		}

		tags := make([]map[string]any, len(ann.Tags))
		for i, tag := range ann.Tags {
			tags[i] = map[string]any{
				"kind":    tag.Kind,
				"type":    tag.Type,
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
		result, err := marshalJSON(data)
		if err != nil {
			return "", trace.Wrap(err, "marshal result")
		}
		return result, nil
	}))
}

// queryGraph runs one of the predefined graph traversal patterns.
// @intent 반복 사용되는 그래프 질의를 패턴 기반 단일 도구로 제공한다.
// @param request pattern은 허용된 질의 종류여야 하고 target은 노드명 또는 파일 경로다.
// @domainRule pattern은 미리 정의된 질의 집합 안에 있어야 한다.
// @requires QueryService가 구성되어 있어야 한다.
// @ensures 성공 시 pattern, target, results를 포함한 응답을 반환한다.
// @see mcp.QueryService
func (h *handlers) queryGraph(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ctx = h.applyWorkspace(ctx, request)
	log := h.logger()

	pattern, err := request.RequireString("pattern")
	if err != nil {
		return missingParamResult(err)
	}
	target, err := request.RequireString("target")
	if err != nil {
		return missingParamResult(err)
	}

	log.Info("query_graph called", "pattern", pattern, "target", target)

	// 패턴 유효성 검사
	validPatterns := map[string]bool{
		"callers_of": true, "callees_of": true, "imports_of": true,
		"importers_of": true, "children_of": true, "tests_for": true,
		"inheritors_of": true, "file_summary": true,
	}
	if !validPatterns[pattern] {
		return mcp.NewToolResultError(fmt.Sprintf("unknown pattern: %q", pattern)), nil
	}

	return finalizeToolResult(h.cachedExecute("query_graph:", map[string]any{"pattern": pattern, "target": target, "workspace": request.GetString("workspace", "")}, func() (string, error) {
		// file_summary는 노드 조회가 불필요
		if pattern == "file_summary" {
			if h.deps.QueryService == nil {
				return "", newToolResultErr("QueryService not configured")
			}
			summary, err := h.deps.QueryService.FileSummaryOf(ctx, target)
			if err != nil {
				return "", newToolResultErr(fmt.Sprintf("file summary error: %v", err))
			}
			fsData := map[string]any{
				"pattern": pattern,
				"target":  target,
				"results": summary,
			}
			result, err := marshalJSON(fsData)
			if err != nil {
				return "", trace.Wrap(err, "marshal result")
			}
			return result, nil
		}

		// 나머지 패턴은 노드를 먼저 조회
		node, err := h.deps.Store.GetNode(ctx, target)
		if err != nil {
			return "", trace.Wrap(err, "store error")
		}
		if node == nil {
			return "", nodeNotFoundErr(target)
		}

		if h.deps.QueryService == nil {
			return "", newToolResultErr("QueryService not configured")
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
			return "", trace.Wrap(err, "query error")
		}

		qgResults := make([]map[string]any, len(nodes))
		for i, n := range nodes {
			qgResults[i] = nodeToBasicMap(n)
		}

		resp := map[string]any{
			"pattern": pattern,
			"target":  target,
			"results": qgResults,
		}
		result, err := marshalJSON(resp)
		if err != nil {
			return "", trace.Wrap(err, "marshal result")
		}
		return result, nil
	}))
}

// listGraphStats returns aggregate node and edge statistics for the graph.
// @intent 현재 그래프 적재 상태를 종류·언어별 분포와 함께 요약한다.
// @ensures 성공 시 총 노드/엣지 수와 kind/language별 집계를 반환한다.
func (h *handlers) listGraphStats(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ctx = h.applyWorkspace(ctx, request)
	log := h.logger()
	log.Info("list_graph_stats called")

	ws := request.GetString("workspace", "")
	return finalizeToolResult(h.cachedExecute("list_graph_stats:", map[string]any{"workspace": ws}, func() (string, error) {
		ns := ctxns.FromContext(ctx)
		nodeQ := h.deps.DB.WithContext(ctx).Model(&model.Node{})
		if ns != "" {
			nodeQ = nodeQ.Where("namespace = ?", ns)
		}

		var nodeCount, edgeCount int64
		if err := nodeQ.Count(&nodeCount).Error; err != nil {
			return "", trace.Wrap(err, "count nodes")
		}
		if err := h.deps.DB.WithContext(ctx).Model(&model.Edge{}).Count(&edgeCount).Error; err != nil {
			return "", trace.Wrap(err, "count edges")
		}

		// kindCount stores grouped count results from aggregate queries.
		// @intent kind 또는 language별 집계 결과를 GORM 스캔 대상으로 사용한다.
		type kindCount struct {
			Kind  string
			Count int64
		}

		nodesByKindQ := h.deps.DB.WithContext(ctx).Model(&model.Node{})
		if ns != "" {
			nodesByKindQ = nodesByKindQ.Where("namespace = ?", ns)
		}
		var nodesByKind []kindCount
		if err := nodesByKindQ.
			Select("kind, COUNT(*) as count").
			Group("kind").Scan(&nodesByKind).Error; err != nil {
			return "", trace.Wrap(err, "group nodes by kind")
		}

		nodesByLangQ := h.deps.DB.WithContext(ctx).Model(&model.Node{})
		if ns != "" {
			nodesByLangQ = nodesByLangQ.Where("namespace = ?", ns)
		}
		var nodesByLang []kindCount
		if err := nodesByLangQ.
			Select("language as kind, COUNT(*) as count").
			Where("language != ''").
			Group("language").Scan(&nodesByLang).Error; err != nil {
			return "", trace.Wrap(err, "group nodes by language")
		}

		var edgesByKind []kindCount
		if err := h.deps.DB.WithContext(ctx).Model(&model.Edge{}).
			Select("kind, COUNT(*) as count").
			Group("kind").Scan(&edgesByKind).Error; err != nil {
			return "", trace.Wrap(err, "group edges by kind")
		}

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
		result, err := marshalJSON(statsData)
		if err != nil {
			return "", trace.Wrap(err, "marshal result")
		}
		return result, nil
	}))
}

// findLargeFunctions returns functions whose line counts exceed a threshold.
// @intent 장문의 함수 후보를 찾아 리팩터링 또는 리뷰 우선순위를 정하게 한다.
// @param request min_lines는 길이 기준이고 path는 파일 경로 접두사 필터다.
// @requires LargefuncAnalyzer가 구성되어 있어야 한다.
// @ensures 성공 시 길이 기준을 넘는 함수 목록과 개수를 반환한다.
// @domainRule 함수 길이는 end_line-start_line+1로 계산한다.
func (h *handlers) findLargeFunctions(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ctx = h.applyWorkspace(ctx, request)
	log := h.logger()

	minLines := request.GetInt("min_lines", 50)
	limit := request.GetInt("limit", 50)
	pathPrefix := request.GetString("path", "")

	log.Info("find_large_functions called", "min_lines", minLines, "limit", limit, "path", pathPrefix)

	if h.deps.LargefuncAnalyzer == nil {
		return mcp.NewToolResultError("LargefuncAnalyzer not configured"), nil
	}

	return finalizeToolResult(h.cachedExecute("find_large_functions:", map[string]any{"min_lines": minLines, "limit": limit, "path": pathPrefix, "workspace": request.GetString("workspace", "")}, func() (string, error) {
		nodes, err := h.deps.LargefuncAnalyzer.Find(ctx, minLines)
		if err != nil {
			return "", trace.Wrap(err, "largefunc error")
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
		result, err := marshalJSON(resp)
		if err != nil {
			return "", trace.Wrap(err, "marshal result")
		}
		return result, nil
	}))
}
