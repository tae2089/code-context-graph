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

	"github.com/tae2089/code-context-graph/internal/parse/treesitter"
	"github.com/tae2089/code-context-graph/internal/store/gormstore"
)

// setupIntegrationDeps creates a full deps set suitable for end-to-end tests.
func setupIntegrationDeps(t *testing.T) (*Deps, *bytes.Buffer, *bytes.Buffer) {
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
	deps.Walkers = map[string]*treesitter.Walker{
		".go": treesitter.NewWalker(treesitter.GoSpec),
	}

	return deps, stdout, stderr
}

// TestIntegration_BuildThenDocs exercises the full build → docs pipeline.
func TestIntegration_BuildThenDocs(t *testing.T) {
	deps, stdout, stderr := setupIntegrationDeps(t)

	srcDir := t.TempDir()
	writeGoFile(t, srcDir, "service.go", `package service

// @intent 사용자 서비스 진입점
func Handle() {}
`)

	// 1. Build
	if err := executeCmd(deps, stdout, stderr, "build", srcDir); err != nil {
		t.Fatalf("build: %v", err)
	}

	// 2. Docs
	outDir := t.TempDir()
	stdout.Reset()
	if err := executeCmd(deps, stdout, stderr, "docs", "--out", outDir); err != nil {
		t.Fatalf("docs: %v", err)
	}

	// index.md must exist
	indexContent, err := os.ReadFile(filepath.Join(outDir, "index.md"))
	if err != nil {
		t.Fatalf("expected index.md: %v", err)
	}
	if !strings.Contains(string(indexContent), "Handle") {
		t.Errorf("expected Handle in index.md, got:\n%s", indexContent)
	}

	// Per-file doc must exist
	fileDoc, err := os.ReadFile(filepath.Join(outDir, "service.go.md"))
	if err != nil {
		t.Fatalf("expected service.go.md: %v", err)
	}
	if !strings.Contains(string(fileDoc), "Handle") {
		t.Errorf("expected Handle in service.go.md, got:\n%s", fileDoc)
	}
}

// TestIntegration_BuildWithExcludeThenIndex verifies that excluded files
// do not appear in the regenerated index.
func TestIntegration_BuildWithExcludeThenIndex(t *testing.T) {
	deps, stdout, stderr := setupIntegrationDeps(t)

	srcDir := t.TempDir()
	writeGoFile(t, srcDir, "public.go", `package pub
func Pub() {}
`)

	vendorDir := filepath.Join(srcDir, "vendor")
	os.MkdirAll(vendorDir, 0o755)
	writeGoFile(t, vendorDir, "lib.go", `package lib
func Lib() {}
`)

	// Build without exclusion (both files indexed)
	if err := executeCmd(deps, stdout, stderr, "build", srcDir); err != nil {
		t.Fatalf("build: %v", err)
	}

	// Index with exclusion
	outDir := t.TempDir()
	stdout.Reset()
	if err := executeCmd(deps, stdout, stderr, "index", "--out", outDir, "--exclude", "vendor"); err != nil {
		t.Fatalf("index: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(outDir, "index.md"))
	if err != nil {
		t.Fatalf("expected index.md: %v", err)
	}

	got := string(content)
	if !strings.Contains(got, "Pub") {
		t.Errorf("expected Pub in index.md")
	}
	if strings.Contains(got, "Lib") {
		t.Errorf("vendor/lib.go::Lib should be excluded from index")
	}
}

// TestIntegration_NoRecursive ensures that --no-recursive only processes
// the top-level directory.
func TestIntegration_NoRecursive(t *testing.T) {
	deps, stdout, stderr := setupIntegrationDeps(t)

	srcDir := t.TempDir()
	writeGoFile(t, srcDir, "top.go", `package top
func Top() {}
`)

	nested := filepath.Join(srcDir, "nested")
	os.MkdirAll(nested, 0o755)
	writeGoFile(t, nested, "inner.go", `package inner
func Inner() {}
`)

	if err := executeCmd(deps, stdout, stderr, "build", "--no-recursive", srcDir); err != nil {
		t.Fatalf("build: %v", err)
	}

	outDir := t.TempDir()
	stdout.Reset()
	if err := executeCmd(deps, stdout, stderr, "index", "--out", outDir); err != nil {
		t.Fatalf("index: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(outDir, "index.md"))
	if err != nil {
		t.Fatalf("expected index.md: %v", err)
	}

	got := string(content)
	if !strings.Contains(got, "Top") {
		t.Errorf("expected Top in index.md")
	}
	if strings.Contains(got, "Inner") {
		t.Errorf("nested/inner.go::Inner should not appear with --no-recursive")
	}
}
