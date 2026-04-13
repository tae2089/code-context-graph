package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/imtaebin/code-context-graph/internal/model"
	"github.com/imtaebin/code-context-graph/internal/store/gormstore"
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

func TestDocsCommand_NoDB(t *testing.T) {
	deps, stdout, stderr := newTestDeps()
	// deps.DB == nil

	outDir := t.TempDir()
	err := executeCmd(deps, stdout, stderr, "docs", "--out", outDir)
	if err == nil {
		t.Fatal("expected error when DB is nil")
	}
}
