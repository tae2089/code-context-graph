//go:build fts5

package search

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/tae2089/code-context-graph/internal/ctxns"
	"github.com/tae2089/code-context-graph/internal/model"
)

type inQueryCaptureLogger struct {
	logger.Interface
	needle string
	maxIDs int
	hits   int
}

func (l *inQueryCaptureLogger) LogMode(level logger.LogLevel) logger.Interface { return l }
func (l *inQueryCaptureLogger) Trace(ctx context.Context, begin time.Time, fc func() (string, int64), err error) {
	sql, _ := fc()
	if strings.Contains(sql, l.needle) && strings.Contains(sql, " IN (") {
		l.hits++
		ids := countSQLInList(sql)
		if ids > l.maxIDs {
			l.maxIDs = ids
		}
	}
}

func countSQLInList(sql string) int {
	start := strings.Index(sql, " IN (")
	if start < 0 {
		return 0
	}
	start += len(" IN (")
	end := strings.Index(sql[start:], ")")
	if end < 0 {
		return 0
	}
	list := strings.TrimSpace(sql[start : start+end])
	if list == "" {
		return 0
	}
	return strings.Count(list, ",") + 1
}

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

func TestSQLiteFTS_Query_PromotesExactNameMatch(t *testing.T) {
	db := setupTestDB(t)
	backend := NewSQLiteBackend()
	if err := backend.Migrate(db); err != nil {
		t.Fatal(err)
	}

	nodes := []model.Node{
		{Namespace: ctxns.DefaultNamespace, QualifiedName: "cpp.UserService.getUser", Kind: model.NodeKindFunction, Name: "getUser", FilePath: "cpp/sample.cpp", StartLine: 1, EndLine: 2, Language: "cpp"},
		{Namespace: ctxns.DefaultNamespace, QualifiedName: "go.UserService.GetUser", Kind: model.NodeKindFunction, Name: "GetUser", FilePath: "go/sample.go", StartLine: 1, EndLine: 2, Language: "go"},
		{Namespace: ctxns.DefaultNamespace, QualifiedName: "java.UserService.getUser", Kind: model.NodeKindFunction, Name: "getUser", FilePath: "java/Sample.java", StartLine: 1, EndLine: 2, Language: "java"},
	}
	for i := range nodes {
		if err := db.Create(&nodes[i]).Error; err != nil {
			t.Fatal(err)
		}
	}

	docs := []model.SearchDocument{
		{Namespace: ctxns.DefaultNamespace, NodeID: nodes[0].ID, Content: "getUser cpp sample cpp", Language: "cpp"},
		{Namespace: ctxns.DefaultNamespace, NodeID: nodes[1].ID, Content: "GetUser go sample go", Language: "go"},
		{Namespace: ctxns.DefaultNamespace, NodeID: nodes[2].ID, Content: "getUser java sample java", Language: "java"},
	}
	for i := range docs {
		if err := db.Create(&docs[i]).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := backend.Rebuild(context.Background(), db); err != nil {
		t.Fatal(err)
	}

	got, err := backend.Query(context.Background(), db, "GetUser", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 {
		t.Fatal("expected query results")
	}
	if got[0].Name != "GetUser" {
		t.Fatalf("expected exact-name match first, got %q (%q)", got[0].Name, got[0].QualifiedName)
	}
}

func TestSQLiteFTS_RebuildNodes_RefreshesOnlyScopedNodeRows(t *testing.T) {
	db := setupTestDB(t)
	backend := NewSQLiteBackend()
	if err := backend.Migrate(db); err != nil {
		t.Fatal(err)
	}

	changed := model.Node{Namespace: ctxns.DefaultNamespace, QualifiedName: "pkg.Changed", Kind: model.NodeKindFunction, Name: "Changed", FilePath: "changed.go", StartLine: 1, EndLine: 1, Language: "go"}
	untouched := model.Node{Namespace: ctxns.DefaultNamespace, QualifiedName: "pkg.Untouched", Kind: model.NodeKindFunction, Name: "Untouched", FilePath: "untouched.go", StartLine: 1, EndLine: 1, Language: "go"}
	foreign := model.Node{Namespace: "ns-b", QualifiedName: "pkg.Foreign", Kind: model.NodeKindFunction, Name: "Foreign", FilePath: "foreign.go", StartLine: 1, EndLine: 1, Language: "go"}
	for _, node := range []*model.Node{&changed, &untouched, &foreign} {
		if err := db.Create(node).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Exec("INSERT INTO search_fts(node_id, content, language, namespace) VALUES (?, ?, ?, ?), (?, ?, ?, ?), (?, ?, ?, ?)", changed.ID, "stale changed", "go", ctxns.DefaultNamespace, untouched.ID, "keep untouched", "go", ctxns.DefaultNamespace, foreign.ID, "keep foreign", "go", "ns-b").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.SearchDocument{Namespace: ctxns.DefaultNamespace, NodeID: changed.ID, Content: "fresh changed", Language: "go"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.SearchDocument{Namespace: ctxns.DefaultNamespace, NodeID: untouched.ID, Content: "fresh untouched", Language: "go"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.SearchDocument{Namespace: "ns-b", NodeID: foreign.ID, Content: "fresh foreign", Language: "go"}).Error; err != nil {
		t.Fatal(err)
	}

	if err := backend.RebuildNodes(context.Background(), db, []uint{changed.ID, foreign.ID}); err != nil {
		t.Fatal(err)
	}

	var changedContent, untouchedContent, foreignContent string
	if err := db.Raw("SELECT content FROM search_fts WHERE node_id = ? AND namespace = ?", changed.ID, ctxns.DefaultNamespace).Scan(&changedContent).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Raw("SELECT content FROM search_fts WHERE node_id = ? AND namespace = ?", untouched.ID, ctxns.DefaultNamespace).Scan(&untouchedContent).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Raw("SELECT content FROM search_fts WHERE node_id = ? AND namespace = ?", foreign.ID, "ns-b").Scan(&foreignContent).Error; err != nil {
		t.Fatal(err)
	}
	if changedContent != "fresh changed" {
		t.Fatalf("expected changed row refreshed, got %q", changedContent)
	}
	if untouchedContent != "keep untouched" {
		t.Fatalf("expected untouched row preserved, got %q", untouchedContent)
	}
	if foreignContent != "keep foreign" {
		t.Fatalf("expected foreign namespace row preserved, got %q", foreignContent)
	}
}

func TestSQLiteFTS_RebuildNodes_EmptyScopeIsNoOp(t *testing.T) {
	db := setupTestDB(t)
	backend := NewSQLiteBackend()
	if err := backend.Migrate(db); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO search_fts(node_id, content, language, namespace) VALUES (?, ?, ?, ?)", 1, "keep", "go", ctxns.DefaultNamespace).Error; err != nil {
		t.Fatal(err)
	}
	if err := backend.RebuildNodes(context.Background(), db, nil); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := db.Raw("SELECT count(*) FROM search_fts").Scan(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("expected empty scope to preserve rows, got %d", count)
	}
}

func TestSQLiteFTS_RebuildNodes_ChunksLargeNodeScopes(t *testing.T) {
	capture := &inQueryCaptureLogger{Interface: logger.Discard, needle: "search_fts"}
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: capture})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.Node{}, &model.SearchDocument{}); err != nil {
		t.Fatal(err)
	}
	backend := NewSQLiteBackend()
	if err := backend.Migrate(db); err != nil {
		t.Fatal(err)
	}

	nodeIDs := make([]uint, 0, scopedRebuildChunkSize+1)
	for i := range scopedRebuildChunkSize + 1 {
		node := model.Node{Namespace: ctxns.DefaultNamespace, QualifiedName: fmt.Sprintf("pkg.Node%d", i), Kind: model.NodeKindFunction, Name: fmt.Sprintf("Node%d", i), FilePath: fmt.Sprintf("node-%d.go", i), StartLine: 1, EndLine: 1, Language: "go"}
		if err := db.Create(&node).Error; err != nil {
			t.Fatalf("create node %d: %v", i, err)
		}
		nodeIDs = append(nodeIDs, node.ID)
		if err := db.Create(&model.SearchDocument{Namespace: ctxns.DefaultNamespace, NodeID: node.ID, Content: fmt.Sprintf("fresh node %d", i), Language: "go"}).Error; err != nil {
			t.Fatalf("create doc %d: %v", i, err)
		}
	}

	if err := backend.RebuildNodes(context.Background(), db, nodeIDs); err != nil {
		t.Fatal(err)
	}
	if capture.maxIDs > scopedRebuildChunkSize {
		t.Fatalf("expected scoped FTS IN queries to be chunked to <= %d IDs, got %d", scopedRebuildChunkSize, capture.maxIDs)
	}
	if capture.hits < 2 {
		t.Fatalf("expected multiple scoped FTS IN queries, got %d", capture.hits)
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

func TestSQLiteFTS_Query_EmptyNamespace_IsLiteralFilter(t *testing.T) {
	db := setupTestDB(t)
	backend := NewSQLiteBackend()
	if err := backend.Migrate(db); err != nil {
		t.Fatal(err)
	}

	defaultNode := model.Node{Namespace: ctxns.DefaultNamespace, QualifiedName: "pkg.Default", Kind: model.NodeKindFunction, Name: "Default", FilePath: "default.go", StartLine: 1, EndLine: 2, Language: "go"}
	otherNode := model.Node{Namespace: "ns-b", QualifiedName: "pkg.Other", Kind: model.NodeKindFunction, Name: "Other", FilePath: "other.go", StartLine: 1, EndLine: 2, Language: "go"}
	if err := db.Create(&defaultNode).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&otherNode).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.SearchDocument{Namespace: ctxns.DefaultNamespace, NodeID: defaultNode.ID, Content: "sharedterm default", Language: "go"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.SearchDocument{Namespace: "ns-b", NodeID: otherNode.ID, Content: "sharedterm other", Language: "go"}).Error; err != nil {
		t.Fatal(err)
	}
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

func TestSQLiteFTS_Rebuild_EmptyNamespace_PreservesOtherRows(t *testing.T) {
	db := setupTestDB(t)
	backend := NewSQLiteBackend()
	if err := backend.Migrate(db); err != nil {
		t.Fatal(err)
	}

	defaultNode := model.Node{Namespace: ctxns.DefaultNamespace, QualifiedName: "pkg.Default", Kind: model.NodeKindFunction, Name: "Default", FilePath: "default.go", StartLine: 1, EndLine: 1, Language: "go"}
	otherNode := model.Node{Namespace: "ns-b", QualifiedName: "pkg.Other", Kind: model.NodeKindFunction, Name: "Other", FilePath: "other.go", StartLine: 1, EndLine: 1, Language: "go"}
	if err := db.Create(&defaultNode).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&otherNode).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.SearchDocument{Namespace: ctxns.DefaultNamespace, NodeID: defaultNode.ID, Content: "default term", Language: "go"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.SearchDocument{Namespace: "ns-b", NodeID: otherNode.ID, Content: "other term", Language: "go"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := backend.Rebuild(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO search_fts(node_id, content, language, namespace) VALUES (?, ?, ?, ?)", 9999, "foreign stale", "go", "ns-b").Error; err != nil {
		t.Fatal(err)
	}

	if err := backend.Rebuild(context.Background(), db); err != nil {
		t.Fatal(err)
	}

	var defaultCount, otherCount int64
	if err := db.Raw("SELECT count(*) FROM search_fts WHERE namespace = ?", ctxns.DefaultNamespace).Scan(&defaultCount).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Raw("SELECT count(*) FROM search_fts WHERE namespace = ?", "ns-b").Scan(&otherCount).Error; err != nil {
		t.Fatal(err)
	}
	if defaultCount != 1 {
		t.Fatalf("expected one default namespace row, got %d", defaultCount)
	}
	if otherCount != 1 {
		t.Fatalf("expected ns-b rows preserved, got %d", otherCount)
	}
}

func TestSQLiteFTS_PurgeNamespace_RemovesOnlyTargetNamespace(t *testing.T) {
	db := setupTestDB(t)
	backend := NewSQLiteBackend()
	if err := backend.Migrate(db); err != nil {
		t.Fatal(err)
	}

	if err := db.Exec("INSERT INTO search_fts(node_id, content, language, namespace) VALUES (?, ?, ?, ?), (?, ?, ?, ?)", 1, "alpha", "go", "ns-a", 2, "beta", "go", "ns-b").Error; err != nil {
		t.Fatal(err)
	}

	if err := backend.PurgeNamespace(ctxns.WithNamespace(context.Background(), "ns-a"), db); err != nil {
		t.Fatal(err)
	}

	var countA, countB int64
	if err := db.Raw("SELECT count(*) FROM search_fts WHERE namespace = ?", "ns-a").Scan(&countA).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Raw("SELECT count(*) FROM search_fts WHERE namespace = ?", "ns-b").Scan(&countB).Error; err != nil {
		t.Fatal(err)
	}
	if countA != 0 {
		t.Fatalf("expected ns-a rows purged, got %d", countA)
	}
	if countB != 1 {
		t.Fatalf("expected ns-b rows preserved, got %d", countB)
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

func TestSQLiteFTS_Query_RejectsNonPositiveLimit(t *testing.T) {
	db := setupTestDB(t)
	backend := NewSQLiteBackend()
	if err := backend.Migrate(db); err != nil {
		t.Fatal(err)
	}

	if _, err := backend.Query(context.Background(), db, "sharedterm", 0); err == nil {
		t.Fatal("expected query with limit=0 to fail")
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

func TestSQLiteFTS_Rebuild_RollsBackDeleteOnInsertFailure(t *testing.T) {
	db := setupTestDB(t)
	backend := NewSQLiteBackend()
	if err := backend.Migrate(db); err != nil {
		t.Fatal(err)
	}

	seedNode := model.Node{QualifiedName: "pkg.Seed", Kind: model.NodeKindFunction, Name: "Seed", FilePath: "seed.go", StartLine: 1, EndLine: 1, Language: "go"}
	if err := db.Create(&seedNode).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.SearchDocument{NodeID: seedNode.ID, Content: "seed term", Language: "go"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := backend.Rebuild(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	originalInserter := backend.batchInserter
	defer func() { backend.batchInserter = originalInserter }()
	backend.batchInserter = func(ctx context.Context, tx *gorm.DB, tableName string, docs []model.SearchDocument) error {
		return errors.New("boom")
	}

	err := backend.Rebuild(context.Background(), db)
	if err == nil {
		t.Fatal("expected rebuild to fail when insert trigger aborts")
	}

	var count int64
	if err := db.Raw("SELECT count(*) FROM search_fts").Scan(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("expected previous FTS rows to survive rollback, got %d", count)
	}
}

func TestSQLiteFTS_Rebuild_NamespaceRollbackPreservesOtherRows(t *testing.T) {
	db := setupTestDB(t)
	backend := NewSQLiteBackend()
	if err := backend.Migrate(db); err != nil {
		t.Fatal(err)
	}

	nodeA := model.Node{Namespace: "ns-a", QualifiedName: "pkg.A", Kind: model.NodeKindFunction, Name: "A", FilePath: "a.go", StartLine: 1, EndLine: 1, Language: "go"}
	nodeB := model.Node{Namespace: "ns-b", QualifiedName: "pkg.B", Kind: model.NodeKindFunction, Name: "B", FilePath: "b.go", StartLine: 1, EndLine: 1, Language: "go"}
	if err := db.Create(&nodeA).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&nodeB).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.SearchDocument{Namespace: "ns-a", NodeID: nodeA.ID, Content: "term a", Language: "go"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.SearchDocument{Namespace: "ns-b", NodeID: nodeB.ID, Content: "term b", Language: "go"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := backend.Rebuild(ctxns.WithNamespace(context.Background(), "ns-a"), db); err != nil {
		t.Fatal(err)
	}
	if err := backend.Rebuild(ctxns.WithNamespace(context.Background(), "ns-b"), db); err != nil {
		t.Fatal(err)
	}
	originalInserter := backend.batchInserter
	defer func() { backend.batchInserter = originalInserter }()
	backend.batchInserter = func(ctx context.Context, tx *gorm.DB, tableName string, docs []model.SearchDocument) error {
		for _, doc := range docs {
			if doc.Namespace == "ns-a" {
				return errors.New("boom")
			}
		}
		return originalInserter(ctx, tx, tableName, docs)
	}
	err := backend.Rebuild(ctxns.WithNamespace(context.Background(), "ns-a"), db)
	if err == nil {
		t.Fatal("expected namespace-scoped rebuild to fail when ns-a insert trigger aborts")
	}

	var countA, countB int64
	if err := db.Raw("SELECT count(*) FROM search_fts WHERE namespace = ?", "ns-a").Scan(&countA).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Raw("SELECT count(*) FROM search_fts WHERE namespace = ?", "ns-b").Scan(&countB).Error; err != nil {
		t.Fatal(err)
	}
	if countA != 1 {
		t.Fatalf("expected ns-a rows to survive rollback, got %d", countA)
	}
	if countB != 1 {
		t.Fatalf("expected ns-b rows to remain untouched, got %d", countB)
	}
}

func TestSQLiteFTS_Rebuild_RollsBackWithOuterTransaction(t *testing.T) {
	db := setupTestDB(t)
	backend := NewSQLiteBackend()
	if err := backend.Migrate(db); err != nil {
		t.Fatal(err)
	}

	seedNode := model.Node{QualifiedName: "pkg.Seed", Kind: model.NodeKindFunction, Name: "Seed", FilePath: "seed.go", StartLine: 1, EndLine: 1, Language: "go"}
	newNode := model.Node{QualifiedName: "pkg.New", Kind: model.NodeKindFunction, Name: "New", FilePath: "new.go", StartLine: 1, EndLine: 1, Language: "go"}
	if err := db.Create(&seedNode).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.SearchDocument{NodeID: seedNode.ID, Content: "seedterm", Language: "go"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := backend.Rebuild(context.Background(), db); err != nil {
		t.Fatal(err)
	}

	err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&newNode).Error; err != nil {
			return err
		}
		if err := tx.Where("node_id = ?", seedNode.ID).Delete(&model.SearchDocument{}).Error; err != nil {
			return err
		}
		if err := tx.Create(&model.SearchDocument{NodeID: newNode.ID, Content: "newterm", Language: "go"}).Error; err != nil {
			return err
		}
		if err := backend.Rebuild(context.Background(), tx); err != nil {
			return err
		}
		return errors.New("force outer rollback")
	})
	if err == nil {
		t.Fatal("expected forced rollback")
	}

	results, err := backend.Query(context.Background(), db, "seedterm", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].QualifiedName != "pkg.Seed" {
		t.Fatalf("expected seed FTS row after outer rollback, got %+v", results)
	}
	results, err = backend.Query(context.Background(), db, "newterm", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatalf("expected new FTS row to roll back, got %+v", results)
	}
}
