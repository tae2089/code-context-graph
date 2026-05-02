package service

import (
	"context"

	"gorm.io/gorm"

	"github.com/tae2089/trace"

	"github.com/tae2089/code-context-graph/internal/ctxns"
	"github.com/tae2089/code-context-graph/internal/model"
	"github.com/tae2089/code-context-graph/internal/store"
	storesearch "github.com/tae2089/code-context-graph/internal/store/search"
)

// NamespacePurger는 단일 namespace의 모든 영속 상태를 일관되게 제거하는 단일 진입점이다.
// @intent gormstore.DeleteGraph(노드/엣지/어노테이션/멤버십/검색문서)와 그동안 MCP
// handler 안에 흩어져 있던 Community/Flow/orphan-membership/검색 백엔드 정리를
// 한 곳으로 모아 namespace-scoped 테이블 추가 시의 drift 위험을 줄인다.
// @sideEffect graph store DeleteGraph 호출 후 별도 트랜잭션으로 service 레벨 테이블과
// 검색 백엔드를 정리한다. 단계 실패 시 service-level 트랜잭션은 rollback 된다.
type NamespacePurger struct {
	store   store.NodeWriter
	db      *gorm.DB
	backend storesearch.Backend
}

// NewNamespacePurger는 NamespacePurger를 생성한다.
// @param graphStore namespace 단위 graph 정리를 수행할 저장소.
// @param db Community/Flow 등 service-level 테이블을 정리할 DB. nil이면 해당 단계는 skip 한다.
// @param backend FTS 등 검색 백엔드. nil이면 해당 단계는 skip 한다.
func NewNamespacePurger(graphStore store.NodeWriter, db *gorm.DB, backend storesearch.Backend) *NamespacePurger {
	return &NamespacePurger{store: graphStore, db: db, backend: backend}
}

// Purge는 ctx의 namespace에 속한 모든 그래프/서비스/검색 상태를 제거한다.
// @intent workspace 삭제와 같은 namespace 단위 purge 경로의 단일 진입점을 제공한다.
// @requires ctx에 ctxns.WithNamespace로 namespace가 설정되어 있어야 한다.
// @sideEffect 노드, 엣지, 어노테이션, 멤버십, 검색 문서, 커뮤니티, 플로우, 검색 인덱스를 삭제한다.
func (p *NamespacePurger) Purge(ctx context.Context) error {
	if p.store != nil {
		if err := p.store.DeleteGraph(ctx); err != nil {
			return trace.Wrap(err, "delete namespace graph")
		}
	}
	if p.db == nil {
		return nil
	}

	ns := ctxns.FromContext(ctx)
	return p.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		mig := tx.Migrator()
		if mig.HasTable(&model.Community{}) {
			communityIDs := tx.Model(&model.Community{}).Select("id").Where("namespace = ?", ns)
			if mig.HasTable(&model.CommunityMembership{}) {
				if err := tx.Where("community_id IN (?)", communityIDs).Delete(&model.CommunityMembership{}).Error; err != nil {
					return trace.Wrap(err, "purge namespace community memberships")
				}
			}
			if err := tx.Where("namespace = ?", ns).Delete(&model.Community{}).Error; err != nil {
				return trace.Wrap(err, "purge namespace communities")
			}
		}
		if mig.HasTable(&model.Flow{}) {
			flowIDs := tx.Model(&model.Flow{}).Select("id").Where("namespace = ?", ns)
			if mig.HasTable(&model.FlowMembership{}) {
				if err := tx.Where("flow_id IN (?) OR namespace = ?", flowIDs, ns).Delete(&model.FlowMembership{}).Error; err != nil {
					return trace.Wrap(err, "purge namespace flow memberships")
				}
			}
			if err := tx.Where("namespace = ?", ns).Delete(&model.Flow{}).Error; err != nil {
				return trace.Wrap(err, "purge namespace flows")
			}
		}
		if p.backend != nil {
			if err := p.backend.PurgeNamespace(ctxns.WithNamespace(ctx, ns), tx); err != nil {
				return trace.Wrap(err, "purge namespace search index")
			}
		}
		return nil
	})
}
