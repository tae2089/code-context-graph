// @index Shared search backend interface and errors for SQLite FTS5 and PostgreSQL tsvector implementations.
package search

import (
	"context"

	"gorm.io/gorm"

	"github.com/tae2089/trace"

	"github.com/tae2089/code-context-graph/internal/model"
)

// ErrFTS5NotAvailable indicates the SQLite build lacks the fts5 extension.
var ErrFTS5NotAvailable = trace.New("fts5 module not available")

// Backend defines the full-text search backend contract.
// @intent provide one interface for backend-specific search index migration, rebuild, and query operations.
type Backend interface {
	// @intent prepare search tables and indexes for the active database driver.
	// @sideEffect may create or update search index schema objects.
	Migrate(db *gorm.DB) error
	// @intent refresh backend-specific full-text index state from the current persisted search documents for the active namespace.
	// @requires db must be a valid connection for processing the active namespace.
	// @sideEffect rewrites backend-specific search index records or derived vectors.
	Rebuild(ctx context.Context, db *gorm.DB) error
	// @intent reindex only changed nodes so incremental updates cost less than full rebuilds.
	// @param nodeIDs is the set of node IDs to reindex.
	// @sideEffect updates search index records for the specified nodes.
	RebuildNodes(ctx context.Context, db *gorm.DB, nodeIDs []uint) error
	// @intent remove or reconcile backend-specific search index state for the active namespace when physical cleanup is required.
	// @sideEffect may clear namespace-scoped search index records, though implementations may intentionally no-op.
	PurgeNamespace(ctx context.Context, db *gorm.DB) error
	// @intent execute a user query using the backend-specific full-text search syntax.
	// @param query is the raw query string to search for.
	// @param limit is the maximum number of results to return.
	// @return returns nodes ordered by relevance.
	Query(ctx context.Context, db *gorm.DB, query string, limit int) ([]model.Node, error)
}
