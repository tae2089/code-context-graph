package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	requestctx "github.com/tae2089/code-context-graph/internal/ctx"
	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

// seedDescribeGraph writes one small tree: two files in one folder, one in a nested folder.
func seedDescribeGraph(t *testing.T, deps *Deps) {
	t.Helper()
	ctx := context.Background()
	err := testGraphStoreFor(deps).UpsertNodes(ctx, []graph.Node{
		{QualifiedName: "internal/app/pay/refund.go", Kind: graph.NodeKindFile, Name: "refund.go", FilePath: "internal/app/pay/refund.go", Language: "go"},
		{QualifiedName: "pay.Reject", Kind: graph.NodeKindFunction, Name: "Reject", FilePath: "internal/app/pay/refund.go", StartLine: 40, EndLine: 60, Language: "go"},
		{QualifiedName: "pay.Refund", Kind: graph.NodeKindType, Name: "Refund", FilePath: "internal/app/pay/refund.go", StartLine: 10, EndLine: 20, Language: "go"},
		{QualifiedName: "internal/app/pay/ledger/write.go", Kind: graph.NodeKindFile, Name: "write.go", FilePath: "internal/app/pay/ledger/write.go", Language: "go"},
		{QualifiedName: "ledger.Write", Kind: graph.NodeKindFunction, Name: "Write", FilePath: "internal/app/pay/ledger/write.go", StartLine: 5, EndLine: 9, Language: "go"},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func decodeDescribe(t *testing.T, text string) map[string]any {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal([]byte(text), &payload); err != nil {
		t.Fatalf("expected JSON, got: %s", text)
	}
	return payload
}

func TestDescribe_ListsAFilesDeclarationsWithLinesAndNodeIDs(t *testing.T) {
	deps := setupTestDeps(t)
	seedDescribeGraph(t, deps)

	result := callTool(t, deps, "describe", map[string]any{"target": "internal/app/pay/refund.go"})
	if result.IsError {
		t.Fatalf("describe error: %s", getTextContent(result))
	}

	payload := decodeDescribe(t, getTextContent(result))
	if payload["scope"] != "file" {
		t.Errorf("expected scope=file, got %v", payload["scope"])
	}
	declarations, _ := payload["declarations"].([]any)
	if len(declarations) != 2 {
		t.Fatalf("expected the file's 2 declarations, got %d: %s", len(declarations), getTextContent(result))
	}
	first, _ := declarations[0].(map[string]any)
	if first["qualified_name"] != "pay.Refund" {
		t.Errorf("expected the declarations in written order, got %v first", first["qualified_name"])
	}
	if first["start_line"] != float64(10) {
		t.Errorf("expected a line to open, got start_line %v", first["start_line"])
	}
	if id, ok := first["node_id"].(float64); !ok || id == 0 {
		t.Errorf("expected a node_id the caller can hand to another tool, got %v", first["node_id"])
	}
}

func TestDescribe_CollapsesAFolderToItsImmediateChildren(t *testing.T) {
	deps := setupTestDeps(t)
	seedDescribeGraph(t, deps)

	result := callTool(t, deps, "describe", map[string]any{"target": "internal/app/pay"})
	if result.IsError {
		t.Fatalf("describe error: %s", getTextContent(result))
	}

	payload := decodeDescribe(t, getTextContent(result))
	if payload["scope"] != "directory" {
		t.Errorf("expected scope=directory, got %v", payload["scope"])
	}
	children, _ := payload["children"].([]any)
	paths := make([]string, 0, len(children))
	for _, child := range children {
		entry, _ := child.(map[string]any)
		paths = append(paths, entry["path"].(string))
	}
	want := []string{"internal/app/pay/ledger", "internal/app/pay/refund.go"}
	if strings.Join(paths, ",") != strings.Join(want, ",") {
		t.Errorf("expected one level down %v, got %v", want, paths)
	}
	if payload["declarations"] != nil {
		t.Errorf("expected a folder to list children, not declarations: %s", getTextContent(result))
	}
}

func TestDescribe_AnswersAMissedTargetWithWhereToLookInstead(t *testing.T) {
	deps := setupTestDeps(t)
	seedDescribeGraph(t, deps)

	result := callTool(t, deps, "describe", map[string]any{"target": "Reject"})
	if result.IsError {
		t.Fatalf("a name that is not a path must not be an error: %s", getTextContent(result))
	}

	payload := decodeDescribe(t, getTextContent(result))
	if payload["scope"] != "unknown" {
		t.Fatalf("expected scope=unknown, got %v", payload["scope"])
	}
	suggestions, _ := payload["suggestions"].([]any)
	if len(suggestions) == 0 {
		t.Fatalf("expected the place that name is declared: %s", getTextContent(result))
	}
	first, _ := suggestions[0].(map[string]any)
	if first["file_path"] != "internal/app/pay/refund.go" {
		t.Errorf("expected the file holding Reject, got %v", first["file_path"])
	}
	next, _ := payload["next"].([]any)
	if len(next) == 0 {
		t.Fatal("expected a call the caller can make next")
	}
	action, _ := next[0].(map[string]any)
	if action["tool"] != "describe" {
		t.Errorf("expected the next call to describe the file, got %v", action["tool"])
	}
	args, _ := action["args"].(map[string]any)
	if args["target"] != "internal/app/pay/refund.go" {
		t.Errorf("expected ready-to-send args, got %v", args)
	}
}

func TestDescribe_SendsATargetNobodyDeclaredToTheRankedTools(t *testing.T) {
	deps := setupTestDeps(t)
	seedDescribeGraph(t, deps)

	result := callTool(t, deps, "describe", map[string]any{"target": "nothing/like/this"})
	if result.IsError {
		t.Fatalf("describe error: %s", getTextContent(result))
	}

	payload := decodeDescribe(t, getTextContent(result))
	if payload["scope"] != "unknown" {
		t.Fatalf("expected scope=unknown, got %v", payload["scope"])
	}
	next, _ := payload["next"].([]any)
	tools := make([]string, 0, len(next))
	for _, entry := range next {
		action, _ := entry.(map[string]any)
		tools = append(tools, action["tool"].(string))
	}
	if strings.Join(tools, ",") != "search,find_by_intent" {
		t.Errorf("expected a hand-off to the ranked tools, got %v", tools)
	}
}

func TestDescribe_KeepsOneNamespacesFileOutOfAnothers(t *testing.T) {
	deps := setupTestDeps(t)
	store := testGraphStoreFor(deps)
	seed := map[string]graph.Node{
		"ns-a": {QualifiedName: "a.Foo", Kind: graph.NodeKindFunction, Name: "Foo", FilePath: "pkg/shared.go", StartLine: 1, EndLine: 4, Language: "go"},
		"ns-b": {QualifiedName: "b.Bar", Kind: graph.NodeKindFunction, Name: "Bar", FilePath: "pkg/shared.go", StartLine: 6, EndLine: 9, Language: "go"},
	}
	for namespace, node := range seed {
		ctx := requestctx.WithNamespace(context.Background(), namespace)
		if err := store.UpsertNodes(ctx, []graph.Node{node}); err != nil {
			t.Fatal(err)
		}
	}

	result := callTool(t, deps, "describe", map[string]any{"target": "pkg/shared.go", "namespace": "ns-a"})
	if result.IsError {
		t.Fatalf("describe error: %s", getTextContent(result))
	}

	payload := decodeDescribe(t, getTextContent(result))
	declarations, _ := payload["declarations"].([]any)
	if len(declarations) != 1 {
		t.Fatalf("expected only ns-a's declaration, got %d: %s", len(declarations), getTextContent(result))
	}
	only, _ := declarations[0].(map[string]any)
	if only["qualified_name"] != "a.Foo" {
		t.Errorf("expected a.Foo, got %v", only["qualified_name"])
	}
}

func TestDescribe_RefusesACallWithNoTarget(t *testing.T) {
	deps := setupTestDeps(t)

	result := callTool(t, deps, "describe", map[string]any{})
	if !result.IsError {
		t.Fatalf("expected a missing target to be rejected, got: %s", getTextContent(result))
	}
}

func TestDescribe_SaysItIsUnconfiguredRatherThanPanicking(t *testing.T) {
	deps := setupTestDeps(t)
	deps.Graph.Describe = nil

	result := callTool(t, deps, "describe", map[string]any{"target": "internal/app/pay"})
	if !result.IsError {
		t.Fatalf("expected an unconfigured describe to report itself, got: %s", getTextContent(result))
	}
	if !strings.Contains(getTextContent(result), "not configured") {
		t.Errorf("expected a configuration message, got: %s", getTextContent(result))
	}
}
