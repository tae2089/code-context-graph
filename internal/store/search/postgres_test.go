//go:build postgres

package search

import (
	"context"
	"os"
	"testing"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/tae2089/code-context-graph/internal/model"
	"github.com/tae2089/code-context-graph/internal/ctxns"
)

func setupPostgresDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		dsn = "host=localhost user=postgres password=postgres dbname=ccg_test port=5432 sslmode=disable"
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Skipf("PostgreSQL not available: %v", err)
	}

	db.Exec("DROP TABLE IF EXISTS search_documents CASCADE")
	db.Exec("DROP TABLE IF EXISTS nodes CASCADE")

	if err := db.AutoMigrate(&model.Node{}, &model.SearchDocument{}); err != nil {
		t.Fatal(err)
	}
	return db
}

func seedPostgresNodes(t *testing.T, db *gorm.DB) {
	t.Helper()
	nodes := []model.Node{
		{QualifiedName: "pkg.AuthenticateUser", Kind: model.NodeKindFunction, Name: "AuthenticateUser", FilePath: "auth.go", StartLine: 1, EndLine: 10, Language: "go"},
		{QualifiedName: "pkg.CreateSession", Kind: model.NodeKindFunction, Name: "CreateSession", FilePath: "session.go", StartLine: 1, EndLine: 8, Language: "go"},
		{QualifiedName: "pkg.DeleteUser", Kind: model.NodeKindFunction, Name: "DeleteUser", FilePath: "user.go", StartLine: 1, EndLine: 5, Language: "go"},
	}
	for i := range nodes {
		db.Create(&nodes[i])
	}

	docs := []model.SearchDocument{
		{Namespace: nodes[0].Namespace, NodeID: nodes[0].ID, Content: "AuthenticateUser authenticates user credentials and returns JWT token", Language: "go"},
		{Namespace: nodes[1].Namespace, NodeID: nodes[1].ID, Content: "CreateSession creates a new session for authenticated user", Language: "go"},
		{Namespace: nodes[2].Namespace, NodeID: nodes[2].ID, Content: "DeleteUser removes a user account from the database", Language: "go"},
	}
	for i := range docs {
		db.Create(&docs[i])
	}
}

func TestPostgresFTS_Migrate(t *testing.T) {
	db := setupPostgresDB(t)
	backend := NewPostgresBackend()

	if err := backend.Migrate(db); err != nil {
		t.Fatal(err)
	}

	var count int64
	db.Raw(`
		SELECT COUNT(*) FROM pg_indexes
		WHERE tablename = 'search_documents'
		AND indexname = 'idx_search_documents_tsv'
	`).Scan(&count)
	if count != 1 {
		t.Errorf("expected GIN index to exist, got count=%d", count)
	}
}

func TestPostgresFTS_Rebuild(t *testing.T) {
	db := setupPostgresDB(t)
	seedPostgresNodes(t, db)

	backend := NewPostgresBackend()
	if err := backend.Migrate(db); err != nil {
		t.Fatal(err)
	}
	if err := backend.Rebuild(context.Background(), db); err != nil {
		t.Fatal(err)
	}

	var nonNull int64
	db.Raw("SELECT COUNT(*) FROM search_documents WHERE tsv IS NOT NULL").Scan(&nonNull)
	if nonNull != 3 {
		t.Errorf("expected 3 rows with tsv populated, got %d", nonNull)
	}
}

func TestPostgresFTS_Query(t *testing.T) {
	db := setupPostgresDB(t)
	seedPostgresNodes(t, db)

	backend := NewPostgresBackend()
	backend.Migrate(db)
	backend.Rebuild(context.Background(), db)

	nodes, err := backend.Query(context.Background(), db, "authenticate", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) == 0 {
		t.Fatal("expected at least 1 result for 'authenticate'")
	}
	found := false
	for _, n := range nodes {
		if n.QualifiedName == "pkg.AuthenticateUser" {
			found = true
		}
	}
	if !found {
		t.Error("expected pkg.AuthenticateUser in results")
	}
}

func TestPostgresFTS_Query_NamespaceIsolation(t *testing.T) {
	db := setupPostgresDB(t)
	backend := NewPostgresBackend()
	if err := backend.Migrate(db); err != nil {
		t.Fatal(err)
	}

	nodeA := model.Node{Namespace: "ns-a", QualifiedName: "pkg.A", Kind: model.NodeKindFunction, Name: "A", FilePath: "a.go", StartLine: 1, EndLine: 2, Language: "go"}
	nodeB := model.Node{Namespace: "ns-b", QualifiedName: "pkg.B", Kind: model.NodeKindFunction, Name: "B", FilePath: "b.go", StartLine: 1, EndLine: 2, Language: "go"}
	db.Create(&nodeA)
	db.Create(&nodeB)
	db.Create(&model.SearchDocument{Namespace: "ns-a", NodeID: nodeA.ID, Content: "sharedterm alpha", Language: "go"})
	db.Create(&model.SearchDocument{Namespace: "ns-b", NodeID: nodeB.ID, Content: "sharedterm beta", Language: "go"})

	if err := backend.Rebuild(ctxns.WithNamespace(context.Background(), "ns-a"), db); err != nil {
		t.Fatal(err)
	}
	if err := backend.Rebuild(ctxns.WithNamespace(context.Background(), "ns-b"), db); err != nil {
		t.Fatal(err)
	}

	resultsA, err := backend.Query(ctxns.WithNamespace(context.Background(), "ns-a"), db, "sharedterm", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(resultsA) != 1 || resultsA[0].Namespace != "ns-a" {
		t.Fatalf("expected only ns-a result, got %#v", resultsA)
	}

	resultsB, err := backend.Query(ctxns.WithNamespace(context.Background(), "ns-b"), db, "sharedterm", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(resultsB) != 1 || resultsB[0].Namespace != "ns-b" {
		t.Fatalf("expected only ns-b result, got %#v", resultsB)
	}
}

func TestPostgresFTS_Query_EmptyNamespace_IsLiteralFilter(t *testing.T) {
	db := setupPostgresDB(t)
	backend := NewPostgresBackend()
	if err := backend.Migrate(db); err != nil {
		t.Fatal(err)
	}

	defaultNode := model.Node{Namespace: ctxns.DefaultNamespace, QualifiedName: "pkg.Default", Kind: model.NodeKindFunction, Name: "Default", FilePath: "default.go", StartLine: 1, EndLine: 2, Language: "go"}
	otherNode := model.Node{Namespace: "ns-b", QualifiedName: "pkg.Other", Kind: model.NodeKindFunction, Name: "Other", FilePath: "other.go", StartLine: 1, EndLine: 2, Language: "go"}
	db.Create(&defaultNode)
	db.Create(&otherNode)
	db.Create(&model.SearchDocument{Namespace: ctxns.DefaultNamespace, NodeID: defaultNode.ID, Content: "sharedterm default", Language: "go"})
	db.Create(&model.SearchDocument{Namespace: "ns-b", NodeID: otherNode.ID, Content: "sharedterm other", Language: "go"})

	if err := backend.Rebuild(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if err := backend.Rebuild(ctxns.WithNamespace(context.Background(), "ns-b"), db); err != nil {
		t.Fatal(err)
	}

	results, err := backend.Query(context.Background(), db, "sharedterm", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Namespace != ctxns.DefaultNamespace {
		t.Fatalf("expected only default namespace result, got %#v", results)
	}
}

func TestPostgresFTS_Query_DefensivelyFiltersNodeNamespace(t *testing.T) {
	db := setupPostgresDB(t)
	backend := NewPostgresBackend()
	if err := backend.Migrate(db); err != nil {
		t.Fatal(err)
	}

	goodNode := model.Node{Namespace: "ns-a", QualifiedName: "pkg.Safe", Kind: model.NodeKindFunction, Name: "Safe", FilePath: "safe.go", StartLine: 1, EndLine: 2, Language: "go"}
	foreignNode := model.Node{Namespace: "ns-b", QualifiedName: "pkg.Foreign", Kind: model.NodeKindFunction, Name: "Foreign", FilePath: "foreign.go", StartLine: 1, EndLine: 2, Language: "go"}
	if err := db.Create(&goodNode).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&foreignNode).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.SearchDocument{Namespace: "ns-a", NodeID: goodNode.ID, Content: "sharedterm safe", Language: "go"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.SearchDocument{Namespace: "ns-a", NodeID: foreignNode.ID, Content: "sharedterm leaked", Language: "go"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := backend.Rebuild(ctxns.WithNamespace(context.Background(), "ns-a"), db); err != nil {
		t.Fatal(err)
	}

	results, err := backend.Query(ctxns.WithNamespace(context.Background(), "ns-a"), db, "sharedterm", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Namespace != "ns-a" || results[0].Name != "Safe" {
		t.Fatalf("expected only ns-a canonical node, got %#v", results)
	}
}

func TestPostgresFTS_Query_RejectsNonPositiveLimit(t *testing.T) {
	db := setupPostgresDB(t)
	backend := NewPostgresBackend()
	if err := backend.Migrate(db); err != nil {
		t.Fatal(err)
	}

	if _, err := backend.Query(context.Background(), db, "sharedterm", 0); err == nil {
		t.Fatal("expected query with limit=0 to fail")
	}
}
