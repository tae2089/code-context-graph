package flows

import (
	"context"
	"fmt"
	"testing"

	"github.com/imtaebin/code-context-graph/internal/model"
)

type mockStore struct {
	nodes map[uint]*model.Node
	edges map[uint][]model.Edge
}

func (m *mockStore) GetEdgesFrom(_ context.Context, nodeID uint) ([]model.Edge, error) {
	return m.edges[nodeID], nil
}

func (m *mockStore) GetNodeByID(_ context.Context, id uint) (*model.Node, error) {
	n, ok := m.nodes[id]
	if !ok {
		return nil, nil
	}
	return n, nil
}

func newNode(id uint, name string) *model.Node {
	return &model.Node{ID: id, QualifiedName: name, Kind: model.NodeKindFunction, Name: name, FilePath: "a.go", StartLine: 1, EndLine: 2, Language: "go"}
}

func callEdge(from, to uint, idx int) model.Edge {
	return model.Edge{ID: uint(idx), FromNodeID: from, ToNodeID: to, Kind: model.EdgeKindCalls, Fingerprint: fmt.Sprintf("e%d", idx)}
}

func TestTraceFlow_SimpleChain(t *testing.T) {
	ms := &mockStore{
		nodes: map[uint]*model.Node{1: newNode(1, "A"), 2: newNode(2, "B"), 3: newNode(3, "C")},
		edges: map[uint][]model.Edge{
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

func TestTraceFlow_Branch(t *testing.T) {
	ms := &mockStore{
		nodes: map[uint]*model.Node{1: newNode(1, "A"), 2: newNode(2, "B"), 3: newNode(3, "C")},
		edges: map[uint][]model.Edge{
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
		nodes: map[uint]*model.Node{1: newNode(1, "A"), 2: newNode(2, "B"), 3: newNode(3, "C"), 4: newNode(4, "D")},
		edges: map[uint][]model.Edge{
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
		nodes: map[uint]*model.Node{1: newNode(1, "A")},
		edges: map[uint][]model.Edge{},
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
