package graphgorm

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	requestctx "github.com/tae2089/code-context-graph/internal/ctx"
	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

type deleteGraphSQLRecorder struct {
	mu         sync.Mutex
	statements []string
}

func (r *deleteGraphSQLRecorder) LogMode(logger.LogLevel) logger.Interface { return r }
func (r *deleteGraphSQLRecorder) Info(context.Context, string, ...any)     {}
func (r *deleteGraphSQLRecorder) Warn(context.Context, string, ...any)     {}
func (r *deleteGraphSQLRecorder) Error(context.Context, string, ...any)    {}
func (r *deleteGraphSQLRecorder) Trace(_ context.Context, _ time.Time, fc func() (string, int64), _ error) {
	sql, _ := fc()
	r.mu.Lock()
	r.statements = append(r.statements, sql)
	r.mu.Unlock()
}

func (r *deleteGraphSQLRecorder) reset() {
	r.mu.Lock()
	r.statements = nil
	r.mu.Unlock()
}

func (r *deleteGraphSQLRecorder) searchDocumentDelete() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, statement := range r.statements {
		if strings.Contains(statement, "DELETE FROM `search_documents`") {
			return statement
		}
	}
	return ""
}

func TestDeleteGraph_DeletesSearchDocumentsByNamespace(t *testing.T) {
	recorder := &deleteGraphSQLRecorder{}
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: recorder})
	if err != nil {
		t.Fatalf("open SQLite: %v", err)
	}
	store := New(db)
	if err := store.AutoMigrate(); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	if err := db.AutoMigrate(&graph.SearchDocument{}); err != nil {
		t.Fatalf("migrate search documents: %v", err)
	}

	ctx := requestctx.WithNamespace(context.Background(), "delete-me")
	if err := store.UpsertNodes(ctx, []graph.Node{{
		QualifiedName: "pkg.DeleteMe",
		Kind:          graph.NodeKindFunction,
		Name:          "DeleteMe",
		FilePath:      "delete.go",
		StartLine:     1,
		EndLine:       1,
		Language:      "go",
	}}); err != nil {
		t.Fatalf("upsert node: %v", err)
	}
	node, err := store.GetNode(ctx, "pkg.DeleteMe")
	if err != nil {
		t.Fatalf("get node: %v", err)
	}
	if err := db.Create(&graph.SearchDocument{
		Namespace: "delete-me",
		NodeID:    node.ID,
		Content:   "delete me",
		Language:  "go",
	}).Error; err != nil {
		t.Fatalf("create search document: %v", err)
	}

	recorder.reset()
	if err := store.DeleteGraph(ctx); err != nil {
		t.Fatalf("DeleteGraph: %v", err)
	}

	statement := recorder.searchDocumentDelete()
	if statement == "" {
		t.Fatal("search_documents DELETE statement not recorded")
	}
	if !strings.Contains(statement, "WHERE namespace = \"delete-me\"") {
		t.Fatalf("search_documents DELETE = %q, want direct namespace predicate", statement)
	}
	if strings.Contains(statement, "SELECT `id` FROM `nodes`") {
		t.Fatalf("search_documents DELETE = %q, must not select node IDs", statement)
	}
}
