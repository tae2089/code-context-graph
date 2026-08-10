//go:build postgres

package searchsql

import (
	"context"
	"testing"

	"gorm.io/gorm"

	requestctx "github.com/tae2089/code-context-graph/internal/ctx"
)

// setupPostgresRankOrder seeds the rank-order ladder into a fresh PostgreSQL
// database and hands back what the tests need to query it.
func setupPostgresRankOrder(t *testing.T) (context.Context, *gorm.DB, *PostgresBackend) {
	t.Helper()
	db := setupPostgresDB(t)
	backend := NewPostgresBackend()
	if err := backend.Migrate(db); err != nil {
		t.Fatal(err)
	}
	ctx := requestctx.WithNamespace(context.Background(), rankOrderNamespace)
	seedRankOrderCorpus(t, ctx, db, backend)
	return ctx, db, backend
}

// TestPostgresFTS_Query_ReturnsBestMatchesFirst asks for the whole ladder and
// requires it back best first.
//
// This is the hole issue #68 was filed for: matchRows' `ts_rank(...) DESC` could
// be turned into `ASC` — worst match first — and the entire suite still passed.
// See rankorder_fixture_test.go for why term frequency is the signal being
// ordered on and why that outlives any one ranking function.
func TestPostgresFTS_Query_ReturnsBestMatchesFirst(t *testing.T) {
	ctx, db, backend := setupPostgresRankOrder(t)

	got := queryRankOrder(t, ctx, db, backend, rankOrderDocs)

	requireRankOrder(t, got, rankOrderExpected(rankOrderDocs))
}

// TestPostgresFTS_Query_LimitKeepsTheBestMatches asks for fewer rows than match,
// which is the case that actually costs an answer.
//
// The order is applied before the LIMIT, so reversing it does not just shuffle
// the reply — the four worst matches fill the quota and the six best never leave
// the database. Whatever reranks downstream cannot recover a row it was never
// given.
func TestPostgresFTS_Query_LimitKeepsTheBestMatches(t *testing.T) {
	ctx, db, backend := setupPostgresRankOrder(t)

	got := queryRankOrder(t, ctx, db, backend, rankOrderLimit)

	if len(got) != rankOrderLimit {
		t.Fatalf("expected the limit to be filled, got %d rows: %v", len(got), got)
	}
	requireRankOrder(t, got, rankOrderExpected(rankOrderLimit))
}
