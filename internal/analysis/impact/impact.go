// @index BFS 기반 blast-radius 분석 엔진. 코드 변경의 영향 범위를 그래프 순회로 계산한다.
package impact

import (
	"context"
	"sort"

	"github.com/tae2089/code-context-graph/internal/model"
)

// EdgeReader exposes graph queries required for impact analysis.
// @intent abstract bidirectional edge and node lookups for blast-radius traversal
type EdgeReader interface {
	GetEdgesFrom(ctx context.Context, nodeID uint) ([]model.Edge, error)
	GetEdgesFromNodes(ctx context.Context, nodeIDs []uint) ([]model.Edge, error)
	GetEdgesTo(ctx context.Context, nodeID uint) ([]model.Edge, error)
	GetEdgesToNodes(ctx context.Context, nodeIDs []uint) ([]model.Edge, error)
	GetNodeByID(ctx context.Context, id uint) (*model.Node, error)
	GetNodesByIDs(ctx context.Context, ids []uint) ([]model.Node, error)
}

// Analyzer computes impact radius over the graph.
// @intent estimate which nodes may be affected by a change
type Analyzer struct {
	store EdgeReader
}

// RadiusOptions caps how far ImpactRadiusBounded is allowed to expand.
// @intent let callers limit BFS depth and visited node count for safety
type RadiusOptions struct {
	MaxDepth int
	MaxNodes int
}

// RadiusResult reports the resolved nodes and whether limits truncated traversal.
// @intent surface the visited node set together with truncation metadata
type RadiusResult struct {
	Nodes         []model.Node
	Truncated     bool
	MaxDepth      int
	MaxNodes      int
	ReturnedNodes int
}

// New creates an impact analyzer.
// @intent construct a blast-radius analyzer around a graph reader
func New(store EdgeReader) *Analyzer {
	return &Analyzer{store: store}
}

// ImpactRadius performs BFS traversal to find all nodes within the given depth.
// Used by MCP get_impact_radius tool and pre-merge check prompt.
//
// @param nodeID the starting node for blast-radius analysis
// @param depth BFS traversal depth limit
// @return all nodes reachable within depth hops
// @intent identify blast radius of code changes for risk assessment
// @domainRule traverses both outgoing and incoming edges bidirectionally
// @see changes.Service.Analyze
func (a *Analyzer) ImpactRadius(ctx context.Context, nodeID uint, depth int) ([]model.Node, error) {
	result, err := a.ImpactRadiusBounded(ctx, nodeID, depth, RadiusOptions{})
	if err != nil {
		return nil, err
	}
	return result.Nodes, nil
}

// ImpactRadiusBounded performs the bidirectional BFS that backs ImpactRadius with explicit caps.
// @intent expose a limit-aware blast radius traversal for cost-sensitive callers
// @param nodeID starting node for the BFS
// @param depth maximum BFS hop count (further capped by opts.MaxDepth)
// @param opts traversal limits applied during BFS
// @return RadiusResult with visited nodes and truncation metadata
// @domainRule traverses outgoing and incoming edges in lock step at each depth
// @ensures Truncated is true when MaxNodes stopped traversal before adding another node
func (a *Analyzer) ImpactRadiusBounded(ctx context.Context, nodeID uint, depth int, opts RadiusOptions) (*RadiusResult, error) {
	if opts.MaxDepth > 0 && depth > opts.MaxDepth {
		depth = opts.MaxDepth
	}
	visited := map[uint]bool{nodeID: true}
	visitOrder := []uint{nodeID}
	frontier := []uint{nodeID}
	truncated := false
	maxNodesReached := func() bool {
		return opts.MaxNodes > 0 && len(visitOrder) >= opts.MaxNodes
	}

	for d := 0; d < depth && len(frontier) > 0; d++ {
		var next []uint

		edgesFrom, err := a.store.GetEdgesFromNodes(ctx, frontier)
		if err != nil {
			return nil, err
		}
		for _, e := range edgesFrom {
			if !visited[e.ToNodeID] {
				if maxNodesReached() {
					truncated = true
					break
				}
				visited[e.ToNodeID] = true
				visitOrder = append(visitOrder, e.ToNodeID)
				next = append(next, e.ToNodeID)
			}
		}
		if truncated {
			break
		}

		edgesTo, err := a.store.GetEdgesToNodes(ctx, frontier)
		if err != nil {
			return nil, err
		}
		for _, e := range edgesTo {
			if !visited[e.FromNodeID] {
				if maxNodesReached() {
					truncated = true
					break
				}
				visited[e.FromNodeID] = true
				visitOrder = append(visitOrder, e.FromNodeID)
				next = append(next, e.FromNodeID)
			}
		}
		if truncated {
			break
		}

		frontier = next
	}

	nodes, err := a.store.GetNodesByIDs(ctx, visitOrder)
	if err != nil {
		return nil, err
	}
	order := make(map[uint]int, len(visitOrder))
	for idx, id := range visitOrder {
		order[id] = idx
	}
	sort.Slice(nodes, func(i, j int) bool {
		return order[nodes[i].ID] < order[nodes[j].ID]
	})

	return &RadiusResult{Nodes: nodes, Truncated: truncated, MaxDepth: opts.MaxDepth, MaxNodes: opts.MaxNodes, ReturnedNodes: len(nodes)}, nil
}
