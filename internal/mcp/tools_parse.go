package mcp

import (
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func parseTools(h *handlers) []server.ServerTool {
	return []server.ServerTool{
		{
			Tool: mcp.NewTool("parse_project", withNamespaceParam(
				mcp.WithDescription("Parse source files and store nodes/edges in the graph database"),
				mcp.WithString("path", mcp.Description("Project directory path to parse"), mcp.Required()),
				mcp.WithArray("include_paths", mcp.Description("Only include specific sub-paths (e.g. [\"src/api\", \"src/auth\"])"), mcp.WithStringItems()),
				mcp.WithNumber("max_file_bytes", mcp.Description("Maximum bytes allowed per parsed source file; overrides server default when set")),
				mcp.WithNumber("max_total_parsed_bytes", mcp.Description("Maximum total bytes allowed across parsed source files; overrides server default when set")),
			)...),
			Handler: h.parseProject,
		},
		{
			Tool: mcp.NewTool("build_or_update_graph", withNamespaceParam(
				mcp.WithDescription("Build or incrementally update the code graph with recursive directory traversal and optional postprocessing"),
				mcp.WithString("path", mcp.Description("Project directory path to parse"), mcp.Required()),
				mcp.WithBoolean("full_rebuild", mcp.Description("If true, do a full rebuild; if false, use incremental sync")),
				mcp.WithString("postprocess", mcp.Description("Postprocessing mode: full, minimal, or none (default: full)")),
				mcp.WithString("postprocess_policy", mcp.Description("Postprocessing failure policy: degraded or fail_closed (default: degraded)")),
				mcp.WithArray("include_paths", mcp.Description("Only include specific sub-paths (e.g. [\"src/api\", \"src/auth\"])"), mcp.WithStringItems()),
				mcp.WithBoolean("replace", mcp.Description("When true (default), incremental include_paths replaces prior namespace graph state outside the included scope; when false, preserves out-of-scope files")),
				mcp.WithNumber("max_file_bytes", mcp.Description("Maximum bytes allowed per parsed source file; overrides server default when set")),
				mcp.WithNumber("max_total_parsed_bytes", mcp.Description("Maximum total bytes allowed across parsed source files; overrides server default when set")),
			)...),
			Handler: h.buildOrUpdateGraph,
		},
		{
			Tool: mcp.NewTool("run_postprocess", withNamespaceParam(
				mcp.WithDescription("Run postprocessing steps independently: communities and/or full-text search indexing, while reporting stored flow rebuild as skipped"),
				mcp.WithBoolean("flows", mcp.Description("Report persisted flow rebuild as skipped; trace_flow still works per entry point (default: true)")),
				mcp.WithBoolean("communities", mcp.Description("Rebuild community detection (default: true)")),
				mcp.WithBoolean("fts", mcp.Description("Rebuild full-text search index (default: true)")),
				mcp.WithNumber("community_depth", mcp.Description("Directory depth for community detection (default: 2)")),
			)...),
			Handler: h.runPostprocess,
		},
	}
}
