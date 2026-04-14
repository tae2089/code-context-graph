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

func TestLint_DetectsUnannotated(t *testing.T) {
	db := newLintTestDB(t)
	outDir := t.TempDir()

	// Function WITH annotation
	annotated := model.Node{
		QualifiedName: "pkg/a.go::Annotated",
		Kind:          model.NodeKindFunction,
		Name:          "Annotated",
		FilePath:      "pkg/a.go",
		StartLine:     1, EndLine: 10,
		Hash: "h1", Language: "go",
	}
	db.Create(&annotated)
	db.Create(&model.Annotation{
		NodeID: annotated.ID,
		Tags:   []model.DocTag{{Kind: model.TagIntent, Value: "does something", Ordinal: 0}},
	})

	// Function WITHOUT annotation
	db.Create(&model.Node{
		QualifiedName: "pkg/b.go::Bare",
		Kind:          model.NodeKindFunction,
		Name:          "Bare",
		FilePath:      "pkg/b.go",
		StartLine:     1, EndLine: 10,
		Hash: "h2", Language: "go",
	})

	// Type WITHOUT annotation
	db.Create(&model.Node{
		QualifiedName: "pkg/b.go::Config",
		Kind:          model.NodeKindType,
		Name:          "Config",
		FilePath:      "pkg/b.go",
		StartLine:     12, EndLine: 20,
		Hash: "h3", Language: "go",
	})

	gen := &Generator{DB: db, OutDir: outDir}
	report, err := gen.Lint()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(report.Unannotated) != 2 {
		t.Fatalf("expected 2 unannotated, got %d: %v", len(report.Unannotated), report.Unannotated)
	}

	// Should NOT include the annotated function
	for _, u := range report.Unannotated {
		if u == "pkg/a.go::Annotated" {
			t.Error("annotated function should not appear in unannotated list")
		}
	}
}

func TestLint_SkipsTestNodesForUnannotated(t *testing.T) {
	db := newLintTestDB(t)
	outDir := t.TempDir()

	// Test function — should not be reported as unannotated
	db.Create(&model.Node{
		QualifiedName: "pkg/a_test.go::TestFoo",
		Kind:          model.NodeKindTest,
		Name:          "TestFoo",
		FilePath:      "pkg/a_test.go",
		StartLine:     1, EndLine: 10,
		Hash: "h1", Language: "go",
	})

	gen := &Generator{DB: db, OutDir: outDir}
	report, err := gen.Lint()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(report.Unannotated) != 0 {
		t.Errorf("test nodes should not be in unannotated list, got: %v", report.Unannotated)
	}
}

func TestLint_DetectsContradiction(t *testing.T) {
	db := newLintTestDB(t)
	outDir := t.TempDir()

	// Create a node
	node := model.Node{
		QualifiedName: "pkg/auth.go::Login",
		Kind:          model.NodeKindFunction,
		Name:          "Login",
		FilePath:      "pkg/auth.go",
		StartLine:     1, EndLine: 10,
		Hash: "hash_v1", Language: "go",
	}
	db.Create(&node)

	// Create annotation with @param tag
	ann := model.Annotation{
		NodeID:  node.ID,
		Summary: "Handles login",
		Tags:    []model.DocTag{{Kind: model.TagParam, Name: "ctx", Value: "request context", Ordinal: 0}},
	}
	db.Create(&ann)

	// Force node UpdatedAt to be AFTER annotation UpdatedAt
	db.Model(&model.Node{}).Where("id = ?", node.ID).Update("updated_at", time.Now().Add(1*time.Hour))

	gen := &Generator{DB: db, OutDir: outDir}
	report, err := gen.Lint()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(report.Contradictions) != 1 {
		t.Fatalf("expected 1 contradiction, got %d: %v", len(report.Contradictions), report.Contradictions)
	}
	if report.Contradictions[0].QualifiedName != "pkg/auth.go::Login" {
		t.Errorf("expected qualified name 'pkg/auth.go::Login', got %q", report.Contradictions[0].QualifiedName)
	}
}

func TestLint_NoContradiction_WhenAnnotationFresh(t *testing.T) {
	db := newLintTestDB(t)
	outDir := t.TempDir()

	node := model.Node{
		QualifiedName: "pkg/auth.go::Login",
		Kind:          model.NodeKindFunction,
		Name:          "Login",
		FilePath:      "pkg/auth.go",
		StartLine:     1, EndLine: 10,
		Hash: "hash_v1", Language: "go",
	}
	db.Create(&node)

	ann := model.Annotation{
		NodeID:  node.ID,
		Summary: "Handles login",
		Tags:    []model.DocTag{{Kind: model.TagParam, Name: "ctx", Value: "request context", Ordinal: 0}},
	}
	db.Create(&ann)

	// Force annotation UpdatedAt to be AFTER node UpdatedAt
	db.Model(&model.Annotation{}).Where("id = ?", ann.ID).Update("updated_at", time.Now().Add(1*time.Hour))

	gen := &Generator{DB: db, OutDir: outDir}
	report, err := gen.Lint()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(report.Contradictions) != 0 {
		t.Errorf("expected 0 contradictions, got %d: %v", len(report.Contradictions), report.Contradictions)
	}
}
