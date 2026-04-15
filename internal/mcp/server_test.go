package mcp

import (
	"testing"

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
		"list_workspaces",
		"list_files",
		"delete_file",
		"upload_files",
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

	if len(tools) != 29 {
		t.Fatalf("expected 29 tools, got %d", len(tools))
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
