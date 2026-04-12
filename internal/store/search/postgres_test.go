//go:build postgres

package search

import (
	"context"
	"os"
	"testing"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/imtaebin/code-context-graph/internal/model"
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
		{NodeID: nodes[0].ID, Content: "AuthenticateUser authenticates user credentials and returns JWT token", Language: "go"},
		{NodeID: nodes[1].ID, Content: "CreateSession creates a new session for authenticated user", Language: "go"},
		{NodeID: nodes[2].ID, Content: "DeleteUser removes a user account from the database", Language: "go"},
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
