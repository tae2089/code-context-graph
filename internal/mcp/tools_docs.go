// @index MCP tool registration for documentation and RAG index operations.
package mcp

import (
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// docsTools registers tools that build and query the documentation index.
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
		{
			Tool: mcp.NewTool("search_docs",
				mcp.WithDescription("Search DB-backed documentation candidates by keyword. Matches graph nodes and annotation evidence, then returns breadcrumb paths to matching files."),
				mcp.WithString("query", mcp.Description("Search keyword (case-insensitive)"), mcp.Required()),
				mcp.WithNumber("limit", mcp.Description("Maximum number of results (default: 10, max: 500)")),
				mcp.WithString("namespace", mcp.Description("Namespace. When set, searches that namespace's DB-backed documentation candidates.")),
			),
			Handler: h.searchDocs,
		},
	}
}
