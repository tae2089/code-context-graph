// @index MCP handlers for impact, flow, change-risk, dead-code, and suspect-edge analyses over the stored graph.
package mcp

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/tae2089/trace"

	"github.com/tae2089/code-context-graph/internal/analysis/changes"
	"github.com/tae2089/code-context-graph/internal/analysis/deadcode"
	fallbackanalysis "github.com/tae2089/code-context-graph/internal/analysis/fallback"
	flowspkg "github.com/tae2089/code-context-graph/internal/analysis/flows"
	impactpkg "github.com/tae2089/code-context-graph/internal/analysis/impact"
	"github.com/tae2089/code-context-graph/internal/ctxns"
	"github.com/tae2089/code-context-graph/internal/model"
	"github.com/tae2089/code-context-graph/internal/obs"
)

const (
	defaultImpactMaxDepth = 3
	defaultImpactMaxNodes = 200
	defaultTraceMaxNodes  = 200
)

// getImpactRadius returns nodes reachable within a bounded dependency radius.
// @intent explore the blast radius of a node change so reviewers can prioritize follow-up checks.
// @param request uses qualified_name and depth to define the starting node and traversal depth.
// @requires ImpactAnalyzer must be configured and the target node must exist.
// @ensures returns the impacted node set when analysis succeeds.
// @see mcp.handlers.getNode
func (h *handlers) getImpactRadius(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ctx = h.applyWorkspace(ctx, request)
	log := h.logger()

	qn, err := request.RequireString("qualified_name")
	if err != nil {
		return missingParamResult(err)
	}
	depth := request.GetInt("depth", 1)
	maxDepth := request.GetInt("max_depth", defaultImpactMaxDepth)
	maxNodes := request.GetInt("max_nodes", defaultImpactMaxNodes)

	log.InfoContext(ctx, "get_impact_radius called", append(obs.TraceLogArgs(ctx), "qualified_name", qn, "depth", depth)...)

	if h.deps.ImpactAnalyzer == nil {
		return mcp.NewToolResultError("ImpactAnalyzer not configured"), nil
	}

	return finalizeToolResult(h.cachedExecute(ctx, "get_impact_radius:", map[string]any{"qualified_name": qn, "depth": depth, "max_depth": maxDepth, "max_nodes": maxNodes, "namespace": requestNamespace(request)}, func() (string, error) {
		node, err := h.deps.Store.GetNode(ctx, qn)
		if err != nil {
			log.ErrorContext(ctx, "store error", append(obs.TraceLogArgs(ctx), "tool", "get_impact_radius", trace.SlogError(err))...)
			return "", trace.Wrap(err, "store error")
		}
		if node == nil {
			log.WarnContext(ctx, "node not found", append(obs.TraceLogArgs(ctx), "qualified_name", qn)...)
			return "", nodeNotFoundErr(qn)
		}

		var nodes []model.Node
		truncated := false
		if bounded, ok := h.deps.ImpactAnalyzer.(BoundedImpactAnalyzer); ok {
			res, err := bounded.ImpactRadiusBounded(ctx, node.ID, depth, impactpkg.RadiusOptions{MaxDepth: maxDepth, MaxNodes: maxNodes})
			if err != nil {
				log.ErrorContext(ctx, "impact analysis error", append(obs.TraceLogArgs(ctx), "node_id", node.ID, trace.SlogError(err))...)
				return "", trace.Wrap(err, "impact analysis error")
			}
			nodes = res.Nodes
			truncated = res.Truncated
		} else {
			if maxDepth > 0 && depth > maxDepth {
				depth = maxDepth
				truncated = true
			}
			var err error
			nodes, err = h.deps.ImpactAnalyzer.ImpactRadius(ctx, node.ID, depth)
			if err != nil {
				log.ErrorContext(ctx, "impact analysis error", append(obs.TraceLogArgs(ctx), "node_id", node.ID, trace.SlogError(err))...)
				return "", trace.Wrap(err, "impact analysis error")
			}
			if maxNodes > 0 && len(nodes) > maxNodes {
				nodes = nodes[:maxNodes]
				truncated = true
			}
		}

		log.InfoContext(ctx, "get_impact_radius completed", append(obs.TraceLogArgs(ctx), "qualified_name", qn, "result_count", len(nodes))...)

		impactResult := make([]map[string]any, len(nodes))
		for i, n := range nodes {
			impactResult[i] = nodeToBasicMap(n)
		}
		result, err := marshalJSON(map[string]any{
			"nodes": impactResult,
			"metadata": map[string]any{
				"truncated":      truncated,
				"max_depth":      maxDepth,
				"max_nodes":      maxNodes,
				"returned_nodes": len(impactResult),
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
	ctx = h.applyWorkspace(ctx, request)
	log := h.logger()

	qn, err := request.RequireString("qualified_name")
	if err != nil {
		return missingParamResult(err)
	}

	log.InfoContext(ctx, "trace_flow called", append(obs.TraceLogArgs(ctx), "qualified_name", qn)...)

	if h.deps.FlowTracer == nil {
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
		node, err := h.deps.Store.GetNode(ctx, qn)
		if err != nil {
			log.ErrorContext(ctx, "store error", append(obs.TraceLogArgs(ctx), "tool", "trace_flow", trace.SlogError(err))...)
			return "", trace.Wrap(err, "store error")
		}
		if node == nil {
			log.WarnContext(ctx, "node not found", append(obs.TraceLogArgs(ctx), "qualified_name", qn)...)
			return "", nodeNotFoundErr(qn)
		}

		var flow *model.Flow
		truncated := false
		containsFallbackCalls := false
		fallbackEdgesCount := 0
		if bounded, ok := h.deps.FlowTracer.(BoundedFlowTracer); ok {
			res, err := bounded.TraceFlowBounded(ctx, node.ID, flowspkg.TraceOptions{MaxNodes: maxNodes, IncludeFallbackCalls: &includeFallbackCalls})
			if err != nil {
				log.ErrorContext(ctx, "trace error", append(obs.TraceLogArgs(ctx), "node_id", node.ID, trace.SlogError(err))...)
				return "", trace.Wrap(err, "trace error")
			}
			flow = res.Flow
			truncated = res.Truncated
			containsFallbackCalls = res.ContainsFallbackCalls
			fallbackEdgesCount = res.FallbackEdgesCount
		} else {
			var err error
			flow, err = h.deps.FlowTracer.TraceFlow(ctx, node.ID)
			if err != nil {
				log.ErrorContext(ctx, "trace error", append(obs.TraceLogArgs(ctx), "node_id", node.ID, trace.SlogError(err))...)
				return "", trace.Wrap(err, "trace error")
			}
			if maxNodes > 0 && len(flow.Members) > maxNodes {
				flow.Members = flow.Members[:maxNodes]
				truncated = true
			}
		}

		log.InfoContext(ctx, "trace_flow completed", append(obs.TraceLogArgs(ctx), "qualified_name", qn, "members", len(flow.Members))...)

		members := make([]map[string]any, len(flow.Members))
		for i, m := range flow.Members {
			members[i] = map[string]any{
				"node_id": m.NodeID,
				"ordinal": m.Ordinal,
			}
		}

		data := map[string]any{
			"name":    flow.Name,
			"members": members,
			"metadata": map[string]any{
				"truncated":               truncated,
				"max_nodes":               maxNodes,
				"returned_nodes":          len(members),
				"contains_fallback_calls": containsFallbackCalls,
				"fallback_edges_count":    fallbackEdgesCount,
			},
			"evidence": h.workspaceEvidenceFromContext(ctx),
		}
		result, err := marshalJSON(data)
		if err != nil {
			return "", trace.Wrap(err, "marshal result")
		}
		return result, nil
	}))
}

// detectChanges analyzes git diff hunks and returns node-level risk scores.
// @intent identify changed files and functions with elevated review risk from recent git diff hunks.
// @param request uses repo_root as the Git repository root and base as the comparison commit.
// @requires ChangesGitClient must be configured.
// @ensures returns per-node hunk counts and risk scores when analysis succeeds.
// @sideEffect reads git diff data from the configured repository root.
// @see mcp.handlers.getAffectedFlows
func (h *handlers) detectChanges(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ctx = h.applyWorkspace(ctx, request)
	log := h.logger()

	repoRoot, err := request.RequireString("repo_root")
	if err != nil {
		return missingParamResult(err)
	}
	base := request.GetString("base", "HEAD~1")

	validatedRepoRoot, err := h.validateRepoRoot(repoRoot)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	repoRoot = validatedRepoRoot

	log.Info("detect_changes called", "repo_root", repoRoot, "base", base)

	if h.deps.ChangesGitClient == nil {
		return mcp.NewToolResultError("ChangesGitClient not configured"), nil
	}

	return finalizeToolResult(h.cachedExecute(ctx, "detect_changes:", map[string]any{"repo_root": repoRoot, "base": base, "namespace": requestNamespace(request)}, func() (string, error) {
		chSvc := changes.New(h.deps.DB, h.deps.ChangesGitClient)
		risks, err := chSvc.Analyze(ctx, repoRoot, base)
		if err != nil {
			return "", trace.Wrap(err, "changes analyze error")
		}

		entries := make([]map[string]any, len(risks))
		for i, r := range risks {
			entries[i] = map[string]any{
				"name":       r.Node.QualifiedName,
				"file":       r.Node.FilePath,
				"hunk_count": r.HunkCount,
				"risk_score": r.RiskScore,
			}
		}

		resp := map[string]any{
			"base":    base,
			"entries": entries,
		}
		result, err := marshalJSON(resp)
		if err != nil {
			return "", trace.Wrap(err, "marshal result")
		}
		return result, nil
	}))
}

// getAffectedFlows finds stored flows touched by recent code changes.
// @intent trace flows touched by changed nodes so regression review can happen at the flow level.
// @param request uses repo_root and base to define the diff range.
// @requires ChangesGitClient must be configured.
// @ensures returns affected flows with their changed node IDs when analysis succeeds.
// @sideEffect reads git diff data from the configured repository root.
// @see mcp.handlers.detectChanges
func (h *handlers) getAffectedFlows(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ctx = h.applyWorkspace(ctx, request)
	log := h.logger()

	repoRoot, err := request.RequireString("repo_root")
	if err != nil {
		return missingParamResult(err)
	}
	base := request.GetString("base", "HEAD~1")

	validatedRepoRoot, err := h.validateRepoRoot(repoRoot)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	repoRoot = validatedRepoRoot

	log.Info("get_affected_flows called", "repo_root", repoRoot, "base", base)

	if h.deps.ChangesGitClient == nil {
		return mcp.NewToolResultError("ChangesGitClient not configured"), nil
	}

	return finalizeToolResult(h.cachedExecute(ctx, "get_affected_flows:", map[string]any{"repo_root": repoRoot, "base": base, "namespace": requestNamespace(request)}, func() (string, error) {
		chSvc := changes.New(h.deps.DB, h.deps.ChangesGitClient)
		risks, err := chSvc.Analyze(ctx, repoRoot, base)
		if err != nil {
			return "", trace.Wrap(err, "changes analyze error")
		}

		if len(risks) == 0 {
			result, err := marshalJSON(map[string]any{"affected_flows": []any{}})
			if err != nil {
				return "", trace.Wrap(err, "marshal result")
			}
			return result, nil
		}

		changedNodeIDs := make([]uint, 0, len(risks))
		for _, r := range risks {
			changedNodeIDs = append(changedNodeIDs, r.Node.ID)
		}
		ns := ctxns.FromContext(ctx)

		var memberships []model.FlowMembership
		q := h.deps.DB.WithContext(ctx).Where("node_id IN ?", changedNodeIDs).Where("namespace = ?", ns)
		if err := q.Find(&memberships).Error; err != nil {
			return "", trace.Wrap(err, "find affected flow memberships")
		}

		flowNodes := map[uint][]uint{}
		for _, m := range memberships {
			flowNodes[m.FlowID] = append(flowNodes[m.FlowID], m.NodeID)
		}

		if len(flowNodes) == 0 {
			result, err := marshalJSON(map[string]any{"affected_flows": []any{}})
			if err != nil {
				return "", trace.Wrap(err, "marshal result")
			}
			return result, nil
		}

		flowIDs := make([]uint, 0, len(flowNodes))
		for fid := range flowNodes {
			flowIDs = append(flowIDs, fid)
		}

		var flowList []model.Flow
		flowQ := h.deps.DB.WithContext(ctx).Where("id IN ?", flowIDs).Where("namespace = ?", ns)
		if err := flowQ.Find(&flowList).Error; err != nil {
			return "", trace.Wrap(err, "find affected flows")
		}

		affected := make([]map[string]any, len(flowList))
		for i, f := range flowList {
			affected[i] = map[string]any{
				"id":             f.ID,
				"name":           f.Name,
				"affected_nodes": flowNodes[f.ID],
			}
		}

		result, err := marshalJSON(map[string]any{"affected_flows": affected})
		if err != nil {
			return "", trace.Wrap(err, "marshal result")
		}
		return result, nil
	}))
}

// findDeadCode returns nodes that have no incoming usage edges.
// @intent find unused code candidates so maintainers can reduce long-term maintenance burden.
// @param request path and kinds narrow the detection scope.
// @requires DeadcodeAnalyzer must be configured.
// @ensures returns dead_code entries and their count when analysis succeeds.
// @domainRule only nodes without incoming edges qualify as dead code candidates.
func (h *handlers) findDeadCode(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ctx = h.applyWorkspace(ctx, request)
	log := h.logger()
	log.Info("find_dead_code called")

	opts := deadcode.Options{}
	kinds := request.GetStringSlice("kinds", nil)
	for _, k := range kinds {
		opts.Kinds = append(opts.Kinds, model.NodeKind(k))
	}
	pathPrefix := request.GetString("path", "")
	opts.FilePattern = pathPrefix

	if h.deps.DeadcodeAnalyzer == nil {
		return mcp.NewToolResultError("DeadcodeAnalyzer not configured"), nil
	}

	return finalizeToolResult(h.cachedExecute(ctx, "find_dead_code:", map[string]any{"path": pathPrefix, "kinds": kinds, "namespace": requestNamespace(request)}, func() (string, error) {
		nodes, err := h.deps.DeadcodeAnalyzer.Find(ctx, opts)
		if err != nil {
			return "", trace.Wrap(err, "deadcode error")
		}

		dcResults := make([]map[string]any, len(nodes))
		for i, n := range nodes {
			dcResults[i] = map[string]any{
				"name":       n.QualifiedName,
				"kind":       n.Kind,
				"file":       n.FilePath,
				"start_line": n.StartLine,
			}
		}

		resp := map[string]any{
			"dead_code": dcResults,
			"count":     len(dcResults),
		}
		result, err := marshalJSON(resp)
		if err != nil {
			return "", trace.Wrap(err, "marshal result")
		}
		return result, nil
	}))
}

// findSuspectFallbackEdges returns fallback call edges whose source/target annotations do not overlap on intent/domain rules.
// @intent surface low-confidence fallback call candidates so operators can manually review weakly explained edges.
// @requires FallbackAnalyzer must be configured.
// @ensures returns suspect fallback edge entries and their count when analysis succeeds.
func (h *handlers) findSuspectFallbackEdges(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ctx = h.applyWorkspace(ctx, request)
	log := h.logger()
	log.Info("find_suspect_fallback_edges called")

	if h.deps.FallbackAnalyzer == nil {
		return mcp.NewToolResultError("FallbackAnalyzer not configured"), nil
	}

	return finalizeToolResult(h.cachedExecute(ctx, "find_suspect_fallback_edges:", map[string]any{"namespace": requestNamespace(request)}, func() (string, error) {
		results, err := h.deps.FallbackAnalyzer.FindSuspects(ctx, fallbackanalysis.Options{})
		if err != nil {
			return "", trace.Wrap(err, "fallback suspect analysis error")
		}

		items := make([]map[string]any, len(results))
		for i, result := range results {
			items[i] = map[string]any{
				"edge_kind":   result.Edge.Kind,
				"fingerprint": result.Edge.Fingerprint,
				"source":      result.Source.QualifiedName,
				"source_file": result.Source.FilePath,
				"target":      result.Target.QualifiedName,
				"target_file": result.Target.FilePath,
				"suspect":     result.Suspect,
			}
		}

		resp := map[string]any{
			"suspect_fallback_edges": items,
			"count":                  len(items),
		}
		payload, err := marshalJSON(resp)
		if err != nil {
			return "", trace.Wrap(err, "marshal result")
		}
		return payload, nil
	}))
}

// @intent validate repo_root inputs against configured analysis roots before git-based analysis reads the filesystem.
func (h *handlers) validateRepoRoot(repoRoot string) (string, error) {
	return validateRepoRootWithin(repoRoot, h.deps.RepoRoot, h.workspaceRoot())
}

// validateRepoRootWithin checks that repoRoot resolves to a canonical path within one of the allowed analysis roots.
// @intent prevent git-based analysis from reading paths outside the configured project boundaries.
// @requires configuredRepoRoot or workspaceRoot must be non-empty; repoRoot must be a valid filesystem path.
// @ensures returned path is absolute, symlink-resolved, and contained within an allowed root.
func validateRepoRootWithin(repoRoot, configuredRepoRoot, workspaceRoot string) (string, error) {
	if repoRoot == "" {
		return "", fmt.Errorf("repo_root is required")
	}
	allowedRoots := configuredAnalysisRoots(configuredRepoRoot, workspaceRoot)
	if len(allowedRoots) == 0 {
		return "", fmt.Errorf("analysis repo root is not configured")
	}
	repo, err := canonicalPath(repoRoot)
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

// configuredAnalysisRoots returns the deduplicated list of allowed root paths derived from server config and workspace.
// @intent build the allowlist used by path validation so each source of truth contributes exactly once.
func configuredAnalysisRoots(repoRoot, workspaceRoot string) []string {
	roots := make([]string, 0, 2)
	for _, root := range []string{repoRoot, workspaceRoot} {
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
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

// validatePathWithinAllowedRoots reports whether target falls inside any of the canonical allowed roots.
// @intent enforce that user-supplied paths cannot escape the configured analysis boundary.
// @requires allowedRoots must be non-empty and each entry must be a valid filesystem path.
func validatePathWithinAllowedRoots(target string, allowedRoots []string) (bool, error) {
	for _, root := range allowedRoots {
		base, err := canonicalPath(root)
		if err != nil {
			return false, err
		}
		within, err := isWithinRoot(base, target)
		if err != nil {
			return false, err
		}
		if within {
			return true, nil
		}
	}
	return false, nil
}

// isWithinRoot reports whether target is the same as root or a descendant of it.
// @intent detect path traversal by checking the relative path does not escape upward.
func isWithinRoot(root, target string) (bool, error) {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false, err
	}
	if rel == "." {
		return true, nil
	}
	if rel == ".." {
		return false, nil
	}
	if len(rel) >= 3 && rel[:3] == ".."+string(filepath.Separator) {
		return false, nil
	}
	if filepath.IsAbs(rel) {
		return false, nil
	}
	return true, nil
}

// canonicalPath resolves path to an absolute, symlink-free, cleaned filesystem path.
// @intent normalize user-supplied paths before comparison to prevent symlink-based boundary escapes.
func canonicalPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	return filepath.Clean(real), nil
}
