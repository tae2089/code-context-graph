//go:build postgres

package workflow

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"gorm.io/gorm"

	"github.com/tae2089/code-context-graph/internal/adapters/outbound/graphgorm"
	"github.com/tae2089/code-context-graph/internal/adapters/outbound/treesitter"
	requestctx "github.com/tae2089/code-context-graph/internal/ctx"
	"github.com/tae2089/code-context-graph/internal/db/dbtest"
	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

func TestBuild_PostgresSearchFailureRollsBackGraphAndDocuments(t *testing.T) {
	db := setupIsolatedPostgresServiceDB(t)
	st := graphgorm.New(db)
	if err := st.AutoMigrate(); err != nil {
		t.Fatalf("migrate graph schema: %v", err)
	}
	if err := db.AutoMigrate(&graph.SearchDocument{}, &graph.SearchReason{}); err != nil {
		t.Fatalf("migrate search documents: %v", err)
	}

	seedNode := graph.Node{Namespace: requestctx.DefaultNamespace, QualifiedName: "pkg.PostgresSeed", Kind: graph.NodeKindFunction, Name: "PostgresSeed", FilePath: "seed.go", StartLine: 1, EndLine: 2, Language: "go"}
	if err := db.Create(&seedNode).Error; err != nil {
		t.Fatalf("seed node: %v", err)
	}
	if err := db.Create(&graph.SearchDocument{Namespace: requestctx.DefaultNamespace, NodeID: seedNode.ID, Content: "postgres seed searchable", Language: "go"}).Error; err != nil {
		t.Fatalf("seed search document: %v", err)
	}

	wantErr := errors.New("postgres search rebuild boom")
	svc := &Service{
		Store:      st,
		UnitOfWork: newTestUnitOfWork(db, &failSearchBackend{err: wantErr}),
		Walkers:    map[string]Parser{".go": treesitter.NewWalker(treesitter.GoSpec)},
		Logger:     slog.Default(),
	}
	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, "sample.go"), []byte("package sample\n\nfunc PostgresNew() {}\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	_, err := svc.Build(context.Background(), BuildOptions{Dir: tmpDir})
	if !errors.Is(err, wantErr) {
		t.Fatalf("Build error = %v, want wrapped %v", err, wantErr)
	}

	assertPostgresCount(t, db.Model(&graph.Node{}).Where("qualified_name = ?", "pkg.PostgresSeed"), 1, "seed graph node")
	assertPostgresCount(t, db.Model(&graph.Node{}).Where("qualified_name = ?", "sample.PostgresNew"), 0, "new graph node")
	assertPostgresCount(t, db.Model(&graph.SearchDocument{}).Where("content = ?", "postgres seed searchable"), 1, "seed search document")
	assertPostgresCount(t, db.Model(&graph.SearchDocument{}), 1, "all search documents after rollback")
}

func setupIsolatedPostgresServiceDB(t *testing.T) *gorm.DB {
	t.Helper()
	return dbtest.OpenIsolatedPostgres(t)
}

func assertPostgresCount(t *testing.T, query *gorm.DB, want int64, label string) {
	t.Helper()
	var got int64
	if err := query.Count(&got).Error; err != nil {
		t.Fatalf("count %s: %v", label, err)
	}
	if got != want {
		t.Fatalf("%s count = %d, want %d", label, got, want)
	}
}
