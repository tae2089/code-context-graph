// @index MCP tool registration for node lookup and graph query primitives.
package mcp

import (
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// withNamespaceParam appends canonical namespace arguments to a tool definition.
// @intent give every namespace-aware MCP tool the same isolation parameter.
func withNamespaceParam(opts ...mcp.ToolOption) []mcp.ToolOption {
	return append(opts,
		mcp.WithString("namespace", mcp.Description("Namespace for isolation")),
	)
}

// withFederatedNamespaceParams appends single- and multi-namespace arguments to a tool definition.
// @intent let federated read tools accept an explicit namespace set alongside the canonical single namespace.
func withFederatedNamespaceParams(opts ...mcp.ToolOption) []mcp.ToolOption {
	return append(withNamespaceParam(opts...),
		mcp.WithArray("namespaces", mcp.Description("Federate this call across multiple namespaces (overrides 'namespace'); results are labeled per namespace"), mcp.WithStringItems()),
	)
}

// queryTools registers lookup and traversal tools over the stored graph.
// @intent expose reusable graph query primitives that other prompts and agents can compose.
func queryTools(h *handlers) []server.ServerTool {
	return []server.ServerTool{
		{
			Tool: mcp.NewTool("get_node", withNamespaceParam(
				mcp.WithDescription("Get a node by its qualified name"),
				mcp.WithString("qualified_name", mcp.Description("Fully qualified node name"), mcp.Required()),
			)...),
			Handler: h.getNode,
		},
		{
			Tool: mcp.NewTool("search", withFederatedNamespaceParams(
				mcp.WithDescription("Full-text search across code nodes. Use 'path' to scope results to a module for token-efficient queries."),
				mcp.WithString("query", mcp.Description("Search query string"), mcp.Required()),
				mcp.WithNumber("limit", mcp.Description("Maximum number of results"), mcp.DefaultNumber(10)),
				mcp.WithString("path", mcp.Description("Filter results to file paths starting with this prefix (e.g. internal/auth)")),
			)...),
			Handler: h.search,
		},
		{
			Tool: mcp.NewTool("get_annotation", withNamespaceParam(
				mcp.WithDescription("Get annotation and doc tags for a node"),
				mcp.WithString("qualified_name", mcp.Description("Fully qualified node name"), mcp.Required()),
			)...),
			Handler: h.getAnnotation,
		},
		{
			Tool: mcp.NewTool("query_graph", withFederatedNamespaceParams(
				mcp.WithDescription("Run predefined graph queries: callers_of, callees_of, imports_of, importers_of, children_of, tests_for, inheritors_of, file_summary"),
				mcp.WithString("pattern", mcp.Description("Query pattern"), mcp.Required()),
				mcp.WithString("target", mcp.Description("Target qualified name or file path"), mcp.Required()),
				mcp.WithNumber("limit", mcp.Description("Maximum number of results returned (default: 50, max: 500)"), mcp.DefaultNumber(50)),
				mcp.WithNumber("offset", mcp.Description("Zero-based result offset for pagination (default: 0)"), mcp.DefaultNumber(0)),
				mcp.WithBoolean("include_fallback_calls", mcp.Description("When false, callers_of/callees_of exclude fallback_calls edges; defaults to true")),
			)...),
			Handler: h.queryGraph,
		},
		{
			Tool: mcp.NewTool("list_graph_stats", withFederatedNamespaceParams(
				mcp.WithDescription("Get graph statistics: total nodes, edges, and breakdowns by kind and language"),
			)...),
			Handler: h.listGraphStats,
		},
		{
			Tool: mcp.NewTool("list_namespaces",
				mcp.WithDescription("List namespaces that hold graph data with per-namespace node counts, for scoping cross-namespace queries"),
				mcp.WithNumber("limit", mcp.Description("Maximum namespaces to return (default: 50)"), mcp.DefaultNumber(50)),
				mcp.WithNumber("offset", mcp.Description("Zero-based result offset for pagination (default: 0)"), mcp.DefaultNumber(0)),
			),
			Handler: h.listNamespaces,
		},
	}
}
