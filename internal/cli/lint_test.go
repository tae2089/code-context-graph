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

func TestLintCommand_ReportsContradiction(t *testing.T) {
	deps, stdout, stderr, db := setupLintTest(t)

	node := model.Node{
		QualifiedName: "pkg/auth.go::Login",
		Kind:          model.NodeKindFunction,
		Name:          "Login",
		FilePath:      "pkg/auth.go",
		StartLine:     1, EndLine: 10,
		Hash: "h1", Language: "go",
	}
	db.Create(&node)

	ann := model.Annotation{
		NodeID: node.ID,
		Tags:   []model.DocTag{{Kind: model.TagParam, Name: "ctx", Value: "context", Ordinal: 0}},
	}
	db.Create(&ann)
	db.Model(&model.Node{}).Where("id = ?", node.ID).Update("updated_at", time.Now().Add(1*time.Hour))

	outDir := t.TempDir()
	if err := executeCmd(deps, stdout, stderr, "lint", "--out", outDir); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, "Contradiction") {
		t.Errorf("expected 'Contradiction' in output, got:\n%s", out)
	}
	if !strings.Contains(out, "pkg/auth.go::Login") {
		t.Errorf("expected qualified name in output, got:\n%s", out)
	}
}

func TestLintCommand_ReportsDeadRef(t *testing.T) {
	deps, stdout, stderr, db := setupLintTest(t)

	node := model.Node{
		QualifiedName: "pkg/pay.go::Pay",
		Kind:          model.NodeKindFunction,
		Name:          "Pay",
		FilePath:      "pkg/pay.go",
		StartLine:     1, EndLine: 10,
		Hash: "h1", Language: "go",
	}
	db.Create(&node)
	db.Create(&model.Annotation{
		NodeID: node.ID,
		Tags: []model.DocTag{
			{Kind: model.TagSee, Value: "pkg/gone.go::Gone", Ordinal: 0},
		},
	})

	outDir := t.TempDir()
	if err := executeCmd(deps, stdout, stderr, "lint", "--out", outDir); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, "Dead ref") {
		t.Errorf("expected 'Dead ref' in output, got:\n%s", out)
	}
	if !strings.Contains(out, "pkg/gone.go::Gone") {
		t.Errorf("expected see target in output, got:\n%s", out)
	}
}

func TestLintCommand_ReportsIncomplete(t *testing.T) {
	deps, stdout, stderr, db := setupLintTest(t)

	node := model.Node{
		QualifiedName: "pkg/util.go::Format",
		Kind:          model.NodeKindFunction,
		Name:          "Format",
		FilePath:      "pkg/util.go",
		StartLine:     1, EndLine: 5,
		Hash: "h2", Language: "go",
	}
	db.Create(&node)
	db.Create(&model.Annotation{
		NodeID: node.ID,
		Tags:   []model.DocTag{{Kind: model.TagReturn, Value: "formatted string", Ordinal: 0}},
	})

	outDir := t.TempDir()
	if err := executeCmd(deps, stdout, stderr, "lint", "--out", outDir); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, "Incomplete") {
		t.Errorf("expected 'Incomplete' in output, got:\n%s", out)
	}
	if !strings.Contains(out, "pkg/util.go::Format") {
		t.Errorf("expected qualified name in output, got:\n%s", out)
	}
}

func TestLintCommand_IgnoreRule_ExcludedFromStrict(t *testing.T) {
	deps, stdout, stderr, db := setupLintTest(t)

	// Create unannotated node
	db.Create(&model.Node{
		QualifiedName: "pkg/ignored.go::Ignored",
		Kind:          model.NodeKindFunction,
		Name:          "Ignored",
		FilePath:      "pkg/ignored.go",
		StartLine:     1, EndLine: 5,
		Hash: "h1", Language: "go",
	})

	outDir := t.TempDir()
	histDir := t.TempDir()

	// Pre-create the doc file to avoid a "missing" issue — we only want unannotated.
	docDir := filepath.Join(outDir, "pkg")
	os.MkdirAll(docDir, 0o755)
	os.WriteFile(filepath.Join(docDir, "ignored.go.md"), []byte("# pkg/ignored.go\n"), 0o644)

	// Create a .ccg.yaml with ignore rule for this symbol
	cfgFile := filepath.Join(t.TempDir(), ".ccg.yaml")
	os.WriteFile(cfgFile, []byte(`rules:
  - pattern: "pkg/ignored.go::Ignored"
    category: unannotated
    action: ignore
    auto: true
    created: "2026-04-14"
`), 0o644)

	// --strict should NOT fail because the only issue is ignored
	err := executeCmd(deps, stdout, stderr, "lint", "--out", outDir, "--config", cfgFile, "--strict", "--history-dir", histDir)
	if err != nil {
		t.Fatalf("expected no error with --strict when only issue is ignored, got: %v", err)
	}
}

func TestLintCommand_IgnoreRule_RegexPattern(t *testing.T) {
	deps, stdout, stderr, db := setupLintTest(t)

	// Create two unannotated nodes under pkg/store/
	db.Create(&model.Node{
		QualifiedName: "pkg/store/user.go::CreateUser",
		Kind:          model.NodeKindFunction,
		Name:          "CreateUser",
		FilePath:      "pkg/store/user.go",
		StartLine:     1, EndLine: 5,
		Hash: "h1", Language: "go",
	})
	db.Create(&model.Node{
		QualifiedName: "pkg/store/order.go::CreateOrder",
		Kind:          model.NodeKindFunction,
		Name:          "CreateOrder",
		FilePath:      "pkg/store/order.go",
		StartLine:     1, EndLine: 5,
		Hash: "h2", Language: "go",
	})

	outDir := t.TempDir()
	histDir := t.TempDir()

	// Pre-create doc files to avoid "missing" issues
	docDir := filepath.Join(outDir, "pkg", "store")
	os.MkdirAll(docDir, 0o755)
	os.WriteFile(filepath.Join(docDir, "user.go.md"), []byte("# user\n"), 0o644)
	os.WriteFile(filepath.Join(docDir, "order.go.md"), []byte("# order\n"), 0o644)

	// Regex pattern that matches both qualified names
	cfgFile := filepath.Join(t.TempDir(), ".ccg.yaml")
	os.WriteFile(cfgFile, []byte(`rules:
  - pattern: "pkg/store/.*::Create.*"
    category: unannotated
    action: ignore
    auto: false
    created: "2026-04-15"
`), 0o644)

	// --strict should NOT fail because both issues are ignored by regex
	err := executeCmd(deps, stdout, stderr, "lint", "--out", outDir, "--config", cfgFile, "--strict", "--history-dir", histDir)
	if err != nil {
		t.Fatalf("expected no error with --strict when issues matched by regex ignore rule, got: %v", err)
	}
}

func TestLintCommand_IgnoreRule_RegexDoesNotOverMatch(t *testing.T) {
	deps, stdout, stderr, db := setupLintTest(t)

	// Create two nodes: one should match regex, one should NOT
	db.Create(&model.Node{
		QualifiedName: "pkg/store/user.go::CreateUser",
		Kind:          model.NodeKindFunction,
		Name:          "CreateUser",
		FilePath:      "pkg/store/user.go",
		StartLine:     1, EndLine: 5,
		Hash: "h1", Language: "go",
	})
	db.Create(&model.Node{
		QualifiedName: "pkg/api/handler.go::HandleRequest",
		Kind:          model.NodeKindFunction,
		Name:          "HandleRequest",
		FilePath:      "pkg/api/handler.go",
		StartLine:     1, EndLine: 5,
		Hash: "h2", Language: "go",
	})

	outDir := t.TempDir()
	histDir := t.TempDir()

	// Pre-create doc files
	for _, dir := range []string{"pkg/store", "pkg/api"} {
		d := filepath.Join(outDir, dir)
		os.MkdirAll(d, 0o755)
	}
	os.WriteFile(filepath.Join(outDir, "pkg/store/user.go.md"), []byte("# user\n"), 0o644)
	os.WriteFile(filepath.Join(outDir, "pkg/api/handler.go.md"), []byte("# handler\n"), 0o644)

	// Regex pattern only matches pkg/store/...
	cfgFile := filepath.Join(t.TempDir(), ".ccg.yaml")
	os.WriteFile(cfgFile, []byte(`rules:
  - pattern: "pkg/store/.*"
    category: unannotated
    action: ignore
    auto: false
    created: "2026-04-15"
`), 0o644)

	// --strict SHOULD fail because pkg/api/handler.go::HandleRequest is NOT ignored
	err := executeCmd(deps, stdout, stderr, "lint", "--out", outDir, "--config", cfgFile, "--strict", "--history-dir", histDir)
	if err == nil {
		t.Fatal("expected error with --strict when regex does not cover all issues")
	}
}

func TestLintCommand_TwiceRule_TriggersOnSecondRun(t *testing.T) {
	deps, stdout, stderr, db := setupLintTest(t)

	// Create an unannotated node
	db.Create(&model.Node{
		QualifiedName: "pkg/bare.go::Bare",
		Kind:          model.NodeKindFunction,
		Name:          "Bare",
		FilePath:      "pkg/bare.go",
		StartLine:     1, EndLine: 5,
		Hash: "h1", Language: "go",
	})

	histDir := t.TempDir()
	outDir := t.TempDir()

	// First run
	if err := executeCmd(deps, stdout, stderr, "lint", "--out", outDir, "--history-dir", histDir); err != nil {
		t.Fatalf("first run unexpected error: %v", err)
	}
	stdout.Reset()
	stderr.Reset()

	// Second run — same issue persists
	if err := executeCmd(deps, stdout, stderr, "lint", "--out", outDir, "--history-dir", histDir); err != nil {
		t.Fatalf("second run unexpected error: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, "Twice Rule") {
		t.Errorf("expected 'Twice Rule' in output on second run, got:\n%s", out)
	}
}
