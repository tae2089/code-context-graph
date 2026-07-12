// @index MCP prompt handlers that compose graph queries into review, debug, onboarding, and pre-merge workflows.
package mcp

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/tae2089/trace"

	"github.com/tae2089/code-context-graph/internal/app/analyze/changes"
	"github.com/tae2089/code-context-graph/internal/app/analyze/query"
	requestctx "github.com/tae2089/code-context-graph/internal/ctx"
	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

const (
	promptHardCap      = 20
	promptSectionCap   = 10
	promptCallGraphCap = 10
)

// @intent clamp the optional prompt limit argument to the handler's hard cap.
// @domainRule invalid or non-positive prompt limits fall back to the section default instead of failing the prompt.
func promptLimitArg(args map[string]string, defaultLimit, hardCap int) int {
	raw := strings.TrimSpace(args["limit"])
	if raw == "" {
		return defaultLimit
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit <= 0 {
		return defaultLimit
	}
	return min(limit, hardCap)
}

// @intent append a visible truncation marker when a prompt section omits extra items.
// @domainRule prompt truncation messages show how many items were rendered, not an expensive total count.
func appendPromptTruncation(sb *strings.Builder, shown int) {
	if shown <= 0 {
		return
	}
	sb.WriteString(fmt.Sprintf("_... 일부 결과 생략 (표시: %d건)_\n", shown))
}

// @intent render a prompt list section and append a truncation marker when needed.
func appendPromptItems[T any](sb *strings.Builder, items []T, render func(T), truncated bool) {
	for _, item := range items {
		render(item)
	}
	if truncated {
		appendPromptTruncation(sb, len(items))
	}
}

// promptHandlers groups dependencies for MCP prompt generation.
// @intent Groups dependencies so prompt handlers can reuse the shared database and analyzers.
type promptHandlers struct {
	deps *Deps
}

// @intent resolve the query service dependency with a DB-backed default.
func (p *promptHandlers) queryService() QueryService {
	return p.deps.Graph.Query
}

// promptResult wraps plain text in the MCP prompt result shape.
// @intent Enables prompt handlers to generate consistent user message responses from plain strings.
// @param text The user message to be returned as the prompt body.
// @return Returns a GetPromptResult containing a single user text message.
func promptResult(text string) *mcp.GetPromptResult {
	return &mcp.GetPromptResult{
		Messages: []mcp.PromptMessage{
			{
				Role: mcp.RoleUser,
				Content: mcp.TextContent{
					Type: "text",
					Text: text,
				},
			},
		},
	}
}

// @intent pick the namespace for a prompt invocation, preferring an explicit argument over context.
func resolvePromptNamespace(ctx context.Context, args map[string]string) string {
	if namespace := args["namespace"]; namespace != "" {
		return requestctx.Normalize(namespace)
	}
	return resolveNamespace(ctx, "")
}

// @intent resolve the on-disk root used to validate prompt repo paths, falling back to the namespace default.
func promptNamespaceRoot(deps *Deps) string {
	if deps.Runtime.NamespaceRoot != "" {
		return deps.Runtime.NamespaceRoot
	}
	return "namespaces"
}

// reviewChanges builds a prompt summarizing change risk for the diff range.
// @intent Provides a single view of high-risk functions before reviewing changes.
// @param request Defines the Git comparison range using repo_root and base arguments.
// @requires ChangesGitClient must be configured to perform actual change analysis.
// @ensures Returns a prompt including risk analysis on success.
// @sideEffect Performs Git diff lookups and database-based risk queries.
// @see mcp.promptHandlers.preMergeCheck
func (p *promptHandlers) reviewChanges(ctx context.Context, request mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	args := request.Params.Arguments
	ctx = requestctx.WithNamespace(ctx, resolvePromptNamespace(ctx, args))
	riskLimit := promptLimitArg(args, promptHardCap, promptHardCap)
	repoRoot := args["repo_root"]
	base := args["base"]
	if base == "" {
		base = "HEAD~1"
	}

	validatedRepoRoot, err := validateRepoRootWithin(repoRoot, p.deps.Runtime.RepoRoot, promptNamespaceRoot(p.deps))
	if err != nil {
		return nil, err
	}
	repoRoot = validatedRepoRoot

	if p.deps.Analysis.Changes == nil {
		return promptResult("변경사항이 없습니다 (GitClient가 설정되지 않음)"), nil
	}

	risksPage, err := p.deps.Analysis.Changes.AnalyzePage(ctx, repoRoot, base, riskLimit, 0)
	if err != nil {
		return nil, trace.Wrap(err, "changes analyze")
	}
	risks := risksPage.Items

	if len(risks) == 0 {
		return promptResult("변경사항이 없습니다"), nil
	}

	var sb strings.Builder
	sb.WriteString("## 변경사항 리스크 분석\n\n")

	appendPromptItems(&sb, risks, func(r changes.RiskEntry) {
		sb.WriteString(fmt.Sprintf("- **%s** (%s:%d-%d) — 리스크 점수: %.1f, Hunk 수: %d\n",
			r.Node.QualifiedName, r.Node.FilePath, r.Node.StartLine, r.Node.EndLine,
			r.RiskScore, r.HunkCount))
	}, risksPage.HasMore)

	return promptResult(sb.String()), nil
}

// debugIssue builds a prompt with related code search results and call graph hints.
// @intent Creates a starting point for debugging by gathering code candidates and call relationships related to an issue description.
// @param request description is the issue narrative used for searching.
// @requires SearchBackend and DB must be configured.
// @ensures Returns a prompt including a list of related code and a call graph section on success.
// @sideEffect Performs search index lookups and graph queries.
func (p *promptHandlers) debugIssue(ctx context.Context, request mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	args := request.Params.Arguments
	ctx = requestctx.WithNamespace(ctx, resolvePromptNamespace(ctx, args))
	description := args["description"]
	resultLimit := promptLimitArg(args, promptHardCap, promptHardCap)
	callGraphLimit := promptLimitArg(args, promptCallGraphCap, promptCallGraphCap)
	searchFetchLimit := resultLimit + 1

	if p.deps.Graph.Search == nil {
		return promptResult("검색 백엔드가 설정되지 않았습니다."), nil
	}

	nodes, err := p.deps.Graph.Search.Query(ctx, description, searchFetchLimit)
	if err != nil {
		return nil, trace.Wrap(err, "search")
	}
	searchTruncated := len(nodes) > resultLimit

	if len(nodes) == 0 {
		seen := map[uint]bool{}
		for _, token := range strings.Fields(description) {
			tokenNodes, err := p.deps.Graph.Search.Query(ctx, token, searchFetchLimit)
			if err != nil {
				continue
			}
			if len(tokenNodes) > resultLimit {
				searchTruncated = true
			}
			for _, n := range tokenNodes {
				if !seen[n.ID] {
					nodes = append(nodes, n)
					seen[n.ID] = true
					if len(nodes) > resultLimit {
						searchTruncated = true
						nodes = nodes[:resultLimit]
						break
					}
				}
			}
			if len(nodes) >= resultLimit {
				break
			}
		}
	}

	if len(nodes) == 0 {
		return promptResult(fmt.Sprintf("'%s'과(와) 관련된 코드를 찾을 수 없습니다.", description)), nil
	}

	var sb strings.Builder
	sb.WriteString("## 관련 코드 검색 결과\n\n")
	if len(nodes) > resultLimit {
		nodes = nodes[:resultLimit]
		searchTruncated = true
	}

	appendPromptItems(&sb, nodes, func(n graph.Node) {
		sb.WriteString(fmt.Sprintf("- **%s** (%s, %s:%d-%d)\n",
			n.QualifiedName, n.Kind, n.FilePath, n.StartLine, n.EndLine))
	}, searchTruncated)

	querySvc := p.queryService()
	sb.WriteString("\n## 호출 그래프\n\n")
	pageOpts := query.QueryOptions{Limit: callGraphLimit}
	for _, n := range nodes {
		callersPage, _ := querySvc.CallersOfPage(ctx, n.ID, pageOpts)
		calleesPage, _ := querySvc.CalleesOfPage(ctx, n.ID, pageOpts)
		callers := callersPage.Nodes
		callees := calleesPage.Nodes

		if len(callers) > 0 || len(callees) > 0 {
			sb.WriteString(fmt.Sprintf("### %s\n", n.QualifiedName))
			for _, c := range callers {
				sb.WriteString(fmt.Sprintf("  ← 호출자: %s\n", c.QualifiedName))
			}
			if callersPage.TotalCount > len(callers) {
				appendPromptTruncation(&sb, len(callers))
			}
			for _, c := range callees {
				sb.WriteString(fmt.Sprintf("  → 호출 대상: %s\n", c.QualifiedName))
			}
			if calleesPage.TotalCount > len(callees) {
				appendPromptTruncation(&sb, len(callees))
			}
		}
	}

	return promptResult(sb.String()), nil
}

// onboardDeveloper builds a prompt introducing project scale and structure.
// @intent Quickly familiarizes new developers with graph scale, language distribution, communities, and large functions.
// @ensures Returns an onboarding statistics and structure summary prompt on success.
// @sideEffect Queries statistics, communities, and large functions from the database.
func (p *promptHandlers) onboardDeveloper(ctx context.Context, request mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	args := request.Params.Arguments
	ctx = requestctx.WithNamespace(ctx, resolvePromptNamespace(ctx, args))
	sectionLimit := promptLimitArg(args, promptSectionCap, promptSectionCap)
	stats, err := p.deps.Graph.Statistics.GraphStatistics(ctx)
	if err != nil {
		return nil, err
	}

	if stats.NodeCount == 0 {
		return promptResult("프로젝트가 비어있습니다. 먼저 소스 코드를 파싱하세요."), nil
	}

	// langStat is the prompt-local language aggregate used for deterministic rendering.
	// @intent sort language counts without exposing a transport type to application ports.
	type langStat struct {
		Language string
		Count    int64
	}
	langs := make([]langStat, 0, len(stats.NodesByLanguage))
	for language, count := range stats.NodesByLanguage {
		langs = append(langs, langStat{Language: language, Count: count})
	}
	sort.Slice(langs, func(i, j int) bool {
		if langs[i].Count != langs[j].Count {
			return langs[i].Count > langs[j].Count
		}
		return langs[i].Language < langs[j].Language
	})

	var sb strings.Builder
	sb.WriteString("## 프로젝트 온보딩 가이드\n\n### 기본 통계\n\n")
	sb.WriteString(fmt.Sprintf("- 전체 노드: %d\n", stats.NodeCount))
	sb.WriteString(fmt.Sprintf("- 전체 엣지: %d\n", stats.EdgeCount))
	sb.WriteString("\n### 언어 분포\n\n")
	langsTruncated := len(langs) > sectionLimit
	if langsTruncated {
		langs = langs[:sectionLimit]
	}
	for _, l := range langs {
		sb.WriteString(fmt.Sprintf("- %s: %d\n", l.Language, l.Count))
	}
	if langsTruncated {
		appendPromptTruncation(&sb, len(langs))
	}

	return promptResult(sb.String()), nil
}

// preMergeCheck builds a prompt covering merge-time risk, coverage, dead code, and large functions.
// @intent Consolidates merge-time check items into a single prompt to assist with pre-release verification.
// @param request Specifies the change analysis range using repo_root and base arguments.
// @requires ChangesGitClient must be configured to enable change-based checks.
// @ensures Returns a prompt including risk, coverage, dead code, and large function sections on success.
// @sideEffect Performs Git diff lookups and multiple database-based analysis queries.
// @see mcp.promptHandlers.reviewChanges
func (p *promptHandlers) preMergeCheck(ctx context.Context, request mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	args := request.Params.Arguments
	ctx = requestctx.WithNamespace(ctx, resolvePromptNamespace(ctx, args))
	riskLimit := promptLimitArg(args, promptHardCap, promptHardCap)
	repoRoot := args["repo_root"]
	base := args["base"]
	if base == "" {
		base = "HEAD~1"
	}

	validatedRepoRoot, err := validateRepoRootWithin(repoRoot, p.deps.Runtime.RepoRoot, promptNamespaceRoot(p.deps))
	if err != nil {
		return nil, err
	}
	repoRoot = validatedRepoRoot

	if p.deps.Analysis.Changes == nil {
		return promptResult("변경사항이 없습니다 (GitClient가 설정되지 않음)"), nil
	}

	risksPage, err := p.deps.Analysis.Changes.AnalyzePage(ctx, repoRoot, base, riskLimit, 0)
	if err != nil {
		return nil, trace.Wrap(err, "changes analyze")
	}
	risks := risksPage.Items

	var sb strings.Builder
	sb.WriteString("## 머지 전 체크리스트\n\n")

	sb.WriteString("### 리스크 분석\n\n")
	if len(risks) == 0 {
		sb.WriteString("변경사항이 없습니다.\n")
	} else {
		appendPromptItems(&sb, risks, func(r changes.RiskEntry) {
			sb.WriteString(fmt.Sprintf("- **%s** — 리스크 점수: %.1f\n", r.Node.QualifiedName, r.RiskScore))
		}, risksPage.HasMore)
	}

	return promptResult(sb.String()), nil
}
