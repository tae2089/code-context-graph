// @index MCP tool registration for graph summaries and architecture views.
package mcp

import (
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// graphTools registers tools that summarize stored flows, communities, and architecture state.
// @intent expose high-level graph inspection separately from low-level query primitives.
func graphTools(h *handlers) []server.ServerTool {
	return []server.ServerTool{
		{
			Tool: mcp.NewTool("list_flows", withNamespaceParam(
				mcp.WithDescription("List all stored flows with member counts"),
				mcp.WithString("sort_by", mcp.Description("Sort order: name or node_count (default: name)")),
				mcp.WithNumber("limit", mcp.Description("Maximum number of results (default: 50)")),
				mcp.WithNumber("offset", mcp.Description("Zero-based offset for pagination (default: 0)")),
			)...),
			Handler: h.listFlows,
		},
	}
}
