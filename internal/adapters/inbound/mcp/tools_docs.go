// @index MCP tool registration for DB-backed documentation search and generated document reads.
package mcp

import (
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// docsTools registers tools that search documentation candidates and read generated documents.
// @intent keep documentation retrieval flows discoverable as one MCP tool family.
func docsTools(h *handlers) []server.ServerTool {
	return []server.ServerTool{
		{
			Tool: mcp.NewTool("get_doc_content",
				mcp.WithDescription("Get the content of a documentation file by its path. When namespace is specified, reads from {namespace_root}/{namespace}/{file_path}."),
				mcp.WithString("file_path", mcp.Description("Path to the doc file (e.g. 'docs/internal/mcp/handlers.go.md')"), mcp.Required()),
				mcp.WithString("namespace", mcp.Description("Namespace. When set, reads from the namespace directory.")),
			),
			Handler: h.getDocContent,
		},
	}
}
