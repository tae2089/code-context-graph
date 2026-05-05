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

	"github.com/tae2089/code-context-graph/internal/ctxns"
	"github.com/tae2089/code-context-graph/internal/model"
	"github.com/tae2089/code-context-graph/internal/store/gormstore"
)

func setupDocsTest(t *testing.T) (*Deps, *bytes.Buffer, *bytes.Buffer, *gorm.DB) {
	t.Helper()
	deps, stdout, stderr := newTestDeps()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	st := gormstore.New(db)
	if err := st.AutoMigrate(); err != nil {
		t.Fatal(err)
	}

	deps.DB = db
	deps.Store = st
	return deps, stdout, stderr, db
}

func TestDocsCommand_WritesIndexAndFileDocs(t *testing.T) {
	deps, stdout, stderr, db := setupDocsTest(t)

	fnNode := model.Node{
		Namespace:     ctxns.DefaultNamespace,
		QualifiedName: "main.go::main",
		Kind:          model.NodeKindFunction,
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

func TestDocsCommand_BuildsRAGIndexByDefault(t *testing.T) {
	deps, stdout, stderr, db := setupDocsTest(t)

	db.Create(&model.Node{
		Namespace:     ctxns.DefaultNamespace,
		QualifiedName: "pkg.Main",
		Kind:          model.NodeKindFunction,
		Name:          "Main",
		FilePath:      "pkg/main.go",
		StartLine:     1, EndLine: 5,
		Hash: "h1", Language: "go",
	})

	outDir := t.TempDir()
	indexDir := t.TempDir()
	if err := executeCmd(deps, stdout, stderr, "docs", "--out", outDir, "--rag-index-dir", indexDir); err != nil {
		t.Fatalf("docs: %v", err)
	}

	if _, err := os.Stat(filepath.Join(indexDir, "doc-index.json")); err != nil {
		t.Fatalf("expected doc-index.json to be created: %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, "Communities rebuilt for RAG index") {
		t.Fatalf("expected community rebuild output, got:\n%s", out)
	}
	if !strings.Contains(out, "RAG index written: 1 communities, 1 files") {
		t.Fatalf("expected rag index output, got:\n%s", out)
	}
}

func TestDocsCommand_BuildsNamespaceIndexes(t *testing.T) {
	deps, stdout, stderr, db := setupDocsTest(t)

	db.Create(&model.Node{
		Namespace:     "alpha",
		QualifiedName: "pkg.Main",
		Kind:          model.NodeKindFunction,
		Name:          "Main",
		FilePath:      "pkg/main.go",
		StartLine:     1, EndLine: 5,
		Hash: "h1", Language: "go",
	})

	outDir := t.TempDir()
	indexDir := t.TempDir()
	if err := executeCmd(deps, stdout, stderr, "--namespace", "alpha", "docs", "--out", outDir, "--rag-index-dir", indexDir); err != nil {
		t.Fatalf("docs: %v", err)
	}

	if _, err := os.Stat(filepath.Join(indexDir, "alpha", "doc-index.json")); err != nil {
		t.Fatalf("expected namespace doc-index.json to be created: %v", err)
	}
	if _, err := os.Stat(filepath.Join(indexDir, "alpha", "wiki-index.json")); err != nil {
		t.Fatalf("expected namespace wiki-index.json to be created: %v", err)
	}
	if _, err := os.Stat(filepath.Join(indexDir, "doc-index.json")); !os.IsNotExist(err) {
		t.Fatalf("default doc-index.json should not be written for named namespace, stat err=%v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, "Wiki index written:") || !strings.Contains(out, "RAG index written:") {
		t.Fatalf("expected index output, got:\n%s", out)
	}
}

func TestDocsCommand_RAGFlagCanDisableIndexBuild(t *testing.T) {
	deps, stdout, stderr, db := setupDocsTest(t)

	db.Create(&model.Node{
		Namespace:     ctxns.DefaultNamespace,
		QualifiedName: "pkg.Main",
		Kind:          model.NodeKindFunction,
		Name:          "Main",
		FilePath:      "pkg/main.go",
		StartLine:     1, EndLine: 5,
		Hash: "h1", Language: "go",
	})

	outDir := t.TempDir()
	indexDir := t.TempDir()
	if err := executeCmd(deps, stdout, stderr, "docs", "--out", outDir, "--rag=false", "--rag-index-dir", indexDir); err != nil {
		t.Fatalf("docs --rag=false: %v", err)
	}

	if _, err := os.Stat(filepath.Join(indexDir, "doc-index.json")); !os.IsNotExist(err) {
		t.Fatalf("doc-index.json should not be created when --rag=false, stat err=%v", err)
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

	oldNode := model.Node{Namespace: ctxns.DefaultNamespace, QualifiedName: "pkg.Old", Kind: model.NodeKindFunction, Name: "Old", FilePath: "pkg/old.go", StartLine: 1, EndLine: 5, Hash: "h1", Language: "go"}
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
	db.Create(&model.Node{Namespace: ctxns.DefaultNamespace, QualifiedName: "pkg.New", Kind: model.NodeKindFunction, Name: "New", FilePath: "pkg/new.go", StartLine: 1, EndLine: 5, Hash: "h2", Language: "go"})

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

	oldNode := model.Node{Namespace: ctxns.DefaultNamespace, QualifiedName: "pkg.Old", Kind: model.NodeKindFunction, Name: "Old", FilePath: "pkg/old.go", StartLine: 1, EndLine: 5, Hash: "h1", Language: "go"}
	db.Create(&oldNode)
	if err := executeCmd(deps, stdout, stderr, "docs", "--out", outDir); err != nil {
		t.Fatalf("initial docs: %v", err)
	}
	db.Delete(&oldNode)
	db.Create(&model.Node{Namespace: ctxns.DefaultNamespace, QualifiedName: "pkg.New", Kind: model.NodeKindFunction, Name: "New", FilePath: "pkg/new.go", StartLine: 1, EndLine: 5, Hash: "h2", Language: "go"})

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
	db.Create(&model.Node{Namespace: "alpha", QualifiedName: "alpha.Foo", Kind: model.NodeKindFunction, Name: "Foo", FilePath: "alpha/foo.go", StartLine: 1, EndLine: 5, Hash: "h1", Language: "go"})
	db.Create(&model.Node{Namespace: "beta", QualifiedName: "beta.Bar", Kind: model.NodeKindFunction, Name: "Bar", FilePath: "beta/bar.go", StartLine: 1, EndLine: 5, Hash: "h2", Language: "go"})
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
