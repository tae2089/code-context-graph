package flows

import (
	"context"

	"github.com/tae2089/code-context-graph/internal/ctxns"
	"github.com/tae2089/code-context-graph/internal/model"
	"gorm.io/gorm"
)

// Config controls persisted flow rebuild behavior.
// @intent stored flow rebuild 설정 확장 지점을 제공한다.
type Config struct{}

// Stats summarizes one rebuilt stored flow.
// @intent 재생성된 stored flow의 크기를 후처리 결과로 반환한다.
type Stats struct {
	Flow      model.Flow
	NodeCount int
}

// Builder rebuilds persisted flows from graph data.
// @intent entrypoint별 traced flow를 flows 테이블에 다시 저장한다.
type Builder struct {
	db    *gorm.DB
	store EdgeReader
}

// NewBuilder creates a persisted flow builder.
// @intent DB와 graph reader를 묶어 stored flow rebuild 서비스를 만든다.
func NewBuilder(db *gorm.DB, store EdgeReader) *Builder {
	return &Builder{db: db, store: store}
}

// Rebuild recreates persisted flows for the current namespace.
// @intent namespace 범위 stored flows를 전량 교체해 list_flows를 최신화한다.
// @sideEffect 현재 namespace의 flow/flow_membership 레코드를 삭제 후 재생성한다.
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

func findEntrypoints(tx *gorm.DB, ns string) ([]model.Node, error) {
	var nodes []model.Node
	inboundCalls := tx.Model(&model.Edge{}).
		Select("to_node_id").
		Where("namespace = ? AND kind = ?", ns, model.EdgeKindCalls)
	if err := tx.Where("namespace = ? AND kind IN ?", ns, []model.NodeKind{model.NodeKindFunction, model.NodeKindTest}).
		Where("id NOT IN (?)", inboundCalls).
		Order("id asc").
		Find(&nodes).Error; err != nil {
		return nil, err
	}
	return nodes, nil
}
