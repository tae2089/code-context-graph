// @index GORM persistence adapter for git-diff change-risk analysis.
package graphgorm

import (
	"context"

	analyzeapp "github.com/tae2089/code-context-graph/internal/app/analyze"
	requestctx "github.com/tae2089/code-context-graph/internal/ctx"
	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

// NodesByFiles loads deterministic namespace-scoped graph nodes for changed files.
// @intent supply diff-overlap inputs without exposing database filters to change policy.
func (s *Store) NodesByFiles(ctx context.Context, filePaths []string) ([]graph.Node, error) {
	var nodes []graph.Node
	if err := s.db.WithContext(ctx).
		Where("namespace = ? AND file_path IN ?", requestctx.FromContext(ctx), filePaths).
		Order("file_path ASC").
		Order("start_line ASC").
		Order("qualified_name ASC").
		Order("id ASC").
		Find(&nodes).Error; err != nil {
		return nil, err
	}
	return nodes, nil
}

// @intent carry one grouped edge-count projection from GORM into the change-risk repository result.
type outgoingEdgeCount struct {
	FromNodeID uint
	Count      int64
}

// OutgoingEdgeCounts returns namespace-scoped outgoing edge counts keyed by source node ID.
// @intent provide risk-weight inputs through one grouped persistence query.
func (s *Store) OutgoingEdgeCounts(ctx context.Context, nodeIDs []uint) (map[uint]int64, error) {
	if len(nodeIDs) == 0 {
		return map[uint]int64{}, nil
	}
	var rows []outgoingEdgeCount
	if err := s.db.WithContext(ctx).
		Model(&graph.Edge{}).
		Select("from_node_id, COUNT(*) as count").
		Where("namespace = ? AND from_node_id IN ?", requestctx.FromContext(ctx), nodeIDs).
		Group("from_node_id").
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	counts := make(map[uint]int64, len(rows))
	for _, row := range rows {
		counts[row.FromNodeID] = row.Count
	}
	return counts, nil
}

var _ analyzeapp.ChangeRepository = (*Store)(nil)
