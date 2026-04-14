package search

import (
	"context"
	"strings"

	"gorm.io/gorm"

	"github.com/tae2089/trace"

	"github.com/imtaebin/code-context-graph/internal/model"
)

type SQLiteBackend struct{}

func NewSQLiteBackend() *SQLiteBackend {
	return &SQLiteBackend{}
}

func (s *SQLiteBackend) Migrate(db *gorm.DB) error {
	if err := db.Exec(`
		CREATE VIRTUAL TABLE IF NOT EXISTS search_fts
		USING fts5(node_id, content, language)
	`).Error; err != nil {
		if strings.Contains(err.Error(), "no such module: fts5") {
			return trace.Wrap(ErrFTS5NotAvailable, err.Error())
		}
		return err
	}
	return nil
}

func (s *SQLiteBackend) Rebuild(ctx context.Context, db *gorm.DB) error {
	if err := db.WithContext(ctx).Exec("DELETE FROM search_fts").Error; err != nil {
		return trace.Wrap(err, "clear fts")
	}

	var docs []model.SearchDocument
	if err := db.WithContext(ctx).Find(&docs).Error; err != nil {
		return trace.Wrap(err, "load docs")
	}

	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, doc := range docs {
			if err := tx.Exec(
				"INSERT INTO search_fts(node_id, content, language) VALUES (?, ?, ?)",
				doc.NodeID, doc.Content, doc.Language,
			).Error; err != nil {
				return trace.Wrap(err, "insert fts row")
			}
		}
		return nil
	})
}

func (s *SQLiteBackend) Query(ctx context.Context, db *gorm.DB, query string, limit int) ([]model.Node, error) {
	tokens := strings.Fields(query)
	for i, tok := range tokens {
		if !strings.HasSuffix(tok, "*") {
			tokens[i] = tok + "*"
		}
	}
	ftsQuery := strings.Join(tokens, " ")

	type ftsRow struct {
		NodeID uint
	}

	var rows []ftsRow
	if err := db.WithContext(ctx).Raw(
		`SELECT CAST(node_id AS INTEGER) AS node_id
		 FROM search_fts
		 WHERE search_fts MATCH ?
		 ORDER BY rank
		 LIMIT ?`,
		ftsQuery, limit,
	).Scan(&rows).Error; err != nil {
		return nil, trace.Wrap(err, "fts query")
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
		return nil, trace.Wrap(err, "load nodes")
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

var _ Backend = (*SQLiteBackend)(nil)
