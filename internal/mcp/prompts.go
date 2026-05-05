// @index MCP prompt handlers that compose graph queries into review, debug, onboarding, and pre-merge workflows.
package mcp

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/tae2089/trace"

	"github.com/tae2089/code-context-graph/internal/analysis/changes"
	"github.com/tae2089/code-context-graph/internal/analysis/coupling"
	"github.com/tae2089/code-context-graph/internal/analysis/coverage"
	"github.com/tae2089/code-context-graph/internal/analysis/deadcode"
	"github.com/tae2089/code-context-graph/internal/analysis/largefunc"
	"github.com/tae2089/code-context-graph/internal/analysis/query"
	"github.com/tae2089/code-context-graph/internal/ctxns"
	"github.com/tae2089/code-context-graph/internal/model"
	"github.com/tae2089/code-context-graph/internal/paging"
)

const (
	promptHardCap      = 20
	promptSectionCap   = 10
	promptCallGraphCap = 10
)

// @intent build a bounded page request for prompt-only analyzer reads.
// @domainRule prompt handlers never request more than their hard cap from paged services.
func promptPageRequest(limit int) paging.Request {
	return paging.Request{Limit: limit}
}

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

// @intent preserve risk ordering while collapsing repeated files into a stable coverage worklist.
func uniqueRiskFiles(risks []changes.RiskEntry) []string {
	filesSeen := make(map[string]bool, len(risks))
	files := make([]string, 0, len(risks))
	for _, r := range risks {
		if filesSeen[r.Node.FilePath] {
			continue
		}
		filesSeen[r.Node.FilePath] = true
		files = append(files, r.Node.FilePath)
	}
	return files
}

// promptHandlers groups dependencies for MCP prompt generation.
// @intent Groups dependencies so prompt handlers can reuse the shared database and analyzers.
type promptHandlers struct {
	deps *Deps
}

// @intent resolve the coverage analyzer dependency with a DB-backed default.
func (p *promptHandlers) coverageAnalyzer() CoverageAnalyzer {
	if p.deps.CoverageAnalyzer != nil {
		return p.deps.CoverageAnalyzer
	}
	return coverage.New(p.deps.DB)
}

// @intent resolve the coupling analyzer dependency with a DB-backed default.
func (p *promptHandlers) couplingAnalyzer() CouplingAnalyzer {
	if p.deps.CouplingAnalyzer != nil {
		return p.deps.CouplingAnalyzer
	}
	return coupling.New(p.deps.DB)
}

// @intent resolve the query service dependency with a DB-backed default.
func (p *promptHandlers) queryService() QueryService {
	if p.deps.QueryService != nil {
		return p.deps.QueryService
	}
	return query.New(p.deps.DB)
}

// @intent resolve the large-function analyzer dependency with a DB-backed default.
func (p *promptHandlers) largefuncAnalyzer() LargefuncAnalyzer {
	if p.deps.LargefuncAnalyzer != nil {
		return p.deps.LargefuncAnalyzer
	}
	return largefunc.New(p.deps.DB)
}

// @intent resolve the dead-code analyzer dependency with a DB-backed default.
func (p *promptHandlers) deadcodeAnalyzer() DeadcodeAnalyzer {
	if p.deps.DeadcodeAnalyzer != nil {
		return p.deps.DeadcodeAnalyzer
	}
	return deadcode.New(p.deps.DB)
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

// @intent pick the namespace for a prompt invocation, preferring an explicit argument over the workspace fallback.
func resolvePromptNamespace(ctx context.Context, args map[string]string) string {
	if namespace := args["namespace"]; namespace != "" {
		return ctxns.Normalize(namespace)
	}
	return resolveNamespace(ctx, args["workspace"])
}

// @intent resolve the on-disk root used to validate prompt repo paths, falling back through namespace and workspace roots.
func promptNamespaceRoot(deps *Deps) string {
	if deps.NamespaceRoot != "" {
		return deps.NamespaceRoot
	}
	if deps.WorkspaceRoot != "" {
		return deps.WorkspaceRoot
	}
	return "workspaces"
}

// reviewChanges builds a prompt summarizing change risk and coverage gaps.
// @intent Provides a single view of high-risk functions and test gaps before reviewing changes.
// @param request Defines the Git comparison range using repo_root and base arguments.
// @requires ChangesGitClient must be configured to perform actual change analysis.
// @ensures Returns a prompt including risk analysis and coverage summary on success.
// @sideEffect Performs Git diff lookups and database-based coverage queries.
// @see mcp.promptHandlers.preMergeCheck
func (p *promptHandlers) reviewChanges(ctx context.Context, request mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	args := request.Params.Arguments
	ctx = ctxns.WithNamespace(ctx, resolvePromptNamespace(ctx, args))
	riskLimit := promptLimitArg(args, promptHardCap, promptHardCap)
	coverageLimit := promptLimitArg(args, promptSectionCap, promptSectionCap)
	repoRoot := args["repo_root"]
	base := args["base"]
	if base == "" {
		base = "HEAD~1"
	}

	validatedRepoRoot, err := validateRepoRootWithin(repoRoot, p.deps.RepoRoot, promptNamespaceRoot(p.deps))
	if err != nil {
		return nil, err
	}
	repoRoot = validatedRepoRoot

	if p.deps.ChangesGitClient == nil {
		return promptResult("변경사항이 없습니다 (GitClient가 설정되지 않음)"), nil
	}

	chSvc := changes.New(p.deps.DB, p.deps.ChangesGitClient)
	risksPage, err := chSvc.AnalyzePage(ctx, repoRoot, base, promptPageRequest(riskLimit))
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
	}, risksPage.Pagination.HasMore)

	sb.WriteString("\n## 테스트 커버리지 갭\n\n")

	covAnalyzer := p.coverageAnalyzer()
	files := uniqueRiskFiles(risks)
	coverageFiles := files
	coverageTruncated := len(files) > coverageLimit
	if coverageTruncated {
		coverageFiles = files[:coverageLimit]
	}
	appendPromptItems(&sb, coverageFiles, func(filePath string) {
		fc, err := covAnalyzer.ByFile(ctx, filePath)
		if err != nil {
			return
		}
		sb.WriteString(fmt.Sprintf("- %s: 테스트 %d/%d (%.0f%%)\n",
			fc.FilePath, fc.Tested, fc.Total, fc.Ratio*100))
	}, coverageTruncated)

	return promptResult(sb.String()), nil
}

// architectureMap builds a prompt summarizing communities and coupling.
// @intent Aids in understanding module structure by providing a full architectural overview via a natural language prompt.
// @ensures Returns a prompt containing a list of communities and inter-module coupling on success.
// @sideEffect Queries community and coupling information from the database.
func (p *promptHandlers) architectureMap(ctx context.Context, request mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	args := request.Params.Arguments
	ctx = ctxns.WithNamespace(ctx, resolvePromptNamespace(ctx, args))
	sectionLimit := promptLimitArg(args, promptSectionCap, promptSectionCap)
	ns := ctxns.FromContext(ctx)
	var communities []model.Community
	query := p.deps.DB.WithContext(ctx).Where("namespace = ?", ns).Order("id ASC").Limit(sectionLimit + 1)
	if err := query.Find(&communities).Error; err != nil {
		return nil, trace.Wrap(err, "query communities")
	}

	if len(communities) == 0 {
		return promptResult("커뮤니티가 없습니다. 먼저 `community rebuild` 명령으로 커뮤니티를 생성하세요."), nil
	}

	var sb strings.Builder
	sb.WriteString("## 아키텍처 맵\n\n### 커뮤니티 목록\n\n")

	log := p.deps.Logger
	communityTruncated := len(communities) > sectionLimit
	if communityTruncated {
		communities = communities[:sectionLimit]
	}
	appendPromptItems(&sb, communities, func(c model.Community) {
		var memberCount int64
		memberQ := p.deps.DB.WithContext(ctx).Model(&model.CommunityMembership{}).
			Joins("JOIN communities ON communities.id = community_memberships.community_id").
			Where("community_id = ?", c.ID).
			Where("communities.namespace = ?", ns)
		if err := memberQ.Count(&memberCount).Error; err != nil {
			log.Warn("count community members failed", "community", c.ID, trace.SlogError(err))
		}
		sb.WriteString(fmt.Sprintf("- **%s** (전략: %s, 멤버: %d)\n", c.Label, c.Strategy, memberCount))
	}, communityTruncated)

	coupAnalyzer := p.couplingAnalyzer()
	pairsPage, err := coupAnalyzer.AnalyzePage(ctx, promptPageRequest(sectionLimit))
	if err != nil {
		return nil, trace.Wrap(err, "coupling analyze")
	}
	pairs := pairsPage.Items

	if len(pairs) > 0 {
		sb.WriteString("\n### 모듈 간 결합도\n\n")
		appendPromptItems(&sb, pairs, func(cp coupling.CouplingPair) {
			sb.WriteString(fmt.Sprintf("- %s → %s: 결합도 %.2f (%d edges)\n",
				cp.FromCommunity, cp.ToCommunity, cp.Strength, cp.EdgeCount))
		}, pairsPage.Pagination.HasMore)
	}

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
	ctx = ctxns.WithNamespace(ctx, resolvePromptNamespace(ctx, args))
	description := args["description"]
	resultLimit := promptLimitArg(args, promptHardCap, promptHardCap)
	callGraphLimit := promptLimitArg(args, promptCallGraphCap, promptCallGraphCap)
	searchFetchLimit := resultLimit + 1

	if p.deps.SearchBackend == nil || p.deps.DB == nil {
		return promptResult("검색 백엔드가 설정되지 않았습니다."), nil
	}

	nodes, err := p.deps.SearchBackend.Query(ctx, p.deps.DB, description, searchFetchLimit)
	if err != nil {
		return nil, trace.Wrap(err, "search")
	}
	searchTruncated := len(nodes) > resultLimit

	if len(nodes) == 0 {
		seen := map[uint]bool{}
		for _, token := range strings.Fields(description) {
			tokenNodes, err := p.deps.SearchBackend.Query(ctx, p.deps.DB, token, searchFetchLimit)
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

	appendPromptItems(&sb, nodes, func(n model.Node) {
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
	ctx = ctxns.WithNamespace(ctx, resolvePromptNamespace(ctx, args))
	sectionLimit := promptLimitArg(args, promptSectionCap, promptSectionCap)
	log := p.deps.Logger
	ns := ctxns.FromContext(ctx)
	var nodeCount int64
	nodeQ := p.deps.DB.WithContext(ctx).Model(&model.Node{}).Where("namespace = ?", ns)
	if err := nodeQ.Count(&nodeCount).Error; err != nil {
		return nil, trace.Wrap(err, "count nodes")
	}

	if nodeCount == 0 {
		return promptResult("프로젝트가 비어있습니다. 먼저 소스 코드를 파싱하세요."), nil
	}

	var edgeCount int64
	edgeQ := p.deps.DB.WithContext(ctx).Model(&model.Edge{}).Where("namespace = ?", ns)
	if err := edgeQ.Count(&edgeCount).Error; err != nil {
		log.Warn("count edges failed", trace.SlogError(err))
	}

	// langStat stores grouped node counts by language.
	// @intent Holds aggregation results for building the language distribution section of the onboarding prompt.
	type langStat struct {
		Language string
		Count    int64
	}
	var langs []langStat
	langQ := p.deps.DB.WithContext(ctx).Model(&model.Node{}).Where("namespace = ?", ns)
	if err := langQ.
		Select("language, COUNT(*) as count").
		Group("language").
		Having("language != ''").
		Order("count DESC").
		Order("language ASC").
		Limit(sectionLimit + 1).
		Scan(&langs).Error; err != nil {
		log.Warn("scan language stats failed", trace.SlogError(err))
	}

	var sb strings.Builder
	sb.WriteString("## 프로젝트 온보딩 가이드\n\n### 기본 통계\n\n")
	sb.WriteString(fmt.Sprintf("- 전체 노드: %d\n", nodeCount))
	sb.WriteString(fmt.Sprintf("- 전체 엣지: %d\n", edgeCount))
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

	var communities []model.Community
	commQ := p.deps.DB.WithContext(ctx).Where("namespace = ?", ns).Order("id ASC").Limit(sectionLimit + 1)
	if err := commQ.Find(&communities).Error; err != nil {
		log.Warn("find communities failed", trace.SlogError(err))
	}
	communitiesTruncated := len(communities) > sectionLimit
	if communitiesTruncated {
		communities = communities[:sectionLimit]
	}
	if len(communities) > 0 {
		sb.WriteString("\n### 커뮤니티 구조\n\n")
		for _, c := range communities {
			sb.WriteString(fmt.Sprintf("- %s (%s)\n", c.Label, c.Strategy))
		}
		if communitiesTruncated {
			appendPromptTruncation(&sb, len(communities))
		}
	}

	lfAnalyzer := p.largefuncAnalyzer()
	largeFuncsPage, err := lfAnalyzer.FindPage(ctx, largefunc.Options{Threshold: 50, Page: promptPageRequest(sectionLimit)})
	if err != nil {
		log.Warn("find large functions failed", trace.SlogError(err))
	}
	largeFuncs := largeFuncsPage.Items
	if len(largeFuncs) > 0 {
		sb.WriteString("\n### 대형 함수 (50줄 초과)\n\n")
		appendPromptItems(&sb, largeFuncs, func(f model.Node) {
			lines := f.EndLine - f.StartLine + 1
			sb.WriteString(fmt.Sprintf("- %s (%s:%d-%d, %d줄)\n",
				f.QualifiedName, f.FilePath, f.StartLine, f.EndLine, lines))
		}, largeFuncsPage.Pagination.HasMore)
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
	log := p.deps.Logger
	args := request.Params.Arguments
	ctx = ctxns.WithNamespace(ctx, resolvePromptNamespace(ctx, args))
	riskLimit := promptLimitArg(args, promptHardCap, promptHardCap)
	sectionLimit := promptLimitArg(args, promptSectionCap, promptSectionCap)
	repoRoot := args["repo_root"]
	base := args["base"]
	if base == "" {
		base = "HEAD~1"
	}

	validatedRepoRoot, err := validateRepoRootWithin(repoRoot, p.deps.RepoRoot, promptNamespaceRoot(p.deps))
	if err != nil {
		return nil, err
	}
	repoRoot = validatedRepoRoot

	if p.deps.ChangesGitClient == nil {
		return promptResult("변경사항이 없습니다 (GitClient가 설정되지 않음)"), nil
	}

	chSvc := changes.New(p.deps.DB, p.deps.ChangesGitClient)
	risksPage, err := chSvc.AnalyzePage(ctx, repoRoot, base, promptPageRequest(riskLimit))
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
		}, risksPage.Pagination.HasMore)
	}

	sb.WriteString("\n### 커버리지\n\n")
	covAnalyzer2 := p.coverageAnalyzer()
	files := uniqueRiskFiles(risks)
	coverageFiles := files
	coverageTruncated := len(files) > sectionLimit
	if coverageTruncated {
		coverageFiles = files[:sectionLimit]
	}
	appendPromptItems(&sb, coverageFiles, func(filePath string) {
		fc, err := covAnalyzer2.ByFile(ctx, filePath)
		if err != nil {
			return
		}
		sb.WriteString(fmt.Sprintf("- %s: %d/%d (%.0f%%)\n",
			fc.FilePath, fc.Tested, fc.Total, fc.Ratio*100))
	}, false)
	if len(risks) == 0 {
		sb.WriteString("변경사항이 없습니다.\n")
	} else if coverageTruncated {
		appendPromptTruncation(&sb, len(coverageFiles))
	}

	sb.WriteString("\n### 미사용 코드\n\n")
	dcAnalyzer := p.deadcodeAnalyzer()
	deadPage, err := dcAnalyzer.FindPage(ctx, deadcode.Options{Page: promptPageRequest(sectionLimit)})
	if err != nil {
		log.Warn("find dead code failed", trace.SlogError(err))
	}
	deadNodes := deadPage.Items
	if len(deadNodes) > 0 {
		appendPromptItems(&sb, deadNodes, func(n model.Node) {
			sb.WriteString(fmt.Sprintf("- 미사용: %s (%s)\n", n.QualifiedName, n.FilePath))
		}, deadPage.Pagination.HasMore)
	} else {
		sb.WriteString("미사용 코드 없음\n")
	}

	sb.WriteString("\n### 대형 함수\n\n")
	lfAnalyzer2 := p.largefuncAnalyzer()
	var largeFuncs []model.Node
	largeFuncsPage, err := lfAnalyzer2.FindPage(ctx, largefunc.Options{Threshold: 50, Page: promptPageRequest(sectionLimit)})
	if err != nil {
		log.Warn("find large functions failed", trace.SlogError(err))
	}
	largeFuncs = largeFuncsPage.Items
	if len(largeFuncs) > 0 {
		appendPromptItems(&sb, largeFuncs, func(f model.Node) {
			lines := f.EndLine - f.StartLine + 1
			sb.WriteString(fmt.Sprintf("- %s (%d줄)\n", f.QualifiedName, lines))
		}, largeFuncsPage.Pagination.HasMore)
	} else {
		sb.WriteString("대형 함수 없음\n")
	}

	return promptResult(sb.String()), nil
}
