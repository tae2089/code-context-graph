package query

import (
	"context"
	"fmt"
	"testing"

	"github.com/imtaebin/code-context-graph/internal/ctxns"
	"github.com/imtaebin/code-context-graph/internal/model"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

func setupDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: gormlogger.Discard,
	})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.Node{}, &model.Edge{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func seedNode(t *testing.T, db *gorm.DB, id uint, name string, kind model.NodeKind, file string) model.Node {
	t.Helper()
	return seedNodeNS(t, db, id, name, kind, file, "")
}

func seedNodeNS(t *testing.T, db *gorm.DB, id uint, name string, kind model.NodeKind, file string, ns string) model.Node {
	t.Helper()
	n := model.Node{
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

func seedEdge(t *testing.T, db *gorm.DB, from, to uint, kind model.EdgeKind) {
	t.Helper()
	e := model.Edge{
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
	seedNode(t, db, 1, "A", model.NodeKindFunction, "a.go")
	seedNode(t, db, 2, "B", model.NodeKindFunction, "b.go")
	seedEdge(t, db, 1, 2, model.EdgeKindCalls) // A calls B

	svc := New(db)
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
	seedNode(t, db, 1, "A", model.NodeKindFunction, "a.go")

	svc := New(db)
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
	seedNode(t, db, 1, "A", model.NodeKindFunction, "a.go")
	seedNode(t, db, 2, "B", model.NodeKindFunction, "b.go")
	seedEdge(t, db, 1, 2, model.EdgeKindCalls) // A calls B

	svc := New(db)
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

func TestCalleesOf_NoCallees(t *testing.T) {
	db := setupDB(t)
	seedNode(t, db, 1, "A", model.NodeKindFunction, "a.go")

	svc := New(db)
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
	seedNode(t, db, 1, "A", model.NodeKindFile, "a.go")
	seedNode(t, db, 2, "B", model.NodeKindFile, "b.go")
	seedEdge(t, db, 1, 2, model.EdgeKindImportsFrom) // A imports B

	svc := New(db)
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
	seedNode(t, db, 1, "A", model.NodeKindFile, "a.go")
	seedNode(t, db, 2, "B", model.NodeKindFile, "b.go")
	seedEdge(t, db, 1, 2, model.EdgeKindImportsFrom) // A imports B

	svc := New(db)
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
	seedNode(t, db, 1, "pkg", model.NodeKindFile, "pkg.go")
	seedNode(t, db, 2, "Foo", model.NodeKindFunction, "pkg.go")
	seedEdge(t, db, 1, 2, model.EdgeKindContains) // pkg contains Foo

	svc := New(db)
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
	seedNode(t, db, 1, "Foo", model.NodeKindFunction, "foo.go")
	seedNode(t, db, 2, "TestFoo", model.NodeKindTest, "foo_test.go")
	seedEdge(t, db, 2, 1, model.EdgeKindTestedBy) // TestFoo tested_by → Foo

	svc := New(db)
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
	seedNode(t, db, 1, "Parent", model.NodeKindClass, "parent.go")
	seedNode(t, db, 2, "Child", model.NodeKindClass, "child.go")
	seedEdge(t, db, 2, 1, model.EdgeKindInherits) // Child inherits Parent

	svc := New(db)
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
	seedNode(t, db, 1, "pkg", model.NodeKindFile, "pkg.go")
	seedNode(t, db, 2, "Foo", model.NodeKindFunction, "pkg.go")
	seedNode(t, db, 3, "Bar", model.NodeKindFunction, "pkg.go")
	seedNode(t, db, 4, "Baz", model.NodeKindClass, "pkg.go")
	seedNode(t, db, 5, "MyType", model.NodeKindType, "pkg.go")
	seedNode(t, db, 6, "TestFoo", model.NodeKindTest, "pkg.go")

	svc := New(db)
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

	svc := New(db)
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
	seedNodeNS(t, db, 1, "FooA", model.NodeKindFunction, "pkg.go", "ns-a")
	seedNodeNS(t, db, 2, "FooB", model.NodeKindFunction, "pkg.go", "ns-b")

	svc := New(db)

	ctxA := ctxns.WithNamespace(context.Background(), "ns-a")
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
	seedNodeNS(t, db, 1, "CallerA", model.NodeKindFunction, "a.go", "ns-a")
	seedNodeNS(t, db, 2, "CalleeA", model.NodeKindFunction, "a.go", "ns-a")
	seedNodeNS(t, db, 3, "CallerB", model.NodeKindFunction, "b.go", "ns-b")
	seedNodeNS(t, db, 4, "CalleeB", model.NodeKindFunction, "b.go", "ns-b")
	seedEdge(t, db, 1, 2, model.EdgeKindCalls)
	seedEdge(t, db, 3, 4, model.EdgeKindCalls)

	svc := New(db)

	ctxA := ctxns.WithNamespace(context.Background(), "ns-a")
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
