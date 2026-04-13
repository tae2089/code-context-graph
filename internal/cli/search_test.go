package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/imtaebin/code-context-graph/internal/model"
	"github.com/imtaebin/code-context-graph/internal/store/gormstore"
	"github.com/imtaebin/code-context-graph/internal/store/search"
)

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
		if strings.Contains(err.Error(), "no such module: fts5") {
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
		{NodeID: nodes[0].ID, Content: "Hello function says hello", Language: "go"},
		{NodeID: nodes[1].ID, Content: "World function says world", Language: "go"},
		{NodeID: nodes[2].ID, Content: "Foo function does foo stuff", Language: "go"},
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
		{NodeID: nodes[0].ID, Content: "handle user login", Language: "go"},
		{NodeID: nodes[1].ID, Content: "handle payment", Language: "go"},
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
