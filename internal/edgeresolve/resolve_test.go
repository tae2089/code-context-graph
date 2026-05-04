package edgeresolve

import (
	"context"
	"path"
	"strings"
	"testing"

	"github.com/tae2089/code-context-graph/internal/model"
)

type fakeLookup struct {
	nodes         []model.Node
	fileNodesByFP map[string][]model.Node
}

func (f fakeLookup) GetFileNodesByPathSuffix(_ context.Context, suffix string) ([]model.Node, error) {
	suffix = strings.Trim(suffix, "/")
	var out []model.Node
	var exact []model.Node
	bestDepth := -1
	for _, n := range f.nodes {
		if n.Kind != model.NodeKindFile {
			continue
		}
		dir := strings.Trim(path.Dir(n.FilePath), "/")
		if dir == "." || dir == "" {
			continue
		}
		if suffix == dir {
			exact = append(exact, n)
			continue
		}
		if depth := commonSuffixDepth(suffix, dir); depth > 0 {
			if depth > bestDepth {
				bestDepth = depth
				out = []model.Node{n}
				continue
			}
			if depth == bestDepth {
				out = append(out, n)
			}
		}
	}
	if len(exact) > 0 {
		return exact, nil
	}
	return out, nil
}

func (f fakeLookup) GetNodesByFiles(_ context.Context, filePaths []string) (map[string][]model.Node, error) {
	if f.fileNodesByFP != nil {
		out := make(map[string][]model.Node, len(filePaths))
		for _, fp := range filePaths {
			out[fp] = append(out[fp], f.fileNodesByFP[fp]...)
		}
		return out, nil
	}
	set := make(map[string]bool, len(filePaths))
	for _, fp := range filePaths {
		set[fp] = true
	}
	out := make(map[string][]model.Node)
	for _, n := range f.nodes {
		if set[n.FilePath] {
			out[n.FilePath] = append(out[n.FilePath], n)
		}
	}
	return out, nil
}

func (f fakeLookup) GetNodesByIDs(_ context.Context, ids []uint) ([]model.Node, error) {
	set := make(map[uint]bool, len(ids))
	for _, id := range ids {
		set[id] = true
	}
	var out []model.Node
	for _, n := range f.nodes {
		if set[n.ID] {
			out = append(out, n)
		}
	}
	return out, nil
}

func (f fakeLookup) GetNodesByQualifiedNames(_ context.Context, names []string) (map[string][]model.Node, error) {
	set := make(map[string]bool, len(names))
	for _, name := range names {
		set[name] = true
	}
	out := make(map[string][]model.Node)
	for _, n := range f.nodes {
		if set[n.QualifiedName] {
			out[n.QualifiedName] = append(out[n.QualifiedName], n)
		}
	}
	return out, nil
}

func TestResolveCallsConnectsBareFunctionCall(t *testing.T) {
	lookup := fakeLookup{nodes: []model.Node{
		{ID: 1, QualifiedName: "pkg.A", Name: "A", Kind: model.NodeKindFunction, FilePath: "a.go", StartLine: 3, EndLine: 5, Language: "go"},
		{ID: 2, QualifiedName: "pkg.B", Name: "B", Kind: model.NodeKindFunction, FilePath: "a.go", StartLine: 7, EndLine: 7, Language: "go"},
	}}
	edges, err := Resolve(context.Background(), lookup, []model.Edge{{
		Kind:        model.EdgeKindCalls,
		FilePath:    "a.go",
		Line:        4,
		Fingerprint: "calls:a.go:B:4",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if got := edges[0].FromNodeID; got != 1 {
		t.Fatalf("FromNodeID=%d, want 1", got)
	}
	if got := edges[0].ToNodeID; got != 2 {
		t.Fatalf("ToNodeID=%d, want 2", got)
	}
}

func TestDispatchForLanguage_GoRustTSJavaKotlinRegistered(t *testing.T) {
	if dispatchForLanguage("go") == nil {
		t.Fatal("expected Go dispatch to be registered")
	}
	if dispatchForLanguage("rust") == nil {
		t.Fatal("expected Rust dispatch to be registered")
	}
	if dispatchForLanguage("python") != nil {
		t.Fatal("expected Python dispatch to remain unregistered")
	}
	if dispatchForLanguage("typescript") == nil {
		t.Fatal("expected TypeScript dispatch to be registered")
	}
	if dispatchForLanguage("java") == nil {
		t.Fatal("expected Java dispatch to be registered")
	}
	if dispatchForLanguage("kotlin") == nil {
		t.Fatal("expected Kotlin dispatch to be registered")
	}
}

func TestResolveCallsConnectsUniqueSelectorMethodInSameFile(t *testing.T) {
	lookup := fakeLookup{nodes: []model.Node{
		{ID: 1, QualifiedName: "flows.Tracer.TraceFlow", Name: "TraceFlow", Kind: model.NodeKindFunction, FilePath: "flows.go", StartLine: 10, EndLine: 12, Language: "go"},
		{ID: 2, QualifiedName: "flows.Tracer.TraceFlowBounded", Name: "TraceFlowBounded", Kind: model.NodeKindFunction, FilePath: "flows.go", StartLine: 14, EndLine: 16, Language: "go"},
	}}
	edges, err := Resolve(context.Background(), lookup, []model.Edge{{
		Kind:        model.EdgeKindCalls,
		FilePath:    "flows.go",
		Line:        11,
		Fingerprint: "calls:flows.go:t.TraceFlowBounded:11",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if got := edges[0].FromNodeID; got != 1 {
		t.Fatalf("FromNodeID=%d, want 1", got)
	}
	if got := edges[0].ToNodeID; got != 2 {
		t.Fatalf("ToNodeID=%d, want 2", got)
	}
}

func TestResolveCallsLeavesAmbiguousCalleeUnresolved(t *testing.T) {
	lookup := fakeLookup{nodes: []model.Node{
		{ID: 1, QualifiedName: "pkg.A", Name: "A", Kind: model.NodeKindFunction, FilePath: "a.go", StartLine: 3, EndLine: 5, Language: "go"},
		{ID: 2, QualifiedName: "pkg.X.Save", Name: "Save", Kind: model.NodeKindFunction, FilePath: "a.go", StartLine: 7, EndLine: 7, Language: "go"},
		{ID: 3, QualifiedName: "pkg.Y.Save", Name: "Save", Kind: model.NodeKindFunction, FilePath: "a.go", StartLine: 9, EndLine: 9, Language: "go"},
	}}
	edges, err := Resolve(context.Background(), lookup, []model.Edge{{
		Kind:        model.EdgeKindCalls,
		FilePath:    "a.go",
		Line:        4,
		Fingerprint: "calls:a.go:svc.Save:4",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if got := edges[0].FromNodeID; got != 1 {
		t.Fatalf("FromNodeID=%d, want 1", got)
	}
	if got := edges[0].ToNodeID; got != 0 {
		t.Fatalf("ToNodeID=%d, want unresolved 0", got)
	}
}

func TestResolveCallsLeavesMissingCallerUnresolved(t *testing.T) {
	lookup := fakeLookup{nodes: []model.Node{
		{ID: 2, QualifiedName: "pkg.B", Name: "B", Kind: model.NodeKindFunction, FilePath: "a.go", StartLine: 7, EndLine: 7, Language: "go"},
	}}
	edges, err := Resolve(context.Background(), lookup, []model.Edge{{
		Kind:        model.EdgeKindCalls,
		FilePath:    "a.go",
		Line:        1,
		Fingerprint: "calls:a.go:B:1",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if got := edges[0].FromNodeID; got != 0 {
		t.Fatalf("FromNodeID=%d, want unresolved 0", got)
	}
}

func TestResolveImplementsAndInterfaceSelectorCall(t *testing.T) {
	lookup := fakeLookup{nodes: []model.Node{
		{ID: 1, QualifiedName: "main.handler.Start", Name: "Start", Kind: model.NodeKindFunction, FilePath: "main.go", StartLine: 10, EndLine: 12, Language: "go"},
		{ID: 2, QualifiedName: "mcp.FlowTracer", Name: "FlowTracer", Kind: model.NodeKindType, FilePath: "deps.go", StartLine: 3, EndLine: 5, Language: "go"},
		{ID: 3, QualifiedName: "flows.Tracer", Name: "Tracer", Kind: model.NodeKindClass, FilePath: "flows.go", StartLine: 3, EndLine: 3, Language: "go"},
		{ID: 4, QualifiedName: "flows.Tracer.TraceFlow", Name: "TraceFlow", Kind: model.NodeKindFunction, FilePath: "flows.go", StartLine: 5, EndLine: 7, Language: "go"},
	}}
	edges, err := Resolve(context.Background(), lookup, []model.Edge{
		{Kind: model.EdgeKindImplements, FilePath: "main.go", Line: 4, Fingerprint: "implements:main.go:flows.Tracer:mcp.FlowTracer"},
		{Kind: model.EdgeKindCalls, FilePath: "main.go", Line: 11, Fingerprint: "calls:main.go:h.deps.FlowTracer.TraceFlow:11"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if edges[0].FromNodeID != 3 || edges[0].ToNodeID != 2 {
		t.Fatalf("implements edge endpoints=(%d,%d), want (3,2)", edges[0].FromNodeID, edges[0].ToNodeID)
	}
	if edges[1].FromNodeID != 1 || edges[1].ToNodeID != 4 {
		t.Fatalf("call edge endpoints=(%d,%d), want (1,4)", edges[1].FromNodeID, edges[1].ToNodeID)
	}
}

func TestResolveInterfaceSelectorLeavesAmbiguousImplementersUnresolved(t *testing.T) {
	lookup := fakeLookup{nodes: []model.Node{
		{ID: 1, QualifiedName: "main.handler.Start", Name: "Start", Kind: model.NodeKindFunction, FilePath: "main.go", StartLine: 10, EndLine: 12, Language: "go"},
		{ID: 2, QualifiedName: "mcp.FlowTracer", Name: "FlowTracer", Kind: model.NodeKindType, FilePath: "deps.go", StartLine: 3, EndLine: 5, Language: "go"},
		{ID: 3, QualifiedName: "flows.Tracer", Name: "Tracer", Kind: model.NodeKindClass, FilePath: "flows.go", StartLine: 3, EndLine: 3, Language: "go"},
		{ID: 4, QualifiedName: "flows.Tracer.TraceFlow", Name: "TraceFlow", Kind: model.NodeKindFunction, FilePath: "flows.go", StartLine: 5, EndLine: 7, Language: "go"},
		{ID: 5, QualifiedName: "alt.Tracer", Name: "Tracer", Kind: model.NodeKindClass, FilePath: "alt.go", StartLine: 3, EndLine: 3, Language: "go"},
		{ID: 6, QualifiedName: "alt.Tracer.TraceFlow", Name: "TraceFlow", Kind: model.NodeKindFunction, FilePath: "alt.go", StartLine: 5, EndLine: 7, Language: "go"},
	}}
	edges, err := Resolve(context.Background(), lookup, []model.Edge{
		{Kind: model.EdgeKindImplements, FilePath: "main.go", Line: 4, Fingerprint: "implements:main.go:flows.Tracer:mcp.FlowTracer"},
		{Kind: model.EdgeKindImplements, FilePath: "main.go", Line: 5, Fingerprint: "implements:main.go:alt.Tracer:mcp.FlowTracer"},
		{Kind: model.EdgeKindCalls, FilePath: "main.go", Line: 11, Fingerprint: "calls:main.go:h.deps.FlowTracer.TraceFlow:11"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if edges[2].ToNodeID != 0 {
		t.Fatalf("ambiguous call ToNodeID=%d, want unresolved 0", edges[2].ToNodeID)
	}
}

func TestResolveRustSameReceiverCallRemainsUnresolvedWithoutSafeReceiverContract(t *testing.T) {
	lookup := fakeLookup{nodes: []model.Node{
		{ID: 1, QualifiedName: "Foo.run", Name: "run", Kind: model.NodeKindFunction, FilePath: "main.rs", StartLine: 3, EndLine: 5, Language: "rust"},
		{ID: 2, QualifiedName: "Foo", Name: "Foo", Kind: model.NodeKindClass, FilePath: "main.rs", StartLine: 1, EndLine: 1, Language: "rust"},
		{ID: 3, QualifiedName: "Foo.bar", Name: "bar", Kind: model.NodeKindFunction, FilePath: "main.rs", StartLine: 6, EndLine: 7, Language: "rust"},
		{ID: 4, QualifiedName: "Other.bar", Name: "bar", Kind: model.NodeKindFunction, FilePath: "main.rs", StartLine: 9, EndLine: 10, Language: "rust"},
	}}
	edges, err := Resolve(context.Background(), lookup, []model.Edge{{
		Kind:        model.EdgeKindCalls,
		FilePath:    "main.rs",
		Line:        4,
		Fingerprint: "calls:main.rs:self.bar:4",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if edges[0].FromNodeID != 1 {
		t.Fatalf("call edge FromNodeID=%d, want 1", edges[0].FromNodeID)
	}
	if edges[0].ToNodeID != 0 {
		t.Fatalf("call edge ToNodeID=%d, want unresolved 0", edges[0].ToNodeID)
	}
}

func TestResolveRustTraitMethodCall(t *testing.T) {
	lookup := fakeLookup{nodes: []model.Node{
		{ID: 1, QualifiedName: "main", Name: "main", Kind: model.NodeKindFunction, FilePath: "main.rs", StartLine: 7, EndLine: 10, Language: "rust"},
		{ID: 2, QualifiedName: "Foo", Name: "Foo", Kind: model.NodeKindClass, FilePath: "main.rs", StartLine: 1, EndLine: 1, Language: "rust"},
		{ID: 3, QualifiedName: "MyTrait", Name: "MyTrait", Kind: model.NodeKindType, FilePath: "main.rs", StartLine: 3, EndLine: 3, Language: "rust"},
		{ID: 4, QualifiedName: "Foo.bar", Name: "bar", Kind: model.NodeKindFunction, FilePath: "main.rs", StartLine: 4, EndLine: 5, Language: "rust"},
	}}
	edges, err := Resolve(context.Background(), lookup, []model.Edge{
		{Kind: model.EdgeKindImplements, FilePath: "main.rs", Line: 3, Fingerprint: "implements:main.rs:Foo:MyTrait"},
		{Kind: model.EdgeKindCalls, FilePath: "main.rs", Line: 9, Fingerprint: "calls:main.rs:MyTrait::bar:9"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if edges[1].ToNodeID != 4 {
		t.Fatalf("call edge ToNodeID=%d, want 4", edges[1].ToNodeID)
	}
}

func TestResolveRustTraitMethodCallLeavesAmbiguousImplementersUnresolved(t *testing.T) {
	lookup := fakeLookup{nodes: []model.Node{
		{ID: 1, QualifiedName: "main", Name: "main", Kind: model.NodeKindFunction, FilePath: "main.rs", StartLine: 9, EndLine: 12, Language: "rust"},
		{ID: 2, QualifiedName: "Foo", Name: "Foo", Kind: model.NodeKindClass, FilePath: "main.rs", StartLine: 1, EndLine: 1, Language: "rust"},
		{ID: 3, QualifiedName: "AltFoo", Name: "AltFoo", Kind: model.NodeKindClass, FilePath: "main.rs", StartLine: 2, EndLine: 2, Language: "rust"},
		{ID: 4, QualifiedName: "MyTrait", Name: "MyTrait", Kind: model.NodeKindType, FilePath: "main.rs", StartLine: 4, EndLine: 4, Language: "rust"},
		{ID: 5, QualifiedName: "Foo.bar", Name: "bar", Kind: model.NodeKindFunction, FilePath: "main.rs", StartLine: 5, EndLine: 6, Language: "rust"},
		{ID: 6, QualifiedName: "AltFoo.bar", Name: "bar", Kind: model.NodeKindFunction, FilePath: "main.rs", StartLine: 7, EndLine: 8, Language: "rust"},
	}}
	edges, err := Resolve(context.Background(), lookup, []model.Edge{
		{Kind: model.EdgeKindImplements, FilePath: "main.rs", Line: 4, Fingerprint: "implements:main.rs:Foo:MyTrait"},
		{Kind: model.EdgeKindImplements, FilePath: "main.rs", Line: 4, Fingerprint: "implements:main.rs:AltFoo:MyTrait"},
		{Kind: model.EdgeKindCalls, FilePath: "main.rs", Line: 11, Fingerprint: "calls:main.rs:MyTrait::bar:11"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if edges[2].ToNodeID != 0 {
		t.Fatalf("ambiguous rust call ToNodeID=%d, want unresolved 0", edges[2].ToNodeID)
	}
}

func TestResolveRustAssociatedFunctionDoesNotUseTraitDispatch(t *testing.T) {
	lookup := fakeLookup{nodes: []model.Node{
		{ID: 1, QualifiedName: "main", Name: "main", Kind: model.NodeKindFunction, FilePath: "main.rs", StartLine: 7, EndLine: 10, Language: "rust"},
		{ID: 2, QualifiedName: "Foo", Name: "Foo", Kind: model.NodeKindClass, FilePath: "main.rs", StartLine: 1, EndLine: 1, Language: "rust"},
		{ID: 3, QualifiedName: "MyTrait", Name: "MyTrait", Kind: model.NodeKindType, FilePath: "main.rs", StartLine: 3, EndLine: 3, Language: "rust"},
		{ID: 4, QualifiedName: "Foo.bar", Name: "bar", Kind: model.NodeKindFunction, FilePath: "main.rs", StartLine: 4, EndLine: 5, Language: "rust"},
	}}
	edges, err := Resolve(context.Background(), lookup, []model.Edge{
		{Kind: model.EdgeKindImplements, FilePath: "main.rs", Line: 3, Fingerprint: "implements:main.rs:Foo:MyTrait"},
		{Kind: model.EdgeKindCalls, FilePath: "main.rs", Line: 9, Fingerprint: "calls:main.rs:Foo::bar:9"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if edges[1].ToNodeID != 0 {
		t.Fatalf("associated function call ToNodeID=%d, want unresolved 0", edges[1].ToNodeID)
	}
}

func TestImplementsEndpointsHandlesRustQualifiedTraitPath(t *testing.T) {
	impl, iface, ok := implementsEndpoints(model.Edge{
		FilePath:    "main.rs",
		Fingerprint: "implements:main.rs:Foo:crate::traits::MyTrait",
	})
	if !ok {
		t.Fatal("implementsEndpoints returned ok=false, want true")
	}
	if impl != "Foo" || iface != "crate::traits::MyTrait" {
		t.Fatalf("implementsEndpoints() = (%q, %q), want (%q, %q)", impl, iface, "Foo", "crate::traits::MyTrait")
	}
}

func TestResolveRustQualifiedTraitPathResolvesExactTraitPath(t *testing.T) {
	lookup := fakeLookup{nodes: []model.Node{
		{ID: 1, QualifiedName: "main", Name: "main", Kind: model.NodeKindFunction, FilePath: "main.rs", StartLine: 7, EndLine: 10, Language: "rust"},
		{ID: 2, QualifiedName: "Foo", Name: "Foo", Kind: model.NodeKindClass, FilePath: "main.rs", StartLine: 1, EndLine: 1, Language: "rust"},
		{ID: 3, QualifiedName: "crate::traits::MyTrait", Name: "MyTrait", Kind: model.NodeKindType, FilePath: "main.rs", StartLine: 3, EndLine: 3, Language: "rust"},
		{ID: 4, QualifiedName: "Foo.bar", Name: "bar", Kind: model.NodeKindFunction, FilePath: "main.rs", StartLine: 4, EndLine: 5, Language: "rust"},
	}}
	edges, err := Resolve(context.Background(), lookup, []model.Edge{
		{Kind: model.EdgeKindImplements, FilePath: "main.rs", Line: 3, Fingerprint: "implements:main.rs:Foo:crate::traits::MyTrait"},
		{Kind: model.EdgeKindCalls, FilePath: "main.rs", Line: 9, Fingerprint: "calls:main.rs:crate::traits::MyTrait::bar:9"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if edges[0].FromNodeID != 2 || edges[0].ToNodeID != 3 {
		t.Fatalf("implements edge endpoints=(%d,%d), want (2,3)", edges[0].FromNodeID, edges[0].ToNodeID)
	}
	if edges[1].ToNodeID != 4 {
		t.Fatalf("qualified trait path call ToNodeID=%d, want 4", edges[1].ToNodeID)
	}
}

func TestResolveRustQualifiedTraitPathDoesNotFallbackByBareName(t *testing.T) {
	lookup := fakeLookup{nodes: []model.Node{
		{ID: 1, QualifiedName: "main", Name: "main", Kind: model.NodeKindFunction, FilePath: "main.rs", StartLine: 7, EndLine: 10, Language: "rust"},
		{ID: 2, QualifiedName: "Foo", Name: "Foo", Kind: model.NodeKindClass, FilePath: "main.rs", StartLine: 1, EndLine: 1, Language: "rust"},
		{ID: 3, QualifiedName: "other::MyTrait", Name: "MyTrait", Kind: model.NodeKindType, FilePath: "main.rs", StartLine: 3, EndLine: 3, Language: "rust"},
		{ID: 4, QualifiedName: "Foo.bar", Name: "bar", Kind: model.NodeKindFunction, FilePath: "main.rs", StartLine: 4, EndLine: 5, Language: "rust"},
	}}
	edges, err := Resolve(context.Background(), lookup, []model.Edge{
		{Kind: model.EdgeKindImplements, FilePath: "main.rs", Line: 3, Fingerprint: "implements:main.rs:Foo:other::MyTrait"},
		{Kind: model.EdgeKindCalls, FilePath: "main.rs", Line: 9, Fingerprint: "calls:main.rs:crate::traits::MyTrait::bar:9"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if edges[1].ToNodeID != 0 {
		t.Fatalf("qualified trait path fallback ToNodeID=%d, want unresolved 0", edges[1].ToNodeID)
	}
	}

func TestResolveRustUFCSCall(t *testing.T) {
	lookup := fakeLookup{nodes: []model.Node{
		{ID: 1, QualifiedName: "main", Name: "main", Kind: model.NodeKindFunction, FilePath: "main.rs", StartLine: 7, EndLine: 10, Language: "rust"},
		{ID: 2, QualifiedName: "Foo", Name: "Foo", Kind: model.NodeKindClass, FilePath: "main.rs", StartLine: 1, EndLine: 1, Language: "rust"},
		{ID: 3, QualifiedName: "MyTrait", Name: "MyTrait", Kind: model.NodeKindType, FilePath: "main.rs", StartLine: 3, EndLine: 3, Language: "rust"},
		{ID: 4, QualifiedName: "Foo.bar", Name: "bar", Kind: model.NodeKindFunction, FilePath: "main.rs", StartLine: 4, EndLine: 5, Language: "rust"},
	}}
	edges, err := Resolve(context.Background(), lookup, []model.Edge{
		{Kind: model.EdgeKindImplements, FilePath: "main.rs", Line: 3, Fingerprint: "implements:main.rs:Foo:MyTrait"},
		{Kind: model.EdgeKindCalls, FilePath: "main.rs", Line: 9, Fingerprint: "calls:main.rs:<Foo as MyTrait>::bar:9"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if edges[1].ToNodeID != 4 {
		t.Fatalf("UFCS call ToNodeID=%d, want 4", edges[1].ToNodeID)
	}
	}

func TestResolveRustUFCSCallLeavesMismatchedConcreteTypeUnresolved(t *testing.T) {
	lookup := fakeLookup{nodes: []model.Node{
		{ID: 1, QualifiedName: "main", Name: "main", Kind: model.NodeKindFunction, FilePath: "main.rs", StartLine: 7, EndLine: 10, Language: "rust"},
		{ID: 2, QualifiedName: "Foo", Name: "Foo", Kind: model.NodeKindClass, FilePath: "main.rs", StartLine: 1, EndLine: 1, Language: "rust"},
		{ID: 3, QualifiedName: "MyTrait", Name: "MyTrait", Kind: model.NodeKindType, FilePath: "main.rs", StartLine: 3, EndLine: 3, Language: "rust"},
		{ID: 4, QualifiedName: "Foo.bar", Name: "bar", Kind: model.NodeKindFunction, FilePath: "main.rs", StartLine: 4, EndLine: 5, Language: "rust"},
	}}
	edges, err := Resolve(context.Background(), lookup, []model.Edge{
		{Kind: model.EdgeKindImplements, FilePath: "main.rs", Line: 3, Fingerprint: "implements:main.rs:Foo:MyTrait"},
		{Kind: model.EdgeKindCalls, FilePath: "main.rs", Line: 9, Fingerprint: "calls:main.rs:<Other as MyTrait>::bar:9"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if edges[1].ToNodeID != 0 {
		t.Fatalf("UFCS mismatched concrete type ToNodeID=%d, want unresolved 0", edges[1].ToNodeID)
	}
}

func TestResolveImportsFromBindsFileEndpoints(t *testing.T) {
	lookup := fakeLookup{nodes: []model.Node{
		{ID: 10, QualifiedName: "main.go", Name: "main.go", Kind: model.NodeKindFile, FilePath: "main.go", Language: "go"},
		{ID: 20, QualifiedName: "fmt", Name: "fmt", Kind: model.NodeKindFile, FilePath: "vendor/fmt/fmt.go", Language: "go"},
	}}
	edges, err := Resolve(context.Background(), lookup, []model.Edge{{
		Kind:        model.EdgeKindImportsFrom,
		FilePath:    "main.go",
		Line:        2,
		Fingerprint: "imports_from:main.go:fmt:2",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if got := edges[0].FromNodeID; got != 10 {
		t.Fatalf("FromNodeID=%d, want 10", got)
	}
	if got := edges[0].ToNodeID; got != 20 {
		t.Fatalf("ToNodeID=%d, want 20", got)
	}
}

func TestResolveImportsFromLeavesUnknownTargetUnresolved(t *testing.T) {
	lookup := fakeLookup{nodes: []model.Node{
		{ID: 10, QualifiedName: "main.go", Name: "main.go", Kind: model.NodeKindFile, FilePath: "main.go", Language: "go"},
	}}
	edges, err := Resolve(context.Background(), lookup, []model.Edge{{
		Kind:        model.EdgeKindImportsFrom,
		FilePath:    "main.go",
		Line:        2,
		Fingerprint: "imports_from:main.go:external/unknown:2",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if got := edges[0].FromNodeID; got != 10 {
		t.Fatalf("FromNodeID=%d, want 10", got)
	}
	if got := edges[0].ToNodeID; got != 0 {
		t.Fatalf("ToNodeID=%d, want unresolved 0", got)
	}
}

func TestResolveImportsFromBindsInternalPackageByImportPathSuffix(t *testing.T) {
	lookup := fakeLookup{nodes: []model.Node{
		{ID: 10, QualifiedName: "cmd/main.go", Name: "cmd/main.go", Kind: model.NodeKindFile, FilePath: "cmd/main.go", Language: "go"},
		{ID: 20, QualifiedName: "internal/mcp/deps.go", Name: "internal/mcp/deps.go", Kind: model.NodeKindFile, FilePath: "internal/mcp/deps.go", Language: "go"},
	}}
	edges, err := Resolve(context.Background(), lookup, []model.Edge{{
		Kind:        model.EdgeKindImportsFrom,
		FilePath:    "cmd/main.go",
		Line:        2,
		Fingerprint: "imports_from:cmd/main.go:github.com/example/project/internal/mcp:2",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if got := edges[0].FromNodeID; got != 10 {
		t.Fatalf("FromNodeID=%d, want 10", got)
	}
	if got := edges[0].ToNodeID; got != 20 {
		t.Fatalf("ToNodeID=%d, want 20", got)
	}
}

func TestResolveImportsFromPrefersExactDirectoryMatch(t *testing.T) {
	lookup := fakeLookup{nodes: []model.Node{
		{ID: 10, QualifiedName: "cmd/main.go", Name: "cmd/main.go", Kind: model.NodeKindFile, FilePath: "cmd/main.go", Language: "go"},
		{ID: 20, QualifiedName: "internal/mcp/deps.go", Name: "internal/mcp/deps.go", Kind: model.NodeKindFile, FilePath: "internal/mcp/deps.go", Language: "go"},
		{ID: 21, QualifiedName: "pkg/internal/mcp/deps.go", Name: "pkg/internal/mcp/deps.go", Kind: model.NodeKindFile, FilePath: "pkg/internal/mcp/deps.go", Language: "go"},
	}}
	edges, err := Resolve(context.Background(), lookup, []model.Edge{{
		Kind:        model.EdgeKindImportsFrom,
		FilePath:    "cmd/main.go",
		Line:        2,
		Fingerprint: "imports_from:cmd/main.go:internal/mcp:2",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if got := edges[0].ToNodeID; got != 20 {
		t.Fatalf("ToNodeID=%d, want exact dir match 20", got)
	}
}

func TestResolveImportsFromBindsLexicographicRepresentativeForMultiFilePackage(t *testing.T) {
	lookup := fakeLookup{nodes: []model.Node{
		{ID: 10, QualifiedName: "cmd/main.go", Name: "cmd/main.go", Kind: model.NodeKindFile, FilePath: "cmd/main.go", Language: "go"},
		{ID: 21, QualifiedName: "internal/mcp/z.go", Name: "internal/mcp/z.go", Kind: model.NodeKindFile, FilePath: "internal/mcp/z.go", Language: "go"},
		{ID: 20, QualifiedName: "internal/mcp/a.go", Name: "internal/mcp/a.go", Kind: model.NodeKindFile, FilePath: "internal/mcp/a.go", Language: "go"},
	}}
	edges, err := Resolve(context.Background(), lookup, []model.Edge{{
		Kind:        model.EdgeKindImportsFrom,
		FilePath:    "cmd/main.go",
		Line:        2,
		Fingerprint: "imports_from:cmd/main.go:internal/mcp:2",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if got := edges[0].ToNodeID; got != 20 {
		t.Fatalf("ToNodeID=%d, want lexicographic representative 20", got)
	}
}

func TestResolveImportsFromPrefersPackageNodeOverFileRepresentative(t *testing.T) {
	lookup := fakeLookup{nodes: []model.Node{
		{ID: 10, QualifiedName: "cmd/main.go", Name: "cmd/main.go", Kind: model.NodeKindFile, FilePath: "cmd/main.go", Language: "go"},
		{ID: 20, QualifiedName: "internal/mcp/a.go", Name: "internal/mcp/a.go", Kind: model.NodeKindFile, FilePath: "internal/mcp/a.go", Language: "go"},
		{ID: 21, QualifiedName: "internal/mcp/z.go", Name: "internal/mcp/z.go", Kind: model.NodeKindFile, FilePath: "internal/mcp/z.go", Language: "go"},
		{ID: 30, QualifiedName: "github.com/example/project/internal/mcp", Name: "mcp", Kind: model.NodeKindPackage, FilePath: "internal/mcp", Language: "go"},
	}}
	edges, err := Resolve(context.Background(), lookup, []model.Edge{{
		Kind:        model.EdgeKindImportsFrom,
		FilePath:    "cmd/main.go",
		Line:        2,
		Fingerprint: "imports_from:cmd/main.go:github.com/example/project/internal/mcp:2",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if got := edges[0].ToNodeID; got != 30 {
		t.Fatalf("ToNodeID=%d, want package node 30", got)
	}
}

func TestResolveImportsFromPrefersAliasPackageNodeOverFiles(t *testing.T) {
	lookup := fakeLookup{nodes: []model.Node{
		{ID: 10, QualifiedName: "src/app/main.ts", Name: "src/app/main.ts", Kind: model.NodeKindFile, FilePath: "src/app/main.ts", Language: "typescript"},
		{ID: 20, QualifiedName: "src/utils/math.ts", Name: "src/utils/math.ts", Kind: model.NodeKindFile, FilePath: "src/utils/math.ts", Language: "typescript"},
		{ID: 30, QualifiedName: "@app/utils", Name: "utils", Kind: model.NodeKindPackage, FilePath: "src/utils", Language: "typescript"},
	}}
	edges, err := Resolve(context.Background(), lookup, []model.Edge{{
		Kind:        model.EdgeKindImportsFrom,
		FilePath:    "src/app/main.ts",
		Line:        1,
		Fingerprint: "imports_from:src/app/main.ts:@app/utils:1",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if got := edges[0].ToNodeID; got != 30 {
		t.Fatalf("ToNodeID=%d, want alias package node 30", got)
	}
}

func TestResolveImportsFromLeavesAmbiguousSuffixUnresolved(t *testing.T) {
	lookup := fakeLookup{nodes: []model.Node{
		{ID: 10, QualifiedName: "cmd/main.go", Name: "cmd/main.go", Kind: model.NodeKindFile, FilePath: "cmd/main.go", Language: "go"},
		{ID: 20, QualifiedName: "internal/mcp/deps.go", Name: "internal/mcp/deps.go", Kind: model.NodeKindFile, FilePath: "internal/mcp/deps.go", Language: "go"},
		{ID: 21, QualifiedName: "pkg/mcp/deps.go", Name: "pkg/mcp/deps.go", Kind: model.NodeKindFile, FilePath: "pkg/mcp/deps.go", Language: "go"},
	}}
	edges, err := Resolve(context.Background(), lookup, []model.Edge{{
		Kind:        model.EdgeKindImportsFrom,
		FilePath:    "cmd/main.go",
		Line:        2,
		Fingerprint: "imports_from:cmd/main.go:github.com/example/project/mcp:2",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if got := edges[0].ToNodeID; got != 0 {
		t.Fatalf("ToNodeID=%d, want unresolved 0 for ambiguous suffix", got)
	}
}

func TestResolveInheritsBindsTypeEndpoints(t *testing.T) {
	lookup := fakeLookup{nodes: []model.Node{
		{ID: 1, QualifiedName: "pkg.Child", Name: "Child", Kind: model.NodeKindClass, FilePath: "child.go", StartLine: 3, EndLine: 5, Language: "go"},
		{ID: 2, QualifiedName: "pkg.Parent", Name: "Parent", Kind: model.NodeKindClass, FilePath: "parent.go", StartLine: 3, EndLine: 5, Language: "go"},
	}}
	edges, err := Resolve(context.Background(), lookup, []model.Edge{{
		Kind:        model.EdgeKindInherits,
		FilePath:    "child.go",
		Line:        4,
		Fingerprint: model.BuildInheritsFingerprintV2("child.go", "Child", "Parent"),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if got := edges[0].FromNodeID; got != 1 {
		t.Fatalf("FromNodeID=%d, want 1", got)
	}
	if got := edges[0].ToNodeID; got != 2 {
		t.Fatalf("ToNodeID=%d, want 2", got)
	}
}

func TestResolveInheritsBindsQualifiedTypeEndpoints(t *testing.T) {
	lookup := fakeLookup{nodes: []model.Node{
		{ID: 1, QualifiedName: "com.example.auth.User", Name: "User", Kind: model.NodeKindClass, FilePath: "User.kt", StartLine: 3, EndLine: 5, Language: "kotlin"},
		{ID: 2, QualifiedName: "com.example.auth.Base", Name: "Base", Kind: model.NodeKindClass, FilePath: "Base.kt", StartLine: 3, EndLine: 5, Language: "kotlin"},
	}}
	edges, err := Resolve(context.Background(), lookup, []model.Edge{{
		Kind:        model.EdgeKindInherits,
		FilePath:    "User.kt",
		Line:        3,
		Fingerprint: model.BuildInheritsFingerprintV2("User.kt", "com.example.auth.User", "Base"),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if got := edges[0].FromNodeID; got != 1 {
		t.Fatalf("FromNodeID=%d, want 1", got)
	}
	if got := edges[0].ToNodeID; got != 2 {
		t.Fatalf("ToNodeID=%d, want 2", got)
	}
}

func TestResolveTypeScriptImportedHeritageBindsQualifiedEndpoints(t *testing.T) {
	lookup := fakeLookup{nodes: []model.Node{
		{ID: 1, QualifiedName: "@acme/app/src/models.User", Name: "User", Kind: model.NodeKindClass, FilePath: "src/models/user.ts", StartLine: 3, EndLine: 5, Language: "typescript"},
		{ID: 2, QualifiedName: "@acme/app/src/base.Base", Name: "Base", Kind: model.NodeKindClass, FilePath: "src/base/index.ts", StartLine: 1, EndLine: 1, Language: "typescript"},
		{ID: 3, QualifiedName: "@acme/app/src/contracts.Authenticated", Name: "Authenticated", Kind: model.NodeKindType, FilePath: "src/contracts/index.ts", StartLine: 1, EndLine: 1, Language: "typescript"},
	}}
	edges, err := Resolve(context.Background(), lookup, []model.Edge{
		{Kind: model.EdgeKindInherits, FilePath: "src/models/user.ts", Line: 3, Fingerprint: model.BuildInheritsFingerprintV2("src/models/user.ts", "@acme/app/src/models.User", "@acme/app/src/base.Base")},
		{Kind: model.EdgeKindImplements, FilePath: "src/models/user.ts", Line: 3, Fingerprint: "implements:src/models/user.ts:@acme/app/src/models.User:@acme/app/src/contracts.Authenticated"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := edges[0].FromNodeID; got != 1 {
		t.Fatalf("inherits FromNodeID=%d, want 1", got)
	}
	if got := edges[0].ToNodeID; got != 2 {
		t.Fatalf("inherits ToNodeID=%d, want 2", got)
	}
	if got := edges[1].FromNodeID; got != 1 {
		t.Fatalf("implements FromNodeID=%d, want 1", got)
	}
	if got := edges[1].ToNodeID; got != 3 {
		t.Fatalf("implements ToNodeID=%d, want 3", got)
	}
}

func TestResolveInheritsLegacyFingerprint(t *testing.T) {
	lookup := fakeLookup{nodes: []model.Node{
		{ID: 1, QualifiedName: "pkg.Child", Name: "Child", Kind: model.NodeKindClass, FilePath: "child.go", StartLine: 3, EndLine: 5, Language: "go"},
		{ID: 2, QualifiedName: "pkg.Parent", Name: "Parent", Kind: model.NodeKindClass, FilePath: "parent.go", StartLine: 3, EndLine: 5, Language: "go"},
	}}
	edges, err := Resolve(context.Background(), lookup, []model.Edge{{
		Kind:        model.EdgeKindInherits,
		FilePath:    "child.go",
		Line:        4,
		Fingerprint: "inherits:child.go:Child:Parent",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if got := edges[0].FromNodeID; got != 1 {
		t.Fatalf("FromNodeID=%d, want 1", got)
	}
	if got := edges[0].ToNodeID; got != 2 {
		t.Fatalf("ToNodeID=%d, want 2", got)
	}
}

func TestResolveInheritsV2SupportsSeparatorHeavyNames(t *testing.T) {
	lookup := fakeLookup{nodes: []model.Node{
		{ID: 1, QualifiedName: `pkg::child:Child`, Name: `pkg::child:Child`, Kind: model.NodeKindClass, FilePath: `pkg:file".kt`, StartLine: 3, EndLine: 5, Language: "kotlin"},
		{ID: 2, QualifiedName: `pkg::base::Parent`, Name: `pkg::base::Parent`, Kind: model.NodeKindClass, FilePath: "Base.kt", StartLine: 3, EndLine: 5, Language: "kotlin"},
	}}
	edges, err := Resolve(context.Background(), lookup, []model.Edge{{
		Kind:        model.EdgeKindInherits,
		FilePath:    `pkg:file".kt`,
		Line:        3,
		Fingerprint: model.BuildInheritsFingerprintV2(`pkg:file".kt`, `pkg::child:Child`, `pkg::base::Parent`),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if got := edges[0].FromNodeID; got != 1 {
		t.Fatalf("FromNodeID=%d, want 1", got)
	}
	if got := edges[0].ToNodeID; got != 2 {
		t.Fatalf("ToNodeID=%d, want 2", got)
	}
}

func TestResolveInheritsMalformedFingerprintLeavesEndpointsUnresolved(t *testing.T) {
	edges, err := Resolve(context.Background(), fakeLookup{}, []model.Edge{{
		Kind:        model.EdgeKindInherits,
		FilePath:    "bad.go",
		Line:        1,
		Fingerprint: "inherits:v2:not-json",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if edges[0].FromNodeID != 0 || edges[0].ToNodeID != 0 {
		t.Fatalf("inherits malformed edge endpoints=(%d,%d), want unresolved (0,0)", edges[0].FromNodeID, edges[0].ToNodeID)
	}
}

func TestResolveTestedByBindsTestAndProductionEndpoints(t *testing.T) {
	lookup := fakeLookup{nodes: []model.Node{
		{ID: 1, QualifiedName: "pkg.Add", Name: "Add", Kind: model.NodeKindFunction, FilePath: "add.go", StartLine: 3, EndLine: 5, Language: "go"},
		{ID: 2, QualifiedName: "pkg.TestAdd", Name: "TestAdd", Kind: model.NodeKindTest, FilePath: "add_test.go", StartLine: 3, EndLine: 7, Language: "go"},
	}}
	edges, err := Resolve(context.Background(), lookup, []model.Edge{{
		Kind:        model.EdgeKindTestedBy,
		FilePath:    "add_test.go",
		Line:        5,
		Fingerprint: "tested_by:add_test.go:Add:pkg.TestAdd",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if got := edges[0].FromNodeID; got != 2 {
		t.Fatalf("FromNodeID=%d, want test node 2", got)
	}
	if got := edges[0].ToNodeID; got != 1 {
		t.Fatalf("ToNodeID=%d, want production node 1", got)
	}
}

func TestFilterResolvedDropsEdgesWithMissingEndpoints(t *testing.T) {
	edges := FilterResolved([]model.Edge{
		{FromNodeID: 1, ToNodeID: 2, Kind: model.EdgeKindCalls, Fingerprint: "resolved"},
		{FromNodeID: 0, ToNodeID: 2, Kind: model.EdgeKindCalls, Fingerprint: "missing-from"},
		{FromNodeID: 1, ToNodeID: 0, Kind: model.EdgeKindCalls, Fingerprint: "missing-to"},
	})
	if len(edges) != 1 {
		t.Fatalf("expected 1 resolved edge, got %d: %+v", len(edges), edges)
	}
	if edges[0].Fingerprint != "resolved" {
		t.Fatalf("expected resolved edge to remain, got %+v", edges[0])
	}
}

func TestFilterResolvedWithDiagnosticsSummarizesDroppedEdges(t *testing.T) {
	resolved, diagnostics := FilterResolvedWithDiagnostics([]model.Edge{
		{FromNodeID: 1, ToNodeID: 2, Kind: model.EdgeKindCalls, FilePath: "ok.go", Fingerprint: "resolved"},
		{FromNodeID: 0, ToNodeID: 2, Kind: model.EdgeKindCalls, FilePath: "a.go", Fingerprint: "missing-from"},
		{FromNodeID: 1, ToNodeID: 0, Kind: model.EdgeKindImportsFrom, FilePath: "a.go", Fingerprint: "missing-to"},
		{FromNodeID: 0, ToNodeID: 0, Kind: model.EdgeKindCalls, FilePath: "b.go", Fingerprint: "missing-both"},
	})

	if len(resolved) != 1 {
		t.Fatalf("resolved edges=%d, want 1", len(resolved))
	}
	if diagnostics.DroppedCount != 3 {
		t.Fatalf("DroppedCount=%d, want 3", diagnostics.DroppedCount)
	}
	if got := diagnostics.ByKind[model.EdgeKindCalls]; got != 2 {
		t.Fatalf("ByKind[calls]=%d, want 2", got)
	}
	if got := diagnostics.ByKind[model.EdgeKindImportsFrom]; got != 1 {
		t.Fatalf("ByKind[imports_from]=%d, want 1", got)
	}
	if got := diagnostics.ByFile["a.go"]; got != 2 {
		t.Fatalf("ByFile[a.go]=%d, want 2", got)
	}
	if got := diagnostics.ByFile["b.go"]; got != 1 {
		t.Fatalf("ByFile[b.go]=%d, want 1", got)
	}
	if got := diagnostics.ByReason["missing_from"]; got != 1 {
		t.Fatalf("ByReason[missing_from]=%d, want 1", got)
	}
	if got := diagnostics.ByReason["missing_to"]; got != 1 {
		t.Fatalf("ByReason[missing_to]=%d, want 1", got)
	}
	if got := diagnostics.ByReason["missing_both"]; got != 1 {
		t.Fatalf("ByReason[missing_both]=%d, want 1", got)
	}
	if len(diagnostics.Samples) != 3 {
		t.Fatalf("Samples=%d, want 3", len(diagnostics.Samples))
	}
	if diagnostics.Samples[0].Fingerprint != "missing-from" || diagnostics.Samples[0].Reason != "missing_from" {
		t.Fatalf("first sample=%+v, want missing-from/missing_from", diagnostics.Samples[0])
	}
}

func TestResolveTestedByBindsQualifiedProductionEndpoint(t *testing.T) {
	lookup := fakeLookup{nodes: []model.Node{
		{ID: 1, QualifiedName: "calc.Add", Name: "Add", Kind: model.NodeKindFunction, FilePath: "add.go", StartLine: 3, EndLine: 5, Language: "go"},
		{ID: 2, QualifiedName: "calc_test.TestAdd", Name: "TestAdd", Kind: model.NodeKindTest, FilePath: "add_test.go", StartLine: 3, EndLine: 7, Language: "go"},
	}}
	edges, err := Resolve(context.Background(), lookup, []model.Edge{{
		Kind:        model.EdgeKindTestedBy,
		FilePath:    "add_test.go",
		Line:        5,
		Fingerprint: "tested_by:add_test.go:calc.Add:calc_test.TestAdd",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if got := edges[0].FromNodeID; got != 2 {
		t.Fatalf("FromNodeID=%d, want test node 2", got)
	}
	if got := edges[0].ToNodeID; got != 1 {
		t.Fatalf("ToNodeID=%d, want production node 1", got)
	}
}

func TestResolveTestedByLeavesAmbiguousProductionUnresolved(t *testing.T) {
	lookup := fakeLookup{nodes: []model.Node{
		{ID: 1, QualifiedName: "pkg.Add", Name: "Add", Kind: model.NodeKindFunction, FilePath: "add_test.go", StartLine: 3, EndLine: 5, Language: "go"},
		{ID: 3, QualifiedName: "pkg.Add", Name: "Add", Kind: model.NodeKindFunction, FilePath: "add_test.go", StartLine: 8, EndLine: 10, Language: "go"},
		{ID: 2, QualifiedName: "pkg.TestAdd", Name: "TestAdd", Kind: model.NodeKindTest, FilePath: "add_test.go", StartLine: 3, EndLine: 7, Language: "go"},
	}}
	edges, err := Resolve(context.Background(), lookup, []model.Edge{{
		Kind:        model.EdgeKindTestedBy,
		FilePath:    "add_test.go",
		Line:        5,
		Fingerprint: "tested_by:add_test.go:Add:pkg.TestAdd",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if got := edges[0].FromNodeID; got != 2 {
		t.Fatalf("FromNodeID=%d, want test node 2", got)
	}
	if got := edges[0].ToNodeID; got != 0 {
		t.Fatalf("ToNodeID=%d, want unresolved 0 for ambiguous production", got)
	}
}

func TestResolveLateLoadedNodesRefreshAllIndexes(t *testing.T) {
	lookup := fakeLookup{nodes: []model.Node{
		{ID: 1, QualifiedName: "pkg.Run", Name: "Run", Kind: model.NodeKindFunction, FilePath: "main.go", StartLine: 1, EndLine: 50, Language: "go"},
		{ID: 2, QualifiedName: "pkg.RunBounded", Name: "RunBounded", Kind: model.NodeKindFunction, FilePath: "main.go", StartLine: 20, EndLine: 30, Language: "go"},
	}, fileNodesByFP: map[string][]model.Node{
		"main.go": {{ID: 1, QualifiedName: "pkg.Run", Name: "Run", Kind: model.NodeKindFunction, FilePath: "main.go", StartLine: 1, EndLine: 50, Language: "go"}},
	}}
	edges, err := Resolve(context.Background(), lookup, []model.Edge{{
		Kind:        model.EdgeKindCalls,
		FilePath:    "main.go",
		Line:        21,
		Fingerprint: "calls:main.go:RunBounded:21",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if got := edges[0].FromNodeID; got != 2 {
		t.Fatalf("FromNodeID=%d, want late-loaded caller node 2", got)
	}
	if got := edges[0].ToNodeID; got != 2 {
		t.Fatalf("ToNodeID=%d, want late-loaded callee node 2", got)
	}
}

func TestResolveSamePackageUnexportedInterfaceDispatch(t *testing.T) {
	lookup := fakeLookup{nodes: []model.Node{
		{ID: 1, QualifiedName: "pkg.handler.Start", Name: "Start", Kind: model.NodeKindFunction, FilePath: "main.go", StartLine: 10, EndLine: 12, Language: "go"},
		{ID: 2, QualifiedName: "pkg.tracer", Name: "tracer", Kind: model.NodeKindType, FilePath: "deps.go", StartLine: 3, EndLine: 5, Language: "go"},
		{ID: 3, QualifiedName: "pkg.Tracer", Name: "Tracer", Kind: model.NodeKindClass, FilePath: "flows.go", StartLine: 3, EndLine: 3, Language: "go"},
		{ID: 4, QualifiedName: "pkg.Tracer.trace", Name: "trace", Kind: model.NodeKindFunction, FilePath: "flows.go", StartLine: 5, EndLine: 7, Language: "go"},
	}}
	edges, err := Resolve(context.Background(), lookup, []model.Edge{
		{Kind: model.EdgeKindImplements, FilePath: "main.go", Line: 4, Fingerprint: "implements:main.go:pkg.Tracer:pkg.tracer"},
		{Kind: model.EdgeKindCalls, FilePath: "main.go", Line: 11, Fingerprint: "calls:main.go:t.tracer.trace:11"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if edges[1].FromNodeID != 1 || edges[1].ToNodeID != 4 {
		t.Fatalf("same-package unexported dispatch endpoints=(%d,%d), want (1,4)", edges[1].FromNodeID, edges[1].ToNodeID)
	}
}

func TestResolveCrossPackageUnexportedInterfaceDispatchBlocked(t *testing.T) {
	lookup := fakeLookup{nodes: []model.Node{
		{ID: 1, QualifiedName: "main.handler.Start", Name: "Start", Kind: model.NodeKindFunction, FilePath: "main.go", StartLine: 10, EndLine: 12, Language: "go"},
		{ID: 2, QualifiedName: "deps.tracer", Name: "tracer", Kind: model.NodeKindType, FilePath: "deps.go", StartLine: 3, EndLine: 5, Language: "go"},
		{ID: 3, QualifiedName: "flows.Tracer", Name: "Tracer", Kind: model.NodeKindClass, FilePath: "flows.go", StartLine: 3, EndLine: 3, Language: "go"},
		{ID: 4, QualifiedName: "flows.Tracer.trace", Name: "trace", Kind: model.NodeKindFunction, FilePath: "flows.go", StartLine: 5, EndLine: 7, Language: "go"},
	}}
	edges, err := Resolve(context.Background(), lookup, []model.Edge{
		{Kind: model.EdgeKindImplements, FilePath: "main.go", Line: 4, Fingerprint: "implements:main.go:flows.Tracer:deps.tracer"},
		{Kind: model.EdgeKindCalls, FilePath: "main.go", Line: 11, Fingerprint: "calls:main.go:t.tracer.trace:11"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if edges[1].ToNodeID != 0 {
		t.Fatalf("cross-package unexported dispatch ToNodeID=%d, want unresolved 0", edges[1].ToNodeID)
	}
}

func TestResolveInterfaceSelectorIsGoOnly(t *testing.T) {
	lookup := fakeLookup{nodes: []model.Node{
		{ID: 1, QualifiedName: "main.handler.Start", Name: "Start", Kind: model.NodeKindFunction, FilePath: "main.py", StartLine: 10, EndLine: 12, Language: "python"},
		{ID: 2, QualifiedName: "mcp.FlowTracer", Name: "FlowTracer", Kind: model.NodeKindType, FilePath: "deps.go", StartLine: 3, EndLine: 5, Language: "go"},
		{ID: 3, QualifiedName: "flows.Tracer", Name: "Tracer", Kind: model.NodeKindClass, FilePath: "flows.go", StartLine: 3, EndLine: 3, Language: "go"},
		{ID: 4, QualifiedName: "flows.Tracer.TraceFlow", Name: "TraceFlow", Kind: model.NodeKindFunction, FilePath: "flows.go", StartLine: 5, EndLine: 7, Language: "go"},
	}}
	edges, err := Resolve(context.Background(), lookup, []model.Edge{
		{Kind: model.EdgeKindImplements, FilePath: "main.py", Line: 4, Fingerprint: "implements:main.py:flows.Tracer:mcp.FlowTracer"},
		{Kind: model.EdgeKindCalls, FilePath: "main.py", Line: 11, Fingerprint: "calls:main.py:h.deps.FlowTracer.TraceFlow:11"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if edges[1].FromNodeID != 1 {
		t.Fatalf("FromNodeID=%d, want 1", edges[1].FromNodeID)
	}
	if edges[1].ToNodeID != 0 {
		t.Fatalf("non-Go interface selector ToNodeID=%d, want unresolved 0", edges[1].ToNodeID)
	}
}

func TestResolveTypeScriptInterfaceDispatchContract_RemainsUnresolvedUntilReceiverTypeKnown(t *testing.T) {
	lookup := fakeLookup{nodes: []model.Node{
		{ID: 1, QualifiedName: "app.start", Name: "start", Kind: model.NodeKindFunction, FilePath: "app.ts", StartLine: 10, EndLine: 12, Language: "typescript"},
		{ID: 2, QualifiedName: "contracts.FlowTracer", Name: "FlowTracer", Kind: model.NodeKindType, FilePath: "contracts.ts", StartLine: 1, EndLine: 3, Language: "typescript"},
		{ID: 3, QualifiedName: "impl.Tracer", Name: "Tracer", Kind: model.NodeKindClass, FilePath: "impl.ts", StartLine: 1, EndLine: 6, Language: "typescript"},
		{ID: 4, QualifiedName: "impl.Tracer.traceFlow", Name: "traceFlow", Kind: model.NodeKindFunction, FilePath: "impl.ts", StartLine: 3, EndLine: 5, Language: "typescript"},
	}}
	edges, err := Resolve(context.Background(), lookup, []model.Edge{
		{Kind: model.EdgeKindImplements, FilePath: "app.ts", Line: 2, Fingerprint: "implements:app.ts:impl.Tracer:contracts.FlowTracer"},
		{Kind: model.EdgeKindCalls, FilePath: "app.ts", Line: 11, Fingerprint: "calls:app.ts:deps.flowTracer.traceFlow:11"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if edges[1].FromNodeID != 1 {
		t.Fatalf("FromNodeID=%d, want 1", edges[1].FromNodeID)
	}
	if edges[1].ToNodeID != 0 {
		t.Fatalf("TypeScript interface-like call ToNodeID=%d, want unresolved 0", edges[1].ToNodeID)
	}
}

func TestResolveJavaInterfaceDispatchContract_RemainsUnresolvedUntilReceiverTypeKnown(t *testing.T) {
	lookup := fakeLookup{nodes: []model.Node{
		{ID: 1, QualifiedName: "com.example.App.run", Name: "run", Kind: model.NodeKindFunction, FilePath: "App.java", StartLine: 10, EndLine: 12, Language: "java"},
		{ID: 2, QualifiedName: "com.example.FlowTracer", Name: "FlowTracer", Kind: model.NodeKindType, FilePath: "FlowTracer.java", StartLine: 1, EndLine: 3, Language: "java"},
		{ID: 3, QualifiedName: "com.example.Tracer", Name: "Tracer", Kind: model.NodeKindClass, FilePath: "Tracer.java", StartLine: 1, EndLine: 6, Language: "java"},
		{ID: 4, QualifiedName: "com.example.Tracer.traceFlow", Name: "traceFlow", Kind: model.NodeKindFunction, FilePath: "Tracer.java", StartLine: 3, EndLine: 5, Language: "java"},
	}}
	edges, err := Resolve(context.Background(), lookup, []model.Edge{
		{Kind: model.EdgeKindImplements, FilePath: "App.java", Line: 2, Fingerprint: "implements:App.java:com.example.Tracer:com.example.FlowTracer"},
		{Kind: model.EdgeKindCalls, FilePath: "App.java", Line: 11, Fingerprint: "calls:App.java:deps.flowTracer.traceFlow:11"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if edges[1].FromNodeID != 1 {
		t.Fatalf("FromNodeID=%d, want 1", edges[1].FromNodeID)
	}
	if edges[1].ToNodeID != 0 {
		t.Fatalf("Java interface-like call ToNodeID=%d, want unresolved 0", edges[1].ToNodeID)
	}
}

func TestResolveKotlinInterfaceDispatchContract_RemainsUnresolvedUntilReceiverTypeKnown(t *testing.T) {
	lookup := fakeLookup{nodes: []model.Node{
		{ID: 1, QualifiedName: "com.example.App.run", Name: "run", Kind: model.NodeKindFunction, FilePath: "App.kt", StartLine: 10, EndLine: 12, Language: "kotlin"},
		{ID: 2, QualifiedName: "com.example.FlowTracer", Name: "FlowTracer", Kind: model.NodeKindType, FilePath: "FlowTracer.kt", StartLine: 1, EndLine: 3, Language: "kotlin"},
		{ID: 3, QualifiedName: "com.example.Tracer", Name: "Tracer", Kind: model.NodeKindClass, FilePath: "Tracer.kt", StartLine: 1, EndLine: 6, Language: "kotlin"},
		{ID: 4, QualifiedName: "com.example.Tracer.traceFlow", Name: "traceFlow", Kind: model.NodeKindFunction, FilePath: "Tracer.kt", StartLine: 3, EndLine: 5, Language: "kotlin"},
	}}
	edges, err := Resolve(context.Background(), lookup, []model.Edge{
		{Kind: model.EdgeKindImplements, FilePath: "App.kt", Line: 2, Fingerprint: "implements:App.kt:com.example.Tracer:com.example.FlowTracer"},
		{Kind: model.EdgeKindCalls, FilePath: "App.kt", Line: 11, Fingerprint: "calls:App.kt:deps.flowTracer.traceFlow:11"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if edges[1].FromNodeID != 1 {
		t.Fatalf("FromNodeID=%d, want 1", edges[1].FromNodeID)
	}
	if edges[1].ToNodeID != 0 {
		t.Fatalf("Kotlin interface-like call ToNodeID=%d, want unresolved 0", edges[1].ToNodeID)
	}
}

func TestResolveTypeScriptTypedReceiverInterfaceDispatch_ResolvesUniqueImplementerMethod(t *testing.T) {
	lookup := fakeLookup{nodes: []model.Node{
		{ID: 1, QualifiedName: "app.start", Name: "start", Kind: model.NodeKindFunction, FilePath: "app.ts", StartLine: 10, EndLine: 12, Language: "typescript"},
		{ID: 2, QualifiedName: "FlowTracer", Name: "FlowTracer", Kind: model.NodeKindType, FilePath: "contracts.ts", StartLine: 1, EndLine: 3, Language: "typescript"},
		{ID: 3, QualifiedName: "impl.Tracer", Name: "Tracer", Kind: model.NodeKindClass, FilePath: "impl.ts", StartLine: 1, EndLine: 6, Language: "typescript"},
		{ID: 4, QualifiedName: "impl.Tracer.traceFlow", Name: "traceFlow", Kind: model.NodeKindFunction, FilePath: "impl.ts", StartLine: 3, EndLine: 5, Language: "typescript"},
	}}
	edges, err := Resolve(context.Background(), lookup, []model.Edge{
		{Kind: model.EdgeKindImplements, FilePath: "app.ts", Line: 2, Fingerprint: "implements:app.ts:impl.Tracer:FlowTracer"},
		{Kind: model.EdgeKindCalls, FilePath: "app.ts", Line: 11, Fingerprint: "calls:app.ts:FlowTracer.traceFlow:11"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if edges[1].ToNodeID != 4 {
		t.Fatalf("TypeScript typed receiver dispatch ToNodeID=%d, want 4", edges[1].ToNodeID)
	}
}

func TestResolveJavaTypedReceiverInterfaceDispatch_ResolvesUniqueImplementerMethod(t *testing.T) {
	lookup := fakeLookup{nodes: []model.Node{
		{ID: 1, QualifiedName: "com.example.App.run", Name: "run", Kind: model.NodeKindFunction, FilePath: "App.java", StartLine: 10, EndLine: 12, Language: "java"},
		{ID: 2, QualifiedName: "FlowTracer", Name: "FlowTracer", Kind: model.NodeKindType, FilePath: "FlowTracer.java", StartLine: 1, EndLine: 3, Language: "java"},
		{ID: 3, QualifiedName: "com.example.Tracer", Name: "Tracer", Kind: model.NodeKindClass, FilePath: "Tracer.java", StartLine: 1, EndLine: 6, Language: "java"},
		{ID: 4, QualifiedName: "com.example.Tracer.traceFlow", Name: "traceFlow", Kind: model.NodeKindFunction, FilePath: "Tracer.java", StartLine: 3, EndLine: 5, Language: "java"},
	}}
	edges, err := Resolve(context.Background(), lookup, []model.Edge{
		{Kind: model.EdgeKindImplements, FilePath: "App.java", Line: 2, Fingerprint: "implements:App.java:com.example.Tracer:FlowTracer"},
		{Kind: model.EdgeKindCalls, FilePath: "App.java", Line: 11, Fingerprint: "calls:App.java:FlowTracer.traceFlow:11"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if edges[1].ToNodeID != 4 {
		t.Fatalf("Java typed receiver dispatch ToNodeID=%d, want 4", edges[1].ToNodeID)
	}
}

func TestResolveJavaTypedReceiverInterfaceDispatch_UsesQualifiedImportedOwner(t *testing.T) {
	lookup := fakeLookup{nodes: []model.Node{
		{ID: 1, QualifiedName: "com.example.app.App.run", Name: "run", Kind: model.NodeKindFunction, FilePath: "App.java", StartLine: 10, EndLine: 12, Language: "java"},
		{ID: 2, QualifiedName: "com.contracts.FlowTracer", Name: "FlowTracer", Kind: model.NodeKindType, FilePath: "FlowTracer.java", StartLine: 1, EndLine: 3, Language: "java"},
		{ID: 3, QualifiedName: "com.example.impl.Tracer", Name: "Tracer", Kind: model.NodeKindClass, FilePath: "Tracer.java", StartLine: 1, EndLine: 6, Language: "java"},
		{ID: 4, QualifiedName: "com.example.impl.Tracer.traceFlow", Name: "traceFlow", Kind: model.NodeKindFunction, FilePath: "Tracer.java", StartLine: 3, EndLine: 5, Language: "java"},
	}}
	edges, err := Resolve(context.Background(), lookup, []model.Edge{
		{Kind: model.EdgeKindImplements, FilePath: "App.java", Line: 2, Fingerprint: "implements:App.java:com.example.impl.Tracer:com.contracts.FlowTracer"},
		{Kind: model.EdgeKindCalls, FilePath: "App.java", Line: 11, Fingerprint: "calls:App.java:com.contracts.FlowTracer.traceFlow:11"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if edges[1].ToNodeID != 4 {
		t.Fatalf("Java qualified typed receiver dispatch ToNodeID=%d, want 4", edges[1].ToNodeID)
	}
}

func TestResolveKotlinTypedReceiverInterfaceDispatch_ResolvesUniqueImplementerMethod(t *testing.T) {
	lookup := fakeLookup{nodes: []model.Node{
		{ID: 1, QualifiedName: "com.example.App.run", Name: "run", Kind: model.NodeKindFunction, FilePath: "App.kt", StartLine: 10, EndLine: 12, Language: "kotlin"},
		{ID: 2, QualifiedName: "FlowTracer", Name: "FlowTracer", Kind: model.NodeKindType, FilePath: "FlowTracer.kt", StartLine: 1, EndLine: 3, Language: "kotlin"},
		{ID: 3, QualifiedName: "com.example.Tracer", Name: "Tracer", Kind: model.NodeKindClass, FilePath: "Tracer.kt", StartLine: 1, EndLine: 6, Language: "kotlin"},
		{ID: 4, QualifiedName: "com.example.Tracer.traceFlow", Name: "traceFlow", Kind: model.NodeKindFunction, FilePath: "Tracer.kt", StartLine: 3, EndLine: 5, Language: "kotlin"},
	}}
	edges, err := Resolve(context.Background(), lookup, []model.Edge{
		{Kind: model.EdgeKindImplements, FilePath: "App.kt", Line: 2, Fingerprint: "implements:App.kt:com.example.Tracer:FlowTracer"},
		{Kind: model.EdgeKindCalls, FilePath: "App.kt", Line: 11, Fingerprint: "calls:App.kt:FlowTracer.traceFlow:11"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if edges[1].ToNodeID != 4 {
		t.Fatalf("Kotlin typed receiver dispatch ToNodeID=%d, want 4", edges[1].ToNodeID)
	}
}

func TestResolveKotlinTypedReceiverInterfaceDispatch_UsesQualifiedImportedOwner(t *testing.T) {
	lookup := fakeLookup{nodes: []model.Node{
		{ID: 1, QualifiedName: "com.example.app.App.run", Name: "run", Kind: model.NodeKindFunction, FilePath: "App.kt", StartLine: 10, EndLine: 12, Language: "kotlin"},
		{ID: 2, QualifiedName: "com.contracts.FlowTracer", Name: "FlowTracer", Kind: model.NodeKindType, FilePath: "FlowTracer.kt", StartLine: 1, EndLine: 3, Language: "kotlin"},
		{ID: 3, QualifiedName: "com.example.impl.Tracer", Name: "Tracer", Kind: model.NodeKindClass, FilePath: "Tracer.kt", StartLine: 1, EndLine: 6, Language: "kotlin"},
		{ID: 4, QualifiedName: "com.example.impl.Tracer.traceFlow", Name: "traceFlow", Kind: model.NodeKindFunction, FilePath: "Tracer.kt", StartLine: 3, EndLine: 5, Language: "kotlin"},
	}}
	edges, err := Resolve(context.Background(), lookup, []model.Edge{
		{Kind: model.EdgeKindImplements, FilePath: "App.kt", Line: 2, Fingerprint: "implements:App.kt:com.example.impl.Tracer:com.contracts.FlowTracer"},
		{Kind: model.EdgeKindCalls, FilePath: "App.kt", Line: 11, Fingerprint: "calls:App.kt:com.contracts.FlowTracer.traceFlow:11"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if edges[1].ToNodeID != 4 {
		t.Fatalf("Kotlin qualified typed receiver dispatch ToNodeID=%d, want 4", edges[1].ToNodeID)
	}
}

func TestResolveTypeScriptExplicitOwnerInterfaceDispatch_ResolvesUniqueImplementerMethod(t *testing.T) {
	lookup := fakeLookup{nodes: []model.Node{
		{ID: 1, QualifiedName: "app.start", Name: "start", Kind: model.NodeKindFunction, FilePath: "app.ts", StartLine: 10, EndLine: 12, Language: "typescript"},
		{ID: 2, QualifiedName: "contracts.FlowTracer", Name: "FlowTracer", Kind: model.NodeKindType, FilePath: "contracts.ts", StartLine: 1, EndLine: 3, Language: "typescript"},
		{ID: 3, QualifiedName: "impl.Tracer", Name: "Tracer", Kind: model.NodeKindClass, FilePath: "impl.ts", StartLine: 1, EndLine: 6, Language: "typescript"},
		{ID: 4, QualifiedName: "impl.Tracer.traceFlow", Name: "traceFlow", Kind: model.NodeKindFunction, FilePath: "impl.ts", StartLine: 3, EndLine: 5, Language: "typescript"},
	}}
	edges, err := Resolve(context.Background(), lookup, []model.Edge{
		{Kind: model.EdgeKindImplements, FilePath: "app.ts", Line: 2, Fingerprint: "implements:app.ts:impl.Tracer:contracts.FlowTracer"},
		{Kind: model.EdgeKindCalls, FilePath: "app.ts", Line: 11, Fingerprint: "calls:app.ts:contracts.FlowTracer.traceFlow:11"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if edges[1].ToNodeID != 4 {
		t.Fatalf("TypeScript explicit owner dispatch ToNodeID=%d, want 4", edges[1].ToNodeID)
	}
}

func TestResolveJavaExplicitOwnerInterfaceDispatch_ResolvesUniqueImplementerMethod(t *testing.T) {
	lookup := fakeLookup{nodes: []model.Node{
		{ID: 1, QualifiedName: "com.example.App.run", Name: "run", Kind: model.NodeKindFunction, FilePath: "App.java", StartLine: 10, EndLine: 12, Language: "java"},
		{ID: 2, QualifiedName: "com.example.FlowTracer", Name: "FlowTracer", Kind: model.NodeKindType, FilePath: "FlowTracer.java", StartLine: 1, EndLine: 3, Language: "java"},
		{ID: 3, QualifiedName: "com.example.Tracer", Name: "Tracer", Kind: model.NodeKindClass, FilePath: "Tracer.java", StartLine: 1, EndLine: 6, Language: "java"},
		{ID: 4, QualifiedName: "com.example.Tracer.traceFlow", Name: "traceFlow", Kind: model.NodeKindFunction, FilePath: "Tracer.java", StartLine: 3, EndLine: 5, Language: "java"},
	}}
	edges, err := Resolve(context.Background(), lookup, []model.Edge{
		{Kind: model.EdgeKindImplements, FilePath: "App.java", Line: 2, Fingerprint: "implements:App.java:com.example.Tracer:com.example.FlowTracer"},
		{Kind: model.EdgeKindCalls, FilePath: "App.java", Line: 11, Fingerprint: "calls:App.java:com.example.FlowTracer.traceFlow:11"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if edges[1].ToNodeID != 4 {
		t.Fatalf("Java explicit owner dispatch ToNodeID=%d, want 4", edges[1].ToNodeID)
	}
}

func TestResolveKotlinExplicitOwnerInterfaceDispatch_ResolvesUniqueImplementerMethod(t *testing.T) {
	lookup := fakeLookup{nodes: []model.Node{
		{ID: 1, QualifiedName: "com.example.App.run", Name: "run", Kind: model.NodeKindFunction, FilePath: "App.kt", StartLine: 10, EndLine: 12, Language: "kotlin"},
		{ID: 2, QualifiedName: "com.example.FlowTracer", Name: "FlowTracer", Kind: model.NodeKindType, FilePath: "FlowTracer.kt", StartLine: 1, EndLine: 3, Language: "kotlin"},
		{ID: 3, QualifiedName: "com.example.Tracer", Name: "Tracer", Kind: model.NodeKindClass, FilePath: "Tracer.kt", StartLine: 1, EndLine: 6, Language: "kotlin"},
		{ID: 4, QualifiedName: "com.example.Tracer.traceFlow", Name: "traceFlow", Kind: model.NodeKindFunction, FilePath: "Tracer.kt", StartLine: 3, EndLine: 5, Language: "kotlin"},
	}}
	edges, err := Resolve(context.Background(), lookup, []model.Edge{
		{Kind: model.EdgeKindImplements, FilePath: "App.kt", Line: 2, Fingerprint: "implements:App.kt:com.example.Tracer:com.example.FlowTracer"},
		{Kind: model.EdgeKindCalls, FilePath: "App.kt", Line: 11, Fingerprint: "calls:App.kt:com.example.FlowTracer.traceFlow:11"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if edges[1].ToNodeID != 4 {
		t.Fatalf("Kotlin explicit owner dispatch ToNodeID=%d, want 4", edges[1].ToNodeID)
	}
}

func TestResolveTypeScriptExplicitOwnerInterfaceDispatch_RemainsUnresolvedWhenMultipleImplementers(t *testing.T) {
	lookup := fakeLookup{nodes: []model.Node{
		{ID: 1, QualifiedName: "app.start", Name: "start", Kind: model.NodeKindFunction, FilePath: "app.ts", StartLine: 10, EndLine: 12, Language: "typescript"},
		{ID: 2, QualifiedName: "contracts.FlowTracer", Name: "FlowTracer", Kind: model.NodeKindType, FilePath: "contracts.ts", StartLine: 1, EndLine: 3, Language: "typescript"},
		{ID: 3, QualifiedName: "impl.Tracer", Name: "Tracer", Kind: model.NodeKindClass, FilePath: "impl.ts", StartLine: 1, EndLine: 6, Language: "typescript"},
		{ID: 4, QualifiedName: "impl.Tracer.traceFlow", Name: "traceFlow", Kind: model.NodeKindFunction, FilePath: "impl.ts", StartLine: 3, EndLine: 5, Language: "typescript"},
		{ID: 5, QualifiedName: "impl.OtherTracer", Name: "OtherTracer", Kind: model.NodeKindClass, FilePath: "other.ts", StartLine: 1, EndLine: 6, Language: "typescript"},
		{ID: 6, QualifiedName: "impl.OtherTracer.traceFlow", Name: "traceFlow", Kind: model.NodeKindFunction, FilePath: "other.ts", StartLine: 3, EndLine: 5, Language: "typescript"},
	}}
	edges, err := Resolve(context.Background(), lookup, []model.Edge{
		{Kind: model.EdgeKindImplements, FilePath: "app.ts", Line: 2, Fingerprint: "implements:app.ts:impl.Tracer:contracts.FlowTracer"},
		{Kind: model.EdgeKindImplements, FilePath: "app.ts", Line: 3, Fingerprint: "implements:app.ts:impl.OtherTracer:contracts.FlowTracer"},
		{Kind: model.EdgeKindCalls, FilePath: "app.ts", Line: 11, Fingerprint: "calls:app.ts:contracts.FlowTracer.traceFlow:11"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if edges[2].ToNodeID != 0 {
		t.Fatalf("TypeScript explicit owner ambiguous dispatch ToNodeID=%d, want unresolved 0", edges[2].ToNodeID)
	}
}

func TestResolveJavaExplicitOwnerInterfaceDispatch_RemainsUnresolvedWhenMultipleMatchingMethods(t *testing.T) {
	lookup := fakeLookup{nodes: []model.Node{
		{ID: 1, QualifiedName: "com.example.App.run", Name: "run", Kind: model.NodeKindFunction, FilePath: "App.java", StartLine: 10, EndLine: 12, Language: "java"},
		{ID: 2, QualifiedName: "com.example.FlowTracer", Name: "FlowTracer", Kind: model.NodeKindType, FilePath: "FlowTracer.java", StartLine: 1, EndLine: 3, Language: "java"},
		{ID: 3, QualifiedName: "com.example.Tracer", Name: "Tracer", Kind: model.NodeKindClass, FilePath: "Tracer.java", StartLine: 1, EndLine: 6, Language: "java"},
		{ID: 4, QualifiedName: "com.example.Tracer.traceFlow", Name: "traceFlow", Kind: model.NodeKindFunction, FilePath: "Tracer.java", StartLine: 3, EndLine: 5, Language: "java"},
		{ID: 5, QualifiedName: "com.example.Tracer.traceFlow", Name: "traceFlow", Kind: model.NodeKindFunction, FilePath: "Tracer.java", StartLine: 7, EndLine: 9, Language: "java"},
	}}
	edges, err := Resolve(context.Background(), lookup, []model.Edge{
		{Kind: model.EdgeKindImplements, FilePath: "App.java", Line: 2, Fingerprint: "implements:App.java:com.example.Tracer:com.example.FlowTracer"},
		{Kind: model.EdgeKindCalls, FilePath: "App.java", Line: 11, Fingerprint: "calls:App.java:com.example.FlowTracer.traceFlow:11"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if edges[1].ToNodeID != 0 {
		t.Fatalf("Java explicit owner ambiguous method ToNodeID=%d, want unresolved 0", edges[1].ToNodeID)
	}
}

func TestResolveKotlinExplicitOwnerInterfaceDispatch_RemainsUnresolvedWhenOwnerIsNotKnownInterface(t *testing.T) {
	lookup := fakeLookup{nodes: []model.Node{
		{ID: 1, QualifiedName: "com.example.App.run", Name: "run", Kind: model.NodeKindFunction, FilePath: "App.kt", StartLine: 10, EndLine: 12, Language: "kotlin"},
		{ID: 3, QualifiedName: "com.example.Tracer", Name: "Tracer", Kind: model.NodeKindClass, FilePath: "Tracer.kt", StartLine: 1, EndLine: 6, Language: "kotlin"},
		{ID: 4, QualifiedName: "com.example.Tracer.traceFlow", Name: "traceFlow", Kind: model.NodeKindFunction, FilePath: "Tracer.kt", StartLine: 3, EndLine: 5, Language: "kotlin"},
	}}
	edges, err := Resolve(context.Background(), lookup, []model.Edge{
		{Kind: model.EdgeKindImplements, FilePath: "App.kt", Line: 2, Fingerprint: "implements:App.kt:com.example.Tracer:com.example.FlowTracer"},
		{Kind: model.EdgeKindCalls, FilePath: "App.kt", Line: 11, Fingerprint: "calls:App.kt:com.example.MissingFlowTracer.traceFlow:11"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if edges[1].ToNodeID != 0 {
		t.Fatalf("Kotlin explicit owner unknown interface ToNodeID=%d, want unresolved 0", edges[1].ToNodeID)
	}
}

func TestResolveTypeScriptExplicitOwnerDispatch_PrefersConcreteImplementerOverExactInterfaceMethod(t *testing.T) {
	lookup := fakeLookup{nodes: []model.Node{
		{ID: 1, QualifiedName: "app.start", Name: "start", Kind: model.NodeKindFunction, FilePath: "app.ts", StartLine: 10, EndLine: 12, Language: "typescript"},
		{ID: 2, QualifiedName: "contracts.FlowTracer", Name: "FlowTracer", Kind: model.NodeKindType, FilePath: "contracts.ts", StartLine: 1, EndLine: 3, Language: "typescript"},
		{ID: 3, QualifiedName: "impl.Tracer", Name: "Tracer", Kind: model.NodeKindClass, FilePath: "impl.ts", StartLine: 1, EndLine: 6, Language: "typescript"},
		{ID: 4, QualifiedName: "impl.Tracer.traceFlow", Name: "traceFlow", Kind: model.NodeKindFunction, FilePath: "impl.ts", StartLine: 3, EndLine: 5, Language: "typescript"},
		{ID: 7, QualifiedName: "contracts.FlowTracer.traceFlow", Name: "traceFlow", Kind: model.NodeKindFunction, FilePath: "contracts.ts", StartLine: 5, EndLine: 7, Language: "typescript"},
	}}
	edges, err := Resolve(context.Background(), lookup, []model.Edge{
		{Kind: model.EdgeKindImplements, FilePath: "app.ts", Line: 2, Fingerprint: "implements:app.ts:impl.Tracer:contracts.FlowTracer"},
		{Kind: model.EdgeKindCalls, FilePath: "app.ts", Line: 11, Fingerprint: "calls:app.ts:contracts.FlowTracer.traceFlow:11"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if edges[1].ToNodeID != 4 {
		t.Fatalf("TypeScript concrete-preferred dispatch ToNodeID=%d, want 4", edges[1].ToNodeID)
	}
}

func TestResolveJavaShortOwnerInterfaceDispatch_ResolvesUniqueImplementerMethod(t *testing.T) {
	lookup := fakeLookup{nodes: []model.Node{
		{ID: 1, QualifiedName: "com.example.App.run", Name: "run", Kind: model.NodeKindFunction, FilePath: "App.java", StartLine: 10, EndLine: 12, Language: "java"},
		{ID: 2, QualifiedName: "com.example.FlowTracer", Name: "FlowTracer", Kind: model.NodeKindType, FilePath: "FlowTracer.java", StartLine: 1, EndLine: 3, Language: "java"},
		{ID: 3, QualifiedName: "com.example.Tracer", Name: "Tracer", Kind: model.NodeKindClass, FilePath: "Tracer.java", StartLine: 1, EndLine: 6, Language: "java"},
		{ID: 4, QualifiedName: "com.example.Tracer.traceFlow", Name: "traceFlow", Kind: model.NodeKindFunction, FilePath: "Tracer.java", StartLine: 3, EndLine: 5, Language: "java"},
	}}
	edges, err := Resolve(context.Background(), lookup, []model.Edge{
		{Kind: model.EdgeKindImplements, FilePath: "App.java", Line: 2, Fingerprint: "implements:App.java:com.example.Tracer:com.example.FlowTracer"},
		{Kind: model.EdgeKindCalls, FilePath: "App.java", Line: 11, Fingerprint: "calls:App.java:FlowTracer.traceFlow:11"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if edges[1].ToNodeID != 4 {
		t.Fatalf("Java short owner dispatch ToNodeID=%d, want 4", edges[1].ToNodeID)
	}
	if edges[1].FromNodeID != 1 {
		t.Fatalf("Java short owner dispatch FromNodeID=%d, want 1", edges[1].FromNodeID)
	}
}

func TestResolveJavaExplicitOwnerDispatch_DoesNotFallbackToShorterOwnerPrefix(t *testing.T) {
	lookup := fakeLookup{nodes: []model.Node{
		{ID: 1, QualifiedName: "com.example.App.run", Name: "run", Kind: model.NodeKindFunction, FilePath: "App.java", StartLine: 10, EndLine: 12, Language: "java"},
		{ID: 2, QualifiedName: "com.example.FlowTracer", Name: "FlowTracer", Kind: model.NodeKindType, FilePath: "FlowTracer.java", StartLine: 1, EndLine: 3, Language: "java"},
		{ID: 3, QualifiedName: "com.example.Tracer", Name: "Tracer", Kind: model.NodeKindClass, FilePath: "Tracer.java", StartLine: 1, EndLine: 6, Language: "java"},
		{ID: 4, QualifiedName: "com.example.Tracer.traceFlow", Name: "traceFlow", Kind: model.NodeKindFunction, FilePath: "Tracer.java", StartLine: 3, EndLine: 5, Language: "java"},
	}}
	edges, err := Resolve(context.Background(), lookup, []model.Edge{
		{Kind: model.EdgeKindImplements, FilePath: "App.java", Line: 2, Fingerprint: "implements:App.java:com.example.Tracer:com.example.FlowTracer"},
		{Kind: model.EdgeKindCalls, FilePath: "App.java", Line: 11, Fingerprint: "calls:App.java:com.example.deep.FlowTracer.traceFlow:11"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if edges[1].ToNodeID != 0 {
		t.Fatalf("Java shorter-prefix fallback ToNodeID=%d, want unresolved 0", edges[1].ToNodeID)
	}
}

func TestResolveKotlinShortOwnerInterfaceDispatch_RemainsUnresolvedWhenOwnerIsAmbiguous(t *testing.T) {
	lookup := fakeLookup{nodes: []model.Node{
		{ID: 1, QualifiedName: "com.example.deep.app.App.run", Name: "run", Kind: model.NodeKindFunction, FilePath: "App.kt", StartLine: 10, EndLine: 12, Language: "kotlin"},
		{ID: 2, QualifiedName: "com.example.FlowTracer", Name: "FlowTracer", Kind: model.NodeKindType, FilePath: "FlowTracer.kt", StartLine: 1, EndLine: 3, Language: "kotlin"},
		{ID: 3, QualifiedName: "com.example.deep.FlowTracer", Name: "FlowTracer", Kind: model.NodeKindType, FilePath: "DeepFlowTracer.kt", StartLine: 1, EndLine: 3, Language: "kotlin"},
		{ID: 4, QualifiedName: "com.example.Tracer", Name: "Tracer", Kind: model.NodeKindClass, FilePath: "Tracer.kt", StartLine: 1, EndLine: 6, Language: "kotlin"},
		{ID: 5, QualifiedName: "com.example.Tracer.traceFlow", Name: "traceFlow", Kind: model.NodeKindFunction, FilePath: "Tracer.kt", StartLine: 3, EndLine: 5, Language: "kotlin"},
	}}
	edges, err := Resolve(context.Background(), lookup, []model.Edge{
		{Kind: model.EdgeKindImplements, FilePath: "App.kt", Line: 2, Fingerprint: "implements:App.kt:com.example.Tracer:com.example.FlowTracer"},
		{Kind: model.EdgeKindCalls, FilePath: "App.kt", Line: 11, Fingerprint: "calls:App.kt:FlowTracer.traceFlow:11"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if edges[1].ToNodeID != 0 {
		t.Fatalf("Kotlin ambiguous short owner ToNodeID=%d, want unresolved 0", edges[1].ToNodeID)
	}
}

func TestResolveKotlinExplicitOwnerDispatch_IgnoresPackageNodeOwners(t *testing.T) {
	lookup := fakeLookup{nodes: []model.Node{
		{ID: 1, QualifiedName: "com.example.App.run", Name: "run", Kind: model.NodeKindFunction, FilePath: "App.kt", StartLine: 10, EndLine: 12, Language: "kotlin"},
		{ID: 2, QualifiedName: "com.example.FlowTracer", Name: "FlowTracer", Kind: model.NodeKindPackage, FilePath: "com/example/FlowTracer", Language: "kotlin"},
		{ID: 3, QualifiedName: "com.example.Tracer", Name: "Tracer", Kind: model.NodeKindClass, FilePath: "Tracer.kt", StartLine: 1, EndLine: 6, Language: "kotlin"},
		{ID: 4, QualifiedName: "com.example.Tracer.traceFlow", Name: "traceFlow", Kind: model.NodeKindFunction, FilePath: "Tracer.kt", StartLine: 3, EndLine: 5, Language: "kotlin"},
	}}
	edges, err := Resolve(context.Background(), lookup, []model.Edge{
		{Kind: model.EdgeKindImplements, FilePath: "App.kt", Line: 2, Fingerprint: "implements:App.kt:com.example.Tracer:com.example.FlowTracer"},
		{Kind: model.EdgeKindCalls, FilePath: "App.kt", Line: 11, Fingerprint: "calls:App.kt:com.example.FlowTracer.traceFlow:11"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if edges[1].ToNodeID != 0 {
		t.Fatalf("Kotlin package owner dispatch ToNodeID=%d, want unresolved 0", edges[1].ToNodeID)
	}
}

func TestPackagePrefixUsesLanguageDispatchWhenRegistered(t *testing.T) {
	original := languageDispatchRegistry
	languageDispatchRegistry = map[string]languageDispatch{
		"python": stubLanguageDispatch{},
	}
	defer func() {
		languageDispatchRegistry = original
	}()

	node := model.Node{QualifiedName: "ignored.value", Name: "value", Language: "python"}
	if got := packagePrefix(node); got != "stub.pkg" {
		t.Fatalf("packagePrefix=%q, want stub.pkg", got)
	}
}

type stubLanguageDispatch struct{}

func (stubLanguageDispatch) Language() string { return "python" }
func (stubLanguageDispatch) CollectQualifiedCallCandidates(model.Node, string) []string { return nil }
func (stubLanguageDispatch) EnsureDispatchTargets(*model.Node, string, *resolveState) []string {
	return nil
}
func (stubLanguageDispatch) ResolveSameReceiverCall(*model.Node, string, *resolveState) *model.Node {
	return nil
}
func (stubLanguageDispatch) ResolveInterfaceDispatch(*model.Node, string, *resolveState) *model.Node {
	return nil
}
func (stubLanguageDispatch) PackagePrefix(model.Node) string { return "stub.pkg" }
