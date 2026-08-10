//go:build fts5 && postgres

package searchsql

import (
	"testing"

	requestctx "github.com/tae2089/code-context-graph/internal/ctx"
	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

// TestQueryIntentPostgres_CoverageCountsDeclarationsNotReasons is the PostgreSQL
// twin of the SQLite test of the same name. The counting is one GORM query per
// number, so the thing worth checking on the second backend is that the distinct
// count really is distinct there too — a repository's coverage must not depend on
// which database it is stored in.
func TestQueryIntentPostgres_CoverageCountsDeclarationsNotReasons(t *testing.T) {
	db := setupPostgresDB(t)
	seedReasoned(t, db, "one", graph.DocTag{Kind: graph.TagIntent, Value: "keep the queue draining under backpressure"})
	seedReasoned(t, db, "three",
		graph.DocTag{Kind: graph.TagIntent, Value: "decide which push may trigger a build"},
		graph.DocTag{Kind: graph.TagDomainRule, Value: "only allowlisted repositories are admitted"},
		graph.DocTag{Kind: graph.TagDomainRule, Value: "a tag push never starts a build"},
	)
	seedReasoned(t, db, "silent")
	backend := NewPostgresBackend()
	ctx := requestctx.WithNamespace(t.Context(), requestctx.DefaultNamespace)
	if _, err := RefreshSearchDocuments(ctx, db); err != nil {
		t.Fatalf("RefreshSearchDocuments: %v", err)
	}
	if err := backend.Rebuild(ctx, db); err != nil {
		t.Fatalf("Rebuild: %v", err)
	}

	result, err := NewReader(db, backend).QueryIntent(ctx, "what keeps the queue draining", 10)
	if err != nil {
		t.Fatalf("QueryIntent: %v", err)
	}
	if result.Coverage.WithReason != 2 {
		t.Errorf("WithReason = %d, want 2 declarations — four reasons live on two of them", result.Coverage.WithReason)
	}
	if result.Coverage.Declarations != 3 {
		t.Errorf("Declarations = %d, want 3", result.Coverage.Declarations)
	}
}
