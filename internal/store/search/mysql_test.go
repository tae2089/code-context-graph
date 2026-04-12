//go:build mysql

package search

import (
	"context"
	"os"
	"testing"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/imtaebin/code-context-graph/internal/model"
)

func setupMySQLDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("TEST_MYSQL_DSN")
	if dsn == "" {
		dsn = "root:root@tcp(127.0.0.1:3306)/ccg_test?charset=utf8mb4&parseTime=True&loc=Local"
	}
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Skipf("MySQL not available: %v", err)
	}

	db.Exec("DROP TABLE IF EXISTS search_documents")
	db.Exec("DROP TABLE IF EXISTS nodes")

	if err := db.AutoMigrate(&model.Node{}, &model.SearchDocument{}); err != nil {
		t.Fatal(err)
	}
	return db
}

func seedMySQLNodes(t *testing.T, db *gorm.DB) {
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

func TestMySQLFTS_Migrate(t *testing.T) {
	db := setupMySQLDB(t)
	backend := NewMySQLBackend()

	if err := backend.Migrate(db); err != nil {
		t.Fatal(err)
	}

	var count int64
	db.Raw(`
		SELECT COUNT(*) FROM information_schema.statistics
		WHERE table_schema = DATABASE()
		AND table_name = 'search_documents'
		AND index_name = 'idx_search_documents_ft'
	`).Scan(&count)
	if count == 0 {
		t.Error("expected FULLTEXT index to exist")
	}
}

func TestMySQLFTS_Rebuild(t *testing.T) {
	db := setupMySQLDB(t)
	seedMySQLNodes(t, db)

	backend := NewMySQLBackend()
	if err := backend.Migrate(db); err != nil {
		t.Fatal(err)
	}
	if err := backend.Rebuild(context.Background(), db); err != nil {
		t.Fatal(err)
	}
}

func TestMySQLFTS_Query(t *testing.T) {
	db := setupMySQLDB(t)
	seedMySQLNodes(t, db)

	backend := NewMySQLBackend()
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
