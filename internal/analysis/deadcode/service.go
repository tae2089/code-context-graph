package deadcode

import (
	"context"

	"github.com/imtaebin/code-context-graph/internal/model"
	"gorm.io/gorm"
)

type Options struct {
	Kinds       []model.NodeKind
	FilePattern string
}

type Service struct {
	db *gorm.DB
}

func New(db *gorm.DB) *Service {
	return &Service{db: db}
}

func (s *Service) Find(ctx context.Context, opts Options) ([]model.Node, error) {
	q := s.db.WithContext(ctx).
		Where("kind NOT IN ?", []model.NodeKind{model.NodeKindFile, model.NodeKindTest}).
		Where("id NOT IN (?)",
			s.db.Model(&model.Edge{}).Select("to_node_id"),
		)

	if len(opts.Kinds) > 0 {
		q = q.Where("kind IN ?", opts.Kinds)
	}
	if opts.FilePattern != "" {
		q = q.Where("file_path LIKE ?", opts.FilePattern+"%")
	}

	var nodes []model.Node
	if err := q.Find(&nodes).Error; err != nil {
		return nil, err
	}
	return nodes, nil
}
