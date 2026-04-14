// @index 미사용 코드 탐지. incoming edge가 없는 함수와 클래스를 dead code 후보로 식별한다.
package deadcode

import (
	"context"

	"github.com/imtaebin/code-context-graph/internal/model"
	"gorm.io/gorm"
)

// Options controls dead code filtering.
// @intent narrow dead code detection by node kind and file scope
type Options struct {
	Kinds       []model.NodeKind
	FilePattern string
}

// Service finds unreachable graph nodes.
// @intent surface code elements that have no incoming references
type Service struct {
	db *gorm.DB
}

// New creates a dead code analysis service.
// @intent construct a service for querying unused graph nodes
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
