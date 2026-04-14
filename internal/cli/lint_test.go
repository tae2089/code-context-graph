package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/imtaebin/code-context-graph/internal/model"
	"github.com/imtaebin/code-context-graph/internal/store/gormstore"
)

func setupLintTest(t *testing.T) (*Deps, *bytes.Buffer, *bytes.Buffer, *gorm.DB) {
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

func TestLintCommand_ReportsOrphan(t *testing.T) {
	deps, stdout, stderr, _ := setupLintTest(t)

	outDir := t.TempDir()
	orphanDir := filepath.Join(outDir, "pkg")
	os.MkdirAll(orphanDir, 0o755)
	os.WriteFile(filepath.Join(orphanDir, "gone.go.md"), []byte("# pkg/gone.go\n"), 0o644)

	if err := executeCmd(deps, stdout, stderr, "lint", "--out", outDir); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, "pkg/gone.go") {
		t.Errorf("expected orphan 'pkg/gone.go' in output, got:\n%s", out)
	}
	if !strings.Contains(out, "orphan") || !strings.Contains(out, "Orphan") {
		t.Errorf("expected 'orphan' label in output, got:\n%s", out)
	}
}

func TestLintCommand_ReportsMissing(t *testing.T) {
	deps, stdout, stderr, db := setupLintTest(t)

	db.Create(&model.Node{
		QualifiedName: "pkg/new.go::Fn",
		Kind:          model.NodeKindFunction,
		Name:          "Fn",
		FilePath:      "pkg/new.go",
		StartLine:     1, EndLine: 5,
		Hash: "h1", Language: "go",
	})

	outDir := t.TempDir()
	if err := executeCmd(deps, stdout, stderr, "lint", "--out", outDir); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, "pkg/new.go") {
		t.Errorf("expected missing 'pkg/new.go' in output, got:\n%s", out)
	}
}

func TestLintCommand_ReportsStale(t *testing.T) {
	deps, stdout, stderr, db := setupLintTest(t)

	db.Create(&model.Node{
		QualifiedName: "pkg/old.go::Fn",
		Kind:          model.NodeKindFunction,
		Name:          "Fn",
		FilePath:      "pkg/old.go",
		StartLine:     1, EndLine: 5,
		Hash: "h1", Language: "go",
	})

	outDir := t.TempDir()
	docDir := filepath.Join(outDir, "pkg")
	os.MkdirAll(docDir, 0o755)
	docPath := filepath.Join(docDir, "old.go.md")
	os.WriteFile(docPath, []byte("# pkg/old.go\n"), 0o644)
	oldTime := time.Now().Add(-24 * time.Hour)
	os.Chtimes(docPath, oldTime, oldTime)

	if err := executeCmd(deps, stdout, stderr, "lint", "--out", outDir); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, "pkg/old.go") {
		t.Errorf("expected stale 'pkg/old.go' in output, got:\n%s", out)
	}
}

func TestLintCommand_CleanReport(t *testing.T) {
	deps, stdout, stderr, db := setupLintTest(t)

	fn := model.Node{
		QualifiedName: "pkg/ok.go::Fn",
		Kind:          model.NodeKindFunction,
		Name:          "Fn",
		FilePath:      "pkg/ok.go",
		StartLine:     1, EndLine: 5,
		Hash: "h1", Language: "go",
	}
	db.Create(&fn)
	db.Create(&model.Annotation{
		NodeID: fn.ID,
		Tags:   []model.DocTag{{Kind: model.TagIntent, Value: "does something", Ordinal: 0}},
	})

	// Generate docs so everything is fresh
	outDir := t.TempDir()
	if err := executeCmd(deps, stdout, stderr, "docs", "--out", outDir); err != nil {
		t.Fatalf("docs: %v", err)
	}

	stdout.Reset()
	if err := executeCmd(deps, stdout, stderr, "lint", "--out", outDir); err != nil {
		t.Fatalf("lint: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, "clean") && !strings.Contains(out, "0 issues") {
		t.Errorf("expected clean report, got:\n%s", out)
	}
}

func TestLintCommand_ReportsUnannotated(t *testing.T) {
	deps, stdout, stderr, db := setupLintTest(t)

	db.Create(&model.Node{
		QualifiedName: "pkg/svc.go::Handle",
		Kind:          model.NodeKindFunction,
		Name:          "Handle",
		FilePath:      "pkg/svc.go",
		StartLine:     1, EndLine: 10,
		Hash: "h1", Language: "go",
	})

	outDir := t.TempDir()
	if err := executeCmd(deps, stdout, stderr, "lint", "--out", outDir); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, "pkg/svc.go::Handle") {
		t.Errorf("expected unannotated 'pkg/svc.go::Handle' in output, got:\n%s", out)
	}
	if !strings.Contains(out, "unannotated") || !strings.Contains(out, "Unannotated") {
		t.Errorf("expected 'unannotated' label in output, got:\n%s", out)
	}
}

func TestLintCommand_Strict_FailsOnIssues(t *testing.T) {
	deps, stdout, stderr, db := setupLintTest(t)

	db.Create(&model.Node{
		QualifiedName: "pkg/bare.go::Bare",
		Kind:          model.NodeKindFunction,
		Name:          "Bare",
		FilePath:      "pkg/bare.go",
		StartLine:     1, EndLine: 5,
		Hash: "h1", Language: "go",
	})

	outDir := t.TempDir()
	err := executeCmd(deps, stdout, stderr, "lint", "--out", outDir, "--strict")
	if err == nil {
		t.Fatal("expected error with --strict when issues exist")
	}
}

func TestLintCommand_Strict_PassesWhenClean(t *testing.T) {
	deps, stdout, stderr, db := setupLintTest(t)

	fn := model.Node{
		QualifiedName: "pkg/ok.go::Ok",
		Kind:          model.NodeKindFunction,
		Name:          "Ok",
		FilePath:      "pkg/ok.go",
		StartLine:     1, EndLine: 5,
		Hash: "h1", Language: "go",
	}
	db.Create(&fn)
	db.Create(&model.Annotation{
		NodeID: fn.ID,
		Tags:   []model.DocTag{{Kind: model.TagIntent, Value: "ok", Ordinal: 0}},
	})

	outDir := t.TempDir()
	// Generate docs first
	if err := executeCmd(deps, stdout, stderr, "docs", "--out", outDir); err != nil {
		t.Fatalf("docs: %v", err)
	}

	stdout.Reset()
	err := executeCmd(deps, stdout, stderr, "lint", "--out", outDir, "--strict")
	if err != nil {
		t.Fatalf("expected no error with --strict when clean, got: %v", err)
	}
}
