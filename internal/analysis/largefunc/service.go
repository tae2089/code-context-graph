package largefunc

import (
	"context"

	"github.com/imtaebin/code-context-graph/internal/ctxns"
	"github.com/imtaebin/code-context-graph/internal/model"
	"gorm.io/gorm"
)

// Service finds oversized functions and tests.
// @intent highlight large code units that may need refactoring or review
type Service struct {
	db *gorm.DB
}

// New creates a large function analysis service.
// @intent construct a service for querying nodes above a line threshold
func New(db *gorm.DB) *Service {
	return &Service{db: db}
}

// Find returns functions and tests longer than the threshold.
// @intent identify oversized executable nodes for maintainability analysis
// @param threshold minimum inclusive line-count threshold to exceed
// @return functions and tests ordered from longest to shortest
// @domainRule only function and test nodes participate in large-function analysis
func (s *Service) Find(ctx context.Context, threshold int) ([]model.Node, error) {
	var nodes []model.Node
	err := s.db.WithContext(ctx).
		Where("namespace = ?", ctxns.FromContext(ctx)).
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
