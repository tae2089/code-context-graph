package deadcode

import (
	"context"
	"fmt"
	"testing"

	"github.com/tae2089/code-context-graph/internal/ctxns"
	"github.com/tae2089/code-context-graph/internal/model"
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

func seedNode(t *testing.T, db *gorm.DB, id uint, name string, kind model.NodeKind, file string) {
	t.Helper()
	seedNodeNS(t, db, id, name, kind, file, "")
}

func seedNodeNS(t *testing.T, db *gorm.DB, id uint, name string, kind model.NodeKind, file string, ns string) {
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

func TestFind_NoIncomingEdges(t *testing.T) {
	db := setupDB(t)
	seedNode(t, db, 1, "Unused", model.NodeKindFunction, "a.go")
	seedNode(t, db, 2, "Used", model.NodeKindFunction, "b.go")
	seedEdge(t, db, 1, 2, model.EdgeKindCalls)

	svc := New(db)
	got, err := svc.Find(context.Background(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 dead code node, got %d", len(got))
	}
	if got[0].Name != "Unused" {
		t.Errorf("expected Unused, got %s", got[0].Name)
	}
}

func TestFind_HasIncomingEdges(t *testing.T) {
	db := setupDB(t)
	seedNode(t, db, 1, "Caller", model.NodeKindFunction, "a.go")
	seedNode(t, db, 2, "Called", model.NodeKindFunction, "b.go")
	seedEdge(t, db, 1, 2, model.EdgeKindCalls)

	svc := New(db)
	got, err := svc.Find(context.Background(), Options{Kinds: []model.NodeKind{model.NodeKindFunction}})
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range got {
		if n.Name == "Called" {
			t.Error("Called should not be in dead code (has incoming edge)")
		}
	}
}

func TestFind_FilterByKind(t *testing.T) {
	db := setupDB(t)
	seedNode(t, db, 1, "UnusedFunc", model.NodeKindFunction, "a.go")
	seedNode(t, db, 2, "UnusedClass", model.NodeKindClass, "a.go")

	svc := New(db)
	got, err := svc.Find(context.Background(), Options{Kinds: []model.NodeKind{model.NodeKindFunction}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1, got %d", len(got))
	}
	if got[0].Name != "UnusedFunc" {
		t.Errorf("expected UnusedFunc, got %s", got[0].Name)
	}
}

func TestFind_FilterByFilePattern(t *testing.T) {
	db := setupDB(t)
	seedNode(t, db, 1, "InternalFunc", model.NodeKindFunction, "internal/a.go")
	seedNode(t, db, 2, "ExternalFunc", model.NodeKindFunction, "external/b.go")

	svc := New(db)
	got, err := svc.Find(context.Background(), Options{FilePattern: "internal/"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1, got %d", len(got))
	}
	if got[0].Name != "InternalFunc" {
		t.Errorf("expected InternalFunc, got %s", got[0].Name)
	}
}

func TestFind_ExcludesFileNodes(t *testing.T) {
	db := setupDB(t)
	seedNode(t, db, 1, "a.go", model.NodeKindFile, "a.go")
	seedNode(t, db, 2, "UnusedFunc", model.NodeKindFunction, "a.go")

	svc := New(db)
	got, err := svc.Find(context.Background(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range got {
		if n.Kind == model.NodeKindFile {
			t.Error("file nodes should be excluded from dead code")
		}
	}
	if len(got) != 1 {
		t.Fatalf("expected 1, got %d", len(got))
	}
}

func TestFind_ExcludesTestNodes(t *testing.T) {
	db := setupDB(t)
	seedNode(t, db, 1, "TestFoo", model.NodeKindTest, "a_test.go")
	seedNode(t, db, 2, "UnusedFunc", model.NodeKindFunction, "a.go")

	svc := New(db)
	got, err := svc.Find(context.Background(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range got {
		if n.Kind == model.NodeKindTest {
			t.Error("test nodes should be excluded from dead code")
		}
	}
	if len(got) != 1 {
		t.Fatalf("expected 1, got %d", len(got))
	}
}

func TestFind_RespectsNamespace(t *testing.T) {
	db := setupDB(t)
	seedNodeNS(t, db, 1, "UnusedA", model.NodeKindFunction, "a.go", "ns-a")
	seedNodeNS(t, db, 2, "UnusedB", model.NodeKindFunction, "b.go", "ns-b")

	svc := New(db)

	ctxA := ctxns.WithNamespace(context.Background(), "ns-a")
	got, err := svc.Find(ctxA, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 node for ns-a, got %d", len(got))
	}
	if got[0].Name != "UnusedA" {
		t.Errorf("expected UnusedA, got %s", got[0].Name)
	}

	ctxB := ctxns.WithNamespace(context.Background(), "ns-b")
	got, err = svc.Find(ctxB, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 node for ns-b, got %d", len(got))
	}
	if got[0].Name != "UnusedB" {
		t.Errorf("expected UnusedB, got %s", got[0].Name)
	}
}
