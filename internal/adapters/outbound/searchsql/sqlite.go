// @index SQLite FTS5 virtual table-based full-text search backend implementation (including migration, legacy upgrades, and incremental re-indexing).
package searchsql

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"

	"gorm.io/gorm"

	"github.com/tae2089/trace"

	"github.com/tae2089/code-context-graph/internal/app/search/intentrank"
	requestctx "github.com/tae2089/code-context-graph/internal/ctx"
	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

const (
	sqliteFTSTable            = "search_fts"
	sqliteIntentFTSTable      = "intent_fts"
	sqliteFTSUpgradeTable     = "search_fts_upgrade"
	sqliteFTSLegacyBackup     = "search_fts_legacy_backup"
	sqliteFTSRebuildBatchSize = 500
	scopedRebuildChunkSize    = 400
)

// SQLiteBackend is a full-text search backend based on SQLite FTS5.
// @intent Handles full-text search indexing and querying in a SQLite environment.
type SQLiteBackend struct {
	batchInserter func(ctx context.Context, tx *gorm.DB, tableName string, docs []graph.SearchDocument) error
}

var _ Backend = (*SQLiteBackend)(nil)

// NewSQLiteBackend creates a SQLite search backend.
// @intent Provides a Backend implementation specifically for SQLite.
func NewSQLiteBackend() *SQLiteBackend {
	return &SQLiteBackend{batchInserter: insertSQLiteFTSBatch}
}

// Migrate prepares the SQLite FTS5 virtual table.
// @intent Creates a full-text search index table for SQLite.
// @sideEffect May create the search_fts virtual table.
// @ensures search_fts exists if FTS5 is available.
func (s *SQLiteBackend) Migrate(db *gorm.DB) error {
	return db.Transaction(func(tx *gorm.DB) error {
		existed, err := sqliteTableExists(tx, sqliteFTSTable)
		if err != nil {
			return trace.Wrap(err, "check fts table")
		}
		if err := createSQLiteFTSTable(tx, sqliteFTSTable, true); err != nil {
			if strings.Contains(err.Error(), "no such module: fts5") {
				return trace.Wrap(ErrFTS5NotAvailable, err.Error())
			}
			return err
		}
		hasNamespace, err := sqliteColumnExists(tx, sqliteFTSTable, "namespace")
		if err != nil {
			return trace.Wrap(err, "inspect fts schema")
		}
		if !hasNamespace {
			return s.upgradeLegacyFTSTable(tx)
		}
		if !existed {
			if err := s.rebuildTable(context.Background(), tx, sqliteFTSTable); err != nil {
				return trace.Wrap(err, "seed new fts")
			}
		}
		return s.migrateIntentTable(tx)
	})
}

// migrateIntentTable prepares the intent-only FTS5 table beside the name index.
// @intent give recorded reasons their own index so an intent question is never scored against identifier text.
// @sideEffect may create the intent_fts virtual table.
func (s *SQLiteBackend) migrateIntentTable(tx *gorm.DB) error {
	existed, err := sqliteTableExists(tx, sqliteIntentFTSTable)
	if err != nil {
		return trace.Wrap(err, "check intent fts table")
	}
	if err := createSQLiteIntentFTSTable(tx); err != nil {
		if strings.Contains(err.Error(), "no such module: fts5") {
			return trace.Wrap(ErrFTS5NotAvailable, err.Error())
		}
		return err
	}
	if existed {
		return nil
	}
	if err := s.rebuildIntentTable(context.Background(), tx); err != nil {
		return trace.Wrap(err, "seed new intent fts")
	}
	return nil
}

// Rebuild reloads the persisted documents into the two FTS indexes: search_fts
// from search_documents, and intent_fts from search_reasons, which holds one row
// per recorded reason.
// @intent Synchronizes stored search documents and recorded reasons with the SQLite FTS indexes.
// @sideEffect Deletes and re-inserts search_fts and intent_fts content.
// @domainRule Index content must match the current snapshot of search_documents and search_reasons.
func (s *SQLiteBackend) Rebuild(ctx context.Context, db *gorm.DB) error {
	if err := s.rebuildTable(ctx, db, sqliteFTSTable); err != nil {
		return err
	}
	return s.rebuildIntentTable(ctx, db)
}

// RebuildNodes synchronizes only the FTS rows of specified nodes with search_documents.
// @intent Avoids full namespace FTS reloading during incremental update paths.
func (s *SQLiteBackend) RebuildNodes(ctx context.Context, db *gorm.DB, nodeIDs []uint) error {
	if len(nodeIDs) == 0 {
		return nil
	}
	if err := s.rebuildTableNodes(ctx, db, sqliteFTSTable, nodeIDs); err != nil {
		return err
	}
	return s.rebuildIntentTableNodes(ctx, db, nodeIDs)
}

// PurgeNamespace removes the physical FTS index for a specific namespace.
// @intent Cleans up stale FTS rows even in paths without a rebuild, such as namespace deletion.
func (s *SQLiteBackend) PurgeNamespace(ctx context.Context, db *gorm.DB) error {
	exists, err := sqliteTableExists(db, sqliteFTSTable)
	if err != nil {
		return trace.Wrap(err, "check fts table before purge")
	}
	if !exists {
		return nil
	}
	ns := requestctx.FromContext(ctx)
	if err := db.WithContext(ctx).Exec("DELETE FROM "+sqliteFTSTable+" WHERE namespace = ?", ns).Error; err != nil {
		return err
	}
	intentExists, err := sqliteTableExists(db, sqliteIntentFTSTable)
	if err != nil {
		return trace.Wrap(err, "check intent fts table before purge")
	}
	if !intentExists {
		return nil
	}
	return db.WithContext(ctx).Exec("DELETE FROM "+sqliteIntentFTSTable+" WHERE namespace = ?", ns).Error
}

// rebuildTable clears all FTS rows for the current namespace in tableName and
// repopulates them from search_documents in batches. Used by both full Rebuild
// and the legacy-upgrade path.
// @intent resynchronize one namespace-scoped SQLite FTS table from persisted search documents without disturbing other namespaces.
func (s *SQLiteBackend) rebuildTable(ctx context.Context, db *gorm.DB, tableName string) error {
	ns := requestctx.FromContext(ctx)
	clearSQL := fmt.Sprintf("DELETE FROM %s WHERE namespace = ?", tableName)
	clearArgs := []any{ns}
	return db.WithContext(ctx).Transaction(func(outerTx *gorm.DB) error {
		if err := outerTx.Exec(clearSQL, clearArgs...).Error; err != nil {
			return trace.Wrap(err, "clear fts")
		}

		docsQ := outerTx.WithContext(ctx).Where("namespace = ?", ns)

		var batchDocs []graph.SearchDocument
		result := docsQ.FindInBatches(&batchDocs, sqliteFTSRebuildBatchSize, func(batchTx *gorm.DB, batch int) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := s.batchInserter(ctx, batchTx, tableName, batchDocs); err != nil {
				return trace.Wrap(err, "insert fts batch "+strconv.Itoa(batch))
			}
			return nil
		})
		if result.Error != nil {
			return trace.Wrap(result.Error, "load docs")
		}
		return nil
	})
}

// rebuildTableNodes deletes and re-inserts FTS rows only for the given nodeIDs
// within the current namespace, processing them in chunks of scopedRebuildChunkSize
// to avoid oversized IN clauses.
// @intent refresh only the requested node documents in SQLite FTS so incremental updates can avoid a full namespace rebuild.
func (s *SQLiteBackend) rebuildTableNodes(ctx context.Context, db *gorm.DB, tableName string, nodeIDs []uint) error {
	ns := requestctx.FromContext(ctx)
	return db.WithContext(ctx).Transaction(func(outerTx *gorm.DB) error {
		for start := 0; start < len(nodeIDs); start += scopedRebuildChunkSize {
			end := min(start+scopedRebuildChunkSize, len(nodeIDs))
			chunk := nodeIDs[start:end]
			if err := outerTx.Exec("DELETE FROM "+tableName+" WHERE namespace = ? AND node_id IN ?", ns, chunk).Error; err != nil {
				return trace.Wrap(err, "clear scoped fts")
			}

			docsQ := outerTx.WithContext(ctx).Where("namespace = ? AND node_id IN ?", ns, chunk)
			var batchDocs []graph.SearchDocument
			result := docsQ.FindInBatches(&batchDocs, sqliteFTSRebuildBatchSize, func(batchTx *gorm.DB, batch int) error {
				if err := ctx.Err(); err != nil {
					return err
				}
				if err := s.batchInserter(ctx, batchTx, tableName, batchDocs); err != nil {
					return trace.Wrap(err, "insert scoped fts batch "+strconv.Itoa(batch))
				}
				return nil
			})
			if result.Error != nil {
				return trace.Wrap(result.Error, "load scoped docs")
			}
		}
		return nil
	})
}

// rebuildIntentTable clears the intent index for the current namespace and
// reloads it from search_reasons, one row per recorded reason.
//
// It is a separate walk from rebuildTable because the two indexes now read
// different tables: search_fts follows search_documents, one row per node, and
// intent_fts follows search_reasons, which can hold several rows for one node.
// @intent resynchronize the namespace-scoped intent index from the recorded reasons without disturbing other namespaces.
func (s *SQLiteBackend) rebuildIntentTable(ctx context.Context, db *gorm.DB) error {
	ns := requestctx.FromContext(ctx)
	return db.WithContext(ctx).Transaction(func(outerTx *gorm.DB) error {
		if err := outerTx.Exec("DELETE FROM "+sqliteIntentFTSTable+" WHERE namespace = ?", ns).Error; err != nil {
			return trace.Wrap(err, "clear intent fts")
		}
		reasonsQ := outerTx.WithContext(ctx).Model(&graph.SearchReason{}).Where("namespace = ?", ns).Order("id")
		var batch []graph.SearchReason
		result := reasonsQ.FindInBatches(&batch, sqliteFTSRebuildBatchSize, func(batchTx *gorm.DB, index int) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := insertSQLiteIntentBatch(ctx, batchTx, batch); err != nil {
				return trace.Wrap(err, "insert intent fts batch "+strconv.Itoa(index))
			}
			return nil
		})
		if result.Error != nil {
			return trace.Wrap(result.Error, "load reasons")
		}
		return nil
	})
}

// rebuildIntentTableNodes reloads the intent index rows of the given nodes only,
// in chunks of scopedRebuildChunkSize so the IN clause stays within limits.
// @intent refresh only the requested nodes' reasons so incremental updates can avoid a full namespace rebuild.
func (s *SQLiteBackend) rebuildIntentTableNodes(ctx context.Context, db *gorm.DB, nodeIDs []uint) error {
	ns := requestctx.FromContext(ctx)
	return db.WithContext(ctx).Transaction(func(outerTx *gorm.DB) error {
		for start := 0; start < len(nodeIDs); start += scopedRebuildChunkSize {
			end := min(start+scopedRebuildChunkSize, len(nodeIDs))
			chunk := nodeIDs[start:end]
			if err := outerTx.Exec("DELETE FROM "+sqliteIntentFTSTable+" WHERE namespace = ? AND node_id IN ?", ns, chunk).Error; err != nil {
				return trace.Wrap(err, "clear scoped intent fts")
			}
			reasonsQ := outerTx.WithContext(ctx).Model(&graph.SearchReason{}).
				Where("namespace = ? AND node_id IN ?", ns, chunk).Order("id")
			var batch []graph.SearchReason
			result := reasonsQ.FindInBatches(&batch, sqliteFTSRebuildBatchSize, func(batchTx *gorm.DB, index int) error {
				if err := ctx.Err(); err != nil {
					return err
				}
				if err := insertSQLiteIntentBatch(ctx, batchTx, batch); err != nil {
					return trace.Wrap(err, "insert scoped intent fts batch "+strconv.Itoa(index))
				}
				return nil
			})
			if result.Error != nil {
				return trace.Wrap(result.Error, "load scoped reasons")
			}
		}
		return nil
	})
}

// ftsRow scans node_id values from search_fts MATCH queries.
// @intent decode the single-column FTS result before joining back to nodes.
type ftsRow struct {
	NodeID uint
}

// matchRows runs one FTS expression and returns the matching node ids in rank order.
//
// The keys after `rank` are what make the answer reproducible. bm25 ties often —
// short documents, repeated identifiers — and a tie with no further key falls
// back on storage order, which tracks the order rows were written and so tracks
// the order files happened to be walked. Because the LIMIT cuts after the sort,
// tied rows past it are not reordered but dropped, so the same repository indexed
// twice could answer differently. `n.id` last makes the key total.
//
// The key order here is deliberately NOT graph.CompareIdentity's, which compares
// file path before qualified name (and then kind, namespace and start line). The
// difference is not cosmetic: it decides which tied rows survive the LIMIT.
// File-path-first would cut on file boundaries more often, which is closer to what
// wire.FileGroup promises one layer up — that a file appearing in an answer appears
// whole.
//
// #106 gave rerank and intent scoring that one shared key; retrieval is the layer
// it left alone, because flipping these keys changes which rows the LIMIT keeps and
// so moves measured ranking numbers. Do not flip them on their own: every ranking
// number and every recapture figure recorded for #103 was measured with this order,
// so the flip needs a -capture-golden recapture and a re-judgment landing as its own
// change.
//
// @intent let Query run the same retrieval twice with a different expression.
func (s *SQLiteBackend) matchRows(ctx context.Context, db *gorm.DB, ftsQuery, ns string, limit int) ([]ftsRow, error) {
	var rows []ftsRow
	if err := db.WithContext(ctx).Raw(
		`SELECT CAST(search_fts.node_id AS INTEGER) AS node_id
		 FROM search_fts
		 JOIN nodes n ON n.id = CAST(search_fts.node_id AS INTEGER)
		 WHERE search_fts MATCH ? AND search_fts.namespace = ?
		 ORDER BY search_fts.rank, n.qualified_name, n.file_path, n.id
		 LIMIT ?`, ftsQuery, ns, limit).Scan(&rows).Error; err != nil {
		return nil, trace.Wrap(err, "fts query")
	}
	return rows, nil
}

// Query searches for related nodes using FTS5 MATCH queries.
//
// Every term is required, and SanitizeFTS5 decides what counts as a term. That
// pairing is the whole retrieval policy: requiring all terms is right when the
// searcher typed identifiers, and it only became wrong for sentences because
// ordinary English words were being required too.
//
// Widening to any-term when all-terms matches nothing was measured and
// rejected. It answered no query the narrow expression missed — the two extra
// nodes it retrieved never reached the top ten — and it filled the deliberate
// nonsense query in the golden set with fifty unrelated hits.
//
// @intent Converts the user's search term into a SQLite FTS prefix query to find nodes.
// @requires limit must be greater than 0 to get meaningful results.
// @return Returns a list of nodes sorted by FTS rank.
func (s *SQLiteBackend) Query(ctx context.Context, db *gorm.DB, query string, limit int) ([]graph.Node, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("limit must be > 0, got %d", limit)
	}
	ftsQuery := SanitizeFTS5(query)
	if ftsQuery == "" {
		return nil, nil
	}
	ns := requestctx.FromContext(ctx)

	rows, err := s.matchRows(ctx, db, ftsQuery, ns, limit)
	if err != nil {
		return nil, err
	}
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
// FTS5 could order these by bm25 and used to, but the ordering moved to
// intentrank so that this backend and the PostgreSQL one answer the same
// question the same way. What is left here is retrieval, which is what the index
// is for.
//
// The join onto nodes carries each candidate's identity — path, qualified name,
// kind, namespace, start line — because that is what intentrank breaks its score
// ties on. It also drops index rows whose node is gone, the same way matchRows
// does: an orphan cannot be scored and would otherwise spend a row of the
// candidate cap.
// @intent hand every candidate reason to shared scoring, with the identity that scoring breaks ties on.
// @requires maxCandidates must be greater than 0.
// @return returns unordered candidates with the exact text that was indexed for each.
func (s *SQLiteBackend) MatchIntent(ctx context.Context, db *gorm.DB, query string, maxCandidates int) ([]intentrank.Doc, error) {
	if maxCandidates <= 0 {
		return nil, fmt.Errorf("maxCandidates must be > 0, got %d", maxCandidates)
	}
	ftsQuery := SanitizeIntentFTS5(query)
	if ftsQuery == "" {
		return nil, nil
	}

	var docs []intentrank.Doc
	if err := db.WithContext(ctx).Raw(
		`SELECT CAST(intent_fts.node_id AS INTEGER) AS node_id, intent_fts.content,
		        n.file_path, n.qualified_name, n.kind, n.namespace, n.start_line
		 FROM intent_fts
		 JOIN nodes n ON n.id = CAST(intent_fts.node_id AS INTEGER)
		 WHERE intent_fts MATCH ? AND intent_fts.namespace = ?
		 LIMIT ?`, ftsQuery, requestctx.FromContext(ctx), maxCandidates).Scan(&docs).Error; err != nil {
		return nil, trace.Wrap(err, "intent fts query")
	}
	return docs, nil
}

// upgradeLegacyFTSTable migrates a pre-namespace search_fts schema to the
// current four-column layout (node_id, content, language, namespace). It builds
// a shadow table, populates it via rebuildTable, then swaps it into place using
// RENAME, keeping the old table as a backup until the swap succeeds.
// @intent upgrade legacy SQLite FTS storage to the namespace-aware schema without losing the indexed search snapshot.
func (s *SQLiteBackend) upgradeLegacyFTSTable(db *gorm.DB) error {
	for _, tableName := range []string{sqliteFTSUpgradeTable, sqliteFTSLegacyBackup} {
		if err := db.Exec(fmt.Sprintf("DROP TABLE IF EXISTS %s", tableName)).Error; err != nil {
			return trace.Wrap(err, "drop stale upgrade table")
		}
	}
	if err := createSQLiteFTSTable(db, sqliteFTSUpgradeTable, false); err != nil {
		if strings.Contains(err.Error(), "no such module: fts5") {
			return trace.Wrap(ErrFTS5NotAvailable, err.Error())
		}
		return trace.Wrap(err, "create upgraded fts shadow")
	}
	if err := s.rebuildTable(context.Background(), db, sqliteFTSUpgradeTable); err != nil {
		return trace.Wrap(err, "populate upgraded fts shadow")
	}
	if err := db.Exec(fmt.Sprintf("ALTER TABLE %s RENAME TO %s", sqliteFTSTable, sqliteFTSLegacyBackup)).Error; err != nil {
		return trace.Wrap(err, "rename legacy fts backup")
	}
	if err := db.Exec(fmt.Sprintf("ALTER TABLE %s RENAME TO %s", sqliteFTSUpgradeTable, sqliteFTSTable)).Error; err != nil {
		_ = db.Exec(fmt.Sprintf("ALTER TABLE %s RENAME TO %s", sqliteFTSLegacyBackup, sqliteFTSTable)).Error
		return trace.Wrap(err, "activate upgraded fts")
	}
	if err := db.Exec(fmt.Sprintf("DROP TABLE IF EXISTS %s", sqliteFTSLegacyBackup)).Error; err != nil {
		return trace.Wrap(err, "drop legacy fts backup")
	}
	return nil
}

// insertSQLiteFTSBatch executes one bulk INSERT for a batch of search documents into an FTS table.
// @intent push many rows in a single statement so rebuild paths avoid per-row round trips.
// @sideEffect inserts rows into the supplied FTS virtual table.
// @mutates search_fts virtual table contents
func insertSQLiteFTSBatch(ctx context.Context, tx *gorm.DB, tableName string, docs []graph.SearchDocument) error {
	if len(docs) == 0 {
		return nil
	}
	insertSQL, args := buildSQLiteFTSInsert(tableName, docs)
	return tx.WithContext(ctx).Exec(insertSQL, args...).Error
}

// insertSQLiteIntentBatch executes one bulk INSERT for a batch of recorded reasons.
// @intent push many reasons in a single statement so rebuild paths avoid per-row round trips.
// @sideEffect inserts rows into the intent_fts virtual table.
// @mutates intent_fts virtual table contents
func insertSQLiteIntentBatch(ctx context.Context, tx *gorm.DB, reasons []graph.SearchReason) error {
	insertSQL, args := buildSQLiteIntentInsert(reasons)
	if insertSQL == "" {
		return nil
	}
	return tx.WithContext(ctx).Exec(insertSQL, args...).Error
}

// buildSQLiteIntentInsert constructs the bulk INSERT for the intent index, one
// row per recorded reason, skipping any whose text is blank.
//
// Those skips are the feature. A reason with no text would otherwise occupy a
// row that can never match anything useful, and it would also lengthen the
// average document, quietly making every real reason score lower.
// @intent keep the intent index limited to reasons that were actually written down.
func buildSQLiteIntentInsert(reasons []graph.SearchReason) (string, []any) {
	placeholders := make([]string, 0, len(reasons))
	args := make([]any, 0, len(reasons)*3)
	for _, reason := range reasons {
		if strings.TrimSpace(reason.Content) == "" {
			continue
		}
		placeholders = append(placeholders, "(?, ?, ?)")
		args = append(args, reason.NodeID, reason.Content, reason.Namespace)
	}
	if len(placeholders) == 0 {
		return "", nil
	}
	insertSQL := fmt.Sprintf(
		"INSERT INTO %s(node_id, content, namespace) VALUES %s",
		sqliteIntentFTSTable,
		strings.Join(placeholders, ", "),
	)
	return insertSQL, args
}

// createSQLiteIntentFTSTable issues the CREATE VIRTUAL TABLE DDL for the intent index.
//
// It carries no `language` column. Language is a fact about the file, and adding
// it here would let a query word match something other than the recorded reason,
// which is exactly what this table exists to prevent.
// @intent create an FTS5 table whose only indexed text is the reason a node exists.
func createSQLiteIntentFTSTable(db *gorm.DB) error {
	stmt := fmt.Sprintf(`
		CREATE VIRTUAL TABLE IF NOT EXISTS %s
		USING fts5(node_id UNINDEXED, content, namespace UNINDEXED)
	`, sqliteIntentFTSTable)
	return db.Exec(stmt).Error
}

// buildSQLiteFTSInsert constructs a bulk INSERT statement for the FTS virtual
// table, returning the SQL string and its positional arguments. Each document
// maps to a (node_id, content, language, namespace) value row.
// @intent batch SQLite FTS inserts into one statement so rebuild paths can stream many documents with minimal per-row overhead.
func buildSQLiteFTSInsert(tableName string, docs []graph.SearchDocument) (string, []any) {
	if len(docs) == 0 {
		return "", nil
	}
	placeholders := make([]string, len(docs))
	args := make([]any, 0, len(docs)*4)
	for i, doc := range docs {
		placeholders[i] = "(?, ?, ?, ?)"
		args = append(args, doc.NodeID, doc.Content, doc.Language, doc.Namespace)
	}
	insertSQL := fmt.Sprintf(
		"INSERT INTO %s(node_id, content, language, namespace) VALUES %s",
		tableName,
		strings.Join(placeholders, ", "),
	)
	return insertSQL, args
}

// sqliteColumnExists reports whether a column is present on a given SQLite table via PRAGMA table_info.
// @intent gate schema migrations on actual table layout instead of guessing from version markers.
func sqliteColumnExists(db *gorm.DB, tableName, columnName string) (bool, error) {
	rows, err := db.Raw("PRAGMA table_info(" + tableName + ")").Rows()
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return false, err
		}
		if name == columnName {
			return true, nil
		}
	}
	return false, rows.Err()
}

// createSQLiteFTSTable issues a CREATE VIRTUAL TABLE … USING fts5 DDL for the
// given tableName. When ifNotExists is true the statement is idempotent; when
// false it is used for the upgrade shadow table where a fresh schema is required.
// @intent create the namespace-aware SQLite FTS table shape used by both first-run migration and legacy upgrade flows.
func createSQLiteFTSTable(db *gorm.DB, tableName string, ifNotExists bool) error {
	clause := ""
	if ifNotExists {
		clause = "IF NOT EXISTS "
	}
	stmt := fmt.Sprintf(`
		CREATE VIRTUAL TABLE %s%s
		USING fts5(node_id UNINDEXED, content, language, namespace UNINDEXED)
	`, clause, tableName)
	return db.Exec(stmt).Error
}

// sqliteTableExists reports whether a regular table with the given name exists in sqlite_master.
// @intent let migration code branch on table presence without depending on GORM AutoMigrate side effects.
func sqliteTableExists(db *gorm.DB, tableName string) (bool, error) {
	var count int64
	if err := db.Raw("SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?", tableName).Scan(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}
