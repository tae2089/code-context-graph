package benchmark

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"gorm.io/gorm"

	"github.com/tae2089/code-context-graph/internal/model"
)

// mockSearchBackend는 테스트용 검색 백엔드다.
type mockSearchBackend struct {
	nodes []model.Node
}

func (m *mockSearchBackend) Query(_ context.Context, _ *gorm.DB, _ string, _ int) ([]model.Node, error) {
	return m.nodes, nil
}

// mockNodeExpander는 테스트용 노드 확장기다.
type mockNodeExpander struct {
	edges map[uint][]model.Edge
	nodes map[uint]model.Node
}

func (m *mockNodeExpander) GetEdgesFrom(_ context.Context, nodeID uint) ([]model.Edge, error) {
	return m.edges[nodeID], nil
}

func (m *mockNodeExpander) GetNodesByIDs(_ context.Context, ids []uint) ([]model.Node, error) {
	var result []model.Node
	for _, id := range ids {
		if n, ok := m.nodes[id]; ok {
			result = append(result, n)
		}
	}
	return result, nil
}

func (m *mockNodeExpander) GetAnnotation(_ context.Context, _ uint) (*model.Annotation, error) {
	return nil, nil
}

func TestEstimateTokens_FourCharsPerToken(t *testing.T) {
	if got := EstimateTokens("abcd"); got != 1 {
		t.Errorf("want 1, got %d", got)
	}
	if got := EstimateTokens("abcdefgh"); got != 2 {
		t.Errorf("want 2, got %d", got)
	}
	if got := EstimateTokens(""); got != 0 {
		t.Errorf("want 0, got %d", got)
	}
}

func TestNaiveTokens_CountsSourceFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("abcd"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "util.go"), []byte("abcdefgh"), 0o600); err != nil {
		t.Fatal(err)
	}
	// .txt는 집계 제외
	if err := os.WriteFile(filepath.Join(dir, "readme.txt"), []byte("abcdabcdabcd"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := NaiveTokens(dir, []string{".go"})
	if err != nil {
		t.Fatal(err)
	}
	// main.go: 1, util.go: 2 → 합계 3
	if got != 3 {
		t.Errorf("want 3, got %d", got)
	}
}

func TestNaiveTokens_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	got, err := NaiveTokens(dir, []string{".go"})
	if err != nil {
		t.Fatal(err)
	}
	if got != 0 {
		t.Errorf("want 0, got %d", got)
	}
}

func TestGraphTokens_SumNodeText(t *testing.T) {
	backend := &mockSearchBackend{
		nodes: []model.Node{
			{QualifiedName: "pkg.Foo", Kind: model.NodeKindFunction, FilePath: "foo.go"},
			{QualifiedName: "pkg.Bar", Kind: model.NodeKindFunction, FilePath: "bar.go"},
		},
	}
	tokens, elapsed, count, err := GraphTokens(context.Background(), nil, backend, nil, "foo", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Errorf("want count=2, got %d", count)
	}
	if tokens <= 0 {
		t.Errorf("want tokens>0, got %d", tokens)
	}
	if elapsed < 0 {
		t.Errorf("want elapsed>=0, got %d", elapsed)
	}
}

func TestGraphTokens_WithExpander_IncludesNeighbors(t *testing.T) {
	searchBackend := &mockSearchBackend{
		nodes: []model.Node{
			{ID: 1, QualifiedName: "pkg.Foo", Kind: model.NodeKindFunction, FilePath: "foo.go"},
		},
	}
	expander := &mockNodeExpander{
		edges: map[uint][]model.Edge{
			1: {{FromNodeID: 1, ToNodeID: 2, Kind: model.EdgeKindCalls}},
		},
		nodes: map[uint]model.Node{
			2: {ID: 2, QualifiedName: "pkg.Bar", Kind: model.NodeKindFunction, FilePath: "bar.go"},
		},
	}

	tokensWithout, _, _, err := GraphTokens(context.Background(), nil, searchBackend, nil, "foo", "", 10)

	if err != nil {
		t.Fatal(err)
	}
	tokensWith, _, _, err := GraphTokens(context.Background(), nil, searchBackend, expander, "foo", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if tokensWith <= tokensWithout {
		t.Errorf("want tokensWith(%d) > tokensWithout(%d)", tokensWith, tokensWithout)
	}
}

func TestRunTokenBench_RatioCalculated(t *testing.T) {
	dir := t.TempDir()
	// 400자 = 100 tokens
	content := make([]byte, 400)
	for i := range content {
		content[i] = 'a'
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), content, 0o600); err != nil {
		t.Fatal(err)
	}

	backend := &mockSearchBackend{
		nodes: []model.Node{
			{QualifiedName: "pkg.Foo", Kind: model.NodeKindFunction, FilePath: "foo.go"},
		},
	}
	corpus := &Corpus{
		Queries: []Query{
			{ID: "q1", Description: "foo 함수 설명"},
		},
	}

	results, err := RunTokenBench(context.Background(), nil, backend, nil, corpus, dir, []string{".go"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("want 1 result, got %d", len(results))
	}
	r := results[0]
	if r.QueryID != "q1" {
		t.Errorf("want query_id=q1, got %s", r.QueryID)
	}
	if r.NaiveTokens != 100 {
		t.Errorf("want naive_tokens=100, got %d", r.NaiveTokens)
	}
	if r.GraphTokens <= 0 {
		t.Errorf("want graph_tokens>0, got %d", r.GraphTokens)
	}
	if r.Ratio <= 0 {
		t.Errorf("want ratio>0, got %f", r.Ratio)
	}
}
