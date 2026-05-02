// @index GORM 기반 그래프 저장소. 노드, 엣지, 어노테이션의 CRUD와 트랜잭션을 관리한다.
package gormstore

import (
	"context"
	"errors"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/tae2089/trace"

	"github.com/tae2089/code-context-graph/internal/ctxns"
	"github.com/tae2089/code-context-graph/internal/model"
	"github.com/tae2089/code-context-graph/internal/store"
)

// Store는 GORM 기반 GraphStore 구현체다.
// @intent GORM DB 핸들을 통해 그래프 저장소 계약을 구현한다.
type Store struct {
	db *gorm.DB
}

// New는 GORM DB를 감싼 저장소를 생성한다.
// @intent 주입된 DB 핸들로 GraphStore 구현체를 초기화한다.
func New(db *gorm.DB) *Store {
	return &Store{db: db}
}

// AutoMigrate는 그래프 저장소 스키마를 생성하거나 갱신한다.
// @intent 그래프 저장에 필요한 GORM 모델 테이블을 준비한다.
// @sideEffect 데이터베이스 스키마를 변경할 수 있다.
func (s *Store) AutoMigrate() error {
	if err := s.db.AutoMigrate(
		&model.Node{},
		&model.Edge{},
		&model.Annotation{},
		&model.DocTag{},
		&model.Community{},
		&model.CommunityMembership{},
		&model.Flow{},
		&model.FlowMembership{},
	); err != nil {
		return err
	}
	if s.db.Migrator().HasIndex(&model.Edge{}, "idx_edges_fingerprint") {
		if err := s.db.Migrator().DropIndex(&model.Edge{}, "idx_edges_fingerprint"); err != nil {
			return trace.Wrap(err, "drop legacy edge fingerprint index")
		}
	}
	return nil
}

// UpsertNodes는 노드 배치를 qualified_name 기준으로 저장한다.
// @intent 파싱 결과 노드를 중복 없이 일괄 반영한다.
// @sideEffect nodes 테이블에 배치 insert/update를 수행한다.
// @requires nodes의 QualifiedName은 비어 있지 않아야 한다.
// @ensures 동일 qualified_name을 가진 기존 노드는 최신 메타데이터로 갱신된다.
func (s *Store) UpsertNodes(ctx context.Context, nodes []model.Node) error {
	if len(nodes) == 0 {
		return nil
	}
	ns := ctxns.FromContext(ctx)
	for i := range nodes {
		nodes[i].Namespace = ns
	}
	err := s.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "namespace"}, {Name: "qualified_name"}, {Name: "file_path"}, {Name: "start_line"}},
			DoUpdates: clause.AssignmentColumns([]string{
				"kind", "name", "end_line", "hash", "language",
			}),
		}).
		CreateInBatches(nodes, 100).Error
	if err != nil {
		return trace.Wrap(err, "batch upsert nodes")
	}
	return nil
}

// GetNode는 정규화된 이름으로 노드 하나를 조회한다.
// @intent 선언의 qualified name으로 단일 노드를 찾는다.
// @return 노드가 없으면 nil을 반환한다.
func (s *Store) GetNode(ctx context.Context, qualifiedName string) (*model.Node, error) {
	var node model.Node
	ns := ctxns.FromContext(ctx)
	result := s.db.WithContext(ctx).Where("namespace = ? AND qualified_name = ?", ns, qualifiedName).First(&node)
	if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if result.Error != nil {
		return nil, trace.Wrap(result.Error, "get node by qualified name")
	}
	return &node, nil
}

// GetNodeByID는 기본 키로 노드 하나를 조회한다.
// @intent 내부 식별자로 단일 노드를 찾는다.
// @return 노드가 없으면 nil을 반환한다.
func (s *Store) GetNodeByID(ctx context.Context, id uint) (*model.Node, error) {
	var node model.Node
	ns := ctxns.FromContext(ctx)
	result := s.db.WithContext(ctx).Where("namespace = ? AND id = ?", ns, id).First(&node)
	if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if result.Error != nil {
		return nil, trace.Wrap(result.Error, "get node by id")
	}
	return &node, nil
}

// GetNodesByIDs는 ID 목록에 해당하는 노드를 조회한다.
// @intent 여러 노드를 내부 식별자 기준으로 한 번에 불러온다.
// @return ids가 비어 있으면 nil 슬라이스를 반환한다.
func (s *Store) GetNodesByIDs(ctx context.Context, ids []uint) ([]model.Node, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var nodes []model.Node
	ns := ctxns.FromContext(ctx)
	if err := s.db.WithContext(ctx).Where("namespace = ? AND id IN ?", ns, ids).Find(&nodes).Error; err != nil {
		return nil, err
	}
	return nodes, nil
}

// GetNodesByQualifiedNames는 이름 목록을 노드 맵으로 조회한다.
// @intent qualified name 기반 참조 해석을 위해 빠른 조회 맵을 만든다.
// @return 키는 QualifiedName이며 동일 이름의 모든 노드를 슬라이스로 포함한다.
func (s *Store) GetNodesByQualifiedNames(ctx context.Context, names []string) (map[string][]model.Node, error) {
	if len(names) == 0 {
		return map[string][]model.Node{}, nil
	}
	ns := ctxns.FromContext(ctx)
	var nodes []model.Node
	if err := s.db.WithContext(ctx).Where("namespace = ? AND qualified_name IN ?", ns, names).Find(&nodes).Error; err != nil {
		return nil, err
	}
	result := make(map[string][]model.Node, len(nodes))
	for _, node := range nodes {
		result[node.QualifiedName] = append(result[node.QualifiedName], node)
	}
	return result, nil
}

// GetNodesByFile는 파일 경로에 속한 노드를 조회한다.
// @intent 특정 소스 파일에서 파싱된 선언들을 불러온다.
func (s *Store) GetNodesByFile(ctx context.Context, filePath string) ([]model.Node, error) {
	ns := ctxns.FromContext(ctx)
	var nodes []model.Node
	if err := s.db.WithContext(ctx).Where("namespace = ? AND file_path = ?", ns, filePath).Find(&nodes).Error; err != nil {
		return nil, err
	}
	return nodes, nil
}

// GetNodesByFiles는 여러 파일의 노드를 파일별로 묶어 조회한다.
// @intent 파일 집합의 선언 목록을 경로별 그룹으로 반환한다.
// @return 키는 파일 경로이며 값은 해당 파일의 노드 목록이다.
func (s *Store) GetNodesByFiles(ctx context.Context, filePaths []string) (map[string][]model.Node, error) {
	if len(filePaths) == 0 {
		return map[string][]model.Node{}, nil
	}
	ns := ctxns.FromContext(ctx)
	var nodes []model.Node
	if err := s.db.WithContext(ctx).Where("namespace = ? AND file_path IN ?", ns, filePaths).Find(&nodes).Error; err != nil {
		return nil, err
	}
	result := make(map[string][]model.Node, len(filePaths))
	for _, n := range nodes {
		result[n.FilePath] = append(result[n.FilePath], n)
	}
	return result, nil
}

// DeleteNodesByFile는 파일에 속한 노드와 연관 데이터를 제거한다.
// @intent 파일 재파싱 전 기존 노드, 엣지, 어노테이션 흔적을 정리한다.
// @sideEffect nodes, edges, annotations, doc_tags 테이블에서 관련 레코드를 삭제한다.
// @domainRule 파일 삭제 시 연결된 엣지와 어노테이션도 함께 제거되어야 한다.
func (s *Store) DeleteNodesByFile(ctx context.Context, filePath string) error {
	ns := ctxns.FromContext(ctx)
	var nodeIDs []uint
	if err := s.db.WithContext(ctx).
		Model(&model.Node{}).
		Where("namespace = ? AND file_path = ?", ns, filePath).
		Pluck("id", &nodeIDs).Error; err != nil {
		return trace.Wrap(err, "pluck node ids")
	}
	if len(nodeIDs) == 0 {
		return nil
	}

	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.
			Where("namespace = ? AND file_path = ?", ns, filePath).
			Delete(&model.Edge{}).Error; err != nil {
			return trace.Wrap(err, "delete file-owned edges")
		}

		if err := tx.
			Where("from_node_id IN ? OR to_node_id IN ?", nodeIDs, nodeIDs).
			Delete(&model.Edge{}).Error; err != nil {
			return trace.Wrap(err, "cascade delete edges")
		}

		if err := tx.
			Where("annotation_id IN (?)",
				tx.Model(&model.Annotation{}).Select("id").Where("node_id IN ?", nodeIDs),
			).Delete(&model.DocTag{}).Error; err != nil {
			return trace.Wrap(err, "cascade delete doc_tags")
		}

		if err := tx.
			Where("node_id IN ?", nodeIDs).
			Delete(&model.Annotation{}).Error; err != nil {
			return trace.Wrap(err, "cascade delete annotations")
		}

		if err := tx.
			Where("node_id IN ?", nodeIDs).
			Delete(&model.CommunityMembership{}).Error; err != nil {
			return trace.Wrap(err, "cascade delete community memberships")
		}

		if tx.Migrator().HasTable(&model.FlowMembership{}) {
			if err := tx.
				Where("node_id IN ?", nodeIDs).
				Delete(&model.FlowMembership{}).Error; err != nil {
				return trace.Wrap(err, "cascade delete flow memberships")
			}
		}

		if tx.Migrator().HasTable(&model.SearchDocument{}) {
			if err := tx.
				Where("node_id IN ?", nodeIDs).
				Delete(&model.SearchDocument{}).Error; err != nil {
				return trace.Wrap(err, "cascade delete search_documents")
			}
		}

		return tx.Where("id IN ?", nodeIDs).Delete(&model.Node{}).Error
	})
}

// DeleteGraph는 현재 namespace의 그래프 상태 전체를 제거한다.
// @intent full rebuild 또는 include_paths rebuild 전에 namespace 범위 상태를 교체한다.
// @sideEffect namespace에 속한 nodes, edges, annotations, doc_tags를 모두 삭제한다.
func (s *Store) DeleteGraph(ctx context.Context) error {
	ns := ctxns.FromContext(ctx)
	var nodeIDs []uint
	var filePaths []string
	if err := s.db.WithContext(ctx).
		Model(&model.Node{}).
		Where("namespace = ?", ns).
		Pluck("id", &nodeIDs).Error; err != nil {
		return trace.Wrap(err, "pluck namespace node ids")
	}
	if err := s.db.WithContext(ctx).
		Model(&model.Node{}).
		Where("namespace = ?", ns).
		Distinct().
		Pluck("file_path", &filePaths).Error; err != nil {
		return trace.Wrap(err, "pluck namespace file paths")
	}

	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if len(filePaths) > 0 {
			if err := tx.
				Where("namespace = ? AND file_path IN ?", ns, filePaths).
				Delete(&model.Edge{}).Error; err != nil {
				return trace.Wrap(err, "delete namespace file-owned edges")
			}
		}

		if len(nodeIDs) > 0 {
			if err := tx.
				Where("annotation_id IN (?)",
					tx.Model(&model.Annotation{}).Select("id").Where("node_id IN ?", nodeIDs),
				).Delete(&model.DocTag{}).Error; err != nil {
				return trace.Wrap(err, "delete namespace doc_tags")
			}

			if err := tx.
				Where("node_id IN ?", nodeIDs).
				Delete(&model.Annotation{}).Error; err != nil {
				return trace.Wrap(err, "delete namespace annotations")
			}

			if err := tx.
				Where("node_id IN ?", nodeIDs).
				Delete(&model.CommunityMembership{}).Error; err != nil {
				return trace.Wrap(err, "delete namespace community memberships")
			}

			if tx.Migrator().HasTable(&model.FlowMembership{}) {
				if err := tx.
					Where("node_id IN ?", nodeIDs).
					Delete(&model.FlowMembership{}).Error; err != nil {
					return trace.Wrap(err, "delete namespace flow memberships")
				}
			}

			if tx.Migrator().HasTable(&model.SearchDocument{}) {
				if err := tx.
					Where("node_id IN ?", nodeIDs).
					Delete(&model.SearchDocument{}).Error; err != nil {
					return trace.Wrap(err, "delete namespace search_documents")
				}
			}

			if err := tx.
				Where("from_node_id IN ? OR to_node_id IN ?", nodeIDs, nodeIDs).
				Delete(&model.Edge{}).Error; err != nil {
				return trace.Wrap(err, "delete namespace connected edges")
			}

			if err := tx.
				Where("id IN ?", nodeIDs).
				Delete(&model.Node{}).Error; err != nil {
				return trace.Wrap(err, "delete namespace nodes")
			}
		}

		return nil
	})
}

// UpsertEdges는 엣지 배치를 fingerprint 기준으로 저장한다.
// @intent 그래프 관계를 중복 없이 일괄 반영한다.
// @sideEffect edges 테이블에 배치 insert를 수행한다.
// @domainRule 동일 fingerprint 엣지는 한 번만 저장한다.
func (s *Store) UpsertEdges(ctx context.Context, edges []model.Edge) error {
	if len(edges) == 0 {
		return nil
	}
	ns := ctxns.FromContext(ctx)
	for i := range edges {
		edges[i].Namespace = ns
	}
	if err := s.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "namespace"}, {Name: "fingerprint"}},
			DoNothing: true,
		}).
		CreateInBatches(edges, 500).Error; err != nil {
		return trace.Wrap(err, "upsert edge batch")
	}
	return nil
}

// GetEdgesFrom는 노드에서 나가는 엣지를 조회한다.
// @intent 특정 선언의 outbound 관계를 불러온다.
func (s *Store) GetEdgesFrom(ctx context.Context, nodeID uint) ([]model.Edge, error) {
	var edges []model.Edge
	ns := ctxns.FromContext(ctx)
	if err := s.db.WithContext(ctx).Where("namespace = ? AND from_node_id = ?", ns, nodeID).Find(&edges).Error; err != nil {
		return nil, err
	}
	return edges, nil
}

// GetEdgesFromNodes는 여러 노드에서 나가는 엣지를 조회한다.
// @intent 여러 선언의 outbound 관계를 한 번에 불러온다.
// @return nodeIDs가 비어 있으면 nil 슬라이스를 반환한다.
func (s *Store) GetEdgesFromNodes(ctx context.Context, nodeIDs []uint) ([]model.Edge, error) {
	if len(nodeIDs) == 0 {
		return nil, nil
	}
	var edges []model.Edge
	ns := ctxns.FromContext(ctx)
	if err := s.db.WithContext(ctx).Where("namespace = ? AND from_node_id IN ?", ns, nodeIDs).Find(&edges).Error; err != nil {
		return nil, err
	}
	return edges, nil
}

// GetEdgesTo는 노드로 들어오는 엣지를 조회한다.
// @intent 특정 선언의 inbound 관계를 불러온다.
func (s *Store) GetEdgesTo(ctx context.Context, nodeID uint) ([]model.Edge, error) {
	var edges []model.Edge
	ns := ctxns.FromContext(ctx)
	if err := s.db.WithContext(ctx).Where("namespace = ? AND to_node_id = ?", ns, nodeID).Find(&edges).Error; err != nil {
		return nil, err
	}
	return edges, nil
}

// GetEdgesToNodes는 여러 노드로 들어오는 엣지를 조회한다.
// @intent 여러 선언의 inbound 관계를 한 번에 불러온다.
// @return nodeIDs가 비어 있으면 nil 슬라이스를 반환한다.
func (s *Store) GetEdgesToNodes(ctx context.Context, nodeIDs []uint) ([]model.Edge, error) {
	if len(nodeIDs) == 0 {
		return nil, nil
	}
	var edges []model.Edge
	ns := ctxns.FromContext(ctx)
	if err := s.db.WithContext(ctx).Where("namespace = ? AND to_node_id IN ?", ns, nodeIDs).Find(&edges).Error; err != nil {
		return nil, err
	}
	return edges, nil
}

// DeleteEdgesByFile는 파일에서 생성된 엣지를 제거한다.
// @intent 파일 단위 갱신 시 기존 관계만 선택적으로 정리한다.
// @sideEffect edges 테이블에서 해당 file_path 레코드를 삭제한다.
func (s *Store) DeleteEdgesByFile(ctx context.Context, filePath string) error {
	ns := ctxns.FromContext(ctx)
	return s.db.WithContext(ctx).Where("namespace = ? AND file_path = ?", ns, filePath).Delete(&model.Edge{}).Error
}

// UpsertAnnotation는 노드의 어노테이션과 태그를 저장한다.
// @intent 노드별 구조화 주석을 최신 상태로 교체한다.
// @sideEffect annotations, doc_tags 테이블에 insert/update/delete를 수행한다.
// @mutates ann.ID를 기존 레코드 ID로 덮어쓸 수 있다.
// @domainRule node_id당 어노테이션은 하나만 유지되어야 한다.
func (s *Store) UpsertAnnotation(ctx context.Context, ann *model.Annotation) error {
	var existing model.Annotation
	ns := ctxns.FromContext(ctx)
	result := s.db.WithContext(ctx).
		Joins("JOIN nodes ON nodes.id = annotations.node_id").
		Where("annotations.node_id = ? AND nodes.namespace = ?", ann.NodeID, ns).
		First(&existing)

	if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			return tx.Create(ann).Error
		})
	}
	if result.Error != nil {
		return result.Error
	}

	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("annotation_id = ?", existing.ID).Delete(&model.DocTag{}).Error; err != nil {
			return trace.Wrap(err, "delete doc tags")
		}
		ann.ID = existing.ID
		return tx.Save(ann).Error
	})
}

// GetAnnotation는 노드 ID에 연결된 어노테이션을 조회한다.
// @intent 검색/표시용으로 노드의 구조화 주석과 태그를 함께 불러온다.
// @return 어노테이션이 없으면 nil을 반환한다.
func (s *Store) GetAnnotation(ctx context.Context, nodeID uint) (*model.Annotation, error) {
	var ann model.Annotation
	ns := ctxns.FromContext(ctx)
	result := s.db.WithContext(ctx).
		Preload("Tags").
		Joins("JOIN nodes ON nodes.id = annotations.node_id").
		Where("annotations.node_id = ? AND nodes.namespace = ?", nodeID, ns).
		First(&ann)
	if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if result.Error != nil {
		return nil, trace.Wrap(result.Error, "get annotation")
	}
	return &ann, nil
}

// WithTx는 주어진 함수를 같은 트랜잭션 안에서 실행한다.
// @intent 여러 저장소 작업을 원자적으로 묶어 수행하게 한다.
// @sideEffect 데이터베이스 트랜잭션을 시작하고 commit 또는 rollback 한다.
// @ensures fn이 nil error를 반환하면 트랜잭션이 커밋된다.
func (s *Store) WithTx(ctx context.Context, fn func(store store.GraphStore) error) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		txStore := New(tx)
		return fn(txStore)
	})
}

var _ store.GraphStore = (*Store)(nil)
