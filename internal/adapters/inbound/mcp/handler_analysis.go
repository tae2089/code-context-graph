// @index MCP handlers for impact, flow, change-risk, dead-code, and suspect-edge analyses over the stored graph.
package mcp

import (
	"context"
	"fmt"
	"slices"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/tae2089/trace"

	flowspkg "github.com/tae2089/code-context-graph/internal/app/analyze/flow"
	impactpkg "github.com/tae2089/code-context-graph/internal/app/analyze/impact"
	"github.com/tae2089/code-context-graph/internal/obs"
	"github.com/tae2089/code-context-graph/internal/safepath"
)

const (
	defaultImpactMaxDepth = 3
	defaultImpactMaxNodes = 200
	defaultTraceMaxNodes  = 200
)

// impactRadiusMetadata records bounded traversal settings and truncation state.
// @intent explain how getImpactRadius constrained the blast-radius traversal for the returned payload.
type impactRadiusMetadata struct {
	Truncated     bool `json:"truncated"`
	MaxDepth      int  `json:"max_depth"`
	MaxNodes      int  `json:"max_nodes"`
	ReturnedNodes int  `json:"returned_nodes"`
}

// impactRadiusResponse is the typed wire payload for getImpactRadius.
// @intent preserve a stable typed response envelope for impact-radius queries.
type impactRadiusResponse struct {
	Nodes    []nodeSummary        `json:"nodes"`
	Metadata impactRadiusMetadata `json:"metadata"`
}

// traceFlowMember captures one ordered member inside a traced flow.
// @intent serialize flow member references without exposing the full node record.
type traceFlowMember struct {
	NodeID  uint `json:"node_id"`
	Ordinal int  `json:"ordinal"`
}

// traceFlowMetadata records bounded trace settings and fallback-edge summary data.
// @intent explain whether traceFlow truncated members and whether fallback edges contributed to the result.
type traceFlowMetadata struct {
	Truncated             bool `json:"truncated"`
	MaxNodes              int  `json:"max_nodes"`
	ReturnedNodes         int  `json:"returned_nodes"`
	ContainsFallbackCalls bool `json:"contains_fallback_calls"`
	FallbackEdgesCount    int  `json:"fallback_edges_count"`
}

// traceFlowResponse is the typed wire payload for traceFlow.
// @intent preserve a stable response envelope for traced flow results and their evidence.
type traceFlowResponse struct {
	Name     string                 `json:"name"`
	Members  []traceFlowMember      `json:"members"`
	Metadata traceFlowMetadata      `json:"metadata"`
	Evidence namespaceEvidenceBlock `json:"evidence"`
}

// detectChangesEntry summarizes one changed node plus its diff-derived risk score.
// @intent preserve a stable per-item DTO for detectChanges pagination results.
type detectChangesEntry struct {
	Name      string  `json:"name"`
	File      string  `json:"file"`
	HunkCount int     `json:"hunk_count"`
	RiskScore float64 `json:"risk_score"`
}

// detectChangesResponse is the typed wire payload for detectChanges.
// @intent expose diff-risk results with both legacy entries and shared pagination fields.
type detectChangesResponse struct {
	Base       string               `json:"base"`
	Entries    []detectChangesEntry `json:"entries"`
	Items      []detectChangesEntry `json:"items"`
	Pagination pagination           `json:"pagination"`
}

// affectedFlowEntry summarizes one stored flow touched by changed nodes.
// @intent preserve a stable DTO for getAffectedFlows items while retaining changed node identifiers.
type affectedFlowEntry struct {
	ID            uint   `json:"id"`
	Name          string `json:"name"`
	AffectedNodes []uint `json:"affected_nodes"`
}

// affectedFlowsResponse is the typed wire payload for getAffectedFlows.
// @intent expose affected stored flows with backward-compatible aliases and pagination metadata.
type affectedFlowsResponse struct {
	Base          string              `json:"base"`
	AffectedFlows []affectedFlowEntry `json:"affected_flows"`
	Items         []affectedFlowEntry `json:"items"`
	Count         int                 `json:"count"`
	Pagination    pagination          `json:"pagination"`
}

// getImpactRadius returns nodes reachable within a bounded dependency radius.
// @intent explore the blast radius of a node change so reviewers can prioritize follow-up checks.
// @param request uses qualified_name and depth to define the starting node and traversal depth.
// @requires ImpactAnalyzer must be configured and the target node must exist.
// @ensures returns the impacted node set when analysis succeeds.
// @see mcp.handlers.getNode
func (h *handlers) getImpactRadius(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ctx = h.applyNamespace(ctx, request)
	log := h.logger()

	qn, err := request.RequireString("qualified_name")
	if err != nil {
		return missingParamResult(err)
	}
	depth := request.GetInt("depth", 1)
	maxDepth := request.GetInt("max_depth", defaultImpactMaxDepth)
	maxNodes := request.GetInt("max_nodes", defaultImpactMaxNodes)

	log.InfoContext(ctx, "get_impact_radius called", append(obs.TraceLogArgs(ctx), "qualified_name", qn, "depth", depth)...)

	if h.deps.Analysis.Impact == nil {
		return mcp.NewToolResultError("ImpactAnalyzer not configured"), nil
	}

	return finalizeToolResult(h.cachedExecute(ctx, "get_impact_radius:", map[string]any{"qualified_name": qn, "depth": depth, "max_depth": maxDepth, "max_nodes": maxNodes, "namespace": requestNamespace(request)}, func() (string, error) {
		node, err := h.deps.Graph.Store.GetNode(ctx, qn)
		if err != nil {
			log.ErrorContext(ctx, "store error", append(obs.TraceLogArgs(ctx), "tool", "get_impact_radius", trace.SlogError(err))...)
			return "", trace.Wrap(err, "store error")
		}
		if node == nil {
			log.WarnContext(ctx, "node not found", append(obs.TraceLogArgs(ctx), "qualified_name", qn)...)
			return "", nodeNotFoundErr(qn)
		}

		res, err := h.deps.Analysis.Impact.ImpactRadiusBounded(ctx, node.ID, depth, impactpkg.RadiusOptions{MaxDepth: maxDepth, MaxNodes: maxNodes})
		if err != nil {
			log.ErrorContext(ctx, "impact analysis error", append(obs.TraceLogArgs(ctx), "node_id", node.ID, trace.SlogError(err))...)
			return "", trace.Wrap(err, "impact analysis error")
		}
		nodes := res.Nodes
		truncated := res.Truncated

		log.InfoContext(ctx, "get_impact_radius completed", append(obs.TraceLogArgs(ctx), "qualified_name", qn, "result_count", len(nodes))...)

		impactResult := make([]nodeSummary, len(nodes))
		for i, n := range nodes {
			impactResult[i] = nodeToSummary(n)
		}
		result, err := marshalJSON(impactRadiusResponse{
			Nodes: impactResult,
			Metadata: impactRadiusMetadata{
				Truncated:     truncated,
				MaxDepth:      maxDepth,
				MaxNodes:      maxNodes,
				ReturnedNodes: len(impactResult),
			},
		})
		if err != nil {
			return "", trace.Wrap(err, "marshal result")
		}
		return result, nil
	}))
}

// traceFlow traces the stored call flow that starts from a node.
// @intent reconstruct the call flow containing the starting node so operators can understand execution context.
// @param request uses qualified_name to identify the flow anchor node.
// @requires FlowTracer must be configured and the target node must exist.
// @ensures returns the flow name and ordered members when tracing succeeds.
// @see mcp.handlers.listFlows
func (h *handlers) traceFlow(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ctx = h.applyNamespace(ctx, request)
	log := h.logger()

	qn, err := request.RequireString("qualified_name")
	if err != nil {
		return missingParamResult(err)
	}

	log.InfoContext(ctx, "trace_flow called", append(obs.TraceLogArgs(ctx), "qualified_name", qn)...)

	if h.deps.Analysis.Flow == nil {
		return mcp.NewToolResultError("FlowTracer not configured"), nil
	}

	maxNodes := request.GetInt("max_nodes", defaultTraceMaxNodes)
	includeFallbackCalls := request.GetBool("include_fallback_calls", true)
	return finalizeToolResult(h.cachedExecute(ctx, "trace_flow:", map[string]any{
		"qualified_name":         qn,
		"max_nodes":              maxNodes,
		"include_fallback_calls": includeFallbackCalls,
		"namespace":              requestNamespace(request),
	}, func() (string, error) {
		node, err := h.deps.Graph.Store.GetNode(ctx, qn)
		if err != nil {
			log.ErrorContext(ctx, "store error", append(obs.TraceLogArgs(ctx), "tool", "trace_flow", trace.SlogError(err))...)
			return "", trace.Wrap(err, "store error")
		}
		if node == nil {
			log.WarnContext(ctx, "node not found", append(obs.TraceLogArgs(ctx), "qualified_name", qn)...)
			return "", nodeNotFoundErr(qn)
		}

		res, err := h.deps.Analysis.Flow.TraceFlowBounded(ctx, node.ID, flowspkg.TraceOptions{MaxNodes: maxNodes, IncludeFallbackCalls: &includeFallbackCalls})
		if err != nil {
			log.ErrorContext(ctx, "trace error", append(obs.TraceLogArgs(ctx), "node_id", node.ID, trace.SlogError(err))...)
			return "", trace.Wrap(err, "trace error")
		}
		flow := res.Flow
		truncated := res.Truncated
		containsFallbackCalls := res.ContainsFallbackCalls
		fallbackEdgesCount := res.FallbackEdgesCount

		log.InfoContext(ctx, "trace_flow completed", append(obs.TraceLogArgs(ctx), "qualified_name", qn, "members", len(flow.Members))...)

		members := make([]traceFlowMember, len(flow.Members))
		for i, m := range flow.Members {
			members[i] = traceFlowMember{NodeID: m.NodeID, Ordinal: m.Ordinal}
		}

		result, err := marshalJSON(traceFlowResponse{
			Name:    flow.Name,
			Members: members,
			Metadata: traceFlowMetadata{
				Truncated:             truncated,
				MaxNodes:              maxNodes,
				ReturnedNodes:         len(members),
				ContainsFallbackCalls: containsFallbackCalls,
				FallbackEdgesCount:    fallbackEdgesCount,
			},
			Evidence: h.namespaceEvidenceFromContext(ctx),
		})
		if err != nil {
			return "", trace.Wrap(err, "marshal result")
		}
		return result, nil
	}))
}

// detectChanges analyzes git diff hunks and returns node-level risk scores.
// @intent identify changed files and functions with elevated review risk from recent git diff hunks.
// @param request uses repo_root as the Git repository root, base as the comparison commit, plus optional limit/offset.
// @requires ChangesGitClient must be configured.
// @ensures returns per-node hunk counts and risk scores plus pagination metadata when analysis succeeds.
// @sideEffect reads git diff data from the configured repository root.
// @see mcp.handlers.getAffectedFlows
func (h *handlers) detectChanges(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ctx = h.applyNamespace(ctx, request)
	log := h.logger()

	repoRoot, err := request.RequireString("repo_root")
	if err != nil {
		return missingParamResult(err)
	}
	base := request.GetString("base", "HEAD~1")

	limit := request.GetInt("limit", 50)
	offset := request.GetInt("offset", 0)
	if err := validatePositiveLimit(limit); err != nil {
		return finalizeToolResult("", err)
	}
	if err := validateOffset(offset); err != nil {
		return finalizeToolResult("", err)
	}
	if limit > maxPaginationLimit {
		return finalizeToolResult("", newToolResultErr(fmt.Sprintf("limit must be <= %d, got %d", maxPaginationLimit, limit)))
	}

	validatedRepoRoot, err := h.validateRepoRoot(repoRoot)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	repoRoot = validatedRepoRoot

	log.Info("detect_changes called", "repo_root", repoRoot, "base", base, "limit", limit, "offset", offset)

	if h.deps.Analysis.Changes == nil {
		return mcp.NewToolResultError("ChangesGitClient not configured"), nil
	}

	return finalizeToolResult(h.cachedExecute(ctx, "detect_changes:", map[string]any{"repo_root": repoRoot, "base": base, "limit": limit, "offset": offset, "namespace": requestNamespace(request)}, func() (string, error) {
		page, err := h.deps.Analysis.Changes.AnalyzePage(ctx, repoRoot, base, limit, offset)
		if err != nil {
			return "", trace.Wrap(err, "changes analyze error")
		}

		entries := make([]detectChangesEntry, len(page.Items))
		for i, r := range page.Items {
			entries[i] = detectChangesEntry{
				Name:      r.Node.QualifiedName,
				File:      r.Node.FilePath,
				HunkCount: r.HunkCount,
				RiskScore: r.RiskScore,
			}
		}

		metadata := pagination{Limit: limit, Offset: offset, Returned: len(entries), HasMore: page.HasMore}
		if page.HasMore {
			nextOffset := offset + limit
			metadata.NextOffset = &nextOffset
		}
		resp := detectChangesResponse{Base: base, Entries: entries, Items: entries, Pagination: metadata}
		result, err := marshalJSON(resp)
		if err != nil {
			return "", trace.Wrap(err, "marshal result")
		}
		return result, nil
	}))
}

// getAffectedFlows finds stored flows touched by recent code changes.
// @intent trace flows touched by changed nodes so regression review can happen at the flow level.
// @param request uses repo_root and base to define the diff range, plus optional limit/offset for pagination.
// @requires ChangesGitClient must be configured.
// @ensures returns affected flows with their changed node IDs plus pagination metadata when analysis succeeds.
// @domainRule changed node collection runs once and does not page through detectChanges risk entries.
// @sideEffect reads git diff data from the configured repository root.
// @see mcp.handlers.detectChanges
func (h *handlers) getAffectedFlows(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ctx = h.applyNamespace(ctx, request)
	log := h.logger()

	repoRoot, err := request.RequireString("repo_root")
	if err != nil {
		return missingParamResult(err)
	}
	base := request.GetString("base", "HEAD~1")

	limit := request.GetInt("limit", 50)
	offset := request.GetInt("offset", 0)
	if err := validatePositiveLimit(limit); err != nil {
		return finalizeToolResult("", err)
	}
	if err := validateOffset(offset); err != nil {
		return finalizeToolResult("", err)
	}
	if limit > maxPaginationLimit {
		return finalizeToolResult("", newToolResultErr(fmt.Sprintf("limit must be <= %d, got %d", maxPaginationLimit, limit)))
	}

	validatedRepoRoot, err := h.validateRepoRoot(repoRoot)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	repoRoot = validatedRepoRoot

	log.Info("get_affected_flows called", "repo_root", repoRoot, "base", base, "limit", limit, "offset", offset)

	if h.deps.Analysis.Changes == nil || h.deps.Analysis.Reader == nil {
		return mcp.NewToolResultError("ChangesGitClient not configured"), nil
	}

	return finalizeToolResult(h.cachedExecute(ctx, "get_affected_flows:", map[string]any{"repo_root": repoRoot, "base": base, "limit": limit, "offset": offset, "namespace": requestNamespace(request)}, func() (string, error) {
		changedNodeIDs, err := h.deps.Analysis.Changes.ChangedNodeIDs(ctx, repoRoot, base)
		if err != nil {
			return "", trace.Wrap(err, "changes analyze error")
		}

		emptyResp := func() (string, error) {
			page := pagination{Limit: limit, Offset: offset, Returned: 0, HasMore: false}
			result, err := marshalJSON(affectedFlowsResponse{
				Base:          base,
				AffectedFlows: []affectedFlowEntry{},
				Items:         []affectedFlowEntry{},
				Count:         0,
				Pagination:    page,
			})
			if err != nil {
				return "", trace.Wrap(err, "marshal result")
			}
			return result, nil
		}

		if len(changedNodeIDs) == 0 {
			return emptyResp()
		}

		flowList, hasMore, err := h.deps.Analysis.Reader.AffectedFlowsPage(ctx, changedNodeIDs, limit, offset)
		if err != nil {
			return "", trace.Wrap(err, "find affected flows")
		}
		if len(flowList) == 0 {
			return emptyResp()
		}
		affected := make([]affectedFlowEntry, len(flowList))
		for i, f := range flowList {
			affected[i] = affectedFlowEntry{ID: f.ID, Name: f.Name, AffectedNodes: f.AffectedNodes}
		}

		page := pagination{Limit: limit, Offset: offset, Returned: len(affected), HasMore: hasMore}
		if hasMore {
			nextOffset := offset + limit
			page.NextOffset = &nextOffset
		}
		result, err := marshalJSON(affectedFlowsResponse{
			Base:          base,
			AffectedFlows: affected,
			Items:         affected,
			Count:         len(affected),
			Pagination:    page,
		})
		if err != nil {
			return "", trace.Wrap(err, "marshal result")
		}
		return result, nil
	}))
}

// @intent validate repo_root inputs against configured analysis roots before git-based analysis reads the filesystem.
func (h *handlers) validateRepoRoot(repoRoot string) (string, error) {
	return validateRepoRootWithin(repoRoot, h.deps.Runtime.RepoRoot, h.namespaceRoot())
}

// validateRepoRootWithin checks that repoRoot resolves to a canonical path within one of the allowed analysis roots.
// @intent prevent git-based analysis from reading paths outside the configured project boundaries.
// @requires configuredRepoRoot or namespaceRoot must be non-empty; repoRoot must be a valid filesystem path.
// @ensures returned path is absolute, symlink-resolved, and contained within an allowed root.
func validateRepoRootWithin(repoRoot, configuredRepoRoot, namespaceRoot string) (string, error) {
	if repoRoot == "" {
		return "", fmt.Errorf("repo_root is required")
	}
	allowedRoots := configuredAnalysisRoots(configuredRepoRoot, namespaceRoot)
	if len(allowedRoots) == 0 {
		return "", fmt.Errorf("analysis repo root is not configured")
	}
	repo, err := safepath.Canonical(repoRoot, false)
	if err != nil {
		return "", fmt.Errorf("invalid repo_root: %w", err)
	}
	allowed, err := validatePathWithinAllowedRoots(repo, allowedRoots)
	if err != nil {
		return "", fmt.Errorf("invalid configured repo root: %w", err)
	}
	if !allowed {
		return "", fmt.Errorf("repo_root %q is outside configured analysis root", repoRoot)
	}
	return repo, nil
}

// configuredAnalysisRoots returns the deduplicated list of allowed root paths derived from server config and namespace storage.
// @intent build the allowlist used by path validation so each source of truth contributes exactly once.
func configuredAnalysisRoots(repoRoot, namespaceRoot string) []string {
	roots := make([]string, 0, 2)
	for _, root := range []string{repoRoot, namespaceRoot} {
		if root == "" {
			continue
		}
		if !sliceContainsString(roots, root) {
			roots = append(roots, root)
		}
	}
	return roots
}

// @intent linear membership check for small string slices used by allowlist evaluation.
func sliceContainsString(values []string, target string) bool {
	return slices.Contains(values, target)
}

// validatePathWithinAllowedRoots reports whether target falls inside any of the canonical allowed roots.
// @intent enforce that user-supplied paths cannot escape the configured analysis boundary.
// @requires allowedRoots must be non-empty and each entry must be a valid filesystem path.
func validatePathWithinAllowedRoots(target string, allowedRoots []string) (bool, error) {
	for _, root := range allowedRoots {
		base, err := safepath.Canonical(root, false)
		if err != nil {
			return false, err
		}
		within, err := safepath.IsWithinRoot(base, target)
		if err != nil {
			return false, err
		}
		if within {
			return true, nil
		}
	}
	return false, nil
}
