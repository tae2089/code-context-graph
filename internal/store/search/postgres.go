package search

import (
	"context"
	"fmt"
	"strings"

	"gorm.io/gorm"

	"github.com/imtaebin/code-context-graph/internal/model"
)

type PostgresBackend struct{}

func NewPostgresBackend() *PostgresBackend {
	return &PostgresBackend{}
}

func (p *PostgresBackend) Migrate(db *gorm.DB) error {
	if err := db.Exec(`
		ALTER TABLE search_documents
		ADD COLUMN IF NOT EXISTS tsv tsvector
	`).Error; err != nil {
		return fmt.Errorf("add tsv column: %w", err)
	}

	if err := db.Exec(`
		CREATE INDEX IF NOT EXISTS idx_search_documents_tsv
		ON search_documents USING gin(tsv)
	`).Error; err != nil {
		return fmt.Errorf("create gin index: %w", err)
	}

	if err := db.Exec(`
		CREATE OR REPLACE FUNCTION search_documents_tsv_trigger() RETURNS trigger AS $$
		BEGIN
			NEW.tsv := to_tsvector('simple', COALESCE(NEW.content, ''));
			RETURN NEW;
		END
		$$ LANGUAGE plpgsql
	`).Error; err != nil {
		return fmt.Errorf("create trigger function: %w", err)
	}

	if err := db.Exec(`
		DROP TRIGGER IF EXISTS trg_search_documents_tsv ON search_documents
	`).Error; err != nil {
		return fmt.Errorf("drop old trigger: %w", err)
	}

	if err := db.Exec(`
		CREATE TRIGGER trg_search_documents_tsv
		BEFORE INSERT OR UPDATE ON search_documents
		FOR EACH ROW EXECUTE FUNCTION search_documents_tsv_trigger()
	`).Error; err != nil {
		return fmt.Errorf("create trigger: %w", err)
	}

	return nil
}

func (p *PostgresBackend) Rebuild(ctx context.Context, db *gorm.DB) error {
	return db.WithContext(ctx).Exec(`
		UPDATE search_documents
		SET tsv = to_tsvector('simple', COALESCE(content, ''))
	`).Error
}

func (p *PostgresBackend) Query(ctx context.Context, db *gorm.DB, query string, limit int) ([]model.Node, error) {
	tokens := strings.Fields(query)
	for i, tok := range tokens {
		tokens[i] = tok + ":*"
	}
	tsQuery := strings.Join(tokens, " & ")

	type resultRow struct {
		NodeID uint
	}

	var rows []resultRow
	if err := db.WithContext(ctx).Raw(`
		SELECT sd.node_id
		FROM search_documents sd
		WHERE sd.tsv @@ to_tsquery('simple', ?)
		ORDER BY ts_rank(sd.tsv, to_tsquery('simple', ?)) DESC
		LIMIT ?
	`, tsQuery, tsQuery, limit).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("ts_query: %w", err)
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

var _ Backend = (*PostgresBackend)(nil)
