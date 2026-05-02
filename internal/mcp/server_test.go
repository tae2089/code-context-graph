package mcp

import (
	"context"
	"encoding/json"
	"reflect"
	"sort"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func TestMCPServer_ListTools(t *testing.T) {
	deps := &Deps{}
	srv := NewServer(deps)
	tools := srv.ListTools()

	expected := []string{
		"parse_project",
		"get_node",
		"get_impact_radius",
		"search",
		"get_annotation",
		"trace_flow",
		"build_or_update_graph",
		"run_postprocess",
		"query_graph",
		"list_graph_stats",
		"find_large_functions",
		"detect_changes",
		"get_affected_flows",
		"list_flows",
		"list_communities",
		"get_community",
		"get_architecture_overview",
		"find_dead_code",
		"build_rag_index",
		"get_rag_tree",
		"get_doc_content",
		"search_docs",
		"upload_file",
		"list_namespaces",
		"list_workspaces",
		"list_files",
		"delete_file",
		"upload_files",
		"delete_namespace",
		"delete_workspace",
		"get_minimal_context",
	}

	if len(tools) != len(expected) {
		t.Fatalf("expected %d tools, got %d", len(expected), len(tools))
	}

	for _, name := range expected {
		if _, ok := tools[name]; !ok {
			t.Errorf("tool %q not registered", name)
		}
	}
}

func TestMCPServer_Start(t *testing.T) {
	deps := &Deps{}
	srv := NewServer(deps)

	if srv == nil {
		t.Fatal("expected non-nil MCPServer")
	}

	var _ *server.MCPServer = srv
}

func TestMCPServer_ListTools_18(t *testing.T) {
	deps := &Deps{}
	srv := NewServer(deps)
	tools := srv.ListTools()

	if len(tools) != 31 {
		t.Fatalf("expected 31 tools, got %d", len(tools))
	}
}

func TestMCPServer_ToolDescriptions(t *testing.T) {
	deps := &Deps{}
	srv := NewServer(deps)
	tools := srv.ListTools()

	for name, tool := range tools {
		if tool.Tool.Description == "" {
			t.Errorf("tool %q has empty description", name)
		}
	}
}

func TestMCPServer_IncludePathsArraySchemaHasStringItems(t *testing.T) {
	deps := &Deps{}
	srv := NewServer(deps)
	tools := srv.ListTools()

	for _, toolName := range []string{"parse_project", "build_or_update_graph"} {
		tool, ok := tools[toolName]
		if !ok {
			t.Fatalf("tool %q not registered", toolName)
		}

		prop, ok := tool.Tool.InputSchema.Properties["include_paths"]
		if !ok {
			t.Fatalf("tool %q missing include_paths property", toolName)
		}

		propMap, ok := prop.(map[string]any)
		if !ok {
			t.Fatalf("tool %q include_paths property has unexpected type %T", toolName, prop)
		}

		if got := propMap["type"]; got != "array" {
			t.Fatalf("tool %q include_paths type = %v, want array", toolName, got)
		}

		items, ok := propMap["items"]
		if !ok {
			t.Fatalf("tool %q include_paths schema missing items", toolName)
		}

		itemsMap, ok := items.(map[string]any)
		if !ok {
			t.Fatalf("tool %q include_paths.items has unexpected type %T", toolName, items)
		}

		if got := itemsMap["type"]; got != "string" {
			t.Fatalf("tool %q include_paths.items.type = %v, want string", toolName, got)
		}
	}
}

func TestMCPServer_ReplaceFlagOnBuildOrUpdateGraphOnly(t *testing.T) {
	deps := &Deps{}
	srv := NewServer(deps)
	tools := srv.ListTools()

	buildTool, ok := tools["build_or_update_graph"]
	if !ok {
		t.Fatal("build_or_update_graph not registered")
	}
	if _, ok := buildTool.Tool.InputSchema.Properties["replace"]; !ok {
		t.Fatal("build_or_update_graph missing replace property")
	}

	parseTool, ok := tools["parse_project"]
	if !ok {
		t.Fatal("parse_project not registered")
	}
	if _, ok := parseTool.Tool.InputSchema.Properties["replace"]; ok {
		t.Fatal("parse_project should not expose replace property")
	}
}

func TestMCPServer_ToolRequiredFlags(t *testing.T) {
	deps := &Deps{}
	srv := NewServer(deps)
	tools := srv.ListTools()

	expected := map[string][]string{
		"parse_project":         {"path"},
		"get_node":              {"qualified_name"},
		"get_impact_radius":     {"qualified_name"},
		"search":                {"query"},
		"get_annotation":        {"qualified_name"},
		"trace_flow":            {"qualified_name"},
		"build_or_update_graph": {"path"},
		"run_postprocess":       nil,
		"query_graph":           {"pattern", "target"},
		"list_graph_stats":      nil,
		"find_large_functions":  nil,
		"detect_changes":        {"repo_root"},
		"get_affected_flows":    {"repo_root"},
		"list_flows":            nil,
		"list_communities":      nil,
		"get_community":         {"community_id"},
		"get_architecture_overview": nil,
		"find_dead_code":             nil,
		"build_rag_index":            nil,
		"get_rag_tree":               nil,
		"get_doc_content":            {"file_path"},
		"search_docs":                {"query"},
		"upload_file":                {"namespace", "file_path", "content"},
		"list_namespaces":            nil,
		"list_workspaces":            nil,
		"list_files":                 {"namespace"},
		"delete_file":                {"namespace", "file_path"},
		"upload_files":               {"files"},
		"delete_namespace":           {"namespace"},
		"delete_workspace":           {"workspace"},
		"get_minimal_context":        nil,
	}

	for name, want := range expected {
		tool, ok := tools[name]
		if !ok {
			t.Fatalf("tool %q not registered", name)
		}
		got := append([]string(nil), tool.Tool.InputSchema.Required...)
		sort.Strings(got)
		wantSorted := append([]string(nil), want...)
		sort.Strings(wantSorted)
		if !reflect.DeepEqual(got, wantSorted) {
			t.Fatalf("tool %q required = %v, want %v", name, got, wantSorted)
		}
	}
}

func TestMCPServer_ListPrompts_ExactSetAndRequiredArgs(t *testing.T) {
	deps := &Deps{}
	srv := NewServer(deps)

	msg, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "prompts/list",
	})
	if err != nil {
		t.Fatal(err)
	}

	resp := srv.HandleMessage(context.Background(), msg)
	rpcResp, ok := resp.(mcp.JSONRPCResponse)
	if !ok {
		t.Fatalf("expected JSONRPCResponse, got %T", resp)
	}

	resultJSON, err := json.Marshal(rpcResp.Result)
	if err != nil {
		t.Fatal(err)
	}

	var result map[string]any
	if err := json.Unmarshal(resultJSON, &result); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}

	promptsList, ok := result["prompts"].([]any)
	if !ok {
		t.Fatalf("expected prompts list, got %T", result["prompts"])
	}

	gotPrompts := make(map[string]map[string]any, len(promptsList))
	for _, p := range promptsList {
		pm, ok := p.(map[string]any)
		if !ok {
			t.Fatalf("expected prompt object, got %T", p)
		}
		name, _ := pm["name"].(string)
		gotPrompts[name] = pm
	}

	expectedNames := []string{
		"review_changes",
		"architecture_map",
		"debug_issue",
		"onboard_developer",
		"pre_merge_check",
	}
	if len(gotPrompts) != len(expectedNames) {
		t.Fatalf("expected %d prompts, got %d", len(expectedNames), len(gotPrompts))
	}
	for _, name := range expectedNames {
		if _, ok := gotPrompts[name]; !ok {
			t.Fatalf("prompt %q not registered", name)
		}
	}

	assertRequired := func(promptName string, want []string) {
		t.Helper()
		pm := gotPrompts[promptName]
		argsAny, _ := pm["arguments"].([]any)
		got := make([]string, 0, len(argsAny))
		for _, arg := range argsAny {
			argMap, ok := arg.(map[string]any)
			if !ok {
				t.Fatalf("prompt %q argument has unexpected type %T", promptName, arg)
			}
			name, _ := argMap["name"].(string)
			required, _ := argMap["required"].(bool)
			if required {
				got = append(got, name)
			}
		}
		if len(got) == 0 {
			got = nil
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("prompt %q required args = %v, want %v", promptName, got, want)
		}
	}

	assertRequired("review_changes", []string{"repo_root"})
	assertRequired("architecture_map", nil)
	assertRequired("debug_issue", []string{"description"})
	assertRequired("onboard_developer", nil)
	assertRequired("pre_merge_check", []string{"repo_root"})
}
