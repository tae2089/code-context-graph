// @index GORM unit-of-work adapter for atomic graph and derived search updates.
package graphgorm

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"github.com/tae2089/code-context-graph/internal/app/ingest"
)

// SearchWriterFactory binds a search writer to the active GORM transaction.
// @intent construct derived-state persistence with the same transaction handle as graph persistence.
type SearchWriterFactory func(tx *gorm.DB) ingest.SearchWriter

// UnitOfWork implements the ingest atomicity boundary with GORM transactions.
// @intent coordinate graph and search writes without exposing GORM to application policy.
type UnitOfWork struct {
	db              *gorm.DB
	newSearchWriter SearchWriterFactory
}

var _ ingest.UnitOfWork = (*UnitOfWork)(nil)

// NewUnitOfWork constructs a GORM-backed ingest unit of work.
// @intent inject the database transaction owner and transaction-scoped search writer factory.
func NewUnitOfWork(db *gorm.DB, newSearchWriter SearchWriterFactory) *UnitOfWork {
	return &UnitOfWork{db: db, newSearchWriter: newSearchWriter}
}

// transaction exposes only the application capabilities bound to one GORM transaction.
// @intent keep the shared transaction handle private while satisfying the ingest transaction port.
type transaction struct {
	graph  ingest.GraphStore
	search ingest.SearchWriter
}

var _ ingest.Transaction = transaction{}

// Graph returns graph persistence bound to this transaction.
// @intent supply transaction-scoped graph operations to the ingest callback.
func (tx transaction) Graph() ingest.GraphStore { return tx.graph }

// Search returns derived-search persistence bound to this transaction.
// @intent supply transaction-scoped search operations to the ingest callback.
func (tx transaction) Search() ingest.SearchWriter { return tx.search }

// WithinTransaction executes graph and search work against one GORM transaction.
// @intent commit graph and derived search state together or roll both back on any callback error.
// @sideEffect starts a database transaction and commits or rolls it back.
// @ensures the callback is not invoked unless both transaction capabilities are available.
func (u *UnitOfWork) WithinTransaction(ctx context.Context, fn func(ingest.Transaction) error) error {
	if u == nil || u.db == nil {
		return errors.New("graphgorm unit of work requires a database")
	}
	if u.newSearchWriter == nil {
		return errors.New("graphgorm unit of work requires a search writer factory")
	}
	return u.db.WithContext(ctx).Transaction(func(txDB *gorm.DB) error {
		searchWriter := u.newSearchWriter(txDB)
		if searchWriter == nil {
			return errors.New("graphgorm search writer factory returned nil")
		}
		return fn(transaction{
			graph:  New(txDB),
			search: searchWriter,
		})
	})
}
