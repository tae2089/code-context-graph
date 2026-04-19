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

	"github.com/tae2089/code-context-graph/internal/analysis/incremental"
	"github.com/tae2089/code-context-graph/internal/model"
	"github.com/tae2089/code-context-graph/internal/parse/treesitter"
	"github.com/tae2089/code-context-graph/internal/store/gormstore"
)

func setupUpdateTest(t *testing.T) (*Deps, *bytes.Buffer, *bytes.Buffer, *gorm.DB) {
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

	walker := treesitter.NewWalker(treesitter.GoSpec)
	deps.Store = st
	deps.Walkers = map[string]*treesitter.Walker{".go": walker}
	deps.Syncer = incremental.New(st, walker)

	return deps, stdout, stderr, db
}

func TestUpdateCommand_IncrementalSync(t *testing.T) {
	deps, stdout, stderr, db := setupUpdateTest(t)

	dir := t.TempDir()
	writeGoFile(t, dir, "a.go", `package a
func A() {}
`)

	err := executeCmd(deps, stdout, stderr, "build", dir)
	if err != nil {
		t.Fatalf("initial build: %v", err)
	}

	var countBefore int64
	db.Model(&model.Node{}).Count(&countBefore)
	if countBefore == 0 {
		t.Fatal("expected nodes after initial build")
	}

	writeGoFile(t, dir, "b.go", `package a
func B() {}
`)

	stdout.Reset()
	stderr.Reset()

	err = executeCmd(deps, stdout, stderr, "update", dir)
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	var countAfter int64
	db.Model(&model.Node{}).Count(&countAfter)
	if countAfter <= countBefore {
		t.Fatalf("expected more nodes after update: before=%d after=%d", countBefore, countAfter)
	}

	foundB := false
	var nodes []model.Node
	db.Find(&nodes)
	for _, n := range nodes {
		if n.Name == "B" {
			foundB = true
		}
	}
	if !foundB {
		t.Fatal("expected to find newly added function B")
	}
}

func TestUpdateCommand_ReportsAddedModifiedDeleted(t *testing.T) {
	deps, stdout, stderr, _ := setupUpdateTest(t)

	dir := t.TempDir()
	writeGoFile(t, dir, "x.go", `package x
func X() {}
`)

	err := executeCmd(deps, stdout, stderr, "build", dir)
	if err != nil {
		t.Fatalf("initial build: %v", err)
	}

	writeGoFile(t, dir, "x.go", `package x
func X2() {}
`)
	writeGoFile(t, dir, "y.go", `package x
func Y() {}
`)

	stdout.Reset()
	stderr.Reset()

	err = executeCmd(deps, stdout, stderr, "update", dir)
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, "Update complete") {
		t.Fatalf("expected 'Update complete' in output, got: %s", out)
	}
	if !strings.Contains(out, "added=") || !strings.Contains(out, "modified=") {
		t.Fatalf("expected added/modified stats in output, got: %s", out)
	}
}

func writeGoFileForUpdate(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}
