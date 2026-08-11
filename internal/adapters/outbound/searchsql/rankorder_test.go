//go:build fts5

package searchsql

import (
	"context"
	"slices"
	"strings"
	"testing"

	"gorm.io/gorm"

	requestctx "github.com/tae2089/code-context-graph/internal/ctx"
)

// setupSQLiteRankOrder seeds the rank-order ladder into a fresh SQLite FTS5
// database and hands back what the tests need to query it.
func setupSQLiteRankOrder(t *testing.T) (context.Context, *gorm.DB, *SQLiteBackend) {
	t.Helper()
	db := setupTestDB(t)
	backend := NewSQLiteBackend()
	if err := backend.Migrate(db); err != nil {
		t.Fatal(err)
	}
	ctx := requestctx.WithNamespace(context.Background(), rankOrderNamespace)
	seedRankOrderCorpus(t, ctx, db, backend)
	return ctx, db, backend
}

// TestSQLiteFTS_Query_ReturnsBestMatchesFirst asks for the whole ladder and
// requires it back best first.
//
// Nothing checked this before. The backend's `ORDER BY rank` could be reversed —
// worst match first — and every test in the suite stayed green, because they all
// asked which nodes came back and none asked in what order. See
// rankorder_fixture_test.go for why term frequency is the signal being ordered
// on and why that outlives any one ranking function.
func TestSQLiteFTS_Query_ReturnsBestMatchesFirst(t *testing.T) {
	ctx, db, backend := setupSQLiteRankOrder(t)

	got := queryRankOrder(t, ctx, db, backend, rankOrderDocs)

	requireRankOrder(t, got, rankOrderExpected(rankOrderDocs))
}

// TestSQLiteFTS_Query_LimitKeepsTheBestMatches asks for fewer rows than match,
// which is the case that actually costs an answer.
//
// The order is applied before the LIMIT, so reversing it does not just shuffle
// the reply — the four worst matches fill the quota and the six best never leave
// the database. Whatever reranks downstream cannot recover a row it was never
// given.
func TestSQLiteFTS_Query_LimitKeepsTheBestMatches(t *testing.T) {
	ctx, db, backend := setupSQLiteRankOrder(t)

	got := queryRankOrder(t, ctx, db, backend, rankOrderLimit)

	if len(got) != rankOrderLimit {
		t.Fatalf("expected the limit to be filled, got %d rows: %v", len(got), got)
	}
	requireRankOrder(t, got, rankOrderExpected(rankOrderLimit))
}

// setupSQLiteTies seeds the tied documents into a fresh SQLite FTS5 database in
// the given order.
func setupSQLiteTies(t *testing.T, docs []rankOrderDoc) (context.Context, *gorm.DB, *SQLiteBackend) {
	t.Helper()
	db := setupTestDB(t)
	backend := NewSQLiteBackend()
	if err := backend.Migrate(db); err != nil {
		t.Fatal(err)
	}
	ctx := requestctx.WithNamespace(context.Background(), rankOrderNamespace)
	seedTieCorpus(t, ctx, db, backend, docs)
	return ctx, db, backend
}

// TestSQLiteFTS_Query_TiesDoNotDependOnInsertionOrder indexes one corpus twice,
// forwards and backwards, and requires the same answer from both.
//
// Nothing but insertion order differs between the two databases, and insertion
// order is not something a user of the tool controls or can see. It is decided
// by the order files were walked. So an answer that moves with it means indexing
// the same unchanged repository a second time can return a different result —
// and because the LIMIT cuts before anything downstream reranks, rows do not
// merely move, they disappear.
func TestSQLiteFTS_Query_TiesDoNotDependOnInsertionOrder(t *testing.T) {
	forward := rankOrderTies()
	backward := slices.Clone(forward)
	slices.Reverse(backward)

	ctxA, dbA, backendA := setupSQLiteTies(t, forward)
	ctxB, dbB, backendB := setupSQLiteTies(t, backward)

	got := queryRankOrder(t, ctxA, dbA, backendA, rankOrderTieLimit)
	reversed := queryRankOrder(t, ctxB, dbB, backendB, rankOrderTieLimit)

	if !slices.Equal(got, reversed) {
		t.Errorf("the same corpus indexed in a different order answered differently\n  forward-seeded:  %s\n  backward-seeded: %s",
			strings.Join(got, " "), strings.Join(reversed, " "))
	}
	requireRankOrder(t, got, rankOrderTieExpected(rankOrderTieLimit))
}
