package mcp

import (
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func parseTools(h *handlers) []server.ServerTool {
	return []server.ServerTool{
		{
			Tool: mcp.NewTool("parse_project",
				mcp.WithDescription("Parse source files and store nodes/edges in the graph database"),
				mcp.WithString("path", mcp.Description("Project directory path to parse"), mcp.Required()),
				mcp.WithString("workspace", mcp.Description("Workspace name for namespace isolation")),
				mcp.WithArray("include_paths", mcp.Description("Only include specific sub-paths (e.g. [\"src/api\", \"src/auth\"])"), mcp.WithStringItems()),
			),
			Handler: h.parseProject,
		},
		{
			Tool: mcp.NewTool("build_or_update_graph",
				mcp.WithDescription("Build or incrementally update the code graph with recursive directory traversal and optional postprocessing"),
				mcp.WithString("path", mcp.Description("Project directory path to parse"), mcp.Required()),
				mcp.WithBoolean("full_rebuild", mcp.Description("If true, do a full rebuild; if false, use incremental sync")),
				mcp.WithString("postprocess", mcp.Description("Postprocessing mode: full, minimal, or none (default: full)")),
				mcp.WithString("workspace", mcp.Description("Workspace name for namespace isolation")),
				mcp.WithArray("include_paths", mcp.Description("Only include specific sub-paths (e.g. [\"src/api\", \"src/auth\"])"), mcp.WithStringItems()),
				mcp.WithBoolean("replace", mcp.Description("When true (default), incremental include_paths replaces prior namespace graph state outside the included scope; when false, preserves out-of-scope files")),
			),
			Handler: h.buildOrUpdateGraph,
		},
		{
			Tool: mcp.NewTool("run_postprocess",
				mcp.WithDescription("Run postprocessing steps independently: flows, communities, and/or full-text search indexing"),
				mcp.WithBoolean("flows", mcp.Description("Rebuild flow traces (default: true)")),
				mcp.WithBoolean("communities", mcp.Description("Rebuild community detection (default: true)")),
				mcp.WithBoolean("fts", mcp.Description("Rebuild full-text search index (default: true)")),
				mcp.WithNumber("community_depth", mcp.Description("Directory depth for community detection (default: 2)")),
				mcp.WithString("workspace", mcp.Description("Workspace name for namespace isolation")),
			),
			Handler: h.runPostprocess,
		},
	}
}
