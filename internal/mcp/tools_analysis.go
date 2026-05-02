package mcp

import (
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func analysisTools(h *handlers) []server.ServerTool {
	return []server.ServerTool{
		{
			Tool: mcp.NewTool("get_impact_radius", withNamespaceParam(
				mcp.WithDescription("Get blast-radius analysis for a node via BFS traversal"),
				mcp.WithString("qualified_name", mcp.Description("Fully qualified node name"), mcp.Required()),
				mcp.WithNumber("depth", mcp.Description("BFS traversal depth"), mcp.DefaultNumber(1)),
				mcp.WithNumber("max_depth", mcp.Description("Maximum BFS depth returned"), mcp.DefaultNumber(defaultImpactMaxDepth)),
				mcp.WithNumber("max_nodes", mcp.Description("Maximum nodes returned"), mcp.DefaultNumber(defaultImpactMaxNodes)),
			)...),
			Handler: h.getImpactRadius,
		},
		{
			Tool: mcp.NewTool("trace_flow", withNamespaceParam(
				mcp.WithDescription("Trace call-chain flow starting from a node"),
				mcp.WithString("qualified_name", mcp.Description("Fully qualified node name"), mcp.Required()),
				mcp.WithNumber("max_nodes", mcp.Description("Maximum flow members returned"), mcp.DefaultNumber(defaultTraceMaxNodes)),
			)...),
			Handler: h.traceFlow,
		},
		{
			Tool: mcp.NewTool("detect_changes", withNamespaceParam(
				mcp.WithDescription("Detect changed functions with risk scores based on git diff"),
				mcp.WithString("repo_root", mcp.Description("Git repository root path"), mcp.Required()),
				mcp.WithString("base", mcp.Description("Base commit reference (default: HEAD~1)")),
			)...),
			Handler: h.detectChanges,
		},
		{
			Tool: mcp.NewTool("get_affected_flows", withNamespaceParam(
				mcp.WithDescription("Get flows affected by recent code changes"),
				mcp.WithString("repo_root", mcp.Description("Git repository root path"), mcp.Required()),
				mcp.WithString("base", mcp.Description("Base commit reference (default: HEAD~1)")),
			)...),
			Handler: h.getAffectedFlows,
		},
		{
			Tool: mcp.NewTool("find_dead_code", withNamespaceParam(
				mcp.WithDescription("Find unused code with no incoming edges"),
				mcp.WithString("path", mcp.Description("Filter results to file paths starting with this prefix")),
			)...),
			Handler: h.findDeadCode,
		},
	}
}
