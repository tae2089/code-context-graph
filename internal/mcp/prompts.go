// @index MCP prompt handlers that compose graph queries into review, debug, onboarding, and pre-merge workflows.
package mcp

import (
	"context"
	"fmt"
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
)

// promptHandlers groups dependencies for MCP prompt generation.
// @intent Groups dependencies so prompt handlers can reuse the shared database and analyzers.
type promptHandlers struct {
	deps *Deps
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
	risks, err := chSvc.Analyze(ctx, repoRoot, base)
	if err != nil {
		return nil, trace.Wrap(err, "changes analyze")
	}

	if len(risks) == 0 {
		return promptResult("변경사항이 없습니다"), nil
	}

	var sb strings.Builder
	sb.WriteString("## 변경사항 리스크 분석\n\n")

	for _, r := range risks {
		sb.WriteString(fmt.Sprintf("- **%s** (%s:%d-%d) — 리스크 점수: %.1f, Hunk 수: %d\n",
			r.Node.QualifiedName, r.Node.FilePath, r.Node.StartLine, r.Node.EndLine,
			r.RiskScore, r.HunkCount))
	}

	sb.WriteString("\n## 테스트 커버리지 갭\n\n")

	var covAnalyzer CoverageAnalyzer
	if p.deps.CoverageAnalyzer != nil {
		covAnalyzer = p.deps.CoverageAnalyzer
	} else {
		covAnalyzer = coverage.New(p.deps.DB)
	}
	filesSeen := map[string]bool{}
	for _, r := range risks {
		if filesSeen[r.Node.FilePath] {
			continue
		}
		filesSeen[r.Node.FilePath] = true
		fc, err := covAnalyzer.ByFile(ctx, r.Node.FilePath)
		if err != nil {
			continue
		}
		sb.WriteString(fmt.Sprintf("- %s: 테스트 %d/%d (%.0f%%)\n",
			fc.FilePath, fc.Tested, fc.Total, fc.Ratio*100))
	}

	return promptResult(sb.String()), nil
}

// architectureMap builds a prompt summarizing communities and coupling.
// @intent Aids in understanding module structure by providing a full architectural overview via a natural language prompt.
// @ensures Returns a prompt containing a list of communities and inter-module coupling on success.
// @sideEffect Queries community and coupling information from the database.
func (p *promptHandlers) architectureMap(ctx context.Context, request mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	ctx = ctxns.WithNamespace(ctx, resolvePromptNamespace(ctx, request.Params.Arguments))
	ns := ctxns.FromContext(ctx)
	var communities []model.Community
	query := p.deps.DB.WithContext(ctx).Where("namespace = ?", ns)
	if err := query.Find(&communities).Error; err != nil {
		return nil, trace.Wrap(err, "query communities")
	}

	if len(communities) == 0 {
		return promptResult("커뮤니티가 없습니다. 먼저 `community rebuild` 명령으로 커뮤니티를 생성하세요."), nil
	}

	var sb strings.Builder
	sb.WriteString("## 아키텍처 맵\n\n### 커뮤니티 목록\n\n")

	log := p.deps.Logger
	for _, c := range communities {
		var memberCount int64
		memberQ := p.deps.DB.WithContext(ctx).Model(&model.CommunityMembership{}).
			Joins("JOIN communities ON communities.id = community_memberships.community_id").
			Where("community_id = ?", c.ID).
			Where("communities.namespace = ?", ns)
		if err := memberQ.Count(&memberCount).Error; err != nil {
			log.Warn("count community members failed", "community", c.ID, trace.SlogError(err))
		}
		sb.WriteString(fmt.Sprintf("- **%s** (전략: %s, 멤버: %d)\n", c.Label, c.Strategy, memberCount))
	}

	var coupAnalyzer CouplingAnalyzer
	if p.deps.CouplingAnalyzer != nil {
		coupAnalyzer = p.deps.CouplingAnalyzer
	} else {
		coupAnalyzer = coupling.New(p.deps.DB)
	}
	pairs, err := coupAnalyzer.Analyze(ctx)
	if err != nil {
		return nil, trace.Wrap(err, "coupling analyze")
	}

	if len(pairs) > 0 {
		sb.WriteString("\n### 모듈 간 결합도\n\n")
		for _, cp := range pairs {
			sb.WriteString(fmt.Sprintf("- %s → %s: 결합도 %.2f (%d edges)\n",
				cp.FromCommunity, cp.ToCommunity, cp.Strength, cp.EdgeCount))
		}
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

	if p.deps.SearchBackend == nil || p.deps.DB == nil {
		return promptResult("검색 백엔드가 설정되지 않았습니다."), nil
	}

	nodes, err := p.deps.SearchBackend.Query(ctx, p.deps.DB, description, 10)
	if err != nil {
		return nil, trace.Wrap(err, "search")
	}

	if len(nodes) == 0 {
		seen := map[uint]bool{}
		for _, token := range strings.Fields(description) {
			tokenNodes, err := p.deps.SearchBackend.Query(ctx, p.deps.DB, token, 10)
			if err != nil {
				continue
			}
			for _, n := range tokenNodes {
				if !seen[n.ID] {
					nodes = append(nodes, n)
					seen[n.ID] = true
				}
			}
		}
	}

	if len(nodes) == 0 {
		return promptResult(fmt.Sprintf("'%s'과(와) 관련된 코드를 찾을 수 없습니다.", description)), nil
	}

	var sb strings.Builder
	sb.WriteString("## 관련 코드 검색 결과\n\n")

	for _, n := range nodes {
		sb.WriteString(fmt.Sprintf("- **%s** (%s, %s:%d-%d)\n",
			n.QualifiedName, n.Kind, n.FilePath, n.StartLine, n.EndLine))
	}

	var querySvc QueryService
	if p.deps.QueryService != nil {
		querySvc = p.deps.QueryService
	} else {
		querySvc = query.New(p.deps.DB)
	}
	sb.WriteString("\n## 호출 그래프\n\n")
	for _, n := range nodes {
		callers, _ := querySvc.CallersOf(ctx, n.ID)
		callees, _ := querySvc.CalleesOf(ctx, n.ID)

		if len(callers) > 0 || len(callees) > 0 {
			sb.WriteString(fmt.Sprintf("### %s\n", n.QualifiedName))
			for _, c := range callers {
				sb.WriteString(fmt.Sprintf("  ← 호출자: %s\n", c.QualifiedName))
			}
			for _, c := range callees {
				sb.WriteString(fmt.Sprintf("  → 호출 대상: %s\n", c.QualifiedName))
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
	ctx = ctxns.WithNamespace(ctx, resolvePromptNamespace(ctx, request.Params.Arguments))
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
		Scan(&langs).Error; err != nil {
		log.Warn("scan language stats failed", trace.SlogError(err))
	}

	var sb strings.Builder
	sb.WriteString("## 프로젝트 온보딩 가이드\n\n### 기본 통계\n\n")
	sb.WriteString(fmt.Sprintf("- 전체 노드: %d\n", nodeCount))
	sb.WriteString(fmt.Sprintf("- 전체 엣지: %d\n", edgeCount))
	sb.WriteString("\n### 언어 분포\n\n")
	for _, l := range langs {
		sb.WriteString(fmt.Sprintf("- %s: %d\n", l.Language, l.Count))
	}

	var communities []model.Community
	commQ := p.deps.DB.WithContext(ctx).Where("namespace = ?", ns)
	if err := commQ.Find(&communities).Error; err != nil {
		log.Warn("find communities failed", trace.SlogError(err))
	}
	if len(communities) > 0 {
		sb.WriteString("\n### 커뮤니티 구조\n\n")
		for _, c := range communities {
			sb.WriteString(fmt.Sprintf("- %s (%s)\n", c.Label, c.Strategy))
		}
	}

	var lfAnalyzer LargefuncAnalyzer
	if p.deps.LargefuncAnalyzer != nil {
		lfAnalyzer = p.deps.LargefuncAnalyzer
	} else {
		lfAnalyzer = largefunc.New(p.deps.DB)
	}
	largeFuncs, err := lfAnalyzer.Find(ctx, 50)
	if err != nil {
		log.Warn("find large functions failed", trace.SlogError(err))
	}
	if len(largeFuncs) > 0 {
		sb.WriteString("\n### 대형 함수 (50줄 초과)\n\n")
		for _, f := range largeFuncs {
			lines := f.EndLine - f.StartLine + 1
			sb.WriteString(fmt.Sprintf("- %s (%s:%d-%d, %d줄)\n",
				f.QualifiedName, f.FilePath, f.StartLine, f.EndLine, lines))
		}
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
	risks, err := chSvc.Analyze(ctx, repoRoot, base)
	if err != nil {
		return nil, trace.Wrap(err, "changes analyze")
	}

	var sb strings.Builder
	sb.WriteString("## 머지 전 체크리스트\n\n")

	sb.WriteString("### 리스크 분석\n\n")
	if len(risks) == 0 {
		sb.WriteString("변경사항이 없습니다.\n")
	} else {
		for _, r := range risks {
			sb.WriteString(fmt.Sprintf("- **%s** — 리스크 점수: %.1f\n", r.Node.QualifiedName, r.RiskScore))
		}
	}

	sb.WriteString("\n### 커버리지\n\n")
	var covAnalyzer2 CoverageAnalyzer
	if p.deps.CoverageAnalyzer != nil {
		covAnalyzer2 = p.deps.CoverageAnalyzer
	} else {
		covAnalyzer2 = coverage.New(p.deps.DB)
	}
	filesSeen := map[string]bool{}
	for _, r := range risks {
		if filesSeen[r.Node.FilePath] {
			continue
		}
		filesSeen[r.Node.FilePath] = true
		fc, err := covAnalyzer2.ByFile(ctx, r.Node.FilePath)
		if err != nil {
			continue
		}
		sb.WriteString(fmt.Sprintf("- %s: %d/%d (%.0f%%)\n",
			fc.FilePath, fc.Tested, fc.Total, fc.Ratio*100))
	}
	if len(risks) == 0 {
		sb.WriteString("변경사항이 없습니다.\n")
	}

	sb.WriteString("\n### 미사용 코드\n\n")
	var dcAnalyzer DeadcodeAnalyzer
	if p.deps.DeadcodeAnalyzer != nil {
		dcAnalyzer = p.deps.DeadcodeAnalyzer
	} else {
		dcAnalyzer = deadcode.New(p.deps.DB)
	}
	deadNodes, err := dcAnalyzer.Find(ctx, deadcode.Options{})
	if err != nil {
		log.Warn("find dead code failed", trace.SlogError(err))
	}
	if len(deadNodes) > 0 {
		for _, n := range deadNodes {
			sb.WriteString(fmt.Sprintf("- 미사용: %s (%s)\n", n.QualifiedName, n.FilePath))
		}
	} else {
		sb.WriteString("미사용 코드 없음\n")
	}

	sb.WriteString("\n### 대형 함수\n\n")
	var lfAnalyzer2 LargefuncAnalyzer
	if p.deps.LargefuncAnalyzer != nil {
		lfAnalyzer2 = p.deps.LargefuncAnalyzer
	} else {
		lfAnalyzer2 = largefunc.New(p.deps.DB)
	}
	var largeFuncs []model.Node
	largeFuncs, err = lfAnalyzer2.Find(ctx, 50)
	if err != nil {
		log.Warn("find large functions failed", trace.SlogError(err))
	}
	if len(largeFuncs) > 0 {
		for _, f := range largeFuncs {
			lines := f.EndLine - f.StartLine + 1
			sb.WriteString(fmt.Sprintf("- %s (%d줄)\n", f.QualifiedName, lines))
		}
	} else {
		sb.WriteString("대형 함수 없음\n")
	}

	return promptResult(sb.String()), nil
}
