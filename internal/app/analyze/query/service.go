// @index Application graph-query policy for callers, imports, inheritance, summaries, and exact-name fallback.
package query

import (
	"context"
	"sort"
	"strings"

	analyzeapp "github.com/tae2089/code-context-graph/internal/app/analyze"
	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

// FileSummary aggregates node counts for one file.
// @intent summarize the kinds of graph nodes stored for a source file
type FileSummary struct {
	FilePath  string
	Functions int
	Classes   int
	Types     int
	Tests     int
	Total     int
}

// Service serves predefined graph relationship queries.
// @intent provide reusable higher-level graph lookups for MCP queries
type Service struct {
	repository analyzeapp.QueryRepository
}

// @intent carry paginated graph query rows together with the total match count for MCP responses.
type PagedNodes struct {
	Nodes      []graph.Node
	TotalCount int
}

// CandidateMatch describes one exact short-name fallback candidate for query_graph.
// @intent provide compact, stable target suggestions when a short symbol name matches multiple nodes.
type CandidateMatch struct {
	QualifiedName string
	Kind          graph.NodeKind
	FilePath      string
	StartLine     int
}

// QueryOptions controls how predefined relationship lookups treat lower-confidence edges.
// @intent let callers choose between compatibility mode and strict call-edge analysis.
type QueryOptions struct {
	IncludeFallbackCalls *bool
	Limit                int
	Offset               int
}

// New creates a predefined query service.
// @intent construct a service for common graph traversal queries
func New(repository analyzeapp.QueryRepository) *Service {
	return &Service{repository: repository}
}

// defaultQueryOptions normalizes zero-value options into the compatibility-preserving defaults.
// @intent keep legacy callers fallback-inclusive unless they explicitly opt into strict mode.
func defaultQueryOptions(opts QueryOptions) QueryOptions {
	if opts.IncludeFallbackCalls != nil {
		return opts
	}
	includeFallbackCalls := true
	opts.IncludeFallbackCalls = &includeFallbackCalls
	return opts
}

// nodesByEdge loads nodes connected by an edge kind and direction.
// @intent centralize directional edge-query logic shared by predefined graph queries
// @param nodeID anchor node for the relationship lookup
// @param kind edge kind to follow
// @param direction incoming selects source nodes, otherwise destination nodes
// @return nodes connected to the anchor node by the requested relationship
func (s *Service) nodesByEdge(ctx context.Context, nodeID uint, kind graph.EdgeKind, direction string) ([]graph.Node, error) {
	includeFallbackCalls := true
	return s.nodesByEdgeWithOptions(ctx, nodeID, kind, direction, QueryOptions{IncludeFallbackCalls: &includeFallbackCalls})
}

// nodesByEdgeWithOptions loads nodes connected by an edge kind and direction with explicit fallback-call control.
// @intent let strict graph queries exclude fallback call edges without changing legacy defaults.
func (s *Service) nodesByEdgeWithOptions(ctx context.Context, nodeID uint, kind graph.EdgeKind, direction string, opts QueryOptions) ([]graph.Node, error) {
	pageResult, err := s.nodesByEdgePageWithOptions(ctx, nodeID, kind, direction, opts)
	if err != nil {
		return nil, err
	}
	return pageResult.Nodes, nil
}

// nodesByEdgePageWithOptions loads nodes connected by an edge kind and direction with optional pagination.
// @intent provide paginated graph query results without changing legacy return shape for non-paged callers.
func (s *Service) nodesByEdgePageWithOptions(ctx context.Context, nodeID uint, kind graph.EdgeKind, direction string, opts QueryOptions) (PagedNodes, error) {
	edgeKinds := []graph.EdgeKind{kind}
	if kind == graph.EdgeKindCalls {
		edgeKinds = []graph.EdgeKind{graph.EdgeKindCalls}
		normalized := defaultQueryOptions(opts)
		if normalized.IncludeFallbackCalls != nil && *normalized.IncludeFallbackCalls {
			edgeKinds = graph.CallEdgeKinds()
		}
	}
	normalized := defaultQueryOptions(opts)
	if normalized.Offset < 0 {
		normalized.Offset = 0
	}
	page, err := s.repository.RelatedNodes(ctx, analyzeapp.RelatedNodesRequest{
		NodeID:    nodeID,
		EdgeKinds: edgeKinds,
		Direction: analyzeapp.EdgeDirection(direction),
		Limit:     normalized.Limit,
		Offset:    normalized.Offset,
	})
	if err != nil {
		return PagedNodes{}, err
	}
	return PagedNodes{
		Nodes:      normalizeResults(page.Nodes),
		TotalCount: page.TotalCount,
	}, nil
}

// normalizeResults deduplicates and sorts graph query results.
// @intent keep predefined query responses stable across joins that may return duplicate nodes.
func normalizeResults(nodes []graph.Node) []graph.Node {
	if len(nodes) <= 1 {
		return nodes
	}
	seen := make(map[uint]struct{}, len(nodes))
	result := make([]graph.Node, 0, len(nodes))
	for _, node := range nodes {
		if _, ok := seen[node.ID]; ok {
			continue
		}
		seen[node.ID] = struct{}{}
		result = append(result, node)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].FilePath != result[j].FilePath {
			return result[i].FilePath < result[j].FilePath
		}
		if result[i].StartLine != result[j].StartLine {
			return result[i].StartLine < result[j].StartLine
		}
		return result[i].QualifiedName < result[j].QualifiedName
	})
	return result
}

// CallersOf returns nodes that call the target node.
// @intent find upstream callers of a function or method node
// @see query.Service.CalleesOf
func (s *Service) CallersOf(ctx context.Context, nodeID uint) ([]graph.Node, error) {
	return s.nodesByEdge(ctx, nodeID, graph.EdgeKindCalls, "incoming")
}

// CallersOfPage returns callers with pagination metadata.
// @intent support paginated query_graph response pagination and cache metadata.
func (s *Service) CallersOfPage(ctx context.Context, nodeID uint, opts QueryOptions) (PagedNodes, error) {
	return s.nodesByEdgePageWithOptions(ctx, nodeID, graph.EdgeKindCalls, "incoming", opts)
}

// CallersOfWithOptions returns nodes that call the target node with explicit fallback-call control.
// @intent support strict caller lookups that ignore fallback-derived edges when requested.
func (s *Service) CallersOfWithOptions(ctx context.Context, nodeID uint, opts QueryOptions) ([]graph.Node, error) {
	return s.nodesByEdgeWithOptions(ctx, nodeID, graph.EdgeKindCalls, "incoming", opts)
}

// CalleesOf returns nodes called by the target node.
// @intent find downstream call dependencies of a function or method node
// @see query.Service.CallersOf
func (s *Service) CalleesOf(ctx context.Context, nodeID uint) ([]graph.Node, error) {
	return s.nodesByEdge(ctx, nodeID, graph.EdgeKindCalls, "outgoing")
}

// CalleesOfPage returns callees with pagination metadata.
// @intent support paginated query_graph response pagination and cache metadata.
func (s *Service) CalleesOfPage(ctx context.Context, nodeID uint, opts QueryOptions) (PagedNodes, error) {
	return s.nodesByEdgePageWithOptions(ctx, nodeID, graph.EdgeKindCalls, "outgoing", opts)
}

// CalleesOfWithOptions returns nodes called by the target node with explicit fallback-call control.
// @intent support strict callee lookups that ignore fallback-derived edges when requested.
func (s *Service) CalleesOfWithOptions(ctx context.Context, nodeID uint, opts QueryOptions) ([]graph.Node, error) {
	return s.nodesByEdgeWithOptions(ctx, nodeID, graph.EdgeKindCalls, "outgoing", opts)
}

// ImportsOf returns nodes imported by the target node.
// @intent reveal outgoing import dependencies for a file or package node
func (s *Service) ImportsOf(ctx context.Context, nodeID uint) ([]graph.Node, error) {
	return s.nodesByEdge(ctx, nodeID, graph.EdgeKindImportsFrom, "outgoing")
}

// ImportsOfPage returns imported nodes with pagination metadata.
// @intent support paginated query_graph response pagination and cache metadata.
func (s *Service) ImportsOfPage(ctx context.Context, nodeID uint, opts QueryOptions) (PagedNodes, error) {
	return s.nodesByEdgePageWithOptions(ctx, nodeID, graph.EdgeKindImportsFrom, "outgoing", opts)
}

// ImportersOf returns nodes that import the target node.
// @intent reveal reverse import dependencies pointing at the target node
func (s *Service) ImportersOf(ctx context.Context, nodeID uint) ([]graph.Node, error) {
	return s.nodesByEdge(ctx, nodeID, graph.EdgeKindImportsFrom, "incoming")
}

// ImportersOfPage returns importing nodes with pagination metadata.
// @intent support paginated query_graph response pagination and cache metadata.
func (s *Service) ImportersOfPage(ctx context.Context, nodeID uint, opts QueryOptions) (PagedNodes, error) {
	return s.nodesByEdgePageWithOptions(ctx, nodeID, graph.EdgeKindImportsFrom, "incoming", opts)
}

// ChildrenOf returns nodes contained by the target node.
// @intent enumerate structural children contained within a file or type node
func (s *Service) ChildrenOf(ctx context.Context, nodeID uint) ([]graph.Node, error) {
	return s.nodesByEdge(ctx, nodeID, graph.EdgeKindContains, "outgoing")
}

// ChildrenOfPage returns child nodes with pagination metadata.
// @intent support paginated query_graph response pagination and cache metadata.
func (s *Service) ChildrenOfPage(ctx context.Context, nodeID uint, opts QueryOptions) (PagedNodes, error) {
	return s.nodesByEdgePageWithOptions(ctx, nodeID, graph.EdgeKindContains, "outgoing", opts)
}

// TestsFor returns tests that exercise the target node.
// @intent find test nodes linked to the target via tested_by edges
func (s *Service) TestsFor(ctx context.Context, nodeID uint) ([]graph.Node, error) {
	return s.nodesByEdge(ctx, nodeID, graph.EdgeKindTestedBy, "incoming")
}

// TestsForPage returns tests with pagination metadata.
// @intent support paginated query_graph response pagination and cache metadata.
func (s *Service) TestsForPage(ctx context.Context, nodeID uint, opts QueryOptions) (PagedNodes, error) {
	return s.nodesByEdgePageWithOptions(ctx, nodeID, graph.EdgeKindTestedBy, "incoming", opts)
}

// InheritorsOf returns nodes inheriting from the target node.
// @intent find derived types that point to the target through inheritance edges
func (s *Service) InheritorsOf(ctx context.Context, nodeID uint) ([]graph.Node, error) {
	return s.nodesByEdge(ctx, nodeID, graph.EdgeKindInherits, "incoming")
}

// InheritorsOfPage returns inheritors with pagination metadata.
// @intent support paginated query_graph response pagination and cache metadata.
func (s *Service) InheritorsOfPage(ctx context.Context, nodeID uint, opts QueryOptions) (PagedNodes, error) {
	return s.nodesByEdgePageWithOptions(ctx, nodeID, graph.EdgeKindInherits, "incoming", opts)
}

// FileSummaryOf returns node counts grouped by kind for one file.
// @intent summarize how much graph structure exists within a specific file
// @param filePath repository-relative source file path to summarize
// @return per-kind node counts and total node count for the file
func (s *Service) FileSummaryOf(ctx context.Context, filePath string) (*FileSummary, error) {
	nodes, err := s.repository.NodesByFile(ctx, filePath)
	if err != nil {
		return nil, err
	}

	summary := &FileSummary{FilePath: filePath, Total: len(nodes)}
	for _, n := range nodes {
		switch n.Kind {
		case graph.NodeKindFunction:
			summary.Functions++
		case graph.NodeKindClass:
			summary.Classes++
		case graph.NodeKindType:
			summary.Types++
		case graph.NodeKindTest:
			summary.Tests++
		}
	}
	return summary, nil
}

// FindExactNameMatches returns nodes whose short name exactly matches target.
// @intent support MCP fallback from short symbol names to fully qualified graph nodes.
func (s *Service) FindExactNameMatches(ctx context.Context, target string, limit int) ([]CandidateMatch, error) {
	if strings.TrimSpace(target) == "" || limit <= 0 {
		return nil, nil
	}
	nodes, err := s.repository.NodesByExactName(ctx, target, limit)
	if err != nil {
		return nil, err
	}
	nodes = normalizeResults(nodes)
	matches := make([]CandidateMatch, len(nodes))
	for i, node := range nodes {
		matches[i] = CandidateMatch{
			QualifiedName: node.QualifiedName,
			Kind:          node.Kind,
			FilePath:      node.FilePath,
			StartLine:     node.StartLine,
		}
	}
	return matches, nil
}
