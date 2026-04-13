package docs

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/imtaebin/code-context-graph/internal/model"
	"github.com/imtaebin/code-context-graph/internal/store/gormstore"
)

func newLintTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	st := gormstore.New(db)
	if err := st.AutoMigrate(); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestLint_DetectsOrphanDocs(t *testing.T) {
	db := newLintTestDB(t)
	outDir := t.TempDir()

	// Create a doc file with no matching node in DB
	orphanDir := filepath.Join(outDir, "internal")
	os.MkdirAll(orphanDir, 0o755)
	os.WriteFile(filepath.Join(orphanDir, "deleted.go.md"), []byte("# internal/deleted.go\n"), 0o644)

	gen := &Generator{DB: db, OutDir: outDir}
	report, err := gen.Lint()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(report.Orphans) != 1 {
		t.Fatalf("expected 1 orphan, got %d: %v", len(report.Orphans), report.Orphans)
	}
	if report.Orphans[0] != "internal/deleted.go" {
		t.Errorf("expected orphan 'internal/deleted.go', got %q", report.Orphans[0])
	}
}

func TestLint_DetectsMissingDocs(t *testing.T) {
	db := newLintTestDB(t)
	outDir := t.TempDir()

	// Node in DB but no doc file
	db.Create(&model.Node{
		QualifiedName: "pkg/service.go::Handle",
		Kind:          model.NodeKindFunction,
		Name:          "Handle",
		FilePath:      "pkg/service.go",
		StartLine:     1, EndLine: 10,
		Hash: "h1", Language: "go",
	})

	gen := &Generator{DB: db, OutDir: outDir}
	report, err := gen.Lint()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(report.Missing) != 1 {
		t.Fatalf("expected 1 missing, got %d: %v", len(report.Missing), report.Missing)
	}
	if report.Missing[0] != "pkg/service.go" {
		t.Errorf("expected missing 'pkg/service.go', got %q", report.Missing[0])
	}
}

func TestLint_DetectsStaleDocs(t *testing.T) {
	db := newLintTestDB(t)
	outDir := t.TempDir()

	// Create a node with recent UpdatedAt
	db.Create(&model.Node{
		QualifiedName: "pkg/service.go::Handle",
		Kind:          model.NodeKindFunction,
		Name:          "Handle",
		FilePath:      "pkg/service.go",
		StartLine:     1, EndLine: 10,
		Hash: "h1", Language: "go",
	})

	// Create a doc file with old mtime
	docDir := filepath.Join(outDir, "pkg")
	os.MkdirAll(docDir, 0o755)
	docPath := filepath.Join(docDir, "service.go.md")
	os.WriteFile(docPath, []byte("# pkg/service.go\n"), 0o644)
	oldTime := time.Now().Add(-24 * time.Hour)
	os.Chtimes(docPath, oldTime, oldTime)

	gen := &Generator{DB: db, OutDir: outDir}
	report, err := gen.Lint()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(report.Stale) != 1 {
		t.Fatalf("expected 1 stale, got %d: %v", len(report.Stale), report.Stale)
	}
	if report.Stale[0] != "pkg/service.go" {
		t.Errorf("expected stale 'pkg/service.go', got %q", report.Stale[0])
	}
}

func TestLint_FreshDocsNotStale(t *testing.T) {
	db := newLintTestDB(t)
	outDir := t.TempDir()

	// Create node, then generate docs (so doc is fresh)
	db.Create(&model.Node{
		QualifiedName: "pkg/service.go::Handle",
		Kind:          model.NodeKindFunction,
		Name:          "Handle",
		FilePath:      "pkg/service.go",
		StartLine:     1, EndLine: 10,
		Hash: "h1", Language: "go",
	})

	gen := &Generator{DB: db, OutDir: outDir}
	if err := gen.Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}

	report, err := gen.Lint()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(report.Orphans) != 0 {
		t.Errorf("expected 0 orphans, got %v", report.Orphans)
	}
	if len(report.Missing) != 0 {
		t.Errorf("expected 0 missing, got %v", report.Missing)
	}
	if len(report.Stale) != 0 {
		t.Errorf("expected 0 stale, got %v", report.Stale)
	}
}

func TestLint_EmptyDB_EmptyDir(t *testing.T) {
	db := newLintTestDB(t)
	outDir := t.TempDir()

	gen := &Generator{DB: db, OutDir: outDir}
	report, err := gen.Lint()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(report.Orphans) != 0 || len(report.Missing) != 0 || len(report.Stale) != 0 {
		t.Errorf("expected empty report, got orphans=%d missing=%d stale=%d",
			len(report.Orphans), len(report.Missing), len(report.Stale))
	}
}
