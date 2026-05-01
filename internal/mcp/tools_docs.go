package mcp

import (
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func docsTools(h *handlers) []server.ServerTool {
	return []server.ServerTool{
		{
			Tool: mcp.NewTool("build_rag_index",
				mcp.WithDescription("Build Vectorless RAG index from docs/ and community structure. Stores result in .ccg/doc-index.json. When workspace is specified, reads docs from {workspace_root}/{workspace}/ instead of local docs/."),
				mcp.WithString("out_dir", mcp.Description("Documentation directory root (default: from config or 'docs')")),
				mcp.WithString("index_dir", mcp.Description("Directory to write doc-index.json (default: '.ccg')")),
				mcp.WithString("workspace", mcp.Description("Workspace name. When set, reads docs from the workspace directory instead of local docs/.")),
			),
			Handler: h.buildRagIndex,
		},
		{
			Tool: mcp.NewTool("get_rag_tree",
				mcp.WithDescription("Get the RAG document tree for navigation. Call without arguments first to see all communities, then pass community_id to drill into a specific one."),
				mcp.WithString("community_id", mcp.Description("Community node ID as shown in the tree (e.g. 'community:auth'). Omit to get the full tree.")),
				mcp.WithNumber("depth", mcp.Description("Maximum tree depth to return (1=communities only, 2=communities+files). Default: 0 (unlimited).")),
				mcp.WithString("workspace", mcp.Description("Workspace name. When set, reads doc-index.json from the workspace-specific index directory.")),
			),
			Handler: h.getRagTree,
		},
		{
			Tool: mcp.NewTool("get_doc_content",
				mcp.WithDescription("Get the content of a documentation file by its path. When workspace is specified, reads from {workspace_root}/{workspace}/{file_path}."),
				mcp.WithString("file_path", mcp.Description("Path to the doc file (e.g. 'docs/internal/mcp/handlers.go.md')"), mcp.Required()),
				mcp.WithString("workspace", mcp.Description("Workspace name. When set, reads from the workspace directory.")),
			),
			Handler: h.getDocContent,
		},
		{
			Tool: mcp.NewTool("search_docs",
				mcp.WithDescription("Search the RAG document tree by keyword. Matches against node labels and summaries. Returns breadcrumb paths to matching nodes."),
				mcp.WithString("query", mcp.Description("Search keyword (case-insensitive)"), mcp.Required()),
				mcp.WithNumber("limit", mcp.Description("Maximum number of results (default: 10)")),
				mcp.WithString("workspace", mcp.Description("Workspace name. When set, searches the workspace-specific doc-index.json.")),
			),
			Handler: h.searchDocs,
		},
	}
}
