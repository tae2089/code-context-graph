package gormstore

import (
	"context"
	"fmt"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/imtaebin/code-context-graph/internal/model"
	"github.com/imtaebin/code-context-graph/internal/store"
)

type Store struct {
	db *gorm.DB
}

func New(db *gorm.DB) *Store {
	return &Store{db: db}
}

func (s *Store) AutoMigrate() error {
	return s.db.AutoMigrate(
		&model.Node{},
		&model.Edge{},
		&model.Annotation{},
		&model.DocTag{},
		&model.Community{},
		&model.CommunityMembership{},
	)
}

func (s *Store) UpsertNodes(ctx context.Context, nodes []model.Node) error {
	for i := range nodes {
		result := s.db.WithContext(ctx).
			Where("qualified_name = ?", nodes[i].QualifiedName).
			First(&model.Node{})

		if result.Error == gorm.ErrRecordNotFound {
			if err := s.db.WithContext(ctx).Create(&nodes[i]).Error; err != nil {
				return fmt.Errorf("create node: %w", err)
			}
		} else if result.Error == nil {
			if err := s.db.WithContext(ctx).
				Model(&model.Node{}).
				Where("qualified_name = ?", nodes[i].QualifiedName).
				Updates(map[string]interface{}{
					"kind":       nodes[i].Kind,
					"name":       nodes[i].Name,
					"file_path":  nodes[i].FilePath,
					"start_line": nodes[i].StartLine,
					"end_line":   nodes[i].EndLine,
					"hash":       nodes[i].Hash,
					"language":   nodes[i].Language,
				}).Error; err != nil {
				return fmt.Errorf("update node: %w", err)
			}
		} else {
			return fmt.Errorf("query node: %w", result.Error)
		}
	}
	return nil
}

func (s *Store) GetNode(ctx context.Context, qualifiedName string) (*model.Node, error) {
	var node model.Node
	result := s.db.WithContext(ctx).Where("qualified_name = ?", qualifiedName).First(&node)
	if result.Error == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if result.Error != nil {
		return nil, result.Error
	}
	return &node, nil
}

func (s *Store) GetNodeByID(ctx context.Context, id uint) (*model.Node, error) {
	var node model.Node
	result := s.db.WithContext(ctx).First(&node, id)
	if result.Error == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if result.Error != nil {
		return nil, result.Error
	}
	return &node, nil
}

func (s *Store) GetNodesByFile(ctx context.Context, filePath string) ([]model.Node, error) {
	var nodes []model.Node
	if err := s.db.WithContext(ctx).Where("file_path = ?", filePath).Find(&nodes).Error; err != nil {
		return nil, err
	}
	return nodes, nil
}

func (s *Store) DeleteNodesByFile(ctx context.Context, filePath string) error {
	var nodeIDs []uint
	if err := s.db.WithContext(ctx).
		Model(&model.Node{}).
		Where("file_path = ?", filePath).
		Pluck("id", &nodeIDs).Error; err != nil {
		return fmt.Errorf("pluck node ids: %w", err)
	}
	if len(nodeIDs) == 0 {
		return nil
	}

	if err := s.db.WithContext(ctx).
		Where("from_node_id IN ? OR to_node_id IN ?", nodeIDs, nodeIDs).
		Delete(&model.Edge{}).Error; err != nil {
		return fmt.Errorf("cascade delete edges: %w", err)
	}

	if err := s.db.WithContext(ctx).
		Where("annotation_id IN (?)",
			s.db.Model(&model.Annotation{}).Select("id").Where("node_id IN ?", nodeIDs),
		).Delete(&model.DocTag{}).Error; err != nil {
		return fmt.Errorf("cascade delete doc_tags: %w", err)
	}

	if err := s.db.WithContext(ctx).
		Where("node_id IN ?", nodeIDs).
		Delete(&model.Annotation{}).Error; err != nil {
		return fmt.Errorf("cascade delete annotations: %w", err)
	}

	return s.db.WithContext(ctx).Where("file_path = ?", filePath).Delete(&model.Node{}).Error
}

func (s *Store) UpsertEdges(ctx context.Context, edges []model.Edge) error {
	for i := range edges {
		if err := s.db.WithContext(ctx).
			Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "fingerprint"}},
				DoNothing: true,
			}).
			Create(&edges[i]).Error; err != nil {
			return fmt.Errorf("upsert edge: %w", err)
		}
	}
	return nil
}

func (s *Store) GetEdgesFrom(ctx context.Context, nodeID uint) ([]model.Edge, error) {
	var edges []model.Edge
	if err := s.db.WithContext(ctx).Where("from_node_id = ?", nodeID).Find(&edges).Error; err != nil {
		return nil, err
	}
	return edges, nil
}

func (s *Store) GetEdgesTo(ctx context.Context, nodeID uint) ([]model.Edge, error) {
	var edges []model.Edge
	if err := s.db.WithContext(ctx).Where("to_node_id = ?", nodeID).Find(&edges).Error; err != nil {
		return nil, err
	}
	return edges, nil
}

func (s *Store) DeleteEdgesByFile(ctx context.Context, filePath string) error {
	return s.db.WithContext(ctx).Where("file_path = ?", filePath).Delete(&model.Edge{}).Error
}

func (s *Store) UpsertAnnotation(ctx context.Context, ann *model.Annotation) error {
	var existing model.Annotation
	result := s.db.WithContext(ctx).Where("node_id = ?", ann.NodeID).First(&existing)

	if result.Error == gorm.ErrRecordNotFound {
		return s.db.WithContext(ctx).Create(ann).Error
	}
	if result.Error != nil {
		return result.Error
	}

	s.db.WithContext(ctx).Where("annotation_id = ?", existing.ID).Delete(&model.DocTag{})

	ann.ID = existing.ID
	return s.db.WithContext(ctx).Save(ann).Error
}

func (s *Store) GetAnnotation(ctx context.Context, nodeID uint) (*model.Annotation, error) {
	var ann model.Annotation
	result := s.db.WithContext(ctx).
		Preload("Tags").
		Where("node_id = ?", nodeID).
		First(&ann)
	if result.Error == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if result.Error != nil {
		return nil, result.Error
	}
	return &ann, nil
}

func (s *Store) WithTx(ctx context.Context, fn func(store store.GraphStore) error) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		txStore := New(tx)
		return fn(txStore)
	})
}

var _ store.GraphStore = (*Store)(nil)
