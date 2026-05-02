package mcp

import (
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func graphTools(h *handlers) []server.ServerTool {
	return []server.ServerTool{
		{
			Tool: mcp.NewTool("list_flows", withNamespaceParam(
				mcp.WithDescription("List all stored flows with member counts"),
				mcp.WithString("sort_by", mcp.Description("Sort order: name or node_count (default: name)")),
				mcp.WithNumber("limit", mcp.Description("Maximum number of results (default: 50)")),
			)...),
			Handler: h.listFlows,
		},
		{
			Tool: mcp.NewTool("list_communities", withNamespaceParam(
				mcp.WithDescription("List communities with node counts and optional filtering"),
				mcp.WithString("sort_by", mcp.Description("Sort order: size, name, or cohesion (default: size)")),
				mcp.WithNumber("min_size", mcp.Description("Minimum node count filter (default: 0)")),
			)...),
			Handler: h.listCommunities,
		},
		{
			Tool: mcp.NewTool("get_community", withNamespaceParam(
				mcp.WithDescription("Get community details with optional member listing and coverage"),
				mcp.WithNumber("community_id", mcp.Description("Community ID"), mcp.Required()),
				mcp.WithBoolean("include_members", mcp.Description("Include member nodes in response (default: false)")),
			)...),
			Handler: h.getCommunity,
		},
		{
			Tool: mcp.NewTool("get_architecture_overview", withNamespaceParam(
				mcp.WithDescription("Get architecture overview: communities, coupling analysis, and warnings"),
			)...),
			Handler: h.getArchitectureOverview,
		},
	}
}
