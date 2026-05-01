// @index BFS 기반 blast-radius 분석 엔진. 코드 변경의 영향 범위를 그래프 순회로 계산한다.
package impact

import (
	"context"

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

type RadiusOptions struct {
	MaxDepth int
	MaxNodes int
}

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

func (a *Analyzer) ImpactRadiusBounded(ctx context.Context, nodeID uint, depth int, opts RadiusOptions) (*RadiusResult, error) {
	if opts.MaxDepth > 0 && depth > opts.MaxDepth {
		depth = opts.MaxDepth
	}
	visited := map[uint]bool{nodeID: true}
	frontier := []uint{nodeID}
	truncated := false

	for d := 0; d < depth && len(frontier) > 0; d++ {
		var next []uint

		edgesFrom, err := a.store.GetEdgesFromNodes(ctx, frontier)
		if err != nil {
			return nil, err
		}
		for _, e := range edgesFrom {
			if !visited[e.ToNodeID] {
				visited[e.ToNodeID] = true
				if opts.MaxNodes > 0 && len(visited) > opts.MaxNodes {
					truncated = true
					break
				}
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
				visited[e.FromNodeID] = true
				if opts.MaxNodes > 0 && len(visited) > opts.MaxNodes {
					truncated = true
					break
				}
				next = append(next, e.FromNodeID)
			}
		}
		if truncated {
			break
		}

		frontier = next
	}

	var allVisited []uint
	for id := range visited {
		allVisited = append(allVisited, id)
	}

	nodes, err := a.store.GetNodesByIDs(ctx, allVisited)
	if err != nil {
		return nil, err
	}
	if opts.MaxNodes > 0 && len(nodes) > opts.MaxNodes {
		nodes = nodes[:opts.MaxNodes]
		truncated = true
	}

	return &RadiusResult{Nodes: nodes, Truncated: truncated, MaxDepth: opts.MaxDepth, MaxNodes: opts.MaxNodes, ReturnedNodes: len(nodes)}, nil
}
