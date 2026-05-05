// @index MCP handlers for namespace uploads, listing, and deletion.
package mcp

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/tae2089/code-context-graph/internal/ctxns"
	nsfs "github.com/tae2089/code-context-graph/internal/namespacefs"
	"github.com/tae2089/code-context-graph/internal/paging"
	"github.com/tae2089/code-context-graph/internal/service"
)

// namespaceFileResult summarizes one uploaded namespace file.
// @intent preserve a stable per-file DTO for namespace upload responses.
type namespaceFileResult struct {
	Namespace string `json:"namespace"`
	FilePath  string `json:"file_path"`
	Size      int    `json:"size,omitempty"`
}

// namespaceUploadResponse is the typed wire payload for uploadFile and uploadFiles.
// @intent preserve a stable confirmation envelope for single and bulk namespace uploads.
type namespaceUploadResponse struct {
	Status    string                `json:"status"`
	Namespace string                `json:"namespace,omitempty"`
	FilePath  string                `json:"file_path,omitempty"`
	Size      int                   `json:"size,omitempty"`
	Uploaded  int                   `json:"uploaded,omitempty"`
	Files     []namespaceFileResult `json:"files,omitempty"`
}

// namespaceDeleteResponse is the typed wire payload for deleteFile and deleteNamespace.
// @intent preserve a stable confirmation envelope for namespace deletion operations.
type namespaceDeleteResponse struct {
	Status    string `json:"status"`
	Namespace string `json:"namespace"`
	FilePath  string `json:"file_path,omitempty"`
}

// namespaceListResponse is the typed wire payload for listNamespaces and listFiles.
// @intent preserve a stable paged list envelope while omitting irrelevant legacy fields.
type namespaceListResponse struct {
	Namespaces []string    `json:"namespaces,omitempty"`
	Files      []string    `json:"files,omitempty"`
	Items      []string    `json:"items"`
	Count      int         `json:"count"`
	Pagination paging.Page `json:"pagination"`
}

// namespaceRoot returns the filesystem root used for namespace storage.
// @intent ensure all file upload tools use the same namespace root.
// @return Returns the default "namespaces" directory if no configuration value is set.
func (h *handlers) namespaceRoot() string {
	root := h.deps.NamespaceRoot
	if root == "" {
		root = "namespaces"
	}
	return root
}

// @intent build a namespacefs.Service bound to the current handler's resolved root.
func (h *handlers) namespaceService() *nsfs.Service {
	return nsfs.NewService(h.namespaceRoot())
}

// @intent require the canonical namespace parameter for namespace file operations.
func requireNamespace(request mcp.CallToolRequest) (string, error) {
	return request.RequireString("namespace")
}

// validateNamespacePath validates namespace and file paths against traversal.
// @intent thin MCP-side delegator shared across handler files.
func validateNamespacePath(namespace, filePath string) error {
	return nsfs.ValidatePath(namespace, filePath)
}

// @intent reject symlink traversal anywhere along a namespace path before file operations touch the filesystem.
func ensureNoSymlinkInPath(root, relPath string, allowMissingLeaf bool) (string, error) {
	return nsfs.EnsureNoSymlinkInPath(root, relPath, allowMissingLeaf)
}

// @intent resolve a namespace-relative path under the trusted root after validation and symlink checks.
func (h *handlers) resolveNamespacePath(namespace, filePath string, allowMissingLeaf bool) (string, error) {
	return h.namespaceService().ResolvePath(namespace, filePath, allowMissingLeaf)
}

// @intent map namespacefs.Service errors to MCP user-error responses.
func namespaceErrorResult(err error, fallbackPrefix string) *mcp.CallToolResult {
	if nsfs.IsValidationError(err) {
		return mcp.NewToolResultError(err.Error())
	}
	if fallbackPrefix == "" {
		return mcp.NewToolResultError(err.Error())
	}
	return mcp.NewToolResultError(fmt.Sprintf("%s: %v", fallbackPrefix, err))
}

// uploadFile writes one base64-encoded file into a namespace.
// @intent Uploads a single file to the server namespace for subsequent analysis or documentation tasks.
// @param request content is the base64-encoded file bytes.
// @requires namespace and file_path must be safe relative paths.
// @ensures Returns the stored file path and size on success.
// @domainRule Uploaded files cannot exceed 10MB.
// @sideEffect Performs directory creation and file writes.
func (h *handlers) uploadFile(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	namespace, err := requireNamespace(request)
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

	res, err := h.namespaceService().UploadFile(nsfs.UploadRequest{
		Namespace:     namespace,
		FilePath:      filePath,
		ContentBase64: contentB64,
	})
	if err != nil {
		return namespaceErrorResult(err, ""), nil
	}

	jsonStr, _ := marshalJSON(namespaceUploadResponse{
		Status:    "ok",
		Namespace: res.Namespace,
		FilePath:  res.FilePath,
		Size:      res.Size,
	})
	return mcp.NewToolResultText(jsonStr), nil
}

// listNamespaces lists available namespace directories.
// @intent Lists namespace names on the server to aid in selecting an upload target.
// @ensures Returns an array of namespace names on success.
// @sideEffect Performs a filesystem directory read.
func (h *handlers) listNamespaces(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
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

	namespaces, err := h.namespaceService().ListNamespaces()
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	hasMore := false
	if pageReq.Offset >= len(namespaces) {
		namespaces = []string{}
	} else {
		namespaces = namespaces[pageReq.Offset:]
	}
	if len(namespaces) > pageReq.Limit {
		namespaces = namespaces[:pageReq.Limit]
		hasMore = true
	}

	jsonStr, _ := marshalJSON(namespaceListResponse{
		Namespaces: namespaces,
		Items:      namespaces,
		Count:      len(namespaces),
		Pagination: paging.BuildPage(pageReq, len(namespaces), hasMore),
	})
	return mcp.NewToolResultText(jsonStr), nil
}

// listFiles lists all files stored inside a namespace.
// @intent Enables checking the current file configuration of a specific namespace.
// @param request namespace is the name of the namespace to check.
// @requires namespace must be a safe relative path.
// @ensures Returns an array of relative file paths inside the namespace on success.
// @sideEffect Performs a filesystem traversal.
func (h *handlers) listFiles(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	namespace, err := requireNamespace(request)
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

	files, err := h.namespaceService().ListFiles(namespace)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			jsonStr, _ := marshalJSON(namespaceListResponse{
				Files:      []string{},
				Items:      []string{},
				Count:      0,
				Pagination: paging.BuildPage(pageReq, 0, false),
			})
			return mcp.NewToolResultText(jsonStr), nil
		}
		return namespaceErrorResult(err, ""), nil
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

	jsonStr, _ := marshalJSON(namespaceListResponse{
		Files:      files,
		Items:      files,
		Count:      len(files),
		Pagination: paging.BuildPage(pageReq, len(files), hasMore),
	})
	return mcp.NewToolResultText(jsonStr), nil
}

// deleteFile removes one file from a namespace.
// @intent Allows individual cleanup of namespace files that are no longer needed.
// @param request Selects the deletion target via namespace and file_path.
// @requires The target file must exist in the specified namespace.
// @ensures Returns information about the deleted file on success.
// @sideEffect Deletes the actual file from the filesystem.
func (h *handlers) deleteFile(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	namespace, err := requireNamespace(request)
	if err != nil {
		return missingParamResult(err)
	}
	filePath, err := request.RequireString("file_path")
	if err != nil {
		return missingParamResult(err)
	}

	if err := h.namespaceService().DeleteFile(namespace, filePath); err != nil {
		return namespaceErrorResult(err, ""), nil
	}

	jsonStr, _ := marshalJSON(namespaceDeleteResponse{
		Status:    "deleted",
		Namespace: namespace,
		FilePath:  filePath,
	})
	return mcp.NewToolResultText(jsonStr), nil
}

// uploadFiles writes multiple base64-encoded files in one request.
// @intent Reduces round-trip costs by uploading multiple namespace files in a single MCP call.
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

	results, err := h.namespaceService().UploadFiles(filesRaw)
	if err != nil {
		var bulkErr *nsfs.BulkEntryError
		if errors.As(err, &bulkErr) {
			return mcp.NewToolResultError(bulkErr.Error()), nil
		}
		if nsfs.IsValidationError(err) {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultError(err.Error()), nil
	}

	files := make([]namespaceFileResult, 0, len(results))
	for _, r := range results {
		files = append(files, namespaceFileResult{
			Namespace: r.Namespace,
			FilePath:  r.FilePath,
			Size:      r.Size,
		})
	}

	jsonStr, _ := marshalJSON(namespaceUploadResponse{
		Status:   "ok",
		Uploaded: len(files),
		Files:    files,
	})
	return mcp.NewToolResultText(jsonStr), nil
}

// deleteNamespace removes an entire namespace directory tree.
// @intent Enables bulk cleanup of uploaded file sets by namespace.
// @param request namespace is the name of the namespace to delete.
// @requires namespace must be a safe relative path and must actually exist.
// @ensures Returns the name of the deleted namespace on success.
// @sideEffect Recursively deletes the namespace directory, RAG index, and namespaced graph state.
func (h *handlers) deleteNamespace(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	namespace, err := requireNamespace(request)
	if err != nil {
		return missingParamResult(err)
	}

	svc := h.namespaceService()
	nsDir, err := svc.ResolveExistingNamespace(namespace)
	if err != nil {
		return namespaceErrorResult(err, ""), nil
	}

	if h.deps != nil && h.deps.Store != nil {
		purger := service.NewNamespacePurger(h.deps.Store, h.deps.DB, h.deps.SearchBackend)
		if err := purger.Purge(ctxns.WithNamespace(ctx, namespace)); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("purge namespace: %v", err)), nil
		}
	}

	if indexPath, err := h.resolvedRagIndexPath(namespace); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("resolve rag index path: %v", err)), nil
	} else if err := os.Remove(indexPath); err != nil && !os.IsNotExist(err) {
		return mcp.NewToolResultError(fmt.Sprintf("delete namespace rag index: %v", err)), nil
	}

	if err := svc.RemoveTree(nsDir); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	if h.cache != nil {
		h.cache.Flush()
	}

	jsonStr, _ := marshalJSON(namespaceDeleteResponse{
		Status:    "deleted",
		Namespace: namespace,
	})
	return mcp.NewToolResultText(jsonStr), nil
}
