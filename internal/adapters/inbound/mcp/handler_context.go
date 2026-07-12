// @index MCP context handlers that summarize graph state for downstream tool selection.
package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/tae2089/trace"
)

// minimalContextRiskPageLimit bounds how many recent risk entries minimal-context summarizes.
// @intent cap risk-aggregation work in get_minimal_context so a large diff cannot drive an unbounded scan.
// @domainRule risk_score average and test_gaps count reflect the top-N riskiest entries (sorted by changes.Service), not the full diff.
const minimalContextRiskPageLimit = 50

// commInfo is the summarized community payload shared by MCP responses.
// @intent serialize minimal-context community summaries without introducing extra response fields.
type minimalContextCommInfo struct {
	Label     string `json:"label"`
	NodeCount int    `json:"node_count"`
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
		stats, err := h.deps.Graph.Statistics.GraphStatistics(ctx)
		if err != nil {
			return "", err
		}
		summary := fmt.Sprintf("%d nodes, %d edges, %d files", stats.NodeCount, stats.EdgeCount, stats.FileCount)

		risk := "unknown"
		var riskScore float64
		var keyEntities []string
		var testGaps int

		if repoRoot != "" && h.deps.Analysis.Changes != nil {
			page, err := h.deps.Analysis.Changes.AnalyzePage(ctx, repoRoot, base, minimalContextRiskPageLimit, 0)
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

				ids := make([]uint, len(risks))
				for i := range risks {
					ids[i] = risks[i].Node.ID
				}
				if h.deps.Analysis.Reader != nil {
					testGaps, _ = h.deps.Analysis.Reader.UntestedCount(ctx, ids)
				}
			}
		}
		if keyEntities == nil {
			keyEntities = []string{}
		}
		ccRows, err := h.deps.Analysis.Reader.TopCommunities(ctx, 3)
		if err != nil {
			return "", trace.Wrap(err, "group community memberships")
		}
		commInfos := make([]minimalContextCommInfo, len(ccRows))
		for i, r := range ccRows {
			commInfos[i] = minimalContextCommInfo{Label: r.Name, NodeCount: int(r.Count)}
		}
		fcRows, err := h.deps.Analysis.Reader.TopFlows(ctx, 3)
		if err != nil {
			return "", trace.Wrap(err, "group flow memberships")
		}
		flowInfos := make([]minimalContextFlowInfo, len(fcRows))
		for i, r := range fcRows {
			flowInfos[i] = minimalContextFlowInfo{Name: r.Name, NodeCount: int(r.Count)}
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
			return []string{"get_impact_radius", "query_graph", "search"}
		}
	}
	for _, kw := range onboardKeywords {
		if strings.Contains(lower, kw) {
			return []string{"list_flows", "get_minimal_context", "search"}
		}
	}

	return []string{"detect_changes", "search", "query_graph"}
}
