// @index GORM persistence adapter for predefined analysis graph queries.
package graphgorm

import (
	"context"

	"gorm.io/gorm"

	analyzeapp "github.com/tae2089/code-context-graph/internal/app/analyze"
	requestctx "github.com/tae2089/code-context-graph/internal/ctx"
	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

// RelatedNodes loads one deterministic distinct relationship page and its total count.
// @intent implement namespace-scoped relationship joins behind the analysis query repository.
func (s *Store) RelatedNodes(ctx context.Context, request analyzeapp.RelatedNodesRequest) (analyzeapp.RelatedNodesPage, error) {
	ns := requestctx.FromContext(ctx)
	var q *gorm.DB
	switch request.Direction {
	case analyzeapp.EdgeDirectionIncoming:
		q = s.db.WithContext(ctx).
			Model(&graph.Node{}).
			Where("nodes.namespace = ?", ns).
			Joins("JOIN edges ON edges.from_node_id = nodes.id").
			Where("edges.namespace = ? AND edges.to_node_id = ? AND edges.kind IN ?", ns, request.NodeID, request.EdgeKinds)
	default:
		q = s.db.WithContext(ctx).
			Model(&graph.Node{}).
			Where("nodes.namespace = ?", ns).
			Joins("JOIN edges ON edges.to_node_id = nodes.id").
			Where("edges.namespace = ? AND edges.from_node_id = ? AND edges.kind IN ?", ns, request.NodeID, request.EdgeKinds)
	}

	var total int64
	if err := q.Session(&gorm.Session{}).Distinct("nodes.id").Count(&total).Error; err != nil {
		return analyzeapp.RelatedNodesPage{}, err
	}
	q = q.Session(&gorm.Session{}).
		Select("DISTINCT nodes.*").
		Order("nodes.file_path ASC").
		Order("nodes.start_line ASC").
		Order("nodes.qualified_name ASC")
	if request.Limit > 0 {
		q = q.Limit(request.Limit).Offset(request.Offset)
	}
	var nodes []graph.Node
	if err := q.Find(&nodes).Error; err != nil {
		return analyzeapp.RelatedNodesPage{}, err
	}
	return analyzeapp.RelatedNodesPage{Nodes: nodes, TotalCount: int(total)}, nil
}

// NodesByFile loads all namespace-scoped nodes belonging to one file.
// @intent supply file-summary inputs without exposing database filtering to app policy.
func (s *Store) NodesByFile(ctx context.Context, filePath string) ([]graph.Node, error) {
	return s.GetNodesByFile(ctx, filePath)
}

// NodesByExactName loads deterministic namespace-scoped short-name matches.
// @intent support exact-name fallback suggestions through the analysis repository.
func (s *Store) NodesByExactName(ctx context.Context, name string, limit int) ([]graph.Node, error) {
	var nodes []graph.Node
	if err := s.db.WithContext(ctx).
		Where("namespace = ? AND name = ?", requestctx.FromContext(ctx), name).
		Order("file_path ASC").
		Order("start_line ASC").
		Order("qualified_name ASC").
		Limit(limit).
		Find(&nodes).Error; err != nil {
		return nil, err
	}
	return nodes, nil
}

var _ analyzeapp.QueryRepository = (*Store)(nil)
