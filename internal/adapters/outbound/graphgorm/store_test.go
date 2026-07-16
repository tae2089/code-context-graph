package graphgorm

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	gormschema "gorm.io/gorm/schema"

	"github.com/tae2089/code-context-graph/internal/app/ingest"
	requestctx "github.com/tae2089/code-context-graph/internal/ctx"
	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

func edgeFingerprints(edges []graph.Edge) []string {
	fingerprints := make([]string, len(edges))
	for i, edge := range edges {
		fingerprints[i] = edge.Fingerprint
	}
	return fingerprints
}

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
	if err := db.AutoMigrate(&graph.SearchDocument{}); err != nil {
		t.Fatalf("failed to migrate search_documents: %v", err)
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
	tables := []string{"nodes", "edges", "annotations", "doc_tags", "communities", "community_memberships", "flows", "flow_memberships"}
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

func TestParseCache_RequiresCompleteIdentityAndReplacesLatestFileEntry(t *testing.T) {
	store := setupTestDB(t)
	ctx := requestctx.WithNamespace(context.Background(), "repo-a")
	v1 := ingest.ParseCacheKey{FilePath: "src/a.go", SourceHash: "hash-1", ParserVersion: "go-v1", ContextHash: "ctx-1"}
	if err := store.StoreParseResult(ctx, v1, []byte("first")); err != nil {
		t.Fatalf("store v1: %v", err)
	}
	if got, ok, err := store.LoadParseResult(ctx, v1); err != nil || !ok || string(got) != "first" {
		t.Fatalf("load v1 = %q, %v, %v", got, ok, err)
	}
	if _, ok, err := store.LoadParseResult(ctx, ingest.ParseCacheKey{FilePath: "src/a.go", SourceHash: "hash-2", ParserVersion: "go-v1", ContextHash: "ctx-1"}); err != nil || ok {
		t.Fatalf("changed source identity should miss, ok=%v err=%v", ok, err)
	}

	v2 := ingest.ParseCacheKey{FilePath: "src/a.go", SourceHash: "hash-2", ParserVersion: "go-v2", ContextHash: "ctx-2"}
	if err := store.StoreParseResult(ctx, v2, []byte("second")); err != nil {
		t.Fatalf("store v2: %v", err)
	}
	if _, ok, err := store.LoadParseResult(ctx, v1); err != nil || ok {
		t.Fatalf("replaced identity should miss, ok=%v err=%v", ok, err)
	}
	if got, ok, err := store.LoadParseResult(ctx, v2); err != nil || !ok || string(got) != "second" {
		t.Fatalf("load v2 = %q, %v, %v", got, ok, err)
	}
	otherCtx := requestctx.WithNamespace(context.Background(), "repo-b")
	if _, ok, err := store.LoadParseResult(otherCtx, v2); err != nil || ok {
		t.Fatalf("other namespace should miss, ok=%v err=%v", ok, err)
	}
}

func TestDeleteNodesByFile_RemovesOnlyMatchingNamespaceUnresolvedCandidates(t *testing.T) {
	store := setupTestDB(t)
	ctxA := requestctx.WithNamespace(context.Background(), "ns-a")
	ctxB := requestctx.WithNamespace(context.Background(), "ns-b")

	for _, ctx := range []context.Context{ctxA, ctxB} {
		if err := store.UpsertNodes(ctx, []graph.Node{{
			QualifiedName: "pkg.Source", Kind: graph.NodeKindFunction, Name: "Source", FilePath: "source.go", StartLine: 1,
		}}); err != nil {
			t.Fatalf("UpsertNodes: %v", err)
		}
		if err := store.UpsertUnresolvedEdges(ctx, []graph.UnresolvedEdgeCandidate{{
			LookupKey: "Target", Fingerprint: "calls:source.go:Target:1", FilePath: "source.go", Kind: graph.EdgeKindCalls, Line: 1,
		}}); err != nil {
			t.Fatalf("UpsertUnresolvedEdges: %v", err)
		}
	}

	if err := store.DeleteNodesByFile(ctxA, "source.go"); err != nil {
		t.Fatalf("DeleteNodesByFile: %v", err)
	}
	gotA, err := store.FindUnresolvedEdgesByLookupKeys(ctxA, []string{"Target"})
	if err != nil || len(gotA) != 0 {
		t.Fatalf("ns-a candidates = %+v, err=%v; want none", gotA, err)
	}
	gotB, err := store.FindUnresolvedEdgesByLookupKeys(ctxB, []string{"Target"})
	if err != nil || len(gotB) != 1 {
		t.Fatalf("ns-b candidates = %+v, err=%v; want one preserved", gotB, err)
	}
}

func TestUnresolvedIndexReady_RequiresMatchingVersion(t *testing.T) {
	store := setupTestDB(t)
	ctx := requestctx.WithNamespace(context.Background(), "repo-a")

	if err := store.MarkUnresolvedIndexReady(ctx, "index-v1"); err != nil {
		t.Fatalf("MarkUnresolvedIndexReady: %v", err)
	}
	ready, err := store.UnresolvedIndexReady(ctx, "index-v1")
	if err != nil || !ready {
		t.Fatalf("matching version ready = %v, err=%v; want true", ready, err)
	}
	ready, err = store.UnresolvedIndexReady(ctx, "index-v2")
	if err != nil || ready {
		t.Fatalf("mismatched version ready = %v, err=%v; want false", ready, err)
	}
	ready, err = store.UnresolvedIndexReady(requestctx.WithNamespace(context.Background(), "repo-b"), "index-v1")
	if err != nil || ready {
		t.Fatalf("other namespace ready = %v, err=%v; want false", ready, err)
	}
}

func TestUpsertNodes_Insert(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()

	nodes := []graph.Node{
		{QualifiedName: "main.Hello", Kind: graph.NodeKindFunction, Name: "Hello", FilePath: "main.go", StartLine: 1, EndLine: 3, Language: "go"},
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

	nodes := []graph.Node{
		{QualifiedName: "main.Hello", Kind: graph.NodeKindFunction, Name: "Hello", FilePath: "main.go", StartLine: 1, EndLine: 3, Hash: "aaa", Language: "go"},
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

	nodes := []graph.Node{
		{QualifiedName: "pkg.Func1", Kind: graph.NodeKindFunction, Name: "Func1", FilePath: "a.go", StartLine: 1, EndLine: 2, Language: "go"},
		{QualifiedName: "pkg.Func2", Kind: graph.NodeKindFunction, Name: "Func2", FilePath: "a.go", StartLine: 3, EndLine: 4, Language: "go"},
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

	nodes := []graph.Node{
		{QualifiedName: "pkg.A", Kind: graph.NodeKindFunction, Name: "A", FilePath: "a.go", StartLine: 1, EndLine: 2, Language: "go"},
		{QualifiedName: "pkg.B", Kind: graph.NodeKindFunction, Name: "B", FilePath: "a.go", StartLine: 3, EndLine: 4, Language: "go"},
		{QualifiedName: "pkg.C", Kind: graph.NodeKindFunction, Name: "C", FilePath: "b.go", StartLine: 1, EndLine: 2, Language: "go"},
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

	nodes := []graph.Node{
		{QualifiedName: "pkg.A", Kind: graph.NodeKindFunction, Name: "A", FilePath: "a.go", StartLine: 1, EndLine: 2, Language: "go"},
		{QualifiedName: "pkg.B", Kind: graph.NodeKindFunction, Name: "B", FilePath: "b.go", StartLine: 1, EndLine: 2, Language: "go"},
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

	nodes := []graph.Node{
		{QualifiedName: "pkg.A", Kind: graph.NodeKindFunction, Name: "A", FilePath: "a.go", StartLine: 1, EndLine: 2, Language: "go"},
		{QualifiedName: "pkg.B", Kind: graph.NodeKindFunction, Name: "B", FilePath: "a.go", StartLine: 3, EndLine: 4, Language: "go"},
	}
	if err := s.UpsertNodes(ctx, nodes); err != nil {
		t.Fatalf("UpsertNodes: %v", err)
	}

	nodeA, _ := s.GetNode(ctx, "pkg.A")
	nodeB, _ := s.GetNode(ctx, "pkg.B")

	edges := []graph.Edge{
		{FromNodeID: nodeA.ID, ToNodeID: nodeB.ID, Kind: graph.EdgeKindCalls, FilePath: "a.go", Line: 2, Fingerprint: "calls:a.go:B:2"},
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
	if got[0].Kind != graph.EdgeKindCalls {
		t.Errorf("Kind = %q, want %q", got[0].Kind, graph.EdgeKindCalls)
	}
}

func TestUpsertEdges_Dedup(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()

	nodes := []graph.Node{
		{QualifiedName: "pkg.A", Kind: graph.NodeKindFunction, Name: "A", FilePath: "a.go", StartLine: 1, EndLine: 2, Language: "go"},
		{QualifiedName: "pkg.B", Kind: graph.NodeKindFunction, Name: "B", FilePath: "a.go", StartLine: 3, EndLine: 4, Language: "go"},
	}
	s.UpsertNodes(ctx, nodes)
	nodeA, _ := s.GetNode(ctx, "pkg.A")
	nodeB, _ := s.GetNode(ctx, "pkg.B")

	edge := graph.Edge{FromNodeID: nodeA.ID, ToNodeID: nodeB.ID, Kind: graph.EdgeKindCalls, FilePath: "a.go", Line: 2, Fingerprint: "calls:a.go:B:2"}
	s.UpsertEdges(ctx, []graph.Edge{edge})
	s.UpsertEdges(ctx, []graph.Edge{edge})

	got, _ := s.GetEdgesFrom(ctx, nodeA.ID)
	if len(got) != 1 {
		t.Errorf("expected 1 edge after dedup, got %d", len(got))
	}
}

func TestGetEdgesFrom(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()

	nodes := []graph.Node{
		{QualifiedName: "pkg.A", Kind: graph.NodeKindFunction, Name: "A", FilePath: "a.go", StartLine: 1, EndLine: 2, Language: "go"},
		{QualifiedName: "pkg.B", Kind: graph.NodeKindFunction, Name: "B", FilePath: "a.go", StartLine: 3, EndLine: 4, Language: "go"},
		{QualifiedName: "pkg.C", Kind: graph.NodeKindFunction, Name: "C", FilePath: "a.go", StartLine: 5, EndLine: 6, Language: "go"},
	}
	s.UpsertNodes(ctx, nodes)
	nodeA, _ := s.GetNode(ctx, "pkg.A")
	nodeB, _ := s.GetNode(ctx, "pkg.B")
	nodeC, _ := s.GetNode(ctx, "pkg.C")

	s.UpsertEdges(ctx, []graph.Edge{
		{FromNodeID: nodeA.ID, ToNodeID: nodeB.ID, Kind: graph.EdgeKindCalls, FilePath: "a.go", Line: 2, Fingerprint: "calls:a.go:B:2"},
		{FromNodeID: nodeA.ID, ToNodeID: nodeC.ID, Kind: graph.EdgeKindCalls, FilePath: "a.go", Line: 3, Fingerprint: "calls:a.go:C:3"},
	})

	got, err := s.GetEdgesFrom(ctx, nodeA.ID)
	if err != nil {
		t.Fatalf("GetEdgesFrom: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("expected 2 edges, got %d", len(got))
	}
}

func TestGetEdgesFrom_OrdersBySourceLocation(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()

	nodes := []graph.Node{
		{QualifiedName: "pkg.A", Kind: graph.NodeKindFunction, Name: "A", FilePath: "root.go", StartLine: 1, EndLine: 2, Language: "go"},
		{QualifiedName: "pkg.B", Kind: graph.NodeKindFunction, Name: "B", FilePath: "root.go", StartLine: 3, EndLine: 4, Language: "go"},
		{QualifiedName: "pkg.C", Kind: graph.NodeKindFunction, Name: "C", FilePath: "root.go", StartLine: 5, EndLine: 6, Language: "go"},
		{QualifiedName: "pkg.D", Kind: graph.NodeKindFunction, Name: "D", FilePath: "root.go", StartLine: 7, EndLine: 8, Language: "go"},
	}
	if err := s.UpsertNodes(ctx, nodes); err != nil {
		t.Fatalf("UpsertNodes: %v", err)
	}
	nodeA, _ := s.GetNode(ctx, "pkg.A")
	nodeB, _ := s.GetNode(ctx, "pkg.B")
	nodeC, _ := s.GetNode(ctx, "pkg.C")
	nodeD, _ := s.GetNode(ctx, "pkg.D")

	if err := s.UpsertEdges(ctx, []graph.Edge{
		{FromNodeID: nodeA.ID, ToNodeID: nodeB.ID, Kind: graph.EdgeKindCalls, FilePath: "z.go", Line: 30, Fingerprint: "edge-z"},
		{FromNodeID: nodeA.ID, ToNodeID: nodeC.ID, Kind: graph.EdgeKindCalls, FilePath: "a.go", Line: 20, Fingerprint: "edge-a20"},
		{FromNodeID: nodeA.ID, ToNodeID: nodeD.ID, Kind: graph.EdgeKindCalls, FilePath: "a.go", Line: 10, Fingerprint: "edge-a10"},
	}); err != nil {
		t.Fatalf("UpsertEdges: %v", err)
	}

	got, err := s.GetEdgesFrom(ctx, nodeA.ID)
	if err != nil {
		t.Fatalf("GetEdgesFrom: %v", err)
	}
	if got, want := edgeFingerprints(got), []string{"edge-a10", "edge-a20", "edge-z"}; !slices.Equal(got, want) {
		t.Fatalf("fingerprints = %v, want %v", got, want)
	}
}

func TestGetEdgesTo(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()

	nodes := []graph.Node{
		{QualifiedName: "pkg.A", Kind: graph.NodeKindFunction, Name: "A", FilePath: "a.go", StartLine: 1, EndLine: 2, Language: "go"},
		{QualifiedName: "pkg.B", Kind: graph.NodeKindFunction, Name: "B", FilePath: "a.go", StartLine: 3, EndLine: 4, Language: "go"},
	}
	s.UpsertNodes(ctx, nodes)
	nodeA, _ := s.GetNode(ctx, "pkg.A")
	nodeB, _ := s.GetNode(ctx, "pkg.B")

	s.UpsertEdges(ctx, []graph.Edge{
		{FromNodeID: nodeA.ID, ToNodeID: nodeB.ID, Kind: graph.EdgeKindCalls, FilePath: "a.go", Line: 2, Fingerprint: "calls:a.go:B:2"},
	})

	got, err := s.GetEdgesTo(ctx, nodeB.ID)
	if err != nil {
		t.Fatalf("GetEdgesTo: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("expected 1 edge, got %d", len(got))
	}
}

func TestGetEdgesFromNodes_OrdersBySourceLocation(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()

	nodes := []graph.Node{
		{QualifiedName: "pkg.A", Kind: graph.NodeKindFunction, Name: "A", FilePath: "root.go", StartLine: 1, EndLine: 2, Language: "go"},
		{QualifiedName: "pkg.B", Kind: graph.NodeKindFunction, Name: "B", FilePath: "root.go", StartLine: 3, EndLine: 4, Language: "go"},
		{QualifiedName: "pkg.C", Kind: graph.NodeKindFunction, Name: "C", FilePath: "root.go", StartLine: 5, EndLine: 6, Language: "go"},
		{QualifiedName: "pkg.D", Kind: graph.NodeKindFunction, Name: "D", FilePath: "root.go", StartLine: 7, EndLine: 8, Language: "go"},
	}
	if err := s.UpsertNodes(ctx, nodes); err != nil {
		t.Fatalf("UpsertNodes: %v", err)
	}
	nodeA, _ := s.GetNode(ctx, "pkg.A")
	nodeB, _ := s.GetNode(ctx, "pkg.B")
	nodeC, _ := s.GetNode(ctx, "pkg.C")
	nodeD, _ := s.GetNode(ctx, "pkg.D")

	if err := s.UpsertEdges(ctx, []graph.Edge{
		{FromNodeID: nodeA.ID, ToNodeID: nodeC.ID, Kind: graph.EdgeKindCalls, FilePath: "b.go", Line: 20, Fingerprint: "edge-b20"},
		{FromNodeID: nodeB.ID, ToNodeID: nodeD.ID, Kind: graph.EdgeKindCalls, FilePath: "a.go", Line: 30, Fingerprint: "edge-a30"},
		{FromNodeID: nodeA.ID, ToNodeID: nodeD.ID, Kind: graph.EdgeKindCalls, FilePath: "a.go", Line: 10, Fingerprint: "edge-a10"},
	}); err != nil {
		t.Fatalf("UpsertEdges: %v", err)
	}

	got, err := s.GetEdgesFromNodes(ctx, []uint{nodeA.ID, nodeB.ID})
	if err != nil {
		t.Fatalf("GetEdgesFromNodes: %v", err)
	}
	if got, want := edgeFingerprints(got), []string{"edge-a10", "edge-a30", "edge-b20"}; !slices.Equal(got, want) {
		t.Fatalf("fingerprints = %v, want %v", got, want)
	}
}

func TestGetEdgesToNodes_OrdersBySourceLocation(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()

	nodes := []graph.Node{
		{QualifiedName: "pkg.A", Kind: graph.NodeKindFunction, Name: "A", FilePath: "root.go", StartLine: 1, EndLine: 2, Language: "go"},
		{QualifiedName: "pkg.B", Kind: graph.NodeKindFunction, Name: "B", FilePath: "root.go", StartLine: 3, EndLine: 4, Language: "go"},
		{QualifiedName: "pkg.C", Kind: graph.NodeKindFunction, Name: "C", FilePath: "root.go", StartLine: 5, EndLine: 6, Language: "go"},
		{QualifiedName: "pkg.D", Kind: graph.NodeKindFunction, Name: "D", FilePath: "root.go", StartLine: 7, EndLine: 8, Language: "go"},
	}
	if err := s.UpsertNodes(ctx, nodes); err != nil {
		t.Fatalf("UpsertNodes: %v", err)
	}
	nodeA, _ := s.GetNode(ctx, "pkg.A")
	nodeB, _ := s.GetNode(ctx, "pkg.B")
	nodeC, _ := s.GetNode(ctx, "pkg.C")
	nodeD, _ := s.GetNode(ctx, "pkg.D")

	if err := s.UpsertEdges(ctx, []graph.Edge{
		{FromNodeID: nodeA.ID, ToNodeID: nodeD.ID, Kind: graph.EdgeKindCalls, FilePath: "c.go", Line: 50, Fingerprint: "edge-c50"},
		{FromNodeID: nodeB.ID, ToNodeID: nodeC.ID, Kind: graph.EdgeKindCalls, FilePath: "a.go", Line: 20, Fingerprint: "edge-a20"},
		{FromNodeID: nodeA.ID, ToNodeID: nodeC.ID, Kind: graph.EdgeKindCalls, FilePath: "a.go", Line: 10, Fingerprint: "edge-a10"},
	}); err != nil {
		t.Fatalf("UpsertEdges: %v", err)
	}

	got, err := s.GetEdgesToNodes(ctx, []uint{nodeC.ID, nodeD.ID})
	if err != nil {
		t.Fatalf("GetEdgesToNodes: %v", err)
	}
	if got, want := edgeFingerprints(got), []string{"edge-a10", "edge-a20", "edge-c50"}; !slices.Equal(got, want) {
		t.Fatalf("fingerprints = %v, want %v", got, want)
	}
}

func TestDeleteEdgesByFile(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()

	nodes := []graph.Node{
		{QualifiedName: "pkg.A", Kind: graph.NodeKindFunction, Name: "A", FilePath: "a.go", StartLine: 1, EndLine: 2, Language: "go"},
		{QualifiedName: "pkg.B", Kind: graph.NodeKindFunction, Name: "B", FilePath: "a.go", StartLine: 3, EndLine: 4, Language: "go"},
	}
	s.UpsertNodes(ctx, nodes)
	nodeA, _ := s.GetNode(ctx, "pkg.A")
	nodeB, _ := s.GetNode(ctx, "pkg.B")

	s.UpsertEdges(ctx, []graph.Edge{
		{FromNodeID: nodeA.ID, ToNodeID: nodeB.ID, Kind: graph.EdgeKindCalls, FilePath: "a.go", Line: 2, Fingerprint: "calls:a.go:B:2"},
	})

	if err := s.DeleteEdgesByFile(ctx, "a.go"); err != nil {
		t.Fatalf("DeleteEdgesByFile: %v", err)
	}

	got, _ := s.GetEdgesFrom(ctx, nodeA.ID)
	if len(got) != 0 {
		t.Errorf("expected 0 edges after delete, got %d", len(got))
	}
}

func TestDeleteGraph_RemovesUnresolvedEdgesByFilePath(t *testing.T) {
	s := setupTestDB(t)
	ctx := requestctx.WithNamespace(context.Background(), "ns-a")

	if err := s.UpsertNodes(ctx, []graph.Node{{
		QualifiedName: "pkg.A",
		Kind:          graph.NodeKindFunction,
		Name:          "A",
		FilePath:      "a.go",
		StartLine:     1,
		EndLine:       2,
		Language:      "go",
	}}); err != nil {
		t.Fatalf("UpsertNodes: %v", err)
	}

	if err := s.UpsertEdges(ctx, []graph.Edge{
		{Kind: graph.EdgeKindCalls, FilePath: "a.go", Line: 1, Fingerprint: "calls:a.go:pkg.B:1"},
		{Kind: graph.EdgeKindContains, FilePath: "a.go", Line: 1, Fingerprint: "contains:a.go:pkg.A"},
	}); err != nil {
		t.Fatalf("UpsertEdges: %v", err)
	}

	if err := s.DeleteGraph(ctx); err != nil {
		t.Fatalf("DeleteGraph: %v", err)
	}

	var edgeCount int64
	if err := s.db.Model(&graph.Edge{}).Where("file_path = ?", "a.go").Count(&edgeCount).Error; err != nil {
		t.Fatalf("count edges: %v", err)
	}
	if edgeCount != 0 {
		t.Fatalf("expected 0 unresolved/file-owned edges after DeleteGraph, got %d", edgeCount)
	}
}

func TestUpsertEdges_AllowsSameFingerprintAcrossNamespaces(t *testing.T) {
	s := setupTestDB(t)

	ctxA := requestctx.WithNamespace(context.Background(), "ns-a")
	ctxB := requestctx.WithNamespace(context.Background(), "ns-b")

	if err := s.UpsertEdges(ctxA, []graph.Edge{{Kind: graph.EdgeKindCalls, FilePath: "shared.go", Line: 1, Fingerprint: "shared-fp"}}); err != nil {
		t.Fatalf("UpsertEdges ns-a: %v", err)
	}
	if err := s.UpsertEdges(ctxB, []graph.Edge{{Kind: graph.EdgeKindCalls, FilePath: "shared.go", Line: 1, Fingerprint: "shared-fp"}}); err != nil {
		t.Fatalf("UpsertEdges ns-b: %v", err)
	}

	var count int64
	if err := s.db.Model(&graph.Edge{}).Where("fingerprint = ?", "shared-fp").Count(&count).Error; err != nil {
		t.Fatalf("count edges: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected 2 namespaced edges with same fingerprint, got %d", count)
	}
}

func TestDeleteEdgesByFile_FiltersByNamespace(t *testing.T) {
	s := setupTestDB(t)

	ctxA := requestctx.WithNamespace(context.Background(), "ns-a")
	ctxB := requestctx.WithNamespace(context.Background(), "ns-b")

	if err := s.UpsertEdges(ctxA, []graph.Edge{{Kind: graph.EdgeKindCalls, FilePath: "shared.go", Line: 1, Fingerprint: "a-fp"}}); err != nil {
		t.Fatalf("UpsertEdges ns-a: %v", err)
	}
	if err := s.UpsertEdges(ctxB, []graph.Edge{{Kind: graph.EdgeKindCalls, FilePath: "shared.go", Line: 1, Fingerprint: "b-fp"}}); err != nil {
		t.Fatalf("UpsertEdges ns-b: %v", err)
	}

	if err := s.DeleteEdgesByFile(ctxA, "shared.go"); err != nil {
		t.Fatalf("DeleteEdgesByFile: %v", err)
	}

	var countA, countB int64
	s.db.Model(&graph.Edge{}).Where("namespace = ?", "ns-a").Count(&countA)
	s.db.Model(&graph.Edge{}).Where("namespace = ?", "ns-b").Count(&countB)
	if countA != 0 {
		t.Fatalf("expected ns-a edges deleted, got %d", countA)
	}
	if countB != 1 {
		t.Fatalf("expected ns-b edges preserved, got %d", countB)
	}
}

func TestListFileNodes_ReturnsMinimalNonPackageStateForNamespace(t *testing.T) {
	s := setupTestDB(t)
	ctxA := requestctx.WithNamespace(context.Background(), "ns-a")
	ctxB := requestctx.WithNamespace(context.Background(), "ns-b")

	if err := s.UpsertNodes(ctxA, []graph.Node{
		{QualifiedName: "a.go", Kind: graph.NodeKindFile, Name: "a.go", FilePath: "a.go", Hash: "hash-a"},
		{QualifiedName: "pkg/a", Kind: graph.NodeKindPackage, Name: "a", FilePath: "a.go"},
	}); err != nil {
		t.Fatalf("UpsertNodes(ns-a): %v", err)
	}
	if err := s.UpsertNodes(ctxB, []graph.Node{
		{QualifiedName: "b.go", Kind: graph.NodeKindFile, Name: "b.go", FilePath: "b.go", Hash: "hash-b"},
	}); err != nil {
		t.Fatalf("UpsertNodes(ns-b): %v", err)
	}

	nodes, err := s.ListFileNodes(ctxA)
	if err != nil {
		t.Fatalf("ListFileNodes: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("ListFileNodes returned %d nodes, want 1: %#v", len(nodes), nodes)
	}
	if nodes[0].FilePath != "a.go" || nodes[0].Hash != "hash-a" || nodes[0].ID == 0 {
		t.Fatalf("ListFileNodes returned unexpected projection: %#v", nodes[0])
	}
	if nodes[0].QualifiedName != "" || nodes[0].Name != "" {
		t.Fatalf("ListFileNodes loaded fields outside the minimal projection: %#v", nodes[0])
	}
}

func TestListImportFileNodes_ReturnsOnlyFileNodesForNamespace(t *testing.T) {
	s := setupTestDB(t)
	ctxA := requestctx.WithNamespace(context.Background(), "ns-a")
	ctxB := requestctx.WithNamespace(context.Background(), "ns-b")
	if err := s.UpsertNodes(ctxA, []graph.Node{
		{QualifiedName: "a.go", Kind: graph.NodeKindFile, Name: "a.go", FilePath: "a.go", Hash: "hash-a"},
		{QualifiedName: "pkg.A", Kind: graph.NodeKindFunction, Name: "A", FilePath: "a.go", Hash: "hash-a"},
	}); err != nil {
		t.Fatalf("UpsertNodes(ns-a): %v", err)
	}
	if err := s.UpsertNodes(ctxB, []graph.Node{{QualifiedName: "b.go", Kind: graph.NodeKindFile, Name: "b.go", FilePath: "b.go"}}); err != nil {
		t.Fatalf("UpsertNodes(ns-b): %v", err)
	}

	nodes, err := s.ListImportFileNodes(ctxA)
	if err != nil {
		t.Fatalf("ListImportFileNodes: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("ListImportFileNodes returned %d nodes, want 1: %#v", len(nodes), nodes)
	}
	if nodes[0].ID == 0 || nodes[0].Kind != graph.NodeKindFile || nodes[0].FilePath != "a.go" {
		t.Fatalf("ListImportFileNodes returned unexpected node: %#v", nodes[0])
	}
}

func TestDeletePackageSemanticEdges_FiltersByNamespaceAnchorKindAndLine(t *testing.T) {
	s := setupTestDB(t)
	ctxA := requestctx.WithNamespace(context.Background(), "ns-a")
	ctxB := requestctx.WithNamespace(context.Background(), "ns-b")

	edges := []graph.Edge{
		{Kind: graph.EdgeKindImplements, FilePath: "pkg/a.go", Line: 0, Fingerprint: "delete-me"},
		{Kind: graph.EdgeKindImplements, FilePath: "pkg/a.go", Line: 12, Fingerprint: "keep-line"},
		{Kind: graph.EdgeKindCalls, FilePath: "pkg/a.go", Line: 0, Fingerprint: "keep-kind"},
		{Kind: graph.EdgeKindImplements, FilePath: "pkg/other.go", Line: 0, Fingerprint: "keep-anchor"},
	}
	if err := s.UpsertEdges(ctxA, edges); err != nil {
		t.Fatalf("UpsertEdges(ns-a): %v", err)
	}
	if err := s.UpsertEdges(ctxB, []graph.Edge{{Kind: graph.EdgeKindImplements, FilePath: "pkg/a.go", Line: 0, Fingerprint: "keep-namespace"}}); err != nil {
		t.Fatalf("UpsertEdges(ns-b): %v", err)
	}

	if err := s.DeletePackageSemanticEdges(ctxA, []string{"pkg/a.go"}); err != nil {
		t.Fatalf("DeletePackageSemanticEdges: %v", err)
	}

	remainingA, err := s.GetEdgesFromNodes(ctxA, []uint{0})
	if err != nil {
		t.Fatalf("GetEdgesFromNodes(ns-a): %v", err)
	}
	if got, want := edgeFingerprints(remainingA), []string{"keep-kind", "keep-line", "keep-anchor"}; !slices.Equal(got, want) {
		t.Fatalf("remaining ns-a fingerprints = %v, want %v", got, want)
	}
	remainingB, err := s.GetEdgesFromNodes(ctxB, []uint{0})
	if err != nil {
		t.Fatalf("GetEdgesFromNodes(ns-b): %v", err)
	}
	if got, want := edgeFingerprints(remainingB), []string{"keep-namespace"}; !slices.Equal(got, want) {
		t.Fatalf("remaining ns-b fingerprints = %v, want %v", got, want)
	}
}

func TestDeleteGraph_FiltersUnresolvedEdgesByNamespace(t *testing.T) {
	s := setupTestDB(t)

	ctxA := requestctx.WithNamespace(context.Background(), "ns-a")
	ctxB := requestctx.WithNamespace(context.Background(), "ns-b")

	if err := s.UpsertNodes(ctxA, []graph.Node{{QualifiedName: "pkg.A", Kind: graph.NodeKindFunction, Name: "A", FilePath: "shared.go", StartLine: 1, EndLine: 2, Language: "go"}}); err != nil {
		t.Fatalf("UpsertNodes ns-a: %v", err)
	}
	if err := s.UpsertNodes(ctxB, []graph.Node{{QualifiedName: "pkg.B", Kind: graph.NodeKindFunction, Name: "B", FilePath: "shared.go", StartLine: 1, EndLine: 2, Language: "go"}}); err != nil {
		t.Fatalf("UpsertNodes ns-b: %v", err)
	}
	if err := s.UpsertEdges(ctxA, []graph.Edge{{Kind: graph.EdgeKindCalls, FilePath: "shared.go", Line: 1, Fingerprint: "ns-a-edge"}}); err != nil {
		t.Fatalf("UpsertEdges ns-a: %v", err)
	}
	if err := s.UpsertEdges(ctxB, []graph.Edge{{Kind: graph.EdgeKindCalls, FilePath: "shared.go", Line: 1, Fingerprint: "ns-b-edge"}}); err != nil {
		t.Fatalf("UpsertEdges ns-b: %v", err)
	}

	if err := s.DeleteGraph(ctxA); err != nil {
		t.Fatalf("DeleteGraph ns-a: %v", err)
	}

	var countA, countB int64
	s.db.Model(&graph.Edge{}).Where("namespace = ?", "ns-a").Count(&countA)
	s.db.Model(&graph.Edge{}).Where("namespace = ?", "ns-b").Count(&countB)
	if countA != 0 {
		t.Fatalf("expected ns-a unresolved edges deleted, got %d", countA)
	}
	if countB != 1 {
		t.Fatalf("expected ns-b unresolved edges preserved, got %d", countB)
	}
}

func TestUpsertAnnotation_Insert(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()

	s.UpsertNodes(ctx, []graph.Node{
		{QualifiedName: "pkg.F", Kind: graph.NodeKindFunction, Name: "F", FilePath: "a.go", StartLine: 1, EndLine: 2, Language: "go"},
	})
	node, _ := s.GetNode(ctx, "pkg.F")

	ann := &graph.Annotation{
		NodeID:  node.ID,
		Summary: "does something",
		Context: "called from handler",
		RawText: "does something\ncalled from handler",
		Tags: []graph.DocTag{
			{Kind: graph.TagParam, Name: "x", Value: "input value", Ordinal: 0},
			{Kind: graph.TagReturn, Value: "result", Ordinal: 0},
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

func TestUpsertAnnotation_RejectsNodeOutsideNamespace(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()

	s.UpsertNodes(ctx, []graph.Node{
		{QualifiedName: "pkg.F", Kind: graph.NodeKindFunction, Name: "F", FilePath: "a.go", StartLine: 1, EndLine: 2, Language: "go"},
	})
	node, _ := s.GetNode(ctx, "pkg.F")

	// Writing from another namespace must not create an annotation on a foreign node.
	foreignCtx := requestctx.WithNamespace(context.Background(), "other-team")
	err := s.UpsertAnnotation(foreignCtx, &graph.Annotation{NodeID: node.ID, Summary: "cross-namespace write"})
	if err == nil {
		t.Fatal("expected cross-namespace annotation create to be rejected")
	}
	if got, _ := s.GetAnnotation(ctx, node.ID); got != nil {
		t.Fatalf("annotation must not exist after rejected write, got %+v", got)
	}

	// Nonexistent node id must also be rejected instead of creating an orphan row.
	if err := s.UpsertAnnotation(ctx, &graph.Annotation{NodeID: 99999, Summary: "orphan"}); err == nil {
		t.Fatal("expected annotation create for missing node to be rejected")
	}
}

func TestUpsertAnnotation_Update(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()

	s.UpsertNodes(ctx, []graph.Node{
		{QualifiedName: "pkg.F", Kind: graph.NodeKindFunction, Name: "F", FilePath: "a.go", StartLine: 1, EndLine: 2, Language: "go"},
	})
	node, _ := s.GetNode(ctx, "pkg.F")

	ann1 := &graph.Annotation{
		NodeID:  node.ID,
		Summary: "old summary",
		Tags: []graph.DocTag{
			{Kind: graph.TagParam, Name: "x", Value: "old", Ordinal: 0},
		},
	}
	s.UpsertAnnotation(ctx, ann1)

	ann2 := &graph.Annotation{
		NodeID:  node.ID,
		Summary: "new summary",
		Tags: []graph.DocTag{
			{Kind: graph.TagReturn, Value: "new result", Ordinal: 0},
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
	if got.Tags[0].Kind != graph.TagReturn {
		t.Errorf("Tag Kind = %q, want %q", got.Tags[0].Kind, graph.TagReturn)
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

	s.UpsertNodes(ctx, []graph.Node{
		{QualifiedName: "pkg.F", Kind: graph.NodeKindFunction, Name: "F", FilePath: "a.go", StartLine: 1, EndLine: 2, Language: "go"},
	})
	node, _ := s.GetNode(ctx, "pkg.F")

	ann := &graph.Annotation{
		NodeID:  node.ID,
		Summary: "summary",
		Tags: []graph.DocTag{
			{Kind: graph.TagParam, Name: "a", Value: "first", Ordinal: 0},
			{Kind: graph.TagParam, Name: "b", Value: "second", Ordinal: 1},
			{Kind: graph.TagIntent, Value: "do stuff", Ordinal: 0},
		},
	}
	s.UpsertAnnotation(ctx, ann)

	got, _ := s.GetAnnotation(ctx, node.ID)
	if len(got.Tags) != 3 {
		t.Fatalf("expected 3 tags, got %d", len(got.Tags))
	}
}

type failFirstDocTagCreatePlugin struct{}

func (p *failFirstDocTagCreatePlugin) Name() string { return "fail-first-doc-tag-create" }

func (p *failFirstDocTagCreatePlugin) Initialize(db *gorm.DB) error {
	return db.Callback().Create().Before("gorm:create").Register("fail-first-doc-tag-create", func(tx *gorm.DB) {
		if tx.Statement == nil || tx.Statement.Schema == nil {
			return
		}
		if tx.Statement.Schema.Name == "DocTag" {
			tx.AddError(fmt.Errorf("boom"))
		}
	})
}

func TestUpsertAnnotation_Insert_RollsBackOnTagFailure(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger:                 logger.Discard,
		SkipDefaultTransaction: true,
		NamingStrategy:         gormschema.NamingStrategy{},
	})
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}
	if err := db.Use(&failFirstDocTagCreatePlugin{}); err != nil {
		t.Fatalf("register plugin: %v", err)
	}
	s := New(db)
	if err := s.AutoMigrate(); err != nil {
		t.Fatalf("failed to migrate: %v", err)
	}

	ctx := context.Background()
	if err := s.UpsertNodes(ctx, []graph.Node{{QualifiedName: "pkg.F", Kind: graph.NodeKindFunction, Name: "F", FilePath: "a.go", StartLine: 1, EndLine: 2, Language: "go"}}); err != nil {
		t.Fatalf("UpsertNodes: %v", err)
	}
	node, _ := s.GetNode(ctx, "pkg.F")

	err = s.UpsertAnnotation(ctx, &graph.Annotation{
		NodeID:  node.ID,
		Summary: "does something",
		Tags: []graph.DocTag{
			{Kind: graph.TagParam, Name: "x", Value: "input value", Ordinal: 0},
		},
	})
	if err == nil {
		t.Fatal("expected UpsertAnnotation insert to fail")
	}

	var annCount int64
	if err := db.Model(&graph.Annotation{}).Where("node_id = ?", node.ID).Count(&annCount).Error; err != nil {
		t.Fatalf("count annotations: %v", err)
	}
	if annCount != 0 {
		t.Fatalf("expected annotation insert to roll back, got %d rows", annCount)
	}
}

func TestGetNodeByID_FiltersByNamespace(t *testing.T) {
	s := setupTestDB(t)
	ctxA := requestctx.WithNamespace(context.Background(), "a")
	ctxB := requestctx.WithNamespace(context.Background(), "b")

	if err := s.UpsertNodes(ctxA, []graph.Node{{QualifiedName: "pkg.F", Kind: graph.NodeKindFunction, Name: "F", FilePath: "a.go", StartLine: 1, EndLine: 2, Language: "go"}}); err != nil {
		t.Fatalf("UpsertNodes: %v", err)
	}
	node, _ := s.GetNode(ctxA, "pkg.F")

	got, err := s.GetNodeByID(ctxB, node.ID)
	if err != nil {
		t.Fatalf("GetNodeByID: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil outside namespace, got %+v", got)
	}
}

func TestGetNodesByIDs_FiltersByNamespace(t *testing.T) {
	s := setupTestDB(t)
	ctxA := requestctx.WithNamespace(context.Background(), "a")
	ctxB := requestctx.WithNamespace(context.Background(), "b")

	if err := s.UpsertNodes(ctxA, []graph.Node{{QualifiedName: "pkg.F", Kind: graph.NodeKindFunction, Name: "F", FilePath: "a.go", StartLine: 1, EndLine: 2, Language: "go"}}); err != nil {
		t.Fatalf("UpsertNodes: %v", err)
	}
	node, _ := s.GetNode(ctxA, "pkg.F")

	got, err := s.GetNodesByIDs(ctxB, []uint{node.ID})
	if err != nil {
		t.Fatalf("GetNodesByIDs: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected no nodes outside namespace, got %d", len(got))
	}
}

func TestGetAnnotation_FiltersByNamespace(t *testing.T) {
	s := setupTestDB(t)
	ctxA := requestctx.WithNamespace(context.Background(), "a")
	ctxB := requestctx.WithNamespace(context.Background(), "b")

	if err := s.UpsertNodes(ctxA, []graph.Node{{QualifiedName: "pkg.F", Kind: graph.NodeKindFunction, Name: "F", FilePath: "a.go", StartLine: 1, EndLine: 2, Language: "go"}}); err != nil {
		t.Fatalf("UpsertNodes: %v", err)
	}
	node, _ := s.GetNode(ctxA, "pkg.F")
	if err := s.UpsertAnnotation(ctxA, &graph.Annotation{NodeID: node.ID, Summary: "private"}); err != nil {
		t.Fatalf("UpsertAnnotation: %v", err)
	}

	got, err := s.GetAnnotation(ctxB, node.ID)
	if err != nil {
		t.Fatalf("GetAnnotation: %v", err)
	}
	if got != nil {
		t.Fatal("expected nil annotation outside namespace")
	}
}

func TestWithTx_Success(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()

	err := s.WithTx(ctx, func(txStore ingest.GraphStore) error {
		return txStore.UpsertNodes(ctx, []graph.Node{
			{QualifiedName: "tx.A", Kind: graph.NodeKindFunction, Name: "A", FilePath: "a.go", StartLine: 1, EndLine: 2, Language: "go"},
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

	err := s.WithTx(ctx, func(txStore ingest.GraphStore) error {
		txStore.UpsertNodes(ctx, []graph.Node{
			{QualifiedName: "tx.B", Kind: graph.NodeKindFunction, Name: "B", FilePath: "a.go", StartLine: 1, EndLine: 2, Language: "go"},
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

func TestWithTxDB_CommitsGraphAndSearchDocumentTogether(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()

	err := s.WithTxDB(ctx, func(txStore ingest.GraphStore, txDB *gorm.DB) error {
		return writeGraphAndSearchDocument(ctx, txStore, txDB, "txdb.Commit", "committed search document")
	})
	if err != nil {
		t.Fatalf("WithTxDB: %v", err)
	}

	assertGraphAndSearchDocumentCounts(t, s.db, "txdb.Commit", "committed search document", 1)
}

func TestWithTxDB_RollsBackGraphAndSearchDocumentTogether(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()
	wantErr := errors.New("rollback graph and search")

	err := s.WithTxDB(ctx, func(txStore ingest.GraphStore, txDB *gorm.DB) error {
		if err := writeGraphAndSearchDocument(ctx, txStore, txDB, "txdb.Rollback", "rolled back search document"); err != nil {
			return err
		}
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("WithTxDB error = %v, want %v", err, wantErr)
	}

	assertGraphAndSearchDocumentCounts(t, s.db, "txdb.Rollback", "rolled back search document", 0)
}

func writeGraphAndSearchDocument(ctx context.Context, txStore ingest.GraphStore, txDB *gorm.DB, qualifiedName, content string) error {
	if err := txStore.UpsertNodes(ctx, []graph.Node{{
		QualifiedName: qualifiedName,
		Kind:          graph.NodeKindFunction,
		Name:          qualifiedName,
		FilePath:      qualifiedName + ".go",
		StartLine:     1,
		EndLine:       2,
		Language:      "go",
	}}); err != nil {
		return err
	}
	var node graph.Node
	if err := txDB.WithContext(ctx).Where("namespace = ? AND qualified_name = ?", requestctx.FromContext(ctx), qualifiedName).First(&node).Error; err != nil {
		return err
	}
	return txDB.WithContext(ctx).Create(&graph.SearchDocument{
		Namespace: node.Namespace,
		NodeID:    node.ID,
		Content:   content,
		Language:  node.Language,
	}).Error
}

func assertGraphAndSearchDocumentCounts(t *testing.T, db *gorm.DB, qualifiedName, content string, want int64) {
	t.Helper()

	var nodeCount int64
	if err := db.Model(&graph.Node{}).Where("qualified_name = ?", qualifiedName).Count(&nodeCount).Error; err != nil {
		t.Fatalf("count graph nodes: %v", err)
	}
	if nodeCount != want {
		t.Fatalf("graph node count = %d, want %d", nodeCount, want)
	}

	var documentCount int64
	if err := db.Model(&graph.SearchDocument{}).Where("content = ?", content).Count(&documentCount).Error; err != nil {
		t.Fatalf("count search documents: %v", err)
	}
	if documentCount != want {
		t.Fatalf("search document count = %d, want %d", documentCount, want)
	}
}

func TestDeleteNode_CascadeEdges(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()

	s.UpsertNodes(ctx, []graph.Node{
		{QualifiedName: "pkg.A", Kind: graph.NodeKindFunction, Name: "A", FilePath: "a.go", StartLine: 1, EndLine: 2, Language: "go"},
		{QualifiedName: "pkg.B", Kind: graph.NodeKindFunction, Name: "B", FilePath: "b.go", StartLine: 1, EndLine: 2, Language: "go"},
	})
	nodeA, _ := s.GetNode(ctx, "pkg.A")
	nodeB, _ := s.GetNode(ctx, "pkg.B")

	s.UpsertEdges(ctx, []graph.Edge{
		{FromNodeID: nodeA.ID, ToNodeID: nodeB.ID, Kind: graph.EdgeKindCalls, FilePath: "a.go", Line: 2, Fingerprint: "calls:a.go:B:2"},
	})

	s.DeleteNodesByFile(ctx, "a.go")

	edges, _ := s.GetEdgesFrom(ctx, nodeA.ID)
	if len(edges) != 0 {
		t.Errorf("expected 0 edges after cascade delete, got %d", len(edges))
	}
}

func TestNode_NamespaceField(t *testing.T) {
	s := setupTestDB(t)
	ctx := requestctx.WithNamespace(context.Background(), "svc")

	nodes := []graph.Node{
		{QualifiedName: "pkg.A", Kind: graph.NodeKindFunction, Name: "A", FilePath: "a.go", StartLine: 1, EndLine: 2, Language: "go"},
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

	ctxA := requestctx.WithNamespace(context.Background(), "a")
	ctxB := requestctx.WithNamespace(context.Background(), "b")

	s.UpsertNodes(ctxA, []graph.Node{
		{QualifiedName: "pkg.F", Kind: graph.NodeKindFunction, Name: "F", FilePath: "a.go", StartLine: 1, EndLine: 2, Language: "go"},
	})
	s.UpsertNodes(ctxB, []graph.Node{
		{QualifiedName: "pkg.F", Kind: graph.NodeKindFunction, Name: "F", FilePath: "a.go", StartLine: 1, EndLine: 2, Language: "go"},
	})

	var count int64
	s.db.Model(&graph.Node{}).Where("qualified_name = ?", "pkg.F").Count(&count)
	if count != 2 {
		t.Errorf("expected 2 nodes with same QN in different namespaces, got %d", count)
	}
}

func TestNode_UniqueIndex_DuplicateWithinNamespace(t *testing.T) {
	s := setupTestDB(t)
	ctx := requestctx.WithNamespace(context.Background(), "a")

	node1 := []graph.Node{
		{QualifiedName: "pkg.F", Kind: graph.NodeKindFunction, Name: "F", FilePath: "a.go", StartLine: 1, EndLine: 2, Language: "go"},
	}
	if err := s.UpsertNodes(ctx, node1); err != nil {
		t.Fatalf("first UpsertNodes: %v", err)
	}

	node2 := []graph.Node{
		{QualifiedName: "pkg.F", Kind: graph.NodeKindFunction, Name: "F_updated", FilePath: "a.go", StartLine: 1, EndLine: 5, Language: "go"},
	}
	if err := s.UpsertNodes(ctx, node2); err != nil {
		t.Fatalf("second UpsertNodes: %v", err)
	}

	var count int64
	s.db.Model(&graph.Node{}).Where("namespace = ? AND qualified_name = ?", "a", "pkg.F").Count(&count)
	if count != 1 {
		t.Errorf("expected 1 node after upsert within same namespace, got %d", count)
	}
}

func TestDeleteNode_CascadeAnnotation(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()

	s.UpsertNodes(ctx, []graph.Node{
		{QualifiedName: "pkg.F", Kind: graph.NodeKindFunction, Name: "F", FilePath: "a.go", StartLine: 1, EndLine: 2, Language: "go"},
	})
	node, _ := s.GetNode(ctx, "pkg.F")

	s.UpsertAnnotation(ctx, &graph.Annotation{
		NodeID:  node.ID,
		Summary: "summary",
		Tags:    []graph.DocTag{{Kind: graph.TagParam, Name: "x", Value: "v", Ordinal: 0}},
	})

	s.DeleteNodesByFile(ctx, "a.go")

	ann, _ := s.GetAnnotation(ctx, node.ID)
	if ann != nil {
		t.Error("expected annotation to be cascade deleted")
	}
}

func TestUpsertNodes_SetsNamespaceFromContext(t *testing.T) {
	s := setupTestDB(t)
	ctx := requestctx.WithNamespace(context.Background(), "pay")

	nodes := []graph.Node{
		{QualifiedName: "pkg.A", Kind: graph.NodeKindFunction, Name: "A", FilePath: "a.go", StartLine: 1, EndLine: 2, Language: "go"},
	}
	if err := s.UpsertNodes(ctx, nodes); err != nil {
		t.Fatalf("UpsertNodes: %v", err)
	}

	var got graph.Node
	s.db.First(&got, "qualified_name = ?", "pkg.A")
	if got.Namespace != "pay" {
		t.Errorf("Namespace = %q, want %q", got.Namespace, "pay")
	}
}

func TestUpsertNodes_DefaultNamespace_WhenContextHasNoNamespace(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()

	nodes := []graph.Node{
		{QualifiedName: "pkg.A", Kind: graph.NodeKindFunction, Name: "A", FilePath: "a.go", StartLine: 1, EndLine: 2, Language: "go"},
	}
	if err := s.UpsertNodes(ctx, nodes); err != nil {
		t.Fatalf("UpsertNodes: %v", err)
	}

	var got graph.Node
	s.db.First(&got, "qualified_name = ?", "pkg.A")
	if got.Namespace != requestctx.DefaultNamespace {
		t.Errorf("Namespace = %q, want %q", got.Namespace, requestctx.DefaultNamespace)
	}
}

func TestGetNode_FiltersByNamespace(t *testing.T) {
	s := setupTestDB(t)

	ctxA := requestctx.WithNamespace(context.Background(), "a")
	ctxB := requestctx.WithNamespace(context.Background(), "b")

	s.UpsertNodes(ctxA, []graph.Node{
		{QualifiedName: "pkg.F", Kind: graph.NodeKindFunction, Name: "F", FilePath: "a.go", StartLine: 1, EndLine: 2, Language: "go"},
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

func TestGetNode_DefaultNamespace_FindsDefaultNodes(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()

	s.UpsertNodes(ctx, []graph.Node{
		{QualifiedName: "pkg.F", Kind: graph.NodeKindFunction, Name: "F", FilePath: "a.go", StartLine: 1, EndLine: 2, Language: "go"},
	})

	got, err := s.GetNode(ctx, "pkg.F")
	if err != nil {
		t.Fatalf("GetNode: %v", err)
	}
	if got == nil {
		t.Fatal("expected node in default namespace, got nil")
	}
}

func TestGetNodesByFile_FiltersByNamespace(t *testing.T) {
	s := setupTestDB(t)

	ctxA := requestctx.WithNamespace(context.Background(), "a")
	ctxB := requestctx.WithNamespace(context.Background(), "b")

	s.UpsertNodes(ctxA, []graph.Node{
		{QualifiedName: "a.F1", Kind: graph.NodeKindFunction, Name: "F1", FilePath: "shared.go", StartLine: 1, EndLine: 2, Language: "go"},
	})
	s.UpsertNodes(ctxB, []graph.Node{
		{QualifiedName: "b.F1", Kind: graph.NodeKindFunction, Name: "F1", FilePath: "shared.go", StartLine: 1, EndLine: 2, Language: "go"},
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

	ctxA := requestctx.WithNamespace(context.Background(), "a")
	ctxB := requestctx.WithNamespace(context.Background(), "b")

	s.UpsertNodes(ctxA, []graph.Node{
		{QualifiedName: "pkg.F", Kind: graph.NodeKindFunction, Name: "F", FilePath: "a.go", StartLine: 1, EndLine: 2, Language: "go"},
	})
	s.UpsertNodes(ctxB, []graph.Node{
		{QualifiedName: "pkg.F", Kind: graph.NodeKindFunction, Name: "F", FilePath: "a.go", StartLine: 1, EndLine: 2, Language: "go"},
	})

	got, err := s.GetNodesByQualifiedNames(ctxA, []string{"pkg.F"})
	if err != nil {
		t.Fatalf("GetNodesByQualifiedNames: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("expected 1 node in namespace a, got %d", len(got))
	}
	if matchedNodes, ok := got["pkg.F"]; ok {
		if len(matchedNodes) != 1 {
			t.Fatalf("expected 1 matched node, got %d", len(matchedNodes))
		}
		if matchedNodes[0].Namespace != "a" {
			t.Errorf("Namespace = %q, want %q", matchedNodes[0].Namespace, "a")
		}
	}
}

func TestGetNodesByQualifiedNames_PreservesDuplicateQualifiedNames(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()

	if err := s.UpsertNodes(ctx, []graph.Node{
		{QualifiedName: "save", Kind: graph.NodeKindFunction, Name: "save", FilePath: "python/dup_methods.py", StartLine: 7, EndLine: 9, Language: "python"},
		{QualifiedName: "save", Kind: graph.NodeKindFunction, Name: "save", FilePath: "python/dup_methods.py", StartLine: 15, EndLine: 17, Language: "python"},
	}); err != nil {
		t.Fatalf("UpsertNodes: %v", err)
	}

	got, err := s.GetNodesByQualifiedNames(ctx, []string{"save"})
	if err != nil {
		t.Fatalf("GetNodesByQualifiedNames: %v", err)
	}
	matchedNodes := got["save"]
	if len(matchedNodes) != 2 {
		t.Fatalf("expected 2 nodes for duplicate qualified name, got %d", len(matchedNodes))
	}
	startLines := map[int]bool{}
	for _, node := range matchedNodes {
		startLines[node.StartLine] = true
	}
	if !startLines[7] || !startLines[15] {
		t.Fatalf("expected start lines 7 and 15, got %#v", startLines)
	}
}

func TestGetFileNodesByPathSuffix_PrefersExactDirectoryMatch(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()
	if err := s.UpsertNodes(ctx, []graph.Node{
		{QualifiedName: "cmd/main.go", Kind: graph.NodeKindFile, Name: "cmd/main.go", FilePath: "cmd/main.go", StartLine: 1, EndLine: 10, Language: "go"},
		{QualifiedName: "internal/mcp/deps.go", Kind: graph.NodeKindFile, Name: "internal/mcp/deps.go", FilePath: "internal/mcp/deps.go", StartLine: 1, EndLine: 10, Language: "go"},
		{QualifiedName: "pkg/internal/mcp/deps.go", Kind: graph.NodeKindFile, Name: "pkg/internal/mcp/deps.go", FilePath: "pkg/internal/mcp/deps.go", StartLine: 1, EndLine: 10, Language: "go"},
	}); err != nil {
		t.Fatalf("UpsertNodes: %v", err)
	}
	got, err := s.GetFileNodesByPathSuffix(ctx, "internal/mcp")
	if err != nil {
		t.Fatalf("GetFileNodesByPathSuffix: %v", err)
	}
	if len(got) != 1 || got[0].FilePath != "internal/mcp/deps.go" {
		t.Fatalf("expected exact directory match only, got %+v", got)
	}
}

func TestGetFileNodesByPathSuffix_ReturnsAmbiguousExactDirectoryMatches(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()
	if err := s.UpsertNodes(ctx, []graph.Node{
		{QualifiedName: "internal/mcp/deps.go", Kind: graph.NodeKindFile, Name: "internal/mcp/deps.go", FilePath: "internal/mcp/deps.go", StartLine: 1, EndLine: 10, Language: "go"},
		{QualifiedName: "internal/mcp/extra.go", Kind: graph.NodeKindFile, Name: "internal/mcp/extra.go", FilePath: "internal/mcp/extra.go", StartLine: 1, EndLine: 10, Language: "go"},
	}); err != nil {
		t.Fatalf("UpsertNodes: %v", err)
	}
	got, err := s.GetFileNodesByPathSuffix(ctx, "internal/mcp")
	if err != nil {
		t.Fatalf("GetFileNodesByPathSuffix: %v", err)
	}
	paths := []string{got[0].FilePath, got[1].FilePath}
	slices.Sort(paths)
	if len(got) != 2 || !slices.Equal(paths, []string{"internal/mcp/deps.go", "internal/mcp/extra.go"}) {
		t.Fatalf("expected both exact directory matches, got %+v", got)
	}
}

func TestGetFileNodesByPathSuffix_ReturnsAmbiguousLongestMatches(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()
	if err := s.UpsertNodes(ctx, []graph.Node{
		{QualifiedName: "pkg/mcp/deps.go", Kind: graph.NodeKindFile, Name: "pkg/mcp/deps.go", FilePath: "pkg/mcp/deps.go", StartLine: 1, EndLine: 10, Language: "go"},
		{QualifiedName: "internal/mcp/deps.go", Kind: graph.NodeKindFile, Name: "internal/mcp/deps.go", FilePath: "internal/mcp/deps.go", StartLine: 1, EndLine: 10, Language: "go"},
	}); err != nil {
		t.Fatalf("UpsertNodes: %v", err)
	}
	got, err := s.GetFileNodesByPathSuffix(ctx, "github.com/example/project/mcp")
	if err != nil {
		t.Fatalf("GetFileNodesByPathSuffix: %v", err)
	}
	paths := []string{got[0].FilePath, got[1].FilePath}
	slices.Sort(paths)
	if len(got) != 2 || !slices.Equal(paths, []string{"internal/mcp/deps.go", "pkg/mcp/deps.go"}) {
		t.Fatalf("expected both ambiguous longest matches, got %+v", got)
	}
}

func TestDeleteNodesByFile_FiltersByNamespace(t *testing.T) {
	s := setupTestDB(t)

	ctxA := requestctx.WithNamespace(context.Background(), "a")
	ctxB := requestctx.WithNamespace(context.Background(), "b")

	s.UpsertNodes(ctxA, []graph.Node{
		{QualifiedName: "a.F", Kind: graph.NodeKindFunction, Name: "F", FilePath: "shared.go", StartLine: 1, EndLine: 2, Language: "go"},
	})
	s.UpsertNodes(ctxB, []graph.Node{
		{QualifiedName: "b.F", Kind: graph.NodeKindFunction, Name: "F", FilePath: "shared.go", StartLine: 1, EndLine: 2, Language: "go"},
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

func TestUpsertNodes_CrossFile_SameQualifiedName_BothSurvive(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()

	// Cross-file: nodes in different files with the same qualified_name.
	// Example: C add (c/attr.c:3) vs Python add (python/oneline.py:2).
	nodes := []graph.Node{
		{QualifiedName: "add", Kind: graph.NodeKindFunction, Name: "add", FilePath: "c/attr.c", StartLine: 3, EndLine: 5, Language: "c"},
		{QualifiedName: "add", Kind: graph.NodeKindFunction, Name: "add", FilePath: "python/oneline.py", StartLine: 2, EndLine: 4, Language: "python"},
	}
	if err := s.UpsertNodes(ctx, nodes); err != nil {
		t.Fatalf("UpsertNodes: %v", err)
	}

	// Both nodes must exist in the DB.
	var count int64
	s.db.Model(&graph.Node{}).Where("qualified_name = ?", "add").Count(&count)
	if count != 2 {
		t.Errorf("expected 2 nodes with qualified_name='add', got %d", count)
	}

	// Each file_path must map to the correct node.
	var cNode graph.Node
	err := s.db.Where("qualified_name = ? AND file_path = ?", "add", "c/attr.c").First(&cNode).Error
	if err != nil {
		t.Errorf("C node not found: %v", err)
	}
	var pyNode graph.Node
	err = s.db.Where("qualified_name = ? AND file_path = ?", "add", "python/oneline.py").First(&pyNode).Error
	if err != nil {
		t.Errorf("Python node not found: %v", err)
	}
}

func TestUpsertNodes_SameFile_SameQualifiedName_DifferentLine_BothSurvive(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()

	// Same-file: same qualified_name in one file, but different start_line.
	// Example: Alpha.save (line 7) vs Beta.save (line 15) — both have QN = "save".
	nodes := []graph.Node{
		{QualifiedName: "save", Kind: graph.NodeKindFunction, Name: "save", FilePath: "python/dup_methods.py", StartLine: 7, EndLine: 9, Language: "python"},
		{QualifiedName: "save", Kind: graph.NodeKindFunction, Name: "save", FilePath: "python/dup_methods.py", StartLine: 15, EndLine: 17, Language: "python"},
	}
	if err := s.UpsertNodes(ctx, nodes); err != nil {
		t.Fatalf("UpsertNodes: %v", err)
	}

	// Both nodes must exist in the DB.
	var count int64
	s.db.Model(&graph.Node{}).Where("qualified_name = ? AND file_path = ?", "save", "python/dup_methods.py").Count(&count)
	if count != 2 {
		t.Errorf("expected 2 nodes with qualified_name='save' in same file, got %d", count)
	}

	// They must be distinguishable by start_line.
	var node1 graph.Node
	err := s.db.Where("qualified_name = ? AND start_line = ?", "save", 7).First(&node1).Error
	if err != nil {
		t.Errorf("node at line 7 not found: %v", err)
	}
	var node2 graph.Node
	err = s.db.Where("qualified_name = ? AND start_line = ?", "save", 15).First(&node2).Error
	if err != nil {
		t.Errorf("node at line 15 not found: %v", err)
	}
}

func TestUpsertNodes_ConflictWithinSameNamespace(t *testing.T) {
	s := setupTestDB(t)
	ctx := requestctx.WithNamespace(context.Background(), "ns")

	s.UpsertNodes(ctx, []graph.Node{
		{QualifiedName: "pkg.F", Kind: graph.NodeKindFunction, Name: "F", FilePath: "a.go", StartLine: 1, EndLine: 2, Hash: "aaa", Language: "go"},
	})

	s.UpsertNodes(ctx, []graph.Node{
		{QualifiedName: "pkg.F", Kind: graph.NodeKindFunction, Name: "F", FilePath: "a.go", StartLine: 1, EndLine: 5, Hash: "bbb", Language: "go"},
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

func TestDeleteNodesByFile_CleansSearchDocuments(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()

	s.UpsertNodes(ctx, []graph.Node{
		{QualifiedName: "pkg.A", Kind: graph.NodeKindFunction, Name: "A", FilePath: "a.go", StartLine: 1, EndLine: 2, Language: "go"},
	})
	node, _ := s.GetNode(ctx, "pkg.A")

	if err := s.db.Create(&graph.SearchDocument{NodeID: node.ID, Content: "pkg.A content", Language: "go"}).Error; err != nil {
		t.Fatalf("insert search_document: %v", err)
	}

	if err := s.DeleteNodesByFile(ctx, "a.go"); err != nil {
		t.Fatalf("DeleteNodesByFile: %v", err)
	}

	var count int64
	s.db.Model(&graph.SearchDocument{}).Where("node_id = ?", node.ID).Count(&count)
	if count != 0 {
		t.Errorf("expected search_documents to be deleted, got %d rows", count)
	}
}

func TestDeleteNodesByFile_CleansMemberships(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()

	s.UpsertNodes(ctx, []graph.Node{{
		QualifiedName: "pkg.A",
		Kind:          graph.NodeKindFunction,
		Name:          "A",
		FilePath:      "a.go",
		StartLine:     1,
		EndLine:       2,
		Language:      "go",
	}})
	node, _ := s.GetNode(ctx, "pkg.A")

	community := graph.Community{Key: "core", Label: "core", Strategy: "directory"}
	flow := graph.Flow{Name: "login-flow"}
	if err := s.db.Create(&community).Error; err != nil {
		t.Fatalf("insert community: %v", err)
	}
	if err := s.db.Create(&flow).Error; err != nil {
		t.Fatalf("insert flow: %v", err)
	}
	if err := s.db.Create(&graph.CommunityMembership{CommunityID: community.ID, NodeID: node.ID}).Error; err != nil {
		t.Fatalf("insert community membership: %v", err)
	}
	if err := s.db.Create(&graph.FlowMembership{FlowID: flow.ID, NodeID: node.ID, Ordinal: 0}).Error; err != nil {
		t.Fatalf("insert flow membership: %v", err)
	}

	if err := s.DeleteNodesByFile(ctx, "a.go"); err != nil {
		t.Fatalf("DeleteNodesByFile: %v", err)
	}

	var communityCount, flowCount int64
	s.db.Model(&graph.CommunityMembership{}).Where("node_id = ?", node.ID).Count(&communityCount)
	s.db.Model(&graph.FlowMembership{}).Where("node_id = ?", node.ID).Count(&flowCount)
	if communityCount != 0 {
		t.Fatalf("expected community memberships to be deleted, got %d", communityCount)
	}
	if flowCount != 0 {
		t.Fatalf("expected flow memberships to be deleted, got %d", flowCount)
	}
}

func TestDeleteNodesByFile_LeavesOtherFlowMembershipsInNamespace(t *testing.T) {
	s := setupTestDB(t)
	ctx := requestctx.WithNamespace(context.Background(), "ns-x")

	if err := s.UpsertNodes(ctx, []graph.Node{{
		QualifiedName: "pkg.A",
		Kind:          graph.NodeKindFunction,
		Name:          "A",
		FilePath:      "a.go",
		StartLine:     1,
		EndLine:       2,
		Language:      "go",
	}}); err != nil {
		t.Fatalf("UpsertNodes: %v", err)
	}
	node, _ := s.GetNode(ctx, "pkg.A")

	flow := graph.Flow{Namespace: "ns-x", Name: "login-flow"}
	if err := s.db.Create(&flow).Error; err != nil {
		t.Fatalf("insert flow: %v", err)
	}
	if err := s.db.Create(&graph.FlowMembership{Namespace: "ns-x", FlowID: flow.ID, NodeID: node.ID, Ordinal: 0}).Error; err != nil {
		t.Fatalf("insert deleted-file flow membership: %v", err)
	}
	if err := s.db.Create(&graph.FlowMembership{Namespace: requestctx.DefaultNamespace, FlowID: flow.ID, NodeID: 999999, Ordinal: 1}).Error; err != nil {
		t.Fatalf("insert untouched flow membership: %v", err)
	}

	if err := s.DeleteNodesByFile(ctx, "a.go"); err != nil {
		t.Fatalf("DeleteNodesByFile: %v", err)
	}

	var count int64
	if err := s.db.Model(&graph.FlowMembership{}).Where("flow_id = ? AND node_id = ?", flow.ID, 999999).Count(&count).Error; err != nil {
		t.Fatalf("count flow memberships: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected unrelated flow membership to remain after file delete, got %d", count)
	}
}

func TestDeleteGraph_CleansSearchDocuments(t *testing.T) {
	s := setupTestDB(t)
	ctx := requestctx.WithNamespace(context.Background(), "ns-x")

	s.UpsertNodes(ctx, []graph.Node{
		{QualifiedName: "pkg.B", Kind: graph.NodeKindFunction, Name: "B", FilePath: "b.go", StartLine: 1, EndLine: 2, Language: "go"},
	})
	node, _ := s.GetNode(ctx, "pkg.B")

	if err := s.db.Create(&graph.SearchDocument{NodeID: node.ID, Content: "pkg.B content", Language: "go"}).Error; err != nil {
		t.Fatalf("insert search_document: %v", err)
	}

	if err := s.DeleteGraph(ctx); err != nil {
		t.Fatalf("DeleteGraph: %v", err)
	}

	var count int64
	s.db.Model(&graph.SearchDocument{}).Where("node_id = ?", node.ID).Count(&count)
	if count != 0 {
		t.Errorf("expected search_documents to be deleted after DeleteGraph, got %d rows", count)
	}
}

func TestDeleteGraph_CleansMemberships(t *testing.T) {
	s := setupTestDB(t)
	ctx := requestctx.WithNamespace(context.Background(), "ns-x")

	s.UpsertNodes(ctx, []graph.Node{{
		QualifiedName: "pkg.B",
		Kind:          graph.NodeKindFunction,
		Name:          "B",
		FilePath:      "b.go",
		StartLine:     1,
		EndLine:       2,
		Language:      "go",
	}})
	node, _ := s.GetNode(ctx, "pkg.B")

	community := graph.Community{Namespace: "ns-x", Key: "ns-x/core", Label: "ns-x/core", Strategy: "directory"}
	flow := graph.Flow{Namespace: "ns-x", Name: "checkout-flow"}
	if err := s.db.Create(&community).Error; err != nil {
		t.Fatalf("insert community: %v", err)
	}
	if err := s.db.Create(&flow).Error; err != nil {
		t.Fatalf("insert flow: %v", err)
	}
	if err := s.db.Create(&graph.CommunityMembership{CommunityID: community.ID, NodeID: node.ID}).Error; err != nil {
		t.Fatalf("insert community membership: %v", err)
	}
	if err := s.db.Create(&graph.FlowMembership{Namespace: "ns-x", FlowID: flow.ID, NodeID: node.ID, Ordinal: 0}).Error; err != nil {
		t.Fatalf("insert flow membership: %v", err)
	}

	if err := s.DeleteGraph(ctx); err != nil {
		t.Fatalf("DeleteGraph: %v", err)
	}

	var communityCount, flowCount int64
	s.db.Model(&graph.CommunityMembership{}).Where("node_id = ?", node.ID).Count(&communityCount)
	s.db.Model(&graph.FlowMembership{}).Where("node_id = ?", node.ID).Count(&flowCount)
	if communityCount != 0 {
		t.Fatalf("expected community memberships to be deleted, got %d", communityCount)
	}
	if flowCount != 0 {
		t.Fatalf("expected flow memberships to be deleted, got %d", flowCount)
	}
}

func TestDeleteGraph_CleansFlowMembershipsByFlowNamespace(t *testing.T) {
	s := setupTestDB(t)
	ctx := requestctx.WithNamespace(context.Background(), "ns-x")
	otherCtx := requestctx.WithNamespace(context.Background(), "ns-y")

	if err := s.UpsertNodes(ctx, []graph.Node{{
		QualifiedName: "pkg.B",
		Kind:          graph.NodeKindFunction,
		Name:          "B",
		FilePath:      "b.go",
		StartLine:     1,
		EndLine:       2,
		Language:      "go",
	}}); err != nil {
		t.Fatalf("UpsertNodes ns-x: %v", err)
	}
	if err := s.UpsertNodes(otherCtx, []graph.Node{{
		QualifiedName: "pkg.C",
		Kind:          graph.NodeKindFunction,
		Name:          "C",
		FilePath:      "c.go",
		StartLine:     1,
		EndLine:       2,
		Language:      "go",
	}}); err != nil {
		t.Fatalf("UpsertNodes ns-y: %v", err)
	}
	node, _ := s.GetNode(ctx, "pkg.B")
	otherNode, _ := s.GetNode(otherCtx, "pkg.C")

	flow := graph.Flow{Namespace: "ns-x", Name: "checkout-flow"}
	otherFlow := graph.Flow{Namespace: "ns-y", Name: "other-flow"}
	if err := s.db.Create(&flow).Error; err != nil {
		t.Fatalf("insert ns-x flow: %v", err)
	}
	if err := s.db.Create(&otherFlow).Error; err != nil {
		t.Fatalf("insert ns-y flow: %v", err)
	}
	if err := s.db.Create(&graph.FlowMembership{Namespace: requestctx.DefaultNamespace, FlowID: flow.ID, NodeID: 999999, Ordinal: 0}).Error; err != nil {
		t.Fatalf("insert orphaned ns-x flow membership: %v", err)
	}
	if err := s.db.Create(&graph.FlowMembership{Namespace: "other", FlowID: flow.ID, NodeID: node.ID, Ordinal: 1}).Error; err != nil {
		t.Fatalf("insert mismatched ns-x flow membership: %v", err)
	}
	if err := s.db.Create(&graph.FlowMembership{Namespace: "ns-y", FlowID: otherFlow.ID, NodeID: otherNode.ID, Ordinal: 0}).Error; err != nil {
		t.Fatalf("insert ns-y flow membership: %v", err)
	}

	if err := s.DeleteGraph(ctx); err != nil {
		t.Fatalf("DeleteGraph: %v", err)
	}

	var nsXCount, nsYCount int64
	if err := s.db.Model(&graph.FlowMembership{}).Where("flow_id = ?", flow.ID).Count(&nsXCount).Error; err != nil {
		t.Fatalf("count ns-x flow memberships: %v", err)
	}
	if err := s.db.Model(&graph.FlowMembership{}).Where("flow_id = ?", otherFlow.ID).Count(&nsYCount).Error; err != nil {
		t.Fatalf("count ns-y flow memberships: %v", err)
	}
	if nsXCount != 0 {
		t.Fatalf("expected ns-x flow memberships to be deleted by flow scope, got %d", nsXCount)
	}
	if nsYCount != 1 {
		t.Fatalf("expected other namespace flow membership to remain, got %d", nsYCount)
	}
}

func TestDeleteGraph_LeavesCommunityParentsUntilExplicitCleanup(t *testing.T) {
	s := setupTestDB(t)
	ctx := requestctx.WithNamespace(context.Background(), "ns-x")

	if err := s.UpsertNodes(ctx, []graph.Node{{
		QualifiedName: "pkg.B",
		Kind:          graph.NodeKindFunction,
		Name:          "B",
		FilePath:      "b.go",
		StartLine:     1,
		EndLine:       2,
		Language:      "go",
	}}); err != nil {
		t.Fatalf("UpsertNodes: %v", err)
	}
	node, _ := s.GetNode(ctx, "pkg.B")

	community := graph.Community{Namespace: "ns-x", Key: "ns-x/core", Label: "ns-x/core", Strategy: "directory"}
	if err := s.db.Create(&community).Error; err != nil {
		t.Fatalf("insert community: %v", err)
	}
	if err := s.db.Create(&graph.CommunityMembership{CommunityID: community.ID, NodeID: node.ID}).Error; err != nil {
		t.Fatalf("insert community membership: %v", err)
	}

	if err := s.DeleteGraph(ctx); err != nil {
		t.Fatalf("DeleteGraph: %v", err)
	}

	var parentCount int64
	if err := s.db.Model(&graph.Community{}).Where("id = ?", community.ID).Count(&parentCount).Error; err != nil {
		t.Fatalf("count community parent: %v", err)
	}
	if parentCount != 1 {
		t.Fatalf("expected community parent row to remain for explicit cleanup, got %d", parentCount)
	}
}

func TestDeleteGraph_LeavesFlowParentsUntilExplicitCleanup(t *testing.T) {
	s := setupTestDB(t)
	ctx := requestctx.WithNamespace(context.Background(), "ns-x")

	if err := s.UpsertNodes(ctx, []graph.Node{{
		QualifiedName: "pkg.B",
		Kind:          graph.NodeKindFunction,
		Name:          "B",
		FilePath:      "b.go",
		StartLine:     1,
		EndLine:       2,
		Language:      "go",
	}}); err != nil {
		t.Fatalf("UpsertNodes: %v", err)
	}
	node, _ := s.GetNode(ctx, "pkg.B")

	flow := graph.Flow{Namespace: "ns-x", Name: "checkout-flow"}
	if err := s.db.Create(&flow).Error; err != nil {
		t.Fatalf("insert flow: %v", err)
	}
	if err := s.db.Create(&graph.FlowMembership{Namespace: "ns-x", FlowID: flow.ID, NodeID: node.ID, Ordinal: 0}).Error; err != nil {
		t.Fatalf("insert flow membership: %v", err)
	}

	if err := s.DeleteGraph(ctx); err != nil {
		t.Fatalf("DeleteGraph: %v", err)
	}

	var parentCount int64
	if err := s.db.Model(&graph.Flow{}).Where("id = ?", flow.ID).Count(&parentCount).Error; err != nil {
		t.Fatalf("count flow parent: %v", err)
	}
	if parentCount != 1 {
		t.Fatalf("expected flow parent row to remain for explicit cleanup, got %d", parentCount)
	}
}
