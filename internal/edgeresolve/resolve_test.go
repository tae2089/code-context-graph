package edgeresolve

import (
	"context"
	"testing"

	"github.com/tae2089/code-context-graph/internal/model"
)

type fakeLookup struct {
	nodes []model.Node
}

func (f fakeLookup) GetNodesByFiles(_ context.Context, filePaths []string) (map[string][]model.Node, error) {
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
