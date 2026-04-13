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

	"github.com/imtaebin/code-context-graph/internal/model"
	"github.com/imtaebin/code-context-graph/internal/store/gormstore"
)

func setupIndexTest(t *testing.T) (*Deps, *bytes.Buffer, *bytes.Buffer, *gorm.DB) {
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
	return deps, stdout, stderr, db
}

func TestIndexCommand_GeneratesIndexMd(t *testing.T) {
	deps, stdout, stderr, db := setupIndexTest(t)

	db.Create(&model.Node{
		QualifiedName: "pkg/service.go::Handle",
		Kind:          model.NodeKindFunction,
		Name:          "Handle",
		FilePath:      "pkg/service.go",
		StartLine:     1, EndLine: 10,
		Hash: "h1", Language: "go",
	})

	outDir := t.TempDir()
	if err := executeCmd(deps, stdout, stderr, "index", "--out", outDir); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(outDir, "index.md"))
	if err != nil {
		t.Fatalf("expected index.md: %v", err)
	}

	got := string(content)
	for _, want := range []string{"# Code Context Index", "pkg/service.go", "Handle"} {
		if !strings.Contains(got, want) {
			t.Errorf("expected %q in index.md, got:\n%s", want, got)
		}
	}
}

func TestIndexCommand_DoesNotWriteFileDocs(t *testing.T) {
	deps, stdout, stderr, db := setupIndexTest(t)

	db.Create(&model.Node{
		QualifiedName: "pkg/service.go::Handle",
		Kind:          model.NodeKindFunction,
		Name:          "Handle",
		FilePath:      "pkg/service.go",
		StartLine:     1, EndLine: 10,
		Hash: "h1", Language: "go",
	})

	outDir := t.TempDir()
	if err := executeCmd(deps, stdout, stderr, "index", "--out", outDir); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// index.md exists
	if _, err := os.Stat(filepath.Join(outDir, "index.md")); err != nil {
		t.Errorf("expected index.md to exist: %v", err)
	}

	// pkg/service.go.md should NOT be created (index only)
	if _, err := os.Stat(filepath.Join(outDir, "pkg", "service.go.md")); err == nil {
		t.Errorf("expected pkg/service.go.md NOT to be created by index command")
	}
}

func TestIndexCommand_NilDB_Fails(t *testing.T) {
	deps, stdout, stderr := newTestDeps()
	// deps.DB is nil

	err := executeCmd(deps, stdout, stderr, "index", "--out", t.TempDir())
	if err == nil {
		t.Fatal("expected error when DB is nil")
	}
}
