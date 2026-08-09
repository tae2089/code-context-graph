//go:build fts5

package searchsql

import (
	"fmt"
	"testing"

	"gorm.io/gorm"

	intentapp "github.com/tae2089/code-context-graph/internal/app/search/intent"
	requestctx "github.com/tae2089/code-context-graph/internal/ctx"
	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

// answeringNodes pulls the declarations out of a ranked intent result, for the
// tests that ask which nodes answered rather than what matched inside them.
func answeringNodes(result intentapp.Result) []graph.Node {
	nodes := make([]graph.Node, 0, len(result.Hits))
	for _, hit := range result.Hits {
		nodes = append(nodes, hit.Node)
	}
	return nodes
}

// seedIntentFixture builds the smallest graph that can tell the two indexes
// apart: one node whose name says nothing and whose recorded reason says
// everything, and one node whose name contains the query word by accident.
func seedIntentFixture(t *testing.T, db *gorm.DB) (reasoned, namesake graph.Node) {
	t.Helper()
	reasoned = graph.Node{QualifiedName: "webhook.handle", Kind: graph.NodeKindFunction, Name: "handle", FilePath: "webhook/handle.go", Language: "go"}
	namesake = graph.Node{QualifiedName: "util.signatureFormatter", Kind: graph.NodeKindFunction, Name: "signatureFormatter", FilePath: "util/format.go", Language: "go"}
	for _, node := range []*graph.Node{&reasoned, &namesake} {
		if err := db.Create(node).Error; err != nil {
			t.Fatalf("seed node %s: %v", node.Name, err)
		}
	}
	annotation := graph.Annotation{NodeID: reasoned.ID, Summary: "handle processes an incoming push."}
	if err := db.Create(&annotation).Error; err != nil {
		t.Fatalf("seed annotation: %v", err)
	}
	tags := []graph.DocTag{
		{AnnotationID: annotation.ID, Kind: graph.TagIntent, Value: "verify the signature so a push from anywhere else is rejected", Ordinal: 0},
	}
	for i := range tags {
		if err := db.Create(&tags[i]).Error; err != nil {
			t.Fatalf("seed tag: %v", err)
		}
	}
	return reasoned, namesake
}

// seedTiedIntentFixture gives several declarations the same recorded reason, so
// every one of them scores identically and only the tiebreak decides the order.
func seedTiedIntentFixture(t *testing.T, db *gorm.DB, count int) {
	t.Helper()
	for i := range count {
		node := graph.Node{
			QualifiedName: fmt.Sprintf("tied.decl%02d", i),
			Kind:          graph.NodeKindFunction,
			Name:          fmt.Sprintf("decl%02d", i),
			FilePath:      fmt.Sprintf("tied/file%02d.go", i),
			Language:      "go",
		}
		if err := db.Create(&node).Error; err != nil {
			t.Fatalf("seed tied node %d: %v", i, err)
		}
		annotation := graph.Annotation{NodeID: node.ID}
		if err := db.Create(&annotation).Error; err != nil {
			t.Fatalf("seed tied annotation %d: %v", i, err)
		}
		tag := graph.DocTag{AnnotationID: annotation.ID, Kind: graph.TagIntent, Value: "keep the queue draining under backpressure"}
		if err := db.Create(&tag).Error; err != nil {
			t.Fatalf("seed tied tag %d: %v", i, err)
		}
	}
}

// Asking for more rows must extend the list, never reshuffle it.
//
// Scores tie constantly here: a recorded reason is one short sentence, so many
// of them earn the same score for the same question. With nothing but the score
// in ORDER BY, the database is free to return tied rows in whatever order the
// plan produced, and that order changes with LIMIT. PostgreSQL does change it —
// `ts_rank` put nine reasons on exactly 0.020264236 for one golden question, and
// raising the limit reordered them, which moved the right file down the answer
// for no reason anybody could act on. An answer that moves when the caller asks
// for one more row cannot be measured, and cannot be trusted twice in a row
// during an incident.
func TestQueryIntent_OrdersTiedReasonsTheSameWayAtEveryLimit(t *testing.T) {
	db := setupTestDB(t)
	seedTiedIntentFixture(t, db, 12)
	backend := buildIntentIndex(t, db)
	ctx := requestctx.WithNamespace(t.Context(), requestctx.DefaultNamespace)

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

func buildIntentIndex(t *testing.T, db *gorm.DB) *SQLiteBackend {
	t.Helper()
	backend := NewSQLiteBackend()
	if err := backend.Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	ctx := requestctx.WithNamespace(t.Context(), requestctx.DefaultNamespace)
	if _, err := RefreshSearchDocuments(ctx, db); err != nil {
		t.Fatalf("RefreshSearchDocuments: %v", err)
	}
	if err := backend.Rebuild(ctx, db); err != nil {
		t.Fatalf("Rebuild: %v", err)
	}
	return backend
}

// The point of a separate index is that a name cannot answer an intent question.
// `signatureFormatter` contains the word a caller asking about signature checking
// would type, and it is the wrong answer: nothing was ever recorded about why it
// exists. If it comes back here, the split has bought nothing.
func TestQueryIntent_AnswersFromTheReasonNotTheName(t *testing.T) {
	db := setupTestDB(t)
	reasoned, namesake := seedIntentFixture(t, db)
	backend := buildIntentIndex(t, db)
	ctx := requestctx.WithNamespace(t.Context(), requestctx.DefaultNamespace)

	result, err := NewReader(db, backend).QueryIntent(ctx, "why do we verify the signature on a push", 10)
	if err != nil {
		t.Fatalf("QueryIntent: %v", err)
	}
	nodes := answeringNodes(result)
	if len(nodes) == 0 {
		t.Fatal("no node answered a question its @intent plainly answers")
	}
	if nodes[0].ID != reasoned.ID {
		t.Errorf("first answer is node %d (%s), want %d (%s)", nodes[0].ID, nodes[0].Name, reasoned.ID, reasoned.Name)
	}
	for _, node := range nodes {
		if node.ID == namesake.ID {
			t.Errorf("node %q came back on its name alone; it has no recorded reason", node.Name)
		}
	}
}

// A question about something nobody wrote a reason for has to come back empty.
// Returning a near-miss would make "no reason was recorded here" indistinguishable
// from an answer, which is the one thing the caller has to be able to tell apart.
func TestQueryIntent_SaysNothingWhenNoReasonMatches(t *testing.T) {
	db := setupTestDB(t)
	seedIntentFixture(t, db)
	backend := buildIntentIndex(t, db)
	ctx := requestctx.WithNamespace(t.Context(), requestctx.DefaultNamespace)

	result, err := NewReader(db, backend).QueryIntent(ctx, "how does the scheduler pick a leader", 10)
	if err != nil {
		t.Fatalf("QueryIntent: %v", err)
	}
	nodes := answeringNodes(result)
	if len(nodes) != 0 {
		t.Errorf("expected nothing, got %d nodes (first %q)", len(nodes), nodes[0].Name)
	}
}
