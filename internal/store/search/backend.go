package search

import (
	"context"

	"gorm.io/gorm"

	"github.com/tae2089/trace"

	"github.com/imtaebin/code-context-graph/internal/model"
)

// ErrFTS5NotAvailable indicates the SQLite build lacks the fts5 extension.
var ErrFTS5NotAvailable = trace.New("fts5 module not available")

type Backend interface {
	Migrate(db *gorm.DB) error
	Rebuild(ctx context.Context, db *gorm.DB) error
	Query(ctx context.Context, db *gorm.DB, query string, limit int) ([]model.Node, error)
}
