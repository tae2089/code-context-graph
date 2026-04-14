package impact

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

func (m *mockStore) GetEdgesTo(_ context.Context, nodeID uint) ([]model.Edge, error) {
	var result []model.Edge
	for _, edgeList := range m.edges {
		for _, e := range edgeList {
			if e.ToNodeID == nodeID {
				result = append(result, e)
			}
		}
	}
	return result, nil
}

func (m *mockStore) GetNodeByID(_ context.Context, id uint) (*model.Node, error) {
	n, ok := m.nodes[id]
	if !ok {
		return nil, nil
	}
	return n, nil
}

func (m *mockStore) GetEdgesFromNodes(_ context.Context, nodeIDs []uint) ([]model.Edge, error) {
	var result []model.Edge
	for _, id := range nodeIDs {
		result = append(result, m.edges[id]...)
	}
	return result, nil
}

func (m *mockStore) GetEdgesToNodes(_ context.Context, nodeIDs []uint) ([]model.Edge, error) {
	var result []model.Edge
	idMap := make(map[uint]bool)
	for _, id := range nodeIDs {
		idMap[id] = true
	}
	for _, edgeList := range m.edges {
		for _, e := range edgeList {
			if idMap[e.ToNodeID] {
				result = append(result, e)
			}
		}
	}
	return result, nil
}

func (m *mockStore) GetNodesByIDs(_ context.Context, ids []uint) ([]model.Node, error) {
	var result []model.Node
	for _, id := range ids {
		if n, ok := m.nodes[id]; ok {
			result = append(result, *n)
		}
	}
	return result, nil
}

func newNode(id uint, name string) *model.Node {
	return &model.Node{ID: id, QualifiedName: name, Kind: model.NodeKindFunction, Name: name, FilePath: "a.go", StartLine: 1, EndLine: 2, Language: "go"}
}

func edge(from, to uint, idx int) model.Edge {
	return model.Edge{ID: uint(idx), FromNodeID: from, ToNodeID: to, Kind: model.EdgeKindCalls, Fingerprint: fmt.Sprintf("e%d", idx)}
}

func TestImpactRadius_Depth0(t *testing.T) {
	ms := &mockStore{
		nodes: map[uint]*model.Node{1: newNode(1, "A"), 2: newNode(2, "B")},
		edges: map[uint][]model.Edge{1: {edge(1, 2, 1)}},
	}
	a := New(ms)
	got, err := a.ImpactRadius(context.Background(), 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 node, got %d", len(got))
	}
	if got[0].ID != 1 {
		t.Errorf("expected node 1, got %d", got[0].ID)
	}
}

func TestImpactRadius_Depth1(t *testing.T) {
	ms := &mockStore{
		nodes: map[uint]*model.Node{1: newNode(1, "A"), 2: newNode(2, "B"), 3: newNode(3, "C")},
		edges: map[uint][]model.Edge{1: {edge(1, 2, 1), edge(1, 3, 2)}},
	}
	a := New(ms)
	got, err := a.ImpactRadius(context.Background(), 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 nodes, got %d", len(got))
	}
}

func TestImpactRadius_Depth2(t *testing.T) {
	ms := &mockStore{
		nodes: map[uint]*model.Node{1: newNode(1, "A"), 2: newNode(2, "B"), 3: newNode(3, "C")},
		edges: map[uint][]model.Edge{
			1: {edge(1, 2, 1)},
			2: {edge(2, 3, 2)},
		},
	}
	a := New(ms)
	got, err := a.ImpactRadius(context.Background(), 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 nodes, got %d", len(got))
	}
}

func TestImpactRadius_Cycle(t *testing.T) {
	ms := &mockStore{
		nodes: map[uint]*model.Node{1: newNode(1, "A"), 2: newNode(2, "B")},
		edges: map[uint][]model.Edge{
			1: {edge(1, 2, 1)},
			2: {edge(2, 1, 2)},
		},
	}
	a := New(ms)
	got, err := a.ImpactRadius(context.Background(), 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 nodes (cycle handled), got %d", len(got))
	}
}

func TestImpactRadius_Disconnected(t *testing.T) {
	ms := &mockStore{
		nodes: map[uint]*model.Node{1: newNode(1, "A"), 2: newNode(2, "B")},
		edges: map[uint][]model.Edge{},
	}
	a := New(ms)
	got, err := a.ImpactRadius(context.Background(), 1, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 node (disconnected), got %d", len(got))
	}
	if got[0].ID != 1 {
		t.Errorf("expected node 1, got %d", got[0].ID)
	}
}

func TestImpactRadius_LargeGraph(t *testing.T) {
	nodes := make(map[uint]*model.Node, 101)
	edges := make(map[uint][]model.Edge)
	for i := uint(1); i <= 101; i++ {
		nodes[i] = newNode(i, fmt.Sprintf("N%d", i))
	}
	for i := uint(1); i <= 100; i++ {
		edges[i] = append(edges[i], edge(i, i+1, int(i)))
	}

	ms := &mockStore{nodes: nodes, edges: edges}
	a := New(ms)

	got, err := a.ImpactRadius(context.Background(), 1, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 51 {
		t.Fatalf("expected 51 nodes (depth 50 in chain), got %d", len(got))
	}
}
