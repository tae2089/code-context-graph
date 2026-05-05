// @index MCP tool registration for namespace file management.
package mcp

import (
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// namespaceTools registers file and namespace management tools for isolated namespaces.
// @intent expose upload, listing, and deletion workflows as one coherent MCP surface.
func namespaceTools(h *handlers) []server.ServerTool {
	return []server.ServerTool{
		{
			Tool: mcp.NewTool("upload_file",
				mcp.WithDescription("Upload a file to a namespace. Content must be base64-encoded. Creates {namespace}/{file_path} on the server."),
				mcp.WithString("namespace", mcp.Description("Namespace (e.g. service name)"), mcp.Required()),
				mcp.WithString("file_path", mcp.Description("Relative file path within namespace (e.g. docs/readme.md)"), mcp.Required()),
				mcp.WithString("content", mcp.Description("Base64-encoded file content"), mcp.Required()),
			),
			Handler: h.uploadFile,
		},
		{
			Tool: mcp.NewTool("list_namespaces",
				mcp.WithDescription("List all available namespaces"),
				mcp.WithNumber("limit", mcp.Description("Maximum number of results (default: 50, max: 500)")),
				mcp.WithNumber("offset", mcp.Description("Zero-based result offset for pagination (default: 0)")),
			),
			Handler: h.listNamespaces,
		},
		{
			Tool: mcp.NewTool("list_files",
				mcp.WithDescription("List all files in a namespace"),
				mcp.WithString("namespace", mcp.Description("Namespace"), mcp.Required()),
				mcp.WithNumber("limit", mcp.Description("Maximum number of results (default: 50, max: 500)")),
				mcp.WithNumber("offset", mcp.Description("Zero-based result offset for pagination (default: 0)")),
			),
			Handler: h.listFiles,
		},
		{
			Tool: mcp.NewTool("delete_file",
				mcp.WithDescription("Delete a file from a namespace"),
				mcp.WithString("namespace", mcp.Description("Namespace"), mcp.Required()),
				mcp.WithString("file_path", mcp.Description("Relative file path within namespace"), mcp.Required()),
			),
			Handler: h.deleteFile,
		},
		{
			Tool: mcp.NewTool("upload_files",
				mcp.WithDescription("Upload multiple files to namespaces in a single call. The 'files' parameter is a JSON array of objects with namespace, file_path, and content (base64-encoded) fields."),
				mcp.WithString("files", mcp.Description("JSON array of file entries: [{\"namespace\":\"...\",\"file_path\":\"...\",\"content\":\"base64...\"}]"), mcp.Required()),
			),
			Handler: h.uploadFiles,
		},
		{
			Tool: mcp.NewTool("delete_namespace",
				mcp.WithDescription("Delete an entire namespace and all its files"),
				mcp.WithString("namespace", mcp.Description("Namespace to delete"), mcp.Required()),
			),
			Handler: h.deleteNamespace,
		},
	}
}
