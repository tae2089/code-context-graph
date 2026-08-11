package searchsql

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	requestctx "github.com/tae2089/code-context-graph/internal/ctx"
	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

func TestGoldenIntentCapturerRejectsSameSizeReindexedCorpus(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "graph.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&graph.Node{}, &graph.SearchReason{}, &graph.Annotation{}, &graph.DocTag{}); err != nil {
		t.Fatal(err)
	}
	node := graph.Node{ID: 2, Namespace: "golden", Name: "Build", QualifiedName: "workflow.Build", Kind: graph.NodeKindFunction, FilePath: "workflow/build.go", StartLine: 10}
	if err := db.Create(&node).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&graph.SearchReason{ID: 20, Namespace: "golden", NodeID: node.ID, Content: "build the graph"}).Error; err != nil {
		t.Fatal(err)
	}
	ctx := requestctx.WithNamespace(context.Background(), "golden")
	capturer, err := newGoldenIntentCapturer(ctx, NewReader(db, &SQLiteBackend{}))
	if err != nil {
		t.Fatal(err)
	}
	fixture := goldenIntentFixture{
		Corpus: 1,
		Nodes: map[uint]goldenIntentNode{1: {
			Name: "Build", QualifiedName: "workflow.Build", Kind: "function",
			FilePath: "workflow/build.go", Namespace: "golden", StartLine: 10,
		}},
		Documents: map[uint]goldenIntentDocument{10: {NodeID: 1, Content: "build the graph"}},
		Queries:   map[string][]uint{"build graph": {10}},
	}
	err = capturer.validateExisting(ctx, fixture)
	if err == nil || !strings.Contains(err.Error(), "full capture") {
		t.Fatalf("error = %v, want same-size reindex to require a full capture", err)
	}
}
