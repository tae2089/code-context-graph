package search

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"

	"gorm.io/gorm"

	"github.com/tae2089/trace"

	"github.com/tae2089/code-context-graph/internal/ctxns"
	"github.com/tae2089/code-context-graph/internal/model"
)

const (
	sqliteFTSTable            = "search_fts"
	sqliteFTSUpgradeTable     = "search_fts_upgrade"
	sqliteFTSLegacyBackup     = "search_fts_legacy_backup"
	sqliteFTSRebuildBatchSize = 500
	scopedRebuildChunkSize    = 400
)

// SQLiteBackend는 SQLite FTS5 기반 검색 백엔드다.
// @intent SQLite 환경에서 전문 검색 색인 구축과 질의를 처리한다.
type SQLiteBackend struct {
	batchInserter func(ctx context.Context, tx *gorm.DB, tableName string, docs []model.SearchDocument) error
}

// NewSQLiteBackend는 SQLite 검색 백엔드를 생성한다.
// @intent SQLite 전용 Backend 구현체를 제공한다.
func NewSQLiteBackend() *SQLiteBackend {
	return &SQLiteBackend{batchInserter: insertSQLiteFTSBatch}
}

// Migrate는 SQLite FTS5 가상 테이블을 준비한다.
// @intent SQLite 검색용 전문 색인 테이블을 생성한다.
// @sideEffect search_fts 가상 테이블을 생성할 수 있다.
// @ensures FTS5를 사용할 수 있으면 search_fts가 존재한다.
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
		return nil
	})
}

// Rebuild는 search_documents 내용을 FTS 색인으로 다시 적재한다.
// @intent 저장된 검색 문서를 SQLite FTS 색인과 동기화한다.
// @sideEffect search_fts 내용을 삭제하고 다시 삽입한다.
// @domainRule 색인 내용은 search_documents의 현재 스냅샷과 일치해야 한다.
func (s *SQLiteBackend) Rebuild(ctx context.Context, db *gorm.DB) error {
	return s.rebuildTable(ctx, db, sqliteFTSTable)
}

// RebuildNodes는 지정된 노드의 FTS 행만 search_documents와 동기화한다.
// @intent incremental update 경로에서 전체 namespace FTS 재적재를 피한다.
func (s *SQLiteBackend) RebuildNodes(ctx context.Context, db *gorm.DB, nodeIDs []uint) error {
	if len(nodeIDs) == 0 {
		return nil
	}
	return s.rebuildTableNodes(ctx, db, sqliteFTSTable, nodeIDs)
}

// PurgeNamespace는 특정 namespace의 FTS 물리 인덱스를 제거한다.
// @intent workspace 삭제 등 rebuild 없는 경로에서도 stale FTS row를 정리한다.
func (s *SQLiteBackend) PurgeNamespace(ctx context.Context, db *gorm.DB) error {
	exists, err := sqliteTableExists(db, sqliteFTSTable)
	if err != nil {
		return trace.Wrap(err, "check fts table before purge")
	}
	if !exists {
		return nil
	}
	return db.WithContext(ctx).Exec("DELETE FROM "+sqliteFTSTable+" WHERE namespace = ?", ctxns.FromContext(ctx)).Error
}

// rebuildTable clears all FTS rows for the current namespace in tableName and
// repopulates them from search_documents in batches. Used by both full Rebuild
// and the legacy-upgrade path.
// @intent resynchronize one namespace-scoped SQLite FTS table from persisted search documents without disturbing other namespaces.
func (s *SQLiteBackend) rebuildTable(ctx context.Context, db *gorm.DB, tableName string) error {
	ns := ctxns.FromContext(ctx)
	clearSQL := fmt.Sprintf("DELETE FROM %s WHERE namespace = ?", tableName)
	clearArgs := []any{ns}
	return db.WithContext(ctx).Transaction(func(outerTx *gorm.DB) error {
		if err := outerTx.Exec(clearSQL, clearArgs...).Error; err != nil {
			return trace.Wrap(err, "clear fts")
		}

		docsQ := outerTx.WithContext(ctx).Where("namespace = ?", ns)

		var batchDocs []model.SearchDocument
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
	ns := ctxns.FromContext(ctx)
	return db.WithContext(ctx).Transaction(func(outerTx *gorm.DB) error {
		for start := 0; start < len(nodeIDs); start += scopedRebuildChunkSize {
			end := min(start+scopedRebuildChunkSize, len(nodeIDs))
			chunk := nodeIDs[start:end]
			if err := outerTx.Exec("DELETE FROM "+tableName+" WHERE namespace = ? AND node_id IN ?", ns, chunk).Error; err != nil {
				return trace.Wrap(err, "clear scoped fts")
			}

			docsQ := outerTx.WithContext(ctx).Where("namespace = ? AND node_id IN ?", ns, chunk)
			var batchDocs []model.SearchDocument
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

// insertSQLiteFTSBatch executes one bulk INSERT for a batch of search documents into an FTS table.
// @intent push many rows in a single statement so rebuild paths avoid per-row round trips.
// @sideEffect inserts rows into the supplied FTS virtual table.
// @mutates search_fts virtual table contents
func insertSQLiteFTSBatch(ctx context.Context, tx *gorm.DB, tableName string, docs []model.SearchDocument) error {
	if len(docs) == 0 {
		return nil
	}
	insertSQL, args := buildSQLiteFTSInsert(tableName, docs)
	return tx.WithContext(ctx).Exec(insertSQL, args...).Error
}

// buildSQLiteFTSInsert constructs a bulk INSERT statement for the FTS virtual
// table, returning the SQL string and its positional arguments. Each document
// maps to a (node_id, content, language, namespace) value row.
// @intent batch SQLite FTS inserts into one statement so rebuild paths can stream many documents with minimal per-row overhead.
func buildSQLiteFTSInsert(tableName string, docs []model.SearchDocument) (string, []any) {
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

// ftsRow scans node_id values from search_fts MATCH queries.
// @intent decode the single-column FTS result before joining back to nodes.
type ftsRow struct {
	NodeID uint
}

// Query는 FTS5 MATCH 질의로 관련 노드를 검색한다.
// @intent 사용자 검색어를 SQLite FTS prefix 질의로 변환해 노드를 찾는다.
// @requires limit는 0보다 커야 의미 있는 결과를 얻는다.
// @return FTS 순위 순서를 유지한 노드 목록을 반환한다.
func (s *SQLiteBackend) Query(ctx context.Context, db *gorm.DB, query string, limit int) ([]model.Node, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("limit must be > 0, got %d", limit)
	}
	ftsQuery := SanitizeFTS5(query)
	if ftsQuery == "" {
		return nil, nil
	}
	ns := ctxns.FromContext(ctx)

	var rows []ftsRow
	querySQL := `SELECT CAST(node_id AS INTEGER) AS node_id
		 FROM search_fts
		 WHERE search_fts MATCH ? AND namespace = ?`
	args := []any{ftsQuery, ns}
	querySQL += ` ORDER BY rank LIMIT ?`
	args = append(args, limit)
	if err := db.WithContext(ctx).Raw(querySQL, args...).Scan(&rows).Error; err != nil {
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

	return result, nil
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

// sqliteTableExists reports whether a regular table with the given name exists in sqlite_master.
// @intent let migration code branch on table presence without depending on GORM AutoMigrate side effects.
func sqliteTableExists(db *gorm.DB, tableName string) (bool, error) {
	var count int64
	if err := db.Raw("SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?", tableName).Scan(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

var _ Backend = (*SQLiteBackend)(nil)
