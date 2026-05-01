package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/tae2089/code-context-graph/internal/ctxns"
	"github.com/tae2089/code-context-graph/internal/model"
	"github.com/tae2089/code-context-graph/internal/store/gormstore"
	"github.com/tae2089/code-context-graph/internal/store/search"
)

type spySearchBackend struct {
	queryFn func(ctx context.Context, db *gorm.DB, query string, limit int) ([]model.Node, error)
}

func (s *spySearchBackend) Migrate(db *gorm.DB) error                      { return nil }
func (s *spySearchBackend) Rebuild(ctx context.Context, db *gorm.DB) error { return nil }
func (s *spySearchBackend) Query(ctx context.Context, db *gorm.DB, query string, limit int) ([]model.Node, error) {
	if s.queryFn != nil {
		return s.queryFn(ctx, db, query, limit)
	}
	return nil, nil
}

func setupSearchTest(t *testing.T) (*Deps, *bytes.Buffer, *bytes.Buffer, *gorm.DB) {
	t.Helper()
	deps, stdout, stderr := newTestDeps()

	db, err := gorm.Open(sqlite.Open(":memory:?_pragma=journal_mode(WAL)"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatal(err)
	}

	st := gormstore.New(db)
	if err := st.AutoMigrate(); err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.SearchDocument{}); err != nil {
		t.Fatal(err)
	}

	sb := search.NewSQLiteBackend()
	if err := sb.Migrate(db); err != nil {
		if errors.Is(err, search.ErrFTS5NotAvailable) {
			t.Skip("fts5 module not available, skipping test")
		}
		t.Fatal(err)
	}

	deps.DB = db
	deps.Store = st
	deps.SearchBackend = sb

	return deps, stdout, stderr, db
}

func seedSearchData(t *testing.T, db *gorm.DB) {
	t.Helper()
	ctx := context.Background()

	nodes := []model.Node{
		{Name: "Hello", QualifiedName: "pkg.Hello", Kind: model.NodeKindFunction, FilePath: "hello.go", StartLine: 3, EndLine: 5, Language: "go"},
		{Name: "World", QualifiedName: "pkg.World", Kind: model.NodeKindFunction, FilePath: "world.go", StartLine: 1, EndLine: 3, Language: "go"},
		{Name: "Foo", QualifiedName: "pkg.Foo", Kind: model.NodeKindFunction, FilePath: "foo.go", StartLine: 1, EndLine: 2, Language: "go"},
	}
	if err := db.WithContext(ctx).Create(&nodes).Error; err != nil {
		t.Fatal(err)
	}

	docs := []model.SearchDocument{
		{Namespace: nodes[0].Namespace, NodeID: nodes[0].ID, Content: "Hello function says hello", Language: "go"},
		{Namespace: nodes[1].Namespace, NodeID: nodes[1].ID, Content: "World function says world", Language: "go"},
		{Namespace: nodes[2].Namespace, NodeID: nodes[2].ID, Content: "Foo function does foo stuff", Language: "go"},
	}
	if err := db.WithContext(ctx).Create(&docs).Error; err != nil {
		t.Fatal(err)
	}

	sb := search.NewSQLiteBackend()
	if err := sb.Rebuild(ctx, db); err != nil {
		t.Fatal(err)
	}
}

func TestSearchCommand_FindsResults(t *testing.T) {
	deps, stdout, stderr, db := setupSearchTest(t)
	seedSearchData(t, db)

	stdout.Reset()

	err := executeCmd(deps, stdout, stderr, "search", "Hello")
	if err != nil {
		t.Fatalf("search: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, "pkg.Hello") {
		t.Fatalf("expected pkg.Hello in output, got: %s", out)
	}
}

func TestSearchCommand_NoResults(t *testing.T) {
	deps, stdout, stderr, db := setupSearchTest(t)
	seedSearchData(t, db)

	stdout.Reset()

	err := executeCmd(deps, stdout, stderr, "search", "zzzznotfound")
	if err != nil {
		t.Fatalf("search: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, "No results") {
		t.Fatalf("expected 'No results', got: %s", out)
	}
}

func TestSearchCommand_LimitFlag(t *testing.T) {
	deps, stdout, stderr, db := setupSearchTest(t)
	seedSearchData(t, db)

	stdout.Reset()

	err := executeCmd(deps, stdout, stderr, "search", "--limit", "1", "function")
	if err != nil {
		t.Fatalf("search: %v", err)
	}

	out := stdout.String()
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected 1 result with --limit 1, got %d: %s", len(lines), out)
	}
}

func TestSearchCommand_PathFilter_IncludesMatch(t *testing.T) {
	deps, stdout, stderr, db := setupSearchTest(t)

	ctx := context.Background()
	nodes := []model.Node{
		{Name: "AuthLogin", QualifiedName: "internal/auth/login.go::AuthLogin", Kind: model.NodeKindFunction, FilePath: "internal/auth/login.go", StartLine: 1, EndLine: 5, Language: "go"},
		{Name: "PayPay", QualifiedName: "internal/payment/pay.go::PayPay", Kind: model.NodeKindFunction, FilePath: "internal/payment/pay.go", StartLine: 1, EndLine: 5, Language: "go"},
	}
	db.WithContext(ctx).Create(&nodes)

	docs := []model.SearchDocument{
		{Namespace: nodes[0].Namespace, NodeID: nodes[0].ID, Content: "handle user login", Language: "go"},
		{Namespace: nodes[1].Namespace, NodeID: nodes[1].ID, Content: "handle payment", Language: "go"},
	}
	db.WithContext(ctx).Create(&docs)
	search.NewSQLiteBackend().Rebuild(ctx, db)

	if err := executeCmd(deps, stdout, stderr, "search", "--path", "internal/auth", "handle"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, "AuthLogin") {
		t.Errorf("expected AuthLogin in output, got:\n%s", out)
	}
	if strings.Contains(out, "PayPay") {
		t.Errorf("PayPay should be excluded by --path filter, got:\n%s", out)
	}
}

func TestSearchCommand_PathFilter_NoMatch(t *testing.T) {
	deps, stdout, stderr, db := setupSearchTest(t)
	seedSearchData(t, db)

	if err := executeCmd(deps, stdout, stderr, "search", "--path", "internal/nonexistent", "Hello"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(stdout.String(), "No results") {
		t.Errorf("expected 'No results' for unmatched path, got:\n%s", stdout.String())
	}
}

func TestSearchCommand_NamespaceIsolation(t *testing.T) {
	deps, _, _, db := setupSearchTest(t)
	ctxA := ctxns.WithNamespace(context.Background(), "ns-a")
	ctxB := ctxns.WithNamespace(context.Background(), "ns-b")

	nodeA := model.Node{Namespace: "ns-a", Name: "SearchA", QualifiedName: "pkg.SearchA", Kind: model.NodeKindFunction, FilePath: "a.go", StartLine: 1, EndLine: 2, Language: "go"}
	nodeB := model.Node{Namespace: "ns-b", Name: "SearchB", QualifiedName: "pkg.SearchB", Kind: model.NodeKindFunction, FilePath: "b.go", StartLine: 1, EndLine: 2, Language: "go"}
	if err := db.Create(&nodeA).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&nodeB).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.SearchDocument{Namespace: "ns-a", NodeID: nodeA.ID, Content: "sharedterm alpha", Language: "go"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.SearchDocument{Namespace: "ns-b", NodeID: nodeB.ID, Content: "sharedterm beta", Language: "go"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := deps.SearchBackend.Rebuild(ctxA, db); err != nil {
		t.Fatal(err)
	}
	if err := deps.SearchBackend.Rebuild(ctxB, db); err != nil {
		t.Fatal(err)
	}

	resultsA, err := deps.SearchBackend.Query(ctxA, db, "sharedterm", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(resultsA) != 1 || resultsA[0].Namespace != "ns-a" {
		t.Fatalf("expected only ns-a result, got %#v", resultsA)
	}

	resultsB, err := deps.SearchBackend.Query(ctxB, db, "sharedterm", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(resultsB) != 1 || resultsB[0].Namespace != "ns-b" {
		t.Fatalf("expected only ns-b result, got %#v", resultsB)
	}
}

func TestSearchCommand_SpecialCharactersDoNotError(t *testing.T) {
	deps, stdout, stderr, db := setupSearchTest(t)
	seedSearchData(t, db)

	for _, query := range []string{"func(x)", "foo:bar", "hello-world", "\"unterminated"} {
		stdout.Reset()
		stderr.Reset()
		if err := executeCmd(deps, stdout, stderr, "search", query); err != nil {
			t.Fatalf("search %q returned error: %v", query, err)
		}
	}
}

func TestSearchCommand_RejectsNonPositiveLimit(t *testing.T) {
	for _, limit := range []string{"0", "-5"} {
		deps, stdout, stderr, _ := setupSearchTest(t)
		called := false
		deps.SearchBackend = &spySearchBackend{queryFn: func(ctx context.Context, db *gorm.DB, query string, queryLimit int) ([]model.Node, error) {
			called = true
			return nil, nil
		}}

		err := executeCmd(deps, stdout, stderr, "search", "--limit", limit, "hello")
		if err == nil || !strings.Contains(err.Error(), "limit must be > 0") {
			t.Fatalf("expected limit validation error for %s, got %v", limit, err)
		}
		if called {
			t.Fatalf("search backend should not be called for invalid limit %s", limit)
		}
	}
}

func TestSearchCommand_UsesCommandContext(t *testing.T) {
	deps, stdout, stderr, _ := setupSearchTest(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	deps.SearchBackend = &spySearchBackend{queryFn: func(ctx context.Context, db *gorm.DB, query string, queryLimit int) ([]model.Node, error) {
		if !errors.Is(ctx.Err(), context.Canceled) {
			t.Fatalf("expected canceled command context, got %v", ctx.Err())
		}
		return nil, ctx.Err()
	}}

	err := executeCmdWithContext(ctx, deps, stdout, stderr, "search", "hello")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context canceled, got %v", err)
	}
}
