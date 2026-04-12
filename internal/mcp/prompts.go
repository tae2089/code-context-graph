package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/imtaebin/code-context-graph/internal/analysis/changes"
	"github.com/imtaebin/code-context-graph/internal/analysis/coupling"
	"github.com/imtaebin/code-context-graph/internal/analysis/coverage"
	"github.com/imtaebin/code-context-graph/internal/analysis/deadcode"
	"github.com/imtaebin/code-context-graph/internal/analysis/largefunc"
	"github.com/imtaebin/code-context-graph/internal/analysis/query"
	"github.com/imtaebin/code-context-graph/internal/model"
)

type promptHandlers struct {
	deps *Deps
}

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

func (p *promptHandlers) reviewChanges(ctx context.Context, request mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	args := request.Params.Arguments
	repoRoot := args["repo_root"]
	base := args["base"]
	if base == "" {
		base = "HEAD~1"
	}

	if p.deps.ChangesGitClient == nil {
		return promptResult("변경사항이 없습니다 (GitClient가 설정되지 않음)"), nil
	}

	chSvc := changes.New(p.deps.DB, p.deps.ChangesGitClient)
	risks, err := chSvc.Analyze(ctx, repoRoot, base)
	if err != nil {
		return nil, fmt.Errorf("changes analyze: %w", err)
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

func (p *promptHandlers) architectureMap(ctx context.Context, request mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	var communities []model.Community
	if err := p.deps.DB.WithContext(ctx).Find(&communities).Error; err != nil {
		return nil, fmt.Errorf("query communities: %w", err)
	}

	if len(communities) == 0 {
		return promptResult("커뮤니티가 없습니다. 먼저 `community rebuild` 명령으로 커뮤니티를 생성하세요."), nil
	}

	var sb strings.Builder
	sb.WriteString("## 아키텍처 맵\n\n### 커뮤니티 목록\n\n")

	for _, c := range communities {
		var memberCount int64
		p.deps.DB.WithContext(ctx).Model(&model.CommunityMembership{}).
			Where("community_id = ?", c.ID).Count(&memberCount)
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
		return nil, fmt.Errorf("coupling analyze: %w", err)
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

func (p *promptHandlers) debugIssue(ctx context.Context, request mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	args := request.Params.Arguments
	description := args["description"]

	if p.deps.SearchBackend == nil || p.deps.DB == nil {
		return promptResult("검색 백엔드가 설정되지 않았습니다."), nil
	}

	nodes, err := p.deps.SearchBackend.Query(ctx, p.deps.DB, description, 10)
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}

	if len(nodes) == 0 {
		for _, token := range strings.Fields(description) {
			tokenNodes, err := p.deps.SearchBackend.Query(ctx, p.deps.DB, token, 10)
			if err != nil {
				continue
			}
			seen := map[uint]bool{}
			for _, n := range nodes {
				seen[n.ID] = true
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

func (p *promptHandlers) onboardDeveloper(ctx context.Context, request mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	var nodeCount int64
	if err := p.deps.DB.WithContext(ctx).Model(&model.Node{}).Count(&nodeCount).Error; err != nil {
		return nil, fmt.Errorf("count nodes: %w", err)
	}

	if nodeCount == 0 {
		return promptResult("프로젝트가 비어있습니다. 먼저 소스 코드를 파싱하세요."), nil
	}

	var edgeCount int64
	p.deps.DB.WithContext(ctx).Model(&model.Edge{}).Count(&edgeCount)

	type langStat struct {
		Language string
		Count    int64
	}
	var langs []langStat
	p.deps.DB.WithContext(ctx).Model(&model.Node{}).
		Select("language, COUNT(*) as count").
		Group("language").
		Having("language != ''").
		Scan(&langs)

	var sb strings.Builder
	sb.WriteString("## 프로젝트 온보딩 가이드\n\n### 기본 통계\n\n")
	sb.WriteString(fmt.Sprintf("- 전체 노드: %d\n", nodeCount))
	sb.WriteString(fmt.Sprintf("- 전체 엣지: %d\n", edgeCount))
	sb.WriteString("\n### 언어 분포\n\n")
	for _, l := range langs {
		sb.WriteString(fmt.Sprintf("- %s: %d\n", l.Language, l.Count))
	}

	var communities []model.Community
	p.deps.DB.WithContext(ctx).Find(&communities)
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
	largeFuncs, _ := lfAnalyzer.Find(ctx, 50)
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

func (p *promptHandlers) preMergeCheck(ctx context.Context, request mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	args := request.Params.Arguments
	repoRoot := args["repo_root"]
	base := args["base"]
	if base == "" {
		base = "HEAD~1"
	}

	if p.deps.ChangesGitClient == nil {
		return promptResult("변경사항이 없습니다 (GitClient가 설정되지 않음)"), nil
	}

	chSvc := changes.New(p.deps.DB, p.deps.ChangesGitClient)
	risks, err := chSvc.Analyze(ctx, repoRoot, base)
	if err != nil {
		return nil, fmt.Errorf("changes analyze: %w", err)
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
	deadNodes, _ := dcAnalyzer.Find(ctx, deadcode.Options{})
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
	largeFuncs, _ := lfAnalyzer2.Find(ctx, 50)
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
