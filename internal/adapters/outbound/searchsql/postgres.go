// @index PostgreSQL tsvector + GIN based full-text search backend implementation (including schema, triggers, and queries).
package searchsql

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"

	"gorm.io/gorm"

	"github.com/tae2089/trace"

	requestctx "github.com/tae2089/code-context-graph/internal/ctx"
	"github.com/tae2089/code-context-graph/internal/db/migration"
	"github.com/tae2089/code-context-graph/internal/domain/graph"
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

// Migrate ensures the PostgreSQL search schema exists by running the versioned migrations,
// which are the single source of truth for the tsvector column, trigger, GIN index, and the
// pg_trgm fuzzy indexes. It no longer hand-writes DDL, so the schema cannot drift from the
// migration files.
// @intent give tests and callers a one-call schema setup that reuses the production migrations.
// @sideEffect applies any pending schema migrations to the connected database.
func (p *PostgresBackend) Migrate(db *gorm.DB) error {
	return migration.RunMigrations(db, "postgres", "")
}

// Rebuild recalculates the tsvector for all search documents.
// @intent Batch regenerates the full-text search index for existing search_documents rows.
// @sideEffect Updates search_documents.tsv values.
func (p *PostgresBackend) Rebuild(ctx context.Context, db *gorm.DB) error {
	ns := requestctx.FromContext(ctx)
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
	ns := requestctx.FromContext(ctx)
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
func (p *PostgresBackend) Query(ctx context.Context, db *gorm.DB, query string, limit int) ([]graph.Node, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("limit must be > 0, got %d", limit)
	}
	tsQuery := SanitizePostgresTSQuery(query)
	if tsQuery == "" {
		return nil, nil
	}
	ns := requestctx.FromContext(ctx)

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
	// exactly, and the pg_trgm fuzzy supplement can still surface candidates. Fuzzy fires
	// only on a total exact miss so it never dilutes the precision of queries that did match.
	if len(rows) == 0 {
		fuzzy := p.appendFuzzyMatches(ctx, db, query, ns, limit)
		return promoteExactNameMatch(fuzzy, query), nil
	}

	nodeIDs := make([]uint, len(rows))
	for i, r := range rows {
		nodeIDs[i] = r.NodeID
	}

	var nodes []graph.Node
	nodesQ := db.WithContext(ctx).Where("id IN ?", nodeIDs).Where("namespace = ?", ns)
	if err := nodesQ.Find(&nodes).Error; err != nil {
		return nil, trace.Wrap(err, "load nodes")
	}

	idxMap := make(map[uint]int, len(nodeIDs))
	for i, id := range nodeIDs {
		idxMap[id] = i
	}
	sorted := make([]graph.Node, len(nodes))
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

	result = promoteExactNameMatch(result, query)
	return result, nil
}

// appendFuzzyMatches returns up to limit pg_trgm fuzzy matches on symbol names for queries
// that produced no exact FTS hit (typo tolerance). It is best-effort: if pg_trgm is
// unavailable the query errors and an empty result is returned so exact search still works.
// @intent add typo tolerance to Postgres search without letting a missing extension break exact search.
// @domainRule only nodes that also have a search_documents row are eligible, so fuzzy stays
// within the same corpus and kind mix as the exact FTS path.
func (p *PostgresBackend) appendFuzzyMatches(ctx context.Context, db *gorm.DB, query, ns string, limit int) []graph.Node {
	if limit <= 0 {
		return nil
	}

	// The `<%` operator is index-accelerated by the gin_trgm_ops indexes on name/qualified_name
	// (a functional `word_similarity(...) >= const` filter cannot use them); its cutoff is the
	// session-local word_similarity_threshold, set here so the operator and our tuned threshold
	// agree. Ordering uses the functional form over the already-filtered small set.
	var rows []resultRow
	fuzzySQL := `
		SELECT n.id AS node_id
		FROM nodes n
		JOIN search_documents sd ON sd.node_id = n.id AND sd.namespace = n.namespace
		WHERE n.namespace = ?
		AND (? <% n.name OR ? <% n.qualified_name)
		ORDER BY GREATEST(word_similarity(?, n.name), word_similarity(?, n.qualified_name)) DESC
		LIMIT ?`
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if e := tx.Exec(`SELECT set_config('pg_trgm.word_similarity_threshold', ?, true)`,
			strconv.FormatFloat(fuzzyWordSimilarityThreshold, 'f', -1, 64)).Error; e != nil {
			return e
		}
		return tx.Raw(fuzzySQL, ns, query, query, query, query, limit).Scan(&rows).Error
	})
	if err != nil {
		// Context cancellation is the caller's concern, not a fuzzy fault; only surface real errors.
		if ctx.Err() == nil {
			slog.Warn("pg_trgm fuzzy supplement failed", trace.SlogError(err))
		}
		return nil
	}
	if len(rows) == 0 {
		return nil
	}

	nodeIDs := make([]uint, len(rows))
	for i, r := range rows {
		nodeIDs[i] = r.NodeID
	}
	var nodes []graph.Node
	if err := db.WithContext(ctx).Where("id IN ?", nodeIDs).Where("namespace = ?", ns).Find(&nodes).Error; err != nil {
		if ctx.Err() == nil {
			slog.Warn("pg_trgm fuzzy node load failed", trace.SlogError(err))
		}
		return nil
	}
	byID := make(map[uint]graph.Node, len(nodes))
	for _, n := range nodes {
		byID[n.ID] = n
	}
	ordered := make([]graph.Node, 0, len(nodeIDs))
	for _, id := range nodeIDs {
		if n, ok := byID[id]; ok && n.ID != 0 {
			ordered = append(ordered, n)
		}
	}
	return ordered
}

var _ Backend = (*PostgresBackend)(nil)
