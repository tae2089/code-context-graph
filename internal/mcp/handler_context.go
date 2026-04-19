package mcp

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/tae2089/trace"

	"github.com/tae2089/code-context-graph/internal/analysis/changes"
	"github.com/tae2089/code-context-graph/internal/ctxns"
	"github.com/tae2089/code-context-graph/internal/model"
)

func (h *handlers) getMinimalContext(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ctx = h.applyWorkspace(ctx, request)
	log := h.logger()

	task := request.GetString("task", "")
	repoRoot := request.GetString("repo_root", "")
	base := request.GetString("base", "HEAD~1")

	log.Info("get_minimal_context called", "task", task, "repo_root", repoRoot, "base", base)

	return finalizeToolResult(h.cachedExecute("get_minimal_context:", map[string]any{
		"task":      task,
		"repo_root": repoRoot,
		"base":      base,
		"workspace": request.GetString("workspace", ""),
	}, func() (string, error) {
		ns := ctxns.FromContext(ctx)

		nodeQ := h.deps.DB.WithContext(ctx).Model(&model.Node{})
		if ns != "" {
			nodeQ = nodeQ.Where("namespace = ?", ns)
		}
		var nodeCount, edgeCount int64
		if err := nodeQ.Count(&nodeCount).Error; err != nil {
			return "", trace.Wrap(err, "count nodes")
		}
		if err := h.deps.DB.WithContext(ctx).Model(&model.Edge{}).Count(&edgeCount).Error; err != nil {
			return "", trace.Wrap(err, "count edges")
		}

		type fileCount struct{ Count int64 }
		var fc fileCount
		fileQ := h.deps.DB.WithContext(ctx).Model(&model.Node{}).Select("COUNT(DISTINCT file_path) as count")
		if ns != "" {
			fileQ = fileQ.Where("namespace = ?", ns)
		}
		if err := fileQ.Scan(&fc).Error; err != nil {
			return "", trace.Wrap(err, "count files")
		}

		summary := fmt.Sprintf("%d nodes, %d edges, %d files", nodeCount, edgeCount, fc.Count)

		risk := "unknown"
		var riskScore float64
		var keyEntities []string
		var testGaps int

		if repoRoot != "" && h.deps.ChangesGitClient != nil {
			chSvc := changes.New(h.deps.DB, h.deps.ChangesGitClient)
			risks, err := chSvc.Analyze(ctx, repoRoot, base)
			if err == nil && len(risks) > 0 {
				var maxRisk float64
				var totalRisk float64
				for _, r := range risks {
					if r.RiskScore > maxRisk {
						maxRisk = r.RiskScore
					}
					totalRisk += r.RiskScore
					keyEntities = append(keyEntities, r.Node.QualifiedName)
				}
				riskScore = totalRisk / float64(len(risks))

				switch {
				case maxRisk >= 0.7:
					risk = "high"
				case maxRisk >= 0.4:
					risk = "medium"
				default:
					risk = "low"
				}

				if len(keyEntities) > 5 {
					keyEntities = keyEntities[:5]
				}

				for _, r := range risks {
					hasTest := false
					var testEdges int64
					h.deps.DB.WithContext(ctx).Model(&model.Edge{}).
						Where("to_node_id = ? AND kind = ?", r.Node.ID, model.EdgeKindTestedBy).
						Count(&testEdges)
					if testEdges > 0 {
						hasTest = true
					}
					if !hasTest {
						testGaps++
					}
				}
			}
		}
		if keyEntities == nil {
			keyEntities = []string{}
		}

		type commCount struct {
			CommunityID uint
			Count       int
		}
		var ccRows []commCount
		if err := h.deps.DB.WithContext(ctx).
			Model(&model.CommunityMembership{}).
			Select("community_id, COUNT(*) as count").
			Group("community_id").
			Scan(&ccRows).Error; err != nil {
			return "", trace.Wrap(err, "group community memberships")
		}
		ccMap := make(map[uint]int, len(ccRows))
		for _, r := range ccRows {
			ccMap[r.CommunityID] = r.Count
		}

		var communities []model.Community
		if err := h.deps.DB.WithContext(ctx).Find(&communities).Error; err != nil {
			return "", trace.Wrap(err, "find communities")
		}

		type commInfo struct {
			Label     string `json:"label"`
			NodeCount int    `json:"node_count"`
		}
		commInfos := make([]commInfo, len(communities))
		for i, c := range communities {
			commInfos[i] = commInfo{Label: c.Label, NodeCount: ccMap[c.ID]}
		}
		sort.Slice(commInfos, func(i, j int) bool {
			return commInfos[i].NodeCount > commInfos[j].NodeCount
		})
		if len(commInfos) > 3 {
			commInfos = commInfos[:3]
		}

		type flowCount struct {
			FlowID uint
			Count  int
		}
		var fcRows []flowCount
		if err := h.deps.DB.WithContext(ctx).
			Model(&model.FlowMembership{}).
			Select("flow_id, COUNT(*) as count").
			Group("flow_id").
			Scan(&fcRows).Error; err != nil {
			return "", trace.Wrap(err, "group flow memberships")
		}
		fcMap := make(map[uint]int, len(fcRows))
		for _, r := range fcRows {
			fcMap[r.FlowID] = r.Count
		}

		var flowList []model.Flow
		if err := h.deps.DB.WithContext(ctx).Find(&flowList).Error; err != nil {
			return "", trace.Wrap(err, "find flows")
		}

		type flowInfo struct {
			Name      string `json:"name"`
			NodeCount int    `json:"node_count"`
		}
		flowInfos := make([]flowInfo, len(flowList))
		for i, f := range flowList {
			flowInfos[i] = flowInfo{Name: f.Name, NodeCount: fcMap[f.ID]}
		}
		sort.Slice(flowInfos, func(i, j int) bool {
			return flowInfos[i].NodeCount > flowInfos[j].NodeCount
		})
		if len(flowInfos) > 3 {
			flowInfos = flowInfos[:3]
		}

		suggestedTools := suggestTools(task)

		resp := map[string]any{
			"summary":         summary,
			"risk":            risk,
			"risk_score":      riskScore,
			"key_entities":    keyEntities,
			"test_gaps":       testGaps,
			"top_communities": commInfos,
			"top_flows":       flowInfos,
			"suggested_tools": suggestedTools,
		}

		result, err := marshalJSON(resp)
		if err != nil {
			return "", trace.Wrap(err, "marshal result")
		}
		return result, nil
	}))
}

func suggestTools(task string) []string {
	lower := strings.ToLower(task)

	reviewKeywords := []string{"review", "pr", "merge", "diff"}
	debugKeywords := []string{"debug", "bug", "error", "fix"}
	refactorKeywords := []string{"refactor", "rename", "dead", "clean"}
	onboardKeywords := []string{"onboard", "understand", "explore", "arch"}

	for _, kw := range reviewKeywords {
		if strings.Contains(lower, kw) {
			return []string{"detect_changes", "get_affected_flows", "search"}
		}
	}
	for _, kw := range debugKeywords {
		if strings.Contains(lower, kw) {
			return []string{"search", "query_graph", "trace_flow"}
		}
	}
	for _, kw := range refactorKeywords {
		if strings.Contains(lower, kw) {
			return []string{"find_dead_code", "find_large_functions", "get_architecture_overview"}
		}
	}
	for _, kw := range onboardKeywords {
		if strings.Contains(lower, kw) {
			return []string{"get_architecture_overview", "list_communities", "list_flows"}
		}
	}

	return []string{"detect_changes", "search", "get_architecture_overview"}
}
