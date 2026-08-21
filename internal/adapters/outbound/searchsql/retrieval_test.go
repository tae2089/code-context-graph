//go:build fts5

package searchsql

import (
	"context"
	"testing"

	searchapp "github.com/tae2089/code-context-graph/internal/app/search"
	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

func TestReaderQuery_NaturalLanguageUsesSoftContentRanking(t *testing.T) {
	db := setupTestDB(t)
	backend := NewSQLiteBackend()
	if err := backend.Migrate(db); err != nil {
		t.Fatal(err)
	}

	nodes := []graph.Node{
		{Name: "SyncQueue", QualifiedName: "reposync.SyncQueue", Kind: graph.NodeKindType, FilePath: "internal/app/reposync/queue.go", Language: "go"},
		{Name: "WebhookHandler", QualifiedName: "webhook.WebhookHandler", Kind: graph.NodeKindType, FilePath: "internal/adapters/inbound/webhook/handler.go", Language: "go"},
		{Name: "GraphStats", QualifiedName: "status.GraphStats", Kind: graph.NodeKindType, FilePath: "internal/status/graph.go", Language: "go"},
	}
	for i := range nodes {
		if err := db.Create(&nodes[i]).Error; err != nil {
			t.Fatal(err)
		}
	}

	docs := []graph.SearchDocument{
		{NodeID: nodes[0].ID, Content: "SyncQueue coalesces webhook push graph updates into a background job", Language: "go"},
		{NodeID: nodes[1].ID, Content: "WebhookHandler receives a GitHub push webhook", Language: "go"},
		{NodeID: nodes[2].ID, Content: "GraphStats reports graph state", Language: "go"},
	}
	for i := range docs {
		if err := db.Create(&docs[i]).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := backend.Rebuild(context.Background(), db); err != nil {
		t.Fatal(err)
	}

	reader := NewReader(db, backend)
	query := "why are graph updates processed as a separate background job instead of immediately when a GitHub push webhook is received"
	got, err := reader.Query(context.Background(), query, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 {
		t.Fatal("natural-language query returned no candidates")
	}
	if got[0].QualifiedName != "reposync.SyncQueue" {
		t.Fatalf("first candidate = %q, want reposync.SyncQueue", got[0].QualifiedName)
	}
}

func TestSearchService_NaturalLanguageReturnsJustifiedSoftCandidate(t *testing.T) {
	db := setupTestDB(t)
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	backend := NewSQLiteBackend()
	if err := backend.Migrate(db); err != nil {
		t.Fatal(err)
	}

	node := graph.Node{
		Name: "SyncQueue", QualifiedName: "reposync.SyncQueue", Kind: graph.NodeKindType,
		FilePath: "internal/app/reposync/queue.go", Language: "go",
	}
	if err := db.Create(&node).Error; err != nil {
		t.Fatal(err)
	}
	annotation := graph.Annotation{NodeID: node.ID, Tags: []graph.DocTag{{
		Kind: graph.TagIntent, Value: "coalesce webhook push graph updates into a background job",
	}}}
	if err := db.Create(&annotation).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&graph.SearchDocument{
		NodeID: node.ID, Content: "SyncQueue coalesces webhook push graph updates into a background job", Language: "go",
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := backend.Rebuild(context.Background(), db); err != nil {
		t.Fatal(err)
	}

	query := "why are graph updates processed as a separate background job instead of immediately when a GitHub push webhook is received"
	list, err := searchapp.New(NewReader(db, backend)).Search(context.Background(), searchapp.Params{Query: query, Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Files) != 1 || len(list.Files[0].Hits) != 1 {
		t.Fatalf("files = %#v, want one justified SyncQueue hit", list.Files)
	}
	if got := list.Files[0].Hits[0]; got.Node.QualifiedName != "reposync.SyncQueue" || len(got.Matched) == 0 {
		t.Fatalf("hit = %#v, want justified reposync.SyncQueue", got)
	}
}

func TestReaderQuery_ShortMultiTermQueryRemainsStrict(t *testing.T) {
	db := setupTestDB(t)
	seedNodes(t, db)
	backend := NewSQLiteBackend()
	if err := backend.Migrate(db); err != nil {
		t.Fatal(err)
	}
	if err := backend.Rebuild(context.Background(), db); err != nil {
		t.Fatal(err)
	}

	got, err := NewReader(db, backend).Query(context.Background(), "session credentials", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("strict query returned %d candidates, want none", len(got))
	}
}

func TestDistinctTermCount_DoesNotCountRepeatedWordsTwice(t *testing.T) {
	if got := distinctTermCount([]string{"graph", "graph", "updates"}); got != 2 {
		t.Fatalf("distinctTermCount = %d, want 2", got)
	}
}
