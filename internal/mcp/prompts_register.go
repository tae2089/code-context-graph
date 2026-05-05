// @index Prompt registration for curated MCP workflows.
package mcp

import (
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// registerPrompts exposes guided prompt entry points on top of the MCP tool surface.
// @intent package common review, onboarding, and debugging flows into reusable server prompts.
// @sideEffect registers prompt descriptors and handlers on the running MCP server.
// @mutates srv
func registerPrompts(srv *server.MCPServer, p *promptHandlers) {
	srv.AddPrompts(
		server.ServerPrompt{
			Prompt: mcp.NewPrompt("review_changes",
				mcp.WithPromptDescription("변경사항 리뷰: 리스크 분석 및 테스트 커버리지 갭 확인"),
				mcp.WithArgument("repo_root", mcp.ArgumentDescription("Git 저장소 루트 경로"), mcp.RequiredArgument()),
				mcp.WithArgument("base", mcp.ArgumentDescription("비교 기준 커밋 (기본: HEAD~1)")),
				mcp.WithArgument("namespace", mcp.ArgumentDescription("조회할 namespace (선택)")),
				mcp.WithArgument("workspace", mcp.ArgumentDescription("namespace의 deprecated alias (선택)")),
			),
			Handler: p.reviewChanges,
		},
		server.ServerPrompt{
			Prompt: mcp.NewPrompt("architecture_map",
				mcp.WithPromptDescription("아키텍처 맵: 커뮤니티 구조 및 모듈 간 결합도 분석"),
				mcp.WithArgument("namespace", mcp.ArgumentDescription("조회할 namespace (선택)")),
				mcp.WithArgument("workspace", mcp.ArgumentDescription("namespace의 deprecated alias (선택)")),
			),
			Handler: p.architectureMap,
		},
		server.ServerPrompt{
			Prompt: mcp.NewPrompt("debug_issue",
				mcp.WithPromptDescription("이슈 디버깅: 관련 코드 검색 및 호출 그래프 분석"),
				mcp.WithArgument("description", mcp.ArgumentDescription("이슈 설명"), mcp.RequiredArgument()),
				mcp.WithArgument("namespace", mcp.ArgumentDescription("조회할 namespace (선택)")),
				mcp.WithArgument("workspace", mcp.ArgumentDescription("namespace의 deprecated alias (선택)")),
			),
			Handler: p.debugIssue,
		},
		server.ServerPrompt{
			Prompt: mcp.NewPrompt("onboard_developer",
				mcp.WithPromptDescription("온보딩: 프로젝트 통계, 커뮤니티 구조, 대형 함수 요약"),
				mcp.WithArgument("namespace", mcp.ArgumentDescription("조회할 namespace (선택)")),
				mcp.WithArgument("workspace", mcp.ArgumentDescription("namespace의 deprecated alias (선택)")),
			),
			Handler: p.onboardDeveloper,
		},
		server.ServerPrompt{
			Prompt: mcp.NewPrompt("pre_merge_check",
				mcp.WithPromptDescription("머지 전 체크: 리스크, 커버리지, 미사용 코드, 대형 함수 확인"),
				mcp.WithArgument("repo_root", mcp.ArgumentDescription("Git 저장소 루트 경로"), mcp.RequiredArgument()),
				mcp.WithArgument("base", mcp.ArgumentDescription("비교 기준 커밋 (기본: HEAD~1)")),
				mcp.WithArgument("namespace", mcp.ArgumentDescription("조회할 namespace (선택)")),
				mcp.WithArgument("workspace", mcp.ArgumentDescription("namespace의 deprecated alias (선택)")),
			),
			Handler: p.preMergeCheck,
		},
	)
}
