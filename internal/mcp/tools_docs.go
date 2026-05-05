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
			Tool: mcp.NewTool("build_rag_index",
				mcp.WithDescription("Build Vectorless RAG index from docs/ and community structure. Stores result in .ccg/doc-index.json. When namespace is specified, reads docs from {namespace_root}/{namespace}/ instead of local docs/."),
				mcp.WithString("out_dir", mcp.Description("Documentation directory root (default: from config or 'docs')")),
				mcp.WithString("index_dir", mcp.Description("Directory to write doc-index.json (default: '.ccg')")),
				mcp.WithString("namespace", mcp.Description("Namespace. When set, reads docs from the namespace directory instead of local docs/.")),
				mcp.WithString("workspace", mcp.Description("Deprecated alias for namespace.")),
			),
			Handler: h.buildRagIndex,
		},
		{
			Tool: mcp.NewTool("get_rag_tree",
				mcp.WithDescription("Get the RAG document tree for navigation. Call without arguments first to see all communities, then pass community_id to drill into a specific one."),
				mcp.WithString("community_id", mcp.Description("Community node ID as shown in the tree (e.g. 'community:auth'). Omit to get the full tree.")),
				mcp.WithNumber("depth", mcp.Description("Maximum tree depth to return (1=communities only, 2=communities+files). Default: 0 (unlimited).")),
				mcp.WithString("namespace", mcp.Description("Namespace. When set, reads doc-index.json from the namespace-specific index directory.")),
				mcp.WithString("workspace", mcp.Description("Deprecated alias for namespace.")),
			),
			Handler: h.getRagTree,
		},
		{
			Tool: mcp.NewTool("get_doc_content",
				mcp.WithDescription("Get the content of a documentation file by its path. When namespace is specified, reads from {namespace_root}/{namespace}/{file_path}."),
				mcp.WithString("file_path", mcp.Description("Path to the doc file (e.g. 'docs/internal/mcp/handlers.go.md')"), mcp.Required()),
				mcp.WithString("namespace", mcp.Description("Namespace. When set, reads from the namespace directory.")),
				mcp.WithString("workspace", mcp.Description("Deprecated alias for namespace.")),
			),
			Handler: h.getDocContent,
		},
		{
			Tool: mcp.NewTool("search_docs",
				mcp.WithDescription("Search the RAG document tree by keyword. Matches against node labels and summaries. Returns breadcrumb paths to matching nodes."),
				mcp.WithString("query", mcp.Description("Search keyword (case-insensitive)"), mcp.Required()),
				mcp.WithNumber("limit", mcp.Description("Maximum number of results (default: 10, max: 500)")),
				mcp.WithString("namespace", mcp.Description("Namespace. When set, searches the namespace-specific doc-index.json.")),
				mcp.WithString("workspace", mcp.Description("Deprecated alias for namespace.")),
			),
			Handler: h.searchDocs,
		},
	}
}
