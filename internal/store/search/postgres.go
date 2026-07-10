// @index PostgreSQL tsvector + GIN based full-text search backend implementation (including schema, triggers, and queries).
package search

import (
	"context"
	"fmt"
	"log/slog"

	"gorm.io/gorm"

	"github.com/tae2089/trace"

	"github.com/tae2089/code-context-graph/internal/ctxns"
	"github.com/tae2089/code-context-graph/internal/model"
)

// fuzzyWordSimilarityThreshold is the minimum pg_trgm word_similarity for a symbol name to be
// accepted as a typo-tolerant fuzzy match. Tuned to admit single-character typos while rejecting
// unrelated names.
const fuzzyWordSimilarityThreshold = 0.4

// PostgresBackend is a full-text search backend based on PostgreSQL tsvector.
// @intent Handles full-text search indexing and querying in a PostgreSQL environment.
type PostgresBackend struct{}

// NewPostgresBackend creates a PostgreSQL search backend.
// @intent Provides a Backend implementation specifically for PostgreSQL.
func NewPostgresBackend() *PostgresBackend {
	return &PostgresBackend{}
}

// Migrate prepares the PostgreSQL full-text search schema.
// @intent Sets up the tsvector-based search infrastructure on the search_documents table.
// @sideEffect Creates or replaces columns, indexes, trigger functions, and triggers.
// @ensures The tsv column is automatically updated when search_documents is modified.
func (p *PostgresBackend) Migrate(db *gorm.DB) error {
	if err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`
			ALTER TABLE search_documents
			ADD COLUMN IF NOT EXISTS tsv tsvector,
			ADD COLUMN IF NOT EXISTS namespace varchar(256) NOT NULL DEFAULT ''
		`).Error; err != nil {
			return trace.Wrap(err, "add tsv column")
		}

		if err := tx.Exec(`
			CREATE OR REPLACE FUNCTION search_documents_tsv_trigger() RETURNS trigger AS $$
			BEGIN
				NEW.tsv := to_tsvector('simple', COALESCE(NEW.content, ''));
				RETURN NEW;
			END
			$$ LANGUAGE plpgsql
		`).Error; err != nil {
			return trace.Wrap(err, "create trigger function")
		}

		if err := tx.Exec(`
			DROP TRIGGER IF EXISTS trg_search_documents_tsv ON search_documents
		`).Error; err != nil {
			return trace.Wrap(err, "drop old trigger")
		}

		if err := tx.Exec(`
			CREATE TRIGGER trg_search_documents_tsv
			BEFORE INSERT OR UPDATE ON search_documents
			FOR EACH ROW EXECUTE FUNCTION search_documents_tsv_trigger()
		`).Error; err != nil {
			return trace.Wrap(err, "create trigger")
		}

		return nil
	}); err != nil {
		return err
	}

	if err := db.Exec(`
		CREATE INDEX IF NOT EXISTS idx_search_documents_tsv
		ON search_documents USING gin(tsv)
	`).Error; err != nil {
		return trace.Wrap(err, "create gin index")
	}

	// pg_trgm powers typo-tolerant fuzzy symbol matching. The extension may need
	// elevated privileges, so treat its absence as a graceful downgrade to exact FTS
	// rather than a fatal migration error.
	if err := db.Exec(`CREATE EXTENSION IF NOT EXISTS pg_trgm`).Error; err != nil {
		slog.Warn("pg_trgm unavailable; fuzzy symbol search disabled", trace.SlogError(err))
		return nil
	}
	if err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_nodes_name_trgm ON nodes USING gin (name gin_trgm_ops)`).Error; err != nil {
		return trace.Wrap(err, "create name trgm index")
	}
	if err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_nodes_qualified_name_trgm ON nodes USING gin (qualified_name gin_trgm_ops)`).Error; err != nil {
		return trace.Wrap(err, "create qualified_name trgm index")
	}

	return nil
}

// Rebuild recalculates the tsvector for all search documents.
// @intent Batch regenerates the full-text search index for existing search_documents rows.
// @sideEffect Updates search_documents.tsv values.
func (p *PostgresBackend) Rebuild(ctx context.Context, db *gorm.DB) error {
	ns := ctxns.FromContext(ctx)
	query := `
		UPDATE search_documents
		SET tsv = to_tsvector('simple', COALESCE(content, ''))
		WHERE namespace = ?`
	args := []any{ns}
	return db.WithContext(ctx).Exec(query, args...).Error
}

// RebuildNodes recalculates the tsvector only for specified nodes.
// @intent Avoids full namespace tsv updates during incremental update paths.
func (p *PostgresBackend) RebuildNodes(ctx context.Context, db *gorm.DB, nodeIDs []uint) error {
	if len(nodeIDs) == 0 {
		return nil
	}
	ns := ctxns.FromContext(ctx)
	query := `
		UPDATE search_documents
		SET tsv = to_tsvector('simple', COALESCE(content, ''))
		WHERE namespace = ? AND node_id IN ?`
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for start := 0; start < len(nodeIDs); start += scopedRebuildChunkSize {
			end := min(start+scopedRebuildChunkSize, len(nodeIDs))
			chunk := nodeIDs[start:end]
			if err := tx.Exec(query, ns, chunk).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// PurgeNamespace is a no-op as PostgreSQL search_documents deletion does not require separate physical cleanup.
// @intent Aligns with the Backend interface and maintains consistency in the namespace purge path.
func (p *PostgresBackend) PurgeNamespace(ctx context.Context, db *gorm.DB) error {
	return nil
}

// resultRow scans node_id values from PostgreSQL tsquery matches.
// @intent decode the single-column tsquery result before joining back to nodes.
type resultRow struct {
	NodeID uint
}

// Query searches for related nodes using PostgreSQL tsquery.
// @intent Converts the user's search term into a prefix tsquery to find related nodes.
// @requires limit must be greater than 0 to get meaningful results.
// @return Returns a list of nodes sorted by ts_rank.
func (p *PostgresBackend) Query(ctx context.Context, db *gorm.DB, query string, limit int) ([]model.Node, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("limit must be > 0, got %d", limit)
	}
	tsQuery := SanitizePostgresTSQuery(query)
	if tsQuery == "" {
		return nil, nil
	}
	ns := ctxns.FromContext(ctx)

	var rows []resultRow
	querySQL := `
		SELECT sd.node_id
		FROM search_documents sd
		WHERE sd.tsv @@ to_tsquery('simple', ?)
		AND sd.namespace = ?`
	args := []any{tsQuery, ns}
	querySQL += `
		ORDER BY ts_rank(sd.tsv, to_tsquery('simple', ?)) DESC
		LIMIT ?`
	args = append(args, tsQuery, limit)
	if err := db.WithContext(ctx).Raw(querySQL, args...).Scan(&rows).Error; err != nil {
		return nil, trace.Wrap(err, "ts_query")
	}

	// A zero-hit tsquery is not terminal: a misspelled query legitimately matches nothing
	// exactly, and the pg_trgm fuzzy supplement below can still surface candidates.
	if len(rows) == 0 {
		return p.appendFuzzyMatches(ctx, db, query, ns, nil, limit), nil
	}

	nodeIDs := make([]uint, len(rows))
	for i, r := range rows {
		nodeIDs[i] = r.NodeID
	}

	var nodes []model.Node
	nodesQ := db.WithContext(ctx).Where("id IN ?", nodeIDs).Where("namespace = ?", ns)
	if err := nodesQ.Find(&nodes).Error; err != nil {
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

	// When exact FTS underfills the requested limit, supplement with typo-tolerant
	// trigram matches on symbol names so misspelled queries still return candidates.
	if len(result) < limit {
		result = p.appendFuzzyMatches(ctx, db, query, ns, result, limit)
	}

	result = promoteExactNameMatch(result, query)
	return result, nil
}

// appendFuzzyMatches supplements exact-FTS results with pg_trgm similarity matches on
// symbol names, ordered by similarity. It is best-effort: when pg_trgm is unavailable the
// query errors and the exact results are returned unchanged.
// @intent add typo tolerance to Postgres search without letting a missing extension break exact search.
func (p *PostgresBackend) appendFuzzyMatches(ctx context.Context, db *gorm.DB, query, ns string, existing []model.Node, limit int) []model.Node {
	remaining := limit - len(existing)
	if remaining <= 0 {
		return existing
	}
	seen := make(map[uint]struct{}, len(existing))
	for _, n := range existing {
		seen[n.ID] = struct{}{}
	}

	// word_similarity (not similarity) is used so a short query matches the best-fitting
	// extent of a longer symbol name; plain similarity penalizes length differences and
	// misses typos like "authentcate" inside "AuthenticateUser".
	var rows []resultRow
	fuzzySQL := `
		SELECT id AS node_id
		FROM nodes
		WHERE namespace = ?
		AND (word_similarity(?, name) >= ? OR word_similarity(?, qualified_name) >= ?)
		ORDER BY GREATEST(word_similarity(?, name), word_similarity(?, qualified_name)) DESC
		LIMIT ?`
	if err := db.WithContext(ctx).Raw(fuzzySQL, ns, query, fuzzyWordSimilarityThreshold, query, fuzzyWordSimilarityThreshold, query, query, remaining+len(existing)).Scan(&rows).Error; err != nil {
		slog.Debug("pg_trgm fuzzy supplement skipped", trace.SlogError(err))
		return existing
	}

	fuzzyIDs := make([]uint, 0, len(rows))
	for _, r := range rows {
		if _, ok := seen[r.NodeID]; ok {
			continue
		}
		seen[r.NodeID] = struct{}{}
		fuzzyIDs = append(fuzzyIDs, r.NodeID)
	}
	if len(fuzzyIDs) == 0 {
		return existing
	}

	var nodes []model.Node
	if err := db.WithContext(ctx).Where("id IN ?", fuzzyIDs).Where("namespace = ?", ns).Find(&nodes).Error; err != nil {
		slog.Debug("pg_trgm fuzzy node load skipped", trace.SlogError(err))
		return existing
	}
	byID := make(map[uint]model.Node, len(nodes))
	for _, n := range nodes {
		byID[n.ID] = n
	}
	for _, id := range fuzzyIDs {
		if n, ok := byID[id]; ok && n.ID != 0 {
			existing = append(existing, n)
			if len(existing) >= limit {
				break
			}
		}
	}
	return existing
}

var _ Backend = (*PostgresBackend)(nil)
