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
