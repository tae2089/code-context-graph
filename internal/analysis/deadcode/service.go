// @index 미사용 코드 탐지. incoming edge가 없는 함수와 클래스를 dead code 후보로 식별한다.
package deadcode

import (
	"context"
	"path"

	"github.com/tae2089/code-context-graph/internal/ctxns"
	"github.com/tae2089/code-context-graph/internal/model"
	"github.com/tae2089/code-context-graph/internal/paging"
	"gorm.io/gorm"
)

// Options controls dead code filtering.
// @intent narrow dead code detection by node kind and file scope
type Options struct {
	Kinds       []model.NodeKind
	FilePattern string
	Page        paging.Request
}

// Result carries one dead-code page plus pagination metadata.
// @intent let callers expose bounded dead-code responses while preserving legacy fields.
type Result struct {
	Items      []model.Node
	Pagination paging.Page
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
	page, err := s.FindPage(ctx, opts)
	if err != nil {
		return nil, err
	}
	return page.Items, nil
}

// FindPage detects unused code with no incoming edges and returns one bounded page.
// @intent push dead-code pagination into the query layer so handlers do not slice full results.
func (s *Service) FindPage(ctx context.Context, opts Options) (Result, error) {
	req, err := paging.Normalize(opts.Page)
	if err != nil {
		return Result{}, err
	}

	q := s.db.WithContext(ctx).
		Where("namespace = ?", ctxns.FromContext(ctx)).
		Where("kind NOT IN ?", []model.NodeKind{model.NodeKindFile, model.NodeKindTest}).
		Where("id NOT IN (?)",
			s.db.Model(&model.Edge{}).Select("to_node_id").Where("namespace = ?", ctxns.FromContext(ctx)),
		)

	if len(opts.Kinds) > 0 {
		q = q.Where("kind IN ?", opts.Kinds)
	}
	if cleanPrefix := normalizePathPrefix(opts.FilePattern); cleanPrefix != "" {
		q = q.Where("file_path = ? OR file_path LIKE ?", cleanPrefix, cleanPrefix+"/%")
	}

	var nodes []model.Node
	if err := q.
		Order("file_path ASC").
		Order("start_line ASC").
		Order("qualified_name ASC").
		Limit(req.Limit + 1).
		Offset(req.Offset).
		Find(&nodes).Error; err != nil {
		return Result{}, err
	}
	hasMore := len(nodes) > req.Limit
	if hasMore {
		nodes = nodes[:req.Limit]
	}
	return Result{Items: nodes, Pagination: paging.BuildPage(req, len(nodes), hasMore)}, nil
}

// normalizePathPrefix cleans a path prefix and returns an empty string if the cleaned result is ".".
// @intent ensure path prefix filtering is consistent regardless of trailing slashes or "." input
// @domainRule a path prefix of "." should be treated the same as an empty prefix (no filtering)
// @see deadcode.Service.FindPage
func normalizePathPrefix(prefix string) string {
	if prefix == "" {
		return ""
	}
	clean := path.Clean(prefix)
	if clean == "." {
		return ""
	}
	return clean
}
