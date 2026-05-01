package mcp

import (
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func contextTools(h *handlers) []server.ServerTool {
	return []server.ServerTool{
		{
			Tool: mcp.NewTool("get_minimal_context",
				mcp.WithDescription("Get ultra-compact context for any task (~100 tokens). Always call this first."),
				mcp.WithString("task", mcp.Description("Natural language task description for tool suggestions")),
				mcp.WithString("repo_root", mcp.Description("Git repository root path for change analysis")),
				mcp.WithString("base", mcp.Description("Base commit reference (default: HEAD~1)")),
				mcp.WithString("workspace", mcp.Description("Workspace name for namespace isolation")),
			),
			Handler: h.getMinimalContext,
		},
	}
}
