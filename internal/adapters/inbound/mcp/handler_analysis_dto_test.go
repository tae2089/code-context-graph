package mcp

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"testing"

	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

func TestHandler_AnalysisResponses_WireContractFrozen(t *testing.T) {
	t.Run("get_impact_radius", func(t *testing.T) {
		deps := setupTestDeps(t)
		ctx := context.Background()

		if err := testGraphStoreFor(deps).UpsertNodes(ctx, []graph.Node{
			{QualifiedName: "pkg.A", Kind: graph.NodeKindFunction, Name: "A", FilePath: "a.go", StartLine: 1, EndLine: 5, Language: "go"},
			{QualifiedName: "pkg.B", Kind: graph.NodeKindFunction, Name: "B", FilePath: "b.go", StartLine: 1, EndLine: 5, Language: "go"},
		}); err != nil {
			t.Fatal(err)
		}
		nodeA, _ := testGraphStoreFor(deps).GetNode(ctx, "pkg.A")
		nodeB, _ := testGraphStoreFor(deps).GetNode(ctx, "pkg.B")
		if err := testGraphStoreFor(deps).UpsertEdges(ctx, []graph.Edge{{FromNodeID: nodeA.ID, ToNodeID: nodeB.ID, Kind: graph.EdgeKindCalls, Fingerprint: "calls-a-b"}}); err != nil {
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

		if err := testGraphStoreFor(deps).UpsertNodes(ctx, []graph.Node{
			{QualifiedName: "pkg.Start", Kind: graph.NodeKindFunction, Name: "Start", FilePath: "start.go", StartLine: 1, EndLine: 5, Language: "go"},
			{QualifiedName: "pkg.Next", Kind: graph.NodeKindFunction, Name: "Next", FilePath: "next.go", StartLine: 1, EndLine: 5, Language: "go"},
		}); err != nil {
			t.Fatal(err)
		}
		start, _ := testGraphStoreFor(deps).GetNode(ctx, "pkg.Start")
		next, _ := testGraphStoreFor(deps).GetNode(ctx, "pkg.Next")
		if err := testGraphStoreFor(deps).UpsertEdges(ctx, []graph.Edge{{FromNodeID: start.ID, ToNodeID: next.ID, Kind: graph.EdgeKindCalls, Fingerprint: "calls-s-n"}}); err != nil {
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

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
