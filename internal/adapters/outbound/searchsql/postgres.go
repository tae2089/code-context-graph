// @index PostgreSQL tsvector + GIN based full-text search backend implementation (including schema, triggers, and queries).
package searchsql

import (
	"context"
	"fmt"

	"gorm.io/gorm"

	"github.com/tae2089/trace"

	"github.com/tae2089/code-context-graph/internal/app/search/intentrank"
	requestctx "github.com/tae2089/code-context-graph/internal/ctx"
	"github.com/tae2089/code-context-graph/internal/db/migration"
	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

// PostgresBackend is a full-text search backend based on PostgreSQL tsvector.
// @intent Handles full-text search indexing and querying in a PostgreSQL environment.
type PostgresBackend struct{}

var _ Backend = (*PostgresBackend)(nil)

// NewPostgresBackend creates a PostgreSQL search backend.
// @intent Provides a Backend implementation specifically for PostgreSQL.
func NewPostgresBackend() *PostgresBackend {
	return &PostgresBackend{}
}

// Migrate ensures the PostgreSQL search schema exists by running the versioned migrations,
// which are the single source of truth for the tsvector column, trigger, and GIN index. It no
// longer hand-writes DDL, so the schema cannot drift from the migration files.
// @intent give tests and callers a one-call schema setup that reuses the production migrations.
// @sideEffect applies any pending schema migrations to the connected database.
func (p *PostgresBackend) Migrate(db *gorm.DB) error {
	return migration.RunMigrations(db, "postgres", "")
}

// Rebuild recalculates the tsvector for all search documents.
//
// The separators '/', '.', and '_' are translated to spaces before
// to_tsvector, because FTS5's unicode61 tokenizer splits on them and this
// vector has to see the same tokens: without it, PostgreSQL keeps a dotted
// qualified name as one host-like token and cannot answer a query naming one
// of its segments. The trigger in migration 000019 applies the same
// expression on every write.
// @intent Batch regenerates the full-text search index for existing search_documents rows.
// @sideEffect Updates search_documents.tsv values.
func (p *PostgresBackend) Rebuild(ctx context.Context, db *gorm.DB) error {
	ns := requestctx.FromContext(ctx)
	query := `
		UPDATE search_documents
		SET tsv = to_tsvector('simple', translate(COALESCE(content, ''), '/._', '   '))
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
		SET tsv = to_tsvector('simple', translate(COALESCE(content, ''), '/._', '   '))
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

// matchRows runs one tsquery and returns the matching node ids in rank order.
// @intent let Query run the same retrieval twice with a different expression.
func (p *PostgresBackend) matchRows(ctx context.Context, db *gorm.DB, tsQuery, ns string, limit int) ([]resultRow, error) {
	var rows []resultRow
	if err := db.WithContext(ctx).Raw(`
		SELECT sd.node_id
		FROM search_documents sd
		WHERE sd.tsv @@ to_tsquery('simple', ?)
		AND sd.namespace = ?
		ORDER BY ts_rank(sd.tsv, to_tsquery('simple', ?)) DESC
		LIMIT ?`, tsQuery, ns, tsQuery, limit).Scan(&rows).Error; err != nil {
		return nil, trace.Wrap(err, "ts_query")
	}
	return rows, nil
}

// Query searches for related nodes using PostgreSQL tsquery.
//
// Every term is required, mirroring the SQLite backend. See SQLiteBackend.Query
// for why widening to any-term was measured and rejected.
//
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

	rows, err := p.matchRows(ctx, db, tsQuery, ns, limit)
	if err != nil {
		return nil, err
	}

	// A zero-hit tsquery is terminal. A pg_trgm supplement used to fire here and
	// answer misspelled queries approximately, but both callers of search are
	// driven by an agent quoting identifiers out of code it has already read, so
	// a query that matches nothing exactly is naming something that does not
	// exist. Returning nothing says that; returning a near neighbour would not.
	if len(rows) == 0 {
		return nil, nil
	}

	nodeIDs := make([]uint, len(rows))
	for i, r := range rows {
		nodeIDs[i] = r.NodeID
	}
	nodes, err := loadNodesInOrder(ctx, db, nodeIDs)
	if err != nil {
		return nil, err
	}
	return promoteExactNameMatch(nodes, query), nil
}

// MatchIntent finds every recorded reason holding any term of the question.
//
// It used to order by ts_rank, which is where the deployment gap lived: ts_rank
// reads one document at a time and never learns that a word appears in most of
// them, so it could not tell a distinctive word from a filler one. Retrieval is
// what the GIN index is genuinely good at; scoring moved to intentrank, which
// counts the corpus and gives both backends the same answer.
// @intent hand every candidate reason to shared scoring, in whatever order the index produced.
// @requires maxCandidates must be greater than 0.
// @return returns unordered candidates with the exact text that was indexed for each.
func (p *PostgresBackend) MatchIntent(ctx context.Context, db *gorm.DB, query string, maxCandidates int) ([]intentrank.Doc, error) {
	if maxCandidates <= 0 {
		return nil, fmt.Errorf("maxCandidates must be > 0, got %d", maxCandidates)
	}
	tsQuery := SanitizePostgresIntentTSQuery(query)
	if tsQuery == "" {
		return nil, nil
	}

	var docs []intentrank.Doc
	if err := db.WithContext(ctx).Raw(`
		SELECT sd.node_id, sd.intent_content AS content
		FROM search_documents sd
		WHERE sd.intent_tsv @@ to_tsquery('simple', ?)
		AND sd.namespace = ?
		LIMIT ?`, tsQuery, requestctx.FromContext(ctx), maxCandidates).Scan(&docs).Error; err != nil {
		return nil, trace.Wrap(err, "intent ts_query")
	}
	return docs, nil
}
