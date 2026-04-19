package mcp

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/tae2089/trace"

	"github.com/tae2089/code-context-graph/internal/analysis/changes"
	"github.com/tae2089/code-context-graph/internal/analysis/deadcode"
	"github.com/tae2089/code-context-graph/internal/model"
)

// getImpactRadius returns nodes reachable within a bounded dependency radius.
// @intent 특정 노드 변경의 파급 범위를 탐색해 리뷰 우선순위를 정하게 한다.
// @param request qualified_name과 depth로 분석 시작점과 탐색 깊이를 지정한다.
// @requires ImpactAnalyzer가 구성되어 있고 대상 노드가 존재해야 한다.
// @ensures 성공 시 영향 범위 노드 목록을 반환한다.
// @see mcp.handlers.getNode
func (h *handlers) getImpactRadius(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ctx = h.applyWorkspace(ctx, request)
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

	return finalizeToolResult(h.cachedExecute("get_impact_radius:", map[string]any{"qualified_name": qn, "depth": depth, "workspace": request.GetString("workspace", "")}, func() (string, error) {
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

// traceFlow traces the stored call flow that starts from a node.
// @intent 시작 노드가 속한 호출 흐름을 복원해 실행 맥락을 이해하게 한다.
// @param request qualified_name으로 흐름 시작 노드를 지정한다.
// @requires FlowTracer가 구성되어 있고 대상 노드가 존재해야 한다.
// @ensures 성공 시 flow 이름과 멤버 순서를 반환한다.
// @see mcp.handlers.listFlows
func (h *handlers) traceFlow(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ctx = h.applyWorkspace(ctx, request)
	log := h.logger()

	qn, err := request.RequireString("qualified_name")
	if err != nil {
		return missingParamResult(err)
	}

	log.Info("trace_flow called", "qualified_name", qn)

	if h.deps.FlowTracer == nil {
		return mcp.NewToolResultError("FlowTracer not configured"), nil
	}

	return finalizeToolResult(h.cachedExecute("trace_flow:", map[string]any{"qualified_name": qn, "workspace": request.GetString("workspace", "")}, func() (string, error) {
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

// detectChanges analyzes git diff hunks and returns node-level risk scores.
// @intent 최근 변경 중 리뷰 리스크가 높은 함수와 파일을 빠르게 식별한다.
// @param request repo_root는 Git 저장소 루트이고 base는 비교 기준 커밋이다.
// @requires ChangesGitClient가 구성되어 있어야 한다.
// @ensures 성공 시 변경 노드별 hunk 수와 risk score를 반환한다.
// @sideEffect Git diff 조회를 수행한다.
// @see mcp.handlers.getAffectedFlows
func (h *handlers) detectChanges(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ctx = h.applyWorkspace(ctx, request)
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

	return finalizeToolResult(h.cachedExecute("detect_changes:", map[string]any{"repo_root": repoRoot, "base": base, "workspace": request.GetString("workspace", "")}, func() (string, error) {
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
// @intent 변경된 노드가 속한 흐름을 추적해 회귀 영향 범위를 흐름 단위로 보여준다.
// @param request repo_root와 base로 변경 비교 범위를 지정한다.
// @requires ChangesGitClient가 구성되어 있어야 한다.
// @ensures 성공 시 영향받은 flow와 해당 changed node id 목록을 반환한다.
// @sideEffect Git diff 조회를 수행한다.
// @see mcp.handlers.detectChanges
func (h *handlers) getAffectedFlows(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ctx = h.applyWorkspace(ctx, request)
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

	return finalizeToolResult(h.cachedExecute("get_affected_flows:", map[string]any{"repo_root": repoRoot, "base": base, "workspace": request.GetString("workspace", "")}, func() (string, error) {
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

// findDeadCode returns nodes that have no incoming usage edges.
// @intent 정리 후보인 미사용 코드 노드를 찾아 유지보수 부담을 줄이게 한다.
// @param request path와 kinds로 탐지 대상을 좁힐 수 있다.
// @requires DeadcodeAnalyzer가 구성되어 있어야 한다.
// @ensures 성공 시 dead_code 목록과 개수를 반환한다.
// @domainRule incoming edge가 없는 노드만 미사용 코드 후보로 간주한다.
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

	return finalizeToolResult(h.cachedExecute("find_dead_code:", map[string]any{"path": pathPrefix, "kinds": kinds, "workspace": request.GetString("workspace", "")}, func() (string, error) {
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
