package eval

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/tae2089/code-context-graph/internal/model"
)

func TestLoadGoldenCorpus(t *testing.T) {
	dir := t.TempDir()

	corpus := GoldenCorpus{
		Language: "go",
		File:     "sample.go",
		Nodes: []EvalNode{
			{ID: "n1", Kind: "function", Name: "Hello", File: "sample.go", StartLine: 3},
		},
		Edges: []EvalEdge{
			{Kind: "calls", From: "n1", To: "n2"},
		},
	}

	data, _ := json.MarshalIndent(corpus, "", "  ")
	langDir := filepath.Join(dir, "go")
	os.MkdirAll(langDir, 0o755)
	os.WriteFile(filepath.Join(langDir, "sample.golden.json"), data, 0o644)

	loaded, err := LoadGoldenDir(dir)
	if err != nil {
		t.Fatalf("LoadGoldenDir: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("got %d corpora, want 1", len(loaded))
	}
	if loaded[0].Language != "go" {
		t.Errorf("language: got %s, want go", loaded[0].Language)
	}
	if len(loaded[0].Nodes) != 1 {
		t.Errorf("nodes: got %d, want 1", len(loaded[0].Nodes))
	}
}

func TestNormalizeNodes(t *testing.T) {
	nodes := []EvalNode{
		{ID: "n1", Kind: "function", Name: "Hello", File: "sample.go", StartLine: 3},
		{ID: "n2", Kind: "class", Name: "World", File: "sample.go", StartLine: 10},
	}
	keys := NodeKeys(nodes)
	if len(keys) != 2 {
		t.Fatalf("got %d keys, want 2", len(keys))
	}
	if keys[0] != "function:Hello@sample.go" {
		t.Errorf("key[0]: got %s", keys[0])
	}
}

func TestCompareCorpus(t *testing.T) {
	expected := GoldenCorpus{
		Language: "go",
		File:     "sample.go",
		Nodes: []EvalNode{
			{ID: "n1", Kind: "function", Name: "Hello", File: "sample.go", StartLine: 3},
			{ID: "n2", Kind: "function", Name: "World", File: "sample.go", StartLine: 10},
		},
		Edges: []EvalEdge{
			{Kind: "calls", From: "n1", To: "n2"},
		},
	}

	actual := GoldenCorpus{
		Language: "go",
		File:     "sample.go",
		Nodes: []EvalNode{
			{ID: "n1", Kind: "function", Name: "Hello", File: "sample.go", StartLine: 3},
			{ID: "n3", Kind: "function", Name: "Extra", File: "sample.go", StartLine: 20},
		},
		Edges: []EvalEdge{
			{Kind: "calls", From: "n1", To: "n2"},
		},
	}

	report := CompareCorpus(expected, actual)
	if report.NodeMetrics.TruePositive != 1 {
		t.Errorf("node TP: got %d, want 1", report.NodeMetrics.TruePositive)
	}
	if report.NodeMetrics.FalsePositive != 1 {
		t.Errorf("node FP: got %d, want 1", report.NodeMetrics.FalsePositive)
	}
	if report.NodeMetrics.FalseNegative != 1 {
		t.Errorf("node FN: got %d, want 1", report.NodeMetrics.FalseNegative)
	}
	if report.EdgeMetrics.TruePositive != 1 {
		t.Errorf("edge TP: got %d, want 1", report.EdgeMetrics.TruePositive)
	}
}

func TestUpdateGolden(t *testing.T) {
	dir := t.TempDir()
	langDir := filepath.Join(dir, "go")
	os.MkdirAll(langDir, 0o755)

	corpus := GoldenCorpus{
		Language: "go",
		File:     "sample.go",
		Nodes: []EvalNode{
			{ID: "n1", Kind: "function", Name: "Hello", File: "sample.go", StartLine: 3},
		},
	}

	outPath := filepath.Join(langDir, "sample.golden.json")
	err := WriteGolden(outPath, corpus)
	if err != nil {
		t.Fatalf("WriteGolden: %v", err)
	}

	loaded, err := LoadGoldenDir(dir)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(loaded) != 1 || loaded[0].Nodes[0].Name != "Hello" {
		t.Errorf("round-trip failed: %+v", loaded)
	}
}

func TestNormalizeEdges_PreservesParserStageEndpoints(t *testing.T) {
	nodes := []model.Node{
		{ID: 0, QualifiedName: "sample.Hello", Kind: model.NodeKindFunction, Name: "Hello", FilePath: "sample.go", StartLine: 3, EndLine: 5},
		{ID: 0, QualifiedName: "sample.World", Kind: model.NodeKindFunction, Name: "World", FilePath: "sample.go", StartLine: 10, EndLine: 12},
	}
	edges := []model.Edge{
		{Kind: model.EdgeKindCalls, FilePath: "sample.go", Line: 4, Fingerprint: "calls:sample.go:sample.World:4"},
	}

	actual := NormalizeEdges(edges, nodes)
	if len(actual) != 1 {
		t.Fatalf("got %d edges, want 1", len(actual))
	}
	if actual[0].From != "sample.Hello" {
		t.Fatalf("from collapsed: got %q, want %q", actual[0].From, "sample.Hello")
	}
	if actual[0].To != "sample.World" {
		t.Fatalf("to mismatch: got %q, want %q", actual[0].To, "sample.World")
	}
}

func TestNormalizeEdges_PreservesFallbackCallEndpoints(t *testing.T) {
	nodes := []model.Node{
		{ID: 0, QualifiedName: "sample.Hello", Kind: model.NodeKindFunction, Name: "Hello", FilePath: "sample.go", StartLine: 3, EndLine: 5},
		{ID: 0, QualifiedName: "sample.World", Kind: model.NodeKindFunction, Name: "World", FilePath: "sample.go", StartLine: 10, EndLine: 12},
	}
	edges := []model.Edge{
		{Kind: model.EdgeKindFallbackCalls, FilePath: "sample.go", Line: 4, Fingerprint: "fallback_calls:sample.go:sample.World:4"},
	}

	actual := NormalizeEdges(edges, nodes)
	if len(actual) != 1 {
		t.Fatalf("got %d edges, want 1", len(actual))
	}
	if actual[0].From != "sample.Hello" {
		t.Fatalf("from collapsed: got %q, want %q", actual[0].From, "sample.Hello")
	}
	if actual[0].To != "sample.World" {
		t.Fatalf("to mismatch: got %q, want %q", actual[0].To, "sample.World")
	}
}

func TestNormalizeEdges_ImportsFromUsesFilePathAndFullTarget(t *testing.T) {
	nodes := []model.Node{
		{ID: 0, QualifiedName: "pkg.File", Kind: model.NodeKindFile, Name: "File", FilePath: "dir:with:colon/sample.go"},
		{ID: 0, QualifiedName: "pkg.Helper", Kind: model.NodeKindFunction, Name: "Helper", FilePath: "dir:with:colon/sample.go", StartLine: 3, EndLine: 5},
	}
	edges := []model.Edge{
		{Kind: model.EdgeKindImportsFrom, FilePath: "dir:with:colon/sample.go", Line: 4, Fingerprint: "imports_from:dir:with:colon/sample.go:github.com/acme/lib:v2/util:4"},
	}

	actual := NormalizeEdges(edges, nodes)
	if len(actual) != 1 {
		t.Fatalf("got %d edges, want 1", len(actual))
	}
	if actual[0].From != "dir:with:colon/sample.go" {
		t.Fatalf("from mismatch: got %q, want %q", actual[0].From, "dir:with:colon/sample.go")
	}
	if actual[0].To != "github.com/acme/lib:v2/util" {
		t.Fatalf("to mismatch: got %q, want %q", actual[0].To, "github.com/acme/lib:v2/util")
	}
}

func TestNormalizeEdges_TestedByUsesTestQNameAsFrom(t *testing.T) {
	nodes := []model.Node{
		{ID: 0, QualifiedName: "sample.TestHello", Kind: model.NodeKindFunction, Name: "TestHello", FilePath: "sample_test.go", StartLine: 3, EndLine: 5},
		{ID: 0, QualifiedName: "github.com/acme/lib:v2/util.Helper", Kind: model.NodeKindFunction, Name: "Helper", FilePath: "sample.go", StartLine: 10, EndLine: 12},
	}
	edges := []model.Edge{
		{Kind: model.EdgeKindTestedBy, FilePath: "sample_test.go", Line: 4, Fingerprint: "tested_by:sample_test.go:github.com/acme/lib:v2/util.Helper:sample.TestHello"},
	}

	actual := NormalizeEdges(edges, nodes)
	if len(actual) != 1 {
		t.Fatalf("got %d edges, want 1", len(actual))
	}
	if actual[0].From != "sample.TestHello" {
		t.Fatalf("from mismatch: got %q, want %q", actual[0].From, "sample.TestHello")
	}
	if actual[0].To != "github.com/acme/lib:v2/util.Helper" {
		t.Fatalf("to mismatch: got %q, want %q", actual[0].To, "github.com/acme/lib:v2/util.Helper")
	}
}

func TestNormalizeEdges_ImplementsUsesFingerprintEndpoints(t *testing.T) {
	nodes := []model.Node{
		{ID: 0, QualifiedName: "pkg.Interface", Kind: model.NodeKindClass, Name: "Interface", FilePath: "iface.go", StartLine: 10, EndLine: 12},
		{ID: 0, QualifiedName: "pkg.Implementation", Kind: model.NodeKindClass, Name: "Implementation", FilePath: "impl.go", StartLine: 20, EndLine: 24},
	}
	edges := []model.Edge{
		{Kind: model.EdgeKindImplements, FilePath: "parser.go", Line: 0, Fingerprint: "implements:parser.go:pkg.Implementation:pkg.Interface"},
	}

	actual := NormalizeEdges(edges, nodes)
	if len(actual) != 1 {
		t.Fatalf("got %d edges, want 1", len(actual))
	}
	if actual[0].From != "pkg.Implementation" {
		t.Fatalf("from mismatch: got %q, want %q", actual[0].From, "pkg.Implementation")
	}
	if actual[0].To != "pkg.Interface" {
		t.Fatalf("to mismatch: got %q, want %q", actual[0].To, "pkg.Interface")
	}
}

func TestNormalizeEdges_InheritsUsesFingerprintEndpoints(t *testing.T) {
	nodes := []model.Node{
		{ID: 0, QualifiedName: "pkg.Child", Kind: model.NodeKindClass, Name: "Child", FilePath: "models.py", StartLine: 10, EndLine: 20},
		{ID: 0, QualifiedName: "github.com/acme/lib:v2/util.Base", Kind: model.NodeKindClass, Name: "Base", FilePath: "models.py", StartLine: 30, EndLine: 40},
	}
	edges := []model.Edge{
		{Kind: model.EdgeKindInherits, FilePath: "models.py", Line: 0, Fingerprint: model.BuildInheritsFingerprintV2("models.py", "pkg.Child", "github.com/acme/lib:v2/util.Base")},
	}

	actual := NormalizeEdges(edges, nodes)
	if len(actual) != 1 {
		t.Fatalf("got %d edges, want 1", len(actual))
	}
	if actual[0].From != "pkg.Child" {
		t.Fatalf("from mismatch: got %q, want %q", actual[0].From, "pkg.Child")
	}
	if actual[0].To != "github.com/acme/lib:v2/util.Base" {
		t.Fatalf("to mismatch: got %q, want %q", actual[0].To, "github.com/acme/lib:v2/util.Base")
	}
}

func TestNormalizeEdges_CallsUsesLastColonSafeTargetAndNumericLine(t *testing.T) {
	nodes := []model.Node{
		{ID: 0, QualifiedName: "pkg.Owner", Kind: model.NodeKindFunction, Name: "Owner", FilePath: "dir:with:colon/parser.go", StartLine: 10, EndLine: 20},
		{ID: 0, QualifiedName: "github.com/acme/lib:v2/util.Helper", Kind: model.NodeKindFunction, Name: "Helper", FilePath: "dir:with:colon/parser.go", StartLine: 30, EndLine: 40},
	}
	edges := []model.Edge{
		{Kind: model.EdgeKindCalls, FilePath: "dir:with:colon/parser.go", Line: 12, Fingerprint: "calls:dir:with:colon/parser.go:github.com/acme/lib:v2/util.Helper:12"},
	}

	actual := NormalizeEdges(edges, nodes)
	if len(actual) != 1 {
		t.Fatalf("got %d edges, want 1", len(actual))
	}
	if actual[0].From != "pkg.Owner" {
		t.Fatalf("from mismatch: got %q, want %q", actual[0].From, "pkg.Owner")
	}
	if actual[0].To != "github.com/acme/lib:v2/util.Helper" {
		t.Fatalf("to mismatch: got %q, want %q", actual[0].To, "github.com/acme/lib:v2/util.Helper")
	}
}

func TestNormalizeEdges_ContainsUsesFullTargetAfterFilePathPrefix(t *testing.T) {
	nodes := []model.Node{
		{ID: 0, QualifiedName: "pkg.Owner", Kind: model.NodeKindFunction, Name: "Owner", FilePath: "dir:with:colon/parser.go", StartLine: 10, EndLine: 20},
		{ID: 0, QualifiedName: "github.com/acme/lib:v2/util.Helper", Kind: model.NodeKindFunction, Name: "Helper", FilePath: "dir:with:colon/parser.go", StartLine: 30, EndLine: 40},
	}
	edges := []model.Edge{
		{Kind: model.EdgeKindContains, FilePath: "dir:with:colon/parser.go", Line: 11, Fingerprint: "contains:dir:with:colon/parser.go:github.com/acme/lib:v2/util.Helper"},
	}

	actual := NormalizeEdges(edges, nodes)
	if len(actual) != 1 {
		t.Fatalf("got %d edges, want 1", len(actual))
	}
	if actual[0].From != "dir:with:colon/parser.go" {
		t.Fatalf("from mismatch: got %q, want %q", actual[0].From, "dir:with:colon/parser.go")
	}
	if actual[0].To != "github.com/acme/lib:v2/util.Helper" {
		t.Fatalf("to mismatch: got %q, want %q", actual[0].To, "github.com/acme/lib:v2/util.Helper")
	}
}
