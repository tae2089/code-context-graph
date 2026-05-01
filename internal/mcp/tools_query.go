package mcp

import (
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func queryTools(h *handlers) []server.ServerTool {
	return []server.ServerTool{
		{
			Tool: mcp.NewTool("get_node",
				mcp.WithDescription("Get a node by its qualified name"),
				mcp.WithString("qualified_name", mcp.Description("Fully qualified node name"), mcp.Required()),
				mcp.WithString("workspace", mcp.Description("Workspace name for namespace isolation")),
			),
			Handler: h.getNode,
		},
		{
			Tool: mcp.NewTool("search",
				mcp.WithDescription("Full-text search across code nodes. Use 'path' to scope results to a module for token-efficient queries."),
				mcp.WithString("query", mcp.Description("Search query string"), mcp.Required()),
				mcp.WithNumber("limit", mcp.Description("Maximum number of results"), mcp.DefaultNumber(10)),
				mcp.WithString("path", mcp.Description("Filter results to file paths starting with this prefix (e.g. internal/auth)")),
				mcp.WithString("workspace", mcp.Description("Workspace name for namespace isolation")),
			),
			Handler: h.search,
		},
		{
			Tool: mcp.NewTool("get_annotation",
				mcp.WithDescription("Get annotation and doc tags for a node"),
				mcp.WithString("qualified_name", mcp.Description("Fully qualified node name"), mcp.Required()),
				mcp.WithString("workspace", mcp.Description("Workspace name for namespace isolation")),
			),
			Handler: h.getAnnotation,
		},
		{
			Tool: mcp.NewTool("query_graph",
				mcp.WithDescription("Run predefined graph queries: callers_of, callees_of, imports_of, importers_of, children_of, tests_for, inheritors_of, file_summary"),
				mcp.WithString("pattern", mcp.Description("Query pattern"), mcp.Required()),
				mcp.WithString("target", mcp.Description("Target qualified name or file path"), mcp.Required()),
				mcp.WithString("workspace", mcp.Description("Workspace name for namespace isolation")),
			),
			Handler: h.queryGraph,
		},
		{
			Tool: mcp.NewTool("list_graph_stats",
				mcp.WithDescription("Get graph statistics: total nodes, edges, and breakdowns by kind and language"),
				mcp.WithString("workspace", mcp.Description("Workspace name for namespace isolation")),
			),
			Handler: h.listGraphStats,
		},
		{
			Tool: mcp.NewTool("find_large_functions",
				mcp.WithDescription("Find functions exceeding a line count threshold"),
				mcp.WithNumber("min_lines", mcp.Description("Minimum line count threshold (default: 50)")),
				mcp.WithNumber("limit", mcp.Description("Maximum number of results (default: 50)")),
				mcp.WithString("path", mcp.Description("Filter results to file paths starting with this prefix")),
				mcp.WithString("workspace", mcp.Description("Workspace name for namespace isolation")),
			),
			Handler: h.findLargeFunctions,
		},
	}
}
