package flows

import (
	"context"
	"fmt"

	"github.com/imtaebin/code-context-graph/internal/model"
)

type EdgeReader interface {
	GetEdgesFrom(ctx context.Context, nodeID uint) ([]model.Edge, error)
	GetNodeByID(ctx context.Context, id uint) (*model.Node, error)
}

type Tracer struct {
	store EdgeReader
}

func New(store EdgeReader) *Tracer {
	return &Tracer{store: store}
}

func (t *Tracer) TraceFlow(ctx context.Context, startNodeID uint) (*model.Flow, error) {
	visited := map[uint]bool{}
	var members []model.FlowMembership
	ordinal := 0

	queue := []uint{startNodeID}
	visited[startNodeID] = true

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		members = append(members, model.FlowMembership{
			NodeID:  current,
			Ordinal: ordinal,
		})
		ordinal++

		edges, err := t.store.GetEdgesFrom(ctx, current)
		if err != nil {
			return nil, err
		}
		for _, e := range edges {
			if e.Kind == model.EdgeKindCalls && !visited[e.ToNodeID] {
				visited[e.ToNodeID] = true
				queue = append(queue, e.ToNodeID)
			}
		}
	}

	node, _ := t.store.GetNodeByID(ctx, startNodeID)
	name := fmt.Sprintf("flow_from_%d", startNodeID)
	if node != nil {
		name = fmt.Sprintf("flow_from_%s", node.QualifiedName)
	}

	return &model.Flow{
		Name:    name,
		Members: members,
	}, nil
}
