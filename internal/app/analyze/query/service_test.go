// Graph query application characterization tests.
package query

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/tae2089/code-context-graph/internal/adapters/outbound/graphgorm"
	requestctx "github.com/tae2089/code-context-graph/internal/ctx"
	"github.com/tae2089/code-context-graph/internal/domain/graph"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

func boolPtr(v bool) *bool {
	return &v
}

func setupDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: gormlogger.Discard,
	})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&graph.Node{}, &graph.Edge{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func seedNode(t *testing.T, db *gorm.DB, id uint, name string, kind graph.NodeKind, file string) graph.Node {
	t.Helper()
	return seedNodeNS(t, db, id, name, kind, file, "")
}

func seedNodeNS(t *testing.T, db *gorm.DB, id uint, name string, kind graph.NodeKind, file string, ns string) graph.Node {
	t.Helper()
	n := graph.Node{
		ID:            id,
		QualifiedName: fmt.Sprintf("%s::%s", file, name),
		Namespace:     ns,
		Kind:          kind,
		Name:          name,
		FilePath:      file,
		StartLine:     1,
		EndLine:       10,
		Language:      "go",
	}
	if err := db.Create(&n).Error; err != nil {
		t.Fatalf("seed node: %v", err)
	}
	return n
}

func seedEdge(t *testing.T, db *gorm.DB, from, to uint, kind graph.EdgeKind) {
	t.Helper()
	seedEdgeNS(t, db, from, to, kind, "")
}

func seedEdgeNS(t *testing.T, db *gorm.DB, from, to uint, kind graph.EdgeKind, ns string) {
	t.Helper()
	e := graph.Edge{
		Namespace:   ns,
		FromNodeID:  from,
		ToNodeID:    to,
		Kind:        kind,
		Fingerprint: fmt.Sprintf("%d-%d-%s", from, to, kind),
	}
	if err := db.Create(&e).Error; err != nil {
		t.Fatalf("seed edge: %v", err)
	}
}

func TestCallersOf_ReturnsCallingNodes(t *testing.T) {
	db := setupDB(t)
	seedNode(t, db, 1, "A", graph.NodeKindFunction, "a.go")
	seedNode(t, db, 2, "B", graph.NodeKindFunction, "b.go")
	seedEdge(t, db, 1, 2, graph.EdgeKindCalls) // A calls B

	svc := New(graphgorm.New(db))
	got, err := svc.CallersOf(context.Background(), 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 caller, got %d", len(got))
	}
	if got[0].ID != 1 {
		t.Errorf("expected caller ID=1, got %d", got[0].ID)
	}
}

func TestCallersOf_NoCallers(t *testing.T) {
	db := setupDB(t)
	seedNode(t, db, 1, "A", graph.NodeKindFunction, "a.go")

	svc := New(graphgorm.New(db))
	got, err := svc.CallersOf(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("expected 0 callers, got %d", len(got))
	}
}

func TestCalleesOf_ReturnsCalledNodes(t *testing.T) {
	db := setupDB(t)
	seedNode(t, db, 1, "A", graph.NodeKindFunction, "a.go")
	seedNode(t, db, 2, "B", graph.NodeKindFunction, "b.go")
	seedEdge(t, db, 1, 2, graph.EdgeKindCalls) // A calls B

	svc := New(graphgorm.New(db))
	got, err := svc.CalleesOf(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 callee, got %d", len(got))
	}
	if got[0].ID != 2 {
		t.Errorf("expected callee ID=2, got %d", got[0].ID)
	}
}

func TestCalleesOf_IncludesFallbackCalls(t *testing.T) {
	db := setupDB(t)
	seedNode(t, db, 1, "A", graph.NodeKindFunction, "a.go")
	seedNode(t, db, 2, "B", graph.NodeKindFunction, "b.go")
	seedEdgeNS(t, db, 1, 2, graph.EdgeKindFallbackCalls, "") // A → B via fallback-call edge

	svc := New(graphgorm.New(db))
	got, err := svc.CalleesOf(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 callee, got %d", len(got))
	}
	if got[0].ID != 2 {
		t.Errorf("expected callee ID=2, got %d", got[0].ID)
	}
}

func TestCalleesOfWithOptions_ExcludesFallbackCallsWhenDisabled(t *testing.T) {
	db := setupDB(t)
	seedNode(t, db, 1, "A", graph.NodeKindFunction, "a.go")
	seedNode(t, db, 2, "B", graph.NodeKindFunction, "b.go")
	seedEdgeNS(t, db, 1, 2, graph.EdgeKindFallbackCalls, "")

	svc := New(graphgorm.New(db))
	got, err := svc.CalleesOfWithOptions(context.Background(), 1, QueryOptions{IncludeFallbackCalls: boolPtr(false)})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("expected 0 callees when fallback calls disabled, got %d", len(got))
	}
}

func TestCalleesOfWithOptions_IncludesFallbackCallsByDefault(t *testing.T) {
	db := setupDB(t)
	seedNode(t, db, 1, "A", graph.NodeKindFunction, "a.go")
	seedNode(t, db, 2, "B", graph.NodeKindFunction, "b.go")
	seedEdgeNS(t, db, 1, 2, graph.EdgeKindFallbackCalls, "")

	svc := New(graphgorm.New(db))
	got, err := svc.CalleesOfWithOptions(context.Background(), 1, QueryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 callee when fallback calls use default behavior, got %d", len(got))
	}
	if got[0].ID != 2 {
		t.Errorf("expected callee ID=2, got %d", got[0].ID)
	}
}

func TestCalleesOfPage_ReturnsLimitOffsetAndTotalCount(t *testing.T) {
	db := setupDB(t)
	seedNode(t, db, 1, "A", graph.NodeKindFunction, "a.go")
	node2 := graph.Node{
		ID:            2,
		QualifiedName: "pkg.CalleeB",
		Namespace:     "",
		Kind:          graph.NodeKindFunction,
		Name:          "CalleeB",
		FilePath:      "a.go",
		StartLine:     20,
		EndLine:       20,
		Language:      "go",
	}
	node3 := graph.Node{
		ID:            3,
		QualifiedName: "pkg.CalleeC",
		Namespace:     "",
		Kind:          graph.NodeKindFunction,
		Name:          "CalleeC",
		FilePath:      "a.go",
		StartLine:     10,
		EndLine:       10,
		Language:      "go",
	}
	node4 := graph.Node{
		ID:            4,
		QualifiedName: "pkg.CalleeD",
		Namespace:     "",
		Kind:          graph.NodeKindFunction,
		Name:          "CalleeD",
		FilePath:      "a.go",
		StartLine:     30,
		EndLine:       30,
		Language:      "go",
	}
	if err := db.Create(&node2).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&node3).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&node4).Error; err != nil {
		t.Fatal(err)
	}
	seedEdge(t, db, 1, 2, graph.EdgeKindCalls)
	seedEdge(t, db, 1, 3, graph.EdgeKindCalls)
	seedEdge(t, db, 1, 4, graph.EdgeKindCalls)

	svc := New(graphgorm.New(db))
	got, err := svc.CalleesOfPage(context.Background(), 1, QueryOptions{Limit: 2, Offset: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Nodes) != 2 {
		t.Fatalf("expected 2 callees on page, got %d", len(got.Nodes))
	}
	if got.TotalCount != 3 {
		t.Fatalf("expected total count 3, got %d", got.TotalCount)
	}
	if got.Nodes[0].ID != 2 || got.Nodes[1].ID != 4 {
		t.Fatalf("expected sorted page second slice to be nodes 2 and 4, got %d, %d", got.Nodes[0].ID, got.Nodes[1].ID)
	}
}

func TestCallersOfWithOptions_ExcludesFallbackCallsWhenDisabled(t *testing.T) {
	db := setupDB(t)
	seedNode(t, db, 1, "A", graph.NodeKindFunction, "a.go")
	seedNode(t, db, 2, "B", graph.NodeKindFunction, "b.go")
	seedEdgeNS(t, db, 1, 2, graph.EdgeKindFallbackCalls, "")

	svc := New(graphgorm.New(db))
	got, err := svc.CallersOfWithOptions(context.Background(), 2, QueryOptions{IncludeFallbackCalls: boolPtr(false)})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("expected 0 callers when fallback calls disabled, got %d", len(got))
	}
}

func TestCalleesOf_NoCallees(t *testing.T) {
	db := setupDB(t)
	seedNode(t, db, 1, "A", graph.NodeKindFunction, "a.go")

	svc := New(graphgorm.New(db))
	got, err := svc.CalleesOf(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("expected 0 callees, got %d", len(got))
	}
}

func TestImportsOf_ReturnsImportedNodes(t *testing.T) {
	db := setupDB(t)
	seedNode(t, db, 1, "A", graph.NodeKindFile, "a.go")
	seedNode(t, db, 2, "B", graph.NodeKindFile, "b.go")
	seedEdge(t, db, 1, 2, graph.EdgeKindImportsFrom) // A imports B

	svc := New(graphgorm.New(db))
	got, err := svc.ImportsOf(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 import, got %d", len(got))
	}
	if got[0].ID != 2 {
		t.Errorf("expected import ID=2, got %d", got[0].ID)
	}
}

func TestImportersOf_ReturnsImportingNodes(t *testing.T) {
	db := setupDB(t)
	seedNode(t, db, 1, "A", graph.NodeKindFile, "a.go")
	seedNode(t, db, 2, "B", graph.NodeKindFile, "b.go")
	seedEdge(t, db, 1, 2, graph.EdgeKindImportsFrom) // A imports B

	svc := New(graphgorm.New(db))
	got, err := svc.ImportersOf(context.Background(), 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 importer, got %d", len(got))
	}
	if got[0].ID != 1 {
		t.Errorf("expected importer ID=1, got %d", got[0].ID)
	}
}

func TestChildrenOf_ReturnsContainedNodes(t *testing.T) {
	db := setupDB(t)
	seedNode(t, db, 1, "pkg", graph.NodeKindFile, "pkg.go")
	seedNode(t, db, 2, "Foo", graph.NodeKindFunction, "pkg.go")
	seedEdge(t, db, 1, 2, graph.EdgeKindContains) // pkg contains Foo

	svc := New(graphgorm.New(db))
	got, err := svc.ChildrenOf(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 child, got %d", len(got))
	}
	if got[0].ID != 2 {
		t.Errorf("expected child ID=2, got %d", got[0].ID)
	}
}

func TestTestsFor_ReturnsTestNodes(t *testing.T) {
	db := setupDB(t)
	seedNode(t, db, 1, "Foo", graph.NodeKindFunction, "foo.go")
	seedNode(t, db, 2, "TestFoo", graph.NodeKindTest, "foo_test.go")
	seedEdge(t, db, 2, 1, graph.EdgeKindTestedBy) // TestFoo tested_by → Foo

	svc := New(graphgorm.New(db))
	got, err := svc.TestsFor(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 test, got %d", len(got))
	}
	if got[0].ID != 2 {
		t.Errorf("expected test ID=2, got %d", got[0].ID)
	}
}

func TestInheritorsOf_ReturnsInheritingNodes(t *testing.T) {
	db := setupDB(t)
	seedNode(t, db, 1, "Parent", graph.NodeKindClass, "parent.go")
	seedNode(t, db, 2, "Child", graph.NodeKindClass, "child.go")
	seedEdge(t, db, 2, 1, graph.EdgeKindInherits) // Child inherits Parent

	svc := New(graphgorm.New(db))
	got, err := svc.InheritorsOf(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 inheritor, got %d", len(got))
	}
	if got[0].ID != 2 {
		t.Errorf("expected inheritor ID=2, got %d", got[0].ID)
	}
}

func TestFileSummary_ReturnsNodesByKind(t *testing.T) {
	db := setupDB(t)
	seedNode(t, db, 1, "pkg", graph.NodeKindFile, "pkg.go")
	seedNode(t, db, 2, "Foo", graph.NodeKindFunction, "pkg.go")
	seedNode(t, db, 3, "Bar", graph.NodeKindFunction, "pkg.go")
	seedNode(t, db, 4, "Baz", graph.NodeKindClass, "pkg.go")
	seedNode(t, db, 5, "MyType", graph.NodeKindType, "pkg.go")
	seedNode(t, db, 6, "TestFoo", graph.NodeKindTest, "pkg.go")

	svc := New(graphgorm.New(db))
	got, err := svc.FileSummaryOf(context.Background(), "pkg.go")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("expected non-nil FileSummary")
	}
	if got.Functions != 2 {
		t.Errorf("expected 2 functions, got %d", got.Functions)
	}
	if got.Classes != 1 {
		t.Errorf("expected 1 class, got %d", got.Classes)
	}
	if got.Types != 1 {
		t.Errorf("expected 1 type, got %d", got.Types)
	}
	if got.Tests != 1 {
		t.Errorf("expected 1 test, got %d", got.Tests)
	}
	if got.Total != 6 {
		t.Errorf("expected 6 total, got %d", got.Total)
	}
}

func TestFileSummary_FileNotFound(t *testing.T) {
	db := setupDB(t)

	svc := New(graphgorm.New(db))
	got, err := svc.FileSummaryOf(context.Background(), "nonexistent.go")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("expected non-nil FileSummary")
	}
	if got.Total != 0 {
		t.Errorf("expected 0 total for missing file, got %d", got.Total)
	}
}

func TestFileSummaryOf_RespectsNamespace(t *testing.T) {
	db := setupDB(t)
	seedNodeNS(t, db, 1, "FooA", graph.NodeKindFunction, "pkg.go", "ns-a")
	seedNodeNS(t, db, 2, "FooB", graph.NodeKindFunction, "pkg.go", "ns-b")

	svc := New(graphgorm.New(db))

	ctxA := requestctx.WithNamespace(context.Background(), "ns-a")
	got, err := svc.FileSummaryOf(ctxA, "pkg.go")
	if err != nil {
		t.Fatal(err)
	}
	if got.Total != 1 {
		t.Errorf("expected total=1 for ns-a, got %d", got.Total)
	}
	if got.Functions != 1 {
		t.Errorf("expected functions=1 for ns-a, got %d", got.Functions)
	}
}

func TestNodesByEdge_RespectsNamespace(t *testing.T) {
	db := setupDB(t)
	seedNodeNS(t, db, 1, "CallerA", graph.NodeKindFunction, "a.go", "ns-a")
	seedNodeNS(t, db, 2, "CalleeA", graph.NodeKindFunction, "a.go", "ns-a")
	seedNodeNS(t, db, 3, "CallerB", graph.NodeKindFunction, "b.go", "ns-b")
	seedNodeNS(t, db, 4, "CalleeB", graph.NodeKindFunction, "b.go", "ns-b")
	seedEdgeNS(t, db, 1, 2, graph.EdgeKindCalls, "ns-a")
	seedEdgeNS(t, db, 3, 4, graph.EdgeKindCalls, "ns-b")

	svc := New(graphgorm.New(db))

	ctxA := requestctx.WithNamespace(context.Background(), "ns-a")
	got, err := svc.CalleesOf(ctxA, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 callee for ns-a, got %d", len(got))
	}
	if got[0].Name != "CalleeA" {
		t.Errorf("expected CalleeA, got %s", got[0].Name)
	}
}

func TestCalleesOf_DedupsAndSortsResults(t *testing.T) {
	db := setupDB(t)
	caller := seedNode(t, db, 1, "Caller", graph.NodeKindFunction, "caller.go")
	calleeB := seedNode(t, db, 2, "B", graph.NodeKindFunction, "b.go")
	calleeA := seedNode(t, db, 3, "A", graph.NodeKindFunction, "a.go")
	seedEdge(t, db, caller.ID, calleeB.ID, graph.EdgeKindCalls)
	if err := db.Create(&graph.Edge{FromNodeID: caller.ID, ToNodeID: calleeB.ID, Kind: graph.EdgeKindCalls, Fingerprint: "1-2-calls-dup"}).Error; err != nil {
		t.Fatalf("seed duplicate edge: %v", err)
	}
	seedEdge(t, db, caller.ID, calleeA.ID, graph.EdgeKindCalls)

	svc := New(graphgorm.New(db))
	got, err := svc.CalleesOf(context.Background(), caller.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 unique callees, got %d: %+v", len(got), got)
	}
	gotNames := []string{got[0].QualifiedName, got[1].QualifiedName}
	wantNames := []string{calleeA.QualifiedName, calleeB.QualifiedName}
	if !reflect.DeepEqual(gotNames, wantNames) {
		t.Fatalf("callees order = %v, want %v", gotNames, wantNames)
	}
}
