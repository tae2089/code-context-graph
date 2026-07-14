package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/viper"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/tae2089/code-context-graph/internal/adapters/outbound/graphgorm"
	searchsql "github.com/tae2089/code-context-graph/internal/adapters/outbound/searchsql"
	"github.com/tae2089/code-context-graph/internal/adapters/outbound/treesitter"
	"github.com/tae2089/code-context-graph/internal/app/ingest"
	requestctx "github.com/tae2089/code-context-graph/internal/ctx"
	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

func setupStatusTest(t *testing.T) (*Deps, *bytes.Buffer, *bytes.Buffer, *gorm.DB) {
	t.Helper()
	deps, stdout, stderr := newTestDeps()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatal(err)
	}

	st := graphgorm.New(db)
	if err := st.AutoMigrate(); err != nil {
		t.Fatal(err)
	}

	deps.Store = st
	deps.UnitOfWork = searchsql.NewIngestUnitOfWork(db, nil, deps.Logger)
	deps.Search = searchsql.NewSearchWriter(db, nil, deps.Logger)
	deps.Statistics = st
	deps.Walkers = map[string]ingest.Parser{
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
	if !strings.Contains(out, "Fallback call analysis:") {
		t.Fatalf("expected 'Fallback call analysis:' in output, got: %s", out)
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

func TestStatusCommand_ShowsFallbackCallRatio(t *testing.T) {
	deps, stdout, stderr, db := setupStatusTest(t)

	from := graph.Node{
		Namespace:     requestctx.DefaultNamespace,
		QualifiedName: "default.From",
		Kind:          graph.NodeKindFunction,
		Name:          "From",
		FilePath:      "from.go",
		StartLine:     1,
		EndLine:       2,
		Language:      "go",
	}
	to := graph.Node{
		Namespace:     requestctx.DefaultNamespace,
		QualifiedName: "default.To",
		Kind:          graph.NodeKindFunction,
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

	edges := []graph.Edge{
		{
			Namespace:   requestctx.DefaultNamespace,
			FromNodeID:  from.ID,
			ToNodeID:    to.ID,
			Kind:        graph.EdgeKindCalls,
			FilePath:    "from.go",
			Line:        1,
			Fingerprint: "calls:from.go:From:1",
		},
		{
			Namespace:   requestctx.DefaultNamespace,
			FromNodeID:  from.ID,
			ToNodeID:    to.ID,
			Kind:        graph.EdgeKindCalls,
			FilePath:    "from.go",
			Line:        2,
			Fingerprint: "calls:from.go:From:2",
		},
		{
			Namespace:   requestctx.DefaultNamespace,
			FromNodeID:  from.ID,
			ToNodeID:    to.ID,
			Kind:        graph.EdgeKindCalls,
			FilePath:    "from.go",
			Line:        3,
			Fingerprint: "calls:from.go:From:3",
		},
		{
			Namespace:   requestctx.DefaultNamespace,
			FromNodeID:  from.ID,
			ToNodeID:    to.ID,
			Kind:        graph.EdgeKindFallbackCalls,
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
	if !strings.Contains(out, "Fallback call analysis:") {
		t.Fatalf("expected 'Fallback call analysis:' in output, got: %s", out)
	}
	if !strings.Contains(out, "calls: 3") {
		t.Fatalf("expected calls: 3 in output, got: %s", out)
	}
	if !strings.Contains(out, "fallback_calls: 1") {
		t.Fatalf("expected fallback_calls: 1 in output, got: %s", out)
	}
	if !strings.Contains(out, "fallback_ratio: 25.00%") {
		t.Fatalf("expected fallback_ratio: 25.00%% in output, got: %s", out)
	}
}

func TestStatusCommand_WarnsOnElevatedFallbackRatio(t *testing.T) {
	deps, stdout, stderr, db := setupStatusTest(t)

	from := graph.Node{
		Namespace:     requestctx.DefaultNamespace,
		QualifiedName: "default.FromWarn",
		Kind:          graph.NodeKindFunction,
		Name:          "FromWarn",
		FilePath:      "from_warn.go",
		StartLine:     1,
		EndLine:       2,
		Language:      "go",
	}
	to := graph.Node{
		Namespace:     requestctx.DefaultNamespace,
		QualifiedName: "default.ToWarn",
		Kind:          graph.NodeKindFunction,
		Name:          "ToWarn",
		FilePath:      "to_warn.go",
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

	for i := 0; i < 4; i++ {
		edge := graph.Edge{
			Namespace:   requestctx.DefaultNamespace,
			FromNodeID:  from.ID,
			ToNodeID:    to.ID,
			Kind:        graph.EdgeKindCalls,
			FilePath:    "from_warn.go",
			Line:        i + 1,
			Fingerprint: "calls:warn:" + string(rune('a'+i)),
		}
		if err := db.Create(&edge).Error; err != nil {
			t.Fatal(err)
		}
	}
	fallbackEdge := graph.Edge{
		Namespace:   requestctx.DefaultNamespace,
		FromNodeID:  from.ID,
		ToNodeID:    to.ID,
		Kind:        graph.EdgeKindFallbackCalls,
		FilePath:    "from_warn.go",
		Line:        20,
		Fingerprint: "fallback_calls:warn:20",
	}
	if err := db.Create(&fallbackEdge).Error; err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	stderr.Reset()

	if err := executeCmd(deps, stdout, stderr, "status"); err != nil {
		t.Fatalf("status: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, "Warning: fallback call ratio is elevated") {
		t.Fatalf("expected elevated fallback ratio warning, got: %s", out)
	}
	if !strings.Contains(out, "Review fallback edge quality before trusting high-confidence analysis") {
		t.Fatalf("expected high-ratio warning guidance, got: %s", out)
	}
}

func TestStatusCommand_RespectsNamespace(t *testing.T) {
	deps, stdout, stderr, db := setupStatusTest(t)

	if err := db.Create(&graph.Node{Namespace: requestctx.DefaultNamespace, QualifiedName: "default.Foo", Kind: graph.NodeKindFunction, Name: "Foo", FilePath: "default/foo.go", StartLine: 1, EndLine: 2, Language: "go"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&graph.Node{Namespace: "other", QualifiedName: "other.Bar", Kind: graph.NodeKindFunction, Name: "Bar", FilePath: "other/bar.go", StartLine: 1, EndLine: 2, Language: "go"}).Error; err != nil {
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

func TestStatusCommand_NamespaceFromConfig(t *testing.T) {
	viper.Reset()
	defer viper.Reset()

	deps, stdout, stderr, db := setupStatusTest(t)

	// One node in the default namespace, two in "backend".
	if err := db.Create(&graph.Node{Namespace: requestctx.DefaultNamespace, QualifiedName: "default.Foo", Kind: graph.NodeKindFunction, Name: "Foo", FilePath: "default/foo.go", StartLine: 1, EndLine: 2, Language: "go"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&graph.Node{Namespace: "backend", QualifiedName: "backend.Bar", Kind: graph.NodeKindFunction, Name: "Bar", FilePath: "backend/bar.go", StartLine: 1, EndLine: 2, Language: "go"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&graph.Node{Namespace: "backend", QualifiedName: "backend.Baz", Kind: graph.NodeKindFunction, Name: "Baz", FilePath: "backend/baz.go", StartLine: 1, EndLine: 2, Language: "go"}).Error; err != nil {
		t.Fatal(err)
	}

	// Config selects namespace=backend; no --namespace flag is passed, so the
	// config value must win over the flag's default.
	viper.Set("namespace", "backend")

	stdout.Reset()
	stderr.Reset()

	if err := executeCmd(deps, stdout, stderr, "status"); err != nil {
		t.Fatalf("status: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, "Nodes: 2") {
		t.Fatalf("expected config namespace 'backend' (2 nodes), got: %s", out)
	}
}
