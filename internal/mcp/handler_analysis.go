// @index MCP handlers for impact, flow, change-risk, dead-code, and suspect-edge analyses over the stored graph.
package mcp

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"

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
	"github.com/tae2089/code-context-graph/internal/paging"
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
	Name     string            `json:"name"`
	Members  []traceFlowMember `json:"members"`
	Metadata traceFlowMetadata `json:"metadata"`
	Evidence workspaceEvidenceBlock `json:"evidence"`
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
	Pagination paging.Page          `json:"pagination"`
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
	Pagination    paging.Page         `json:"pagination"`
}

// deadCodeItem summarizes one node reported as dead code.
// @intent preserve a stable per-item DTO for findDeadCode responses.
type deadCodeItem struct {
	Name      string         `json:"name"`
	Kind      model.NodeKind `json:"kind"`
	File      string         `json:"file"`
	StartLine int            `json:"start_line"`
}

// suspectFallbackEdgeItem summarizes one fallback edge inspected for suspicion.
// @intent preserve a stable per-item DTO for findSuspectFallbackEdges responses.
type suspectFallbackEdgeItem struct {
	EdgeKind    model.EdgeKind `json:"edge_kind"`
	Fingerprint string         `json:"fingerprint"`
	Source      string         `json:"source"`
	SourceFile  string         `json:"source_file"`
	Target      string         `json:"target"`
	TargetFile  string         `json:"target_file"`
	Suspect     bool           `json:"suspect"`
}

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
			Evidence: h.workspaceEvidenceFromContext(ctx),
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
	ctx = h.applyWorkspace(ctx, request)
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
	pageReq, err := paging.Normalize(paging.Request{Limit: limit, Offset: offset})
	if err != nil {
		return finalizeToolResult("", newToolResultErr(err.Error()))
	}

	validatedRepoRoot, err := h.validateRepoRoot(repoRoot)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	repoRoot = validatedRepoRoot

	log.Info("detect_changes called", "repo_root", repoRoot, "base", base, "limit", pageReq.Limit, "offset", pageReq.Offset)

	if h.deps.ChangesGitClient == nil {
		return mcp.NewToolResultError("ChangesGitClient not configured"), nil
	}

	return finalizeToolResult(h.cachedExecute(ctx, "detect_changes:", map[string]any{"repo_root": repoRoot, "base": base, "limit": pageReq.Limit, "offset": pageReq.Offset, "namespace": requestNamespace(request)}, func() (string, error) {
		chSvc := changes.New(h.deps.DB, h.deps.ChangesGitClient)
		page, err := chSvc.AnalyzePage(ctx, repoRoot, base, pageReq)
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

		resp := detectChangesResponse{Base: base, Entries: entries, Items: entries, Pagination: page.Pagination}
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
	ctx = h.applyWorkspace(ctx, request)
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
	pageReq, err := paging.Normalize(paging.Request{Limit: limit, Offset: offset})
	if err != nil {
		return finalizeToolResult("", newToolResultErr(err.Error()))
	}

	validatedRepoRoot, err := h.validateRepoRoot(repoRoot)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	repoRoot = validatedRepoRoot

	log.Info("get_affected_flows called", "repo_root", repoRoot, "base", base, "limit", pageReq.Limit, "offset", pageReq.Offset)

	if h.deps.ChangesGitClient == nil {
		return mcp.NewToolResultError("ChangesGitClient not configured"), nil
	}

	return finalizeToolResult(h.cachedExecute(ctx, "get_affected_flows:", map[string]any{"repo_root": repoRoot, "base": base, "limit": pageReq.Limit, "offset": pageReq.Offset, "namespace": requestNamespace(request)}, func() (string, error) {
		chSvc := changes.New(h.deps.DB, h.deps.ChangesGitClient)
		changedNodeIDs, err := chSvc.ChangedNodeIDs(ctx, repoRoot, base)
		if err != nil {
			return "", trace.Wrap(err, "changes analyze error")
		}

		emptyResp := func() (string, error) {
			page := paging.BuildPage(pageReq, 0, false)
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
			return emptyResp()
		}

		flowIDs := make([]uint, 0, len(flowNodes))
		for fid := range flowNodes {
			flowIDs = append(flowIDs, fid)
		}

		var flowList []model.Flow
		flowQ := h.deps.DB.WithContext(ctx).
			Where("id IN ?", flowIDs).
			Where("namespace = ?", ns).
			Order("name ASC").
			Order("id ASC").
			Limit(pageReq.Limit + 1).
			Offset(pageReq.Offset)
		if err := flowQ.Find(&flowList).Error; err != nil {
			return "", trace.Wrap(err, "find affected flows")
		}
		hasMore := len(flowList) > pageReq.Limit
		if hasMore {
			flowList = flowList[:pageReq.Limit]
		}

		affected := make([]affectedFlowEntry, len(flowList))
		for i, f := range flowList {
			affected[i] = affectedFlowEntry{ID: f.ID, Name: f.Name, AffectedNodes: flowNodes[f.ID]}
		}

		page := paging.BuildPage(pageReq, len(affected), hasMore)
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

// findDeadCode returns nodes that have no incoming usage edges.
// @intent find unused code candidates so maintainers can reduce long-term maintenance burden.
// @param request path and kinds narrow the detection scope.
// @requires DeadcodeAnalyzer must be configured.
// @ensures returns dead_code entries and their count when analysis succeeds.
// @domainRule only nodes without incoming edges qualify as dead code candidates.
func (h *handlers) findDeadCode(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ctx = h.applyWorkspace(ctx, request)
	h.logger().Info("find_dead_code called")

	input, err := decodeFindDeadCodeRequest(request)
	if err != nil {
		return finalizeToolResult("", err)
	}

	if h.deps.DeadcodeAnalyzer == nil {
		return mcp.NewToolResultError("DeadcodeAnalyzer not configured"), nil
	}

	opts := deadcode.Options{Page: input.Page, FilePattern: input.PathPrefix}
	for _, k := range input.Kinds {
		opts.Kinds = append(opts.Kinds, model.NodeKind(k))
	}

	cacheParams := map[string]any{
		"path":      input.PathPrefix,
		"kinds":     input.Kinds,
		"limit":     input.Page.Limit,
		"offset":    input.Page.Offset,
		"namespace": input.Namespace,
	}
	return finalizeToolResult(h.cachedExecute(ctx, "find_dead_code:", cacheParams, func() (string, error) {
		page, err := h.deps.DeadcodeAnalyzer.FindPage(ctx, opts)
		if err != nil {
			return "", trace.Wrap(err, "deadcode error")
		}

		items := make([]deadCodeItem, len(page.Items))
		for i, n := range page.Items {
			items[i] = deadCodeItem{Name: n.QualifiedName, Kind: n.Kind, File: n.FilePath, StartLine: n.StartLine}
		}

		result, err := encodePagedListResponse("dead_code", items, page.Pagination)
		if err != nil {
			return "", trace.Wrap(err, "marshal result")
		}
		return result, nil
	}))
}

// findSuspectFallbackEdges returns fallback call edges whose source/target annotations do not overlap on intent/domain rules.
// @intent surface low-confidence fallback call candidates so operators can manually review weakly explained edges.
// @param request supports optional limit and offset to bound fallback suspect analysis.
// @requires FallbackAnalyzer must be configured.
// @ensures returns legacy suspect_fallback_edges plus items/count/pagination when analysis succeeds.
func (h *handlers) findSuspectFallbackEdges(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ctx = h.applyWorkspace(ctx, request)
	h.logger().Info("find_suspect_fallback_edges called")

	input, err := decodeFindSuspectFallbackRequest(request)
	if err != nil {
		return finalizeToolResult("", err)
	}

	if h.deps.FallbackAnalyzer == nil {
		return mcp.NewToolResultError("FallbackAnalyzer not configured"), nil
	}

	cacheParams := map[string]any{
		"limit":     input.Page.Limit,
		"offset":    input.Page.Offset,
		"namespace": input.Namespace,
	}
	return finalizeToolResult(h.cachedExecute(ctx, "find_suspect_fallback_edges:", cacheParams, func() (string, error) {
		page, err := h.deps.FallbackAnalyzer.FindSuspectsPage(ctx, fallbackanalysis.Options{Page: input.Page})
		if err != nil {
			return "", trace.Wrap(err, "fallback suspect analysis error")
		}

		items := make([]suspectFallbackEdgeItem, len(page.Items))
		for i, result := range page.Items {
			items[i] = suspectFallbackEdgeItem{
				EdgeKind:    result.Edge.Kind,
				Fingerprint: result.Edge.Fingerprint,
				Source:      result.Source.QualifiedName,
				SourceFile:  result.Source.FilePath,
				Target:      result.Target.QualifiedName,
				TargetFile:  result.Target.FilePath,
				Suspect:     result.Suspect,
			}
		}

		payload, err := encodePagedListResponse("suspect_fallback_edges", items, page.Pagination)
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
	return slices.Contains(values, target)
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
