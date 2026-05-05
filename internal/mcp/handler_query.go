// @index MCP handlers for node lookup, search, predefined graph queries, and graph statistics.
package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/tae2089/trace"
	"gorm.io/gorm"

	"github.com/tae2089/code-context-graph/internal/analysis/largefunc"
	querypkg "github.com/tae2089/code-context-graph/internal/analysis/query"
	"github.com/tae2089/code-context-graph/internal/ctxns"
	"github.com/tae2089/code-context-graph/internal/model"
	"github.com/tae2089/code-context-graph/internal/pathutil"
)

var strictFalse = false

const (
	defaultQueryGraphLimit = 50
	maxQueryGraphLimit     = 500
)

// largeFunctionItem summarizes one oversized function candidate.
// @intent preserve a stable per-item DTO for findLargeFunctions responses.
type largeFunctionItem struct {
	Name  string `json:"name"`
	File  string `json:"file"`
	Lines int    `json:"lines"`
}

// annotationTagItem serializes one stored annotation tag.
// @intent expose annotation tags with typed fields for getAnnotation callers.
type annotationTagItem struct {
	Kind    model.TagKind `json:"kind"`
	Type    string        `json:"type"`
	Name    string        `json:"name"`
	Value   string        `json:"value"`
	Ordinal int           `json:"ordinal"`
}

// annotationResponse is the typed wire payload for getAnnotation.
// @intent preserve a stable response envelope for annotation summary, context, and tags.
type annotationResponse struct {
	Summary string              `json:"summary"`
	Context string              `json:"context"`
	Tags    []annotationTagItem `json:"tags"`
}

// queryGraphEvidence records edge evidence backing one queryGraph result item.
// @intent expose edge location details that justify caller/callee confidence labels.
type queryGraphEvidence struct {
	FilePath    string `json:"file_path"`
	Line        int    `json:"line"`
	Fingerprint string `json:"fingerprint"`
}

// queryGraphResultItem summarizes one node returned by queryGraph.
// @intent preserve a stable DTO for paged graph traversal results.
type queryGraphResultItem struct {
	ID            uint                `json:"id"`
	QualifiedName string              `json:"qualified_name"`
	Kind          model.NodeKind      `json:"kind"`
	Name          string              `json:"name"`
	FilePath      string              `json:"file_path"`
	Confidence    string              `json:"confidence,omitempty"`
	EdgeKind      string              `json:"edge_kind,omitempty"`
	Evidence      *queryGraphEvidence `json:"evidence,omitempty"`
}

// queryGraphMetadata records pagination and fallback-call accounting for queryGraph.
// @intent explain result counts, truncation, and strict-versus-tentative composition in queryGraph responses.
type queryGraphMetadata struct {
	Limit                int   `json:"limit"`
	Offset               int   `json:"offset"`
	ReturnedCount        int   `json:"returned_count"`
	TotalCount           int   `json:"total_count"`
	Truncated            bool  `json:"truncated"`
	NextOffset           *int  `json:"next_offset,omitempty"`
	StrictCount          *int  `json:"strict_count,omitempty"`
	TentativeCount       *int  `json:"tentative_count,omitempty"`
	IncludeFallbackCalls *bool `json:"include_fallback_calls,omitempty"`
}

// queryGraphResponse is the typed wire payload for queryGraph.
// @intent preserve a stable response envelope for predefined graph traversals and their evidence.
type queryGraphResponse struct {
	Pattern  string                 `json:"pattern"`
	Target   string                 `json:"target"`
	Results  []queryGraphResultItem `json:"results"`
	Metadata queryGraphMetadata     `json:"metadata"`
	Evidence workspaceEvidenceBlock `json:"evidence"`
}

// searchResultItem summarizes one node hit returned by full-text search.
// @intent preserve a stable per-item DTO for search responses.
type searchResultItem struct {
	ID            uint           `json:"id"`
	QualifiedName string         `json:"qualified_name"`
	Kind          model.NodeKind `json:"kind"`
	Name          string         `json:"name"`
	FilePath      string         `json:"file_path"`
}

// fileSummaryResponse is the typed wire payload for file_summary queryGraph requests.
// @intent preserve a stable response envelope for file-summary graph queries.
type fileSummaryResponse struct {
	Pattern string                `json:"pattern"`
	Target  string                `json:"target"`
	Results *querypkg.FileSummary `json:"results"`
}

// nodeResponse is the typed wire payload for getNode.
// @intent preserve a stable response envelope for node metadata lookups.
type nodeResponse struct {
	ID            uint           `json:"id"`
	QualifiedName string         `json:"qualified_name"`
	Kind          model.NodeKind `json:"kind"`
	Name          string         `json:"name"`
	FilePath      string         `json:"file_path"`
	StartLine     int            `json:"start_line"`
	EndLine       int            `json:"end_line"`
	Language      string         `json:"language"`
	Evidence      workspaceEvidenceBlock `json:"evidence"`
}

// getNode returns detailed metadata for a graph node by qualified name.
// @intent look up a node by qualified name so callers can retrieve its core identity and location metadata.
// @param request qualified_name is the fully qualified node name to resolve.
// @requires the target node must exist in the graph store.
// @ensures returns node metadata as JSON when lookup succeeds.
// @see mcp.handlers.getAnnotation
func (h *handlers) getNode(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ctx = h.applyWorkspace(ctx, request)
	log := h.logger()

	qn, err := request.RequireString("qualified_name")
	if err != nil {
		return missingParamResult(err)
	}

	log.Info("get_node called", "qualified_name", qn)

	return finalizeToolResult(h.cachedExecute(ctx, "get_node:", map[string]any{"qualified_name": qn, "namespace": requestNamespace(request)}, func() (string, error) {
		node, err := h.deps.Store.GetNode(ctx, qn)
		if err != nil {
			log.Error("store error", "tool", "get_node", trace.SlogError(err))
			return "", trace.Wrap(err, "store error")
		}
		if node == nil {
			log.Warn("node not found", "qualified_name", qn)
			return "", nodeNotFoundErr(qn)
		}

		data := nodeResponse{
			ID:            node.ID,
			QualifiedName: node.QualifiedName,
			Kind:          node.Kind,
			Name:          node.Name,
			FilePath:      node.FilePath,
			StartLine:     node.StartLine,
			EndLine:       node.EndLine,
			Language:      node.Language,
			Evidence:      h.workspaceEvidenceFromContext(ctx),
		}
		result, err := marshalJSON(data)
		if err != nil {
			return "", trace.Wrap(err, "marshal result")
		}
		return result, nil
	}))
}

// search performs full-text search over indexed graph nodes.
// @intent search graph nodes efficiently by keyword and optional path prefix filtering.
// @param request path post-filters results by file path prefix when it is provided.
// @requires SearchBackend must be configured.
// @ensures returns up to limit summarized nodes when search succeeds.
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
	if err := validateQueryGraphLimit(limit); err != nil {
		return finalizeToolResult("", err)
	}

	log.Info("search called", "query", query, "limit", limit, "path", pathPrefix)

	if h.deps.SearchBackend == nil {
		return mcp.NewToolResultError("SearchBackend not configured"), nil
	}

	return finalizeToolResult(h.cachedExecute(ctx, "search:", map[string]any{"query": query, "limit": limit, "path": pathPrefix, "namespace": requestNamespace(request)}, func() (string, error) {
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
				if pathutil.HasPathPrefix(n.FilePath, pathPrefix) {
					filtered = append(filtered, n)
				}
			}
			nodes = filtered
			if len(nodes) > limit {
				nodes = nodes[:limit]
			}
		}

		log.Info("search completed", "query", query, "result_count", len(nodes))

		searchResult := make([]searchResultItem, len(nodes))
		for i, n := range nodes {
			searchResult[i] = searchResultItem{ID: n.ID, QualifiedName: n.QualifiedName, Kind: n.Kind, Name: n.Name, FilePath: n.FilePath}
		}
		result, err := marshalJSON(searchResult)
		if err != nil {
			return "", trace.Wrap(err, "marshal result")
		}
		return result, nil
	}))
}

// validateQueryGraphLimit checks that the limit parameter for queryGraph is within acceptable bounds.
// @intent enforce reasonable limits on queryGraph results to prevent excessive load and encourage pagination.
func validateQueryGraphLimit(limit int) error {
	if err := validatePositiveLimit(limit); err != nil {
		return err
	}
	if limit > maxQueryGraphLimit {
		return newToolResultErr(fmt.Sprintf("limit must be <= %d, got %d", maxQueryGraphLimit, limit))
	}
	return nil
}

// getAnnotation returns stored annotation metadata for a graph node.
// @intent fetch stored annotation tags and summary data so semantic search results can show business context.
// @param request qualified_name is the fully qualified node name whose annotations should be loaded.
// @requires the target node and its annotation record must exist.
// @ensures returns a response containing summary, context, and tags when lookup succeeds.
// @see mcp.handlers.getNode
func (h *handlers) getAnnotation(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ctx = h.applyWorkspace(ctx, request)
	log := h.logger()

	qn, err := request.RequireString("qualified_name")
	if err != nil {
		return missingParamResult(err)
	}

	log.Info("get_annotation called", "qualified_name", qn)

	return finalizeToolResult(h.cachedExecute(ctx, "get_annotation:", map[string]any{"qualified_name": qn, "namespace": requestNamespace(request)}, func() (string, error) {
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

		tags := make([]annotationTagItem, len(ann.Tags))
		for i, tag := range ann.Tags {
			tags[i] = annotationTagItem{Kind: tag.Kind, Type: tag.Type, Name: tag.Name, Value: tag.Value, Ordinal: tag.Ordinal}
		}

		data := annotationResponse{Summary: ann.Summary, Context: ann.Context, Tags: tags}
		result, err := marshalJSON(data)
		if err != nil {
			return "", trace.Wrap(err, "marshal result")
		}
		return result, nil
	}))
}

// queryGraph runs one of the predefined graph traversal patterns.
// @intent expose repeated graph traversals through one pattern-driven tool entry point.
// @param request pattern must be one of the allowlisted query kinds and target is a node name or file path.
// @domainRule pattern must belong to the predefined query set.
// @requires QueryService must be configured.
// @ensures returns a response containing pattern, target, and results when the query succeeds.
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
	limit := request.GetInt("limit", defaultQueryGraphLimit)
	offset := request.GetInt("offset", 0)
	if err := validateQueryGraphLimit(limit); err != nil {
		return finalizeToolResult("", err)
	}
	if offset < 0 {
		return finalizeToolResult("", newToolResultErr(fmt.Sprintf("offset must be >= 0, got %d", offset)))
	}
	includeFallbackCalls := request.GetBool("include_fallback_calls", true)

	log.Info("query_graph called", "pattern", pattern, "target", target, "limit", limit, "offset", offset)

	// Validate pattern against the allowlisted query set.
	validPatterns := map[string]bool{
		"callers_of": true, "callees_of": true, "imports_of": true,
		"importers_of": true, "children_of": true, "tests_for": true,
		"inheritors_of": true, "file_summary": true,
	}
	if !validPatterns[pattern] {
		return mcp.NewToolResultError(fmt.Sprintf("unknown pattern: %q", pattern)), nil
	}

	return finalizeToolResult(h.cachedExecute(ctx, "query_graph:", map[string]any{
		"pattern":                pattern,
		"target":                 target,
		"limit":                  limit,
		"offset":                 offset,
		"include_fallback_calls": includeFallbackCalls,
		"namespace":              requestNamespace(request),
	}, func() (string, error) {
		// file_summary does not require node lookup.
		if pattern == "file_summary" {
			if h.deps.QueryService == nil {
				return "", newToolResultErr("QueryService not configured")
			}
			summary, err := h.deps.QueryService.FileSummaryOf(ctx, target)
			if err != nil {
				return "", newToolResultErr(fmt.Sprintf("file summary error: %v", err))
			}
			result, err := marshalJSON(fileSummaryResponse{Pattern: pattern, Target: target, Results: summary})
			if err != nil {
				return "", trace.Wrap(err, "marshal result")
			}
			return result, nil
		}

		// The remaining patterns resolve the target node first.
		node, err := h.deps.Store.GetNode(ctx, target)
		if err != nil {
			return "", trace.Wrap(err, "store error")
		}
		if node == nil {
			if h.deps.QueryService == nil {
				return "", nodeNotFoundErr(target)
			}
			matches, err := h.deps.QueryService.FindExactNameMatches(ctx, target, 10)
			if err != nil {
				return "", trace.Wrap(err, "query target fallback")
			}
			switch len(matches) {
			case 0:
				return "", nodeNotFoundErr(target)
			case 1:
				node, err = h.deps.Store.GetNode(ctx, matches[0].QualifiedName)
				if err != nil {
					return "", trace.Wrap(err, "store fallback lookup")
				}
				if node == nil {
					return "", nodeNotFoundErr(matches[0].QualifiedName)
				}
			default:
				return "", newToolResultErr(compactQueryTargetAmbiguity(target, matches))
			}
		}

		if h.deps.QueryService == nil {
			return "", newToolResultErr("QueryService not configured")
		}

		queryOpts := querypkg.QueryOptions{
			IncludeFallbackCalls: &includeFallbackCalls,
			Limit:                limit,
			Offset:               offset,
		}

		var nodes []model.Node
		var totalCount int
		var page querypkg.PagedNodes
		switch pattern {
		case "callers_of":
			page, err = h.deps.QueryService.CallersOfPage(ctx, node.ID, queryOpts)
			nodes = page.Nodes
			totalCount = page.TotalCount
		case "callees_of":
			page, err = h.deps.QueryService.CalleesOfPage(ctx, node.ID, queryOpts)
			nodes = page.Nodes
			totalCount = page.TotalCount
		case "imports_of":
			page, err = h.deps.QueryService.ImportsOfPage(ctx, node.ID, queryOpts)
			nodes = page.Nodes
			totalCount = page.TotalCount
		case "importers_of":
			page, err = h.deps.QueryService.ImportersOfPage(ctx, node.ID, queryOpts)
			nodes = page.Nodes
			totalCount = page.TotalCount
		case "children_of":
			page, err = h.deps.QueryService.ChildrenOfPage(ctx, node.ID, queryOpts)
			nodes = page.Nodes
			totalCount = page.TotalCount
		case "tests_for":
			page, err = h.deps.QueryService.TestsForPage(ctx, node.ID, queryOpts)
			nodes = page.Nodes
			totalCount = page.TotalCount
		case "inheritors_of":
			page, err = h.deps.QueryService.InheritorsOfPage(ctx, node.ID, queryOpts)
			nodes = page.Nodes
			totalCount = page.TotalCount
		}

		if err != nil {
			return "", trace.Wrap(err, "query error")
		}

		neighborEdgeByNodeID := map[uint]model.Edge{}
		var strictPage querypkg.PagedNodes
		if pattern == "callers_of" || pattern == "callees_of" {
			if includeFallbackCalls {
				strictOpts := querypkg.QueryOptions{IncludeFallbackCalls: &strictFalse, Limit: 1, Offset: 0}
				switch pattern {
				case "callers_of":
					strictPage, err = h.deps.QueryService.CallersOfPage(ctx, node.ID, strictOpts)
				case "callees_of":
					strictPage, err = h.deps.QueryService.CalleesOfPage(ctx, node.ID, strictOpts)
				}
				if err != nil {
					return "", trace.Wrap(err, "strict query error")
				}
			}
			// Only augment edge evidence for nodes on the current response page.
			neighborEdgeByNodeID, err = h.callQueryPatternEdges(ctx, node.ID, pattern, nodes)
			if err != nil {
				return "", trace.Wrap(err, "query evidence edges")
			}
		}

		strictTotal := 0
		if pattern == "callers_of" || pattern == "callees_of" {
			if includeFallbackCalls {
				strictTotal = strictPage.TotalCount
			} else {
				strictTotal = totalCount
			}
		}
		truncated := false
		nextOffset := 0
		if offset+len(nodes) < totalCount {
			truncated = true
			nextOffset = offset + len(nodes)
		}

		qgResults := make([]queryGraphResultItem, len(nodes))
		for i, n := range nodes {
			item := queryGraphResultItem{ID: n.ID, QualifiedName: n.QualifiedName, Kind: n.Kind, Name: n.Name, FilePath: n.FilePath}
			if pattern == "callers_of" || pattern == "callees_of" {
				edge, hasEvidence := neighborEdgeByNodeID[n.ID]
				if hasEvidence && edge.Kind == model.EdgeKindCalls {
					item.Confidence = "strict"
					item.EdgeKind = string(model.EdgeKindCalls)
				} else {
					item.Confidence = "tentative"
					item.EdgeKind = string(model.EdgeKindFallbackCalls)
				}
				if hasEvidence {
					item.Evidence = &queryGraphEvidence{FilePath: edge.FilePath, Line: edge.Line, Fingerprint: edge.Fingerprint}
				}
			}
			qgResults[i] = item
		}

		metadata := queryGraphMetadata{Limit: limit, Offset: offset, ReturnedCount: len(qgResults), TotalCount: totalCount, Truncated: truncated}
		if truncated {
			metadata.NextOffset = &nextOffset
		}
		if pattern == "callers_of" || pattern == "callees_of" {
			tentativeCount := totalCount - strictTotal
			metadata.StrictCount = &strictTotal
			metadata.TentativeCount = &tentativeCount
			metadata.IncludeFallbackCalls = &includeFallbackCalls
		}
		result, err := marshalJSON(queryGraphResponse{Pattern: pattern, Target: target, Results: qgResults, Metadata: metadata, Evidence: h.workspaceEvidenceFromContext(ctx)})
		if err != nil {
			return "", trace.Wrap(err, "marshal result")
		}
		return result, nil
	}))
}

// callQueryPatternEdges loads only edge evidence for current page nodes.
// @intent limit evidence lookup to the response page to avoid scanning full graph.
func (h *handlers) callQueryPatternEdges(ctx context.Context, anchorID uint, pattern string, page []model.Node) (map[uint]model.Edge, error) {
	if len(page) == 0 {
		return map[uint]model.Edge{}, nil
	}
	if h.deps.DB == nil {
		return map[uint]model.Edge{}, nil
	}

	peerIDs := make([]uint, 0, len(page))
	for _, n := range page {
		if n.ID != 0 {
			peerIDs = append(peerIDs, n.ID)
		}
	}
	if len(peerIDs) == 0 {
		return map[uint]model.Edge{}, nil
	}

	var edges []model.Edge
	var q *gorm.DB
	switch pattern {
	case "callers_of":
		q = h.deps.DB.WithContext(ctx).
			Model(&model.Edge{}).
			Where("namespace = ? AND kind IN ? AND from_node_id IN ? AND to_node_id = ?", ctxns.FromContext(ctx), model.CallEdgeKinds(), peerIDs, anchorID)
	case "callees_of":
		q = h.deps.DB.WithContext(ctx).
			Model(&model.Edge{}).
			Where("namespace = ? AND kind IN ? AND to_node_id IN ? AND from_node_id = ?", ctxns.FromContext(ctx), model.CallEdgeKinds(), peerIDs, anchorID)
	default:
		return map[uint]model.Edge{}, nil
	}

	if err := q.Find(&edges).Error; err != nil {
		return nil, err
	}

	peerToEdge := make(map[uint]model.Edge, len(peerIDs))
	for _, edge := range edges {
		var peerID uint
		if pattern == "callers_of" {
			peerID = edge.FromNodeID
		} else {
			peerID = edge.ToNodeID
		}
		if existing, ok := peerToEdge[peerID]; ok {
			if existing.Kind == model.EdgeKindFallbackCalls && edge.Kind == model.EdgeKindCalls {
				peerToEdge[peerID] = edge
			}
			continue
		}
		peerToEdge[peerID] = edge
	}
	return peerToEdge, nil
}

// compactQueryTargetAmbiguity formats ambiguous query_graph matches into one compact error string.
// @intent compress ambiguous short-symbol matches into one line so callers can choose the intended node.
func compactQueryTargetAmbiguity(target string, matches []querypkg.CandidateMatch) string {
	parts := make([]string, 0, len(matches))
	for _, match := range matches {
		parts = append(parts, fmt.Sprintf("%s (%s, %s:%d)", match.QualifiedName, match.Kind, match.FilePath, match.StartLine))
	}
	return fmt.Sprintf("query_graph target %q is ambiguous: %s", target, strings.Join(parts, "; "))
}

// listGraphStatsResponse is the serialized payload for graph statistics.
// @intent preserve a stable typed JSON response for graph statistics without changing the wire format.
type listGraphStatsResponse struct {
	TotalNodes      int64            `json:"total_nodes"`
	TotalEdges      int64            `json:"total_edges"`
	NodesByKind     map[string]int64 `json:"nodes_by_kind"`
	NodesByLanguage map[string]int64 `json:"nodes_by_language"`
	EdgesByKind     map[string]int64 `json:"edges_by_kind"`
	Evidence        workspaceEvidenceBlock `json:"evidence"`
}

// listGraphStats returns aggregate node and edge statistics for the graph.
// @intent summarize the current graph load state with kind and language distributions.
// @ensures returns total node and edge counts plus kind and language aggregates when the query succeeds.
func (h *handlers) listGraphStats(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ctx = h.applyWorkspace(ctx, request)
	log := h.logger()
	log.Info("list_graph_stats called")

	return finalizeToolResult(h.cachedExecute(ctx, "list_graph_stats:", map[string]any{"namespace": requestNamespace(request)}, func() (string, error) {
		ns := ctxns.FromContext(ctx)
		nodeQ := h.deps.DB.WithContext(ctx).Model(&model.Node{}).Where("namespace = ?", ns)

		var nodeCount, edgeCount int64
		if err := nodeQ.Count(&nodeCount).Error; err != nil {
			return "", trace.Wrap(err, "count nodes")
		}
		edgeQ := h.deps.DB.WithContext(ctx).Model(&model.Edge{}).Where("namespace = ?", ns)
		if err := edgeQ.Count(&edgeCount).Error; err != nil {
			return "", trace.Wrap(err, "count edges")
		}

		// kindCount stores grouped count results from aggregate queries.
		// @intent use one scan target type for grouped kind and language aggregate rows.
		type kindCount struct {
			Kind  string
			Count int64
		}

		nodesByKindQ := h.deps.DB.WithContext(ctx).Model(&model.Node{}).Where("namespace = ?", ns)
		var nodesByKind []kindCount
		if err := nodesByKindQ.
			Select("kind, COUNT(*) as count").
			Group("kind").Scan(&nodesByKind).Error; err != nil {
			return "", trace.Wrap(err, "group nodes by kind")
		}

		nodesByLangQ := h.deps.DB.WithContext(ctx).Model(&model.Node{}).Where("namespace = ?", ns)
		var nodesByLang []kindCount
		if err := nodesByLangQ.
			Select("language as kind, COUNT(*) as count").
			Where("language != ''").
			Group("language").Scan(&nodesByLang).Error; err != nil {
			return "", trace.Wrap(err, "group nodes by language")
		}

		edgesByKindQ := h.deps.DB.WithContext(ctx).Model(&model.Edge{}).Where("namespace = ?", ns)
		var edgesByKind []kindCount
		if err := edgesByKindQ.
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

		statsData := listGraphStatsResponse{
			TotalNodes:      nodeCount,
			TotalEdges:      edgeCount,
			NodesByKind:     nbk,
			NodesByLanguage: nbl,
			EdgesByKind:     ebk,
			Evidence:        h.workspaceEvidenceFromContext(ctx),
		}
		result, err := marshalJSON(statsData)
		if err != nil {
			return "", trace.Wrap(err, "marshal result")
		}
		return result, nil
	}))
}

// findLargeFunctions returns functions whose line counts exceed a threshold.
// @intent find oversized functions so maintainers can prioritize refactoring or review attention.
// @param request min_lines is the length threshold and path is an optional file path prefix filter.
// @requires LargefuncAnalyzer must be configured.
// @ensures returns functions exceeding the threshold and their count when analysis succeeds.
// @domainRule function length is calculated as end_line-start_line+1.
func (h *handlers) findLargeFunctions(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ctx = h.applyWorkspace(ctx, request)
	log := h.logger()

	input, err := decodeFindLargeFuncsRequest(request)
	if err != nil {
		return finalizeToolResult("", err)
	}

	log.Info("find_large_functions called", "min_lines", input.MinLines, "limit", input.Page.Limit, "offset", input.Page.Offset, "path", input.PathPrefix)

	if h.deps.LargefuncAnalyzer == nil {
		return mcp.NewToolResultError("LargefuncAnalyzer not configured"), nil
	}

	cacheParams := map[string]any{
		"min_lines": input.MinLines,
		"limit":     input.Page.Limit,
		"offset":    input.Page.Offset,
		"path":      input.PathPrefix,
		"namespace": input.Namespace,
	}
	return finalizeToolResult(h.cachedExecute(ctx, "find_large_functions:", cacheParams, func() (string, error) {
		page, err := h.deps.LargefuncAnalyzer.FindPage(ctx, largefunc.Options{Threshold: input.MinLines, PathPrefix: input.PathPrefix, Page: input.Page})
		if err != nil {
			return "", trace.Wrap(err, "largefunc error")
		}

		items := make([]largeFunctionItem, len(page.Items))
		for i, n := range page.Items {
			items[i] = largeFunctionItem{Name: n.QualifiedName, File: n.FilePath, Lines: n.EndLine - n.StartLine + 1}
		}

		result, err := encodePagedListResponse("results", items, page.Pagination)
		if err != nil {
			return "", trace.Wrap(err, "marshal result")
		}
		return result, nil
	}))
}
