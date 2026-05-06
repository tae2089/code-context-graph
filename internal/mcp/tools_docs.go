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
			),
			Handler: h.buildRagIndex,
		},
		{
			Tool: mcp.NewTool("get_rag_tree",
				mcp.WithDescription("Get the RAG document tree for navigation. Call without arguments first, then pass node_id to drill into a community, package, file, or symbol node."),
				mcp.WithString("node_id", mcp.Description("Tree node ID as shown in the tree (e.g. 'community:internal/analysis', 'package:internal/core', or 'file:internal/core/runtime.go'). Omit to get the full tree.")),
				mcp.WithString("community_id", mcp.Description("Deprecated alias for node_id; accepts any tree node ID for compatibility.")),
				mcp.WithNumber("depth", mcp.Description("Maximum tree depth to return (1=top-level communities/packages, 2=plus child packages/files). Default: 0 (unlimited).")),
				mcp.WithString("namespace", mcp.Description("Namespace. When set, reads doc-index.json from the namespace-specific index directory.")),
			),
			Handler: h.getRagTree,
		},
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
				mcp.WithDescription("Search the RAG document tree by keyword. Matches against node labels and summaries. Returns breadcrumb paths to matching nodes."),
				mcp.WithString("query", mcp.Description("Search keyword (case-insensitive)"), mcp.Required()),
				mcp.WithNumber("limit", mcp.Description("Maximum number of results (default: 10, max: 500)")),
				mcp.WithString("namespace", mcp.Description("Namespace. When set, searches the namespace-specific doc-index.json.")),
			),
			Handler: h.searchDocs,
		},
		{
			Tool: mcp.NewTool("retrieve_docs",
				mcp.WithDescription("Retrieve relevant generated docs from the RAG tree for a natural-language or multi-keyword query. Scores file subtrees and returns matched evidence plus bounded document content."),
				mcp.WithString("query", mcp.Description("Natural-language or multi-keyword retrieval query"), mcp.Required()),
				mcp.WithNumber("limit", mcp.Description("Maximum number of document results (default: 5, max: 50)")),
				mcp.WithNumber("content_limit", mcp.Description("Maximum bytes of Markdown content per result (default: 4000, max: 20000; use 0 to omit content)")),
				mcp.WithString("namespace", mcp.Description("Namespace. When set, retrieves from the namespace-specific doc-index.json.")),
				mcp.WithBoolean("explain", mcp.Description("When true, include per-result expanded_terms, field_scores, literal_score, and expansion_score diagnostics (default: false).")),
			),
			Handler: h.retrieveDocs,
		},
	}
}
