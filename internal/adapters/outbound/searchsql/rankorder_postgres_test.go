//go:build postgres

package searchsql

import (
	"context"
	"slices"
	"strings"
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

// setupPostgresTies seeds the tied documents into a fresh PostgreSQL database in
// the given order.
func setupPostgresTies(t *testing.T, docs []rankOrderDoc) (context.Context, *gorm.DB, *PostgresBackend) {
	t.Helper()
	db := setupPostgresDB(t)
	backend := NewPostgresBackend()
	if err := backend.Migrate(db); err != nil {
		t.Fatal(err)
	}
	ctx := requestctx.WithNamespace(context.Background(), rankOrderNamespace)
	seedTieCorpus(t, ctx, db, backend, docs)
	return ctx, db, backend
}

// TestPostgresFTS_Query_TiesDoNotDependOnInsertionOrder is the SQLite test's
// twin, and the pair is the point. Both backends must break a tie the same way,
// or the two deployments answer the same question differently — which is the
// promise TestBackendParity_SearchAnswersAreIdentical makes. ts_rank and bm25
// tie on different rows, but once either has tied, what happens next is ours to
// decide and has to match.
func TestPostgresFTS_Query_TiesDoNotDependOnInsertionOrder(t *testing.T) {
	forward := rankOrderTies()
	backward := slices.Clone(forward)
	slices.Reverse(backward)

	ctxA, dbA, backendA := setupPostgresTies(t, forward)
	ctxB, dbB, backendB := setupPostgresTies(t, backward)

	got := queryRankOrder(t, ctxA, dbA, backendA, rankOrderTieLimit)
	reversed := queryRankOrder(t, ctxB, dbB, backendB, rankOrderTieLimit)

	if !slices.Equal(got, reversed) {
		t.Errorf("the same corpus indexed in a different order answered differently\n  forward-seeded:  %s\n  backward-seeded: %s",
			strings.Join(got, " "), strings.Join(reversed, " "))
	}
	requireRankOrder(t, got, rankOrderTieExpected(rankOrderTieLimit))
}
