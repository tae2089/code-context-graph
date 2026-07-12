package mcp

import (
	"context"
	"encoding/json"
	"testing"

	requestctx "github.com/tae2089/code-context-graph/internal/ctx"
	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

func TestListNamespaces_ReturnsGraphNamespacesWithCounts(t *testing.T) {
	deps := setupTestDeps(t)

	ctxA := requestctx.WithNamespace(context.Background(), "alpha")
	testGraphStoreFor(deps).UpsertNodes(ctxA, []graph.Node{
		{QualifiedName: "a.F1", Kind: graph.NodeKindFunction, Name: "F1", FilePath: "a.go", StartLine: 1, EndLine: 5, Language: "go"},
		{QualifiedName: "a.F2", Kind: graph.NodeKindFunction, Name: "F2", FilePath: "a.go", StartLine: 6, EndLine: 9, Language: "go"},
	})
	ctxB := requestctx.WithNamespace(context.Background(), "beta")
	testGraphStoreFor(deps).UpsertNodes(ctxB, []graph.Node{
		{QualifiedName: "b.G1", Kind: graph.NodeKindFunction, Name: "G1", FilePath: "b.go", StartLine: 1, EndLine: 5, Language: "go"},
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
		Count      int `json:"count"`
		Pagination struct {
			Limit int `json:"limit"`
		} `json:"pagination"`
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
	if resp.Pagination.Limit != 50 {
		t.Fatalf("default pagination limit = %d, want 50", resp.Pagination.Limit)
	}
}

func TestListNamespaces_PreservesPaginationMetadata(t *testing.T) {
	deps := setupTestDeps(t)
	for _, namespace := range []string{"alpha", "beta", "gamma"} {
		ctx := requestctx.WithNamespace(context.Background(), namespace)
		testGraphStoreFor(deps).UpsertNodes(ctx, []graph.Node{{
			QualifiedName: namespace + ".F",
			Kind:          graph.NodeKindFunction,
			Name:          "F",
			FilePath:      namespace + ".go",
			StartLine:     1,
			EndLine:       2,
			Language:      "go",
		}})
	}

	result := callTool(t, deps, "list_namespaces", map[string]any{"limit": 2, "offset": 0})
	if result.IsError {
		t.Fatalf("list_namespaces error: %s", getTextContent(result))
	}

	var resp struct {
		Pagination struct {
			Limit      int  `json:"limit"`
			Offset     int  `json:"offset"`
			Returned   int  `json:"returned"`
			HasMore    bool `json:"has_more"`
			NextOffset *int `json:"next_offset"`
		} `json:"pagination"`
	}
	if err := json.Unmarshal([]byte(getTextContent(result)), &resp); err != nil {
		t.Fatalf("expected JSON, got: %s", getTextContent(result))
	}
	if resp.Pagination.Limit != 2 || resp.Pagination.Offset != 0 || resp.Pagination.Returned != 2 {
		t.Fatalf("pagination = %+v, want limit=2 offset=0 returned=2", resp.Pagination)
	}
	if !resp.Pagination.HasMore || resp.Pagination.NextOffset == nil || *resp.Pagination.NextOffset != 2 {
		t.Fatalf("pagination = %+v, want has_more=true and next_offset=2", resp.Pagination)
	}
}

func TestListNamespaces_RejectsLimitAboveMaximum(t *testing.T) {
	deps := setupTestDeps(t)
	result := callTool(t, deps, "list_namespaces", map[string]any{"limit": maxPaginationLimit + 1})
	if !result.IsError {
		t.Fatal("expected over-maximum limit to return a tool error")
	}
}
