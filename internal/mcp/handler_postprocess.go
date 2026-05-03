package mcp

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"

	postprocesspolicy "github.com/tae2089/code-context-graph/internal/postprocess/policy"
)

func (h *handlers) getPostprocessPolicy(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ctx = h.applyWorkspace(ctx, request)
	if h.deps.PostprocessPolicy == nil {
		return mcp.NewToolResultError("postprocess policy engine not configured"), nil
	}
	tool := request.GetString("tool", "")
	if tool != "" && !postprocesspolicy.ValidTool(tool) {
		return mcp.NewToolResultError("tool must be build_or_update_graph or run_postprocess"), nil
	}
	recentLimit := request.GetInt("recent_limit", postprocesspolicy.DefaultStatusLimit)
	if err := validatePositiveLimit(recentLimit); err != nil {
		return finalizeToolResult("", err)
	}
	summary, err := h.deps.PostprocessPolicy.Status(ctx, postprocesspolicy.StatusOptions{
		Namespace: requestNamespace(request),
		Tool:      tool,
		RecentLimit: recentLimit,
	})
	if err != nil {
		return nil, err
	}
	result, err := marshalJSON(summary)
	return finalizeToolResult(result, err)
}

func (h *handlers) resetPostprocessPolicy(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ctx = h.applyWorkspace(ctx, request)
	if h.deps.PostprocessPolicy == nil {
		return mcp.NewToolResultError("postprocess policy engine not configured"), nil
	}
	tool := request.GetString("tool", "")
	if !postprocesspolicy.ValidTool(tool) {
		return mcp.NewToolResultError("tool must be build_or_update_graph or run_postprocess"), nil
	}
	if err := h.deps.PostprocessPolicy.Reset(ctx, tool); err != nil {
		return nil, err
	}
	result, err := marshalJSON(map[string]any{
		"status": "ok",
		"tool":   tool,
		"reset":  true,
	})
	return finalizeToolResult(result, err)
}
