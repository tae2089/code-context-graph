package docs

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/tae2089/code-context-graph/internal/adapters/outbound/contentfiles"
	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

func newLintTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	if err := migrateDocsTestDB(db); err != nil {
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

	gen := &Generator{Repository: testRepository{db: db}, Files: contentfiles.NewRoot(outDir), OutDir: outDir}
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

func TestLint_ExcludesDocFilesFromOrphans(t *testing.T) {
	db := newLintTestDB(t)
	outDir := t.TempDir()

	pkgDir := filepath.Join(outDir, "pkg")
	scriptsDir := filepath.Join(outDir, "scripts")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(scriptsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "service_test.go.md"), []byte("# pkg/service_test.go\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(scriptsDir, "deploy.sh.md"), []byte("# scripts/deploy.sh\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	gen := &Generator{
		Repository: testRepository{db: db},
		Files:      contentfiles.NewRoot(outDir),
		OutDir:     outDir,
		Exclude:    []string{".*_test\\.go$", ".*\\.sh$"},
	}
	report, err := gen.Lint()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(report.Orphans) != 0 {
		t.Fatalf("expected excluded docs not to be reported as orphans, got %v", report.Orphans)
	}
}

func TestLint_DetectsMissingDocs(t *testing.T) {
	db := newLintTestDB(t)
	outDir := t.TempDir()

	// Node in DB but no doc file
	db.Create(&graph.Node{
		QualifiedName: "pkg/service.go::Handle",
		Kind:          graph.NodeKindFunction,
		Name:          "Handle",
		FilePath:      "pkg/service.go",
		StartLine:     1, EndLine: 10,
		Hash: "h1", Language: "go",
	})

	gen := &Generator{Repository: testRepository{db: db}, Files: contentfiles.NewRoot(outDir), OutDir: outDir}
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
	db.Create(&graph.Node{
		QualifiedName: "pkg/service.go::Handle",
		Kind:          graph.NodeKindFunction,
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

	gen := &Generator{Repository: testRepository{db: db}, Files: contentfiles.NewRoot(outDir), OutDir: outDir}
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
	db.Create(&graph.Node{
		QualifiedName: "pkg/service.go::Handle",
		Kind:          graph.NodeKindFunction,
		Name:          "Handle",
		FilePath:      "pkg/service.go",
		StartLine:     1, EndLine: 10,
		Hash: "h1", Language: "go",
	})

	gen := &Generator{Repository: testRepository{db: db}, Files: contentfiles.NewRoot(outDir), OutDir: outDir}
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

	gen := &Generator{Repository: testRepository{db: db}, Files: contentfiles.NewRoot(outDir), OutDir: outDir}
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
	annotated := graph.Node{
		QualifiedName: "pkg/a.go::Annotated",
		Kind:          graph.NodeKindFunction,
		Name:          "Annotated",
		FilePath:      "pkg/a.go",
		StartLine:     1, EndLine: 10,
		Hash: "h1", Language: "go",
	}
	db.Create(&annotated)
	db.Create(&graph.Annotation{
		NodeID: annotated.ID,
		Tags:   []graph.DocTag{{Kind: graph.TagIntent, Value: "does something", Ordinal: 0}},
	})

	// Function WITHOUT annotation
	db.Create(&graph.Node{
		QualifiedName: "pkg/b.go::Bare",
		Kind:          graph.NodeKindFunction,
		Name:          "Bare",
		FilePath:      "pkg/b.go",
		StartLine:     1, EndLine: 10,
		Hash: "h2", Language: "go",
	})

	// Type WITHOUT annotation
	db.Create(&graph.Node{
		QualifiedName: "pkg/b.go::Config",
		Kind:          graph.NodeKindType,
		Name:          "Config",
		FilePath:      "pkg/b.go",
		StartLine:     12, EndLine: 20,
		Hash: "h3", Language: "go",
	})

	gen := &Generator{Repository: testRepository{db: db}, Files: contentfiles.NewRoot(outDir), OutDir: outDir}
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
	db.Create(&graph.Node{
		QualifiedName: "pkg/a_test.go::TestFoo",
		Kind:          graph.NodeKindTest,
		Name:          "TestFoo",
		FilePath:      "pkg/a_test.go",
		StartLine:     1, EndLine: 10,
		Hash: "h1", Language: "go",
	})

	gen := &Generator{Repository: testRepository{db: db}, Files: contentfiles.NewRoot(outDir), OutDir: outDir}
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
	node := graph.Node{
		QualifiedName: "pkg/auth.go::Login",
		Kind:          graph.NodeKindFunction,
		Name:          "Login",
		FilePath:      "pkg/auth.go",
		StartLine:     1, EndLine: 10,
		Hash: "hash_v1", Language: "go",
	}
	db.Create(&node)

	// Create annotation with @param tag
	ann := graph.Annotation{
		NodeID:  node.ID,
		Summary: "Handles login",
		Tags:    []graph.DocTag{{Kind: graph.TagParam, Name: "ctx", Value: "request context", Ordinal: 0}},
	}
	db.Create(&ann)

	// Force node UpdatedAt to be AFTER annotation UpdatedAt
	db.Model(&graph.Node{}).Where("id = ?", node.ID).Update("updated_at", time.Now().Add(1*time.Hour))

	gen := &Generator{Repository: testRepository{db: db}, Files: contentfiles.NewRoot(outDir), OutDir: outDir}
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

func TestLint_DetectsDeadRef(t *testing.T) {
	db := newLintTestDB(t)
	outDir := t.TempDir()

	node := graph.Node{
		QualifiedName: "pkg/pay.go::Pay",
		Kind:          graph.NodeKindFunction,
		Name:          "Pay",
		FilePath:      "pkg/pay.go",
		StartLine:     1, EndLine: 10,
		Hash: "h1", Language: "go",
	}
	db.Create(&node)

	db.Create(&graph.Annotation{
		NodeID: node.ID,
		Tags: []graph.DocTag{
			{Kind: graph.TagSee, Value: "pkg/removed.go::Gone", Ordinal: 0},
			{Kind: graph.TagIntent, Value: "process payment", Ordinal: 1},
		},
	})

	gen := &Generator{Repository: testRepository{db: db}, Files: contentfiles.NewRoot(outDir), OutDir: outDir}
	report, err := gen.Lint()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(report.DeadRefs) != 1 {
		t.Fatalf("expected 1 dead ref, got %d: %v", len(report.DeadRefs), report.DeadRefs)
	}
	if report.DeadRefs[0].SeeTarget != "pkg/removed.go::Gone" {
		t.Errorf("expected see target 'pkg/removed.go::Gone', got %q", report.DeadRefs[0].SeeTarget)
	}
}

func TestLint_ValidRef_NotDeadRef(t *testing.T) {
	db := newLintTestDB(t)
	outDir := t.TempDir()

	target := graph.Node{
		QualifiedName: "pkg/util.go::Helper",
		Kind:          graph.NodeKindFunction,
		Name:          "Helper",
		FilePath:      "pkg/util.go",
		StartLine:     1, EndLine: 5,
		Hash: "h1", Language: "go",
	}
	db.Create(&target)

	source := graph.Node{
		QualifiedName: "pkg/pay.go::Pay",
		Kind:          graph.NodeKindFunction,
		Name:          "Pay",
		FilePath:      "pkg/pay.go",
		StartLine:     1, EndLine: 10,
		Hash: "h2", Language: "go",
	}
	db.Create(&source)

	db.Create(&graph.Annotation{
		NodeID: source.ID,
		Tags: []graph.DocTag{
			{Kind: graph.TagSee, Value: "pkg/util.go::Helper", Ordinal: 0},
		},
	})

	gen := &Generator{Repository: testRepository{db: db}, Files: contentfiles.NewRoot(outDir), OutDir: outDir}
	report, err := gen.Lint()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(report.DeadRefs) != 0 {
		t.Errorf("expected 0 dead refs, got %v", report.DeadRefs)
	}
}

func TestLint_CCGSeeRefResolvesAcrossNamespace(t *testing.T) {
	db := newLintTestDB(t)
	outDir := t.TempDir()

	target := graph.Node{
		Namespace:     "auth-svc",
		QualifiedName: "auth.ValidateToken",
		Kind:          graph.NodeKindFunction,
		Name:          "ValidateToken",
		FilePath:      "internal/auth/token.go",
		StartLine:     1, EndLine: 5,
		Hash: "h1", Language: "go",
	}
	db.Create(&target)

	source := graph.Node{
		Namespace:     "payment-svc",
		QualifiedName: "payment.Charge",
		Kind:          graph.NodeKindFunction,
		Name:          "Charge",
		FilePath:      "internal/payment/charge.go",
		StartLine:     1, EndLine: 10,
		Hash: "h2", Language: "go",
	}
	db.Create(&source)
	db.Create(&graph.Annotation{
		NodeID: source.ID,
		Tags: []graph.DocTag{
			{Kind: graph.TagSee, Value: "ccg://auth-svc/internal/auth/token.go#ValidateToken", Ordinal: 0},
		},
	})

	gen := &Generator{Repository: testRepository{db: db}, Files: contentfiles.NewRoot(outDir), OutDir: outDir, Namespace: "payment-svc"}
	report, err := gen.Lint()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(report.DeadRefs) != 0 {
		t.Fatalf("expected 0 dead refs, got %v", report.DeadRefs)
	}
}

func TestLint_CCGSeeRefMissingTargetIsDeadRef(t *testing.T) {
	db := newLintTestDB(t)
	outDir := t.TempDir()

	source := graph.Node{
		Namespace:     "payment-svc",
		QualifiedName: "payment.Charge",
		Kind:          graph.NodeKindFunction,
		Name:          "Charge",
		FilePath:      "internal/payment/charge.go",
		StartLine:     1, EndLine: 10,
		Hash: "h2", Language: "go",
	}
	db.Create(&source)
	db.Create(&graph.Annotation{
		NodeID: source.ID,
		Tags: []graph.DocTag{
			{Kind: graph.TagSee, Value: "ccg://auth-svc/internal/auth/token.go#ValidateToken", Ordinal: 0},
		},
	})

	gen := &Generator{Repository: testRepository{db: db}, Files: contentfiles.NewRoot(outDir), OutDir: outDir, Namespace: "payment-svc"}
	report, err := gen.Lint()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(report.DeadRefs) != 1 {
		t.Fatalf("expected 1 dead ref, got %d: %v", len(report.DeadRefs), report.DeadRefs)
	}
}

func TestLint_NoContradiction_WhenAnnotationFresh(t *testing.T) {
	db := newLintTestDB(t)
	outDir := t.TempDir()

	node := graph.Node{
		QualifiedName: "pkg/auth.go::Login",
		Kind:          graph.NodeKindFunction,
		Name:          "Login",
		FilePath:      "pkg/auth.go",
		StartLine:     1, EndLine: 10,
		Hash: "hash_v1", Language: "go",
	}
	db.Create(&node)

	ann := graph.Annotation{
		NodeID:  node.ID,
		Summary: "Handles login",
		Tags:    []graph.DocTag{{Kind: graph.TagParam, Name: "ctx", Value: "request context", Ordinal: 0}},
	}
	db.Create(&ann)

	// Force annotation UpdatedAt to be AFTER node UpdatedAt
	db.Model(&graph.Annotation{}).Where("id = ?", ann.ID).Update("updated_at", time.Now().Add(1*time.Hour))

	gen := &Generator{Repository: testRepository{db: db}, Files: contentfiles.NewRoot(outDir), OutDir: outDir}
	report, err := gen.Lint()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(report.Contradictions) != 0 {
		t.Errorf("expected 0 contradictions, got %d: %v", len(report.Contradictions), report.Contradictions)
	}
}

func TestLint_DetectsIncomplete(t *testing.T) {
	db := newLintTestDB(t)
	outDir := t.TempDir()

	node := graph.Node{
		QualifiedName: "pkg/util.go::Parse",
		Kind:          graph.NodeKindFunction,
		Name:          "Parse",
		FilePath:      "pkg/util.go",
		StartLine:     1, EndLine: 10,
		Hash: "h1", Language: "go",
	}
	db.Create(&node)

	// Annotation exists but has NO @intent tag — only @param
	db.Create(&graph.Annotation{
		NodeID:  node.ID,
		Summary: "Parses input",
		Tags:    []graph.DocTag{{Kind: graph.TagParam, Name: "s", Value: "input string", Ordinal: 0}},
	})

	gen := &Generator{Repository: testRepository{db: db}, Files: contentfiles.NewRoot(outDir), OutDir: outDir}
	report, err := gen.Lint()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(report.Incomplete) != 1 {
		t.Fatalf("expected 1 incomplete, got %d: %v", len(report.Incomplete), report.Incomplete)
	}
	if report.Incomplete[0] != "pkg/util.go::Parse" {
		t.Errorf("expected 'pkg/util.go::Parse', got %q", report.Incomplete[0])
	}
}

func TestLint_CompleteAnnotation_NotIncomplete(t *testing.T) {
	db := newLintTestDB(t)
	outDir := t.TempDir()

	node := graph.Node{
		QualifiedName: "pkg/util.go::Parse",
		Kind:          graph.NodeKindFunction,
		Name:          "Parse",
		FilePath:      "pkg/util.go",
		StartLine:     1, EndLine: 10,
		Hash: "h1", Language: "go",
	}
	db.Create(&node)

	db.Create(&graph.Annotation{
		NodeID: node.ID,
		Tags: []graph.DocTag{
			{Kind: graph.TagIntent, Value: "parse user input", Ordinal: 0},
			{Kind: graph.TagParam, Name: "s", Value: "input string", Ordinal: 1},
		},
	})

	gen := &Generator{Repository: testRepository{db: db}, Files: contentfiles.NewRoot(outDir), OutDir: outDir}
	report, err := gen.Lint()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(report.Incomplete) != 0 {
		t.Errorf("expected 0 incomplete, got %v", report.Incomplete)
	}
}

func TestLint_DetectsDrift(t *testing.T) {
	db := newLintTestDB(t)
	outDir := t.TempDir()

	node := graph.Node{
		QualifiedName: "pkg/session.go::Validate",
		Kind:          graph.NodeKindFunction,
		Name:          "Validate",
		FilePath:      "pkg/session.go",
		StartLine:     1, EndLine: 10,
		Hash: "h1", Language: "go",
	}
	db.Create(&node)

	ann := graph.Annotation{
		NodeID: node.ID,
		Tags:   []graph.DocTag{{Kind: graph.TagIntent, Value: "validate session", Ordinal: 0}},
	}
	db.Create(&ann)

	// Force node UpdatedAt to be AFTER annotation UpdatedAt
	db.Model(&graph.Node{}).Where("id = ?", node.ID).Update("updated_at", time.Now().Add(1*time.Hour))

	gen := &Generator{Repository: testRepository{db: db}, Files: contentfiles.NewRoot(outDir), OutDir: outDir}
	report, err := gen.Lint()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(report.Drifted) != 1 {
		t.Fatalf("expected 1 drifted, got %d: %v", len(report.Drifted), report.Drifted)
	}
	if report.Drifted[0] != "pkg/session.go::Validate" {
		t.Errorf("expected 'pkg/session.go::Validate', got %q", report.Drifted[0])
	}
}

func TestLint_NoDrift_WhenAnnotationFresh(t *testing.T) {
	db := newLintTestDB(t)
	outDir := t.TempDir()

	node := graph.Node{
		QualifiedName: "pkg/session.go::Validate",
		Kind:          graph.NodeKindFunction,
		Name:          "Validate",
		FilePath:      "pkg/session.go",
		StartLine:     1, EndLine: 10,
		Hash: "h1", Language: "go",
	}
	db.Create(&node)

	ann := graph.Annotation{
		NodeID: node.ID,
		Tags:   []graph.DocTag{{Kind: graph.TagIntent, Value: "validate session", Ordinal: 0}},
	}
	db.Create(&ann)

	// Force annotation UpdatedAt to be AFTER node UpdatedAt
	db.Model(&graph.Annotation{}).Where("id = ?", ann.ID).Update("updated_at", time.Now().Add(1*time.Hour))

	gen := &Generator{Repository: testRepository{db: db}, Files: contentfiles.NewRoot(outDir), OutDir: outDir}
	report, err := gen.Lint()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(report.Drifted) != 0 {
		t.Errorf("expected 0 drifted, got %v", report.Drifted)
	}
}

func TestLint_RespectsNamespace(t *testing.T) {
	db := newLintTestDB(t)
	outDir := t.TempDir()

	db.Create(&graph.Node{Namespace: "alpha", QualifiedName: "pkg.Alpha", Kind: graph.NodeKindFunction, Name: "Alpha", FilePath: "pkg/alpha.go", StartLine: 1, EndLine: 10, Hash: "h1", Language: "go"})
	db.Create(&graph.Node{Namespace: "beta", QualifiedName: "pkg.Beta", Kind: graph.NodeKindFunction, Name: "Beta", FilePath: "pkg/beta.go", StartLine: 1, EndLine: 10, Hash: "h2", Language: "go"})

	gen := &Generator{Repository: testRepository{db: db}, Files: contentfiles.NewRoot(outDir), OutDir: outDir, Namespace: "alpha"}
	report, err := gen.Lint()
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Missing) != 1 || report.Missing[0] != "pkg/alpha.go" {
		t.Fatalf("missing = %v, want only alpha file", report.Missing)
	}
}

func TestLint_NamedNamespaceIgnoresForeignDocsWithoutManifest(t *testing.T) {
	db := newLintTestDB(t)
	outDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(outDir, "internal"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outDir, "internal", "foreign.go.md"), []byte("# internal/foreign.go\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	db.Create(&graph.Node{Namespace: "trace", QualifiedName: "trace.Context", Kind: graph.NodeKindFunction, Name: "Context", FilePath: "context.go", StartLine: 1, EndLine: 10, Hash: "h1", Language: "go"})

	gen := &Generator{Repository: testRepository{db: db}, Files: contentfiles.NewRoot(outDir), OutDir: outDir, Namespace: "trace"}
	report, err := gen.Lint()
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Orphans) != 0 {
		t.Fatalf("orphans = %v, want none from foreign docs", report.Orphans)
	}
	if len(report.Missing) != 1 || report.Missing[0] != "context.go" {
		t.Fatalf("missing = %v, want only trace source", report.Missing)
	}
}

func TestLint_NamedNamespaceUsesScopedManifest(t *testing.T) {
	db := newLintTestDB(t)
	outDir := t.TempDir()
	db.Create(&graph.Node{Namespace: "trace", QualifiedName: "trace.Context", Kind: graph.NodeKindFunction, Name: "Context", FilePath: "context.go", StartLine: 1, EndLine: 10, Hash: "h1", Language: "go"})
	if err := os.WriteFile(filepath.Join(outDir, "context.go.md"), []byte("# context.go\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outDir, "foreign.go.md"), []byte("# foreign.go\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	gen := &Generator{Repository: testRepository{db: db}, Files: contentfiles.NewRoot(outDir), OutDir: outDir, Namespace: "trace"}
	if err := gen.saveManifest([]string{"context.go.md", "index.md"}); err != nil {
		t.Fatal(err)
	}
	report, err := gen.Lint()
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Orphans) != 0 || len(report.Missing) != 0 {
		t.Fatalf("report = orphans:%v missing:%v, want clean manifest-scoped docs", report.Orphans, report.Missing)
	}
}
