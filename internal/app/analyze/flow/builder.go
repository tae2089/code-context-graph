// @index Persisted flow rebuild service for namespace-scoped stored flows.
package flow

import (
	"context"
	"fmt"

	analyzeapp "github.com/tae2089/code-context-graph/internal/app/analyze"
	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

// Config controls persisted flow rebuild behavior.
// @intent provides an extension point for stored flow rebuild configuration.
type Config struct{}

// Stats summarizes one rebuilt stored flow.
// @intent returns the size of the rebuilt stored flow as a post-process result.
type Stats struct {
	Flow      graph.Flow
	NodeCount int
}

// Builder rebuilds persisted flows from graph data.
// @intent persists traced flows per entrypoint back into the flows table.
type Builder struct {
	uow analyzeapp.FlowUnitOfWork
}

// NewBuilder creates a persisted flow builder.
// @intent binds the database and graph reader to create a stored flow rebuild service.
func NewBuilder(uow analyzeapp.FlowUnitOfWork) *Builder {
	return &Builder{uow: uow}
}

// Rebuild recreates persisted flows for the current namespace.
// @intent refreshes list_flows by replacing all stored flows within the namespace.
// @param ctx extracts the namespace from the shared request context to determine the work scope.
// @param cfg placeholder for future rebuild options; currently ignored.
// @return a list of rebuilt stored flows and the number of member nodes for each.
// @sideEffect deletes and recreates flow/flow_membership records for the current namespace.
// @mutates namespace-scoped rows in the flows and flow_memberships tables.
// @domainRule rebuilds stored flows by running TraceFlow for each entrypoint within the namespace.
// @ensures partial updates are not persisted if the transaction fails.
func (b *Builder) Rebuild(ctx context.Context, cfg Config) ([]Stats, error) {
	_ = cfg
	var result []Stats
	if b == nil || b.uow == nil {
		return nil, fmt.Errorf("flow rebuild unit of work is not configured")
	}
	err := b.uow.WithinFlowRebuild(ctx, func(store analyzeapp.FlowRebuildStore) error {
		if err := store.DeleteFlows(ctx); err != nil {
			return err
		}

		entrypoints, err := store.FindFlowEntrypoints(ctx)
		if err != nil {
			return err
		}
		tracer := New(store)
		for _, entry := range entrypoints {
			flow, err := tracer.TraceFlow(ctx, entry.ID)
			if err != nil {
				return err
			}
			if err := store.CreateFlow(ctx, flow); err != nil {
				return err
			}
			result = append(result, Stats{Flow: *flow, NodeCount: len(flow.Members)})
		}

		return nil
	})

	return result, err
}
