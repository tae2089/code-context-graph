package parser

import (
	"testing"

	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

func TestWireEdgeToEdgeContains(t *testing.T) {
	we := wireEdge{
		Kind:     "contains",
		FilePath: "src/foo.go",
		ToQN:     "pkg.Foo",
	}

	got, err := we.toEdge()
	if err != nil {
		t.Fatalf("toEdge() error = %v", err)
	}
	if got.Kind != graph.EdgeKindContains {
		t.Errorf("Kind = %q, want %q", got.Kind, graph.EdgeKindContains)
	}
	if got.FilePath != "src/foo.go" {
		t.Errorf("FilePath = %q, want %q", got.FilePath, "src/foo.go")
	}
	if want := "contains:src/foo.go:pkg.Foo"; got.Fingerprint != want {
		t.Errorf("Fingerprint = %q, want %q", got.Fingerprint, want)
	}
}

func TestWireEdgeToEdgeCalls(t *testing.T) {
	we := wireEdge{
		Kind:     "calls",
		FilePath: "src/foo.go",
		Line:     14,
		ToName:   "callee",
	}

	got, err := we.toEdge()
	if err != nil {
		t.Fatalf("toEdge() error = %v", err)
	}
	if got.Kind != graph.EdgeKindCalls {
		t.Errorf("Kind = %q, want %q", got.Kind, graph.EdgeKindCalls)
	}
	if got.Line != 14 {
		t.Errorf("Line = %d, want %d", got.Line, 14)
	}
	if want := "calls:src/foo.go:callee:14"; got.Fingerprint != want {
		t.Errorf("Fingerprint = %q, want %q", got.Fingerprint, want)
	}
}

func TestWireEdgeToEdgeImportsFrom(t *testing.T) {
	we := wireEdge{
		Kind:       "imports_from",
		FilePath:   "src/foo.go",
		Line:       3,
		ImportPath: "github.com/acme/bar",
	}

	got, err := we.toEdge()
	if err != nil {
		t.Fatalf("toEdge() error = %v", err)
	}
	if got.Kind != graph.EdgeKindImportsFrom {
		t.Errorf("Kind = %q, want %q", got.Kind, graph.EdgeKindImportsFrom)
	}
	if got.Line != 3 {
		t.Errorf("Line = %d, want %d", got.Line, 3)
	}
	if want := "imports_from:src/foo.go:github.com/acme/bar:3"; got.Fingerprint != want {
		t.Errorf("Fingerprint = %q, want %q", got.Fingerprint, want)
	}
}

func TestWireEdgeToEdgeTestedBy(t *testing.T) {
	we := wireEdge{
		Kind:     "tested_by",
		FilePath: "src/foo_test.go",
		ProdName: "pkg.Foo",
		TestQN:   "pkg.TestFoo",
	}

	got, err := we.toEdge()
	if err != nil {
		t.Fatalf("toEdge() error = %v", err)
	}
	if got.Kind != graph.EdgeKindTestedBy {
		t.Errorf("Kind = %q, want %q", got.Kind, graph.EdgeKindTestedBy)
	}
	if want := "tested_by:src/foo_test.go:pkg.Foo:pkg.TestFoo"; got.Fingerprint != want {
		t.Errorf("Fingerprint = %q, want %q", got.Fingerprint, want)
	}
}

func TestWireEdgeToEdgeImplements(t *testing.T) {
	we := wireEdge{
		Kind:      "implements",
		FilePath:  "src/foo.go",
		ImplQN:    "pkg.Foo",
		IfaceName: "Reader",
	}

	got, err := we.toEdge()
	if err != nil {
		t.Fatalf("toEdge() error = %v", err)
	}
	if got.Kind != graph.EdgeKindImplements {
		t.Errorf("Kind = %q, want %q", got.Kind, graph.EdgeKindImplements)
	}
	if want := "implements:src/foo.go:pkg.Foo:Reader"; got.Fingerprint != want {
		t.Errorf("Fingerprint = %q, want %q", got.Fingerprint, want)
	}
}

func TestWireEdgeToEdgeInherits(t *testing.T) {
	we := wireEdge{
		Kind:       "inherits",
		FilePath:   "src/foo.go",
		ChildQN:    "pkg.Foo",
		ParentName: "Base",
	}

	got, err := we.toEdge()
	if err != nil {
		t.Fatalf("toEdge() error = %v", err)
	}
	if got.Kind != graph.EdgeKindInherits {
		t.Errorf("Kind = %q, want %q", got.Kind, graph.EdgeKindInherits)
	}
	if want := graph.BuildInheritsFingerprintV2("src/foo.go", "pkg.Foo", "Base"); got.Fingerprint != want {
		t.Errorf("Fingerprint = %q, want %q", got.Fingerprint, want)
	}
}

func TestWireNodeToNode(t *testing.T) {
	wn := wireNode{
		QualifiedName: "pkg.Foo.bar",
		Kind:          "function",
		Name:          "bar",
		FilePath:      "src/foo.go",
		StartLine:     10,
		EndLine:       25,
		Hash:          "abc123",
		Language:      "go",
	}

	got, err := wn.toNode()
	if err != nil {
		t.Fatalf("toNode() error = %v", err)
	}

	if got.QualifiedName != "pkg.Foo.bar" {
		t.Errorf("QualifiedName = %q, want %q", got.QualifiedName, "pkg.Foo.bar")
	}
	if got.Kind != graph.NodeKindFunction {
		t.Errorf("Kind = %q, want %q", got.Kind, graph.NodeKindFunction)
	}
	if got.Name != "bar" {
		t.Errorf("Name = %q, want %q", got.Name, "bar")
	}
	if got.FilePath != "src/foo.go" {
		t.Errorf("FilePath = %q, want %q", got.FilePath, "src/foo.go")
	}
	if got.StartLine != 10 || got.EndLine != 25 {
		t.Errorf("lines = (%d,%d), want (10,25)", got.StartLine, got.EndLine)
	}
	if got.Hash != "abc123" {
		t.Errorf("Hash = %q, want %q", got.Hash, "abc123")
	}
	if got.Language != "go" {
		t.Errorf("Language = %q, want %q", got.Language, "go")
	}
}

func TestWireNodeToNodeRejectsInvalidKind(t *testing.T) {
	for _, kind := range []string{"func", "struct", "bogus", ""} {
		t.Run(kind, func(t *testing.T) {
			wn := wireNode{QualifiedName: "pkg.Foo", Kind: kind, Name: "Foo", FilePath: "src/foo.go", Language: "go"}
			if _, err := wn.toNode(); err == nil {
				t.Errorf("toNode() for kind %q = nil error, want error", kind)
			}
		})
	}
}

func TestWireEdgeToEdgeRejectsUnsupportedKinds(t *testing.T) {
	// Core-internal kinds (produced by resolve/package phases) and unknown kinds
	// must not come from a plugin — they are rejected so bad input surfaces as an error.
	for _, kind := range []string{"fallback_calls", "depends_on", "references", "bogus", ""} {
		t.Run(kind, func(t *testing.T) {
			we := wireEdge{Kind: kind, FilePath: "src/foo.go"}
			if _, err := we.toEdge(); err == nil {
				t.Errorf("toEdge() for kind %q = nil error, want error", kind)
			}
		})
	}
}
