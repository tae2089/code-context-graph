package search

import (
	"context"
	"fmt"

	"gorm.io/gorm"

	"github.com/tae2089/trace"

	"github.com/tae2089/code-context-graph/internal/ctxns"
	"github.com/tae2089/code-context-graph/internal/model"
)

// PostgresBackend는 PostgreSQL tsvector 기반 검색 백엔드다.
// @intent PostgreSQL 환경에서 전문 검색 색인 구축과 질의를 처리한다.
type PostgresBackend struct{}

// NewPostgresBackend는 PostgreSQL 검색 백엔드를 생성한다.
// @intent PostgreSQL 전용 Backend 구현체를 제공한다.
func NewPostgresBackend() *PostgresBackend {
	return &PostgresBackend{}
}

// Migrate는 PostgreSQL 전문 검색 스키마를 준비한다.
// @intent search_documents 테이블에 tsvector 기반 검색 인프라를 구성한다.
// @sideEffect 컬럼, 인덱스, 트리거 함수, 트리거를 생성 또는 교체한다.
// @ensures search_documents 변경 시 tsv가 자동으로 갱신된다.
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

	return nil
}

// Rebuild는 모든 검색 문서의 tsvector를 다시 계산한다.
// @intent 기존 search_documents 행의 전문 검색 색인을 일괄 재생성한다.
// @sideEffect search_documents.tsv 값을 갱신한다.
func (p *PostgresBackend) Rebuild(ctx context.Context, db *gorm.DB) error {
	ns := ctxns.FromContext(ctx)
	query := `
		UPDATE search_documents
		SET tsv = to_tsvector('simple', COALESCE(content, ''))
		WHERE namespace = ?`
	args := []any{ns}
	return db.WithContext(ctx).Exec(query, args...).Error
}

// RebuildNodes는 지정된 노드의 tsvector만 다시 계산한다.
// @intent incremental update 경로에서 namespace 전체 tsv 갱신을 피한다.
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

// PurgeNamespace는 PostgreSQL search_documents 삭제 이후 별도 물리 정리가 필요 없으므로 no-op이다.
// @intent Backend 인터페이스를 맞추고 workspace purge 경로를 일관되게 유지한다.
func (p *PostgresBackend) PurgeNamespace(ctx context.Context, db *gorm.DB) error {
	return nil
}

// resultRow scans node_id values from PostgreSQL tsquery matches.
// @intent decode the single-column tsquery result before joining back to nodes.
type resultRow struct {
	NodeID uint
}

// Query는 PostgreSQL tsquery로 관련 노드를 검색한다.
// @intent 사용자 검색어를 prefix tsquery로 변환해 관련 노드를 찾는다.
// @requires limit는 0보다 커야 의미 있는 결과를 얻는다.
// @return ts_rank 기준 정렬 순서를 유지한 노드 목록을 반환한다.
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

var _ Backend = (*PostgresBackend)(nil)
