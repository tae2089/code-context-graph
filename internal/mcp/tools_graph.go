package mcp

import (
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func graphTools(h *handlers) []server.ServerTool {
	return []server.ServerTool{
		{
			Tool: mcp.NewTool("list_flows",
				mcp.WithDescription("List all stored flows with member counts"),
				mcp.WithString("sort_by", mcp.Description("Sort order: name or node_count (default: name)")),
				mcp.WithNumber("limit", mcp.Description("Maximum number of results (default: 50)")),
				mcp.WithString("workspace", mcp.Description("Workspace name for namespace isolation")),
			),
			Handler: h.listFlows,
		},
		{
			Tool: mcp.NewTool("list_communities",
				mcp.WithDescription("List communities with node counts and optional filtering"),
				mcp.WithString("sort_by", mcp.Description("Sort order: size, name, or cohesion (default: size)")),
				mcp.WithNumber("min_size", mcp.Description("Minimum node count filter (default: 0)")),
				mcp.WithString("workspace", mcp.Description("Workspace name for namespace isolation")),
			),
			Handler: h.listCommunities,
		},
		{
			Tool: mcp.NewTool("get_community",
				mcp.WithDescription("Get community details with optional member listing and coverage"),
				mcp.WithNumber("community_id", mcp.Description("Community ID"), mcp.Required()),
				mcp.WithBoolean("include_members", mcp.Description("Include member nodes in response (default: false)")),
				mcp.WithString("workspace", mcp.Description("Workspace name for namespace isolation")),
			),
			Handler: h.getCommunity,
		},
		{
			Tool: mcp.NewTool("get_architecture_overview",
				mcp.WithDescription("Get architecture overview: communities, coupling analysis, and warnings"),
				mcp.WithString("workspace", mcp.Description("Workspace name for namespace isolation")),
			),
			Handler: h.getArchitectureOverview,
		},
	}
}
