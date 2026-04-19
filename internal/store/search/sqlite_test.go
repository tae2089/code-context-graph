//go:build fts5

package search

import (
	"context"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/tae2089/code-context-graph/internal/model"
)

func setupTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.Node{}, &model.SearchDocument{}); err != nil {
		t.Fatal(err)
	}
	return db
}

func seedNodes(t *testing.T, db *gorm.DB) {
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

func TestSQLiteFTS_Migrate(t *testing.T) {
	db := setupTestDB(t)
	backend := NewSQLiteBackend()

	if err := backend.Migrate(db); err != nil {
		t.Fatal(err)
	}

	var count int64
	db.Raw("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='search_fts'").Scan(&count)
	if count != 1 {
		t.Errorf("expected search_fts table to exist, got count=%d", count)
	}
}

func TestSQLiteFTS_Rebuild(t *testing.T) {
	db := setupTestDB(t)
	seedNodes(t, db)

	backend := NewSQLiteBackend()
	if err := backend.Migrate(db); err != nil {
		t.Fatal(err)
	}
	if err := backend.Rebuild(context.Background(), db); err != nil {
		t.Fatal(err)
	}

	var count int64
	db.Raw("SELECT count(*) FROM search_fts").Scan(&count)
	if count != 3 {
		t.Errorf("expected 3 rows in FTS index, got %d", count)
	}
}

func TestSQLiteFTS_Query(t *testing.T) {
	db := setupTestDB(t)
	seedNodes(t, db)

	backend := NewSQLiteBackend()
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

func TestSQLiteFTS_QueryNoResults(t *testing.T) {
	db := setupTestDB(t)
	seedNodes(t, db)

	backend := NewSQLiteBackend()
	backend.Migrate(db)
	backend.Rebuild(context.Background(), db)

	nodes, err := backend.Query(context.Background(), db, "nonexistentkeyword", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 0 {
		t.Errorf("expected 0 results, got %d", len(nodes))
	}
}

func TestSQLiteFTS_Ranking(t *testing.T) {
	db := setupTestDB(t)
	seedNodes(t, db)

	backend := NewSQLiteBackend()
	backend.Migrate(db)
	backend.Rebuild(context.Background(), db)

	nodes, err := backend.Query(context.Background(), db, "user", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) < 2 {
		t.Fatalf("expected at least 2 results for 'user', got %d", len(nodes))
	}
}
