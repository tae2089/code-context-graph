package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/tae2089/code-context-graph/internal/ctxns"
	"github.com/tae2089/code-context-graph/internal/model"
)

func TestListNamespaces_ReturnsGraphNamespacesWithCounts(t *testing.T) {
	deps := setupTestDeps(t)

	ctxA := ctxns.WithNamespace(context.Background(), "alpha")
	deps.Store.UpsertNodes(ctxA, []model.Node{
		{QualifiedName: "a.F1", Kind: model.NodeKindFunction, Name: "F1", FilePath: "a.go", StartLine: 1, EndLine: 5, Language: "go"},
		{QualifiedName: "a.F2", Kind: model.NodeKindFunction, Name: "F2", FilePath: "a.go", StartLine: 6, EndLine: 9, Language: "go"},
	})
	ctxB := ctxns.WithNamespace(context.Background(), "beta")
	deps.Store.UpsertNodes(ctxB, []model.Node{
		{QualifiedName: "b.G1", Kind: model.NodeKindFunction, Name: "G1", FilePath: "b.go", StartLine: 1, EndLine: 5, Language: "go"},
	})

	result := callTool(t, deps, "list_namespaces", map[string]any{})
	if result.IsError {
		t.Fatalf("list_namespaces error: %s", getTextContent(result))
	}

	var resp struct {
		Namespaces []struct {
			Namespace string `json:"namespace"`
			NodeCount int    `json:"node_count"`
		} `json:"namespaces"`
		Count int `json:"count"`
	}
	if err := json.Unmarshal([]byte(getTextContent(result)), &resp); err != nil {
		t.Fatalf("expected JSON, got: %s", getTextContent(result))
	}

	got := map[string]int{}
	for _, ns := range resp.Namespaces {
		got[ns.Namespace] = ns.NodeCount
	}
	if got["alpha"] != 2 {
		t.Fatalf("alpha node_count = %d, want 2 (resp: %s)", got["alpha"], getTextContent(result))
	}
	if got["beta"] != 1 {
		t.Fatalf("beta node_count = %d, want 1 (resp: %s)", got["beta"], getTextContent(result))
	}
}
