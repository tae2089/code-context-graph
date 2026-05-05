// @index Top-level MCP tool registration orchestration.
package mcp

import "github.com/mark3labs/mcp-go/server"

// registerTools adds every MCP tool group to the running server.
// @intent centralize tool registration order so new tool families plug into one startup path.
// @sideEffect registers all tool groups on the running MCP server.
// @mutates srv
func registerTools(srv *server.MCPServer, h *handlers) {
	var tools []server.ServerTool
	tools = append(tools, parseTools(h)...)
	tools = append(tools, postprocessTools(h)...)
	tools = append(tools, queryTools(h)...)
	tools = append(tools, analysisTools(h)...)
	tools = append(tools, graphTools(h)...)
	tools = append(tools, docsTools(h)...)
	tools = append(tools, workspaceTools(h)...)
	tools = append(tools, contextTools(h)...)
	srv.AddTools(tools...)
}
