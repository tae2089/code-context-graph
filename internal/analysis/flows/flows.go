// @index Flow tracing engine that walks call edges into reusable flow records.
package flows

import (
	"context"
	"fmt"

	"github.com/tae2089/code-context-graph/internal/ctxns"
	"github.com/tae2089/code-context-graph/internal/model"
)

// EdgeReader exposes graph traversal primitives needed for flow tracing.
// @intent abstract graph reads so flow tracing can follow call edges from any store
type EdgeReader interface {
	GetEdgesFrom(ctx context.Context, nodeID uint) ([]model.Edge, error)
	GetNodeByID(ctx context.Context, id uint) (*model.Node, error)
}

// Tracer builds call-chain flows from a starting node.
// @intent produce reusable flow records that describe reachable call paths
type Tracer struct {
	store EdgeReader
}

// TraceOptions bounds how far a single flow trace is allowed to expand.
// @intent let callers cap traversal cost when tracing large call graphs
type TraceOptions struct {
	MaxNodes int
}

// TraceResult wraps a built flow with the metadata needed to detect truncation.
// @intent communicate truncation status alongside the produced flow
type TraceResult struct {
	Flow          *model.Flow
	Truncated     bool
	MaxNodes      int
	ReturnedNodes int
}

// New creates a flow tracer.
// @intent construct a tracer bound to a graph edge reader
func New(store EdgeReader) *Tracer {
	return &Tracer{store: store}
}

// TraceFlow traverses outgoing call edges from a start node.
// @intent capture the reachable call chain from one entry node as a flow
// @param startNodeID graph node where traversal begins
// @return flow containing visited nodes in traversal order
// @domainRule only calls edges expand the traced flow
// @ensures each reachable node is included at most once
func (t *Tracer) TraceFlow(ctx context.Context, startNodeID uint) (*model.Flow, error) {
	result, err := t.TraceFlowBounded(ctx, startNodeID, TraceOptions{})
	if err != nil {
		return nil, err
	}
	return result.Flow, nil
}

// TraceFlowBounded performs the BFS traversal that backs TraceFlow with explicit limits.
// @intent expose a flow trace variant that can stop early when MaxNodes is reached
// @param startNodeID graph node where traversal begins
// @param opts traversal limits applied during BFS
// @return TraceResult with the built flow and truncation metadata
// @domainRule only calls edges enqueue new BFS nodes
// @ensures Truncated is true only when MaxNodes stopped traversal
func (t *Tracer) TraceFlowBounded(ctx context.Context, startNodeID uint, opts TraceOptions) (*TraceResult, error) {
	visited := map[uint]bool{}
	var members []model.FlowMembership
	ordinal := 0
	ns := ctxns.FromContext(ctx)
	truncated := false

	queue := []uint{startNodeID}
	visited[startNodeID] = true

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		members = append(members, model.FlowMembership{
			Namespace: ns,
			NodeID:    current,
			Ordinal:   ordinal,
		})
		ordinal++

		edges, err := t.store.GetEdgesFrom(ctx, current)
		if err != nil {
			return nil, err
		}
		for _, e := range edges {
			if model.IsCallKind(e.Kind) && !visited[e.ToNodeID] {
				if opts.MaxNodes > 0 && len(visited) >= opts.MaxNodes {
					truncated = true
					break
				}
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

	flow := &model.Flow{
		Namespace: ns,
		Name:      name,
		Members:   members,
	}
	return &TraceResult{Flow: flow, Truncated: truncated, MaxNodes: opts.MaxNodes, ReturnedNodes: len(members)}, nil
}
