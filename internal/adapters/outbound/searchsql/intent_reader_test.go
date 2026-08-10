//go:build fts5

package searchsql

import (
	"testing"

	requestctx "github.com/tae2089/code-context-graph/internal/ctx"
)

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
