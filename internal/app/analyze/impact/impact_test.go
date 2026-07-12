// Impact traversal characterization tests.
package impact

import (
	"context"
	"fmt"
	"testing"

	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

type mockStore struct {
	nodes               map[uint]*graph.Node
	edges               map[uint][]graph.Edge
	lastGetNodesByIDs   []uint
	getEdgesToNodesFunc func(context.Context, []uint) ([]graph.Edge, error)
}

func (m *mockStore) GetEdgesFrom(_ context.Context, nodeID uint) ([]graph.Edge, error) {
	return m.edges[nodeID], nil
}

func (m *mockStore) GetEdgesTo(_ context.Context, nodeID uint) ([]graph.Edge, error) {
	var result []graph.Edge
	for _, edgeList := range m.edges {
		for _, e := range edgeList {
			if e.ToNodeID == nodeID {
				result = append(result, e)
			}
		}
	}
	return result, nil
}

func (m *mockStore) GetNodeByID(_ context.Context, id uint) (*graph.Node, error) {
	n, ok := m.nodes[id]
	if !ok {
		return nil, nil
	}
	return n, nil
}

func (m *mockStore) GetEdgesFromNodes(_ context.Context, nodeIDs []uint) ([]graph.Edge, error) {
	var result []graph.Edge
	for _, id := range nodeIDs {
		result = append(result, m.edges[id]...)
	}
	return result, nil
}

func (m *mockStore) GetEdgesToNodes(_ context.Context, nodeIDs []uint) ([]graph.Edge, error) {
	if m.getEdgesToNodesFunc != nil {
		return m.getEdgesToNodesFunc(context.Background(), nodeIDs)
	}
	var result []graph.Edge
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

func (m *mockStore) GetNodesByIDs(_ context.Context, ids []uint) ([]graph.Node, error) {
	m.lastGetNodesByIDs = append([]uint(nil), ids...)
	var result []graph.Node
	for _, id := range ids {
		if n, ok := m.nodes[id]; ok {
			result = append(result, *n)
		}
	}
	return result, nil
}

func nodeIDs(nodes []graph.Node) []uint {
	ids := make([]uint, 0, len(nodes))
	for _, node := range nodes {
		ids = append(ids, node.ID)
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

func newNode(id uint, name string) *graph.Node {
	return &graph.Node{ID: id, QualifiedName: name, Kind: graph.NodeKindFunction, Name: name, FilePath: "a.go", StartLine: 1, EndLine: 2, Language: "go"}
}

func edge(from, to uint, idx int) graph.Edge {
	return graph.Edge{ID: uint(idx), FromNodeID: from, ToNodeID: to, Kind: graph.EdgeKindCalls, Fingerprint: fmt.Sprintf("e%d", idx)}
}

func TestImpactRadius_Depth0(t *testing.T) {
	ms := &mockStore{
		nodes: map[uint]*graph.Node{1: newNode(1, "A"), 2: newNode(2, "B")},
		edges: map[uint][]graph.Edge{1: {edge(1, 2, 1)}},
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
		nodes: map[uint]*graph.Node{1: newNode(1, "A"), 2: newNode(2, "B"), 3: newNode(3, "C")},
		edges: map[uint][]graph.Edge{1: {edge(1, 2, 1), edge(1, 3, 2)}},
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
		nodes: map[uint]*graph.Node{1: newNode(1, "A"), 2: newNode(2, "B"), 3: newNode(3, "C")},
		edges: map[uint][]graph.Edge{
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
		nodes: map[uint]*graph.Node{1: newNode(1, "A"), 2: newNode(2, "B")},
		edges: map[uint][]graph.Edge{
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
		nodes: map[uint]*graph.Node{1: newNode(1, "A"), 2: newNode(2, "B")},
		edges: map[uint][]graph.Edge{},
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
	nodes := make(map[uint]*graph.Node, 101)
	edges := make(map[uint][]graph.Edge)
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

func TestImpactRadiusBounded_ExactMaxNodesDoesNotVisitOrReturnExtraNode(t *testing.T) {
	ms := &mockStore{
		nodes: map[uint]*graph.Node{
			1: newNode(1, "A"),
			2: newNode(2, "B"),
			3: newNode(3, "C"),
			4: newNode(4, "D"),
		},
		edges: map[uint][]graph.Edge{1: {edge(1, 2, 1), edge(1, 3, 2), edge(1, 4, 3)}},
	}
	a := New(ms)

	result, err := a.ImpactRadiusBounded(context.Background(), 1, 1, RadiusOptions{MaxNodes: 3})
	if err != nil {
		t.Fatal(err)
	}

	assertUintSliceEqual(t, nodeIDs(result.Nodes), []uint{1, 2, 3})
	assertUintSliceEqual(t, ms.lastGetNodesByIDs, []uint{1, 2, 3})
	if !result.Truncated {
		t.Fatalf("expected truncated result when a fourth node was reachable beyond MaxNodes")
	}
	if result.ReturnedNodes != 3 {
		t.Fatalf("expected ReturnedNodes 3, got %d", result.ReturnedNodes)
	}
}

func TestImpactRadiusBounded_PreservesDeterministicVisitOrder(t *testing.T) {
	ms := &mockStore{
		nodes: map[uint]*graph.Node{
			1: newNode(1, "A"),
			2: newNode(2, "B"),
			3: newNode(3, "C"),
			4: newNode(4, "D"),
			5: newNode(5, "E"),
		},
		edges: map[uint][]graph.Edge{
			1: {edge(1, 2, 1), edge(1, 3, 2)},
			2: {edge(2, 5, 5)},
			3: {edge(3, 5, 6)},
			4: {edge(4, 1, 3)},
		},
	}
	ms.getEdgesToNodesFunc = func(_ context.Context, nodeIDs []uint) ([]graph.Edge, error) {
		switch {
		case len(nodeIDs) == 1 && nodeIDs[0] == 1:
			return []graph.Edge{edge(4, 1, 3)}, nil
		default:
			return nil, nil
		}
	}
	a := New(ms)

	for range 20 {
		result, err := a.ImpactRadiusBounded(context.Background(), 1, 2, RadiusOptions{})
		if err != nil {
			t.Fatal(err)
		}
		assertUintSliceEqual(t, nodeIDs(result.Nodes), []uint{1, 2, 3, 4, 5})
		assertUintSliceEqual(t, ms.lastGetNodesByIDs, []uint{1, 2, 3, 4, 5})
		if result.Truncated {
			t.Fatalf("did not expect truncation")
		}
	}
}
