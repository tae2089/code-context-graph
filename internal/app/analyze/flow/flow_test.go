package flow

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/tae2089/code-context-graph/internal/adapters/outbound/graphgorm"
	requestctx "github.com/tae2089/code-context-graph/internal/ctx"
	"github.com/tae2089/code-context-graph/internal/domain/graph"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func boolPtr(v bool) *bool {
	return &v
}

var flowBuilderTestDBSeq atomic.Int64

type mockStore struct {
	nodes          map[uint]*graph.Node
	edges          map[uint][]graph.Edge
	fetchedNodeIDs []uint
}

func (m *mockStore) GetEdgesFrom(_ context.Context, nodeID uint) ([]graph.Edge, error) {
	m.fetchedNodeIDs = append(m.fetchedNodeIDs, nodeID)
	return m.edges[nodeID], nil
}

func (m *mockStore) GetNodeByID(_ context.Context, id uint) (*graph.Node, error) {
	n, ok := m.nodes[id]
	if !ok {
		return nil, nil
	}
	return n, nil
}

func newNode(id uint, name string) *graph.Node {
	return &graph.Node{ID: id, QualifiedName: name, Kind: graph.NodeKindFunction, Name: name, FilePath: "a.go", StartLine: 1, EndLine: 2, Language: "go"}
}

func callEdge(from, to uint, idx int) graph.Edge {
	return graph.Edge{ID: uint(idx), FromNodeID: from, ToNodeID: to, Kind: graph.EdgeKindCalls, Fingerprint: fmt.Sprintf("e%d", idx)}
}

func flowMemberIDs(flow *graph.Flow) []uint {
	ids := make([]uint, 0, len(flow.Members))
	for _, member := range flow.Members {
		ids = append(ids, member.NodeID)
	}
	return ids
}

func assertUintSliceEqual(t *testing.T, got, want []uint) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("expected IDs %v, got %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("expected IDs %v, got %v", want, got)
		}
	}
}

func setupFlowBuilderTestDB(t *testing.T) (*gorm.DB, *graphgorm.Store) {
	t.Helper()
	dsn := fmt.Sprintf("file:flows-builder-%s-%d?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "-"), flowBuilderTestDBSeq.Add(1))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	st := graphgorm.New(db)
	if err := st.AutoMigrate(); err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&graph.Flow{}, &graph.FlowMembership{}); err != nil {
		t.Fatal(err)
	}
	return db, st
}

func TestTraceFlow_SimpleChain(t *testing.T) {
	ms := &mockStore{
		nodes: map[uint]*graph.Node{1: newNode(1, "A"), 2: newNode(2, "B"), 3: newNode(3, "C")},
		edges: map[uint][]graph.Edge{
			1: {callEdge(1, 2, 1)},
			2: {callEdge(2, 3, 2)},
		},
	}
	tracer := New(ms)
	flow, err := tracer.TraceFlow(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(flow.Members) != 3 {
		t.Fatalf("expected 3 members, got %d", len(flow.Members))
	}
	if flow.Members[0].NodeID != 1 || flow.Members[1].NodeID != 2 || flow.Members[2].NodeID != 3 {
		t.Errorf("expected chain [1,2,3], got [%d,%d,%d]", flow.Members[0].NodeID, flow.Members[1].NodeID, flow.Members[2].NodeID)
	}
}

func TestTraceFlow_IncludesFallbackCalls(t *testing.T) {
	ms := &mockStore{
		nodes: map[uint]*graph.Node{1: newNode(1, "A"), 2: newNode(2, "B"), 3: newNode(3, "C")},
		edges: map[uint][]graph.Edge{
			1: {
				{ID: 1, FromNodeID: 1, ToNodeID: 2, Kind: graph.EdgeKindCalls, Fingerprint: "call-1"},
				{ID: 2, FromNodeID: 1, ToNodeID: 3, Kind: graph.EdgeKindFallbackCalls, Fingerprint: "call-2"},
			},
		},
	}
	tracer := New(ms)
	flow, err := tracer.TraceFlow(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	got := flowMemberIDs(flow)
	assertUintSliceEqual(t, got, []uint{1, 2, 3})
}

func TestTraceFlowBounded_ExcludesFallbackCallsWhenDisabled(t *testing.T) {
	ms := &mockStore{
		nodes: map[uint]*graph.Node{1: newNode(1, "A"), 2: newNode(2, "B"), 3: newNode(3, "C")},
		edges: map[uint][]graph.Edge{
			1: {
				{ID: 1, FromNodeID: 1, ToNodeID: 2, Kind: graph.EdgeKindCalls, Fingerprint: "call-1"},
				{ID: 2, FromNodeID: 1, ToNodeID: 3, Kind: graph.EdgeKindFallbackCalls, Fingerprint: "call-2"},
			},
		},
	}
	tracer := New(ms)

	result, err := tracer.TraceFlowBounded(context.Background(), 1, TraceOptions{IncludeFallbackCalls: boolPtr(false)})
	if err != nil {
		t.Fatal(err)
	}

	assertUintSliceEqual(t, flowMemberIDs(result.Flow), []uint{1, 2})
	if result.ContainsFallbackCalls {
		t.Fatal("expected strict trace to report no fallback calls")
	}
	if result.FallbackEdgesCount != 0 {
		t.Fatalf("expected fallback edge count 0, got %d", result.FallbackEdgesCount)
	}
}

func TestTraceFlowBounded_ReportsFallbackMetadata(t *testing.T) {
	ms := &mockStore{
		nodes: map[uint]*graph.Node{1: newNode(1, "A"), 2: newNode(2, "B"), 3: newNode(3, "C")},
		edges: map[uint][]graph.Edge{
			1: {
				{ID: 1, FromNodeID: 1, ToNodeID: 2, Kind: graph.EdgeKindCalls, Fingerprint: "call-1"},
				{ID: 2, FromNodeID: 1, ToNodeID: 3, Kind: graph.EdgeKindFallbackCalls, Fingerprint: "call-2"},
			},
		},
	}
	tracer := New(ms)

	result, err := tracer.TraceFlowBounded(context.Background(), 1, TraceOptions{})
	if err != nil {
		t.Fatal(err)
	}

	assertUintSliceEqual(t, flowMemberIDs(result.Flow), []uint{1, 2, 3})
	if !result.ContainsFallbackCalls {
		t.Fatal("expected fallback-inclusive trace to report fallback calls")
	}
	if result.FallbackEdgesCount != 1 {
		t.Fatalf("expected fallback edge count 1, got %d", result.FallbackEdgesCount)
	}
}

func TestTraceFlow_Branch(t *testing.T) {
	ms := &mockStore{
		nodes: map[uint]*graph.Node{1: newNode(1, "A"), 2: newNode(2, "B"), 3: newNode(3, "C")},
		edges: map[uint][]graph.Edge{
			1: {callEdge(1, 2, 1), callEdge(1, 3, 2)},
		},
	}
	tracer := New(ms)
	flow, err := tracer.TraceFlow(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(flow.Members) != 3 {
		t.Fatalf("expected 3 members, got %d", len(flow.Members))
	}
}

func TestTraceFlow_Merge(t *testing.T) {
	ms := &mockStore{
		nodes: map[uint]*graph.Node{1: newNode(1, "A"), 2: newNode(2, "B"), 3: newNode(3, "C"), 4: newNode(4, "D")},
		edges: map[uint][]graph.Edge{
			1: {callEdge(1, 2, 1), callEdge(1, 3, 2)},
			2: {callEdge(2, 4, 3)},
			3: {callEdge(3, 4, 4)},
		},
	}
	tracer := New(ms)
	flow, err := tracer.TraceFlow(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(flow.Members) != 4 {
		t.Fatalf("expected 4 members, got %d", len(flow.Members))
	}
}

func TestTraceFlow_NoEdges(t *testing.T) {
	ms := &mockStore{
		nodes: map[uint]*graph.Node{1: newNode(1, "A")},
		edges: map[uint][]graph.Edge{},
	}
	tracer := New(ms)
	flow, err := tracer.TraceFlow(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(flow.Members) != 1 {
		t.Fatalf("expected 1 member, got %d", len(flow.Members))
	}
	if flow.Members[0].NodeID != 1 {
		t.Errorf("expected node 1, got %d", flow.Members[0].NodeID)
	}
}

func TestTraceFlow_PropagatesNamespace(t *testing.T) {
	ms := &mockStore{
		nodes: map[uint]*graph.Node{1: newNode(1, "A"), 2: newNode(2, "B")},
		edges: map[uint][]graph.Edge{1: {callEdge(1, 2, 1)}},
	}
	tracer := New(ms)
	ctx := requestctx.WithNamespace(context.Background(), "payments")
	flow, err := tracer.TraceFlow(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if flow.Namespace != "payments" {
		t.Fatalf("flow namespace = %q, want %q", flow.Namespace, "payments")
	}
	for i, member := range flow.Members {
		if member.Namespace != "payments" {
			t.Fatalf("member[%d] namespace = %q, want %q", i, member.Namespace, "payments")
		}
	}
}

func TestTraceFlowBounded_ExactMaxNodesReturnsCapAndSignalsTruncation(t *testing.T) {
	ms := &mockStore{
		nodes: map[uint]*graph.Node{
			1: newNode(1, "A"),
			2: newNode(2, "B"),
			3: newNode(3, "C"),
			4: newNode(4, "D"),
		},
		edges: map[uint][]graph.Edge{1: {callEdge(1, 2, 1), callEdge(1, 3, 2), callEdge(1, 4, 3)}},
	}
	tracer := New(ms)

	result, err := tracer.TraceFlowBounded(context.Background(), 1, TraceOptions{MaxNodes: 3})
	if err != nil {
		t.Fatal(err)
	}

	assertUintSliceEqual(t, flowMemberIDs(result.Flow), []uint{1, 2, 3})
	if !result.Truncated {
		t.Fatalf("expected truncated result when an additional call was reachable beyond MaxNodes")
	}
	if result.ReturnedNodes != 3 {
		t.Fatalf("expected ReturnedNodes 3, got %d", result.ReturnedNodes)
	}
}

func TestTraceFlowBounded_DoesNotEnqueueBeyondMaxNodesInHighFanout(t *testing.T) {
	nodes := map[uint]*graph.Node{1: newNode(1, "Root")}
	edges := map[uint][]graph.Edge{}
	for id := uint(2); id <= 101; id++ {
		nodes[id] = newNode(id, fmt.Sprintf("N%d", id))
		edges[1] = append(edges[1], callEdge(1, id, int(id)))
	}
	ms := &mockStore{nodes: nodes, edges: edges}
	tracer := New(ms)

	result, err := tracer.TraceFlowBounded(context.Background(), 1, TraceOptions{MaxNodes: 3})
	if err != nil {
		t.Fatal(err)
	}

	assertUintSliceEqual(t, flowMemberIDs(result.Flow), []uint{1, 2, 3})
	assertUintSliceEqual(t, ms.fetchedNodeIDs, []uint{1, 2, 3})
	if !result.Truncated {
		t.Fatalf("expected high fan-out traversal to report truncation")
	}
}

func TestTraceFlowBounded_PreservesBFSMemberOrderWhenBounded(t *testing.T) {
	ms := &mockStore{
		nodes: map[uint]*graph.Node{
			1: newNode(1, "A"),
			2: newNode(2, "B"),
			3: newNode(3, "C"),
			4: newNode(4, "D"),
			5: newNode(5, "E"),
		},
		edges: map[uint][]graph.Edge{
			1: {callEdge(1, 2, 1), callEdge(1, 3, 2)},
			2: {callEdge(2, 4, 3)},
			3: {callEdge(3, 5, 4)},
		},
	}
	tracer := New(ms)

	result, err := tracer.TraceFlowBounded(context.Background(), 1, TraceOptions{MaxNodes: 4})
	if err != nil {
		t.Fatal(err)
	}

	assertUintSliceEqual(t, flowMemberIDs(result.Flow), []uint{1, 2, 3, 4})
	if !result.Truncated {
		t.Fatalf("expected truncated result when node 5 could not be enqueued")
	}
}

func TestFlowBuilder_Rebuild_PersistsFlowPerEntrypoint(t *testing.T) {
	db, st := setupFlowBuilderTestDB(t)

	ctx := requestctx.WithNamespace(context.Background(), "svc")
	if err := st.UpsertNodes(ctx, []graph.Node{
		{QualifiedName: "pkg.A", Kind: graph.NodeKindFunction, Name: "A", FilePath: "a.go", StartLine: 1, EndLine: 2, Language: "go"},
		{QualifiedName: "pkg.B", Kind: graph.NodeKindFunction, Name: "B", FilePath: "b.go", StartLine: 1, EndLine: 2, Language: "go"},
	}); err != nil {
		t.Fatal(err)
	}
	a, err := st.GetNode(ctx, "pkg.A")
	if err != nil || a == nil {
		t.Fatalf("get node A: %v", err)
	}
	b, err := st.GetNode(ctx, "pkg.B")
	if err != nil || b == nil {
		t.Fatalf("get node B: %v", err)
	}
	if err := st.UpsertEdges(ctx, []graph.Edge{{FromNodeID: a.ID, ToNodeID: b.ID, Kind: graph.EdgeKindCalls, Fingerprint: "a-b"}}); err != nil {
		t.Fatal(err)
	}

	builder := NewBuilder(st)
	stats, err := builder.Rebuild(ctx, Config{})
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 1 {
		t.Fatalf("expected 1 rebuilt flow, got %d", len(stats))
	}

	var persisted []graph.Flow
	if err := db.Where("namespace = ?", "svc").Find(&persisted).Error; err != nil {
		t.Fatal(err)
	}
	if len(persisted) != 1 {
		t.Fatalf("expected 1 persisted flow, got %d", len(persisted))
	}
	if persisted[0].Name != "flow_from_pkg.A" {
		t.Fatalf("expected flow name flow_from_pkg.A, got %q", persisted[0].Name)
	}

	var members []graph.FlowMembership
	if err := db.Where("flow_id = ?", persisted[0].ID).Order("ordinal asc").Find(&members).Error; err != nil {
		t.Fatal(err)
	}
	if len(members) != 2 {
		t.Fatalf("expected 2 persisted memberships, got %d", len(members))
	}
	if members[0].NodeID != a.ID || members[1].NodeID != b.ID {
		t.Fatalf("unexpected membership order: %+v", members)
	}
}

func TestFlowBuilder_Rebuild_DeletesPriorFlowsInNamespace(t *testing.T) {
	db, st := setupFlowBuilderTestDB(t)
	builder := NewBuilder(st)

	ctx := requestctx.WithNamespace(context.Background(), "svc")
	otherCtx := requestctx.WithNamespace(context.Background(), "other")
	if err := st.UpsertNodes(ctx, []graph.Node{
		{QualifiedName: "pkg.A", Kind: graph.NodeKindFunction, Name: "A", FilePath: "a.go", StartLine: 1, EndLine: 2, Language: "go"},
		{QualifiedName: "pkg.B", Kind: graph.NodeKindFunction, Name: "B", FilePath: "b.go", StartLine: 1, EndLine: 2, Language: "go"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertNodes(otherCtx, []graph.Node{{QualifiedName: "other.Root", Kind: graph.NodeKindFunction, Name: "Root", FilePath: "root.go", StartLine: 1, EndLine: 2, Language: "go"}}); err != nil {
		t.Fatal(err)
	}
	a, _ := st.GetNode(ctx, "pkg.A")
	b, _ := st.GetNode(ctx, "pkg.B")
	if err := st.UpsertEdges(ctx, []graph.Edge{{FromNodeID: a.ID, ToNodeID: b.ID, Kind: graph.EdgeKindCalls, Fingerprint: "svc-a-b"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := builder.Rebuild(ctx, Config{}); err != nil {
		t.Fatal(err)
	}
	var before []graph.Flow
	if err := db.Where("namespace = ?", "svc").Find(&before).Error; err != nil {
		t.Fatal(err)
	}
	if len(before) != 1 {
		t.Fatalf("expected 1 initial flow, got %d", len(before))
	}
	if err := db.Create(&graph.Flow{Namespace: "other", Name: "flow_from_other.Root"}).Error; err != nil {
		t.Fatal(err)
	}

	if err := st.DeleteNodesByFile(ctx, "b.go"); err != nil {
		t.Fatal(err)
	}
	if _, err := builder.Rebuild(ctx, Config{}); err != nil {
		t.Fatal(err)
	}

	var afterSvc []graph.Flow
	if err := db.Where("namespace = ?", "svc").Order("id asc").Find(&afterSvc).Error; err != nil {
		t.Fatal(err)
	}
	if len(afterSvc) != 1 {
		t.Fatalf("expected 1 rebuilt svc flow, got %d", len(afterSvc))
	}
	if afterSvc[0].ID == before[0].ID {
		t.Fatalf("expected svc flow to be replaced, id stayed %d", afterSvc[0].ID)
	}
	var svcMembers []graph.FlowMembership
	if err := db.Where("flow_id = ?", afterSvc[0].ID).Find(&svcMembers).Error; err != nil {
		t.Fatal(err)
	}
	if len(svcMembers) != 1 {
		t.Fatalf("expected rebuilt svc flow to have 1 member, got %d", len(svcMembers))
	}
	var otherCount int64
	if err := db.Model(&graph.Flow{}).Where("namespace = ?", "other").Count(&otherCount).Error; err != nil {
		t.Fatal(err)
	}
	if otherCount != 1 {
		t.Fatalf("expected other namespace flow to remain, got %d", otherCount)
	}
}

func TestFlowBuilder_Rebuild_NoEntrypointsReturnsEmpty(t *testing.T) {
	db, st := setupFlowBuilderTestDB(t)
	ctx := requestctx.WithNamespace(context.Background(), "svc")
	if err := st.UpsertNodes(ctx, []graph.Node{
		{QualifiedName: "pkg.A", Kind: graph.NodeKindFunction, Name: "A", FilePath: "a.go", StartLine: 1, EndLine: 2, Language: "go"},
		{QualifiedName: "pkg.B", Kind: graph.NodeKindFunction, Name: "B", FilePath: "b.go", StartLine: 1, EndLine: 2, Language: "go"},
	}); err != nil {
		t.Fatal(err)
	}
	a, _ := st.GetNode(ctx, "pkg.A")
	b, _ := st.GetNode(ctx, "pkg.B")
	if err := st.UpsertEdges(ctx, []graph.Edge{
		{FromNodeID: a.ID, ToNodeID: b.ID, Kind: graph.EdgeKindCalls, Fingerprint: "a-b"},
		{FromNodeID: b.ID, ToNodeID: a.ID, Kind: graph.EdgeKindCalls, Fingerprint: "b-a"},
	}); err != nil {
		t.Fatal(err)
	}

	stats, err := NewBuilder(st).Rebuild(ctx, Config{})
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 0 {
		t.Fatalf("expected 0 rebuilt flows, got %d", len(stats))
	}
	var count int64
	if err := db.Model(&graph.Flow{}).Where("namespace = ?", "svc").Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("expected 0 persisted flows, got %d", count)
	}
}
