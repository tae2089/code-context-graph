package gormstore

import (
	"context"
	"fmt"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/imtaebin/code-context-graph/internal/ctxns"
	"github.com/imtaebin/code-context-graph/internal/model"
	"github.com/imtaebin/code-context-graph/internal/store"
)

func setupTestDB(t *testing.T) *Store {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Discard,
	})
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}
	s := New(db)
	if err := s.AutoMigrate(); err != nil {
		t.Fatalf("failed to migrate: %v", err)
	}
	return s
}

func TestAutoMigrate_SQLite(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Discard,
	})
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	s := New(db)
	if err := s.AutoMigrate(); err != nil {
		t.Fatalf("AutoMigrate failed: %v", err)
	}

	sqlDB, _ := db.DB()
	tables := []string{"nodes", "edges", "annotations", "doc_tags"}
	for _, table := range tables {
		var count int
		row := sqlDB.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?", table)
		if err := row.Scan(&count); err != nil {
			t.Fatalf("query table %s: %v", table, err)
		}
		if count != 1 {
			t.Errorf("table %s not created", table)
		}
	}
}

func TestUpsertNodes_Insert(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()

	nodes := []model.Node{
		{QualifiedName: "main.Hello", Kind: model.NodeKindFunction, Name: "Hello", FilePath: "main.go", StartLine: 1, EndLine: 3, Language: "go"},
	}
	if err := s.UpsertNodes(ctx, nodes); err != nil {
		t.Fatalf("UpsertNodes: %v", err)
	}

	got, err := s.GetNode(ctx, "main.Hello")
	if err != nil {
		t.Fatalf("GetNode: %v", err)
	}
	if got == nil {
		t.Fatal("expected node, got nil")
	}
	if got.Name != "Hello" {
		t.Errorf("Name = %q, want %q", got.Name, "Hello")
	}
}

func TestUpsertNodes_Update(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()

	nodes := []model.Node{
		{QualifiedName: "main.Hello", Kind: model.NodeKindFunction, Name: "Hello", FilePath: "main.go", StartLine: 1, EndLine: 3, Hash: "aaa", Language: "go"},
	}
	if err := s.UpsertNodes(ctx, nodes); err != nil {
		t.Fatalf("first UpsertNodes: %v", err)
	}

	nodes[0].Hash = "bbb"
	nodes[0].EndLine = 5
	if err := s.UpsertNodes(ctx, nodes); err != nil {
		t.Fatalf("second UpsertNodes: %v", err)
	}

	got, err := s.GetNode(ctx, "main.Hello")
	if err != nil {
		t.Fatalf("GetNode: %v", err)
	}
	if got.Hash != "bbb" {
		t.Errorf("Hash = %q, want %q", got.Hash, "bbb")
	}
	if got.EndLine != 5 {
		t.Errorf("EndLine = %d, want 5", got.EndLine)
	}
}

func TestGetNode_ByQualifiedName(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()

	nodes := []model.Node{
		{QualifiedName: "pkg.Func1", Kind: model.NodeKindFunction, Name: "Func1", FilePath: "a.go", StartLine: 1, EndLine: 2, Language: "go"},
		{QualifiedName: "pkg.Func2", Kind: model.NodeKindFunction, Name: "Func2", FilePath: "a.go", StartLine: 3, EndLine: 4, Language: "go"},
	}
	if err := s.UpsertNodes(ctx, nodes); err != nil {
		t.Fatalf("UpsertNodes: %v", err)
	}

	got, err := s.GetNode(ctx, "pkg.Func2")
	if err != nil {
		t.Fatalf("GetNode: %v", err)
	}
	if got == nil || got.Name != "Func2" {
		t.Errorf("expected Func2, got %v", got)
	}
}

func TestGetNode_NotFound(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()

	got, err := s.GetNode(ctx, "nonexistent")
	if err != nil {
		t.Fatalf("GetNode: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}

func TestGetNodesByFile(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()

	nodes := []model.Node{
		{QualifiedName: "pkg.A", Kind: model.NodeKindFunction, Name: "A", FilePath: "a.go", StartLine: 1, EndLine: 2, Language: "go"},
		{QualifiedName: "pkg.B", Kind: model.NodeKindFunction, Name: "B", FilePath: "a.go", StartLine: 3, EndLine: 4, Language: "go"},
		{QualifiedName: "pkg.C", Kind: model.NodeKindFunction, Name: "C", FilePath: "b.go", StartLine: 1, EndLine: 2, Language: "go"},
	}
	if err := s.UpsertNodes(ctx, nodes); err != nil {
		t.Fatalf("UpsertNodes: %v", err)
	}

	got, err := s.GetNodesByFile(ctx, "a.go")
	if err != nil {
		t.Fatalf("GetNodesByFile: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("expected 2 nodes, got %d", len(got))
	}
}

func TestDeleteNodesByFile(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()

	nodes := []model.Node{
		{QualifiedName: "pkg.A", Kind: model.NodeKindFunction, Name: "A", FilePath: "a.go", StartLine: 1, EndLine: 2, Language: "go"},
		{QualifiedName: "pkg.B", Kind: model.NodeKindFunction, Name: "B", FilePath: "b.go", StartLine: 1, EndLine: 2, Language: "go"},
	}
	if err := s.UpsertNodes(ctx, nodes); err != nil {
		t.Fatalf("UpsertNodes: %v", err)
	}

	if err := s.DeleteNodesByFile(ctx, "a.go"); err != nil {
		t.Fatalf("DeleteNodesByFile: %v", err)
	}

	got, _ := s.GetNode(ctx, "pkg.A")
	if got != nil {
		t.Error("expected pkg.A to be deleted")
	}
	got, _ = s.GetNode(ctx, "pkg.B")
	if got == nil {
		t.Error("expected pkg.B to still exist")
	}
}

func TestUpsertEdges_Insert(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()

	nodes := []model.Node{
		{QualifiedName: "pkg.A", Kind: model.NodeKindFunction, Name: "A", FilePath: "a.go", StartLine: 1, EndLine: 2, Language: "go"},
		{QualifiedName: "pkg.B", Kind: model.NodeKindFunction, Name: "B", FilePath: "a.go", StartLine: 3, EndLine: 4, Language: "go"},
	}
	if err := s.UpsertNodes(ctx, nodes); err != nil {
		t.Fatalf("UpsertNodes: %v", err)
	}

	nodeA, _ := s.GetNode(ctx, "pkg.A")
	nodeB, _ := s.GetNode(ctx, "pkg.B")

	edges := []model.Edge{
		{FromNodeID: nodeA.ID, ToNodeID: nodeB.ID, Kind: model.EdgeKindCalls, FilePath: "a.go", Line: 2, Fingerprint: "calls:a.go:B:2"},
	}
	if err := s.UpsertEdges(ctx, edges); err != nil {
		t.Fatalf("UpsertEdges: %v", err)
	}

	got, err := s.GetEdgesFrom(ctx, nodeA.ID)
	if err != nil {
		t.Fatalf("GetEdgesFrom: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 edge, got %d", len(got))
	}
	if got[0].Kind != model.EdgeKindCalls {
		t.Errorf("Kind = %q, want %q", got[0].Kind, model.EdgeKindCalls)
	}
}

func TestUpsertEdges_Dedup(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()

	nodes := []model.Node{
		{QualifiedName: "pkg.A", Kind: model.NodeKindFunction, Name: "A", FilePath: "a.go", StartLine: 1, EndLine: 2, Language: "go"},
		{QualifiedName: "pkg.B", Kind: model.NodeKindFunction, Name: "B", FilePath: "a.go", StartLine: 3, EndLine: 4, Language: "go"},
	}
	s.UpsertNodes(ctx, nodes)
	nodeA, _ := s.GetNode(ctx, "pkg.A")
	nodeB, _ := s.GetNode(ctx, "pkg.B")

	edge := model.Edge{FromNodeID: nodeA.ID, ToNodeID: nodeB.ID, Kind: model.EdgeKindCalls, FilePath: "a.go", Line: 2, Fingerprint: "calls:a.go:B:2"}
	s.UpsertEdges(ctx, []model.Edge{edge})
	s.UpsertEdges(ctx, []model.Edge{edge})

	got, _ := s.GetEdgesFrom(ctx, nodeA.ID)
	if len(got) != 1 {
		t.Errorf("expected 1 edge after dedup, got %d", len(got))
	}
}

func TestGetEdgesFrom(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()

	nodes := []model.Node{
		{QualifiedName: "pkg.A", Kind: model.NodeKindFunction, Name: "A", FilePath: "a.go", StartLine: 1, EndLine: 2, Language: "go"},
		{QualifiedName: "pkg.B", Kind: model.NodeKindFunction, Name: "B", FilePath: "a.go", StartLine: 3, EndLine: 4, Language: "go"},
		{QualifiedName: "pkg.C", Kind: model.NodeKindFunction, Name: "C", FilePath: "a.go", StartLine: 5, EndLine: 6, Language: "go"},
	}
	s.UpsertNodes(ctx, nodes)
	nodeA, _ := s.GetNode(ctx, "pkg.A")
	nodeB, _ := s.GetNode(ctx, "pkg.B")
	nodeC, _ := s.GetNode(ctx, "pkg.C")

	s.UpsertEdges(ctx, []model.Edge{
		{FromNodeID: nodeA.ID, ToNodeID: nodeB.ID, Kind: model.EdgeKindCalls, FilePath: "a.go", Line: 2, Fingerprint: "calls:a.go:B:2"},
		{FromNodeID: nodeA.ID, ToNodeID: nodeC.ID, Kind: model.EdgeKindCalls, FilePath: "a.go", Line: 3, Fingerprint: "calls:a.go:C:3"},
	})

	got, err := s.GetEdgesFrom(ctx, nodeA.ID)
	if err != nil {
		t.Fatalf("GetEdgesFrom: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("expected 2 edges, got %d", len(got))
	}
}

func TestGetEdgesTo(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()

	nodes := []model.Node{
		{QualifiedName: "pkg.A", Kind: model.NodeKindFunction, Name: "A", FilePath: "a.go", StartLine: 1, EndLine: 2, Language: "go"},
		{QualifiedName: "pkg.B", Kind: model.NodeKindFunction, Name: "B", FilePath: "a.go", StartLine: 3, EndLine: 4, Language: "go"},
	}
	s.UpsertNodes(ctx, nodes)
	nodeA, _ := s.GetNode(ctx, "pkg.A")
	nodeB, _ := s.GetNode(ctx, "pkg.B")

	s.UpsertEdges(ctx, []model.Edge{
		{FromNodeID: nodeA.ID, ToNodeID: nodeB.ID, Kind: model.EdgeKindCalls, FilePath: "a.go", Line: 2, Fingerprint: "calls:a.go:B:2"},
	})

	got, err := s.GetEdgesTo(ctx, nodeB.ID)
	if err != nil {
		t.Fatalf("GetEdgesTo: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("expected 1 edge, got %d", len(got))
	}
}

func TestDeleteEdgesByFile(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()

	nodes := []model.Node{
		{QualifiedName: "pkg.A", Kind: model.NodeKindFunction, Name: "A", FilePath: "a.go", StartLine: 1, EndLine: 2, Language: "go"},
		{QualifiedName: "pkg.B", Kind: model.NodeKindFunction, Name: "B", FilePath: "a.go", StartLine: 3, EndLine: 4, Language: "go"},
	}
	s.UpsertNodes(ctx, nodes)
	nodeA, _ := s.GetNode(ctx, "pkg.A")
	nodeB, _ := s.GetNode(ctx, "pkg.B")

	s.UpsertEdges(ctx, []model.Edge{
		{FromNodeID: nodeA.ID, ToNodeID: nodeB.ID, Kind: model.EdgeKindCalls, FilePath: "a.go", Line: 2, Fingerprint: "calls:a.go:B:2"},
	})

	if err := s.DeleteEdgesByFile(ctx, "a.go"); err != nil {
		t.Fatalf("DeleteEdgesByFile: %v", err)
	}

	got, _ := s.GetEdgesFrom(ctx, nodeA.ID)
	if len(got) != 0 {
		t.Errorf("expected 0 edges after delete, got %d", len(got))
	}
}

func TestUpsertAnnotation_Insert(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()

	s.UpsertNodes(ctx, []model.Node{
		{QualifiedName: "pkg.F", Kind: model.NodeKindFunction, Name: "F", FilePath: "a.go", StartLine: 1, EndLine: 2, Language: "go"},
	})
	node, _ := s.GetNode(ctx, "pkg.F")

	ann := &model.Annotation{
		NodeID:  node.ID,
		Summary: "does something",
		Context: "called from handler",
		RawText: "does something\ncalled from handler",
		Tags: []model.DocTag{
			{Kind: model.TagParam, Name: "x", Value: "input value", Ordinal: 0},
			{Kind: model.TagReturn, Value: "result", Ordinal: 0},
		},
	}
	if err := s.UpsertAnnotation(ctx, ann); err != nil {
		t.Fatalf("UpsertAnnotation: %v", err)
	}

	got, err := s.GetAnnotation(ctx, node.ID)
	if err != nil {
		t.Fatalf("GetAnnotation: %v", err)
	}
	if got == nil {
		t.Fatal("expected annotation, got nil")
	}
	if got.Summary != "does something" {
		t.Errorf("Summary = %q, want %q", got.Summary, "does something")
	}
}

func TestUpsertAnnotation_Update(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()

	s.UpsertNodes(ctx, []model.Node{
		{QualifiedName: "pkg.F", Kind: model.NodeKindFunction, Name: "F", FilePath: "a.go", StartLine: 1, EndLine: 2, Language: "go"},
	})
	node, _ := s.GetNode(ctx, "pkg.F")

	ann1 := &model.Annotation{
		NodeID:  node.ID,
		Summary: "old summary",
		Tags: []model.DocTag{
			{Kind: model.TagParam, Name: "x", Value: "old", Ordinal: 0},
		},
	}
	s.UpsertAnnotation(ctx, ann1)

	ann2 := &model.Annotation{
		NodeID:  node.ID,
		Summary: "new summary",
		Tags: []model.DocTag{
			{Kind: model.TagReturn, Value: "new result", Ordinal: 0},
		},
	}
	if err := s.UpsertAnnotation(ctx, ann2); err != nil {
		t.Fatalf("UpsertAnnotation update: %v", err)
	}

	got, _ := s.GetAnnotation(ctx, node.ID)
	if got.Summary != "new summary" {
		t.Errorf("Summary = %q, want %q", got.Summary, "new summary")
	}
	if len(got.Tags) != 1 {
		t.Fatalf("expected 1 tag, got %d", len(got.Tags))
	}
	if got.Tags[0].Kind != model.TagReturn {
		t.Errorf("Tag Kind = %q, want %q", got.Tags[0].Kind, model.TagReturn)
	}
}

func TestGetAnnotation(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()

	got, err := s.GetAnnotation(ctx, 9999)
	if err != nil {
		t.Fatalf("GetAnnotation: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil for nonexistent node, got %v", got)
	}
}

func TestGetAnnotation_WithTags(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()

	s.UpsertNodes(ctx, []model.Node{
		{QualifiedName: "pkg.F", Kind: model.NodeKindFunction, Name: "F", FilePath: "a.go", StartLine: 1, EndLine: 2, Language: "go"},
	})
	node, _ := s.GetNode(ctx, "pkg.F")

	ann := &model.Annotation{
		NodeID:  node.ID,
		Summary: "summary",
		Tags: []model.DocTag{
			{Kind: model.TagParam, Name: "a", Value: "first", Ordinal: 0},
			{Kind: model.TagParam, Name: "b", Value: "second", Ordinal: 1},
			{Kind: model.TagIntent, Value: "do stuff", Ordinal: 0},
		},
	}
	s.UpsertAnnotation(ctx, ann)

	got, _ := s.GetAnnotation(ctx, node.ID)
	if len(got.Tags) != 3 {
		t.Fatalf("expected 3 tags, got %d", len(got.Tags))
	}
}

func TestWithTx_Success(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()

	err := s.WithTx(ctx, func(txStore store.GraphStore) error {
		return txStore.UpsertNodes(ctx, []model.Node{
			{QualifiedName: "tx.A", Kind: model.NodeKindFunction, Name: "A", FilePath: "a.go", StartLine: 1, EndLine: 2, Language: "go"},
		})
	})
	if err != nil {
		t.Fatalf("WithTx: %v", err)
	}

	got, _ := s.GetNode(ctx, "tx.A")
	if got == nil {
		t.Error("expected node to be committed")
	}
}

func TestWithTx_Rollback(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()

	err := s.WithTx(ctx, func(txStore store.GraphStore) error {
		txStore.UpsertNodes(ctx, []model.Node{
			{QualifiedName: "tx.B", Kind: model.NodeKindFunction, Name: "B", FilePath: "a.go", StartLine: 1, EndLine: 2, Language: "go"},
		})
		return fmt.Errorf("intentional error")
	})
	if err == nil {
		t.Fatal("expected error from WithTx")
	}

	got, _ := s.GetNode(ctx, "tx.B")
	if got != nil {
		t.Error("expected node to be rolled back, but it exists")
	}
}

func TestDeleteNode_CascadeEdges(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()

	s.UpsertNodes(ctx, []model.Node{
		{QualifiedName: "pkg.A", Kind: model.NodeKindFunction, Name: "A", FilePath: "a.go", StartLine: 1, EndLine: 2, Language: "go"},
		{QualifiedName: "pkg.B", Kind: model.NodeKindFunction, Name: "B", FilePath: "b.go", StartLine: 1, EndLine: 2, Language: "go"},
	})
	nodeA, _ := s.GetNode(ctx, "pkg.A")
	nodeB, _ := s.GetNode(ctx, "pkg.B")

	s.UpsertEdges(ctx, []model.Edge{
		{FromNodeID: nodeA.ID, ToNodeID: nodeB.ID, Kind: model.EdgeKindCalls, FilePath: "a.go", Line: 2, Fingerprint: "calls:a.go:B:2"},
	})

	s.DeleteNodesByFile(ctx, "a.go")

	edges, _ := s.GetEdgesFrom(ctx, nodeA.ID)
	if len(edges) != 0 {
		t.Errorf("expected 0 edges after cascade delete, got %d", len(edges))
	}
}

func TestNode_NamespaceField(t *testing.T) {
	s := setupTestDB(t)
	ctx := ctxns.WithNamespace(context.Background(), "svc")

	nodes := []model.Node{
		{QualifiedName: "pkg.A", Kind: model.NodeKindFunction, Name: "A", FilePath: "a.go", StartLine: 1, EndLine: 2, Language: "go"},
	}
	if err := s.UpsertNodes(ctx, nodes); err != nil {
		t.Fatalf("UpsertNodes: %v", err)
	}

	got, err := s.GetNode(ctx, "pkg.A")
	if err != nil {
		t.Fatalf("GetNode: %v", err)
	}
	if got == nil {
		t.Fatal("expected node, got nil")
	}
	if got.Namespace != "svc" {
		t.Errorf("Namespace = %q, want %q", got.Namespace, "svc")
	}
}

func TestNode_UniqueIndex_NamespaceQualifiedName(t *testing.T) {
	s := setupTestDB(t)

	ctxA := ctxns.WithNamespace(context.Background(), "a")
	ctxB := ctxns.WithNamespace(context.Background(), "b")

	s.UpsertNodes(ctxA, []model.Node{
		{QualifiedName: "pkg.F", Kind: model.NodeKindFunction, Name: "F", FilePath: "a.go", StartLine: 1, EndLine: 2, Language: "go"},
	})
	s.UpsertNodes(ctxB, []model.Node{
		{QualifiedName: "pkg.F", Kind: model.NodeKindFunction, Name: "F", FilePath: "a.go", StartLine: 1, EndLine: 2, Language: "go"},
	})

	var count int64
	s.db.Model(&model.Node{}).Where("qualified_name = ?", "pkg.F").Count(&count)
	if count != 2 {
		t.Errorf("expected 2 nodes with same QN in different namespaces, got %d", count)
	}
}

func TestNode_UniqueIndex_DuplicateWithinNamespace(t *testing.T) {
	s := setupTestDB(t)
	ctx := ctxns.WithNamespace(context.Background(), "a")

	node1 := []model.Node{
		{QualifiedName: "pkg.F", Kind: model.NodeKindFunction, Name: "F", FilePath: "a.go", StartLine: 1, EndLine: 2, Language: "go"},
	}
	if err := s.UpsertNodes(ctx, node1); err != nil {
		t.Fatalf("first UpsertNodes: %v", err)
	}

	node2 := []model.Node{
		{QualifiedName: "pkg.F", Kind: model.NodeKindFunction, Name: "F_updated", FilePath: "a.go", StartLine: 1, EndLine: 5, Language: "go"},
	}
	if err := s.UpsertNodes(ctx, node2); err != nil {
		t.Fatalf("second UpsertNodes: %v", err)
	}

	var count int64
	s.db.Model(&model.Node{}).Where("namespace = ? AND qualified_name = ?", "a", "pkg.F").Count(&count)
	if count != 1 {
		t.Errorf("expected 1 node after upsert within same namespace, got %d", count)
	}
}

func TestDeleteNode_CascadeAnnotation(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()

	s.UpsertNodes(ctx, []model.Node{
		{QualifiedName: "pkg.F", Kind: model.NodeKindFunction, Name: "F", FilePath: "a.go", StartLine: 1, EndLine: 2, Language: "go"},
	})
	node, _ := s.GetNode(ctx, "pkg.F")

	s.UpsertAnnotation(ctx, &model.Annotation{
		NodeID:  node.ID,
		Summary: "summary",
		Tags:    []model.DocTag{{Kind: model.TagParam, Name: "x", Value: "v", Ordinal: 0}},
	})

	s.DeleteNodesByFile(ctx, "a.go")

	ann, _ := s.GetAnnotation(ctx, node.ID)
	if ann != nil {
		t.Error("expected annotation to be cascade deleted")
	}
}

func TestUpsertNodes_SetsNamespaceFromContext(t *testing.T) {
	s := setupTestDB(t)
	ctx := ctxns.WithNamespace(context.Background(), "pay")

	nodes := []model.Node{
		{QualifiedName: "pkg.A", Kind: model.NodeKindFunction, Name: "A", FilePath: "a.go", StartLine: 1, EndLine: 2, Language: "go"},
	}
	if err := s.UpsertNodes(ctx, nodes); err != nil {
		t.Fatalf("UpsertNodes: %v", err)
	}

	var got model.Node
	s.db.First(&got, "qualified_name = ?", "pkg.A")
	if got.Namespace != "pay" {
		t.Errorf("Namespace = %q, want %q", got.Namespace, "pay")
	}
}

func TestUpsertNodes_EmptyNamespace_BackwardCompatible(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()

	nodes := []model.Node{
		{QualifiedName: "pkg.A", Kind: model.NodeKindFunction, Name: "A", FilePath: "a.go", StartLine: 1, EndLine: 2, Language: "go"},
	}
	if err := s.UpsertNodes(ctx, nodes); err != nil {
		t.Fatalf("UpsertNodes: %v", err)
	}

	var got model.Node
	s.db.First(&got, "qualified_name = ?", "pkg.A")
	if got.Namespace != "" {
		t.Errorf("Namespace = %q, want empty string", got.Namespace)
	}
}

func TestGetNode_FiltersByNamespace(t *testing.T) {
	s := setupTestDB(t)

	ctxA := ctxns.WithNamespace(context.Background(), "a")
	ctxB := ctxns.WithNamespace(context.Background(), "b")

	s.UpsertNodes(ctxA, []model.Node{
		{QualifiedName: "pkg.F", Kind: model.NodeKindFunction, Name: "F", FilePath: "a.go", StartLine: 1, EndLine: 2, Language: "go"},
	})

	got, err := s.GetNode(ctxB, "pkg.F")
	if err != nil {
		t.Fatalf("GetNode: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil for different namespace, got %v", got)
	}

	got, err = s.GetNode(ctxA, "pkg.F")
	if err != nil {
		t.Fatalf("GetNode: %v", err)
	}
	if got == nil {
		t.Fatal("expected node in namespace a, got nil")
	}
}

func TestGetNode_EmptyNamespace_FindsLegacyNodes(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()

	s.UpsertNodes(ctx, []model.Node{
		{QualifiedName: "pkg.F", Kind: model.NodeKindFunction, Name: "F", FilePath: "a.go", StartLine: 1, EndLine: 2, Language: "go"},
	})

	got, err := s.GetNode(ctx, "pkg.F")
	if err != nil {
		t.Fatalf("GetNode: %v", err)
	}
	if got == nil {
		t.Fatal("expected legacy node with empty namespace, got nil")
	}
}

func TestGetNodesByFile_FiltersByNamespace(t *testing.T) {
	s := setupTestDB(t)

	ctxA := ctxns.WithNamespace(context.Background(), "a")
	ctxB := ctxns.WithNamespace(context.Background(), "b")

	s.UpsertNodes(ctxA, []model.Node{
		{QualifiedName: "a.F1", Kind: model.NodeKindFunction, Name: "F1", FilePath: "shared.go", StartLine: 1, EndLine: 2, Language: "go"},
	})
	s.UpsertNodes(ctxB, []model.Node{
		{QualifiedName: "b.F1", Kind: model.NodeKindFunction, Name: "F1", FilePath: "shared.go", StartLine: 1, EndLine: 2, Language: "go"},
	})

	got, err := s.GetNodesByFile(ctxA, "shared.go")
	if err != nil {
		t.Fatalf("GetNodesByFile: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("expected 1 node in namespace a, got %d", len(got))
	}
	if len(got) > 0 && got[0].Namespace != "a" {
		t.Errorf("Namespace = %q, want %q", got[0].Namespace, "a")
	}
}

func TestGetNodesByQualifiedNames_FiltersByNamespace(t *testing.T) {
	s := setupTestDB(t)

	ctxA := ctxns.WithNamespace(context.Background(), "a")
	ctxB := ctxns.WithNamespace(context.Background(), "b")

	s.UpsertNodes(ctxA, []model.Node{
		{QualifiedName: "pkg.F", Kind: model.NodeKindFunction, Name: "F", FilePath: "a.go", StartLine: 1, EndLine: 2, Language: "go"},
	})
	s.UpsertNodes(ctxB, []model.Node{
		{QualifiedName: "pkg.F", Kind: model.NodeKindFunction, Name: "F", FilePath: "a.go", StartLine: 1, EndLine: 2, Language: "go"},
	})

	got, err := s.GetNodesByQualifiedNames(ctxA, []string{"pkg.F"})
	if err != nil {
		t.Fatalf("GetNodesByQualifiedNames: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("expected 1 node in namespace a, got %d", len(got))
	}
	if n, ok := got["pkg.F"]; ok && n.Namespace != "a" {
		t.Errorf("Namespace = %q, want %q", n.Namespace, "a")
	}
}

func TestDeleteNodesByFile_FiltersByNamespace(t *testing.T) {
	s := setupTestDB(t)

	ctxA := ctxns.WithNamespace(context.Background(), "a")
	ctxB := ctxns.WithNamespace(context.Background(), "b")

	s.UpsertNodes(ctxA, []model.Node{
		{QualifiedName: "a.F", Kind: model.NodeKindFunction, Name: "F", FilePath: "shared.go", StartLine: 1, EndLine: 2, Language: "go"},
	})
	s.UpsertNodes(ctxB, []model.Node{
		{QualifiedName: "b.F", Kind: model.NodeKindFunction, Name: "F", FilePath: "shared.go", StartLine: 1, EndLine: 2, Language: "go"},
	})

	if err := s.DeleteNodesByFile(ctxA, "shared.go"); err != nil {
		t.Fatalf("DeleteNodesByFile: %v", err)
	}

	got, _ := s.GetNode(ctxA, "a.F")
	if got != nil {
		t.Error("expected namespace a node to be deleted")
	}

	got, _ = s.GetNode(ctxB, "b.F")
	if got == nil {
		t.Error("expected namespace b node to still exist")
	}
}

func TestUpsertNodes_ConflictWithinSameNamespace(t *testing.T) {
	s := setupTestDB(t)
	ctx := ctxns.WithNamespace(context.Background(), "ns")

	s.UpsertNodes(ctx, []model.Node{
		{QualifiedName: "pkg.F", Kind: model.NodeKindFunction, Name: "F", FilePath: "a.go", StartLine: 1, EndLine: 2, Hash: "aaa", Language: "go"},
	})

	s.UpsertNodes(ctx, []model.Node{
		{QualifiedName: "pkg.F", Kind: model.NodeKindFunction, Name: "F", FilePath: "a.go", StartLine: 1, EndLine: 5, Hash: "bbb", Language: "go"},
	})

	got, _ := s.GetNode(ctx, "pkg.F")
	if got == nil {
		t.Fatal("expected node, got nil")
	}
	if got.Hash != "bbb" {
		t.Errorf("Hash = %q, want %q (should be updated)", got.Hash, "bbb")
	}
	if got.EndLine != 5 {
		t.Errorf("EndLine = %d, want 5", got.EndLine)
	}
}
