package mcp

import (
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func workspaceTools(h *handlers) []server.ServerTool {
	return []server.ServerTool{
		{
			Tool: mcp.NewTool("upload_file",
				mcp.WithDescription("Upload a file to a workspace. Content must be base64-encoded. Creates {workspace}/{file_path} on the server."),
				mcp.WithString("workspace", mcp.Description("Workspace name (e.g. service name)"), mcp.Required()),
				mcp.WithString("file_path", mcp.Description("Relative file path within workspace (e.g. docs/readme.md)"), mcp.Required()),
				mcp.WithString("content", mcp.Description("Base64-encoded file content"), mcp.Required()),
			),
			Handler: h.uploadFile,
		},
		{
			Tool: mcp.NewTool("list_workspaces",
				mcp.WithDescription("List all available workspaces"),
			),
			Handler: h.listWorkspaces,
		},
		{
			Tool: mcp.NewTool("list_files",
				mcp.WithDescription("List all files in a workspace"),
				mcp.WithString("workspace", mcp.Description("Workspace name"), mcp.Required()),
			),
			Handler: h.listFiles,
		},
		{
			Tool: mcp.NewTool("delete_file",
				mcp.WithDescription("Delete a file from a workspace"),
				mcp.WithString("workspace", mcp.Description("Workspace name"), mcp.Required()),
				mcp.WithString("file_path", mcp.Description("Relative file path within workspace"), mcp.Required()),
			),
			Handler: h.deleteFile,
		},
		{
			Tool: mcp.NewTool("upload_files",
				mcp.WithDescription("Upload multiple files to workspaces in a single call. The 'files' parameter is a JSON array of objects with workspace, file_path, and content (base64-encoded) fields."),
				mcp.WithString("files", mcp.Description("JSON array of file entries: [{\"workspace\":\"...\",\"file_path\":\"...\",\"content\":\"base64...\"}]"), mcp.Required()),
			),
			Handler: h.uploadFiles,
		},
		{
			Tool: mcp.NewTool("delete_workspace",
				mcp.WithDescription("Delete an entire workspace and all its files"),
				mcp.WithString("workspace", mcp.Description("Workspace name to delete"), mcp.Required()),
			),
			Handler: h.deleteWorkspace,
		},
	}
}
