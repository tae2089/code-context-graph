package search

import (
	"context"

	"gorm.io/gorm"

	"github.com/tae2089/trace"

	"github.com/tae2089/code-context-graph/internal/model"
)

// ErrFTS5NotAvailable indicates the SQLite build lacks the fts5 extension.
var ErrFTS5NotAvailable = trace.New("fts5 module not available")

// Backend는 전문 검색 백엔드 계약을 정의한다.
// @intent DB별 검색 색인 생성, 재구축, 질의를 공통 인터페이스로 제공한다.
type Backend interface {
	Migrate(db *gorm.DB) error
	Rebuild(ctx context.Context, db *gorm.DB) error
	Query(ctx context.Context, db *gorm.DB, query string, limit int) ([]model.Node, error)
}
