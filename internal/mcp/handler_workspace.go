// @index MCP handlers for namespace workspace uploads, listing, and deletion.
package mcp

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/tae2089/code-context-graph/internal/ctxns"
	"github.com/tae2089/code-context-graph/internal/paging"
	"github.com/tae2089/code-context-graph/internal/service"
	wsvc "github.com/tae2089/code-context-graph/internal/workspace"
)

// workspaceFileResult summarizes one uploaded workspace file.
// @intent preserve a stable per-file DTO for workspace upload responses.
type workspaceFileResult struct {
	Namespace string `json:"namespace"`
	Workspace string `json:"workspace"`
	FilePath  string `json:"file_path"`
	Size      int    `json:"size,omitempty"`
}

// workspaceUploadResponse is the typed wire payload for uploadFile and uploadFiles.
// @intent preserve a stable confirmation envelope for single and bulk workspace uploads.
type workspaceUploadResponse struct {
	Status    string                `json:"status"`
	Namespace string                `json:"namespace,omitempty"`
	Workspace string                `json:"workspace,omitempty"`
	FilePath  string                `json:"file_path,omitempty"`
	Size      int                   `json:"size,omitempty"`
	Uploaded  int                   `json:"uploaded,omitempty"`
	Files     []workspaceFileResult `json:"files,omitempty"`
}

// workspaceDeleteResponse is the typed wire payload for deleteFile and deleteWorkspace.
// @intent preserve a stable confirmation envelope for workspace deletion operations.
type workspaceDeleteResponse struct {
	Status    string `json:"status"`
	Namespace string `json:"namespace"`
	Workspace string `json:"workspace"`
	FilePath  string `json:"file_path,omitempty"`
}

// workspaceListResponse is the typed wire payload for listWorkspaces and listFiles.
// @intent preserve a stable paged list envelope while omitting irrelevant legacy fields.
type workspaceListResponse struct {
	Namespaces []string    `json:"namespaces,omitempty"`
	Files      []string    `json:"files,omitempty"`
	Items      []string    `json:"items"`
	Count      int         `json:"count"`
	Pagination paging.Page `json:"pagination"`
}

// workspaceRoot returns the filesystem root used for workspace storage.
// @intent ensure all file upload tools use the same workspace root.
// @return Returns the default "workspaces" directory if no configuration value is set.
func (h *handlers) workspaceRoot() string {
	root := h.deps.NamespaceRoot
	if root == "" {
		root = h.deps.WorkspaceRoot
	}
	if root == "" {
		root = "workspaces"
	}
	return root
}

// @intent build a workspace.Service bound to the current handler's resolved root.
func (h *handlers) workspaceService() *wsvc.Service {
	return wsvc.NewService(h.workspaceRoot())
}

// @intent resolve the canonical namespace parameter while preserving the deprecated workspace alias.
func requestWorkspace(request mcp.CallToolRequest) (string, error) {
	if namespace := request.GetString("namespace", ""); namespace != "" {
		return namespace, nil
	}
	return request.RequireString("workspace")
}

// validateWorkspacePath validates workspace and file paths against traversal.
// @intent thin MCP-side delegator that preserves the historical helper name used across handler files.
func validateWorkspacePath(workspace, filePath string) error {
	return wsvc.ValidatePath(workspace, filePath)
}

// @intent reject symlink traversal anywhere along a workspace path before file operations touch the filesystem.
func ensureNoSymlinkInPath(root, relPath string, allowMissingLeaf bool) (string, error) {
	return wsvc.EnsureNoSymlinkInPath(root, relPath, allowMissingLeaf)
}

// @intent resolve a workspace-relative path under the trusted root after validation and symlink checks.
func (h *handlers) resolveWorkspacePath(workspace, filePath string, allowMissingLeaf bool) (string, error) {
	return h.workspaceService().ResolvePath(workspace, filePath, allowMissingLeaf)
}

// @intent map workspace.Service errors to MCP user-error responses with the historical message format.
func workspaceErrorResult(err error, fallbackPrefix string) *mcp.CallToolResult {
	if wsvc.IsValidationError(err) {
		return mcp.NewToolResultError(err.Error())
	}
	if fallbackPrefix == "" {
		return mcp.NewToolResultError(err.Error())
	}
	return mcp.NewToolResultError(fmt.Sprintf("%s: %v", fallbackPrefix, err))
}

// uploadFile writes one base64-encoded file into a workspace.
// @intent Uploads a single file to the server workspace for subsequent analysis or documentation tasks.
// @param request content is the base64-encoded file bytes.
// @requires workspace and file_path must be safe relative paths.
// @ensures Returns the stored file path and size on success.
// @domainRule Uploaded files cannot exceed 10MB.
// @sideEffect Performs directory creation and file writes.
func (h *handlers) uploadFile(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	workspace, err := requestWorkspace(request)
	if err != nil {
		return missingParamResult(err)
	}
	filePath, err := request.RequireString("file_path")
	if err != nil {
		return missingParamResult(err)
	}
	contentB64, err := request.RequireString("content")
	if err != nil {
		return missingParamResult(err)
	}

	res, err := h.workspaceService().UploadFile(wsvc.UploadRequest{
		Namespace:     workspace,
		FilePath:      filePath,
		ContentBase64: contentB64,
	})
	if err != nil {
		return workspaceErrorResult(err, ""), nil
	}

	jsonStr, _ := marshalJSON(workspaceUploadResponse{
		Status:    "ok",
		Namespace: res.Namespace,
		Workspace: res.Namespace,
		FilePath:  res.FilePath,
		Size:      res.Size,
	})
	return mcp.NewToolResultText(jsonStr), nil
}

// listWorkspaces lists available workspace directories.
// @intent Lists workspace names on the server to aid in selecting an upload target.
// @ensures Returns an array of workspace names on success.
// @sideEffect Performs a filesystem directory read.
func (h *handlers) listWorkspaces(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	limit := request.GetInt("limit", 50)
	offset := request.GetInt("offset", 0)
	if err := validatePositiveLimit(limit); err != nil {
		return finalizeToolResult("", err)
	}
	if err := validateOffset(offset); err != nil {
		return finalizeToolResult("", err)
	}
	pageReq, err := paging.Normalize(paging.Request{Limit: limit, Offset: offset})
	if err != nil {
		return finalizeToolResult("", newToolResultErr(err.Error()))
	}

	workspaces, err := h.workspaceService().ListNamespaces()
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	hasMore := false
	if pageReq.Offset >= len(workspaces) {
		workspaces = []string{}
	} else {
		workspaces = workspaces[pageReq.Offset:]
	}
	if len(workspaces) > pageReq.Limit {
		workspaces = workspaces[:pageReq.Limit]
		hasMore = true
	}

	jsonStr, _ := marshalJSON(workspaceListResponse{
		Namespaces: workspaces,
		Items:      workspaces,
		Count:      len(workspaces),
		Pagination: paging.BuildPage(pageReq, len(workspaces), hasMore),
	})
	return mcp.NewToolResultText(jsonStr), nil
}

// listFiles lists all files stored inside a workspace.
// @intent Enables checking the current file configuration of a specific workspace.
// @param request workspace is the name of the workspace to check.
// @requires workspace must be a safe relative path.
// @ensures Returns an array of relative file paths inside the workspace on success.
// @sideEffect Performs a filesystem traversal.
func (h *handlers) listFiles(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	workspace, err := requestWorkspace(request)
	if err != nil {
		return missingParamResult(err)
	}
	limit := request.GetInt("limit", 50)
	offset := request.GetInt("offset", 0)
	if err := validatePositiveLimit(limit); err != nil {
		return finalizeToolResult("", err)
	}
	if err := validateOffset(offset); err != nil {
		return finalizeToolResult("", err)
	}
	pageReq, err := paging.Normalize(paging.Request{Limit: limit, Offset: offset})
	if err != nil {
		return finalizeToolResult("", newToolResultErr(err.Error()))
	}

	files, err := h.workspaceService().ListFiles(workspace)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			jsonStr, _ := marshalJSON(workspaceListResponse{
				Files:      []string{},
				Items:      []string{},
				Count:      0,
				Pagination: paging.BuildPage(pageReq, 0, false),
			})
			return mcp.NewToolResultText(jsonStr), nil
		}
		return workspaceErrorResult(err, ""), nil
	}

	hasMore := false
	if pageReq.Offset >= len(files) {
		files = []string{}
	} else {
		files = files[pageReq.Offset:]
	}
	if len(files) > pageReq.Limit {
		files = files[:pageReq.Limit]
		hasMore = true
	}

	jsonStr, _ := marshalJSON(workspaceListResponse{
		Files:      files,
		Items:      files,
		Count:      len(files),
		Pagination: paging.BuildPage(pageReq, len(files), hasMore),
	})
	return mcp.NewToolResultText(jsonStr), nil
}

// deleteFile removes one file from a workspace.
// @intent Allows individual cleanup of workspace files that are no longer needed.
// @param request Selects the deletion target via workspace and file_path.
// @requires The target file must exist in the specified workspace.
// @ensures Returns information about the deleted file on success.
// @sideEffect Deletes the actual file from the filesystem.
func (h *handlers) deleteFile(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	workspace, err := requestWorkspace(request)
	if err != nil {
		return missingParamResult(err)
	}
	filePath, err := request.RequireString("file_path")
	if err != nil {
		return missingParamResult(err)
	}

	if err := h.workspaceService().DeleteFile(workspace, filePath); err != nil {
		return workspaceErrorResult(err, ""), nil
	}

	jsonStr, _ := marshalJSON(workspaceDeleteResponse{
		Status:    "deleted",
		Namespace: workspace,
		Workspace: workspace,
		FilePath:  filePath,
	})
	return mcp.NewToolResultText(jsonStr), nil
}

// uploadFiles writes multiple base64-encoded files in one request.
// @intent Reduces round-trip costs by uploading multiple workspace files in a single MCP call.
// @param request files is a JSON string containing an array of upload entries.
// @requires The files array must not be empty, and each entry must be valid.
// @ensures Returns the number of uploaded files and information for each file on success.
// @domainRule Each file is limited to 10MB, the total decoded payload to 20MB, the raw request to 50MB, and all paths must be safe.
// @sideEffect Performs directory creation and multiple file writes.
func (h *handlers) uploadFiles(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	filesRaw, err := request.RequireString("files")
	if err != nil {
		return missingParamResult(err)
	}

	results, err := h.workspaceService().UploadFiles(filesRaw)
	if err != nil {
		var bulkErr *wsvc.BulkEntryError
		if errors.As(err, &bulkErr) {
			return mcp.NewToolResultError(bulkErr.Error()), nil
		}
		if wsvc.IsValidationError(err) {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultError(err.Error()), nil
	}

	files := make([]workspaceFileResult, 0, len(results))
	for _, r := range results {
		files = append(files, workspaceFileResult{
			Namespace: r.Namespace,
			Workspace: r.Namespace,
			FilePath:  r.FilePath,
			Size:      r.Size,
		})
	}

	jsonStr, _ := marshalJSON(workspaceUploadResponse{
		Status:   "ok",
		Uploaded: len(files),
		Files:    files,
	})
	return mcp.NewToolResultText(jsonStr), nil
}

// deleteWorkspace removes an entire workspace directory tree.
// @intent Enables bulk cleanup of uploaded file sets by workspace.
// @param request workspace is the name of the workspace to delete.
// @requires workspace must be a safe relative path and must actually exist.
// @ensures Returns the name of the deleted workspace on success.
// @sideEffect Recursively deletes the workspace directory, RAG index, and namespaced graph state.
func (h *handlers) deleteWorkspace(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	workspace, err := requestWorkspace(request)
	if err != nil {
		return missingParamResult(err)
	}

	svc := h.workspaceService()
	wsDir, err := svc.ResolveExistingNamespace(workspace)
	if err != nil {
		return workspaceErrorResult(err, ""), nil
	}

	if h.deps != nil && h.deps.Store != nil {
		purger := service.NewNamespacePurger(h.deps.Store, h.deps.DB, h.deps.SearchBackend)
		if err := purger.Purge(ctxns.WithNamespace(ctx, workspace)); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("purge namespace: %v", err)), nil
		}
	}

	if indexPath, err := h.resolvedRagIndexPath(workspace); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("resolve rag index path: %v", err)), nil
	} else if err := os.Remove(indexPath); err != nil && !os.IsNotExist(err) {
		return mcp.NewToolResultError(fmt.Sprintf("delete namespace rag index: %v", err)), nil
	}

	if err := svc.RemoveTree(wsDir); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	if h.cache != nil {
		h.cache.Flush()
	}

	jsonStr, _ := marshalJSON(workspaceDeleteResponse{
		Status:    "deleted",
		Namespace: workspace,
		Workspace: workspace,
	})
	return mcp.NewToolResultText(jsonStr), nil
}
