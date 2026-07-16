package graphgorm

import (
	"context"
	"fmt"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	requestctx "github.com/tae2089/code-context-graph/internal/ctx"
	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

func setupDeletionSQLMeasurement(t *testing.T, fileCount int) (*Store, context.Context, []string, *annotationSQLCounter) {
	t.Helper()
	counter := &annotationSQLCounter{}
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: counter})
	if err != nil {
		t.Fatalf("open SQLite: %v", err)
	}
	store := New(db)
	if err := store.AutoMigrate(); err != nil {
		t.Fatalf("auto migrate graph store: %v", err)
	}
	if err := db.AutoMigrate(&graph.SearchDocument{}); err != nil {
		t.Fatalf("auto migrate search documents: %v", err)
	}
	ctx := requestctx.WithNamespace(context.Background(), "bulk-delete")
	filePaths := make([]string, fileCount)
	nodes := make([]graph.Node, fileCount)
	for i := range fileCount {
		filePaths[i] = fmt.Sprintf("src/file-%03d.go", i)
		nodes[i] = graph.Node{
			QualifiedName: fmt.Sprintf("sample.F%d", i),
			Kind:          graph.NodeKindFunction,
			Name:          fmt.Sprintf("F%d", i),
			FilePath:      filePaths[i],
			StartLine:     1,
			EndLine:       1,
			Language:      "go",
		}
	}
	if err := store.UpsertNodes(ctx, nodes); err != nil {
		t.Fatalf("upsert nodes: %v", err)
	}
	counter.reset()
	return store, ctx, filePaths, counter
}

func TestDeleteNodesByFiles_UsesBoundedSQL(t *testing.T) {
	store, ctx, filePaths, counter := setupDeletionSQLMeasurement(t, 5)
	started := time.Now()
	if err := store.DeleteNodesByFiles(ctx, filePaths); err != nil {
		t.Fatalf("delete nodes by files: %v", err)
	}
	statements := counter.statements.Load()
	t.Logf("bulk file deletion: files=%d statements=%d elapsed=%s", len(filePaths), statements, time.Since(started))
	if statements > 12 {
		t.Fatalf("bulk file deletion statements = %d, want <= 12", statements)
	}
	var remaining int64
	if err := store.db.Model(&graph.Node{}).Where("namespace = ?", requestctx.FromContext(ctx)).Count(&remaining).Error; err != nil {
		t.Fatalf("count remaining nodes: %v", err)
	}
	if remaining != 0 {
		t.Fatalf("remaining nodes = %d, want 0", remaining)
	}
}
