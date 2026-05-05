// @index Large function and test detection that returns nodes exceeding a line-count threshold in descending size order.
package largefunc

import (
	"context"
	"path"

	"github.com/tae2089/code-context-graph/internal/ctxns"
	"github.com/tae2089/code-context-graph/internal/model"
	"github.com/tae2089/code-context-graph/internal/paging"
	"gorm.io/gorm"
)

// Service finds oversized functions and tests.
// @intent highlight large code units that may need refactoring or review
type Service struct {
	db *gorm.DB
}

// Options controls bounded large-function detection.
// @intent keep large-function filtering and pagination rules in one typed input.
type Options struct {
	Threshold  int
	PathPrefix string
	Page       paging.Request
}

// Result carries one large-function page plus pagination metadata.
// @intent let MCP handlers expose bounded large-function results without recomputing has_more.
type Result struct {
	Items      []model.Node
	Pagination paging.Page
}

// New creates a large function analysis service.
// @intent construct a service for querying nodes above a line threshold
func New(db *gorm.DB) *Service {
	return &Service{db: db}
}

// Find returns functions and tests longer than the threshold.
// @intent identify oversized executable nodes for maintainability analysis
// @param threshold minimum line-count threshold that results must strictly exceed
// @return functions and tests ordered from longest to shortest
// @domainRule only function and test nodes participate in large-function analysis
// @domainRule line count is computed as (end_line - start_line + 1) and must strictly exceed threshold
// @see mcp.handlers.findLargeFunctions
func (s *Service) Find(ctx context.Context, threshold int) ([]model.Node, error) {
	var nodes []model.Node
	for offset := 0; ; offset += paging.MaxLimit {
		page, err := s.FindPage(ctx, Options{Threshold: threshold, Page: paging.Request{Limit: paging.MaxLimit, Offset: offset}})
		if err != nil {
			return nil, err
		}
		nodes = append(nodes, page.Items...)
		if !page.Pagination.HasMore {
			return nodes, nil
		}
	}
}

// FindPage returns a bounded page of functions and tests longer than the threshold.
// @intent apply pagination at the query layer so large-function analysis stays bounded.
// @domainRule results are sorted by descending line count with deterministic file/name tiebreakers.
func (s *Service) FindPage(ctx context.Context, opts Options) (Result, error) {
	req, err := paging.Normalize(opts.Page)
	if err != nil {
		return Result{}, err
	}

	var nodes []model.Node
	q := s.db.WithContext(ctx).
		Where("namespace = ?", ctxns.FromContext(ctx)).
		Where("kind IN ? AND (end_line - start_line + 1) > ?",
			[]model.NodeKind{model.NodeKindFunction, model.NodeKindTest},
			opts.Threshold,
		).
		Order("(end_line - start_line + 1) DESC").
		Order("file_path ASC").
		Order("start_line ASC").
		Order("qualified_name ASC")

	if cleanPrefix := normalizePathPrefix(opts.PathPrefix); cleanPrefix != "" {
		q = q.Where("file_path = ? OR file_path LIKE ?", cleanPrefix, cleanPrefix+"/%")
	}

	if err := q.Limit(req.Limit + 1).Offset(req.Offset).Find(&nodes).Error; err != nil {
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
// @see largefunc.Service.FindPage
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
