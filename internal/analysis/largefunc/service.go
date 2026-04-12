package largefunc

import (
	"context"

	"github.com/imtaebin/code-context-graph/internal/model"
	"gorm.io/gorm"
)

type Service struct {
	db *gorm.DB
}

func New(db *gorm.DB) *Service {
	return &Service{db: db}
}

func (s *Service) Find(ctx context.Context, threshold int) ([]model.Node, error) {
	var nodes []model.Node
	err := s.db.WithContext(ctx).
		Where("kind IN ? AND (end_line - start_line + 1) > ?",
			[]model.NodeKind{model.NodeKindFunction, model.NodeKindTest},
			threshold,
		).
		Order("(end_line - start_line + 1) DESC").
		Find(&nodes).Error
	if err != nil {
		return nil, err
	}
	return nodes, nil
}
