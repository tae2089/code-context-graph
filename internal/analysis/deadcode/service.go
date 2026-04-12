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

// Find detects unused code with no incoming edges.
// Used by MCP find_dead_code tool and pre_merge_check prompt.
//
// @return nodes that have zero incoming edges (never called or referenced)
// @intent detect dead code candidates for cleanup
// @domainRule file and test nodes are always excluded from results
// @domainRule supports filtering by node kind and file path pattern
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
