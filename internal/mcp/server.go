// @index MCP server. Exposes code analysis capabilities to AI through multiple tools and 5 prompt templates.
package mcp

import (
	"log/slog"

	"github.com/mark3labs/mcp-go/server"
)

// NewServer creates and configures the MCP server with all tools and prompts.
// @intent Configures a server instance that exposes code graph features as MCP tools and prompts.
// @requires deps != nil
// @ensures The returned server is registered with MCP tools and prompts.
// @sideEffect Logs server metadata to the logger.
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

	log.Info("MCP server created", "name", "code-context-graph", "version", "1.0.0", "prompts", 4)

	p := &promptHandlers{deps: deps}
	registerPrompts(srv, p)

	return srv
}
