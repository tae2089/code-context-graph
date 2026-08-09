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
				mcp.WithDescription("Full-text search across code nodes, grouped by file. The answer is a list of files, and every file it shows it shows whole: all of that file's hits are in its 'hits' array. Every hit carries the evidence for it — the signals the query matched (name, path, intent) and the node's own @intent tag. Candidates nothing can justify are left out and counted in weak_filtered. 'truncated' is true when more files answered the query than this page reached, and 'next' lists the exact calls that read on. Use 'path' to scope results to a module for token-efficient queries."),
				mcp.WithString("query", mcp.Description("Search query string"), mcp.Required()),
				mcp.WithNumber("limit", mcp.Description("Maximum number of files to return; every hit inside a returned file is included"), mcp.DefaultNumber(10)),
				mcp.WithNumber("offset", mcp.Description("Skip this many files before the page starts, so paging never splits a file"), mcp.DefaultNumber(0)),
				mcp.WithString("path", mcp.Description("Filter results to file paths starting with this prefix (e.g. internal/auth)")),
				mcp.WithBoolean("include_weak", mcp.Description("Also return candidates whose name, path, and @intent say nothing about the query; they are appended after the justified results")),
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
