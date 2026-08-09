//go:build fts5

package searchsql

import (
	"testing"

	requestctx "github.com/tae2089/code-context-graph/internal/ctx"
	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

// Coverage is what an empty answer leans on, so it has to count the same thing
// the index holds: how many of the searchable declarations had a reason written
// down. Counting rows instead of reasons would report full coverage for a
// codebase nobody has annotated at all.
func TestIntentCoverage_CountsReasonsAgainstSearchableNodes(t *testing.T) {
	db := setupTestDB(t)
	ctx := requestctx.WithNamespace(t.Context(), requestctx.DefaultNamespace)
	seedIntentFixture(t, db)
	if _, err := RefreshSearchDocuments(ctx, db); err != nil {
		t.Fatalf("RefreshSearchDocuments: %v", err)
	}

	reader := NewReader(db, NewSQLiteBackend())
	coverage, err := reader.IntentCoverage(ctx)
	if err != nil {
		t.Fatalf("IntentCoverage: %v", err)
	}
	if coverage.NodesTotal != 2 {
		t.Errorf("NodesTotal = %d, want 2", coverage.NodesTotal)
	}
	if coverage.NodesWithReason != 1 {
		t.Errorf("NodesWithReason = %d, want 1", coverage.NodesWithReason)
	}
}

// A namespace nobody indexed yet must report zero rather than fail, because the
// caller reads coverage precisely when the answer was empty.
func TestIntentCoverage_ReportsZeroForAnEmptyNamespace(t *testing.T) {
	db := setupTestDB(t)
	ctx := requestctx.WithNamespace(t.Context(), requestctx.DefaultNamespace)

	reader := NewReader(db, NewSQLiteBackend())
	coverage, err := reader.IntentCoverage(ctx)
	if err != nil {
		t.Fatalf("IntentCoverage: %v", err)
	}
	if coverage.NodesTotal != 0 || coverage.NodesWithReason != 0 {
		t.Errorf("coverage = %+v, want 0/0", coverage)
	}
}

// Coverage counts one namespace only. Reporting another namespace's annotated
// nodes would let a well-documented repository vouch for an empty one.
func TestIntentCoverage_StaysInsideItsNamespace(t *testing.T) {
	db := setupTestDB(t)
	other := graph.Node{Namespace: "other", QualifiedName: "other.thing", Kind: graph.NodeKindFunction, Name: "thing", FilePath: "other/thing.go", Language: "go"}
	if err := db.Create(&other).Error; err != nil {
		t.Fatalf("seed foreign node: %v", err)
	}
	otherCtx := requestctx.WithNamespace(t.Context(), "other")
	if _, err := RefreshSearchDocuments(otherCtx, db); err != nil {
		t.Fatalf("RefreshSearchDocuments(other): %v", err)
	}

	ctx := requestctx.WithNamespace(t.Context(), requestctx.DefaultNamespace)
	seedIntentFixture(t, db)
	if _, err := RefreshSearchDocuments(ctx, db); err != nil {
		t.Fatalf("RefreshSearchDocuments: %v", err)
	}

	reader := NewReader(db, NewSQLiteBackend())
	coverage, err := reader.IntentCoverage(ctx)
	if err != nil {
		t.Fatalf("IntentCoverage: %v", err)
	}
	if coverage.NodesTotal != 2 {
		t.Errorf("NodesTotal = %d, want 2; the other namespace leaked in", coverage.NodesTotal)
	}
}

// Reader.QueryIntent is the bound port the app service talks to, so it has to
// reach the intent index rather than the name index it sits beside.
func TestReaderQueryIntent_ReachesTheIntentIndex(t *testing.T) {
	db := setupTestDB(t)
	reasoned, namesake := seedIntentFixture(t, db)
	backend := buildIntentIndex(t, db)
	ctx := requestctx.WithNamespace(t.Context(), requestctx.DefaultNamespace)

	result, err := NewReader(db, backend).QueryIntent(ctx, "why do we verify the signature on a push", 10)
	if err != nil {
		t.Fatalf("QueryIntent: %v", err)
	}
	nodes := answeringNodes(result)
	if len(nodes) != 1 || nodes[0].ID != reasoned.ID {
		t.Fatalf("got %d nodes, want only node %d (%s)", len(nodes), reasoned.ID, reasoned.Name)
	}
	if nodes[0].ID == namesake.ID {
		t.Error("the name-only node came back through the bound port")
	}
}
