//go:build fts5

package searchsql

import (
	"testing"

	requestctx "github.com/tae2089/code-context-graph/internal/ctx"
	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

// Coverage is a fraction of declarations, never of reasons. One reason is one
// document, so a node whose author wrote three of them is three index rows —
// counting rows would report that node three times and claim a repository is
// better annotated than it is.
func TestQueryIntent_CoverageCountsDeclarationsNotReasons(t *testing.T) {
	db := setupTestDB(t)
	seedReasoned(t, db, "one", graph.DocTag{Kind: graph.TagIntent, Value: "keep the queue draining under backpressure"})
	seedReasoned(t, db, "three",
		graph.DocTag{Kind: graph.TagIntent, Value: "decide which push may trigger a build"},
		graph.DocTag{Kind: graph.TagDomainRule, Value: "only allowlisted repositories are admitted"},
		graph.DocTag{Kind: graph.TagDomainRule, Value: "a tag push never starts a build"},
	)
	seedReasoned(t, db, "silent")
	backend := buildIntentIndex(t, db)
	ctx := requestctx.WithNamespace(t.Context(), requestctx.DefaultNamespace)

	result, err := NewReader(db, backend).QueryIntent(ctx, "what keeps the queue draining", 10)
	if err != nil {
		t.Fatalf("QueryIntent: %v", err)
	}
	// Four reasons live on two declarations, and a third declaration wrote none.
	if result.Coverage.WithReason != 2 {
		t.Errorf("WithReason = %d, want 2 declarations — four reasons live on two of them", result.Coverage.WithReason)
	}
	if result.Coverage.Declarations != 3 {
		t.Errorf("Declarations = %d, want 3", result.Coverage.Declarations)
	}
}

// The empty answer is the one that needs coverage most: without it, a repository
// nobody annotated and a repository whose reasons simply do not cover this
// question come back as the same nothing.
func TestQueryIntent_ReportsCoverageWhenNoReasonMatches(t *testing.T) {
	db := setupTestDB(t)
	seedReasoned(t, db, "signatureFormatter")
	seedReasoned(t, db, "drainQueue")
	buildIntentIndex(t, db)
	ctx := requestctx.WithNamespace(t.Context(), requestctx.DefaultNamespace)

	result, err := NewReader(db, NewSQLiteBackend()).QueryIntent(ctx, "how does the scheduler pick a leader", 10)
	if err != nil {
		t.Fatalf("QueryIntent: %v", err)
	}
	if len(result.Hits) != 0 {
		t.Fatalf("expected no hits, got %d", len(result.Hits))
	}
	if result.Coverage.WithReason != 0 {
		t.Errorf("WithReason = %d, want 0 — nothing was annotated", result.Coverage.WithReason)
	}
	if result.Coverage.Declarations != 2 {
		t.Errorf("Declarations = %d, want 2; an empty answer still has to say how much was annotated", result.Coverage.Declarations)
	}
}
