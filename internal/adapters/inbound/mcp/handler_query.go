// @index MCP handlers for node lookup, search, predefined graph queries, and graph statistics.
package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/tae2089/code-context-graph/internal/app/analyze"
	querypkg "github.com/tae2089/code-context-graph/internal/app/analyze/query"
	searchrank "github.com/tae2089/code-context-graph/internal/app/search/rank"
	requestctx "github.com/tae2089/code-context-graph/internal/ctx"
	"github.com/tae2089/code-context-graph/internal/domain/graph"
	"github.com/tae2089/code-context-graph/internal/domain/reference"
	"github.com/tae2089/code-context-graph/internal/pathspec"
	"github.com/tae2089/trace"
)

const (
	defaultQueryGraphLimit = 50
	maxQueryGraphLimit     = 500
)

// annotationTagItem serializes one stored annotation tag.
// @intent expose annotation tags with typed fields for getAnnotation callers.
type annotationTagItem struct {
	Kind    graph.TagKind  `json:"kind"`
	Type    string         `json:"type"`
	Name    string         `json:"name"`
	Value   string         `json:"value"`
	Ordinal int            `json:"ordinal"`
	Ref     *reference.Ref `json:"ref,omitempty"`
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
	Kind          graph.NodeKind      `json:"kind"`
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
	Evidence namespaceEvidenceBlock `json:"evidence"`
}

// searchResultItem summarizes one node hit returned by full-text search.
// @intent preserve a stable per-item DTO for search responses.
// @domainRule Namespace is set only in federated (multi-namespace) mode so single-namespace responses stay unchanged.
type searchResultItem struct {
	ID            uint           `json:"id"`
	QualifiedName string         `json:"qualified_name"`
	Kind          graph.NodeKind `json:"kind"`
	Name          string         `json:"name"`
	FilePath      string         `json:"file_path"`
	Namespace     string         `json:"namespace,omitempty"`
}

// federatedNamespaceEntry wraps one namespace's result inside a federated tool response.
// @intent label per-namespace payloads and isolate per-namespace failures in federated reads.
type federatedNamespaceEntry struct {
	Namespace string          `json:"namespace"`
	Response  json.RawMessage `json:"response,omitempty"`
	Error     string          `json:"error,omitempty"`
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
	ID            uint                   `json:"id"`
	QualifiedName string                 `json:"qualified_name"`
	Kind          graph.NodeKind         `json:"kind"`
	Name          string                 `json:"name"`
	FilePath      string                 `json:"file_path"`
	StartLine     int                    `json:"start_line"`
	EndLine       int                    `json:"end_line"`
	Language      string                 `json:"language"`
	Evidence      namespaceEvidenceBlock `json:"evidence"`
}

// listGraphStatsResponse is the serialized payload for graph statistics.
// @intent preserve a stable typed JSON response for graph statistics without changing the wire format.
type listGraphStatsResponse struct {
	TotalNodes      int64                  `json:"total_nodes"`
	TotalEdges      int64                  `json:"total_edges"`
	NodesByKind     map[string]int64       `json:"nodes_by_kind"`
	NodesByLanguage map[string]int64       `json:"nodes_by_language"`
	EdgesByKind     map[string]int64       `json:"edges_by_kind"`
	Evidence        namespaceEvidenceBlock `json:"evidence"`
}

// getNode returns detailed metadata for a graph node by qualified name.
// @intent look up a node by qualified name so callers can retrieve its core identity and location metadata.
// @param request qualified_name is the fully qualified node name to resolve.
// @requires the target node must exist in the graph store.
// @ensures returns node metadata as JSON when lookup succeeds.
// @see mcp.handlers.getAnnotation
func (h *handlers) getNode(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ctx = h.applyNamespace(ctx, request)
	log := h.logger()

	qn, err := request.RequireString("qualified_name")
	if err != nil {
		return missingParamResult(err)
	}

	log.Info("get_node called", "qualified_name", qn)

	return finalizeToolResult(h.cachedExecute(ctx, "get_node:", map[string]any{"qualified_name": qn, "namespace": requestNamespace(request)}, func() (string, error) {
		node, err := h.deps.Graph.Store.GetNode(ctx, qn)
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
			Evidence:      h.namespaceEvidenceFromContext(ctx),
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
	ctx = h.applyNamespace(ctx, request)
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

	if h.deps.Graph.Search == nil {
		return mcp.NewToolResultError("SearchBackend not configured"), nil
	}

	if namespaces := requestNamespaces(request); len(namespaces) > 0 {
		return h.searchFederated(ctx, query, limit, pathPrefix, namespaces)
	}

	return finalizeToolResult(h.cachedExecute(ctx, "search:", map[string]any{"query": query, "limit": limit, "path": pathPrefix, "namespace": requestNamespace(request)}, func() (string, error) {
		// Over-fetch a wider candidate pool so structural reranking can promote
		// good matches that FTS ranked below the caller's limit, and so path
		// filtering still leaves up to 'limit' results.
		nodes, err := h.deps.Graph.Search.Query(ctx, query, searchrank.FetchLimit(limit))
		if err != nil {
			log.Error("search error", "query", query, trace.SlogError(err))
			return "", trace.Wrap(err, "search error")
		}

		if pathPrefix != "" {
			filtered := nodes[:0]
			for _, n := range nodes {
				if pathspec.HasPathPrefix(n.FilePath, pathPrefix) {
					filtered = append(filtered, n)
				}
			}
			nodes = filtered
		}

		// Rerank FTS candidates with structural signals, then bound to limit.
		nodes = searchrank.Rerank(query, nodes, limit)

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

// searchFederated fans full-text search out over an explicit namespace set and merges reranked hits.
// @intent answer one search across several repositories with per-item namespace labels.
// @domainRule each namespace is queried in isolation; reranking happens once over the merged candidate pool.
func (h *handlers) searchFederated(ctx context.Context, query string, limit int, pathPrefix string, namespaces []string) (*mcp.CallToolResult, error) {
	log := h.logger()
	return finalizeToolResult(h.cachedExecute(ctx, "search:", map[string]any{"query": query, "limit": limit, "path": pathPrefix, "namespaces": namespaces}, func() (string, error) {
		var merged []graph.Node
		for _, ns := range namespaces {
			nsCtx := requestctx.WithNamespace(ctx, ns)
			nodes, err := h.deps.Graph.Search.Query(nsCtx, query, searchrank.FetchLimit(limit))
			if err != nil {
				log.Error("federated search error", "query", query, "namespace", ns, trace.SlogError(err))
				return "", trace.Wrap(err, "federated search error")
			}
			for i := range nodes {
				nodes[i].Namespace = ns
			}
			merged = append(merged, nodes...)
		}

		if pathPrefix != "" {
			filtered := merged[:0]
			for _, n := range merged {
				if pathspec.HasPathPrefix(n.FilePath, pathPrefix) {
					filtered = append(filtered, n)
				}
			}
			merged = filtered
		}

		merged = searchrank.Rerank(query, merged, limit)
		log.Info("federated search completed", "query", query, "namespaces", namespaces, "result_count", len(merged))

		items := make([]searchResultItem, len(merged))
		for i, n := range merged {
			items[i] = searchResultItem{ID: n.ID, QualifiedName: n.QualifiedName, Kind: n.Kind, Name: n.Name, FilePath: n.FilePath, Namespace: n.Namespace}
		}
		result, err := marshalJSON(items)
		if err != nil {
			return "", trace.Wrap(err, "marshal result")
		}
		return result, nil
	}))
}

// getAnnotation returns stored annotation metadata for a graph node.
// @intent fetch stored annotation tags and summary data so semantic search results can show business context.
// @param request qualified_name is the fully qualified node name whose annotations should be loaded.
// @requires the target node and its annotation record must exist.
// @ensures returns a response containing summary, context, and tags when lookup succeeds.
// @see mcp.handlers.getNode
func (h *handlers) getAnnotation(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ctx = h.applyNamespace(ctx, request)
	log := h.logger()

	qn, err := request.RequireString("qualified_name")
	if err != nil {
		return missingParamResult(err)
	}

	log.Info("get_annotation called", "qualified_name", qn)

	return finalizeToolResult(h.cachedExecute(ctx, "get_annotation:", map[string]any{"qualified_name": qn, "namespace": requestNamespace(request)}, func() (string, error) {
		node, err := h.deps.Graph.Store.GetNode(ctx, qn)
		if err != nil {
			log.Error("store error", "tool", "get_annotation", trace.SlogError(err))
			return "", trace.Wrap(err, "store error")
		}
		if node == nil {
			log.Warn("node not found", "qualified_name", qn)
			return "", nodeNotFoundErr(qn)
		}

		ann, err := h.deps.Graph.Store.GetAnnotation(ctx, node.ID)
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
			if tag.Kind == graph.TagSee && reference.Is(tag.Value) {
				if ref, err := reference.Parse(tag.Value); err == nil {
					tags[i].Ref = ref
				}
			}
		}

		data := annotationResponse{Summary: ann.Summary, Context: ann.Context, Tags: tags}
		result, err := marshalJSON(data)
		if err != nil {
			return "", trace.Wrap(err, "marshal result")
		}
		return result, nil
	}))
}

var strictFalse = false

// queryGraph runs one of the predefined graph traversal patterns.
// @intent expose repeated graph traversals through one pattern-driven tool entry point.
// @param request pattern must be one of the allowlisted query kinds and target is a node name or file path.
// @domainRule pattern must belong to the predefined query set.
// @requires QueryService must be configured.
// @ensures returns a response containing pattern, target, and results when the query succeeds.
// @see mcp.QueryService
func (h *handlers) queryGraph(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ctx = h.applyNamespace(ctx, request)
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

	if namespaces := requestNamespaces(request); len(namespaces) > 0 {
		return h.queryGraphFederated(ctx, pattern, target, limit, offset, includeFallbackCalls, namespaces)
	}

	return finalizeToolResult(h.cachedExecute(ctx, "query_graph:", map[string]any{
		"pattern":                pattern,
		"target":                 target,
		"limit":                  limit,
		"offset":                 offset,
		"include_fallback_calls": includeFallbackCalls,
		"namespace":              requestNamespace(request),
	}, func() (string, error) {
		return h.queryGraphInNamespace(ctx, pattern, target, limit, offset, includeFallbackCalls)
	}))
}

// queryGraphFederatedResponse is the typed wire payload for federated query_graph calls.
// @intent group per-namespace traversal outcomes under one envelope with per-namespace errors.
type queryGraphFederatedResponse struct {
	Pattern    string                    `json:"pattern"`
	Target     string                    `json:"target"`
	Namespaces []federatedNamespaceEntry `json:"namespaces"`
}

// queryGraphFederated runs one predefined query across several namespaces and groups the outcomes.
// @intent keep federated traversal per-namespace so a missing target in one namespace never fails the rest.
func (h *handlers) queryGraphFederated(ctx context.Context, pattern, target string, limit, offset int, includeFallbackCalls bool, namespaces []string) (*mcp.CallToolResult, error) {
	return finalizeToolResult(h.cachedExecute(ctx, "query_graph:", map[string]any{
		"pattern":                pattern,
		"target":                 target,
		"limit":                  limit,
		"offset":                 offset,
		"include_fallback_calls": includeFallbackCalls,
		"namespaces":             namespaces,
	}, func() (string, error) {
		entries := make([]federatedNamespaceEntry, 0, len(namespaces))
		for _, ns := range namespaces {
			nsCtx := requestctx.WithNamespace(ctx, ns)
			payload, err := h.queryGraphInNamespace(nsCtx, pattern, target, limit, offset, includeFallbackCalls)
			if err != nil {
				var resultErr *toolResultErr
				if errors.As(err, &resultErr) {
					entries = append(entries, federatedNamespaceEntry{Namespace: ns, Error: err.Error()})
					continue
				}
				return "", trace.Wrap(err, "federated query error")
			}
			entries = append(entries, federatedNamespaceEntry{Namespace: ns, Response: json.RawMessage(payload)})
		}
		return marshalJSON(queryGraphFederatedResponse{Pattern: pattern, Target: target, Namespaces: entries})
	}))
}

// queryGraphInNamespace runs one predefined graph query inside the context namespace.
// @intent share one traversal implementation between single-namespace and federated query_graph calls.
func (h *handlers) queryGraphInNamespace(ctx context.Context, pattern, target string, limit, offset int, includeFallbackCalls bool) (string, error) {
	// file_summary does not require node lookup.
	if pattern == "file_summary" {
		if h.deps.Graph.Query == nil {
			return "", newToolResultErr("QueryService not configured")
		}
		summary, err := h.deps.Graph.Query.FileSummaryOf(ctx, target)
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
	node, err := h.deps.Graph.Store.GetNode(ctx, target)
	if err != nil {
		return "", trace.Wrap(err, "store error")
	}
	if node == nil {
		if h.deps.Graph.Query == nil {
			return "", nodeNotFoundErr(target)
		}
		matches, err := h.deps.Graph.Query.FindExactNameMatches(ctx, target, 10)
		if err != nil {
			return "", trace.Wrap(err, "query target fallback")
		}
		switch len(matches) {
		case 0:
			return "", nodeNotFoundErr(target)
		case 1:
			node, err = h.deps.Graph.Store.GetNode(ctx, matches[0].QualifiedName)
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

	if h.deps.Graph.Query == nil {
		return "", newToolResultErr("QueryService not configured")
	}

	queryOpts := querypkg.QueryOptions{
		IncludeFallbackCalls: &includeFallbackCalls,
		Limit:                limit,
		Offset:               offset,
	}

	var nodes []graph.Node
	var totalCount int
	var page querypkg.PagedNodes
	switch pattern {
	case "callers_of":
		page, err = h.deps.Graph.Query.CallersOfPage(ctx, node.ID, queryOpts)
		nodes = page.Nodes
		totalCount = page.TotalCount
	case "callees_of":
		page, err = h.deps.Graph.Query.CalleesOfPage(ctx, node.ID, queryOpts)
		nodes = page.Nodes
		totalCount = page.TotalCount
	case "imports_of":
		page, err = h.deps.Graph.Query.ImportsOfPage(ctx, node.ID, queryOpts)
		nodes = page.Nodes
		totalCount = page.TotalCount
	case "importers_of":
		page, err = h.deps.Graph.Query.ImportersOfPage(ctx, node.ID, queryOpts)
		nodes = page.Nodes
		totalCount = page.TotalCount
	case "children_of":
		page, err = h.deps.Graph.Query.ChildrenOfPage(ctx, node.ID, queryOpts)
		nodes = page.Nodes
		totalCount = page.TotalCount
	case "tests_for":
		page, err = h.deps.Graph.Query.TestsForPage(ctx, node.ID, queryOpts)
		nodes = page.Nodes
		totalCount = page.TotalCount
	case "inheritors_of":
		page, err = h.deps.Graph.Query.InheritorsOfPage(ctx, node.ID, queryOpts)
		nodes = page.Nodes
		totalCount = page.TotalCount
	}

	if err != nil {
		return "", trace.Wrap(err, "query error")
	}

	neighborEdgeByNodeID := map[uint]graph.Edge{}
	var strictPage querypkg.PagedNodes
	if pattern == "callers_of" || pattern == "callees_of" {
		if includeFallbackCalls {
			strictOpts := querypkg.QueryOptions{IncludeFallbackCalls: &strictFalse, Limit: 1, Offset: 0}
			switch pattern {
			case "callers_of":
				strictPage, err = h.deps.Graph.Query.CallersOfPage(ctx, node.ID, strictOpts)
			case "callees_of":
				strictPage, err = h.deps.Graph.Query.CalleesOfPage(ctx, node.ID, strictOpts)
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
			if hasEvidence && edge.Kind == graph.EdgeKindCalls {
				item.Confidence = "strict"
				item.EdgeKind = string(graph.EdgeKindCalls)
			} else {
				item.Confidence = "tentative"
				item.EdgeKind = string(graph.EdgeKindFallbackCalls)
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
	result, err := marshalJSON(queryGraphResponse{Pattern: pattern, Target: target, Results: qgResults, Metadata: metadata, Evidence: h.namespaceEvidenceFromContext(ctx)})
	if err != nil {
		return "", trace.Wrap(err, "marshal result")
	}
	return result, nil
}

// callQueryPatternEdges loads only edge evidence for current page nodes.
// @intent limit evidence lookup to the response page to avoid scanning full graph.
func (h *handlers) callQueryPatternEdges(ctx context.Context, anchorID uint, pattern string, page []graph.Node) (map[uint]graph.Edge, error) {
	if len(page) == 0 {
		return map[uint]graph.Edge{}, nil
	}
	if h.deps.Graph.Reader == nil {
		return map[uint]graph.Edge{}, nil
	}

	peerIDs := make([]uint, 0, len(page))
	for _, n := range page {
		if n.ID != 0 {
			peerIDs = append(peerIDs, n.ID)
		}
	}
	if len(peerIDs) == 0 {
		return map[uint]graph.Edge{}, nil
	}

	direction := analyze.EdgeDirectionOutgoing
	switch pattern {
	case "callers_of":
		direction = analyze.EdgeDirectionIncoming
	case "callees_of":
	default:
		return map[uint]graph.Edge{}, nil
	}
	return h.deps.Graph.Reader.CallEdges(ctx, anchorID, peerIDs, direction)
}

// listGraphStats returns aggregate node and edge statistics for the graph.
// @intent summarize the current graph load state with kind and language distributions.
// @ensures returns total node and edge counts plus kind and language aggregates when the query succeeds.
func (h *handlers) listGraphStats(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ctx = h.applyNamespace(ctx, request)
	log := h.logger()
	log.Info("list_graph_stats called")

	if namespaces := requestNamespaces(request); len(namespaces) > 0 {
		return h.listGraphStatsFederated(ctx, namespaces)
	}

	return finalizeToolResult(h.cachedExecute(ctx, "list_graph_stats:", map[string]any{"namespace": requestNamespace(request)}, func() (string, error) {
		statsData, err := h.graphStatsInNamespace(ctx)
		if err != nil {
			return "", err
		}
		result, err := marshalJSON(statsData)
		if err != nil {
			return "", trace.Wrap(err, "marshal result")
		}
		return result, nil
	}))
}

// graphStatsInNamespace loads the statistics payload for the context namespace.
// @intent share one statistics assembly between single-namespace and federated calls.
func (h *handlers) graphStatsInNamespace(ctx context.Context) (listGraphStatsResponse, error) {
	stats, err := h.deps.Graph.Statistics.GraphStatistics(ctx)
	if err != nil {
		return listGraphStatsResponse{}, err
	}
	return listGraphStatsResponse{
		TotalNodes:      stats.NodeCount,
		TotalEdges:      stats.EdgeCount,
		NodesByKind:     stats.NodesByKind,
		NodesByLanguage: stats.NodesByLanguage,
		EdgesByKind:     stats.EdgesByKind,
		Evidence:        h.namespaceEvidenceFromContext(ctx),
	}, nil
}

// federatedGraphStatsEntry labels one namespace's statistics inside a federated response.
// @intent keep per-namespace statistics separable instead of summing unrelated graphs.
type federatedGraphStatsEntry struct {
	Namespace string `json:"namespace"`
	listGraphStatsResponse
}

// listGraphStatsFederated returns statistics grouped per namespace.
// @intent give one call visibility over several repositories without merging their counts.
func (h *handlers) listGraphStatsFederated(ctx context.Context, namespaces []string) (*mcp.CallToolResult, error) {
	return finalizeToolResult(h.cachedExecute(ctx, "list_graph_stats:", map[string]any{"namespaces": namespaces}, func() (string, error) {
		entries := make([]federatedGraphStatsEntry, 0, len(namespaces))
		for _, ns := range namespaces {
			nsCtx := requestctx.WithNamespace(ctx, ns)
			statsData, err := h.graphStatsInNamespace(nsCtx)
			if err != nil {
				return "", trace.Wrap(err, "federated stats error")
			}
			entries = append(entries, federatedGraphStatsEntry{Namespace: ns, listGraphStatsResponse: statsData})
		}
		return marshalJSON(map[string]any{"namespaces": entries})
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

// compactQueryTargetAmbiguity formats ambiguous query_graph matches into one compact error string.
// @intent compress ambiguous short-symbol matches into one line so callers can choose the intended node.
func compactQueryTargetAmbiguity(target string, matches []querypkg.CandidateMatch) string {
	parts := make([]string, 0, len(matches))
	for _, match := range matches {
		parts = append(parts, fmt.Sprintf("%s (%s, %s:%d)", match.QualifiedName, match.Kind, match.FilePath, match.StartLine))
	}
	return fmt.Sprintf("query_graph target %q is ambiguous: %s", target, strings.Join(parts, "; "))
}
