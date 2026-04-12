// @index BFS 기반 blast-radius 분석 엔진. 코드 변경의 영향 범위를 그래프 순회로 계산한다.
package impact

import (
	"context"

	"github.com/imtaebin/code-context-graph/internal/model"
)

type EdgeReader interface {
	GetEdgesFrom(ctx context.Context, nodeID uint) ([]model.Edge, error)
	GetEdgesTo(ctx context.Context, nodeID uint) ([]model.Edge, error)
	GetNodeByID(ctx context.Context, id uint) (*model.Node, error)
}

type Analyzer struct {
	store EdgeReader
}

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
	visited := map[uint]bool{nodeID: true}
	frontier := []uint{nodeID}

	for d := 0; d < depth && len(frontier) > 0; d++ {
		var next []uint
		for _, nid := range frontier {
			edges, err := a.store.GetEdgesFrom(ctx, nid)
			if err != nil {
				return nil, err
			}
			for _, e := range edges {
				if !visited[e.ToNodeID] {
					visited[e.ToNodeID] = true
					next = append(next, e.ToNodeID)
				}
			}
			incoming, err := a.store.GetEdgesTo(ctx, nid)
			if err != nil {
				return nil, err
			}
			for _, e := range incoming {
				if !visited[e.FromNodeID] {
					visited[e.FromNodeID] = true
					next = append(next, e.FromNodeID)
				}
			}
		}
		frontier = next
	}

	result := make([]model.Node, 0, len(visited))
	for id := range visited {
		node, err := a.store.GetNodeByID(ctx, id)
		if err != nil {
			return nil, err
		}
		if node != nil {
			result = append(result, *node)
		}
	}
	return result, nil
}
