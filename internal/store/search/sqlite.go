package search

import (
	"context"
	"strings"

	"gorm.io/gorm"

	"github.com/tae2089/trace"

	"github.com/tae2089/code-context-graph/internal/ctxns"
	"github.com/tae2089/code-context-graph/internal/model"
)

// SQLiteBackend는 SQLite FTS5 기반 검색 백엔드다.
// @intent SQLite 환경에서 전문 검색 색인 구축과 질의를 처리한다.
type SQLiteBackend struct{}

// NewSQLiteBackend는 SQLite 검색 백엔드를 생성한다.
// @intent SQLite 전용 Backend 구현체를 제공한다.
func NewSQLiteBackend() *SQLiteBackend {
	return &SQLiteBackend{}
}

// Migrate는 SQLite FTS5 가상 테이블을 준비한다.
// @intent SQLite 검색용 전문 색인 테이블을 생성한다.
// @sideEffect search_fts 가상 테이블을 생성할 수 있다.
// @ensures FTS5를 사용할 수 있으면 search_fts가 존재한다.
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

// Rebuild는 search_documents 내용을 FTS 색인으로 다시 적재한다.
// @intent 저장된 검색 문서를 SQLite FTS 색인과 동기화한다.
// @sideEffect search_fts 내용을 삭제하고 다시 삽입한다.
// @domainRule 색인 내용은 search_documents의 현재 스냅샷과 일치해야 한다.
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

// Query는 FTS5 MATCH 질의로 관련 노드를 검색한다.
// @intent 사용자 검색어를 SQLite FTS prefix 질의로 변환해 노드를 찾는다.
// @requires limit는 0보다 커야 의미 있는 결과를 얻는다.
// @return FTS 순위 순서를 유지한 노드 목록을 반환한다.
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
	nodesQ := db.WithContext(ctx).Where("id IN ?", nodeIDs)
	if ns := ctxns.FromContext(ctx); ns != "" {
		nodesQ = nodesQ.Where("namespace = ?", ns)
	}
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

var _ Backend = (*SQLiteBackend)(nil)
