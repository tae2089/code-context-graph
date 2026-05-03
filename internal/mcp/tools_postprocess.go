// @index MCP tool registration for automatic postprocess policy control.
package mcp

import (
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// postprocessTools registers policy inspection and reset tools for postprocess automation.
// @intent make postprocess recovery controls available without mixing them into unrelated tool groups.
func postprocessTools(h *handlers) []server.ServerTool {
	return []server.ServerTool{
		{
			Tool: mcp.NewTool("get_postprocess_policy", withNamespaceParam(
				mcp.WithDescription("Inspect automatic postprocess policy state, fail_closed entries, and recent failures for the current namespace or across namespaces"),
				mcp.WithString("tool", mcp.Description("Optional tool filter: build_or_update_graph or run_postprocess")),
				mcp.WithNumber("recent_limit", mcp.Description("Maximum recent failures returned (default: 5)"), mcp.DefaultNumber(5)),
			)...),
			Handler: h.getPostprocessPolicy,
		},
		{
			Tool: mcp.NewTool("reset_postprocess_policy", withNamespaceParam(
				mcp.WithDescription("Reset automatic postprocess failure streak for a tool by recording a reset marker run in the current namespace"),
				mcp.WithString("tool", mcp.Description("Tool to reset: build_or_update_graph or run_postprocess"), mcp.Required()),
			)...),
			Handler: h.resetPostprocessPolicy,
		},
	}
}
