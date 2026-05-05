// @index MCP context handlers that summarize graph state for downstream tool selection.
package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/tae2089/trace"

	"github.com/tae2089/code-context-graph/internal/analysis/changes"
	"github.com/tae2089/code-context-graph/internal/ctxns"
	"github.com/tae2089/code-context-graph/internal/model"
	"github.com/tae2089/code-context-graph/internal/paging"
)

// minimalContextRiskPageLimit bounds how many recent risk entries minimal-context summarizes.
// @intent cap risk-aggregation work in get_minimal_context so a large diff cannot drive an unbounded scan.
// @domainRule risk_score average and test_gaps count reflect the top-N riskiest entries (sorted by changes.Service), not the full diff.
const minimalContextRiskPageLimit = 50

// fileCount carries a single COUNT(DISTINCT file_path) scan result.
// @intent capture the distinct-file count returned by the minimal-context summary query.
type fileCount struct {
	Count int64
}

// commCount holds aggregated membership counts per community.
// @intent transport GROUP BY community_id results into MCP response shaping.
type commCount struct {
	Label       string
	CommunityID uint
	Count       int
}

// commInfo is the summarized community payload shared by MCP responses.
// @intent serialize minimal-context community summaries without introducing extra response fields.
type minimalContextCommInfo struct {
	Label     string `json:"label"`
	NodeCount int    `json:"node_count"`
}

// flowCount holds aggregated membership counts per flow.
// @intent transport GROUP BY flow_id results into MCP response shaping.
type flowCount struct {
	Name   string
	FlowID uint
	Count  int
}

// flowInfo is the summarized flow payload shared by MCP responses.
// @intent serialize minimal-context flow summaries without introducing extra response fields.
type minimalContextFlowInfo struct {
	Name      string `json:"name"`
	NodeCount int    `json:"node_count"`
}

// minimalContextResponse is the typed minimal-context payload sent over MCP.
// @intent keep the minimal-context wire shape explicit without changing serialized output.
type minimalContextResponse struct {
	Summary        string                   `json:"summary"`
	Risk           string                   `json:"risk"`
	RiskScore      float64                  `json:"risk_score"`
	KeyEntities    []string                 `json:"key_entities"`
	TestGaps       int                      `json:"test_gaps"`
	TopCommunities []minimalContextCommInfo `json:"top_communities"`
	TopFlows       []minimalContextFlowInfo `json:"top_flows"`
	DerivedState   map[string]any           `json:"derived_state"`
	SuggestedTools []string                 `json:"suggested_tools"`
	Evidence       namespaceEvidenceBlock   `json:"evidence"`
}

// getMinimalContext returns a compact project snapshot with risk hints and suggested tools.
// @intent give agents a cheap first read of namespace state before they spend tokens on deeper graph queries.
func (h *handlers) getMinimalContext(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ctx = h.applyNamespace(ctx, request)
	log := h.logger()

	task := request.GetString("task", "")
	repoRoot := request.GetString("repo_root", "")
	base := request.GetString("base", "HEAD~1")
	if repoRoot != "" {
		validatedRepoRoot, err := h.validateRepoRoot(repoRoot)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		repoRoot = validatedRepoRoot
	}

	log.Info("get_minimal_context called", "task", task, "repo_root", repoRoot, "base", base)

	return finalizeToolResult(h.cachedExecute(ctx, "get_minimal_context:", map[string]any{
		"task":            task,
		"repo_root":       repoRoot,
		"base":            base,
		"namespace":       requestNamespace(request),
		"risk_page_limit": minimalContextRiskPageLimit,
	}, func() (string, error) {
		ns := ctxns.FromContext(ctx)

		nodeQ := h.deps.DB.WithContext(ctx).Model(&model.Node{}).Where("namespace = ?", ns)
		var nodeCount, edgeCount int64
		if err := nodeQ.Count(&nodeCount).Error; err != nil {
			return "", trace.Wrap(err, "count nodes")
		}
		edgeQ := h.deps.DB.WithContext(ctx).Model(&model.Edge{}).Where("namespace = ?", ns)
		if err := edgeQ.Count(&edgeCount).Error; err != nil {
			return "", trace.Wrap(err, "count edges")
		}

		var fc fileCount
		fileQ := h.deps.DB.WithContext(ctx).Model(&model.Node{}).Select("COUNT(DISTINCT file_path) as count").Where("namespace = ?", ns)
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
			page, err := chSvc.AnalyzePage(ctx, repoRoot, base, paging.Request{Limit: minimalContextRiskPageLimit, Offset: 0})
			risks := page.Items
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
					testEdgeQ := h.deps.DB.WithContext(ctx).Model(&model.Edge{}).
						Where("to_node_id = ? AND kind = ?", r.Node.ID, model.EdgeKindTestedBy).
						Where("namespace = ?", ns)
					testEdgeQ.Count(&testEdges)
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
		var ccRows []commCount
		commCountQ := h.deps.DB.WithContext(ctx).
			Model(&model.CommunityMembership{}).
			Joins("JOIN communities ON communities.id = community_memberships.community_id").
			Where("communities.namespace = ?", ns)
		if err := commCountQ.
			Select("community_id, communities.label as label, COUNT(*) as count").
			Group("community_id").
			Group("communities.label").
			Order("count DESC").
			Order("community_id ASC").
			Limit(3).
			Scan(&ccRows).Error; err != nil {
			return "", trace.Wrap(err, "group community memberships")
		}
		commInfos := make([]minimalContextCommInfo, len(ccRows))
		for i, r := range ccRows {
			commInfos[i] = minimalContextCommInfo{Label: r.Label, NodeCount: r.Count}
		}
		var fcRows []flowCount
		flowCountQ := h.deps.DB.WithContext(ctx).
			Model(&model.FlowMembership{}).
			Joins("JOIN flows ON flows.id = flow_memberships.flow_id").
			Where("flow_memberships.namespace = ?", ns)
		if err := flowCountQ.
			Select("flow_id, flows.name as name, COUNT(*) as count").
			Group("flow_id").
			Group("flows.name").
			Order("count DESC").
			Order("flow_id ASC").
			Limit(3).
			Scan(&fcRows).Error; err != nil {
			return "", trace.Wrap(err, "group flow memberships")
		}
		flowInfos := make([]minimalContextFlowInfo, len(fcRows))
		for i, r := range fcRows {
			flowInfos[i] = minimalContextFlowInfo{Name: r.Name, NodeCount: r.Count}
		}

		suggestedTools := suggestTools(task)

		resp := minimalContextResponse{
			Summary:        summary,
			Risk:           risk,
			RiskScore:      riskScore,
			KeyEntities:    keyEntities,
			TestGaps:       testGaps,
			TopCommunities: commInfos,
			TopFlows:       flowInfos,
			DerivedState:   derivedStateSummary(),
			SuggestedTools: suggestedTools,
			Evidence:       h.namespaceEvidenceFromContext(ctx),
		}

		result, err := marshalJSON(resp)
		if err != nil {
			return "", trace.Wrap(err, "marshal result")
		}
		return result, nil
	}))
}

// suggestTools maps common task wording to the MCP tools most likely to help.
// @intent steer callers toward high-signal graph operations without requiring them to know the full tool catalog.
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
