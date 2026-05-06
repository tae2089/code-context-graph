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
				mcp.WithDescription("Navigate the RAG document tree after retrieve_docs/search_docs has identified a useful area. Prefer depth=1 for overview or pass node_id with a small depth to expand nearby context; avoid unbounded full-tree calls on large namespaces."),
				mcp.WithString("node_id", mcp.Description("Tree node ID as shown in search/retrieve/tree results (e.g. 'community:internal/analysis', 'package:internal/core', or 'file:internal/core/runtime.go'). Omit only for a bounded overview with depth set.")),
				mcp.WithString("community_id", mcp.Description("Deprecated alias for node_id; accepts any tree node ID for compatibility.")),
				mcp.WithNumber("depth", mcp.Description("Maximum tree depth to return (1=top-level communities/packages, 2=plus child packages/files). Recommended when node_id is omitted. Default: 0 (unlimited, only for small namespaces).")),
				mcp.WithString("namespace", mcp.Description("Namespace. When set, reads that namespace's DB tree or wiki-index.json fallback.")),
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
				mcp.WithDescription("Retrieve relevant generated docs for a natural-language or multi-keyword query. Uses DB-backed graph evidence first, falls back to doc-index.json when DB retrieval is unavailable, and returns matched evidence plus bounded document content."),
				mcp.WithString("query", mcp.Description("Natural-language or multi-keyword retrieval query"), mcp.Required()),
				mcp.WithNumber("limit", mcp.Description("Maximum number of document results (default: 5, max: 50)")),
				mcp.WithNumber("content_limit", mcp.Description("Maximum bytes of Markdown content per result (default: 4000, max: 20000; use 0 to omit content)")),
				mcp.WithString("namespace", mcp.Description("Namespace. When set, retrieves from that namespace's DB rows or namespace-specific doc-index.json fallback.")),
				mcp.WithBoolean("explain", mcp.Description("When true, include available per-result diagnostics such as field_scores, literal_score, expanded_terms, and expansion_score (default: false).")),
			),
			Handler: h.retrieveDocs,
		},
	}
}
