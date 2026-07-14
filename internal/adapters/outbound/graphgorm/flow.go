// @index GORM persistence adapter for atomic stored-flow rebuilds.
package graphgorm

import (
	"context"

	"gorm.io/gorm"

	analyzeapp "github.com/tae2089/code-context-graph/internal/app/analyze"
	requestctx "github.com/tae2089/code-context-graph/internal/ctx"
	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

var _ analyzeapp.FlowRebuildStore = (*Store)(nil)
var _ analyzeapp.FlowUnitOfWork = (*Store)(nil)

// WithinFlowRebuild executes stored-flow replacement against one transaction-scoped store.
// @intent implement the analysis flow unit of work without exposing GORM to application policy.
// @sideEffect starts a transaction and commits or rolls back namespace-scoped flow changes.
func (s *Store) WithinFlowRebuild(ctx context.Context, fn func(analyzeapp.FlowRebuildStore) error) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(New(tx))
	})
}

// DeleteFlows removes all stored flows and memberships in the active namespace.
// @intent clear stale flow state before a transaction-scoped rebuild.
// @sideEffect deletes namespace-owned flow and flow-membership rows.
func (s *Store) DeleteFlows(ctx context.Context) error {
	ns := requestctx.FromContext(ctx)
	var ids []uint
	if err := s.db.WithContext(ctx).Model(&graph.Flow{}).Where("namespace = ?", ns).Pluck("id", &ids).Error; err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}
	if err := s.db.WithContext(ctx).Where("flow_id IN ?", ids).Delete(&graph.FlowMembership{}).Error; err != nil {
		return err
	}
	return s.db.WithContext(ctx).Where("id IN ?", ids).Delete(&graph.Flow{}).Error
}

// FindFlowEntrypoints returns function/test nodes with no inbound call-kind edge.
// @intent provide deterministic namespace-scoped entrypoints for stored-flow rebuild policy.
// @domainRule inbound detection includes every kind returned by graph.CallEdgeKinds.
func (s *Store) FindFlowEntrypoints(ctx context.Context) ([]graph.Node, error) {
	ns := requestctx.FromContext(ctx)
	var nodes []graph.Node
	inboundCalls := s.db.WithContext(ctx).Model(&graph.Edge{}).
		Select("to_node_id").
		Where("namespace = ? AND kind IN ?", ns, graph.CallEdgeKinds())
	if err := s.db.WithContext(ctx).
		Where("namespace = ? AND kind IN ?", ns, []graph.NodeKind{graph.NodeKindFunction, graph.NodeKindTest}).
		Where("id NOT IN (?)", inboundCalls).
		Order("id asc").
		Find(&nodes).Error; err != nil {
		return nil, err
	}
	return nodes, nil
}

// CreateFlow persists one flow and its ordered memberships through the active transaction.
// @intent store traced flow aggregates while keeping generated IDs visible to application results.
// @sideEffect inserts a flow row and zero or more flow-membership rows.
// @mutates flow.ID and each flow.Members item FlowID.
func (s *Store) CreateFlow(ctx context.Context, flow *graph.Flow) error {
	members := append([]graph.FlowMembership(nil), flow.Members...)
	flow.Members = nil
	if err := s.db.WithContext(ctx).Create(flow).Error; err != nil {
		return err
	}
	for i := range members {
		members[i].FlowID = flow.ID
	}
	if len(members) > 0 {
		if err := s.db.WithContext(ctx).Create(&members).Error; err != nil {
			return err
		}
	}
	flow.Members = members
	return nil
}
