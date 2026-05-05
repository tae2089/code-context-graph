// @index MCP handlers for inspecting and resetting automatic postprocess policy state.
package mcp

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"

	postprocesspolicy "github.com/tae2089/code-context-graph/internal/postprocess/policy"
)

// getPostprocessPolicy returns the recorded postprocess policy summary for a namespace and tool.
// @intent expose automatic fail-open versus fail-closed decisions so operators can diagnose degraded postprocess behavior.
// @param request optional tool filter (build_or_update_graph or run_postprocess) and recent_limit for failure history depth.
// @return JSON-encoded StatusSummary with current decision, fail-closed entries, and recent failures.
// @requires deps.PostprocessPolicy is configured.
// @see mcp.handlers.resetPostprocessPolicy
// @see policy.StatusSummary
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

// resetPostprocessPolicy clears the stored failure streak for a postprocess tool.
// @intent let operators recover from fail-closed state after they have fixed the underlying issue.
// @param request requires tool name (build_or_update_graph or run_postprocess) to reset.
// @requires deps.PostprocessPolicy is configured and tool name is valid.
// @ensures next policy resolve treats the tool as if no recent failures occurred.
// @sideEffect writes a reset marker run record into the postprocess policy store.
// @mutates postprocess policy persisted state for the targeted tool.
// @see mcp.handlers.getPostprocessPolicy
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
