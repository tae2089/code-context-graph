package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/tae2089/code-context-graph/internal/adapters/outbound/graphgorm"
	requestctx "github.com/tae2089/code-context-graph/internal/ctx"
	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

func TestDocsCmd_HelpDescribesCurrentOutputs(t *testing.T) {
	deps, _, _ := newTestDeps()
	root := NewRootCmd(deps)
	out := &bytes.Buffer{}
	root.SetOut(out)
	root.SetArgs([]string{"docs", "--help"})

	if err := root.Execute(); err != nil {
		t.Fatalf("docs help: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "Wiki index") {
		t.Fatalf("docs help should describe the Wiki index output, got:\n%s", got)
	}
	if strings.Contains(got, "default RAG index") {
		t.Fatalf("docs help should not advertise the removed RAG index, got:\n%s", got)
	}
}

func setupDocsTest(t *testing.T) (*Deps, *bytes.Buffer, *bytes.Buffer, *gorm.DB) {
	t.Helper()
	deps, stdout, stderr := newTestDeps()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	st := graphgorm.New(db)
	if err := st.AutoMigrate(); err != nil {
		t.Fatal(err)
	}

	deps.Store = st
	deps.Docs = st
	deps.Wiki = st
	return deps, stdout, stderr, db
}

func TestDocsCommand_WritesIndexAndFileDocs(t *testing.T) {
	deps, stdout, stderr, db := setupDocsTest(t)

	fnNode := graph.Node{
		Namespace:     requestctx.DefaultNamespace,
		QualifiedName: "main.go::main",
		Kind:          graph.NodeKindFunction,
		Name:          "main",
		FilePath:      "main.go",
		StartLine:     1, EndLine: 5,
		Hash: "abc", Language: "go",
	}
	db.Create(&fnNode)

	outDir := t.TempDir()
	if err := executeCmd(deps, stdout, stderr, "docs", "--out", outDir); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := os.Stat(filepath.Join(outDir, "index.md")); os.IsNotExist(err) {
		t.Fatal("expected index.md to be created")
	}

	if _, err := os.Stat(filepath.Join(outDir, "main.go.md")); os.IsNotExist(err) {
		t.Fatal("expected main.go.md to be created")
	}

	out := stdout.String()
	if out == "" {
		t.Fatal("expected output message from docs command")
	}
}

func TestDocsCommand_NoDB(t *testing.T) {
	deps, stdout, stderr := newTestDeps()
	// deps.DB == nil

	outDir := t.TempDir()
	err := executeCmd(deps, stdout, stderr, "docs", "--out", outDir)
	if err == nil {
		t.Fatal("expected error when DB is nil")
	}
}

func TestDocsCommand_PrunesManagedStaleDocsByDefault(t *testing.T) {
	deps, stdout, stderr, db := setupDocsTest(t)
	outDir := t.TempDir()

	oldNode := graph.Node{Namespace: requestctx.DefaultNamespace, QualifiedName: "pkg.Old", Kind: graph.NodeKindFunction, Name: "Old", FilePath: "pkg/old.go", StartLine: 1, EndLine: 5, Hash: "h1", Language: "go"}
	db.Create(&oldNode)
	if err := executeCmd(deps, stdout, stderr, "docs", "--out", outDir); err != nil {
		t.Fatalf("initial docs: %v", err)
	}

	userDoc := filepath.Join(outDir, "pkg", "user.go.md")
	if err := os.MkdirAll(filepath.Dir(userDoc), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(userDoc, []byte("# user doc\n"), 0644); err != nil {
		t.Fatal(err)
	}
	db.Delete(&oldNode)
	db.Create(&graph.Node{Namespace: requestctx.DefaultNamespace, QualifiedName: "pkg.New", Kind: graph.NodeKindFunction, Name: "New", FilePath: "pkg/new.go", StartLine: 1, EndLine: 5, Hash: "h2", Language: "go"})

	stdout.Reset()
	stderr.Reset()
	if err := executeCmd(deps, stdout, stderr, "docs", "--out", outDir); err != nil {
		t.Fatalf("second docs: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outDir, "pkg", "old.go.md")); !os.IsNotExist(err) {
		t.Fatalf("managed stale doc should be pruned by default, stat err=%v", err)
	}
	if _, err := os.Stat(userDoc); err != nil {
		t.Fatalf("user doc must survive prune: %v", err)
	}
}

func TestDocsCommand_PruneFlagCanDisableCleanup(t *testing.T) {
	deps, stdout, stderr, db := setupDocsTest(t)
	outDir := t.TempDir()

	oldNode := graph.Node{Namespace: requestctx.DefaultNamespace, QualifiedName: "pkg.Old", Kind: graph.NodeKindFunction, Name: "Old", FilePath: "pkg/old.go", StartLine: 1, EndLine: 5, Hash: "h1", Language: "go"}
	db.Create(&oldNode)
	if err := executeCmd(deps, stdout, stderr, "docs", "--out", outDir); err != nil {
		t.Fatalf("initial docs: %v", err)
	}
	db.Delete(&oldNode)
	db.Create(&graph.Node{Namespace: requestctx.DefaultNamespace, QualifiedName: "pkg.New", Kind: graph.NodeKindFunction, Name: "New", FilePath: "pkg/new.go", StartLine: 1, EndLine: 5, Hash: "h2", Language: "go"})

	stdout.Reset()
	stderr.Reset()
	if err := executeCmd(deps, stdout, stderr, "docs", "--out", outDir, "--prune=false"); err != nil {
		t.Fatalf("docs --prune=false: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outDir, "pkg", "old.go.md")); err != nil {
		t.Fatalf("managed stale doc should remain when prune disabled: %v", err)
	}
}

func TestDocsCommand_RespectsNamespace(t *testing.T) {
	deps, stdout, stderr, db := setupDocsTest(t)
	db.Create(&graph.Node{Namespace: "alpha", QualifiedName: "alpha.Foo", Kind: graph.NodeKindFunction, Name: "Foo", FilePath: "alpha/foo.go", StartLine: 1, EndLine: 5, Hash: "h1", Language: "go"})
	db.Create(&graph.Node{Namespace: "beta", QualifiedName: "beta.Bar", Kind: graph.NodeKindFunction, Name: "Bar", FilePath: "beta/bar.go", StartLine: 1, EndLine: 5, Hash: "h2", Language: "go"})
	outDir := t.TempDir()

	if err := executeCmd(deps, stdout, stderr, "--namespace", "alpha", "docs", "--out", outDir); err != nil {
		t.Fatalf("docs: %v", err)
	}
	content, err := os.ReadFile(filepath.Join(outDir, "index.md"))
	if err != nil {
		t.Fatal(err)
	}
	got := string(content)
	if !strings.Contains(got, "Foo") || strings.Contains(got, "Bar") {
		t.Fatalf("namespace scope mismatch:\n%s", got)
	}
}
