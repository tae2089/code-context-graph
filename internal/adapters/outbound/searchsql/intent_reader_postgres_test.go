//go:build fts5 && postgres

package searchsql

import (
	"testing"

	requestctx "github.com/tae2089/code-context-graph/internal/ctx"
)

// TestQueryIntentPostgres_OrdersTiedReasonsTheSameWayAtEveryLimit is the
// PostgreSQL twin of the SQLite test of the same name. It is the backend that
// actually reshuffled: `ts_rank` scores are coarse enough that nine recorded
// reasons landed on exactly 0.020264236 for one golden question, and changing
// the limit changed which of them came first.
func TestQueryIntentPostgres_OrdersTiedReasonsTheSameWayAtEveryLimit(t *testing.T) {
	db := setupPostgresDB(t)
	seedTiedIntentFixture(t, db, 12)
	backend := NewPostgresBackend()
	ctx := requestctx.WithNamespace(t.Context(), requestctx.DefaultNamespace)
	if _, err := RefreshSearchDocuments(ctx, db); err != nil {
		t.Fatalf("RefreshSearchDocuments: %v", err)
	}
	if err := backend.Rebuild(ctx, db); err != nil {
		t.Fatalf("Rebuild: %v", err)
	}

	reader := NewReader(db, backend)
	shortResult, err := reader.QueryIntent(ctx, "what keeps the queue draining", 4)
	if err != nil {
		t.Fatalf("QueryIntent: %v", err)
	}
	longResult, err := reader.QueryIntent(ctx, "what keeps the queue draining", 12)
	if err != nil {
		t.Fatalf("QueryIntent: %v", err)
	}
	short, long := answeringNodes(shortResult), answeringNodes(longResult)
	if len(short) != 4 || len(long) != 12 {
		t.Fatalf("got %d and %d nodes, want 4 and 12", len(short), len(long))
	}
	for i, node := range short {
		if long[i].ID != node.ID {
			t.Fatalf("row %d is node %d at limit 4 but node %d at limit 12", i, node.ID, long[i].ID)
		}
	}
}
