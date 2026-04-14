package mcp

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/tae2089/trace"

	"github.com/imtaebin/code-context-graph/internal/analysis/changes"
	"github.com/imtaebin/code-context-graph/internal/analysis/deadcode"
	"github.com/imtaebin/code-context-graph/internal/model"
)

func (h *handlers) getImpactRadius(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	log := h.logger()

	qn, err := request.RequireString("qualified_name")
	if err != nil {
		return missingParamResult(err)
	}
	depth := request.GetInt("depth", 1)

	log.Info("get_impact_radius called", "qualified_name", qn, "depth", depth)

	if h.deps.ImpactAnalyzer == nil {
		return mcp.NewToolResultError("ImpactAnalyzer not configured"), nil
	}

	return finalizeToolResult(h.cachedExecute("get_impact_radius:", map[string]any{"qualified_name": qn, "depth": depth}, func() (string, error) {
		node, err := h.deps.Store.GetNode(ctx, qn)
		if err != nil {
			log.Error("store error", "tool", "get_impact_radius", trace.SlogError(err))
			return "", trace.Wrap(err, "store error")
		}
		if node == nil {
			log.Warn("node not found", "qualified_name", qn)
			return "", nodeNotFoundErr(qn)
		}

		nodes, err := h.deps.ImpactAnalyzer.ImpactRadius(ctx, node.ID, depth)
		if err != nil {
			log.Error("impact analysis error", "node_id", node.ID, trace.SlogError(err))
			return "", trace.Wrap(err, "impact analysis error")
		}

		log.Info("get_impact_radius completed", "qualified_name", qn, "result_count", len(nodes))

		impactResult := make([]map[string]any, len(nodes))
		for i, n := range nodes {
			impactResult[i] = nodeToBasicMap(n)
		}
		result, err := marshalJSON(impactResult)
		if err != nil {
			return "", trace.Wrap(err, "marshal result")
		}
		return result, nil
	}))
}

func (h *handlers) traceFlow(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	log := h.logger()

	qn, err := request.RequireString("qualified_name")
	if err != nil {
		return missingParamResult(err)
	}

	log.Info("trace_flow called", "qualified_name", qn)

	if h.deps.FlowTracer == nil {
		return mcp.NewToolResultError("FlowTracer not configured"), nil
	}

	return finalizeToolResult(h.cachedExecute("trace_flow:", map[string]any{"qualified_name": qn}, func() (string, error) {
		node, err := h.deps.Store.GetNode(ctx, qn)
		if err != nil {
			log.Error("store error", "tool", "trace_flow", trace.SlogError(err))
			return "", trace.Wrap(err, "store error")
		}
		if node == nil {
			log.Warn("node not found", "qualified_name", qn)
			return "", nodeNotFoundErr(qn)
		}

		flow, err := h.deps.FlowTracer.TraceFlow(ctx, node.ID)
		if err != nil {
			log.Error("trace error", "node_id", node.ID, trace.SlogError(err))
			return "", trace.Wrap(err, "trace error")
		}

		log.Info("trace_flow completed", "qualified_name", qn, "members", len(flow.Members))

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
		}
		result, err := marshalJSON(data)
		if err != nil {
			return "", trace.Wrap(err, "marshal result")
		}
		return result, nil
	}))
}

func (h *handlers) detectChanges(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	log := h.logger()

	repoRoot, err := request.RequireString("repo_root")
	if err != nil {
		return missingParamResult(err)
	}
	base := request.GetString("base", "HEAD~1")

	log.Info("detect_changes called", "repo_root", repoRoot, "base", base)

	if h.deps.ChangesGitClient == nil {
		return mcp.NewToolResultError("ChangesGitClient not configured"), nil
	}

	return finalizeToolResult(h.cachedExecute("detect_changes:", map[string]any{"repo_root": repoRoot, "base": base}, func() (string, error) {
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

func (h *handlers) getAffectedFlows(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	log := h.logger()

	repoRoot, err := request.RequireString("repo_root")
	if err != nil {
		return missingParamResult(err)
	}
	base := request.GetString("base", "HEAD~1")

	log.Info("get_affected_flows called", "repo_root", repoRoot, "base", base)

	if h.deps.ChangesGitClient == nil {
		return mcp.NewToolResultError("ChangesGitClient not configured"), nil
	}

	return finalizeToolResult(h.cachedExecute("get_affected_flows:", map[string]any{"repo_root": repoRoot, "base": base}, func() (string, error) {
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

		var memberships []model.FlowMembership
		if err := h.deps.DB.WithContext(ctx).Where("node_id IN ?", changedNodeIDs).Find(&memberships).Error; err != nil {
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
		if err := h.deps.DB.WithContext(ctx).Where("id IN ?", flowIDs).Find(&flowList).Error; err != nil {
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

func (h *handlers) findDeadCode(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
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

	return finalizeToolResult(h.cachedExecute("find_dead_code:", map[string]any{"path": pathPrefix, "kinds": kinds}, func() (string, error) {
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
