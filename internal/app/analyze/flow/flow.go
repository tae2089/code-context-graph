// @index Flow tracing engine that walks call edges into reusable flow records.
package flow

import (
	"context"
	"fmt"

	requestctx "github.com/tae2089/code-context-graph/internal/ctx"
	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

// EdgeReader exposes graph traversal primitives needed for flow tracing.
// @intent abstract graph reads so flow tracing can follow call edges from any store
type EdgeReader interface {
	GetEdgesFromNodes(ctx context.Context, nodeIDs []uint) ([]graph.Edge, error)
	GetNodeByID(ctx context.Context, id uint) (*graph.Node, error)
}

// Tracer builds call-chain flows from a starting node.
// @intent produce reusable flow records that describe reachable call paths
type Tracer struct {
	store EdgeReader
}

// TraceOptions bounds how far a single flow trace is allowed to expand.
// @intent let callers cap traversal cost when tracing large call graphs
type TraceOptions struct {
	MaxNodes             int
	IncludeFallbackCalls *bool
}

// TraceResult wraps a built flow with the metadata needed to detect truncation.
// @intent communicate truncation status alongside the produced flow
type TraceResult struct {
	Flow          *graph.Flow
	Truncated     bool
	MaxNodes      int
	ReturnedNodes int
	// ContainsFallbackCalls indicates whether the traced flow used at least one fallback call edge.
	ContainsFallbackCalls bool
	// FallbackEdgesCount counts fallback call edges that participated in flow expansion.
	FallbackEdgesCount int
}

// @intent default flow tracing to include fallback call edges unless a caller explicitly requests strict mode.
func defaultTraceOptions(opts TraceOptions) TraceOptions {
	if opts.IncludeFallbackCalls != nil {
		return opts
	}
	includeFallbackCalls := true
	opts.IncludeFallbackCalls = &includeFallbackCalls
	return opts
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
func (t *Tracer) TraceFlow(ctx context.Context, startNodeID uint) (*graph.Flow, error) {
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
// @domainRule only calls edges enqueue new BFS nodes; outgoing edges are fetched once per BFS depth
// @ensures Truncated is true only when MaxNodes stopped traversal
func (t *Tracer) TraceFlowBounded(ctx context.Context, startNodeID uint, opts TraceOptions) (*TraceResult, error) {
	opts = defaultTraceOptions(opts)
	visited := map[uint]bool{}
	var members []graph.FlowMembership
	ordinal := 0
	ns := requestctx.FromContext(ctx)
	truncated := false
	fallbackEdges := 0
	containsFallbackCalls := false

	frontier := []uint{startNodeID}
	visited[startNodeID] = true

	for len(frontier) > 0 {
		for _, current := range frontier {
			members = append(members, graph.FlowMembership{
				Namespace: ns,
				NodeID:    current,
				Ordinal:   ordinal,
			})
			ordinal++
		}

		edges, err := t.store.GetEdgesFromNodes(ctx, frontier)
		if err != nil {
			return nil, err
		}
		edgesBySource := make(map[uint][]graph.Edge, len(frontier))
		for _, edge := range edges {
			edgesBySource[edge.FromNodeID] = append(edgesBySource[edge.FromNodeID], edge)
		}
		nextFrontier := make([]uint, 0)
		for _, current := range frontier {
			for _, e := range edgesBySource[current] {
				// Cross-ref edges only appear when a cross-namespace reader supplies them;
				// regular stores never return this kind.
				if (!graph.IsCallKind(e.Kind) && e.Kind != graph.EdgeKindCrossRef) || visited[e.ToNodeID] {
					continue
				}
				if e.Kind == graph.EdgeKindFallbackCalls {
					if !*opts.IncludeFallbackCalls {
						continue
					}
					fallbackEdges++
					containsFallbackCalls = true
				}
				if opts.MaxNodes > 0 && len(visited) >= opts.MaxNodes {
					truncated = true
					break
				}
				visited[e.ToNodeID] = true
				nextFrontier = append(nextFrontier, e.ToNodeID)
			}
		}
		frontier = nextFrontier
	}

	node, _ := t.store.GetNodeByID(ctx, startNodeID)
	name := fmt.Sprintf("flow_from_%d", startNodeID)
	if node != nil {
		name = fmt.Sprintf("flow_from_%s", node.QualifiedName)
	}

	flow := &graph.Flow{
		Namespace: ns,
		Name:      name,
		Members:   members,
	}
	return &TraceResult{
		Flow:                  flow,
		Truncated:             truncated,
		MaxNodes:              opts.MaxNodes,
		ReturnedNodes:         len(members),
		ContainsFallbackCalls: containsFallbackCalls,
		FallbackEdgesCount:    fallbackEdges,
	}, nil
}
