//go:build fts5

package search

import (
	"context"
	"fmt"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/tae2089/code-context-graph/internal/ctxns"
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
		{Namespace: nodes[0].Namespace, NodeID: nodes[0].ID, Content: "AuthenticateUser authenticates user credentials and returns JWT token", Language: "go"},
		{Namespace: nodes[1].Namespace, NodeID: nodes[1].ID, Content: "CreateSession creates a new session for authenticated user", Language: "go"},
		{Namespace: nodes[2].Namespace, NodeID: nodes[2].ID, Content: "DeleteUser removes a user account from the database", Language: "go"},
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

func TestSQLiteFTS_Query_NamespaceIsolation(t *testing.T) {
	db := setupTestDB(t)
	backend := NewSQLiteBackend()
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

func TestSQLiteFTS_Query_SanitizesSpecialCharacters(t *testing.T) {
	db := setupTestDB(t)
	seedNodes(t, db)

	backend := NewSQLiteBackend()
	if err := backend.Migrate(db); err != nil {
		t.Fatal(err)
	}
	if err := backend.Rebuild(context.Background(), db); err != nil {
		t.Fatal(err)
	}

	for _, query := range []string{"func(x)", "foo:bar", "hello-world", "\"unterminated"} {
		if _, err := backend.Query(context.Background(), db, query, 10); err != nil {
			t.Fatalf("query %q returned error: %v", query, err)
		}
	}
}

func TestSQLiteFTS_Query_DefensivelyFiltersNodeNamespace(t *testing.T) {
	db := setupTestDB(t)
	backend := NewSQLiteBackend()
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
	if err := db.Exec("INSERT INTO search_fts(node_id, content, language, namespace) VALUES (?, ?, ?, ?)", goodNode.ID, "sharedterm safe", "go", "ns-a").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO search_fts(node_id, content, language, namespace) VALUES (?, ?, ?, ?)", foreignNode.ID, "sharedterm leaked", "go", "ns-a").Error; err != nil {
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

func TestSQLiteFTS_Migrate_PreservesExistingIndexRows(t *testing.T) {
	db := setupTestDB(t)
	seedNodes(t, db)

	backend := NewSQLiteBackend()
	if err := backend.Migrate(db); err != nil {
		t.Fatal(err)
	}
	if err := backend.Rebuild(context.Background(), db); err != nil {
		t.Fatal(err)
	}

	var before int64
	if err := db.Raw("SELECT count(*) FROM search_fts").Scan(&before).Error; err != nil {
		t.Fatal(err)
	}
	if before != 3 {
		t.Fatalf("expected 3 rows before second migrate, got %d", before)
	}

	if err := backend.Migrate(db); err != nil {
		t.Fatal(err)
	}

	var after int64
	if err := db.Raw("SELECT count(*) FROM search_fts").Scan(&after).Error; err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("expected second migrate to preserve FTS rows, before=%d after=%d", before, after)
	}
}

func TestSQLiteFTS_Migrate_UpgradesLegacySchemaAndRebuilds(t *testing.T) {
	db := setupTestDB(t)
	seedNodes(t, db)

	if err := db.Exec(`
		CREATE VIRTUAL TABLE search_fts
		USING fts5(node_id UNINDEXED, content, language)
	`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO search_fts(node_id, content, language) VALUES (?, ?, ?)", 999, "stale row", "go").Error; err != nil {
		t.Fatal(err)
	}

	backend := NewSQLiteBackend()
	if err := backend.Migrate(db); err != nil {
		t.Fatal(err)
	}

	hasNamespace, err := sqliteColumnExists(db, "search_fts", "namespace")
	if err != nil {
		t.Fatal(err)
	}
	if !hasNamespace {
		t.Fatal("expected migrated FTS table to include namespace column")
	}

	var count int64
	if err := db.Raw("SELECT count(*) FROM search_fts").Scan(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Fatalf("expected migrated FTS table to be rebuilt with 3 rows, got %d", count)
	}
}

func TestSQLiteFTS_Migrate_LegacyUpgradeCleansStaleUpgradeTable(t *testing.T) {
	db := setupTestDB(t)
	seedNodes(t, db)

	if err := db.Exec(`
		CREATE VIRTUAL TABLE search_fts
		USING fts5(node_id UNINDEXED, content, language)
	`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO search_fts(node_id, content, language) VALUES (?, ?, ?)", 999, "legacy row", "go").Error; err != nil {
		t.Fatal(err)
	}
	if err := createSQLiteFTSTable(db, sqliteFTSUpgradeTable, false); err != nil {
		t.Fatal(err)
	}

	backend := NewSQLiteBackend()
	if err := backend.Migrate(db); err != nil {
		t.Fatalf("expected migrate to recover from stale upgrade table: %v", err)
	}

	hasNamespace, checkErr := sqliteColumnExists(db, sqliteFTSTable, "namespace")
	if checkErr != nil {
		t.Fatal(checkErr)
	}
	if !hasNamespace {
		t.Fatal("expected upgraded search_fts table to include namespace column")
	}

	exists, checkErr := sqliteTableExists(db, sqliteFTSLegacyBackup)
	if checkErr != nil {
		t.Fatal(checkErr)
	}
	if exists {
		t.Fatal("did not expect legacy backup table to remain after successful upgrade")
	}

	exists, checkErr = sqliteTableExists(db, sqliteFTSUpgradeTable)
	if checkErr != nil {
		t.Fatal(checkErr)
	}
	if exists {
		t.Fatal("did not expect stale upgrade table to remain after successful migrate")
	}
}

func TestSQLiteFTS_Rebuild_BatchesLargeDatasets(t *testing.T) {
	db := setupTestDB(t)
	backend := NewSQLiteBackend()
	if err := backend.Migrate(db); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < sqliteFTSRebuildBatchSize+25; i++ {
		node := model.Node{QualifiedName: fmt.Sprintf("pkg.Node%d", i), Kind: model.NodeKindFunction, Name: fmt.Sprintf("Node%d", i), FilePath: fmt.Sprintf("file%d.go", i), StartLine: i + 1, EndLine: i + 1, Language: "go"}
		if err := db.Create(&node).Error; err != nil {
			t.Fatal(err)
		}
		doc := model.SearchDocument{NodeID: node.ID, Content: fmt.Sprintf("term%d batched content", i), Language: "go"}
		if err := db.Create(&doc).Error; err != nil {
			t.Fatal(err)
		}
	}

	if err := backend.Rebuild(context.Background(), db); err != nil {
		t.Fatal(err)
	}

	var count int64
	if err := db.Raw("SELECT count(*) FROM search_fts").Scan(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != int64(sqliteFTSRebuildBatchSize+25) {
		t.Fatalf("expected %d rows after batched rebuild, got %d", sqliteFTSRebuildBatchSize+25, count)
	}
}

func TestInsertSQLiteFTSBatch_BuildsMultiValuesInsert(t *testing.T) {
	docs := []model.SearchDocument{
		{NodeID: 1, Content: "alpha", Language: "go", Namespace: "ns-a"},
		{NodeID: 2, Content: "beta", Language: "go", Namespace: "ns-b"},
	}

	insertSQL, args := buildSQLiteFTSInsert(sqliteFTSTable, docs)

	wantSQL := "INSERT INTO search_fts(node_id, content, language, namespace) VALUES (?, ?, ?, ?), (?, ?, ?, ?)"
	if insertSQL != wantSQL {
		t.Fatalf("unexpected SQL:\n got: %s\nwant: %s", insertSQL, wantSQL)
	}
	if len(args) != 8 {
		t.Fatalf("expected 8 args, got %d", len(args))
	}
	wantArgs := []any{uint(1), "alpha", "go", "ns-a", uint(2), "beta", "go", "ns-b"}
	for i := range wantArgs {
		if args[i] != wantArgs[i] {
			t.Fatalf("arg[%d] = %#v, want %#v", i, args[i], wantArgs[i])
		}
	}
}

func TestBuildSQLiteFTSInsert_EmptyBatch(t *testing.T) {
	insertSQL, args := buildSQLiteFTSInsert(sqliteFTSTable, nil)
	if insertSQL != "" {
		t.Fatalf("expected empty SQL for empty batch, got %q", insertSQL)
	}
	if args != nil {
		t.Fatalf("expected nil args for empty batch, got %#v", args)
	}
}
