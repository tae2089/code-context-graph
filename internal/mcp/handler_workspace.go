package mcp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/tae2089/code-context-graph/internal/ctxns"
	"github.com/tae2089/code-context-graph/internal/service"
)

const (
	maxUploadSizeBytes         = 10 << 20 // 10 MB
	maxUploadFilesRequestBytes = 50 << 20 // 50 MB
	maxUploadFilesTotalBytes   = 20 << 20 // 20 MB
)

// workspaceRoot returns the filesystem root used for workspace storage.
// @intent Ensures all file upload tools use the same workspace root.
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

// @intent resolve the canonical namespace parameter while preserving the deprecated workspace alias.
func requestWorkspace(request mcp.CallToolRequest) (string, error) {
	if namespace := request.GetString("namespace", ""); namespace != "" {
		return namespace, nil
	}
	return request.RequireString("workspace")
}

// validateWorkspacePath validates workspace and file paths against traversal.
// @intent Blocks path traversal attacks in workspace file manipulation tools.
// @param workspace The workspace name or relative path segment.
// @param filePath The relative file path inside the workspace.
// @domainRule workspace and file_path must not be absolute or contain parent directory traversals.
func validateWorkspacePath(workspace, filePath string) error {
	if workspace == "" {
		return fmt.Errorf("workspace must not be empty")
	}
	cleanWS := filepath.Clean(workspace)
	if cleanWS == "." || cleanWS == ".." || filepath.IsAbs(cleanWS) || strings.HasPrefix(cleanWS, "..") || strings.ContainsAny(cleanWS, `/\\`) {
		return fmt.Errorf("invalid workspace: must be a single safe name")
	}

	if filePath != "" {
		cleanFP := filepath.Clean(filePath)
		if filepath.IsAbs(cleanFP) || strings.HasPrefix(cleanFP, "..") {
			return fmt.Errorf("invalid file_path: path traversal not allowed")
		}
	}
	return nil
}

// @intent canonicalize and create the workspace root before file operations rely on it.
// @sideEffect creates the workspace root directory when it does not yet exist.
func (h *handlers) safeWorkspaceRoot() (string, error) {
	root := h.workspaceRoot()
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve workspace root: %w", err)
	}
	if err := os.MkdirAll(absRoot, 0o755); err != nil {
		return "", fmt.Errorf("create workspace root: %w", err)
	}
	realRoot, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		return "", fmt.Errorf("resolve workspace root symlinks: %w", err)
	}
	return realRoot, nil
}

// @intent compose filesystem paths without repeating join boilerplate in workspace helpers.
func safeJoin(base string, parts ...string) string {
	all := append([]string{base}, parts...)
	return filepath.Join(all...)
}

// @intent reject symlink traversal anywhere along a workspace path before file operations touch the filesystem.
func ensureNoSymlinkInPath(root, relPath string, allowMissingLeaf bool) (string, error) {
	cleanRel := filepath.Clean(relPath)
	if cleanRel == "." {
		return root, nil
	}
	current := root
	segments := strings.Split(cleanRel, string(filepath.Separator))
	for i, segment := range segments {
		current = filepath.Join(current, segment)
		info, err := os.Lstat(current)
		if err != nil {
			if allowMissingLeaf && errors.Is(err, fs.ErrNotExist) && i == len(segments)-1 {
				return current, nil
			}
			if allowMissingLeaf && errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("symlink paths are not allowed")
		}
	}
	return current, nil
}

// @intent resolve a workspace-relative path under the trusted root after validation and symlink checks.
func (h *handlers) resolveWorkspacePath(workspace, filePath string, allowMissingLeaf bool) (string, error) {
	if err := validateWorkspacePath(workspace, filePath); err != nil {
		return "", err
	}
	root, err := h.safeWorkspaceRoot()
	if err != nil {
		return "", err
	}
	wsDir, err := ensureNoSymlinkInPath(root, filepath.Clean(workspace), false)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			wsDir = safeJoin(root, filepath.Clean(workspace))
		} else {
			return "", err
		}
	}
	if filePath == "" {
		return wsDir, nil
	}
	rel := safeJoin(filepath.Clean(workspace), filepath.Clean(filePath))
	return ensureNoSymlinkInPath(root, rel, allowMissingLeaf)
}

// @intent write workspace files atomically so partial writes are never observed as final state.
// @sideEffect creates a temp file and renames it into place.
func safeWriteFile(path string, data []byte, perm os.FileMode) error {
	tmpFile, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp.*")
	if err != nil {
		return err
	}
	tmpPath := tmpFile.Name()
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := writeFileNoFollow(tmpPath, data, perm); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
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

	if err := validateWorkspacePath(workspace, filePath); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	decoded, err := base64.StdEncoding.DecodeString(contentB64)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("invalid base64 content: %v", err)), nil
	}

	if len(decoded) > maxUploadSizeBytes {
		return mcp.NewToolResultError(fmt.Sprintf("file exceeds %d MB size limit", maxUploadSizeBytes>>20)), nil
	}

	target, err := h.resolveWorkspacePath(workspace, filePath, true)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("resolve workspace path: %v", err)), nil
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("create directory: %v", err)), nil
	}
	if _, err := h.resolveWorkspacePath(workspace, filePath, true); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("revalidate workspace path: %v", err)), nil
	}
	if err := safeWriteFile(target, decoded, 0o644); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("write file: %v", err)), nil
	}

	result := map[string]any{
		"status":    "ok",
		"namespace": workspace,
		"workspace": workspace,
		"file_path": filePath,
		"size":      len(decoded),
	}
	jsonStr, _ := marshalJSON(result)
	return mcp.NewToolResultText(jsonStr), nil
}

// listWorkspaces lists available workspace directories.
// @intent Lists workspace names on the server to aid in selecting an upload target.
// @ensures Returns an array of workspace names on success.
// @sideEffect Performs a filesystem directory read.
func (h *handlers) listWorkspaces(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	root := h.workspaceRoot()

	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			jsonStr, _ := marshalJSON([]string{})
			return mcp.NewToolResultText(jsonStr), nil
		}
		return mcp.NewToolResultError(fmt.Sprintf("read namespace root: %v", err)), nil
	}

	var workspaces []string
	for _, e := range entries {
		if e.IsDir() {
			workspaces = append(workspaces, e.Name())
		}
	}
	if workspaces == nil {
		workspaces = []string{}
	}

	jsonStr, _ := marshalJSON(workspaces)
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

	if err := validateWorkspacePath(workspace, ""); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	wsDir, err := h.resolveWorkspacePath(workspace, "", false)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			jsonStr, _ := marshalJSON([]string{})
			return mcp.NewToolResultText(jsonStr), nil
		}
		return mcp.NewToolResultError(fmt.Sprintf("resolve namespace path: %v", err)), nil
	}

	var files []string
	err = filepath.Walk(wsDir, func(fp string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if info.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(wsDir, fp)
		if relErr != nil {
			return nil
		}
		files = append(files, rel)
		return nil
	})
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("walk namespace: %v", err)), nil
	}
	if files == nil {
		files = []string{}
	}

	jsonStr, _ := marshalJSON(files)
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

	if err := validateWorkspacePath(workspace, filePath); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	target, err := h.resolveWorkspacePath(workspace, filePath, false)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("resolve namespace path: %v", err)), nil
	}

	if _, err := os.Stat(target); os.IsNotExist(err) {
		return mcp.NewToolResultError(fmt.Sprintf("file %q not found in namespace %q", filePath, workspace)), nil
	}

	if err := os.Remove(target); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("delete file: %v", err)), nil
	}

	result := map[string]any{
		"status":    "deleted",
		"namespace": workspace,
		"workspace": workspace,
		"file_path": filePath,
	}
	jsonStr, _ := marshalJSON(result)
	return mcp.NewToolResultText(jsonStr), nil
}

// uploadFileEntry describes one file payload for bulk workspace uploads.
// @intent Deserializes each entry in a bulk file upload request.
type uploadFileEntry struct {
	Namespace string `json:"namespace"`
	Workspace string `json:"workspace"`
	FilePath  string `json:"file_path"`
	Content   string `json:"content"`
}

// @intent carry decoded content and the resolved target path through the workspace upload pipeline.
type preparedUploadFile struct {
	entry   uploadFileEntry
	decoded []byte
	target  string
}

// @intent hold validated upload payload state so bulk writes can happen after all entries pass validation.

// uploadFiles writes multiple base64-encoded files in one request.
// @intent Reduces round-trip costs by uploading multiple workspace files in a single MCP call.
// @param request files is a JSON string containing an array of uploadFileEntry objects.
// @requires The files array must not be empty, and each entry must be valid.
// @ensures Returns the number of uploaded files and information for each file on success.
// @domainRule Each file is limited to 10MB, the total decoded payload to 20MB, the raw request to 50MB, and all paths must be safe.
// @sideEffect Performs directory creation and multiple file writes.
func (h *handlers) uploadFiles(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	filesRaw, err := request.RequireString("files")
	if err != nil {
		return missingParamResult(err)
	}
	if len(filesRaw) > maxUploadFilesRequestBytes {
		return mcp.NewToolResultError(fmt.Sprintf("total upload request exceeds %d MB size limit", maxUploadFilesRequestBytes>>20)), nil
	}

	var entries []uploadFileEntry
	if err := json.Unmarshal([]byte(filesRaw), &entries); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("invalid files JSON: %v", err)), nil
	}

	if len(entries) == 0 {
		return mcp.NewToolResultError("files array must not be empty"), nil
	}

	prepared := make([]preparedUploadFile, 0, len(entries))
	totalDecoded := 0
	for i, e := range entries {
		workspace := e.Namespace
		if workspace == "" {
			workspace = e.Workspace
		}
		if workspace == "" || e.FilePath == "" || e.Content == "" {
			return mcp.NewToolResultError(fmt.Sprintf("entry %d: namespace, file_path, and content are required", i)), nil
		}

		if err := validateWorkspacePath(workspace, e.FilePath); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("entry %d: %v", i, err)), nil
		}

		decoded, err := base64.StdEncoding.DecodeString(e.Content)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("entry %d: invalid base64 content: %v", i, err)), nil
		}

		if len(decoded) > maxUploadSizeBytes {
			return mcp.NewToolResultError(fmt.Sprintf("entry %d: file exceeds %d MB size limit", i, maxUploadSizeBytes>>20)), nil
		}
		if totalDecoded+len(decoded) > maxUploadFilesTotalBytes {
			return mcp.NewToolResultError(fmt.Sprintf("entry %d: total decoded upload exceeds %d MB size limit", i, maxUploadFilesTotalBytes>>20)), nil
		}
		totalDecoded += len(decoded)

		e.Workspace = workspace
		target, err := h.resolveWorkspacePath(workspace, e.FilePath, true)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("entry %d: resolve namespace path: %v", i, err)), nil
		}
		prepared = append(prepared, preparedUploadFile{entry: e, decoded: decoded, target: target})
	}

	results := make([]map[string]any, 0, len(prepared))
	for i, file := range prepared {
		target := file.target
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("entry %d: create directory: %v", i, err)), nil
		}
		if _, err := h.resolveWorkspacePath(file.entry.Workspace, file.entry.FilePath, true); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("entry %d: revalidate namespace path: %v", i, err)), nil
		}
		if err := safeWriteFile(target, file.decoded, 0o644); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("entry %d: write file: %v", i, err)), nil
		}

		results = append(results, map[string]any{
			"namespace": file.entry.Workspace,
			"workspace": file.entry.Workspace,
			"file_path": file.entry.FilePath,
			"size":      len(file.decoded),
		})
	}

	resp := map[string]any{
		"status":   "ok",
		"uploaded": len(results),
		"files":    results,
	}
	jsonStr, _ := marshalJSON(resp)
	return mcp.NewToolResultText(jsonStr), nil
}

// deleteWorkspace removes an entire workspace directory tree.
// @intent Enables bulk cleanup of uploaded file sets by workspace.
// @param request workspace is the name of the workspace to delete.
// @requires workspace must be a safe relative path and must actually exist.
// @ensures Returns the name of the deleted workspace on success.
// @sideEffect Recursively deletes the workspace directory from the filesystem.
func (h *handlers) deleteWorkspace(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	workspace, err := requestWorkspace(request)
	if err != nil {
		return missingParamResult(err)
	}

	if err := validateWorkspacePath(workspace, ""); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	wsDir, err := h.resolveWorkspacePath(workspace, "", false)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("resolve namespace path: %v", err)), nil
	}

	if _, err := os.Stat(wsDir); os.IsNotExist(err) {
		return mcp.NewToolResultError(fmt.Sprintf("namespace %q not found", workspace)), nil
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

	if err := os.RemoveAll(wsDir); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("delete namespace: %v", err)), nil
	}

	if h.cache != nil {
		h.cache.Flush()
	}

	delResult := map[string]any{
		"status":    "deleted",
		"namespace": workspace,
		"workspace": workspace,
	}
	jsonStr, _ := marshalJSON(delResult)
	return mcp.NewToolResultText(jsonStr), nil
}
