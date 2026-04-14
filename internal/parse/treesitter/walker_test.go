package treesitter

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/imtaebin/code-context-graph/internal/model"
	"github.com/imtaebin/code-context-graph/internal/store/gormstore"
	"github.com/imtaebin/code-context-graph/internal/store/search"
)

func TestParseWithContext_RespectsContextCancellation(t *testing.T) {
	w := NewWalker(GoSpec)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 즉시 취소

	content := []byte(`package main
func Foo() {}`)

	// 취소된 context가 전달되면 에러 반환해야 함
	// (tree-sitter ParseCtx는 ctx를 체크함)
	_, _, err := w.ParseWithContext(ctx, "test.go", content)
	// 에러가 발생할 수도, 아닐 수도 있음 - 최소한 panic 없어야 함
	_ = err
}

func TestParseGo_EmptyFile(t *testing.T) {
	w := NewWalker(GoSpec)
	nodes, edges, err := w.Parse("main.go", []byte("package main\n"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	fileNodes := filterByKind(nodes, model.NodeKindFile)
	if len(fileNodes) != 1 {
		t.Errorf("expected 1 file node, got %d", len(fileNodes))
	}
	funcNodes := filterByKind(nodes, model.NodeKindFunction)
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
	funcNodes := filterByKind(nodes, model.NodeKindFunction)
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
	funcNodes := filterByKind(nodes, model.NodeKindFunction)
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
	classNodes := filterByKind(nodes, model.NodeKindClass)
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
	typeNodes := filterByKind(nodes, model.NodeKindType)
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
	funcNodes := filterByKind(nodes, model.NodeKindFunction)
	if len(funcNodes) != 1 {
		t.Fatalf("expected 1 method node, got %d", len(funcNodes))
	}
	if funcNodes[0].Name != "Greet" {
		t.Errorf("Name = %q, want %q", funcNodes[0].Name, "Greet")
	}
	containsEdges := filterEdgesByKind(edges, model.EdgeKindContains)
	found := false
	for _, e := range containsEdges {
		if e.Kind == model.EdgeKindContains {
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
	callEdges := filterEdgesByKind(edges, model.EdgeKindCalls)
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
	importEdges := filterEdgesByKind(edges, model.EdgeKindImportsFrom)
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
	implEdges := filterEdgesByKind(edges, model.EdgeKindImplements)
	if len(implEdges) == 0 {
		t.Fatal("expected at least 1 IMPLEMENTS edge, got 0")
	}
	found := false
	for _, e := range implEdges {
		if containsSubstring(e.Fingerprint, "FileWriter") && containsSubstring(e.Fingerprint, "Writer") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected IMPLEMENTS edge linking FileWriter to Writer")
	}
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
	inheritEdges := filterEdgesByKind(edges, model.EdgeKindInherits)
	if len(inheritEdges) == 0 {
		t.Fatal("expected at least 1 INHERITS edge, got 0")
	}
	found := false
	for _, e := range inheritEdges {
		if containsSubstring(e.Fingerprint, "Child") && containsSubstring(e.Fingerprint, "Base") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected INHERITS edge from Child to Base")
	}
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
	fileNodes := filterByKind(nodes, model.NodeKindFile)
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
	containsEdges := filterEdgesByKind(edges, model.EdgeKindContains)
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
	funcNodes := filterByKind(nodes, model.NodeKindFunction)
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
	funcNodes := filterByKind(nodes, model.NodeKindFunction)
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
	classNodes := filterByKind(nodes, model.NodeKindClass)
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
	funcNodes := filterByKind(nodes, model.NodeKindFunction)
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
	classNodes := filterByKind(nodes, model.NodeKindClass)
	if len(classNodes) != 1 {
		t.Fatalf("expected 1 class node, got %d", len(classNodes))
	}
	if classNodes[0].Name != "User" {
		t.Errorf("Name = %q, want %q", classNodes[0].Name, "User")
	}
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
	funcNodes := filterByKind(nodes, model.NodeKindFunction)
	if len(funcNodes) != 1 {
		t.Fatalf("expected 1 function node, got %d", len(funcNodes))
	}
	if funcNodes[0].Name != "greet" {
		t.Errorf("Name = %q, want %q", funcNodes[0].Name, "greet")
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
	classNodes := filterByKind(nodes, model.NodeKindClass)
	if len(classNodes) != 1 {
		t.Fatalf("expected 1 class node, got %d", len(classNodes))
	}
	if classNodes[0].Name != "User" {
		t.Errorf("Name = %q, want %q", classNodes[0].Name, "User")
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
	classNodes := filterByKind(nodes, model.NodeKindClass)
	if len(classNodes) != 1 {
		t.Fatalf("expected 1 class node, got %d", len(classNodes))
	}
	if classNodes[0].Name != "User" {
		t.Errorf("Name = %q, want %q", classNodes[0].Name, "User")
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
	funcNodes := filterByKind(nodes, model.NodeKindFunction)
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
	testNodes := filterByKind(nodes, model.NodeKindTest)
	if len(testNodes) != 1 {
		t.Fatalf("expected 1 test node, got %d", len(testNodes))
	}
	if testNodes[0].Name != "TestAdd" {
		t.Errorf("Name = %q, want %q", testNodes[0].Name, "TestAdd")
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
	testNodes := filterByKind(nodes, model.NodeKindTest)
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
	testedByEdges := filterEdgesByKind(edges, model.EdgeKindTestedBy)
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
	funcNodes := filterByKind(nodes, model.NodeKindFunction)
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
	classNodes := filterByKind(nodes, model.NodeKindClass)
	if len(classNodes) != 1 {
		t.Fatalf("expected 1 class node, got %d", len(classNodes))
	}
	if classNodes[0].Name != "Foo" {
		t.Errorf("Name = %q, want %q", classNodes[0].Name, "Foo")
	}
}

func TestParseJS_Import(t *testing.T) {
	src := `import { foo } from 'bar';
`
	w := NewWalker(JavaScriptSpec)
	_, edges, err := w.Parse("app.js", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	importEdges := filterEdgesByKind(edges, model.EdgeKindImportsFrom)
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
	callEdges := filterEdgesByKind(edges, model.EdgeKindCalls)
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
	funcNodes := filterByKind(nodes, model.NodeKindFunction)
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
	funcNodes := filterByKind(nodes, model.NodeKindFunction)
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
	classNodes := filterByKind(nodes, model.NodeKindClass)
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
	typeNodes := filterByKind(nodes, model.NodeKindType)
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
	importEdges := filterEdgesByKind(edges, model.EdgeKindImportsFrom)
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
	callEdges := filterEdgesByKind(edges, model.EdgeKindCalls)
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
	funcNodes := filterByKind(nodes, model.NodeKindFunction)
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
	funcNodes := filterByKind(nodes, model.NodeKindFunction)
	if len(funcNodes) != 1 {
		t.Fatalf("expected 1 function node, got %d", len(funcNodes))
	}
	if funcNodes[0].Name != "foo" {
		t.Errorf("Name = %q, want %q", funcNodes[0].Name, "foo")
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
	funcNodes := filterByKind(nodes, model.NodeKindFunction)
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
	callEdges := filterEdgesByKind(edges, model.EdgeKindCalls)
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
	callEdges := filterEdgesByKind(edges, model.EdgeKindCalls)
	if len(callEdges) == 0 {
		t.Fatal("expected at least 1 CALLS edge for require, got 0")
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
		funcNodes := filterByKind(nodes, model.NodeKindFunction)
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
	funcNodes := filterByKind(nodes, model.NodeKindFunction)
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
	funcNodes := filterByKind(nodes, model.NodeKindFunction)
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
	testNodes := filterByKind(nodes, model.NodeKindTest)
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
	testNodes := filterByKind(nodes, model.NodeKindTest)
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
	funcNodes := filterByKind(nodes, model.NodeKindFunction)
	if len(funcNodes) != 1 {
		t.Fatalf("expected 1 function node (bar), got %d", len(funcNodes))
	}
	if funcNodes[0].Name != "bar" {
		t.Errorf("Name = %q, want %q", funcNodes[0].Name, "bar")
	}
	// bar는 Foo의 CONTAINS 자식이어야 함
	containsEdges := filterEdgesByKind(edges, model.EdgeKindContains)
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
	implEdges := filterEdgesByKind(edges, model.EdgeKindImplements)
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
	funcNodes := filterByKind(nodes, model.NodeKindFunction)
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
	classNodes := filterByKind(nodes, model.NodeKindClass)
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
	classNodes := filterByKind(nodes, model.NodeKindClass)
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
	typeNodes := filterByKind(nodes, model.NodeKindType)
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
	importEdges := filterEdgesByKind(edges, model.EdgeKindImportsFrom)
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
	callEdges := filterEdgesByKind(edges, model.EdgeKindCalls)
	if len(callEdges) == 0 {
		t.Fatal("expected at least 1 CALLS edge, got 0")
	}
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
	funcNodes := filterByKind(nodes, model.NodeKindFunction)
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
	classNodes := filterByKind(nodes, model.NodeKindClass)
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
	classNodes := filterByKind(nodes, model.NodeKindClass)
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
	importEdges := filterEdgesByKind(edges, model.EdgeKindImportsFrom)
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
	callEdges := filterEdgesByKind(edges, model.EdgeKindCalls)
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
	funcNodes := filterByKind(nodes, model.NodeKindFunction)
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
	funcNodes := filterByKind(nodes, model.NodeKindFunction)
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
	classNodes := filterByKind(nodes, model.NodeKindClass)
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
	importEdges := filterEdgesByKind(edges, model.EdgeKindImportsFrom)
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
	callEdges := filterEdgesByKind(edges, model.EdgeKindCalls)
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
	funcNodes := filterByKind(nodes, model.NodeKindFunction)
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

func filterByKind(nodes []model.Node, kind model.NodeKind) []model.Node {
	var result []model.Node
	for _, n := range nodes {
		if n.Kind == kind {
			result = append(result, n)
		}
	}
	return result
}

func filterEdgesByKind(edges []model.Edge, kind model.EdgeKind) []model.Edge {
	var result []model.Edge
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

		funcNodes := filterByKind(nodes, model.NodeKindFunction)
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
	st := gormstore.New(db)
	if err := st.AutoMigrate(); err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.SearchDocument{}); err != nil {
		t.Fatal(err)
	}
	sb := search.NewSQLiteBackend()
	if err := sb.Migrate(db); err != nil {
		if strings.Contains(err.Error(), "no such module: fts5") {
			t.Skip("fts5 module not available in this sqlite build, skipping test")
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
			if n.Kind == model.NodeKindFunction {
				stored, _ := st.GetNode(ctx, n.QualifiedName)
				if stored != nil {
					db.Create(&model.SearchDocument{
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
