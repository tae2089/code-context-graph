// @index Persisted flow rebuild service for namespace-scoped stored flows.
package flows

import (
	"context"

	"github.com/tae2089/code-context-graph/internal/ctxns"
	"github.com/tae2089/code-context-graph/internal/model"
	"gorm.io/gorm"
)

// Config controls persisted flow rebuild behavior.
// @intent provides an extension point for stored flow rebuild configuration.
type Config struct{}

// Stats summarizes one rebuilt stored flow.
// @intent returns the size of the rebuilt stored flow as a post-process result.
type Stats struct {
	Flow      model.Flow
	NodeCount int
}

// Builder rebuilds persisted flows from graph data.
// @intent persists traced flows per entrypoint back into the flows table.
type Builder struct {
	db    *gorm.DB
	store EdgeReader
}

// NewBuilder creates a persisted flow builder.
// @intent binds the database and graph reader to create a stored flow rebuild service.
func NewBuilder(db *gorm.DB, store EdgeReader) *Builder {
	return &Builder{db: db, store: store}
}

// Rebuild recreates persisted flows for the current namespace.
// @intent refreshes list_flows by replacing all stored flows within the namespace.
// @param ctx extracts the namespace from ctxns to determine the work scope.
// @param cfg placeholder for future rebuild options; currently ignored.
// @return a list of rebuilt stored flows and the number of member nodes for each.
// @sideEffect deletes and recreates flow/flow_membership records for the current namespace.
// @mutates namespace-scoped rows in the flows and flow_memberships tables.
// @domainRule rebuilds stored flows by running TraceFlow for each entrypoint within the namespace.
// @ensures partial updates are not persisted if the transaction fails.
func (b *Builder) Rebuild(ctx context.Context, cfg Config) ([]Stats, error) {
	_ = cfg
	var result []Stats
	ns := ctxns.FromContext(ctx)
	tracer := New(b.store)

	err := b.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := deleteFlows(tx, ns); err != nil {
			return err
		}

		entrypoints, err := findEntrypoints(tx, ns)
		if err != nil {
			return err
		}

		for _, entry := range entrypoints {
			flow, err := tracer.TraceFlow(ctx, entry.ID)
			if err != nil {
				return err
			}
			members := append([]model.FlowMembership(nil), flow.Members...)
			flow.Members = nil
			if err := tx.Create(flow).Error; err != nil {
				return err
			}
			for i := range members {
				members[i].FlowID = flow.ID
			}
			if len(members) > 0 {
				if err := tx.Create(&members).Error; err != nil {
					return err
				}
			}
			flow.Members = members
			result = append(result, Stats{Flow: *flow, NodeCount: len(members)})
		}

		return nil
	})

	return result, err
}

// deleteFlows removes all flows and memberships within the namespace.
// @intent clear stale flow records before a fresh rebuild
// @sideEffect deletes rows from flow_memberships and flows tables
func deleteFlows(tx *gorm.DB, ns string) error {
	var ids []uint
	if err := tx.Model(&model.Flow{}).Where("namespace = ?", ns).Pluck("id", &ids).Error; err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}
	if err := tx.Where("flow_id IN ?", ids).Delete(&model.FlowMembership{}).Error; err != nil {
		return err
	}
	return tx.Where("id IN ?", ids).Delete(&model.Flow{}).Error
}

// findEntrypoints returns function and test nodes with no inbound calls edges.
// @intent locate likely flow entry points by selecting nodes nothing else calls
// @return a list of entrypoint nodes sorted by ID in ascending order.
// @domainRule considers only function or test nodes with no inbound call edges as entrypoints.
// @domainRule inbound detection includes all call types defined in model.CallEdgeKinds().
func findEntrypoints(tx *gorm.DB, ns string) ([]model.Node, error) {
	var nodes []model.Node
	inboundCalls := tx.Model(&model.Edge{}).
		Select("to_node_id").
		Where("namespace = ? AND kind IN ?", ns, model.CallEdgeKinds())
	if err := tx.Where("namespace = ? AND kind IN ?", ns, []model.NodeKind{model.NodeKindFunction, model.NodeKindTest}).
		Where("id NOT IN (?)", inboundCalls).
		Order("id asc").
		Find(&nodes).Error; err != nil {
		return nil, err
	}
	return nodes, nil
}
