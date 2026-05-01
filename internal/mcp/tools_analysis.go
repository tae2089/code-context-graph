package mcp

import (
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func analysisTools(h *handlers) []server.ServerTool {
	return []server.ServerTool{
		{
			Tool: mcp.NewTool("get_impact_radius",
				mcp.WithDescription("Get blast-radius analysis for a node via BFS traversal"),
				mcp.WithString("qualified_name", mcp.Description("Fully qualified node name"), mcp.Required()),
				mcp.WithNumber("depth", mcp.Description("BFS traversal depth"), mcp.DefaultNumber(1)),
				mcp.WithString("workspace", mcp.Description("Workspace name for namespace isolation")),
			),
			Handler: h.getImpactRadius,
		},
		{
			Tool: mcp.NewTool("trace_flow",
				mcp.WithDescription("Trace call-chain flow starting from a node"),
				mcp.WithString("qualified_name", mcp.Description("Fully qualified node name"), mcp.Required()),
				mcp.WithString("workspace", mcp.Description("Workspace name for namespace isolation")),
			),
			Handler: h.traceFlow,
		},
		{
			Tool: mcp.NewTool("detect_changes",
				mcp.WithDescription("Detect changed functions with risk scores based on git diff"),
				mcp.WithString("repo_root", mcp.Description("Git repository root path"), mcp.Required()),
				mcp.WithString("base", mcp.Description("Base commit reference (default: HEAD~1)")),
				mcp.WithString("workspace", mcp.Description("Workspace name for namespace isolation")),
			),
			Handler: h.detectChanges,
		},
		{
			Tool: mcp.NewTool("get_affected_flows",
				mcp.WithDescription("Get flows affected by recent code changes"),
				mcp.WithString("repo_root", mcp.Description("Git repository root path"), mcp.Required()),
				mcp.WithString("base", mcp.Description("Base commit reference (default: HEAD~1)")),
				mcp.WithString("workspace", mcp.Description("Workspace name for namespace isolation")),
			),
			Handler: h.getAffectedFlows,
		},
		{
			Tool: mcp.NewTool("find_dead_code",
				mcp.WithDescription("Find unused code with no incoming edges"),
				mcp.WithString("path", mcp.Description("Filter results to file paths starting with this prefix")),
				mcp.WithString("workspace", mcp.Description("Workspace name for namespace isolation")),
			),
			Handler: h.findDeadCode,
		},
	}
}
