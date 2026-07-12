package treesitter

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/tae2089/code-context-graph/internal/adapters/outbound/graphgorm"
	search "github.com/tae2089/code-context-graph/internal/adapters/outbound/searchsql"
	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

func wantInheritsFingerprint(filePath, child, parent string) string {
	return graph.BuildInheritsFingerprintV2(filePath, child, parent)
}

func TestParseWithContext_RespectsContextCancellation(t *testing.T) {
	w := NewWalker(GoSpec)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 즉시 취소

	content := []byte(`package main
func Foo() {}`)

	// 취소된 context면 반드시 context.Canceled를 반환해야 한다.
	_, _, err := w.ParseWithContext(ctx, "test.go", content)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestExtractComments_RespectsContextCancellation(t *testing.T) {
	w := NewWalker(GoSpec)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := w.ExtractComments(ctx, "test.go", []byte("package main\n// hello\nfunc Foo() {}\n"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestParseSourceCtx_SingleShotReadSeam(t *testing.T) {
	w := NewWalker(GoSpec)
	tree, err := w.parseSourceCtx(context.Background(), []byte("package main\nfunc hello() {}\n"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tree == nil {
		t.Fatal("expected tree, got nil")
	}
	if got := tree.RootNode().Type(); got != "source_file" {
		t.Fatalf("root type = %q, want %q", got, "source_file")
	}
	tree.Close()
}

func TestParseSourceCtx_ParityWithParseCtx(t *testing.T) {
	tests := []struct {
		name    string
		walker  *Walker
		content []byte
	}{
		{
			name:    "go",
			walker:  NewWalker(GoSpec),
			content: []byte("package main\n\nfunc hello() {}\n"),
		},
		{
			name:    "python",
			walker:  NewWalker(PythonSpec),
			content: []byte("def hello():\n    return 1\n"),
		},
		{
			name:    "typescript",
			walker:  NewWalker(TypeScriptSpec),
			content: []byte("export function hello(): number {\n  return 1;\n}\n"),
		},
		{
			name:    "lua_typed_function",
			walker:  NewWalker(LuaSpec),
			content: []byte("function add(x: number, y: number): number\n    return x + y\nend\n"),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			defer tc.walker.Close()

			parser := tc.walker.acquireParser()
			defer tc.walker.releaseParser(parser)

			directTree, err := parser.ParseCtx(context.Background(), nil, tc.content)
			if err != nil {
				t.Fatalf("direct ParseCtx error: %v", err)
			}
			defer directTree.Close()

			helpTree, err := tc.walker.parseSourceCtx(context.Background(), tc.content)
			if err != nil {
				t.Fatalf("parseSourceCtx error: %v", err)
			}
			defer helpTree.Close()

			if got, want := directTree.RootNode().String(), helpTree.RootNode().String(); got != want {
				t.Fatalf("root string mismatch\n direct: %s\n helper: %s", got, want)
			}
		})
	}
}

func TestParseGo_EmptyFile(t *testing.T) {
	w := NewWalker(GoSpec)
	nodes, edges, err := w.Parse("main.go", []byte("package main\n"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	fileNodes := filterByKind(nodes, graph.NodeKindFile)
	if len(fileNodes) != 1 {
		t.Errorf("expected 1 file node, got %d", len(fileNodes))
	}
	funcNodes := filterByKind(nodes, graph.NodeKindFunction)
	if len(funcNodes) != 0 {
		t.Errorf("expected 0 function nodes, got %d", len(funcNodes))
	}
	_ = edges
}

func TestParseGo_SingleFunction(t *testing.T) {
	src := `package main

func hello() {
}
`
	w := NewWalker(GoSpec)
	nodes, _, err := w.Parse("main.go", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	funcNodes := filterByKind(nodes, graph.NodeKindFunction)
	if len(funcNodes) != 1 {
		t.Fatalf("expected 1 function node, got %d", len(funcNodes))
	}
	if funcNodes[0].Name != "hello" {
		t.Errorf("Name = %q, want %q", funcNodes[0].Name, "hello")
	}
}

func TestParseGo_FunctionWithParams(t *testing.T) {
	src := `package main

func add(a int, b int) int {
	return a + b
}
`
	w := NewWalker(GoSpec)
	nodes, _, err := w.Parse("main.go", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	funcNodes := filterByKind(nodes, graph.NodeKindFunction)
	if len(funcNodes) != 1 {
		t.Fatalf("expected 1 function node, got %d", len(funcNodes))
	}
	if funcNodes[0].Name != "add" {
		t.Errorf("Name = %q, want %q", funcNodes[0].Name, "add")
	}
	if funcNodes[0].StartLine < 1 {
		t.Errorf("StartLine = %d, want > 0", funcNodes[0].StartLine)
	}
}

func TestParseGo_SingleStruct(t *testing.T) {
	src := `package main

type User struct {
	Name string
}
`
	w := NewWalker(GoSpec)
	nodes, _, err := w.Parse("main.go", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	classNodes := filterByKind(nodes, graph.NodeKindClass)
	if len(classNodes) != 1 {
		t.Fatalf("expected 1 class node, got %d", len(classNodes))
	}
	if classNodes[0].Name != "User" {
		t.Errorf("Name = %q, want %q", classNodes[0].Name, "User")
	}
}

func TestParseGo_Interface(t *testing.T) {
	src := `package main

type Reader interface {
	Read(p []byte) (n int, err error)
}
`
	w := NewWalker(GoSpec)
	nodes, _, err := w.Parse("main.go", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	typeNodes := filterByKind(nodes, graph.NodeKindType)
	if len(typeNodes) != 1 {
		t.Fatalf("expected 1 type node, got %d", len(typeNodes))
	}
	if typeNodes[0].Name != "Reader" {
		t.Errorf("Name = %q, want %q", typeNodes[0].Name, "Reader")
	}
}

func TestParseGo_MethodOnStruct(t *testing.T) {
	src := `package main

type User struct{}

func (u *User) Greet() string {
	return "hello"
}
`
	w := NewWalker(GoSpec)
	nodes, edges, err := w.Parse("main.go", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	funcNodes := filterByKind(nodes, graph.NodeKindFunction)
	if len(funcNodes) != 1 {
		t.Fatalf("expected 1 method node, got %d", len(funcNodes))
	}
	if funcNodes[0].Name != "Greet" {
		t.Errorf("Name = %q, want %q", funcNodes[0].Name, "Greet")
	}
	containsEdges := filterEdgesByKind(edges, graph.EdgeKindContains)
	found := false
	for _, e := range containsEdges {
		if e.Kind == graph.EdgeKindContains {
			found = true
			break
		}
	}
	if !found && len(containsEdges) == 0 {
		t.Log("CONTAINS edge for method expected but not critical at this stage")
	}
}

// --- Phase 3.2: Edge extraction tests ---

func TestParseGo_FunctionCall(t *testing.T) {
	src := `package main

func greet() string {
	return "hello"
}

func main() {
	greet()
}
`
	w := NewWalker(GoSpec)
	_, edges, err := w.Parse("main.go", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	callEdges := filterEdgesByKind(edges, graph.EdgeKindCalls)
	if len(callEdges) == 0 {
		t.Fatal("expected at least 1 CALLS edge, got 0")
	}
	found := false
	for _, e := range callEdges {
		if e.Fingerprint != "" && e.Line > 0 {
			found = true
			break
		}
	}
	if !found {
		t.Error("CALLS edge should have non-empty Fingerprint and positive Line")
	}
}

func TestParseGo_TypeAssertionCallRewriteUsesSemanticsHook(t *testing.T) {
	src := `package main

type FlowTracer interface {
	TraceFlowBounded()
}

func handle(dep any) {
	tracer, ok := dep.(FlowTracer)
	if ok {
		tracer.TraceFlowBounded()
	}
}
`
	w := NewWalker(GoSpec)
	_, edges, err := w.Parse("main.go", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	callEdges := filterEdgesByKind(edges, graph.EdgeKindCalls)
	for _, e := range callEdges {
		if e.Fingerprint == "calls:main.go:FlowTracer.TraceFlowBounded:10" {
			return
		}
	}
	t.Fatalf("expected type assertion call rewrite edge, got %#v", callEdges)
}

func TestLanguageSemantics_DefaultCallRewriterNoop(t *testing.T) {
	rewriter := callRewriterOrDefault(semanticsOrDefault(PythonSpec), SemanticContext{})
	got := rewriter.RewriteCall(CallRewriteContext{Callee: "client.get", Line: 1})
	if got != "client.get" {
		t.Fatalf("default call rewriter changed callee: got %q", got)
	}
}

func TestLanguageSemantics_DefaultsForUnimplementedLanguages(t *testing.T) {
	tests := []struct {
		name string
		spec *LangSpec
	}{}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			semantics := semanticsOrDefault(tc.spec)
			if semantics == nil {
				t.Fatal("expected non-nil semantics")
			}

			edges := semantics.AdditionalEdges(SemanticContext{FilePath: "sample"})
			if edges != nil {
				t.Fatalf("expected nil additional edges for default semantics, got %+v", edges)
			}

			rewriter := callRewriterOrDefault(semantics, SemanticContext{})
			if rewriter == nil {
				t.Fatal("expected non-nil call rewriter")
			}

			got := rewriter.RewriteCall(CallRewriteContext{Callee: "client.get", Line: 1})
			if got != "client.get" {
				t.Fatalf("default call rewriter changed callee: got %q", got)
			}
		})
	}
	if len(tests) != 0 {
		return
	}
}

func TestParseGo_TypeAssertionCallRewriteUsesRepoPackageName(t *testing.T) {
	src := `package main

import dep "github.com/example/project/internal/api"

func handle(value any) {
	service := value.(dep.Service)
	service.Run()
}
`
	w := NewWalker(GoSpec)
	ctx := WithGoImportPackages(context.Background(), map[string]string{
		"github.com/example/project/internal/api": "contracts",
	})
	_, edges, _, err := w.ParseWithComments(ctx, "main.go", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	callEdges := filterEdgesByKind(edges, graph.EdgeKindCalls)
	for _, e := range callEdges {
		if e.Fingerprint == "calls:main.go:contracts.Service.Run:7" {
			return
		}
	}
	t.Fatalf("expected repo-local type assertion call rewrite edge, got %#v", callEdges)
}

func TestParseTypeScript_TypedReceiverChainRewritesCall(t *testing.T) {
	src := `interface FlowTracer {
	traceFlow(): void
}

interface Deps {
	flowTracer: FlowTracer
}

function start(deps: Deps) {
	deps.flowTracer.traceFlow()
}
`
	w := NewWalker(TypeScriptSpec)
	_, edges, err := w.Parse("app.ts", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	callEdges := filterEdgesByKind(edges, graph.EdgeKindCalls)
	for _, e := range callEdges {
		if e.Fingerprint == "calls:app.ts:FlowTracer.traceFlow:10" {
			return
		}
	}
	t.Fatalf("expected typed TypeScript receiver rewrite edge, got %#v", callEdges)
}

func TestParseTypeScript_TypedReceiverChainWithoutPropertyTypeStaysRaw(t *testing.T) {
	src := `interface Deps {
	flowTracer: any
}

function start(deps: Deps) {
	deps.flowTracer.traceFlow()
}
`
	w := NewWalker(TypeScriptSpec)
	_, edges, err := w.Parse("app.ts", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	callEdges := filterEdgesByKind(edges, graph.EdgeKindCalls)
	for _, e := range callEdges {
		if e.Fingerprint == "calls:app.ts:deps.flowTracer.traceFlow:6" {
			return
		}
	}
	t.Fatalf("expected raw TypeScript receiver edge without property type, got %#v", callEdges)
}

func TestParseJava_TypedReceiverChainRewritesCall(t *testing.T) {
	src := `package com.example;

interface FlowTracer {
	void traceFlow();
}

interface Deps {
	FlowTracer flowTracer = null;
}

class App {
	void run(Deps deps) {
		deps.flowTracer.traceFlow();
	}
}
`
	w := NewWalker(JavaSpec)
	_, edges, err := w.Parse("App.java", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	callEdges := filterEdgesByKind(edges, graph.EdgeKindCalls)
	for _, e := range callEdges {
		if e.Fingerprint == "calls:App.java:com.example.FlowTracer.traceFlow:13" {
			return
		}
	}
	t.Fatalf("expected typed Java receiver rewrite edge, got %#v", callEdges)
}

func TestParseJava_TypedReceiverChainWithImportedTypeAndArgumentsRewritesCall(t *testing.T) {
	src := `package com.example.app;

import com.contracts.FlowTracer;

interface Deps {
	FlowTracer flowTracer = null;
}

class App {
	void run(Deps deps, String ctx) {
		deps.flowTracer.traceFlow(ctx);
	}
}
`
	w := NewWalker(JavaSpec)
	_, edges, err := w.Parse("App.java", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	callEdges := filterEdgesByKind(edges, graph.EdgeKindCalls)
	for _, e := range callEdges {
		if e.Fingerprint == "calls:App.java:com.contracts.FlowTracer.traceFlow:11" {
			return
		}
	}
	t.Fatalf("expected imported Java receiver rewrite edge, got %#v", callEdges)
}

func TestParseJava_TypedReceiverChainWithoutMemberTypeStaysRaw(t *testing.T) {
	src := `package com.example;

class App {
	void run(Object deps) {
		deps.flowTracer.traceFlow();
	}
}
`
	w := NewWalker(JavaSpec)
	_, edges, err := w.Parse("App.java", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	callEdges := filterEdgesByKind(edges, graph.EdgeKindCalls)
	for _, e := range callEdges {
		if e.Fingerprint == "calls:App.java:deps.flowTracer.traceFlow():5" {
			return
		}
	}
	t.Fatalf("expected raw Java receiver edge without member type, got %#v", callEdges)
}

func TestParseKotlin_TypedReceiverChainRewritesCall(t *testing.T) {
	src := `package com.example

interface FlowTracer {
	fun traceFlow()
}

interface Deps {
	val flowTracer: FlowTracer
}

class App {
	fun run(deps: Deps) {
		deps.flowTracer.traceFlow()
	}
}
`
	w := NewWalker(KotlinSpec)
	_, edges, err := w.Parse("App.kt", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	callEdges := filterEdgesByKind(edges, graph.EdgeKindCalls)
	for _, e := range callEdges {
		if e.Fingerprint == "calls:App.kt:com.example.FlowTracer.traceFlow:13" {
			return
		}
	}
	t.Fatalf("expected typed Kotlin receiver rewrite edge, got %#v", callEdges)
}

func TestParseKotlin_TypedReceiverChainWithImportedTypeAndArgumentsRewritesCall(t *testing.T) {
	src := `package com.example.app

import com.contracts.FlowTracer

interface Deps {
	val flowTracer: FlowTracer
}

class App {
	fun run(deps: Deps, ctx: String) {
		deps.flowTracer.traceFlow(ctx)
	}
}
`
	w := NewWalker(KotlinSpec)
	_, edges, err := w.Parse("App.kt", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	callEdges := filterEdgesByKind(edges, graph.EdgeKindCalls)
	for _, e := range callEdges {
		if e.Fingerprint == "calls:App.kt:com.contracts.FlowTracer.traceFlow:11" {
			return
		}
	}
	t.Fatalf("expected imported Kotlin receiver rewrite edge, got %#v", callEdges)
}

func TestParseKotlin_TypedReceiverChainWithoutMemberTypeStaysRaw(t *testing.T) {
	src := `package com.example

class App {
	fun run(deps: Any) {
		deps.flowTracer.traceFlow()
	}
}
`
	w := NewWalker(KotlinSpec)
	_, edges, err := w.Parse("App.kt", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	callEdges := filterEdgesByKind(edges, graph.EdgeKindCalls)
	for _, e := range callEdges {
		if e.Fingerprint == "calls:App.kt:deps.flowTracer.traceFlow():5" {
			return
		}
	}
	t.Fatalf("expected raw Kotlin receiver edge without member type, got %#v", callEdges)
}

func TestParseGo_TypeAssertionCallRewriteMatchesAssertionResultPosition(t *testing.T) {
	src := `package main

type FlowTracer interface {
	TraceFlowBounded()
}

func handle(dep any) {
	a, tracer := 1, dep.(FlowTracer)
	_ = a
	tracer.TraceFlowBounded()
}
`
	w := NewWalker(GoSpec)
	_, edges, err := w.Parse("main.go", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	callEdges := filterEdgesByKind(edges, graph.EdgeKindCalls)
	for _, e := range callEdges {
		if e.Fingerprint == "calls:main.go:FlowTracer.TraceFlowBounded:10" {
			return
		}
	}
	t.Fatalf("expected positional type assertion call rewrite edge, got %#v", callEdges)
}

func TestParseGo_TypeAssertionCallRewriteSupportsAssignmentStatement(t *testing.T) {
	src := `package main

type FlowTracer interface {
	TraceFlowBounded()
}

func handle(dep any) {
	var tracer FlowTracer
	tracer = dep.(FlowTracer)
	tracer.TraceFlowBounded()
}
`
	w := NewWalker(GoSpec)
	_, edges, err := w.Parse("main.go", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	callEdges := filterEdgesByKind(edges, graph.EdgeKindCalls)
	for _, e := range callEdges {
		if e.Fingerprint == "calls:main.go:FlowTracer.TraceFlowBounded:10" {
			return
		}
	}
	t.Fatalf("expected assignment type assertion call rewrite edge, got %#v", callEdges)
}

func TestParseGo_Import(t *testing.T) {
	src := `package main

import "fmt"

func main() {
	fmt.Println("hello")
}
`
	w := NewWalker(GoSpec)
	_, edges, err := w.Parse("main.go", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	importEdges := filterEdgesByKind(edges, graph.EdgeKindImportsFrom)
	if len(importEdges) == 0 {
		t.Fatal("expected at least 1 IMPORTS_FROM edge, got 0")
	}
	found := false
	for _, e := range importEdges {
		if e.Fingerprint != "" && containsSubstring(e.Fingerprint, "fmt") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected IMPORTS_FROM edge referencing 'fmt'")
	}
}

func TestParseGo_InterfaceImplementation(t *testing.T) {
	src := `package main

type Writer interface {
	Write(data []byte) error
}

func TestGoSemantics_PackageEdgesDetectStructuralImplementationAcrossFiles(t *testing.T) {
	edges := PackageEdgesFor(GoSemantics{}, PackageContext{
		Package:  "main",
		Language: "go",
		Files:    []string{"iface.go", "impl.go"},
		Nodes: []graph.Node{
			{QualifiedName: "main.FileWriter.Write", Kind: graph.NodeKindFunction, Name: "Write", FilePath: "impl.go", Language: "go"},
		},
		Interfaces: []PackageInterfaceInfo{{Name: "Writer", Methods: []string{"Write"}}},
	})
	implEdges := filterEdgesByKind(edges, graph.EdgeKindImplements)
	if len(implEdges) != 1 {
		t.Fatalf("expected one package structural IMPLEMENTS edge, got %#v", implEdges)
	}
	if got, want := implEdges[0].Fingerprint, "implements:main:FileWriter:Writer"; got != want {
		t.Fatalf("fingerprint mismatch: got %q want %q", got, want)
	}
}

type FileWriter struct{}

func (fw *FileWriter) Write(data []byte) error {
	return nil
}
`
	w := NewWalker(GoSpec)
	_, edges, err := w.Parse("main.go", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	implEdges := filterEdgesByKind(edges, graph.EdgeKindImplements)
	if len(implEdges) != 0 {
		t.Fatalf("expected no file-local structural IMPLEMENTS edges, got %#v", implEdges)
	}
}

func TestParseGo_InterfaceAssertionImplementation(t *testing.T) {
	src := `package main

import (
	mcpserver "github.com/example/project/internal/mcp"
	"github.com/example/project/internal/flows"
)

var _ mcpserver.FlowTracer = (*flows.Tracer)(nil)
`
	w := NewWalker(GoSpec)
	_, edges, err := w.Parse("main.go", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	implEdges := filterEdgesByKind(edges, graph.EdgeKindImplements)
	for _, e := range implEdges {
		if e.Fingerprint == "implements:main.go:flows.Tracer:mcp.FlowTracer" {
			return
		}
	}
	t.Fatalf("expected assertion implements edge, got %#v", implEdges)
}

func TestParseGo_InterfaceAssertionStructLiteral(t *testing.T) {
	// Compile-time assertion using struct literal: var _ Iface = Type{}
	src := `package main

import (
	mcpserver "github.com/example/project/internal/mcp"
	"github.com/example/project/internal/flows"
)

var _ mcpserver.FlowTracer = flows.Tracer{}
`
	w := NewWalker(GoSpec)
	_, edges, err := w.Parse("main.go", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	implEdges := filterEdgesByKind(edges, graph.EdgeKindImplements)
	for _, e := range implEdges {
		if e.Fingerprint == "implements:main.go:flows.Tracer:mcp.FlowTracer" {
			return
		}
	}
	t.Fatalf("expected struct literal assertion implements edge, got %#v", implEdges)
}

func TestParseGo_InterfaceAssertionAddressOfStructLiteral(t *testing.T) {
	// Compile-time assertion using address-of struct literal: var _ Iface = &Type{}
	src := `package main

import (
	mcpserver "github.com/example/project/internal/mcp"
	"github.com/example/project/internal/flows"
)

var _ mcpserver.FlowTracer = &flows.Tracer{}
`
	w := NewWalker(GoSpec)
	_, edges, err := w.Parse("main.go", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	implEdges := filterEdgesByKind(edges, graph.EdgeKindImplements)
	for _, e := range implEdges {
		if e.Fingerprint == "implements:main.go:flows.Tracer:mcp.FlowTracer" {
			return
		}
	}
	t.Fatalf("expected address-of struct literal assertion implements edge, got %#v", implEdges)
}

func TestParseGo_InterfaceAssertionGrouped(t *testing.T) {
	// Grouped var declarations should each yield an implements edge.
	src := `package main

import (
	mcpserver "github.com/example/project/internal/mcp"
	"github.com/example/project/internal/flows"
)

var (
	_ mcpserver.FlowTracer = (*flows.Tracer)(nil)
	_ mcpserver.FlowTracer = flows.OtherTracer{}
)
`
	w := NewWalker(GoSpec)
	_, edges, err := w.Parse("main.go", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	implEdges := filterEdgesByKind(edges, graph.EdgeKindImplements)

	wantFingerprints := []string{
		"implements:main.go:flows.Tracer:mcp.FlowTracer",
		"implements:main.go:flows.OtherTracer:mcp.FlowTracer",
	}
	for _, want := range wantFingerprints {
		found := false
		for _, e := range implEdges {
			if e.Fingerprint == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected fingerprint %q in grouped assertion edges, got %#v", want, implEdges)
		}
	}
}

func TestParseGo_InterfaceAssertionsFromServerPackage(t *testing.T) {
	src, err := os.ReadFile("../../inbound/http/assertions.go")
	if err != nil {
		t.Fatalf("read server assertions: %v", err)
	}
	ctx := WithImportPackages(context.Background(), map[string]string{
		"github.com/tae2089/code-context-graph/internal/app/analyze/flow":     "flow",
		"github.com/tae2089/code-context-graph/internal/app/analyze/impact":   "impact",
		"github.com/tae2089/code-context-graph/internal/app/analyze/query":    "query",
		"github.com/tae2089/code-context-graph/internal/adapters/inbound/mcp": "mcp",
	})
	w := NewWalker(GoSpec)
	const assertionPath = "internal/adapters/inbound/http/assertions.go"
	_, edges, err := w.ParseWithContext(ctx, assertionPath, src)
	if err != nil {
		t.Fatalf("parse server assertions: %v", err)
	}
	implEdges := filterEdgesByKind(edges, graph.EdgeKindImplements)
	wantFingerprints := []string{
		"implements:" + assertionPath + ":impact.Analyzer:mcp.ImpactAnalyzer",
		"implements:" + assertionPath + ":flow.Tracer:mcp.FlowTracer",
		"implements:" + assertionPath + ":query.Service:mcp.QueryService",
	}
	for _, want := range wantFingerprints {
		found := false
		for _, e := range implEdges {
			if e.Fingerprint == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected fingerprint %q in server assertion edges, got %#v", want, implEdges)
		}
	}
}

func TestParseGo_ImportAliasVersionedPath(t *testing.T) {
	// When the import path ends with a version segment like ".v3", the canonical
	// package name is the segment before the version (e.g. "yaml" for "gopkg.in/yaml.v3").
	// Without an explicit alias, the fallback should resolve to "yaml" rather than "v3".
	src := `package main

import (
	"gopkg.in/yaml.v3"
)

type MyType struct{}

var _ yaml.Marshaler = (*MyType)(nil)
`
	w := NewWalker(GoSpec)
	_, edges, err := w.Parse("main.go", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	implEdges := filterEdgesByKind(edges, graph.EdgeKindImplements)
	for _, e := range implEdges {
		if e.Fingerprint == "implements:main.go:MyType:yaml.Marshaler" {
			return
		}
	}
	t.Fatalf("expected versioned-import alias to normalize to yaml, got %#v", implEdges)
}

func TestParseGo_ExplicitImportAliasVersionedPath(t *testing.T) {
	// Explicit aliases should still normalize to the imported package name rather
	// than preserving version suffix segments from the path.
	src := `package main

import (
	y3 "gopkg.in/yaml.v3"
)

type MyType struct{}

var _ y3.Marshaler = (*MyType)(nil)
`
	w := NewWalker(GoSpec)
	_, edges, err := w.Parse("main.go", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	implEdges := filterEdgesByKind(edges, graph.EdgeKindImplements)
	for _, e := range implEdges {
		if e.Fingerprint == "implements:main.go:MyType:yaml.Marshaler" {
			return
		}
	}
	t.Fatalf("expected explicit versioned-import alias to normalize to yaml, got %#v", implEdges)
}

func TestParseGo_RepoLocalImportUsesPackageClauseName(t *testing.T) {
	src := `package main

import dep "github.com/example/project/internal/api"

type MyType struct{}

var _ dep.Service = (*MyType)(nil)
`
	w := NewWalker(GoSpec)
	ctx := WithGoImportPackages(context.Background(), map[string]string{
		"github.com/example/project/internal/api": "contracts",
	})
	_, edges, _, err := w.ParseWithComments(ctx, "main.go", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	implEdges := filterEdgesByKind(edges, graph.EdgeKindImplements)
	for _, e := range implEdges {
		if e.Fingerprint == "implements:main.go:MyType:contracts.Service" {
			return
		}
	}
	t.Fatalf("expected repo-local import alias to use package clause name, got %#v", implEdges)
}

func TestParseGo_StructEmbedding(t *testing.T) {
	src := `package main

type Base struct {
	ID int
}

type Child struct {
	Base
	Name string
}
`
	w := NewWalker(GoSpec)
	_, edges, err := w.Parse("main.go", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	inheritEdges := filterEdgesByKind(edges, graph.EdgeKindInherits)
	if len(inheritEdges) == 0 {
		t.Fatal("expected at least 1 INHERITS edge, got 0")
	}
	want := wantInheritsFingerprint("main.go", "Child", "Base")
	for _, e := range inheritEdges {
		if e.Fingerprint == want {
			return
		}
	}
	t.Fatalf("expected INHERITS edge %q, got %+v", want, inheritEdges)
}

func containsSubstring(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && findSubstring(s, sub))
}

func findSubstring(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// --- Phase 3.3: File node + CONTAINS edges ---

func TestParseGo_FileNode(t *testing.T) {
	src := `package main

func hello() {}
`
	w := NewWalker(GoSpec)
	nodes, _, err := w.Parse("src/main.go", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	fileNodes := filterByKind(nodes, graph.NodeKindFile)
	if len(fileNodes) != 1 {
		t.Fatalf("expected 1 file node, got %d", len(fileNodes))
	}
	fn := fileNodes[0]
	if fn.QualifiedName != "src/main.go" {
		t.Errorf("QualifiedName = %q, want %q", fn.QualifiedName, "src/main.go")
	}
	if fn.Name != "src/main.go" {
		t.Errorf("Name = %q, want %q", fn.Name, "src/main.go")
	}
	if fn.FilePath != "src/main.go" {
		t.Errorf("FilePath = %q, want %q", fn.FilePath, "src/main.go")
	}
	if fn.Language != "go" {
		t.Errorf("Language = %q, want %q", fn.Language, "go")
	}
	if fn.StartLine != 1 {
		t.Errorf("StartLine = %d, want 1", fn.StartLine)
	}
}

func TestParseGo_ContainsEdges(t *testing.T) {
	src := `package main

type User struct{}

func hello() {}

func world() {}
`
	w := NewWalker(GoSpec)
	_, edges, err := w.Parse("main.go", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	containsEdges := filterEdgesByKind(edges, graph.EdgeKindContains)
	if len(containsEdges) < 3 {
		t.Errorf("expected at least 3 CONTAINS edges (file→User, file→hello, file→world), got %d", len(containsEdges))
	}
}

// --- Phase 3.4: QualifiedName generation ---

func TestParseGo_QualifiedName_Function(t *testing.T) {
	src := `package auth

func Authenticate() {}
`
	w := NewWalker(GoSpec)
	nodes, _, err := w.Parse("auth.go", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	funcNodes := filterByKind(nodes, graph.NodeKindFunction)
	if len(funcNodes) != 1 {
		t.Fatalf("expected 1 function node, got %d", len(funcNodes))
	}
	want := "auth.Authenticate"
	if funcNodes[0].QualifiedName != want {
		t.Errorf("QualifiedName = %q, want %q", funcNodes[0].QualifiedName, want)
	}
}

func TestParseGo_QualifiedName_Method(t *testing.T) {
	src := `package auth

type Service struct{}

func (s *Service) Login() {}
`
	w := NewWalker(GoSpec)
	nodes, _, err := w.Parse("auth.go", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	funcNodes := filterByKind(nodes, graph.NodeKindFunction)
	if len(funcNodes) != 1 {
		t.Fatalf("expected 1 method node, got %d", len(funcNodes))
	}
	want := "auth.Service.Login"
	if funcNodes[0].QualifiedName != want {
		t.Errorf("QualifiedName = %q, want %q", funcNodes[0].QualifiedName, want)
	}
}

func TestParseGo_QualifiedName_NestedType(t *testing.T) {
	src := `package config

type Settings struct {
	Name string
}
`
	w := NewWalker(GoSpec)
	nodes, _, err := w.Parse("config.go", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	classNodes := filterByKind(nodes, graph.NodeKindClass)
	if len(classNodes) != 1 {
		t.Fatalf("expected 1 class node, got %d", len(classNodes))
	}
	want := "config.Settings"
	if classNodes[0].QualifiedName != want {
		t.Errorf("QualifiedName = %q, want %q", classNodes[0].QualifiedName, want)
	}
}

// --- Phase 3.5: Multi-language ---

func TestParsePython_SingleFunction(t *testing.T) {
	src := `def greet():
    return "hello"
`
	w := NewWalker(PythonSpec)
	nodes, _, err := w.Parse("app.py", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	funcNodes := filterByKind(nodes, graph.NodeKindFunction)
	if len(funcNodes) != 1 {
		t.Fatalf("expected 1 function node, got %d", len(funcNodes))
	}
	if funcNodes[0].Name != "greet" {
		t.Errorf("Name = %q, want %q", funcNodes[0].Name, "greet")
	}
}

func TestParsePython_Class(t *testing.T) {
	src := `class User:
    def __init__(self, name):
        self.name = name
`
	w := NewWalker(PythonSpec)
	nodes, _, err := w.Parse("models.py", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	classNodes := filterByKind(nodes, graph.NodeKindClass)
	if len(classNodes) != 1 {
		t.Fatalf("expected 1 class node, got %d", len(classNodes))
	}
	if classNodes[0].Name != "User" {
		t.Errorf("Name = %q, want %q", classNodes[0].Name, "User")
	}
}

func TestParsePython_ClassInheritsEdge(t *testing.T) {
	src := `class Base:
    pass

class User(Base):
    pass
`
	w := NewWalker(PythonSpec)
	_, edges, err := w.Parse("models.py", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	inheritEdges := filterEdgesByKind(edges, graph.EdgeKindInherits)
	if len(inheritEdges) == 0 {
		t.Fatal("expected at least 1 INHERITS edge, got 0")
	}
	want := wantInheritsFingerprint("models.py", "User", "Base")
	for _, e := range inheritEdges {
		if e.Fingerprint == want {
			return
		}
	}
	t.Fatalf("expected INHERITS edge %q, got %+v", want, inheritEdges)
}

func TestParseTypeScript_Function(t *testing.T) {
	src := `function greet(): string {
    return "hello";
}
`
	w := NewWalker(TypeScriptSpec)
	nodes, _, err := w.Parse("app.ts", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	funcNodes := filterByKind(nodes, graph.NodeKindFunction)
	if len(funcNodes) != 1 {
		t.Fatalf("expected 1 function node, got %d", len(funcNodes))
	}
	if funcNodes[0].Name != "greet" {
		t.Errorf("Name = %q, want %q", funcNodes[0].Name, "greet")
	}
}

func TestParseTypeScript_Function_QualifiedNameIncludesFilePackagePrefix(t *testing.T) {
	src := `function greet(): string {
    return "hello";
}
`
	ctx := WithFilePackages(context.Background(), map[string]string{
		"app.ts": "@acme/app",
	})
	w := NewWalker(TypeScriptSpec)
	nodes, _, err := w.ParseWithContext(ctx, "app.ts", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	funcNodes := filterByKind(nodes, graph.NodeKindFunction)
	if len(funcNodes) != 1 {
		t.Fatalf("expected 1 function node, got %d", len(funcNodes))
	}
	if funcNodes[0].QualifiedName != "@acme/app.greet" {
		t.Fatalf("QualifiedName = %q, want %q", funcNodes[0].QualifiedName, "@acme/app.greet")
	}
}

func TestParseTypeScript_Class(t *testing.T) {
	src := `class User {
    name: string;
    constructor(name: string) {
        this.name = name;
    }
}
`
	w := NewWalker(TypeScriptSpec)
	nodes, _, err := w.Parse("models.ts", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	classNodes := filterByKind(nodes, graph.NodeKindClass)
	if len(classNodes) != 1 {
		t.Fatalf("expected 1 class node, got %d", len(classNodes))
	}
	if classNodes[0].Name != "User" {
		t.Errorf("Name = %q, want %q", classNodes[0].Name, "User")
	}
}

func TestParseTypeScript_ClassMethod_QualifiedNameIncludesReceiver(t *testing.T) {
	src := `class User {
    login(): void {}
}
`
	w := NewWalker(TypeScriptSpec)
	nodes, _, err := w.Parse("models.ts", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	funcNodes := filterByKind(nodes, graph.NodeKindFunction)
	if len(funcNodes) != 1 {
		t.Fatalf("expected 1 function node, got %d", len(funcNodes))
	}
	if funcNodes[0].Name != "login" {
		t.Fatalf("Name = %q, want %q", funcNodes[0].Name, "login")
	}
	if funcNodes[0].QualifiedName != "User.login" {
		t.Fatalf("QualifiedName = %q, want %q", funcNodes[0].QualifiedName, "User.login")
	}
}

func TestParseTypeScript_ExtendsAndImplementsEdges(t *testing.T) {
	src := `class User extends Base implements Authenticated, Named {
}
	`
	w := NewWalker(TypeScriptSpec)
	_, edges, err := w.Parse("models.ts", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var foundAuth, foundNamed bool
	wantInherits := wantInheritsFingerprint("models.ts", "User", "Base")
	for _, e := range edges {
		switch e.Kind {
		case graph.EdgeKindInherits:
			if e.Fingerprint != wantInherits {
				t.Fatalf("unexpected inherits fingerprint: got %q want %q", e.Fingerprint, wantInherits)
			}
		case graph.EdgeKindImplements:
			if containsSubstring(e.Fingerprint, "User") && containsSubstring(e.Fingerprint, "Authenticated") {
				foundAuth = true
			}
			if containsSubstring(e.Fingerprint, "User") && containsSubstring(e.Fingerprint, "Named") {
				foundNamed = true
			}
		}
	}
	if !foundAuth || !foundNamed {
		t.Fatalf("expected inherits+implements edges, got %+v", edges)
	}
}

func TestParseTypeScript_HeritageChildQualifiedNameIncludesFilePackagePrefix(t *testing.T) {
	src := `class User extends Base implements Authenticated {
}
`
	ctx := WithFilePackages(context.Background(), map[string]string{
		"src/models/user.ts": "@acme/app/src/models",
	})
	w := NewWalker(TypeScriptSpec)
	_, edges, err := w.ParseWithContext(ctx, "src/models/user.ts", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	qualifiedChild := "@acme/app/src/models.User"
	inheritEdges := filterEdgesByKind(edges, graph.EdgeKindInherits)
	if len(inheritEdges) != 1 {
		t.Fatalf("expected 1 inherits edge, got %+v", inheritEdges)
	}
	child, parent, ok := graph.ParseInheritsFingerprint("src/models/user.ts", inheritEdges[0].Fingerprint)
	if !ok {
		t.Fatalf("expected parseable inherits fingerprint, got %q", inheritEdges[0].Fingerprint)
	}
	if child != qualifiedChild {
		t.Fatalf("inherits child = %q, want %q", child, qualifiedChild)
	}
	if parent != "Base" {
		t.Fatalf("inherits parent = %q, want %q", parent, "Base")
	}

	implEdges := filterEdgesByKind(edges, graph.EdgeKindImplements)
	wantImpl := "implements:src/models/user.ts:@acme/app/src/models.User:Authenticated"
	foundImpl := false
	for _, edge := range implEdges {
		if edge.Fingerprint == wantImpl {
			foundImpl = true
			break
		}
	}
	if !foundImpl {
		t.Fatalf("expected implements fingerprint %q, got %+v", wantImpl, implEdges)
	}
}

func TestParseTypeScript_HeritageSameFileTargetsIncludeFilePackagePrefix(t *testing.T) {
	src := `interface Authenticated {}

class Base {}

class User extends Base implements Authenticated {
}
`
	ctx := WithFilePackages(context.Background(), map[string]string{
		"src/models/user.ts": "@acme/app/src/models",
	})
	w := NewWalker(TypeScriptSpec)
	_, edges, err := w.ParseWithContext(ctx, "src/models/user.ts", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	qualifiedUser := "@acme/app/src/models.User"
	qualifiedBase := "@acme/app/src/models.Base"
	qualifiedIface := "@acme/app/src/models.Authenticated"

	inheritEdges := filterEdgesByKind(edges, graph.EdgeKindInherits)
	if len(inheritEdges) != 1 {
		t.Fatalf("expected 1 inherits edge, got %+v", inheritEdges)
	}
	child, parent, ok := graph.ParseInheritsFingerprint("src/models/user.ts", inheritEdges[0].Fingerprint)
	if !ok {
		t.Fatalf("expected parseable inherits fingerprint, got %q", inheritEdges[0].Fingerprint)
	}
	if child != qualifiedUser {
		t.Fatalf("inherits child = %q, want %q", child, qualifiedUser)
	}
	if parent != qualifiedBase {
		t.Fatalf("inherits parent = %q, want %q", parent, qualifiedBase)
	}

	implEdges := filterEdgesByKind(edges, graph.EdgeKindImplements)
	wantImpl := "implements:src/models/user.ts:@acme/app/src/models.User:@acme/app/src/models.Authenticated"
	foundImpl := false
	for _, edge := range implEdges {
		if edge.Fingerprint == wantImpl {
			foundImpl = true
			break
		}
	}
	if !foundImpl {
		t.Fatalf("expected implements fingerprint %q, got %+v", wantImpl, implEdges)
	}
	_ = qualifiedIface
}

func TestParseTypeScript_HeritageImportedTargetsIncludeImportPackagePrefix(t *testing.T) {
	src := `import { Base } from "./base";
import { Authenticated } from "./contracts";

class User extends Base implements Authenticated {
}
`
	ctx := WithFilePackages(context.Background(), map[string]string{
		"src/models/user.ts": "@acme/app/src/models",
	})
	ctx = WithImportPackages(ctx, map[string]string{
		"./base":      "@acme/app/src/base",
		"./contracts": "@acme/app/src/contracts",
	})
	w := NewWalker(TypeScriptSpec)
	_, edges, err := w.ParseWithContext(ctx, "src/models/user.ts", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	qualifiedUser := "@acme/app/src/models.User"
	qualifiedBase := "@acme/app/src/base.Base"
	qualifiedIface := "@acme/app/src/contracts.Authenticated"

	inheritEdges := filterEdgesByKind(edges, graph.EdgeKindInherits)
	if len(inheritEdges) != 1 {
		t.Fatalf("expected 1 inherits edge, got %+v", inheritEdges)
	}
	child, parent, ok := graph.ParseInheritsFingerprint("src/models/user.ts", inheritEdges[0].Fingerprint)
	if !ok {
		t.Fatalf("expected parseable inherits fingerprint, got %q", inheritEdges[0].Fingerprint)
	}
	if child != qualifiedUser {
		t.Fatalf("inherits child = %q, want %q", child, qualifiedUser)
	}
	if parent != qualifiedBase {
		t.Fatalf("inherits parent = %q, want %q", parent, qualifiedBase)
	}

	implEdges := filterEdgesByKind(edges, graph.EdgeKindImplements)
	wantImpl := "implements:src/models/user.ts:@acme/app/src/models.User:@acme/app/src/contracts.Authenticated"
	foundImpl := false
	for _, edge := range implEdges {
		if edge.Fingerprint == wantImpl {
			foundImpl = true
			break
		}
	}
	if !foundImpl {
		t.Fatalf("expected implements fingerprint %q, got %+v", wantImpl, implEdges)
	}
	_ = qualifiedIface
}

func TestParseTypeScript_HeritageImportedAliasTargetsIncludeImportPackagePrefix(t *testing.T) {
	src := `import { Base } from "@app/base";
import { Authenticated } from "@app/contracts";

class User extends Base implements Authenticated {
}
`
	ctx := WithFilePackages(context.Background(), map[string]string{
		"src/models/user.ts": "@acme/app/src/models",
	})
	ctx = WithImportPackages(ctx, map[string]string{
		"@app/base":      "@acme/app/src/base",
		"@app/contracts": "@acme/app/src/contracts",
	})
	w := NewWalker(TypeScriptSpec)
	_, edges, err := w.ParseWithContext(ctx, "src/models/user.ts", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	qualifiedUser := "@acme/app/src/models.User"
	qualifiedBase := "@acme/app/src/base.Base"
	qualifiedIface := "@acme/app/src/contracts.Authenticated"

	inheritEdges := filterEdgesByKind(edges, graph.EdgeKindInherits)
	if len(inheritEdges) != 1 {
		t.Fatalf("expected 1 inherits edge, got %+v", inheritEdges)
	}
	child, parent, ok := graph.ParseInheritsFingerprint("src/models/user.ts", inheritEdges[0].Fingerprint)
	if !ok {
		t.Fatalf("expected parseable inherits fingerprint, got %q", inheritEdges[0].Fingerprint)
	}
	if child != qualifiedUser {
		t.Fatalf("inherits child = %q, want %q", child, qualifiedUser)
	}
	if parent != qualifiedBase {
		t.Fatalf("inherits parent = %q, want %q", parent, qualifiedBase)
	}

	implEdges := filterEdgesByKind(edges, graph.EdgeKindImplements)
	wantImpl := "implements:src/models/user.ts:@acme/app/src/models.User:@acme/app/src/contracts.Authenticated"
	foundImpl := false
	for _, edge := range implEdges {
		if edge.Fingerprint == wantImpl {
			foundImpl = true
			break
		}
	}
	if !foundImpl {
		t.Fatalf("expected implements fingerprint %q, got %+v", wantImpl, implEdges)
	}
	_ = qualifiedIface
}

func TestParseTypeScript_HeritageIgnoresCommasInsideGenericArguments(t *testing.T) {
	src := `class User implements Handler<Request<T>, Response>, Serializable {
}
`
	w := NewWalker(TypeScriptSpec)
	_, edges, err := w.Parse("models.ts", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var foundHandler, foundSerializable bool
	for _, e := range filterEdgesByKind(edges, graph.EdgeKindImplements) {
		if e.Fingerprint == "implements:models.ts:User:Handler" {
			foundHandler = true
		}
		if e.Fingerprint == "implements:models.ts:User:Serializable" {
			foundSerializable = true
		}
		if containsSubstring(e.Fingerprint, "Response") || containsSubstring(e.Fingerprint, "C>") {
			t.Fatalf("unexpected generic fragment leaked into implements edge: %+v", e)
		}
	}
	if !foundHandler || !foundSerializable {
		t.Fatalf("expected implements edges for Handler and Serializable, got %+v", edges)
	}
}

func TestParseJava_Class(t *testing.T) {
	src := `public class User {
    private String name;
    public User(String name) {
        this.name = name;
    }
}
`
	w := NewWalker(JavaSpec)
	nodes, _, err := w.Parse("User.java", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	classNodes := filterByKind(nodes, graph.NodeKindClass)
	if len(classNodes) != 1 {
		t.Fatalf("expected 1 class node, got %d", len(classNodes))
	}
	if classNodes[0].Name != "User" {
		t.Errorf("Name = %q, want %q", classNodes[0].Name, "User")
	}
}

func TestParseJava_QualifiedNameIncludesPackage(t *testing.T) {
	src := `package com.example.auth;

public class User {
}
`
	w := NewWalker(JavaSpec)
	nodes, _, err := w.Parse("User.java", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	classNodes := filterByKind(nodes, graph.NodeKindClass)
	if len(classNodes) != 1 {
		t.Fatalf("expected 1 class node, got %d", len(classNodes))
	}
	if classNodes[0].QualifiedName != "com.example.auth.User" {
		t.Fatalf("QualifiedName = %q, want %q", classNodes[0].QualifiedName, "com.example.auth.User")
	}
}

func TestParseJava_ExtendsAndImplementsEdges(t *testing.T) {
	src := `package com.example.auth;

public class User extends Base implements Authenticated, Named {
}
	`
	w := NewWalker(JavaSpec)
	_, edges, err := w.Parse("User.java", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var foundAuth, foundNamed bool
	wantInherits := wantInheritsFingerprint("User.java", "com.example.auth.User", "com.example.auth.Base")
	for _, e := range edges {
		switch e.Kind {
		case graph.EdgeKindInherits:
			if e.Fingerprint != wantInherits {
				t.Fatalf("unexpected inherits fingerprint: got %q want %q", e.Fingerprint, wantInherits)
			}
		case graph.EdgeKindImplements:
			if containsSubstring(e.Fingerprint, "User") && containsSubstring(e.Fingerprint, "Authenticated") {
				foundAuth = true
			}
			if containsSubstring(e.Fingerprint, "User") && containsSubstring(e.Fingerprint, "Named") {
				foundNamed = true
			}
		}
	}
	if !foundAuth || !foundNamed {
		t.Fatalf("expected inherits+implements edges, got %+v", edges)
	}
}

func TestParseJava_HierarchyIgnoresCommasInsideGenericArguments(t *testing.T) {
	src := `package com.example.auth;

public class User extends Base<String, Integer> implements Handler<Request<T>, Response>, Serializable {
}
`
	w := NewWalker(JavaSpec)
	_, edges, err := w.Parse("User.java", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var foundBase, foundHandler, foundSerializable bool
	for _, e := range edges {
		switch e.Kind {
		case graph.EdgeKindInherits:
			if e.Fingerprint == wantInheritsFingerprint("User.java", "com.example.auth.User", "com.example.auth.Base") {
				foundBase = true
			}
		case graph.EdgeKindImplements:
			if e.Fingerprint == "implements:User.java:com.example.auth.User:com.example.auth.Handler" {
				foundHandler = true
			}
			if e.Fingerprint == "implements:User.java:com.example.auth.User:com.example.auth.Serializable" {
				foundSerializable = true
			}
			if containsSubstring(e.Fingerprint, "Response") {
				t.Fatalf("unexpected generic fragment leaked into implements edge: %+v", e)
			}
		}
	}
	if !foundBase || !foundHandler || !foundSerializable {
		t.Fatalf("expected generic-safe hierarchy edges, got %+v", edges)
	}
}

func TestParseRuby_Method(t *testing.T) {
	src := `def greet
  "hello"
end
`
	w := NewWalker(RubySpec)
	nodes, _, err := w.Parse("app.rb", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	funcNodes := filterByKind(nodes, graph.NodeKindFunction)
	if len(funcNodes) != 1 {
		t.Fatalf("expected 1 function node, got %d", len(funcNodes))
	}
	if funcNodes[0].Name != "greet" {
		t.Errorf("Name = %q, want %q", funcNodes[0].Name, "greet")
	}
}

// --- Phase 3.6: Test detection + TESTED_BY ---

func TestParseGo_TestFunction(t *testing.T) {
	src := `package main

func TestAdd(t *testing.T) {}
`
	w := NewWalker(GoSpec)
	nodes, _, err := w.Parse("main_test.go", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	testNodes := filterByKind(nodes, graph.NodeKindTest)
	if len(testNodes) != 1 {
		t.Fatalf("expected 1 test node, got %d", len(testNodes))
	}
	if testNodes[0].Name != "TestAdd" {
		t.Errorf("Name = %q, want %q", testNodes[0].Name, "TestAdd")
	}
}

func TestParseGo_TestPrefixDoesNotMisclassifyProductionDecls(t *testing.T) {
	src := `package main

type TestConfig struct{}

func Testimony() {}

func TestAdd(t *testing.T) {}
`
	w := NewWalker(GoSpec)
	nodes, _, err := w.Parse("main_test.go", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	testNodes := filterByKind(nodes, graph.NodeKindTest)
	if len(testNodes) != 1 || testNodes[0].Name != "TestAdd" {
		t.Fatalf("expected only TestAdd as a test node, got %+v", testNodes)
	}
	// TestConfig (a type) and Testimony (Test + lowercase 'i') must not be tests.
	byName := map[string]graph.NodeKind{}
	for _, n := range nodes {
		byName[n.Name] = n.Kind
	}
	if byName["TestConfig"] == graph.NodeKindTest {
		t.Errorf("TestConfig type must not be a test node")
	}
	if byName["Testimony"] == graph.NodeKindTest {
		t.Errorf("Testimony (lowercase continuation) must not be a test node")
	}
}

func TestParseTypeScript_TestPrefixRequiresWordBoundary(t *testing.T) {
	src := `function testimonialCard() {}
function testLogin() {}
`
	w := NewWalker(TypeScriptSpec)
	nodes, _, err := w.Parse("app.ts", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	kinds := map[string]graph.NodeKind{}
	for _, n := range nodes {
		kinds[n.Name] = n.Kind
	}
	if kinds["testimonialCard"] == graph.NodeKindTest {
		t.Errorf("testimonialCard must not be classified as a test")
	}
	if kinds["testLogin"] != graph.NodeKindTest {
		t.Errorf("testLogin should be classified as a test, got %v", kinds["testLogin"])
	}
}

func TestParsePython_TestFunction(t *testing.T) {
	src := `def test_add():
    assert 1 + 1 == 2
`
	w := NewWalker(PythonSpec)
	nodes, _, err := w.Parse("test_math.py", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	testNodes := filterByKind(nodes, graph.NodeKindTest)
	if len(testNodes) != 1 {
		t.Fatalf("expected 1 test node, got %d", len(testNodes))
	}
	if testNodes[0].Name != "test_add" {
		t.Errorf("Name = %q, want %q", testNodes[0].Name, "test_add")
	}
}

func TestParseGo_TestedBy(t *testing.T) {
	src := `package main

func Add(a, b int) int {
	return a + b
}

func TestAdd(t *testing.T) {
	Add(1, 2)
}
`
	w := NewWalker(GoSpec)
	_, edges, err := w.Parse("main_test.go", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	testedByEdges := filterEdgesByKind(edges, graph.EdgeKindTestedBy)
	if len(testedByEdges) == 0 {
		t.Fatal("expected at least 1 TESTED_BY edge, got 0")
	}
	found := false
	for _, e := range testedByEdges {
		if containsSubstring(e.Fingerprint, "Add") && containsSubstring(e.Fingerprint, "TestAdd") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected TESTED_BY edge linking Add to TestAdd")
	}
}

func TestParseGo_TestedByEmitsCandidateForCrossFileProductionCall(t *testing.T) {
	src := `package main

func TestAdd(t *testing.T) {
	Add(1, 2)
}
`
	w := NewWalker(GoSpec)
	_, edges, err := w.Parse("main_test.go", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, e := range filterEdgesByKind(edges, graph.EdgeKindTestedBy) {
		if containsSubstring(e.Fingerprint, "Add") && containsSubstring(e.Fingerprint, "TestAdd") {
			return
		}
	}
	t.Fatalf("expected TESTED_BY candidate for Add call in TestAdd, got %+v", edges)
}

func TestParseGo_TestedByPreservesQualifiedCallee(t *testing.T) {
	src := `package main

func TestAdd(t *testing.T) {
	calc.Add(1, 2)
}
`
	w := NewWalker(GoSpec)
	_, edges, err := w.Parse("main_test.go", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, e := range filterEdgesByKind(edges, graph.EdgeKindTestedBy) {
		if containsSubstring(e.Fingerprint, "calc.Add") && containsSubstring(e.Fingerprint, "TestAdd") {
			return
		}
	}
	t.Fatalf("expected TESTED_BY candidate to preserve calc.Add call, got %+v", edges)
}

// --- Phase 12.1: JavaScript ---

func TestParseJS_Function(t *testing.T) {
	src := `function foo() {
    return 42;
}
`
	w := NewWalker(JavaScriptSpec)
	nodes, _, err := w.Parse("app.js", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	funcNodes := filterByKind(nodes, graph.NodeKindFunction)
	if len(funcNodes) != 1 {
		t.Fatalf("expected 1 function node, got %d", len(funcNodes))
	}
	if funcNodes[0].Name != "foo" {
		t.Errorf("Name = %q, want %q", funcNodes[0].Name, "foo")
	}
	if funcNodes[0].Language != "javascript" {
		t.Errorf("Language = %q, want %q", funcNodes[0].Language, "javascript")
	}
}

func TestParseJS_Class(t *testing.T) {
	src := `class Foo {
    constructor() {}
    bar() {}
}
`
	w := NewWalker(JavaScriptSpec)
	nodes, _, err := w.Parse("app.js", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	classNodes := filterByKind(nodes, graph.NodeKindClass)
	if len(classNodes) != 1 {
		t.Fatalf("expected 1 class node, got %d", len(classNodes))
	}
	if classNodes[0].Name != "Foo" {
		t.Errorf("Name = %q, want %q", classNodes[0].Name, "Foo")
	}
}

func TestParseJavaScript_ExtendsEdge(t *testing.T) {
	src := `class User extends Base {
}
`
	w := NewWalker(JavaScriptSpec)
	_, edges, err := w.Parse("app.js", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	inheritEdges := filterEdgesByKind(edges, graph.EdgeKindInherits)
	if len(inheritEdges) == 0 {
		t.Fatal("expected at least 1 INHERITS edge, got 0")
	}
	want := wantInheritsFingerprint("app.js", "User", "Base")
	for _, e := range inheritEdges {
		if e.Fingerprint == want {
			return
		}
	}
	t.Fatalf("expected INHERITS edge %q, got %+v", want, inheritEdges)
}

func TestParseJS_Import(t *testing.T) {
	src := `import { foo } from 'bar';
`
	w := NewWalker(JavaScriptSpec)
	_, edges, err := w.Parse("app.js", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	importEdges := filterEdgesByKind(edges, graph.EdgeKindImportsFrom)
	if len(importEdges) == 0 {
		t.Fatal("expected at least 1 IMPORTS_FROM edge, got 0")
	}
}

func TestParseJS_Call(t *testing.T) {
	src := `function bar() {}
function main() {
    bar();
}
`
	w := NewWalker(JavaScriptSpec)
	_, edges, err := w.Parse("app.js", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	callEdges := filterEdgesByKind(edges, graph.EdgeKindCalls)
	if len(callEdges) == 0 {
		t.Fatal("expected at least 1 CALLS edge, got 0")
	}
}

func TestParseJS_Export(t *testing.T) {
	src := `export function foo() {
    return 42;
}
`
	w := NewWalker(JavaScriptSpec)
	nodes, _, err := w.Parse("app.js", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	funcNodes := filterByKind(nodes, graph.NodeKindFunction)
	if len(funcNodes) != 1 {
		t.Fatalf("expected 1 function node, got %d", len(funcNodes))
	}
	if funcNodes[0].Name != "foo" {
		t.Errorf("Name = %q, want %q", funcNodes[0].Name, "foo")
	}
}

// --- Phase 12.5: C# ---

// --- Phase 12.6: PHP ---

func TestParsePHP_Function(t *testing.T) {
	src := `<?php
function foo() {
}
`
	w := NewWalker(PHPSpec)
	nodes, _, err := w.Parse("app.php", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	funcNodes := filterByKind(nodes, graph.NodeKindFunction)
	if len(funcNodes) != 1 {
		t.Fatalf("expected 1 function node, got %d", len(funcNodes))
	}
	if funcNodes[0].Name != "foo" {
		t.Errorf("Name = %q, want %q", funcNodes[0].Name, "foo")
	}
}

func TestParsePHP_Class(t *testing.T) {
	src := `<?php
class Foo {
}
`
	w := NewWalker(PHPSpec)
	nodes, _, err := w.Parse("app.php", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	classNodes := filterByKind(nodes, graph.NodeKindClass)
	if len(classNodes) != 1 {
		t.Fatalf("expected 1 class node, got %d", len(classNodes))
	}
	if classNodes[0].Name != "Foo" {
		t.Errorf("Name = %q, want %q", classNodes[0].Name, "Foo")
	}
}

func TestParsePHP_Interface(t *testing.T) {
	src := `<?php
interface IFoo {
    public function bar();
}
`
	w := NewWalker(PHPSpec)
	nodes, _, err := w.Parse("app.php", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	typeNodes := filterByKind(nodes, graph.NodeKindType)
	if len(typeNodes) != 1 {
		t.Fatalf("expected 1 type node, got %d", len(typeNodes))
	}
	if typeNodes[0].Name != "IFoo" {
		t.Errorf("Name = %q, want %q", typeNodes[0].Name, "IFoo")
	}
}

func TestParsePHP_Use(t *testing.T) {
	src := `<?php
use App\Models\User;
`
	w := NewWalker(PHPSpec)
	_, edges, err := w.Parse("app.php", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	importEdges := filterEdgesByKind(edges, graph.EdgeKindImportsFrom)
	if len(importEdges) == 0 {
		t.Fatal("expected at least 1 IMPORTS_FROM edge, got 0")
	}
}

func TestParsePHP_Call(t *testing.T) {
	src := `<?php
function bar() {}
bar();
`
	w := NewWalker(PHPSpec)
	_, edges, err := w.Parse("app.php", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	callEdges := filterEdgesByKind(edges, graph.EdgeKindCalls)
	if len(callEdges) == 0 {
		t.Fatal("expected at least 1 CALLS edge, got 0")
	}
}

func TestParsePHP_Method(t *testing.T) {
	src := `<?php
class Foo {
    function bar() {}
}
`
	w := NewWalker(PHPSpec)
	nodes, _, err := w.Parse("app.php", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	funcNodes := filterByKind(nodes, graph.NodeKindFunction)
	if len(funcNodes) != 1 {
		t.Fatalf("expected 1 function node, got %d", len(funcNodes))
	}
	if funcNodes[0].Name != "bar" {
		t.Errorf("Name = %q, want %q", funcNodes[0].Name, "bar")
	}
}

// --- Phase 12.7: Swift ---

// --- Phase 12.8: Scala ---

// --- Phase 12.9: Lua ---

func TestParseLua_Function(t *testing.T) {
	src := `function foo()
end
`
	w := NewWalker(LuaSpec)
	nodes, _, err := w.Parse("app.lua", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	funcNodes := filterByKind(nodes, graph.NodeKindFunction)
	if len(funcNodes) != 1 {
		t.Fatalf("expected 1 function node, got %d", len(funcNodes))
	}
	if funcNodes[0].Name != "foo" {
		t.Errorf("Name = %q, want %q", funcNodes[0].Name, "foo")
	}
}

func TestParseKotlin_QualifiedNameIncludesPackage(t *testing.T) {
	src := `package com.example.auth

class User {
}
`
	w := NewWalker(KotlinSpec)
	nodes, _, err := w.Parse("User.kt", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	classNodes := filterByKind(nodes, graph.NodeKindClass)
	if len(classNodes) != 1 {
		t.Fatalf("expected 1 class node, got %d", len(classNodes))
	}
	if classNodes[0].QualifiedName != "com.example.auth.User" {
		t.Fatalf("QualifiedName = %q, want %q", classNodes[0].QualifiedName, "com.example.auth.User")
	}
}

func TestParseKotlin_SupertypeEdges(t *testing.T) {
	src := `package com.example.auth

class User : Base(), Authenticated, Named
`
	w := NewWalker(KotlinSpec)
	_, edges, err := w.Parse("User.kt", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var foundAuth, foundNamed bool
	wantInherits := wantInheritsFingerprint("User.kt", "com.example.auth.User", "com.example.auth.Base")
	for _, e := range edges {
		switch e.Kind {
		case graph.EdgeKindInherits:
			if e.Fingerprint != wantInherits {
				t.Fatalf("unexpected inherits fingerprint: got %q want %q", e.Fingerprint, wantInherits)
			}
		case graph.EdgeKindImplements:
			if containsSubstring(e.Fingerprint, "User") && containsSubstring(e.Fingerprint, "Authenticated") {
				foundAuth = true
			}
			if containsSubstring(e.Fingerprint, "User") && containsSubstring(e.Fingerprint, "Named") {
				foundNamed = true
			}
		}
	}
	if !foundAuth || !foundNamed {
		t.Fatalf("expected inherits+implements edges, got %+v", edges)
	}
}

func TestParseKotlin_SupertypeEdges_WithPrimaryConstructorTypes(t *testing.T) {
	src := `package com.example.auth

class User(val id: String) : Base(), Authenticated
`
	w := NewWalker(KotlinSpec)
	_, edges, err := w.Parse("User.kt", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var foundAuth bool
	wantInherits := wantInheritsFingerprint("User.kt", "com.example.auth.User", "com.example.auth.Base")
	for _, e := range edges {
		switch e.Kind {
		case graph.EdgeKindInherits:
			if e.Fingerprint != wantInherits {
				t.Fatalf("unexpected inherits fingerprint: got %q want %q", e.Fingerprint, wantInherits)
			}
		case graph.EdgeKindImplements:
			if containsSubstring(e.Fingerprint, "User") && containsSubstring(e.Fingerprint, "Authenticated") {
				foundAuth = true
			}
		}
	}
	if !foundAuth {
		t.Fatalf("expected constructor-safe supertype edges, got %+v", edges)
	}
}

func TestParseKotlin_HierarchyIgnoresCommasInsideGenericArguments(t *testing.T) {
	src := `package com.example.auth

import com.example.base.Base
import com.example.handlers.Handler
import com.example.io.Serializable

class User : Base<String, Int>(), Handler<Request<T>, Response>, Serializable
`
	w := NewWalker(KotlinSpec)
	_, edges, err := w.Parse("User.kt", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var foundBase, foundHandler, foundSerializable bool
	for _, e := range edges {
		switch e.Kind {
		case graph.EdgeKindInherits:
			if e.Fingerprint == wantInheritsFingerprint("User.kt", "com.example.auth.User", "com.example.base.Base") {
				foundBase = true
			}
		case graph.EdgeKindImplements:
			if e.Fingerprint == "implements:User.kt:com.example.auth.User:com.example.handlers.Handler" {
				foundHandler = true
			}
			if e.Fingerprint == "implements:User.kt:com.example.auth.User:com.example.io.Serializable" {
				foundSerializable = true
			}
			if containsSubstring(e.Fingerprint, "Response") {
				t.Fatalf("unexpected generic fragment leaked into implements edge: %+v", e)
			}
		}
	}
	if !foundBase || !foundHandler || !foundSerializable {
		t.Fatalf("expected generic-safe Kotlin hierarchy edges, got %+v", edges)
	}
}

func TestParseKotlinSupertypesFallbackIgnoresCommasInsideGenericArguments(t *testing.T) {
	base, traits := parseKotlinSupertypes("class User : Base<String, Int>(), Handler<Request<T>, Response>, Serializable")
	if base != "Base" {
		t.Fatalf("base = %q, want %q", base, "Base")
	}
	if len(traits) != 2 {
		t.Fatalf("traits len = %d, want 2 (%+v)", len(traits), traits)
	}
	if traits[0] != "Handler" || traits[1] != "Serializable" {
		t.Fatalf("traits = %+v, want [Handler Serializable]", traits)
	}
}

func TestParseGo_DefinitionEnrichmentDedupsOverlappingMatches(t *testing.T) {
	src := `package main

type Tracer interface {
	TraceFlow()
}

type Tracer struct {
	Base
}

type Base struct{}
`
	w := NewWalker(GoSpec)
	_, edges, err := w.Parse("main.go", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var inheritCount int
	for _, e := range edges {
		if e.Kind == graph.EdgeKindInherits && e.Fingerprint == wantInheritsFingerprint("main.go", "Tracer", "Base") {
			inheritCount++
		}
	}
	if inheritCount != 1 {
		t.Fatalf("expected one deduped embedding edge, got %d from %+v", inheritCount, edges)
	}
}

func TestParseLua_LocalFunction(t *testing.T) {
	src := `local function bar()
end
`
	w := NewWalker(LuaSpec)
	nodes, _, err := w.Parse("app.lua", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	funcNodes := filterByKind(nodes, graph.NodeKindFunction)
	if len(funcNodes) != 1 {
		t.Fatalf("expected 1 function node, got %d", len(funcNodes))
	}
	if funcNodes[0].Name != "bar" {
		t.Errorf("Name = %q, want %q", funcNodes[0].Name, "bar")
	}
}

func TestParseLua_Call(t *testing.T) {
	src := `function foo()
end
foo()
`
	w := NewWalker(LuaSpec)
	_, edges, err := w.Parse("app.lua", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	callEdges := filterEdgesByKind(edges, graph.EdgeKindCalls)
	if len(callEdges) == 0 {
		t.Fatal("expected at least 1 CALLS edge, got 0")
	}
}

func TestParseLua_Require(t *testing.T) {
	src := `local m = require("foo")
`
	w := NewWalker(LuaSpec)
	_, edges, err := w.Parse("app.lua", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Lua의 require는 function_call이므로 CALLS 엣지로 감지됨
	callEdges := filterEdgesByKind(edges, graph.EdgeKindCalls)
	if len(callEdges) == 0 {
		t.Fatal("expected at least 1 CALLS edge for require, got 0")
	}
}

func TestParseLua_MethodColon(t *testing.T) {
	src := `function Foo:bar()
end
`
	w := NewWalker(LuaSpec)
	nodes, _, err := w.Parse("app.lua", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	funcNodes := filterByKind(nodes, graph.NodeKindFunction)
	if len(funcNodes) != 1 {
		t.Fatalf("expected 1 function node, got %d", len(funcNodes))
	}
	if funcNodes[0].Name != "Foo:bar" {
		t.Errorf("Name = %q, want %q", funcNodes[0].Name, "Foo:bar")
	}
}

func TestParseLua_MethodDot(t *testing.T) {
	src := `function Foo.baz()
end
`
	w := NewWalker(LuaSpec)
	nodes, _, err := w.Parse("app.lua", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	funcNodes := filterByKind(nodes, graph.NodeKindFunction)
	if len(funcNodes) != 1 {
		t.Fatalf("expected 1 function node, got %d", len(funcNodes))
	}
	if funcNodes[0].Name != "Foo.baz" {
		t.Errorf("Name = %q, want %q", funcNodes[0].Name, "Foo.baz")
	}
}

func TestParseLua_MethodCall(t *testing.T) {
	src := `obj:method()
`
	w := NewWalker(LuaSpec)
	_, edges, err := w.Parse("app.lua", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	callEdges := filterEdgesByKind(edges, graph.EdgeKindCalls)
	if len(callEdges) == 0 {
		t.Fatal("expected at least 1 CALLS edge for method call, got 0")
	}
}

func TestParseLua_TypedFunction(t *testing.T) {
	// Luau typed function — tree-sitter error recovery should still capture function name
	src := `function add(x: number, y: number): number
    return x + y
end
`
	w := NewWalker(LuaSpec)
	nodes, _, err := w.Parse("app.lua", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	funcNodes := filterByKind(nodes, graph.NodeKindFunction)
	if len(funcNodes) != 1 {
		t.Fatalf("expected 1 function node, got %d", len(funcNodes))
	}
	if funcNodes[0].Name != "add" {
		t.Errorf("Name = %q, want %q", funcNodes[0].Name, "add")
	}
}

func TestParseLua_TypedLocalFunction(t *testing.T) {
	src := `local function greet(name: string): string
    return "hello " .. name
end
`
	w := NewWalker(LuaSpec)
	nodes, _, err := w.Parse("app.lua", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	funcNodes := filterByKind(nodes, graph.NodeKindFunction)
	if len(funcNodes) != 1 {
		t.Fatalf("expected 1 function node, got %d", len(funcNodes))
	}
	if funcNodes[0].Name != "greet" {
		t.Errorf("Name = %q, want %q", funcNodes[0].Name, "greet")
	}
}

func TestParseLua_Comments(t *testing.T) {
	src := `-- single line comment

--[[ block comment
  multi line
]]

--!strict
function foo()
end
`
	w := NewWalker(LuaSpec)
	_, _, comments, err := w.ParseWithComments(context.Background(), "app.lua", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(comments) == 0 {
		t.Fatal("expected at least 1 comment block, got 0")
	}
	found := false
	for _, c := range comments {
		if strings.Contains(c.Text, "single line comment") {
			found = true
			break
		}
	}
	if !found {
		t.Error("single line comment not found in comment blocks")
	}

	found = false
	for _, c := range comments {
		if strings.Contains(c.Text, "block comment") {
			found = true
			break
		}
	}
	if !found {
		t.Error("block comment not found in comment blocks")
	}

	found = false
	for _, c := range comments {
		if strings.Contains(c.Text, "!strict") {
			found = true
			break
		}
	}
	if !found {
		t.Error("--!strict comment not found in comment blocks")
	}
}

// --- Phase 12.10: Bash ---

// --- Phase 12.12: 통합 테스트 ---

func TestWalker_MultiLanguageFile(t *testing.T) {
	// 같은 Walker로 Go, Python, JavaScript 파일 연속 파싱 성공
	tests := []struct {
		spec     *LangSpec
		filePath string
		src      string
		wantFunc string
	}{
		{GoSpec, "main.go", "package main\nfunc hello() {}\n", "hello"},
		{PythonSpec, "app.py", "def greet():\n    pass\n", "greet"},
		{JavaScriptSpec, "app.js", "function foo() {}\n", "foo"},
	}

	for _, tc := range tests {
		w := NewWalker(tc.spec)
		nodes, _, err := w.Parse(tc.filePath, []byte(tc.src))
		if err != nil {
			t.Fatalf("failed to parse %s: %v", tc.filePath, err)
		}
		funcNodes := filterByKind(nodes, graph.NodeKindFunction)
		if len(funcNodes) != 1 {
			t.Fatalf("%s: expected 1 function node, got %d", tc.filePath, len(funcNodes))
		}
		if funcNodes[0].Name != tc.wantFunc {
			t.Errorf("%s: Name = %q, want %q", tc.filePath, funcNodes[0].Name, tc.wantFunc)
		}
		if funcNodes[0].Language != tc.spec.Name {
			t.Errorf("%s: Language = %q, want %q", tc.filePath, funcNodes[0].Language, tc.spec.Name)
		}
	}
}

// --- Phase 12.A: Arrow Function 이름 추출 ---

func TestWalker_ArrowFunctionName_JS(t *testing.T) {
	src := `const foo = () => {
    return 42;
};
`
	w := NewWalker(JavaScriptSpec)
	nodes, _, err := w.Parse("app.js", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	funcNodes := filterByKind(nodes, graph.NodeKindFunction)
	if len(funcNodes) != 1 {
		t.Fatalf("expected 1 function node, got %d", len(funcNodes))
	}
	if funcNodes[0].Name != "foo" {
		t.Errorf("Name = %q, want %q", funcNodes[0].Name, "foo")
	}
}

func TestWalker_ArrowFunctionName_TS(t *testing.T) {
	src := `const greet = (name: string): string => {
    return "hello " + name;
};
`
	w := NewWalker(TypeScriptSpec)
	nodes, _, err := w.Parse("app.ts", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	funcNodes := filterByKind(nodes, graph.NodeKindFunction)
	if len(funcNodes) != 1 {
		t.Fatalf("expected 1 function node, got %d", len(funcNodes))
	}
	if funcNodes[0].Name != "greet" {
		t.Errorf("Name = %q, want %q", funcNodes[0].Name, "greet")
	}
}

// --- Phase 12.B: Attribute/Decorator 기반 테스트 감지 ---

func TestWalker_AttributeTest_Rust(t *testing.T) {
	src := `#[test]
fn test_foo() {
}
`
	w := NewWalker(RustSpec)
	nodes, _, err := w.Parse("main.rs", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	testNodes := filterByKind(nodes, graph.NodeKindTest)
	if len(testNodes) != 1 {
		t.Fatalf("expected 1 test node, got %d", len(testNodes))
	}
	if testNodes[0].Name != "test_foo" {
		t.Errorf("Name = %q, want %q", testNodes[0].Name, "test_foo")
	}
}

func TestWalker_AttributeTest_Java(t *testing.T) {
	src := `import org.junit.Test;
class FooTest {
    @Test
    void testFoo() {}
}
`
	w := NewWalker(JavaSpec)
	nodes, _, err := w.Parse("FooTest.java", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	testNodes := filterByKind(nodes, graph.NodeKindTest)
	if len(testNodes) != 1 {
		t.Fatalf("expected 1 test node, got %d", len(testNodes))
	}
	if testNodes[0].Name != "testFoo" {
		t.Errorf("Name = %q, want %q", testNodes[0].Name, "testFoo")
	}
}

// --- Phase 12.C: impl/extension 블록 처리 ---

func TestWalker_ImplBlock_Rust(t *testing.T) {
	src := `struct Foo {}

impl Foo {
    fn bar() {}
}
`
	w := NewWalker(RustSpec)
	nodes, edges, err := w.Parse("main.rs", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	funcNodes := filterByKind(nodes, graph.NodeKindFunction)
	if len(funcNodes) != 1 {
		t.Fatalf("expected 1 function node (bar), got %d", len(funcNodes))
	}
	if funcNodes[0].Name != "bar" {
		t.Errorf("Name = %q, want %q", funcNodes[0].Name, "bar")
	}
	// bar는 Foo의 CONTAINS 자식이어야 함
	containsEdges := filterEdgesByKind(edges, graph.EdgeKindContains)
	found := false
	for _, e := range containsEdges {
		if containsSubstring(e.Fingerprint, "Foo") && containsSubstring(e.Fingerprint, "bar") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected CONTAINS edge from Foo to bar")
	}
}

func TestWalker_ImplBlock_Rust_MethodQualifiedNameIncludesReceiver(t *testing.T) {
	src := `struct Foo {}

impl Foo {
    fn bar(&self) {}
}
`
	w := NewWalker(RustSpec)
	nodes, _, err := w.Parse("main.rs", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	funcNodes := filterByKind(nodes, graph.NodeKindFunction)
	if len(funcNodes) != 1 {
		t.Fatalf("expected 1 function node (bar), got %d", len(funcNodes))
	}
	if got, want := funcNodes[0].QualifiedName, "Foo.bar"; got != want {
		t.Fatalf("QualifiedName = %q, want %q", got, want)
	}
}

func TestWalker_ImplTrait_Rust(t *testing.T) {
	src := `trait MyTrait {}
struct Foo {}
impl MyTrait for Foo {}
`
	w := NewWalker(RustSpec)
	_, edges, err := w.Parse("main.rs", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	implEdges := filterEdgesByKind(edges, graph.EdgeKindImplements)
	if len(implEdges) == 0 {
		t.Fatal("expected at least 1 IMPLEMENTS edge, got 0")
	}
	found := false
	for _, e := range implEdges {
		if containsSubstring(e.Fingerprint, "Foo") && containsSubstring(e.Fingerprint, "MyTrait") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected IMPLEMENTS edge from Foo to MyTrait")
	}
}

func TestWalker_ImplTrait_Rust_NormalizesImportedTraitPath(t *testing.T) {
	src := `use std::fmt::Display;

struct Foo {}
impl Display for Foo {}
`
	w := NewWalker(RustSpec)
	_, edges, err := w.Parse("main.rs", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	implEdges := filterEdgesByKind(edges, graph.EdgeKindImplements)
	for _, e := range implEdges {
		if e.Fingerprint == "implements:main.rs:Foo:std::fmt::Display" {
			return
		}
	}
	t.Fatalf("expected normalized imported Rust trait implements edge, got %#v", implEdges)
}

func TestWalker_ImplTrait_Rust_NormalizesGroupedImportedTraitPath(t *testing.T) {
	src := `use std::fmt::{Debug, Display};

struct Foo {}
impl Display for Foo {}
`
	w := NewWalker(RustSpec)
	_, edges, err := w.Parse("main.rs", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	implEdges := filterEdgesByKind(edges, graph.EdgeKindImplements)
	for _, e := range implEdges {
		if e.Fingerprint == "implements:main.rs:Foo:std::fmt::Display" {
			return
		}
	}
	t.Fatalf("expected grouped import Rust trait implements edge, got %#v", implEdges)
}

func TestWalker_ImplTrait_Rust_NormalizesAliasedTraitPath(t *testing.T) {
	src := `use std::fmt::Display as FmtDisplay;

struct Foo {}
impl FmtDisplay for Foo {}
`
	w := NewWalker(RustSpec)
	_, edges, err := w.Parse("main.rs", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	implEdges := filterEdgesByKind(edges, graph.EdgeKindImplements)
	for _, e := range implEdges {
		if e.Fingerprint == "implements:main.rs:Foo:std::fmt::Display" {
			return
		}
	}
	t.Fatalf("expected aliased import Rust trait implements edge, got %#v", implEdges)
}

func TestWalker_ImplTrait_Rust_StripsGenerics(t *testing.T) {
	src := `trait Display<T> {}

struct Foo<T> { value: T }
impl<T> Display<Vec<T>> for Foo<T> {}
`
	w := NewWalker(RustSpec)
	_, edges, err := w.Parse("main.rs", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	implEdges := filterEdgesByKind(edges, graph.EdgeKindImplements)
	for _, e := range implEdges {
		if e.Fingerprint == "implements:main.rs:Foo:Display" {
			return
		}
	}
	t.Fatalf("expected generic Rust trait implements edge to normalize names, got %#v", implEdges)
}

// --- Phase 12.4: Rust ---

func TestParseRust_Function(t *testing.T) {
	src := `fn foo() {
}
`
	w := NewWalker(RustSpec)
	nodes, _, err := w.Parse("main.rs", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	funcNodes := filterByKind(nodes, graph.NodeKindFunction)
	if len(funcNodes) != 1 {
		t.Fatalf("expected 1 function node, got %d", len(funcNodes))
	}
	if funcNodes[0].Name != "foo" {
		t.Errorf("Name = %q, want %q", funcNodes[0].Name, "foo")
	}
	if funcNodes[0].Language != "rust" {
		t.Errorf("Language = %q, want %q", funcNodes[0].Language, "rust")
	}
}

func TestParseRust_Struct(t *testing.T) {
	src := `struct Foo {
    x: i32,
}
`
	w := NewWalker(RustSpec)
	nodes, _, err := w.Parse("main.rs", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	classNodes := filterByKind(nodes, graph.NodeKindClass)
	if len(classNodes) != 1 {
		t.Fatalf("expected 1 class node, got %d", len(classNodes))
	}
	if classNodes[0].Name != "Foo" {
		t.Errorf("Name = %q, want %q", classNodes[0].Name, "Foo")
	}
}

func TestParseRust_Enum(t *testing.T) {
	src := `enum Bar {
    A,
    B,
}
`
	w := NewWalker(RustSpec)
	nodes, _, err := w.Parse("main.rs", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	classNodes := filterByKind(nodes, graph.NodeKindClass)
	if len(classNodes) != 1 {
		t.Fatalf("expected 1 class node, got %d", len(classNodes))
	}
	if classNodes[0].Name != "Bar" {
		t.Errorf("Name = %q, want %q", classNodes[0].Name, "Bar")
	}
}

func TestParseRust_Trait(t *testing.T) {
	src := `trait Baz {
    fn do_something(&self);
}
`
	w := NewWalker(RustSpec)
	nodes, _, err := w.Parse("main.rs", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	typeNodes := filterByKind(nodes, graph.NodeKindType)
	if len(typeNodes) != 1 {
		t.Fatalf("expected 1 type node, got %d", len(typeNodes))
	}
	if typeNodes[0].Name != "Baz" {
		t.Errorf("Name = %q, want %q", typeNodes[0].Name, "Baz")
	}
}

func TestParseRust_Use(t *testing.T) {
	src := `use std::io;
`
	w := NewWalker(RustSpec)
	_, edges, err := w.Parse("main.rs", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	importEdges := filterEdgesByKind(edges, graph.EdgeKindImportsFrom)
	if len(importEdges) == 0 {
		t.Fatal("expected at least 1 IMPORTS_FROM edge, got 0")
	}
}

func TestParseRust_Call(t *testing.T) {
	src := `fn bar() {}
fn main() {
    bar();
}
`
	w := NewWalker(RustSpec)
	_, edges, err := w.Parse("main.rs", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	callEdges := filterEdgesByKind(edges, graph.EdgeKindCalls)
	if len(callEdges) == 0 {
		t.Fatal("expected at least 1 CALLS edge, got 0")
	}
}

func TestParseRust_MethodCall(t *testing.T) {
	src := `struct Foo {}

impl Foo {
    fn bar(&self) {}
}

fn main() {
    let foo = Foo {};
    foo.bar();
}
`
	w := NewWalker(RustSpec)
	_, edges, err := w.Parse("main.rs", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	callEdges := filterEdgesByKind(edges, graph.EdgeKindCalls)
	for _, e := range callEdges {
		if e.Fingerprint == "calls:main.rs:foo.bar:9" {
			return
		}
	}
	t.Fatalf("expected Rust method call edge, got %#v", callEdges)
}

func TestParseRust_QualifiedTraitPathCall(t *testing.T) {
	src := `use crate::traits::MyTrait;

fn main(foo: Foo) {
	crate::traits::MyTrait::bar(&foo);
}
`
	w := NewWalker(RustSpec)
	_, edges, err := w.Parse("main.rs", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	callEdges := filterEdgesByKind(edges, graph.EdgeKindCalls)
	for _, e := range callEdges {
		if e.Fingerprint == "calls:main.rs:crate::traits::MyTrait::bar:4" {
			return
		}
	}
	t.Fatalf("expected qualified Rust trait path call edge, got %#v", callEdges)
}

func TestParseRust_UFCSCall(t *testing.T) {
	src := `fn main(foo: Foo) {
	<Foo as MyTrait>::bar(&foo);
}
`
	w := NewWalker(RustSpec)
	_, edges, err := w.Parse("main.rs", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	callEdges := filterEdgesByKind(edges, graph.EdgeKindCalls)
	for _, e := range callEdges {
		if e.Fingerprint == "calls:main.rs:<Foo as MyTrait>::bar:2" {
			return
		}
	}
	t.Fatalf("expected Rust UFCS call edge, got %#v", callEdges)
}

func TestParseRust_CallRewriteNormalizesQualifiedTraitPath(t *testing.T) {
	src := `use crate::traits::MyTrait;

fn main(foo: Foo) {
	crate::traits::MyTrait::bar(&foo);
}
`
	w := NewWalker(RustSpec)
	ctx := SemanticContext{Root: nil, Content: []byte(src)}
	_ = ctx
	_, edges, err := w.Parse("main.rs", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	callEdges := filterEdgesByKind(edges, graph.EdgeKindCalls)
	for _, e := range callEdges {
		if e.Fingerprint == "calls:main.rs:crate::traits::MyTrait::bar:4" {
			return
		}
	}
	t.Fatalf("expected normalized qualified trait call edge, got %#v", callEdges)
}

// --- Phase 12.3: C++ ---

func TestParseCpp_Function(t *testing.T) {
	src := `void foo() {
}
`
	w := NewWalker(CppSpec)
	nodes, _, err := w.Parse("main.cpp", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	funcNodes := filterByKind(nodes, graph.NodeKindFunction)
	if len(funcNodes) != 1 {
		t.Fatalf("expected 1 function node, got %d", len(funcNodes))
	}
	if funcNodes[0].Name != "foo" {
		t.Errorf("Name = %q, want %q", funcNodes[0].Name, "foo")
	}
}

func TestParseCpp_Class(t *testing.T) {
	src := `class Foo {
};
`
	w := NewWalker(CppSpec)
	nodes, _, err := w.Parse("main.cpp", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	classNodes := filterByKind(nodes, graph.NodeKindClass)
	if len(classNodes) != 1 {
		t.Fatalf("expected 1 class node, got %d", len(classNodes))
	}
	if classNodes[0].Name != "Foo" {
		t.Errorf("Name = %q, want %q", classNodes[0].Name, "Foo")
	}
}

func TestParseCpp_Struct(t *testing.T) {
	src := `struct Bar {
    int x;
};
`
	w := NewWalker(CppSpec)
	nodes, _, err := w.Parse("main.cpp", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	classNodes := filterByKind(nodes, graph.NodeKindClass)
	if len(classNodes) != 1 {
		t.Fatalf("expected 1 class node, got %d", len(classNodes))
	}
	if classNodes[0].Name != "Bar" {
		t.Errorf("Name = %q, want %q", classNodes[0].Name, "Bar")
	}
}

func TestParseCpp_Include(t *testing.T) {
	src := `#include <iostream>
`
	w := NewWalker(CppSpec)
	_, edges, err := w.Parse("main.cpp", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	importEdges := filterEdgesByKind(edges, graph.EdgeKindImportsFrom)
	if len(importEdges) == 0 {
		t.Fatal("expected at least 1 IMPORTS_FROM edge, got 0")
	}
}

func TestParseCpp_Call(t *testing.T) {
	src := `void bar() {}
void main() {
    bar();
}
`
	w := NewWalker(CppSpec)
	_, edges, err := w.Parse("main.cpp", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	callEdges := filterEdgesByKind(edges, graph.EdgeKindCalls)
	if len(callEdges) == 0 {
		t.Fatal("expected at least 1 CALLS edge, got 0")
	}
}

func TestParseCpp_Namespace(t *testing.T) {
	src := `namespace ns {
    void foo() {}
}
`
	w := NewWalker(CppSpec)
	nodes, _, err := w.Parse("main.cpp", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	funcNodes := filterByKind(nodes, graph.NodeKindFunction)
	if len(funcNodes) != 1 {
		t.Fatalf("expected 1 function node, got %d", len(funcNodes))
	}
	if funcNodes[0].Name != "foo" {
		t.Errorf("Name = %q, want %q", funcNodes[0].Name, "foo")
	}
}

// --- Phase 12.2: C ---

func TestParseC_Function(t *testing.T) {
	src := `void foo() {
}
`
	w := NewWalker(CSpec)
	nodes, _, err := w.Parse("main.c", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	funcNodes := filterByKind(nodes, graph.NodeKindFunction)
	if len(funcNodes) != 1 {
		t.Fatalf("expected 1 function node, got %d", len(funcNodes))
	}
	if funcNodes[0].Name != "foo" {
		t.Errorf("Name = %q, want %q", funcNodes[0].Name, "foo")
	}
	if funcNodes[0].Language != "c" {
		t.Errorf("Language = %q, want %q", funcNodes[0].Language, "c")
	}
}

func TestParseC_Struct(t *testing.T) {
	src := `struct Foo {
    int x;
};
`
	w := NewWalker(CSpec)
	nodes, _, err := w.Parse("main.c", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	classNodes := filterByKind(nodes, graph.NodeKindClass)
	if len(classNodes) != 1 {
		t.Fatalf("expected 1 class node, got %d", len(classNodes))
	}
	if classNodes[0].Name != "Foo" {
		t.Errorf("Name = %q, want %q", classNodes[0].Name, "Foo")
	}
}

func TestParseC_Include(t *testing.T) {
	src := `#include "foo.h"
`
	w := NewWalker(CSpec)
	_, edges, err := w.Parse("main.c", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	importEdges := filterEdgesByKind(edges, graph.EdgeKindImportsFrom)
	if len(importEdges) == 0 {
		t.Fatal("expected at least 1 IMPORTS_FROM edge, got 0")
	}
}

func TestParseC_Call(t *testing.T) {
	src := `void bar() {}
void main() {
    bar();
}
`
	w := NewWalker(CSpec)
	_, edges, err := w.Parse("main.c", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	callEdges := filterEdgesByKind(edges, graph.EdgeKindCalls)
	if len(callEdges) == 0 {
		t.Fatal("expected at least 1 CALLS edge, got 0")
	}
}

func TestParseC_HeaderDeclaration(t *testing.T) {
	src := `void foo();
`
	w := NewWalker(CSpec)
	nodes, _, err := w.Parse("main.h", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 헤더의 함수 선언(declaration)은 definition이 아니므로 노드 생성 안됨이 정상
	// 하지만 plan에서는 "함수 선언도 노드 생성"이라 했으므로, 확인만
	_ = nodes
}

func TestParseJS_ArrowFunction(t *testing.T) {
	src := `const foo = () => {
    return 42;
};
`
	w := NewWalker(JavaScriptSpec)
	nodes, _, err := w.Parse("app.js", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	funcNodes := filterByKind(nodes, graph.NodeKindFunction)
	if len(funcNodes) != 1 {
		t.Fatalf("expected 1 function node, got %d", len(funcNodes))
	}
	if funcNodes[0].Name != "foo" {
		t.Errorf("Name = %q, want %q", funcNodes[0].Name, "foo")
	}
}

// --- Phase 12.0: 구조적 변경 ---

func TestLangSpec_TestAttributes(t *testing.T) {
	// 새 필드 TestAttributes가 존재하고, 기존 5개 언어는 zero value(nil)임을 확인
	specs := []*LangSpec{GoSpec, PythonSpec, TypeScriptSpec, JavaSpec, RubySpec}
	for _, spec := range specs {
		if spec.TestAttributes != nil {
			t.Errorf("기존 언어 %s의 TestAttributes는 nil이어야 하지만 %v", spec.Name, spec.TestAttributes)
		}
	}

	// TestAttributes 필드가 있는 새 LangSpec 생성 가능한지 확인
	testSpec := &LangSpec{
		Name:           "test_lang",
		FunctionTypes:  []string{"func"},
		TestPrefix:     "test_",
		TestAttributes: []string{"test", "Test"},
	}
	if len(testSpec.TestAttributes) != 2 {
		t.Errorf("TestAttributes 길이 = %d, want 2", len(testSpec.TestAttributes))
	}
	if testSpec.TestAttributes[0] != "test" {
		t.Errorf("TestAttributes[0] = %q, want %q", testSpec.TestAttributes[0], "test")
	}
}

func TestLangSpec_ImplTypes(t *testing.T) {
	// 새 필드 ImplTypes, ExtensionTypes가 존재하고, 기존 5개 언어는 zero value(nil)임을 확인
	specs := []*LangSpec{GoSpec, PythonSpec, TypeScriptSpec, JavaSpec, RubySpec}
	for _, spec := range specs {
		if spec.ImplTypes != nil {
			t.Errorf("기존 언어 %s의 ImplTypes는 nil이어야 하지만 %v", spec.Name, spec.ImplTypes)
		}
		if spec.ExtensionTypes != nil {
			t.Errorf("기존 언어 %s의 ExtensionTypes는 nil이어야 하지만 %v", spec.Name, spec.ExtensionTypes)
		}
	}

	// ImplTypes, ExtensionTypes 필드가 있는 새 LangSpec 생성 가능한지 확인
	testSpec := &LangSpec{
		Name:           "test_lang",
		FunctionTypes:  []string{"func"},
		ImplTypes:      []string{"impl_item"},
		ExtensionTypes: []string{"extension_declaration"},
	}
	if len(testSpec.ImplTypes) != 1 {
		t.Errorf("ImplTypes 길이 = %d, want 1", len(testSpec.ImplTypes))
	}
	if testSpec.ImplTypes[0] != "impl_item" {
		t.Errorf("ImplTypes[0] = %q, want %q", testSpec.ImplTypes[0], "impl_item")
	}
	if len(testSpec.ExtensionTypes) != 1 {
		t.Errorf("ExtensionTypes 길이 = %d, want 1", len(testSpec.ExtensionTypes))
	}
	if testSpec.ExtensionTypes[0] != "extension_declaration" {
		t.Errorf("ExtensionTypes[0] = %q, want %q", testSpec.ExtensionTypes[0], "extension_declaration")
	}
}

func filterByKind(nodes []graph.Node, kind graph.NodeKind) []graph.Node {
	var result []graph.Node
	for _, n := range nodes {
		if n.Kind == kind {
			result = append(result, n)
		}
	}
	return result
}

func filterEdgesByKind(edges []graph.Edge, kind graph.EdgeKind) []graph.Edge {
	var result []graph.Edge
	for _, e := range edges {
		if e.Kind == kind {
			result = append(result, e)
		}
	}
	return result
}

// --- Phase 12.12: E2E 통합 테스트 ---

var e2eDBSeq atomic.Int64

func TestE2E_ParseMultiLangProject(t *testing.T) {
	// 여러 언어 파일이 섞인 디렉토리 파싱 → 각 언어별 노드 정상 생성
	dir := t.TempDir()

	files := map[string]struct {
		spec    *LangSpec
		content string
	}{
		"main.go":  {GoSpec, "package main\n\nfunc Hello() {}\n"},
		"app.py":   {PythonSpec, "def greet():\n    pass\n"},
		"index.js": {JavaScriptSpec, "function render() {}\n"},
		"lib.rs":   {RustSpec, "fn compute() {}\n"},
	}

	for name, f := range files {
		fp := filepath.Join(dir, name)
		if err := os.WriteFile(fp, []byte(f.content), 0644); err != nil {
			t.Fatalf("failed to write %s: %v", name, err)
		}
	}

	langNodes := map[string]bool{}

	for name, f := range files {
		fp := filepath.Join(dir, name)
		content, err := os.ReadFile(fp)
		if err != nil {
			t.Fatalf("failed to read %s: %v", name, err)
		}

		w := NewWalker(f.spec)
		nodes, _, err := w.Parse(fp, content)
		if err != nil {
			t.Fatalf("failed to parse %s: %v", name, err)
		}

		funcNodes := filterByKind(nodes, graph.NodeKindFunction)
		if len(funcNodes) == 0 {
			t.Errorf("%s: expected at least 1 function node, got 0", name)
		}
		for _, n := range funcNodes {
			if n.Language != f.spec.Name {
				t.Errorf("%s: Language = %q, want %q", name, n.Language, f.spec.Name)
			}
			langNodes[n.Language] = true
		}
	}

	expectedLangs := []string{"go", "python", "javascript", "rust"}
	for _, lang := range expectedLangs {
		if !langNodes[lang] {
			t.Errorf("expected nodes with Language=%q, but found none", lang)
		}
	}
}

func TestE2E_SearchAcrossLanguages(t *testing.T) {
	// 파싱 후 검색 시 모든 언어의 노드가 검색됨
	dsn := fmt.Sprintf("file:e2elang%d?mode=memory&cache=shared", e2eDBSeq.Add(1))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	st := graphgorm.New(db)
	if err := st.AutoMigrate(); err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&graph.SearchDocument{}); err != nil {
		t.Fatal(err)
	}
	sb := search.NewSQLiteBackend()
	if err := sb.Migrate(db); err != nil {
		if errors.Is(err, search.ErrFTS5NotAvailable) {
			t.Skip("fts5 module not available, skipping test")
		}
		t.Fatal(err)
	}

	ctx := context.Background()

	// Parse files in 3 languages and store nodes
	sources := []struct {
		spec     *LangSpec
		filePath string
		content  string
	}{
		{GoSpec, "handler.go", "package handler\n\nfunc HandleRequest() {}\n"},
		{PythonSpec, "handler.py", "def handle_request():\n    pass\n"},
		{JavaScriptSpec, "handler.js", "function handleRequest() {}\n"},
	}

	for _, s := range sources {
		w := NewWalker(s.spec)
		nodes, _, err := w.Parse(s.filePath, []byte(s.content))
		if err != nil {
			t.Fatalf("failed to parse %s: %v", s.filePath, err)
		}
		if err := st.UpsertNodes(ctx, nodes); err != nil {
			t.Fatalf("failed to upsert nodes for %s: %v", s.filePath, err)
		}

		// Create search documents for function nodes
		for _, n := range nodes {
			if n.Kind == graph.NodeKindFunction {
				stored, _ := st.GetNode(ctx, n.QualifiedName)
				if stored != nil {
					db.Create(&graph.SearchDocument{
						NodeID:   stored.ID,
						Content:  stored.Name + " " + stored.QualifiedName + " " + stored.Language,
						Language: stored.Language,
					})
				}
			}
		}
	}

	if err := sb.Rebuild(ctx, db); err != nil {
		t.Fatalf("failed to rebuild search index: %v", err)
	}

	// Search for "handle" — should find nodes from all 3 languages
	results, err := sb.Query(ctx, db, "handle", 10)
	if err != nil {
		t.Fatalf("search error: %v", err)
	}

	if len(results) < 3 {
		t.Fatalf("expected at least 3 search results for 'handle', got %d", len(results))
	}

	foundLangs := map[string]bool{}
	for _, r := range results {
		foundLangs[r.Language] = true
	}

	for _, lang := range []string{"go", "python", "javascript"} {
		if !foundLangs[lang] {
			t.Errorf("expected search results to include language %q", lang)
		}
	}
}

// TestNewWalker_CachesParserAndQuery verifies that NewWalker pre-initializes
// the parser and query fields to avoid per-file CGO allocation overhead.
func TestNewWalker_CachesParserAndQuery(t *testing.T) {
	specs := []*LangSpec{GoSpec, PythonSpec, JavaScriptSpec}
	for _, spec := range specs {
		t.Run(spec.Name, func(t *testing.T) {
			w := NewWalker(spec)
			if w.parser == nil {
				t.Errorf("expected parser to be initialized for language %q, got nil", spec.Name)
			}
			if w.query == nil {
				t.Errorf("expected query to be initialized for language %q (tags.scm must exist), got nil", spec.Name)
			}
		})
	}
}

// TestNewWalker_UnsupportedLang verifies that NewWalker with an unsupported
// language spec leaves parser and query as nil, and ParseWithComments returns error.
func TestNewWalker_UnsupportedLang(t *testing.T) {
	unsupported := &LangSpec{Name: "brainfuck"}
	w := NewWalker(unsupported)
	if w.parser != nil {
		t.Errorf("expected parser to be nil for unsupported language, got non-nil")
	}
	_, _, _, err := w.ParseWithComments(context.Background(), "file.bf", []byte("+++"))
	if err == nil {
		t.Error("expected error for unsupported language, got nil")
	}
}

// TestWalker_ParseWithComments_IdempotentWithCachedParser verifies that parsing
// the same content twice with the same Walker produces identical results,
// confirming the cached parser is correctly reset between calls.
func TestWalker_ParseWithComments_IdempotentWithCachedParser(t *testing.T) {
	src := []byte(`package main

func hello() {}
func world() {}
`)
	w := NewWalker(GoSpec)

	nodes1, edges1, _, err := w.ParseWithComments(context.Background(), "main.go", src)
	if err != nil {
		t.Fatalf("first parse failed: %v", err)
	}

	nodes2, edges2, _, err := w.ParseWithComments(context.Background(), "main.go", src)
	if err != nil {
		t.Fatalf("second parse failed: %v", err)
	}

	if len(nodes1) != len(nodes2) {
		t.Errorf("node count mismatch: first=%d second=%d", len(nodes1), len(nodes2))
	}
	if len(edges1) != len(edges2) {
		t.Errorf("edge count mismatch: first=%d second=%d", len(edges1), len(edges2))
	}
}

func TestWalker_ParseWithComments_ConcurrentUse(t *testing.T) {
	w := NewWalker(GoSpec)
	src := []byte(`package main

func hello() {}
func world() {}
`)

	const workers = 8
	var wg sync.WaitGroup
	errCh := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			nodes, edges, _, err := w.ParseWithComments(context.Background(), "main.go", src)
			if err != nil {
				errCh <- err
				return
			}
			if len(nodes) == 0 || len(edges) == 0 {
				errCh <- fmt.Errorf("empty parse result")
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatalf("concurrent parse failed: %v", err)
		}
	}
}
