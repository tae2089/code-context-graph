package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/tae2089/code-context-graph/internal/ctxns"
	"github.com/tae2089/code-context-graph/internal/model"
	"github.com/tae2089/code-context-graph/internal/parse/treesitter"
	postprocesspolicy "github.com/tae2089/code-context-graph/internal/postprocess/policy"
	"github.com/tae2089/code-context-graph/internal/store/gormstore"
)

func setupStatusTest(t *testing.T) (*Deps, *bytes.Buffer, *bytes.Buffer, *gorm.DB) {
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

	return deps, stdout, stderr, db
}

func TestStatusCommand_ShowsStats(t *testing.T) {
	deps, stdout, stderr, _ := setupStatusTest(t)

	dir := t.TempDir()
	writeGoFile(t, dir, "s.go", `package s
func S() {}
`)

	err := executeCmd(deps, stdout, stderr, "build", dir)
	if err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	stderr.Reset()

	err = executeCmd(deps, stdout, stderr, "status")
	if err != nil {
		t.Fatalf("status: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, "Nodes:") {
		t.Fatalf("expected 'Nodes:' in output, got: %s", out)
	}
	if !strings.Contains(out, "Edges:") {
		t.Fatalf("expected 'Edges:' in output, got: %s", out)
	}
	if !strings.Contains(out, "Files:") {
		t.Fatalf("expected 'Files:' in output, got: %s", out)
	}
	if !strings.Contains(out, "Call edge health:") {
		t.Fatalf("expected 'Call edge health:' in output, got: %s", out)
	}
}

func TestStatusCommand_ShowsKindBreakdown(t *testing.T) {
	deps, stdout, stderr, _ := setupStatusTest(t)

	dir := t.TempDir()
	writeGoFile(t, dir, "k.go", `package k

type Foo struct{}
func Bar() {}
`)

	err := executeCmd(deps, stdout, stderr, "build", dir)
	if err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	stderr.Reset()

	err = executeCmd(deps, stdout, stderr, "status")
	if err != nil {
		t.Fatalf("status: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, "Node kinds:") {
		t.Fatalf("expected 'Node kinds:' in output, got: %s", out)
	}
	if !strings.Contains(out, "function:") {
		t.Fatalf("expected 'function:' in kind breakdown, got: %s", out)
	}
}

func TestStatusCommand_EmptyDB(t *testing.T) {
	deps, stdout, stderr, _ := setupStatusTest(t)

	stdout.Reset()
	stderr.Reset()

	err := executeCmd(deps, stdout, stderr, "status")
	if err != nil {
		t.Fatalf("status: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, "No data") {
		t.Fatalf("expected 'No data' for empty DB, got: %s", out)
	}
}

func TestStatusCommand_ShowsCallFallbackHealth(t *testing.T) {
	deps, stdout, stderr, db := setupStatusTest(t)

	from := model.Node{
		Namespace:     ctxns.DefaultNamespace,
		QualifiedName: "default.From",
		Kind:          model.NodeKindFunction,
		Name:          "From",
		FilePath:      "from.go",
		StartLine:     1,
		EndLine:       2,
		Language:      "go",
	}
	to := model.Node{
		Namespace:     ctxns.DefaultNamespace,
		QualifiedName: "default.To",
		Kind:          model.NodeKindFunction,
		Name:          "To",
		FilePath:      "to.go",
		StartLine:     1,
		EndLine:       2,
		Language:      "go",
	}
	if err := db.Create(&from).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&to).Error; err != nil {
		t.Fatal(err)
	}

	edges := []model.Edge{
		{
			Namespace:   ctxns.DefaultNamespace,
			FromNodeID:  from.ID,
			ToNodeID:    to.ID,
			Kind:        model.EdgeKindCalls,
			FilePath:    "from.go",
			Line:        1,
			Fingerprint: "calls:from.go:From:1",
		},
		{
			Namespace:   ctxns.DefaultNamespace,
			FromNodeID:  from.ID,
			ToNodeID:    to.ID,
			Kind:        model.EdgeKindCalls,
			FilePath:    "from.go",
			Line:        2,
			Fingerprint: "calls:from.go:From:2",
		},
		{
			Namespace:   ctxns.DefaultNamespace,
			FromNodeID:  from.ID,
			ToNodeID:    to.ID,
			Kind:        model.EdgeKindCalls,
			FilePath:    "from.go",
			Line:        3,
			Fingerprint: "calls:from.go:From:3",
		},
		{
			Namespace:   ctxns.DefaultNamespace,
			FromNodeID:  from.ID,
			ToNodeID:    to.ID,
			Kind:        model.EdgeKindFallbackCalls,
			FilePath:    "from.go",
			Line:        4,
			Fingerprint: "fallback_calls:from.go:From:4",
		},
	}
	for _, edge := range edges {
		if err := db.Create(&edge).Error; err != nil {
			t.Fatal(err)
		}
	}

	stdout.Reset()
	stderr.Reset()

	err := executeCmd(deps, stdout, stderr, "status")
	if err != nil {
		t.Fatalf("status: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, "Call edge health:") {
		t.Fatalf("expected 'Call edge health:' in output, got: %s", out)
	}
	if !strings.Contains(out, "Strict call edges: 3") {
		t.Fatalf("expected 3 strict call edges in output, got: %s", out)
	}
	if !strings.Contains(out, "Fallback call edges: 1") {
		t.Fatalf("expected 1 fallback call edge in output, got: %s", out)
	}
	if !strings.Contains(out, "Status: warn") {
		t.Fatalf("expected warning status in output, got: %s", out)
	}
}

func TestStatusCommand_RespectsNamespace(t *testing.T) {
	deps, stdout, stderr, db := setupStatusTest(t)

	if err := db.Create(&model.Node{Namespace: ctxns.DefaultNamespace, QualifiedName: "default.Foo", Kind: model.NodeKindFunction, Name: "Foo", FilePath: "default/foo.go", StartLine: 1, EndLine: 2, Language: "go"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.Node{Namespace: "other", QualifiedName: "other.Bar", Kind: model.NodeKindFunction, Name: "Bar", FilePath: "other/bar.go", StartLine: 1, EndLine: 2, Language: "go"}).Error; err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	stderr.Reset()

	if err := executeCmd(deps, stdout, stderr, "status"); err != nil {
		t.Fatalf("status: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, "Nodes: 1") {
		t.Fatalf("expected default namespace only, got: %s", out)
	}
	if strings.Contains(out, "Nodes: 2") {
		t.Fatalf("unexpected cross-namespace aggregation: %s", out)
	}
}

func TestStatusCommand_ShowsPostprocessOKSummary(t *testing.T) {
	deps, stdout, stderr, db := setupStatusTest(t)

	if err := db.Create(&model.Node{Namespace: ctxns.DefaultNamespace, QualifiedName: "default.Foo", Kind: model.NodeKindFunction, Name: "Foo", FilePath: "default/foo.go", StartLine: 1, EndLine: 2, Language: "go"}).Error; err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	stderr.Reset()

	if err := executeCmd(deps, stdout, stderr, "status"); err != nil {
		t.Fatalf("status: %v", err)
	}

	out := stdout.String()
	for _, want := range []string{
		"Postprocess:",
		"Status: ok",
		"Fail-closed: 0",
		"Recent failures: 0",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected %q in output, got: %s", want, out)
		}
	}
}

func TestStatusCommand_ShowsPostprocessErrors(t *testing.T) {
	deps, stdout, stderr, db := setupStatusTest(t)

	if err := db.Create(&model.Node{Namespace: ctxns.DefaultNamespace, QualifiedName: "default.Foo", Kind: model.NodeKindFunction, Name: "Foo", FilePath: "default/foo.go", StartLine: 1, EndLine: 2, Language: "go"}).Error; err != nil {
		t.Fatal(err)
	}

	store := postprocesspolicy.NewStore(db)
	ctx := ctxns.WithNamespace(context.Background(), ctxns.DefaultNamespace)
	base := time.Date(2026, 5, 4, 10, 0, 0, 0, time.UTC)
	for i := 0; i < 3; i++ {
		if err := store.RecordRun(ctx, postprocesspolicy.RunRecord{
			Tool:         postprocesspolicy.ToolRunPostprocess,
			Policy:       postprocesspolicy.PolicyFailClosed,
			Source:       postprocesspolicy.SourceAuto,
			Status:       postprocesspolicy.StatusDegraded,
			FailedSteps:  []string{"communities"},
			SkippedSteps: []string{"fts"},
			CreatedAt:    base.Add(time.Duration(i) * time.Minute),
		}); err != nil {
			t.Fatal(err)
		}
	}

	stdout.Reset()
	stderr.Reset()

	if err := executeCmd(deps, stdout, stderr, "status", "--errors"); err != nil {
		t.Fatalf("status: %v", err)
	}

	out := stdout.String()
	for _, want := range []string{
		"Status: degraded",
		"Fail-closed:",
		"run_postprocess  consecutive_failures=3",
		"Recent failures:",
		"policy=fail_closed",
		"failed_steps=communities",
		"skipped_steps=fts",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected %q in output, got: %s", want, out)
		}
	}
}

func TestStatusCommand_RecentLimitsPostprocessFailures(t *testing.T) {
	deps, stdout, stderr, db := setupStatusTest(t)

	if err := db.Create(&model.Node{Namespace: ctxns.DefaultNamespace, QualifiedName: "default.Foo", Kind: model.NodeKindFunction, Name: "Foo", FilePath: "default/foo.go", StartLine: 1, EndLine: 2, Language: "go"}).Error; err != nil {
		t.Fatal(err)
	}

	store := postprocesspolicy.NewStore(db)
	ctx := ctxns.WithNamespace(context.Background(), ctxns.DefaultNamespace)
	base := time.Date(2026, 5, 4, 10, 0, 0, 0, time.UTC)
	for i, step := range []string{"communities", "flows"} {
		if err := store.RecordRun(ctx, postprocesspolicy.RunRecord{
			Tool:        postprocesspolicy.ToolRunPostprocess,
			Policy:      postprocesspolicy.PolicyDegraded,
			Source:      postprocesspolicy.SourceAuto,
			Status:      postprocesspolicy.StatusDegraded,
			FailedSteps: []string{step},
			CreatedAt:   base.Add(time.Duration(i) * time.Minute),
		}); err != nil {
			t.Fatal(err)
		}
	}

	stdout.Reset()
	stderr.Reset()

	if err := executeCmd(deps, stdout, stderr, "status", "--errors", "--recent", "1"); err != nil {
		t.Fatalf("status: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, "failed_steps=flows") {
		t.Fatalf("expected newest failure in output, got: %s", out)
	}
	if strings.Contains(out, "failed_steps=communities") {
		t.Fatalf("expected older failure to be omitted, got: %s", out)
	}
}

func TestStatusCommand_PostprocessErrorsRespectNamespace(t *testing.T) {
	deps, stdout, stderr, db := setupStatusTest(t)

	for _, ns := range []string{ctxns.DefaultNamespace, "other"} {
		if err := db.Create(&model.Node{Namespace: ns, QualifiedName: ns + ".Foo", Kind: model.NodeKindFunction, Name: "Foo", FilePath: ns + "/foo.go", StartLine: 1, EndLine: 2, Language: "go"}).Error; err != nil {
			t.Fatal(err)
		}
	}

	store := postprocesspolicy.NewStore(db)
	otherCtx := ctxns.WithNamespace(context.Background(), "other")
	if err := store.RecordRun(otherCtx, postprocesspolicy.RunRecord{
		Tool:        postprocesspolicy.ToolRunPostprocess,
		Policy:      postprocesspolicy.PolicyDegraded,
		Source:      postprocesspolicy.SourceAuto,
		Status:      postprocesspolicy.StatusDegraded,
		FailedSteps: []string{"communities"},
		CreatedAt:   time.Date(2026, 5, 4, 10, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	stderr.Reset()

	if err := executeCmd(deps, stdout, stderr, "status"); err != nil {
		t.Fatalf("status: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, "Status: ok") {
		t.Fatalf("expected default namespace postprocess status ok, got: %s", out)
	}
	if strings.Contains(out, "communities") {
		t.Fatalf("unexpected cross-namespace failure in output: %s", out)
	}
}

func TestStatusCommand_RejectsInvalidRecent(t *testing.T) {
	deps, stdout, stderr, _ := setupStatusTest(t)

	stdout.Reset()
	stderr.Reset()

	err := executeCmd(deps, stdout, stderr, "status", "--recent", "0")
	if err == nil {
		t.Fatal("expected invalid recent error")
	}
	if !strings.Contains(err.Error(), "recent must be > 0") {
		t.Fatalf("unexpected error: %v", err)
	}
}
