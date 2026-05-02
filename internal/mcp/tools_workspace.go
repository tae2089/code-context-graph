package mcp

import (
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func workspaceTools(h *handlers) []server.ServerTool {
	return []server.ServerTool{
		{
			Tool: mcp.NewTool("upload_file",
				mcp.WithDescription("Upload a file to a namespace. Content must be base64-encoded. Creates {namespace}/{file_path} on the server."),
				mcp.WithString("namespace", mcp.Description("Namespace (e.g. service name)"), mcp.Required()),
				mcp.WithString("workspace", mcp.Description("Deprecated alias for namespace")),
				mcp.WithString("file_path", mcp.Description("Relative file path within namespace (e.g. docs/readme.md)"), mcp.Required()),
				mcp.WithString("content", mcp.Description("Base64-encoded file content"), mcp.Required()),
			),
			Handler: h.uploadFile,
		},
		{
			Tool: mcp.NewTool("list_namespaces",
				mcp.WithDescription("List all available namespaces"),
			),
			Handler: h.listWorkspaces,
		},
		{
			Tool: mcp.NewTool("list_workspaces",
				mcp.WithDescription("Deprecated alias for list_namespaces"),
			),
			Handler: h.listWorkspaces,
		},
		{
			Tool: mcp.NewTool("list_files",
				mcp.WithDescription("List all files in a namespace"),
				mcp.WithString("namespace", mcp.Description("Namespace"), mcp.Required()),
				mcp.WithString("workspace", mcp.Description("Deprecated alias for namespace")),
			),
			Handler: h.listFiles,
		},
		{
			Tool: mcp.NewTool("delete_file",
				mcp.WithDescription("Delete a file from a namespace"),
				mcp.WithString("namespace", mcp.Description("Namespace"), mcp.Required()),
				mcp.WithString("workspace", mcp.Description("Deprecated alias for namespace")),
				mcp.WithString("file_path", mcp.Description("Relative file path within namespace"), mcp.Required()),
			),
			Handler: h.deleteFile,
		},
		{
			Tool: mcp.NewTool("upload_files",
				mcp.WithDescription("Upload multiple files to namespaces in a single call. The 'files' parameter is a JSON array of objects with namespace, file_path, and content (base64-encoded) fields. workspace is a deprecated alias."),
				mcp.WithString("files", mcp.Description("JSON array of file entries: [{\"namespace\":\"...\",\"file_path\":\"...\",\"content\":\"base64...\"}]"), mcp.Required()),
			),
			Handler: h.uploadFiles,
		},
		{
			Tool: mcp.NewTool("delete_namespace",
				mcp.WithDescription("Delete an entire namespace and all its files"),
				mcp.WithString("namespace", mcp.Description("Namespace to delete"), mcp.Required()),
				mcp.WithString("workspace", mcp.Description("Deprecated alias for namespace")),
			),
			Handler: h.deleteWorkspace,
		},
		{
			Tool: mcp.NewTool("delete_workspace",
				mcp.WithDescription("Deprecated alias for delete_namespace"),
				mcp.WithString("workspace", mcp.Description("Deprecated alias for namespace to delete"), mcp.Required()),
				mcp.WithString("namespace", mcp.Description("Canonical namespace to delete")),
			),
			Handler: h.deleteWorkspace,
		},
	}
}
