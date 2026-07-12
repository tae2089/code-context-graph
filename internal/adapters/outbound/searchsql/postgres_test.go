//go:build postgres

package searchsql

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	requestctx "github.com/tae2089/code-context-graph/internal/ctx"
	"github.com/tae2089/code-context-graph/internal/db/migration"
	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

type postgresINQueryCaptureLogger struct {
	logger.Interface
	needle string
	maxIDs int
	hits   int
}

func (l *postgresINQueryCaptureLogger) LogMode(level logger.LogLevel) logger.Interface { return l }
func (l *postgresINQueryCaptureLogger) Trace(ctx context.Context, begin time.Time, fc func() (string, int64), err error) {
	sql, _ := fc()
	if strings.Contains(sql, l.needle) && strings.Contains(sql, " IN (") {
		l.hits++
		ids := countPostgresSQLInList(sql)
		if ids > l.maxIDs {
			l.maxIDs = ids
		}
	}
}

func countPostgresSQLInList(sql string) int {
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

	// Reset to a clean schema and build it from the production migrations (the single source
	// of truth), rather than AutoMigrate + hand-written backend DDL.
	if err := db.Exec("DROP SCHEMA public CASCADE; CREATE SCHEMA public;").Error; err != nil {
		t.Fatalf("reset schema: %v", err)
	}
	if err := migration.RunMigrations(db, "postgres", ""); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	return db
}

func seedPostgresNodes(t *testing.T, db *gorm.DB) {
	t.Helper()
	nodes := []graph.Node{
		{QualifiedName: "pkg.AuthenticateUser", Kind: graph.NodeKindFunction, Name: "AuthenticateUser", FilePath: "auth.go", StartLine: 1, EndLine: 10, Language: "go"},
		{QualifiedName: "pkg.CreateSession", Kind: graph.NodeKindFunction, Name: "CreateSession", FilePath: "session.go", StartLine: 1, EndLine: 8, Language: "go"},
		{QualifiedName: "pkg.DeleteUser", Kind: graph.NodeKindFunction, Name: "DeleteUser", FilePath: "user.go", StartLine: 1, EndLine: 5, Language: "go"},
	}
	for i := range nodes {
		db.Create(&nodes[i])
	}

	docs := []graph.SearchDocument{
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

// disableTSVTrigger turns off the tsv BEFORE INSERT/UPDATE trigger so tests can stage
// stale tsv values by hand. Tables are dropped per test, so re-enabling is unnecessary.
func disableTSVTrigger(t *testing.T, db *gorm.DB) {
	t.Helper()
	if err := db.Exec("ALTER TABLE search_documents DISABLE TRIGGER trg_search_documents_tsv").Error; err != nil {
		t.Fatalf("disable tsv trigger: %v", err)
	}
}

func TestPostgresFTS_RebuildNodes_RefreshesOnlyScopedRows(t *testing.T) {
	db := setupPostgresDB(t)
	backend := NewPostgresBackend()
	if err := backend.Migrate(db); err != nil {
		t.Fatal(err)
	}

	changed := graph.Node{Namespace: requestctx.DefaultNamespace, QualifiedName: "pkg.Changed", Kind: graph.NodeKindFunction, Name: "Changed", FilePath: "changed.go", StartLine: 1, EndLine: 1, Language: "go"}
	untouched := graph.Node{Namespace: requestctx.DefaultNamespace, QualifiedName: "pkg.Untouched", Kind: graph.NodeKindFunction, Name: "Untouched", FilePath: "untouched.go", StartLine: 1, EndLine: 1, Language: "go"}
	foreign := graph.Node{Namespace: "ns-b", QualifiedName: "pkg.Foreign", Kind: graph.NodeKindFunction, Name: "Foreign", FilePath: "foreign.go", StartLine: 1, EndLine: 1, Language: "go"}
	for _, node := range []*graph.Node{&changed, &untouched, &foreign} {
		if err := db.Create(node).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Create(&graph.SearchDocument{Namespace: requestctx.DefaultNamespace, NodeID: changed.ID, Content: "fresh changed", Language: "go"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&graph.SearchDocument{Namespace: requestctx.DefaultNamespace, NodeID: untouched.ID, Content: "fresh untouched", Language: "go"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&graph.SearchDocument{Namespace: "ns-b", NodeID: foreign.ID, Content: "fresh foreign", Language: "go"}).Error; err != nil {
		t.Fatal(err)
	}
	// The tsv BEFORE UPDATE trigger would recompute tsv from content and overwrite the
	// manual stale marker; disable it so the stale state actually exists and RebuildNodes'
	// own UPDATE statement is what gets verified.
	disableTSVTrigger(t, db)
	if err := db.Exec("UPDATE search_documents SET tsv = to_tsvector('simple', 'stale')").Error; err != nil {
		t.Fatal(err)
	}

	if err := backend.RebuildNodes(context.Background(), db, []uint{changed.ID, foreign.ID}); err != nil {
		t.Fatal(err)
	}

	var changedMatches, untouchedMatches, foreignMatches int64
	db.Raw("SELECT COUNT(*) FROM search_documents WHERE node_id = ? AND tsv @@ to_tsquery('simple', 'fresh')", changed.ID).Scan(&changedMatches)
	db.Raw("SELECT COUNT(*) FROM search_documents WHERE node_id = ? AND tsv @@ to_tsquery('simple', 'fresh')", untouched.ID).Scan(&untouchedMatches)
	db.Raw("SELECT COUNT(*) FROM search_documents WHERE node_id = ? AND tsv @@ to_tsquery('simple', 'fresh')", foreign.ID).Scan(&foreignMatches)
	if changedMatches != 1 {
		t.Fatalf("expected changed row tsv refreshed, got %d", changedMatches)
	}
	if untouchedMatches != 0 {
		t.Fatalf("expected untouched row tsv preserved, got %d", untouchedMatches)
	}
	if foreignMatches != 0 {
		t.Fatalf("expected foreign namespace row tsv preserved, got %d", foreignMatches)
	}
}

func TestPostgresFTS_RebuildNodes_EmptyScopeIsNoOp(t *testing.T) {
	db := setupPostgresDB(t)
	backend := NewPostgresBackend()
	if err := backend.Migrate(db); err != nil {
		t.Fatal(err)
	}
	node := graph.Node{Namespace: requestctx.DefaultNamespace, QualifiedName: "pkg.Keep", Kind: graph.NodeKindFunction, Name: "Keep", FilePath: "keep.go", StartLine: 1, EndLine: 1, Language: "go"}
	if err := db.Create(&node).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&graph.SearchDocument{Namespace: requestctx.DefaultNamespace, NodeID: node.ID, Content: "fresh keep", Language: "go"}).Error; err != nil {
		t.Fatal(err)
	}
	// See TestPostgresFTS_RebuildNodes_RefreshesOnlyScopedRows: the trigger must be off
	// for the stale marker to persist.
	disableTSVTrigger(t, db)
	if err := db.Exec("UPDATE search_documents SET tsv = to_tsvector('simple', 'stale')").Error; err != nil {
		t.Fatal(err)
	}
	if err := backend.RebuildNodes(context.Background(), db, nil); err != nil {
		t.Fatal(err)
	}
	var freshMatches int64
	db.Raw("SELECT COUNT(*) FROM search_documents WHERE node_id = ? AND tsv @@ to_tsquery('simple', 'fresh')", node.ID).Scan(&freshMatches)
	if freshMatches != 0 {
		t.Fatalf("expected empty scope to leave stale tsv, got fresh matches %d", freshMatches)
	}
}

func TestPostgresFTS_RebuildNodes_ChunksLargeNodeScopes(t *testing.T) {
	capture := &postgresINQueryCaptureLogger{Interface: logger.Discard, needle: "search_documents"}
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		dsn = "host=localhost user=postgres password=postgres dbname=ccg_test port=5432 sslmode=disable"
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: capture})
	if err != nil {
		t.Skipf("PostgreSQL not available: %v", err)
	}
	if err := db.Exec("DROP SCHEMA public CASCADE; CREATE SCHEMA public;").Error; err != nil {
		t.Fatalf("reset schema: %v", err)
	}
	if err := migration.RunMigrations(db, "postgres", ""); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	backend := NewPostgresBackend()

	nodeIDs := make([]uint, 0, scopedRebuildChunkSize+1)
	for i := range scopedRebuildChunkSize + 1 {
		node := graph.Node{Namespace: requestctx.DefaultNamespace, QualifiedName: fmt.Sprintf("pkg.Node%d", i), Kind: graph.NodeKindFunction, Name: fmt.Sprintf("Node%d", i), FilePath: fmt.Sprintf("node-%d.go", i), StartLine: 1, EndLine: 1, Language: "go"}
		if err := db.Create(&node).Error; err != nil {
			t.Fatalf("create node %d: %v", i, err)
		}
		nodeIDs = append(nodeIDs, node.ID)
		if err := db.Create(&graph.SearchDocument{Namespace: requestctx.DefaultNamespace, NodeID: node.ID, Content: fmt.Sprintf("fresh node %d", i), Language: "go"}).Error; err != nil {
			t.Fatalf("create doc %d: %v", i, err)
		}
	}

	if err := backend.RebuildNodes(context.Background(), db, nodeIDs); err != nil {
		t.Fatal(err)
	}
	if capture.maxIDs > scopedRebuildChunkSize {
		t.Fatalf("expected scoped tsv IN queries to be chunked to <= %d IDs, got %d", scopedRebuildChunkSize, capture.maxIDs)
	}
	if capture.hits < 2 {
		t.Fatalf("expected multiple scoped tsv IN queries, got %d", capture.hits)
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

func TestPostgresFTS_Query_FuzzyTypoFallsBackToTrigram(t *testing.T) {
	db := setupPostgresDB(t)
	seedPostgresNodes(t, db)

	backend := NewPostgresBackend()
	if err := backend.Migrate(db); err != nil {
		t.Fatal(err)
	}
	if err := backend.Rebuild(context.Background(), db); err != nil {
		t.Fatal(err)
	}

	// "authentcate" is a typo (missing 'i') that exact tsquery cannot match;
	// the pg_trgm supplement should still surface AuthenticateUser by symbol-name similarity.
	nodes, err := backend.Query(context.Background(), db, "authentcate", 10)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, n := range nodes {
		if n.QualifiedName == "pkg.AuthenticateUser" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected fuzzy trigram match to surface pkg.AuthenticateUser for typo query, got %+v", nodes)
	}
}

func TestPostgresFTS_Query_NamespaceIsolation(t *testing.T) {
	db := setupPostgresDB(t)
	backend := NewPostgresBackend()
	if err := backend.Migrate(db); err != nil {
		t.Fatal(err)
	}

	nodeA := graph.Node{Namespace: "ns-a", QualifiedName: "pkg.A", Kind: graph.NodeKindFunction, Name: "A", FilePath: "a.go", StartLine: 1, EndLine: 2, Language: "go"}
	nodeB := graph.Node{Namespace: "ns-b", QualifiedName: "pkg.B", Kind: graph.NodeKindFunction, Name: "B", FilePath: "b.go", StartLine: 1, EndLine: 2, Language: "go"}
	db.Create(&nodeA)
	db.Create(&nodeB)
	db.Create(&graph.SearchDocument{Namespace: "ns-a", NodeID: nodeA.ID, Content: "sharedterm alpha", Language: "go"})
	db.Create(&graph.SearchDocument{Namespace: "ns-b", NodeID: nodeB.ID, Content: "sharedterm beta", Language: "go"})

	if err := backend.Rebuild(requestctx.WithNamespace(context.Background(), "ns-a"), db); err != nil {
		t.Fatal(err)
	}
	if err := backend.Rebuild(requestctx.WithNamespace(context.Background(), "ns-b"), db); err != nil {
		t.Fatal(err)
	}

	resultsA, err := backend.Query(requestctx.WithNamespace(context.Background(), "ns-a"), db, "sharedterm", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(resultsA) != 1 || resultsA[0].Namespace != "ns-a" {
		t.Fatalf("expected only ns-a result, got %#v", resultsA)
	}

	resultsB, err := backend.Query(requestctx.WithNamespace(context.Background(), "ns-b"), db, "sharedterm", 10)
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

	defaultNode := graph.Node{Namespace: requestctx.DefaultNamespace, QualifiedName: "pkg.Default", Kind: graph.NodeKindFunction, Name: "Default", FilePath: "default.go", StartLine: 1, EndLine: 2, Language: "go"}
	otherNode := graph.Node{Namespace: "ns-b", QualifiedName: "pkg.Other", Kind: graph.NodeKindFunction, Name: "Other", FilePath: "other.go", StartLine: 1, EndLine: 2, Language: "go"}
	db.Create(&defaultNode)
	db.Create(&otherNode)
	db.Create(&graph.SearchDocument{Namespace: requestctx.DefaultNamespace, NodeID: defaultNode.ID, Content: "sharedterm default", Language: "go"})
	db.Create(&graph.SearchDocument{Namespace: "ns-b", NodeID: otherNode.ID, Content: "sharedterm other", Language: "go"})

	if err := backend.Rebuild(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if err := backend.Rebuild(requestctx.WithNamespace(context.Background(), "ns-b"), db); err != nil {
		t.Fatal(err)
	}

	results, err := backend.Query(context.Background(), db, "sharedterm", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Namespace != requestctx.DefaultNamespace {
		t.Fatalf("expected only default namespace result, got %#v", results)
	}
}

func TestPostgresFTS_Query_DefensivelyFiltersNodeNamespace(t *testing.T) {
	db := setupPostgresDB(t)
	backend := NewPostgresBackend()
	if err := backend.Migrate(db); err != nil {
		t.Fatal(err)
	}

	goodNode := graph.Node{Namespace: "ns-a", QualifiedName: "pkg.Safe", Kind: graph.NodeKindFunction, Name: "Safe", FilePath: "safe.go", StartLine: 1, EndLine: 2, Language: "go"}
	foreignNode := graph.Node{Namespace: "ns-b", QualifiedName: "pkg.Foreign", Kind: graph.NodeKindFunction, Name: "Foreign", FilePath: "foreign.go", StartLine: 1, EndLine: 2, Language: "go"}
	if err := db.Create(&goodNode).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&foreignNode).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&graph.SearchDocument{Namespace: "ns-a", NodeID: goodNode.ID, Content: "sharedterm safe", Language: "go"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&graph.SearchDocument{Namespace: "ns-a", NodeID: foreignNode.ID, Content: "sharedterm leaked", Language: "go"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := backend.Rebuild(requestctx.WithNamespace(context.Background(), "ns-a"), db); err != nil {
		t.Fatal(err)
	}

	results, err := backend.Query(requestctx.WithNamespace(context.Background(), "ns-a"), db, "sharedterm", 10)
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

func TestPostgresFTS_PurgeNamespace_NoOp(t *testing.T) {
	db := setupPostgresDB(t)
	backend := NewPostgresBackend()
	if err := backend.Migrate(db); err != nil {
		t.Fatal(err)
	}
	if err := backend.PurgeNamespace(requestctx.WithNamespace(context.Background(), "ns-a"), db); err != nil {
		t.Fatalf("expected no-op purge to succeed: %v", err)
	}
}
