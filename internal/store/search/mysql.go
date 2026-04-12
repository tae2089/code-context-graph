package search

import (
	"context"
	"fmt"
	"strings"

	"gorm.io/gorm"

	"github.com/imtaebin/code-context-graph/internal/model"
)

type MySQLBackend struct{}

func NewMySQLBackend() *MySQLBackend {
	return &MySQLBackend{}
}

func (m *MySQLBackend) Migrate(db *gorm.DB) error {
	var count int64
	db.Raw(`
		SELECT COUNT(*) FROM information_schema.statistics
		WHERE table_schema = DATABASE()
		AND table_name = 'search_documents'
		AND index_name = 'idx_search_documents_ft'
	`).Scan(&count)

	if count == 0 {
		if err := db.Exec(`
			ALTER TABLE search_documents
			ADD FULLTEXT INDEX idx_search_documents_ft (content)
		`).Error; err != nil {
			return fmt.Errorf("create fulltext index: %w", err)
		}
	}
	return nil
}

func (m *MySQLBackend) Rebuild(_ context.Context, _ *gorm.DB) error {
	return nil
}

func (m *MySQLBackend) Query(ctx context.Context, db *gorm.DB, query string, limit int) ([]model.Node, error) {
	tokens := strings.Fields(query)
	for i, tok := range tokens {
		tokens[i] = "+" + tok + "*"
	}
	matchExpr := strings.Join(tokens, " ")

	type resultRow struct {
		NodeID uint
	}

	var rows []resultRow
	if err := db.WithContext(ctx).Raw(`
		SELECT sd.node_id
		FROM search_documents sd
		WHERE MATCH(sd.content) AGAINST(? IN BOOLEAN MODE)
		ORDER BY MATCH(sd.content) AGAINST(? IN BOOLEAN MODE) DESC
		LIMIT ?
	`, matchExpr, matchExpr, limit).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("fulltext query: %w", err)
	}

	if len(rows) == 0 {
		return nil, nil
	}

	nodeIDs := make([]uint, len(rows))
	for i, r := range rows {
		nodeIDs[i] = r.NodeID
	}

	var nodes []model.Node
	if err := db.WithContext(ctx).Where("id IN ?", nodeIDs).Find(&nodes).Error; err != nil {
		return nil, fmt.Errorf("load nodes: %w", err)
	}

	idxMap := make(map[uint]int, len(nodeIDs))
	for i, id := range nodeIDs {
		idxMap[id] = i
	}
	sorted := make([]model.Node, len(nodes))
	for _, n := range nodes {
		if idx, ok := idxMap[n.ID]; ok {
			sorted[idx] = n
		}
	}

	result := sorted[:0]
	for _, n := range sorted {
		if n.ID != 0 {
			result = append(result, n)
		}
	}

	return result, nil
}

var _ Backend = (*MySQLBackend)(nil)
