package mcp

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"testing"

	"github.com/tae2089/code-context-graph/internal/model"
	"github.com/tae2089/code-context-graph/internal/paging"
)

func TestHandler_AnalysisResponses_WireContractFrozen(t *testing.T) {
	t.Run("get_impact_radius", func(t *testing.T) {
		deps := setupTestDeps(t)
		ctx := context.Background()

		if err := deps.Store.UpsertNodes(ctx, []model.Node{
			{QualifiedName: "pkg.A", Kind: model.NodeKindFunction, Name: "A", FilePath: "a.go", StartLine: 1, EndLine: 5, Language: "go"},
			{QualifiedName: "pkg.B", Kind: model.NodeKindFunction, Name: "B", FilePath: "b.go", StartLine: 1, EndLine: 5, Language: "go"},
		}); err != nil {
			t.Fatal(err)
		}
		nodeA, _ := deps.Store.GetNode(ctx, "pkg.A")
		nodeB, _ := deps.Store.GetNode(ctx, "pkg.B")
		if err := deps.Store.UpsertEdges(ctx, []model.Edge{{FromNodeID: nodeA.ID, ToNodeID: nodeB.ID, Kind: model.EdgeKindCalls, Fingerprint: "calls-a-b"}}); err != nil {
			t.Fatal(err)
		}

		result := callTool(t, deps, "get_impact_radius", map[string]any{
			"qualified_name": "pkg.A",
			"depth":          1,
		})
		if result.IsError {
			t.Fatalf("get_impact_radius returned error: %s", getTextContent(result))
		}

		var resp map[string]any
		if err := json.Unmarshal([]byte(getTextContent(result)), &resp); err != nil {
			t.Fatalf("expected JSON response, got: %s", getTextContent(result))
		}

		if !reflect.DeepEqual(sortedKeys(resp), []string{"metadata", "nodes"}) {
			t.Fatalf("unexpected top-level keys: %v", sortedKeys(resp))
		}
		metadata, ok := resp["metadata"].(map[string]any)
		if !ok {
			t.Fatalf("metadata type = %T, want map[string]any", resp["metadata"])
		}
		if !reflect.DeepEqual(sortedKeys(metadata), []string{"max_depth", "max_nodes", "returned_nodes", "truncated"}) {
			t.Fatalf("unexpected metadata keys: %v", sortedKeys(metadata))
		}
		nodes, ok := resp["nodes"].([]any)
		if !ok || len(nodes) == 0 {
			t.Fatalf("nodes = %T/%v, want non-empty []any", resp["nodes"], resp["nodes"])
		}
		firstNode, ok := nodes[0].(map[string]any)
		if !ok {
			t.Fatalf("first node type = %T, want map[string]any", nodes[0])
		}
		if !reflect.DeepEqual(sortedKeys(firstNode), []string{"file_path", "id", "kind", "name", "qualified_name"}) {
			t.Fatalf("unexpected node keys: %v", sortedKeys(firstNode))
		}
	})

	t.Run("trace_flow", func(t *testing.T) {
		deps := setupGraphOnlyTestDeps(t)
		ctx := context.Background()

		if err := deps.Store.UpsertNodes(ctx, []model.Node{
			{QualifiedName: "pkg.Start", Kind: model.NodeKindFunction, Name: "Start", FilePath: "start.go", StartLine: 1, EndLine: 5, Language: "go"},
			{QualifiedName: "pkg.Next", Kind: model.NodeKindFunction, Name: "Next", FilePath: "next.go", StartLine: 1, EndLine: 5, Language: "go"},
		}); err != nil {
			t.Fatal(err)
		}
		start, _ := deps.Store.GetNode(ctx, "pkg.Start")
		next, _ := deps.Store.GetNode(ctx, "pkg.Next")
		if err := deps.Store.UpsertEdges(ctx, []model.Edge{{FromNodeID: start.ID, ToNodeID: next.ID, Kind: model.EdgeKindCalls, Fingerprint: "calls-s-n"}}); err != nil {
			t.Fatal(err)
		}

		result := callTool(t, deps, "trace_flow", map[string]any{"qualified_name": "pkg.Start"})
		if result.IsError {
			t.Fatalf("trace_flow returned error: %s", getTextContent(result))
		}

		var resp map[string]any
		if err := json.Unmarshal([]byte(getTextContent(result)), &resp); err != nil {
			t.Fatalf("expected JSON response, got: %s", getTextContent(result))
		}

		if !reflect.DeepEqual(sortedKeys(resp), []string{"evidence", "members", "metadata", "name"}) {
			t.Fatalf("unexpected top-level keys: %v", sortedKeys(resp))
		}
		metadata, ok := resp["metadata"].(map[string]any)
		if !ok {
			t.Fatalf("metadata type = %T, want map[string]any", resp["metadata"])
		}
		if !reflect.DeepEqual(sortedKeys(metadata), []string{"contains_fallback_calls", "fallback_edges_count", "max_nodes", "returned_nodes", "truncated"}) {
			t.Fatalf("unexpected metadata keys: %v", sortedKeys(metadata))
		}
		members, ok := resp["members"].([]any)
		if !ok || len(members) == 0 {
			t.Fatalf("members = %T/%v, want non-empty []any", resp["members"], resp["members"])
		}
		firstMember, ok := members[0].(map[string]any)
		if !ok {
			t.Fatalf("first member type = %T, want map[string]any", members[0])
		}
		if !reflect.DeepEqual(sortedKeys(firstMember), []string{"node_id", "ordinal"}) {
			t.Fatalf("unexpected member keys: %v", sortedKeys(firstMember))
		}
		evidence, ok := resp["evidence"].(map[string]any)
		if !ok {
			t.Fatalf("evidence type = %T, want map[string]any", resp["evidence"])
		}
		if !reflect.DeepEqual(sortedKeys(evidence), []string{"namespace"}) {
			t.Fatalf("unexpected evidence keys: %v", sortedKeys(evidence))
		}
	})
}

func TestPagedListResponse_MarshalJSON_PreservesEnvelope(t *testing.T) {
	b, err := json.Marshal(pagedListResponse[string]{
		LegacyKey:  "dead_code",
		Items:      []string{"one"},
		Count:      1,
		Pagination: paging.Page{Limit: 10, Offset: 0, Returned: 1, HasMore: false},
	})
	if err != nil {
		t.Fatal(err)
	}

	var resp map[string]any
	if err := json.Unmarshal(b, &resp); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(sortedKeys(resp), []string{"count", "dead_code", "items", "pagination"}) {
		t.Fatalf("unexpected top-level keys: %v", sortedKeys(resp))
	}
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
