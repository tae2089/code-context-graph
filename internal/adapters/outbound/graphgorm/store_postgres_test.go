//go:build postgres

package graphgorm

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"gorm.io/gorm"

	"github.com/tae2089/code-context-graph/internal/app/ingest"
	"github.com/tae2089/code-context-graph/internal/db/dbtest"
	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

func TestWithTxDB_PostgresCommitsGraphAndSearchDocumentTogether(t *testing.T) {
	s, db := setupIsolatedPostgresStore(t)
	ctx := context.Background()

	err := s.WithTxDB(ctx, func(txStore ingest.GraphStore, txDB *gorm.DB) error {
		return writeGraphAndSearchDocument(ctx, txStore, txDB, "txdb.PostgresCommit", "postgres committed search document")
	})
	if err != nil {
		t.Fatalf("WithTxDB: %v", err)
	}

	assertGraphAndSearchDocumentCounts(t, db, "txdb.PostgresCommit", "postgres committed search document", 1)
}

func TestWithTxDB_PostgresRollsBackGraphAndSearchDocumentTogether(t *testing.T) {
	s, db := setupIsolatedPostgresStore(t)
	ctx := context.Background()
	wantErr := errors.New("rollback postgres graph and search")

	err := s.WithTxDB(ctx, func(txStore ingest.GraphStore, txDB *gorm.DB) error {
		if err := writeGraphAndSearchDocument(ctx, txStore, txDB, "txdb.PostgresRollback", "postgres rolled back search document"); err != nil {
			return err
		}
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("WithTxDB error = %v, want %v", err, wantErr)
	}

	assertGraphAndSearchDocumentCounts(t, db, "txdb.PostgresRollback", "postgres rolled back search document", 0)
}

func TestUpsertNodes_PostgresAllowsFileNameLongerThan256Characters(t *testing.T) {
	s, db := setupIsolatedPostgresStore(t)
	longPath := strings.Repeat("nested-directory/", 20) + "component.tsx"
	node := graph.Node{
		QualifiedName: longPath,
		Kind:          graph.NodeKindFile,
		Name:          longPath,
		FilePath:      longPath,
		StartLine:     1,
		EndLine:       1,
		Language:      "typescript",
	}

	if err := s.UpsertNodes(context.Background(), []graph.Node{node}); err != nil {
		t.Fatalf("upsert long file node: %v", err)
	}

	var persisted graph.Node
	if err := db.Where("qualified_name = ?", longPath).First(&persisted).Error; err != nil {
		t.Fatalf("load long file node: %v", err)
	}
	if persisted.Name != longPath {
		t.Fatalf("persisted name length = %d, want %d", len(persisted.Name), len(longPath))
	}
}

func TestDeleteGraph_PostgresHandlesMoreThanBindParameterLimit(t *testing.T) {
	s, db := setupIsolatedPostgresStore(t)
	ctx := context.Background()
	const nodeCount = 66_000
	nodes := make([]graph.Node, nodeCount)
	for i := range nodes {
		nodes[i] = graph.Node{
			QualifiedName: fmt.Sprintf("bindlimit.Node%d", i),
			Kind:          graph.NodeKindFunction,
			Name:          fmt.Sprintf("Node%d", i),
			FilePath:      "bind-limit.go",
			StartLine:     i + 1,
			EndLine:       i + 1,
			Language:      "go",
		}
	}
	if err := s.UpsertNodes(ctx, nodes); err != nil {
		t.Fatalf("upsert %d nodes: %v", nodeCount, err)
	}
	if err := s.DeleteGraph(ctx); err != nil {
		t.Fatalf("delete graph with %d nodes: %v", nodeCount, err)
	}
	var remaining int64
	if err := db.Model(&graph.Node{}).Count(&remaining).Error; err != nil {
		t.Fatalf("count remaining nodes: %v", err)
	}
	if remaining != 0 {
		t.Fatalf("remaining nodes = %d, want 0", remaining)
	}
}

func setupIsolatedPostgresStore(t *testing.T) (*Store, *gorm.DB) {
	t.Helper()

	db := dbtest.OpenIsolatedPostgres(t)
	s := New(db)
	if err := s.AutoMigrate(); err != nil {
		t.Fatalf("migrate graph schema: %v", err)
	}
	if err := db.AutoMigrate(&graph.SearchDocument{}, &graph.SearchReason{}); err != nil {
		t.Fatalf("migrate search documents: %v", err)
	}
	return s, db
}
