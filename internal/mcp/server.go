// @index MCP 서버. 다수의 도구와 5개 프롬프트 템플릿을 통해 코드 분석 기능을 AI에게 노출한다.
package mcp

import (
	"log/slog"

	"github.com/mark3labs/mcp-go/server"
)

// NewServer creates and configures the MCP server with all tools and prompts.
// @intent 코드 그래프 기능을 MCP 도구와 프롬프트로 노출하는 서버 인스턴스를 구성한다.
// @requires deps != nil
// @ensures 반환 서버에는 MCP 도구와 프롬프트가 등록된다.
// @sideEffect 서버 메타데이터를 로거에 기록한다.
// @see mcp.Deps
func NewServer(deps *Deps) *server.MCPServer {
	log := deps.Logger
	if log == nil {
		log = slog.Default()
	}

	srv := server.NewMCPServer(
		"code-context-graph",
		"1.0.0",
		server.WithToolCapabilities(true),
		server.WithPromptCapabilities(true),
	)

	h := &handlers{deps: deps, cache: deps.Cache}
	registerTools(srv, h)

	log.Info("MCP server created", "name", "code-context-graph", "version", "1.0.0", "prompts", 5)

	p := &promptHandlers{deps: deps}
	registerPrompts(srv, p)

	return srv
}
