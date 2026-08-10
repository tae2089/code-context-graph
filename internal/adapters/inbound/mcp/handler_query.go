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
	searchapp "github.com/tae2089/code-context-graph/internal/app/search"
	"github.com/tae2089/code-context-graph/internal/app/search/evidence"
	requestctx "github.com/tae2089/code-context-graph/internal/ctx"
	"github.com/tae2089/code-context-graph/internal/domain/graph"
	"github.com/tae2089/code-context-graph/internal/domain/reference"
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
	StartLine     int            `json:"start_line"`
	EndLine       int            `json:"end_line"`
	// Intent is the node's own @intent tag, empty when nobody wrote one.
	Intent string `json:"intent,omitempty"`
	// Matched names the signals this query touched — name, path, intent — so a
	// caller can tell an exact identifier hit from a hit on a written purpose.
	Matched []evidence.Match `json:"matched"`
	// Reason is the recorded reason the intent index answered with — set only
	// when this hit came from the intent query, so its absence means the hit
	// earned its place on name, path, or token overlap alone.
	Reason string `json:"reason,omitempty"`
	// MatchedTerms are the query terms written in Reason — the proof behind an
	// intent match that token overlap alone cannot see.
	MatchedTerms []string `json:"matched_terms,omitempty"`
	Namespace    string   `json:"namespace,omitempty"`
}

// searchFileGroup is one file and every hit it answered the query with.
//
// The file is the unit of a search answer because it is the unit of reading: a
// caller picks a file first and a declaration inside it second. Grouping is also
// what let the old per-file cap go — ten hits in one file cost the reader one
// decision, not ten — so a file that appears here appears whole.
//
// @intent let a caller choose between files, then read inside the one it chose.
type searchFileGroup struct {
	FilePath  string             `json:"file_path"`
	Namespace string             `json:"namespace,omitempty"`
	HitCount  int                `json:"hit_count"`
	Hits      []searchResultItem `json:"hits"`
}

// searchResponse wraps the file list so an empty answer can still say why.
//
// It replaced a bare JSON array. An array has nowhere to put the reason a list
// came back short, and a caller that reads `[]` cannot tell "nothing was
// indexed under those words" from "everything found was unjustifiable".
//
// @intent make a search answer self-describing, including when it is empty.
type searchResponse struct {
	Files        []searchFileGroup `json:"files"`
	FileCount    int               `json:"file_count"`
	WeakFiltered int               `json:"weak_filtered"`
	// Truncated is true when this answer did not reach every file the query
	// answered with. It is never about hits: a shown file is shown whole.
	Truncated bool         `json:"truncated"`
	Limits    searchLimits `json:"limits"`
	Next      []nextAction `json:"next,omitempty"`
	Note      string       `json:"note,omitempty"`
}

// searchLimits states the bounds that shaped this page, all counted in files
// except the budget, which only decides whether one more file joins.
// @intent let a caller tell a short answer from the first page of a long one.
type searchLimits struct {
	Files     int `json:"files"`
	Offset    int `json:"offset"`
	HitBudget int `json:"hit_budget"`
}

// nextAction is one call that widens this answer, written out so an agent can
// make it verbatim.
//
// Before this existed a response could say it withheld things and leave the
// caller with no way to reach them. Naming the tool and its arguments turns a
// dead-end count into a step.
//
// @intent turn what a search withheld into a call the caller can actually make.
type nextAction struct {
	Reason string         `json:"reason"`
	Tool   string         `json:"tool"`
	Args   map[string]any `json:"args"`
}

// newSearchResponse converts an evidence list into the wire payload.
// @requires withNamespace is true only for federated searches, where a caller needs to know which repository a hit came from.
// @requires query, limit and offset are the ones this list was built with, so the suggested calls repeat the caller's own request.
// @ensures Next is empty exactly when the answer is both complete and non-empty.
// @intent keep one conversion so single-namespace and federated search cannot drift apart.
func newSearchResponse(list evidence.List, query string, limit, offset int, withNamespace bool) searchResponse {
	files := make([]searchFileGroup, len(list.Files))
	for i, f := range list.Files {
		hits := make([]searchResultItem, len(f.Hits))
		for j, r := range f.Hits {
			n := r.Node
			hits[j] = searchResultItem{
				ID: n.ID, QualifiedName: n.QualifiedName, Kind: n.Kind, Name: n.Name,
				FilePath: n.FilePath, StartLine: n.StartLine, EndLine: n.EndLine,
				Intent: r.Intent, Matched: r.Matched,
				Reason: r.Reason, MatchedTerms: r.MatchedTerms,
			}
			if withNamespace {
				hits[j].Namespace = n.Namespace
			}
		}
		files[i] = searchFileGroup{FilePath: f.FilePath, HitCount: f.HitCount(), Hits: hits}
		if withNamespace {
			files[i].Namespace = f.Namespace
		}
	}
	return searchResponse{
		Files:        files,
		FileCount:    len(files),
		WeakFiltered: list.WeakFiltered,
		Truncated:    list.OverflowFiles > 0,
		Limits:       searchLimits{Files: limit, Offset: offset, HitBudget: evidence.PageHitBudget},
		Next:         nextActions(list, query, limit, offset),
		Note:         list.Note,
	}
}

// nextActions writes one call per thing this answer withheld, plus the one
// call that helps when the answer withheld nothing because it found nothing.
// @ensures every returned action names a tool this server registers, with arguments that need no editing.
// @intent make the follow-up call obvious enough that an agent does not have to invent one.
func nextActions(list evidence.List, query string, limit, offset int) []nextAction {
	actions := make([]nextAction, 0, 3)
	// Full-text search needs every term inside one document, so a question
	// written as a sentence usually matches nothing here. find_by_intent takes
	// that shape of input on purpose and scores it against recorded reasons
	// rather than against the identifier text that just failed, so it is the
	// only hand-off that can answer where this one could not.
	if len(list.Files) == 0 {
		actions = append(actions, nextAction{
			Reason: "no indexed node contained every term; find_by_intent reads a sentence as a question and matches it against recorded @intent/@domainRule instead of names",
			Tool:   "find_by_intent",
			Args:   map[string]any{"question": query},
		})
	}
	if list.OverflowFiles > 0 {
		actions = append(actions, nextAction{
			Reason: fmt.Sprintf("%d more files answered this query and are not on this page", list.OverflowFiles),
			Tool:   "search",
			Args:   map[string]any{"query": query, "limit": limit, "offset": offset + len(list.Files)},
		})
	}
	if list.WeakFiltered > 0 {
		actions = append(actions, nextAction{
			Reason: fmt.Sprintf("%d candidates had nothing in their name, path, or @intent to justify them", list.WeakFiltered),
			Tool:   "search",
			Args:   map[string]any{"query": query, "include_weak": true},
		})
	}
	return actions
}

// federatedNamespaceEntry wraps one namespace's result inside a federated tool response.
// @intent label per-namespace payloads and isolate per-namespace failures in federated reads.
type federatedNamespaceEntry struct {
	Namespace string          `json:"namespace"`
	Response  json.RawMessage `json:"response,omitempty"`
	Error     string          `json:"error,omitempty"`
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
	offset := request.GetInt("offset", 0)
	pathPrefix := request.GetString("path", "")
	includeWeak := request.GetBool("include_weak", false)
	if err := validateQueryGraphLimit(limit); err != nil {
		return finalizeToolResult("", err)
	}
	if offset < 0 {
		return finalizeToolResult("", trace.New("offset must not be negative"))
	}

	log.Info("search called", "query", query, "limit", limit, "offset", offset, "path", pathPrefix)

	if h.deps.Graph.Search == nil {
		return mcp.NewToolResultError("SearchBackend not configured"), nil
	}

	if namespaces := requestNamespaces(request); len(namespaces) > 0 {
		return h.searchFederated(ctx, query, limit, offset, pathPrefix, includeWeak, namespaces)
	}

	return finalizeToolResult(h.cachedExecute(ctx, "search:", map[string]any{"query": query, "limit": limit, "offset": offset, "path": pathPrefix, "include_weak": includeWeak, "namespace": requestNamespace(request)}, func() (string, error) {
		list, err := searchapp.New(h.deps.Graph.Search).Search(ctx, searchapp.Params{
			Query: query, Limit: limit, Offset: offset, PathPrefix: pathPrefix, IncludeWeak: includeWeak,
		})
		if err != nil {
			log.Error("search error", "query", query, trace.SlogError(err))
			return "", trace.Wrap(err, "search error")
		}

		log.Info("search completed", "query", query, "file_count", len(list.Files), "weak_filtered", list.WeakFiltered)

		result, err := marshalJSON(newSearchResponse(list, query, limit, offset, false))
		if err != nil {
			return "", trace.Wrap(err, "marshal result")
		}
		return result, nil
	}))
}

// searchFederated fans full-text search out over an explicit namespace set and merges reranked hits.
// @intent answer one search across several repositories with per-item namespace labels.
// @domainRule each namespace is queried in isolation; every namespace's hits keep their own backend rank when fused.
func (h *handlers) searchFederated(ctx context.Context, query string, limit, offset int, pathPrefix string, includeWeak bool, namespaces []string) (*mcp.CallToolResult, error) {
	log := h.logger()
	return finalizeToolResult(h.cachedExecute(ctx, "search:", map[string]any{"query": query, "limit": limit, "offset": offset, "path": pathPrefix, "include_weak": includeWeak, "namespaces": namespaces}, func() (string, error) {
		list, err := searchapp.New(h.deps.Graph.Search).SearchFederated(ctx, namespaces, searchapp.Params{
			Query: query, Limit: limit, Offset: offset, PathPrefix: pathPrefix, IncludeWeak: includeWeak,
		})
		if err != nil {
			log.Error("federated search error", "query", query, "namespaces", namespaces, trace.SlogError(err))
			return "", trace.Wrap(err, "federated search error")
		}
		log.Info("federated search completed", "query", query, "namespaces", namespaces, "file_count", len(list.Files), "weak_filtered", list.WeakFiltered)

		result, err := marshalJSON(newSearchResponse(list, query, limit, offset, true))
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
		"importers_of": true, "tests_for": true, "inheritors_of": true,
	}
	if !validPatterns[pattern] {
		if replacement, moved := describeReplacedPatterns[pattern]; moved {
			return mcp.NewToolResultError(fmt.Sprintf("%q was replaced by the describe tool: %s", pattern, replacement)), nil
		}
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
	// Every remaining pattern walks an edge, so each resolves the target node first.
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
