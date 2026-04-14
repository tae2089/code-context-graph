package mcp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

const maxUploadSizeBytes = 10 << 20 // 10 MB

func (h *handlers) workspaceRoot() string {
	root := h.deps.WorkspaceRoot
	if root == "" {
		root = "workspaces"
	}
	return root
}

func validateWorkspacePath(workspace, filePath string) error {
	if workspace == "" {
		return fmt.Errorf("workspace must not be empty")
	}
	cleanWS := filepath.Clean(workspace)
	if filepath.IsAbs(cleanWS) || strings.HasPrefix(cleanWS, "..") {
		return fmt.Errorf("invalid workspace: path traversal not allowed")
	}

	if filePath != "" {
		cleanFP := filepath.Clean(filePath)
		if filepath.IsAbs(cleanFP) || strings.HasPrefix(cleanFP, "..") {
			return fmt.Errorf("invalid file_path: path traversal not allowed")
		}
	}
	return nil
}

func (h *handlers) uploadFile(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	workspace, err := request.RequireString("workspace")
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

	target := filepath.Join(h.workspaceRoot(), filepath.Clean(workspace), filepath.Clean(filePath))
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("create directory: %v", err)), nil
	}
	if err := os.WriteFile(target, decoded, 0o644); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("write file: %v", err)), nil
	}

	result := map[string]any{
		"status":    "ok",
		"workspace": workspace,
		"file_path": filePath,
		"size":      len(decoded),
	}
	jsonStr, _ := marshalJSON(result)
	return mcp.NewToolResultText(jsonStr), nil
}

func (h *handlers) listWorkspaces(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	root := h.workspaceRoot()

	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			jsonStr, _ := marshalJSON([]string{})
			return mcp.NewToolResultText(jsonStr), nil
		}
		return mcp.NewToolResultError(fmt.Sprintf("read workspace root: %v", err)), nil
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

func (h *handlers) listFiles(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	workspace, err := request.RequireString("workspace")
	if err != nil {
		return missingParamResult(err)
	}

	if err := validateWorkspacePath(workspace, ""); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	wsDir := filepath.Join(h.workspaceRoot(), filepath.Clean(workspace))

	var files []string
	err = filepath.Walk(wsDir, func(fp string, info os.FileInfo, err error) error {
		if err != nil {
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
		return mcp.NewToolResultError(fmt.Sprintf("walk workspace: %v", err)), nil
	}
	if files == nil {
		files = []string{}
	}

	jsonStr, _ := marshalJSON(files)
	return mcp.NewToolResultText(jsonStr), nil
}

func (h *handlers) deleteFile(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	workspace, err := request.RequireString("workspace")
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

	target := filepath.Join(h.workspaceRoot(), filepath.Clean(workspace), filepath.Clean(filePath))

	if _, err := os.Stat(target); os.IsNotExist(err) {
		return mcp.NewToolResultError(fmt.Sprintf("file %q not found in workspace %q", filePath, workspace)), nil
	}

	if err := os.Remove(target); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("delete file: %v", err)), nil
	}

	result := map[string]any{
		"status":    "deleted",
		"workspace": workspace,
		"file_path": filePath,
	}
	jsonStr, _ := marshalJSON(result)
	return mcp.NewToolResultText(jsonStr), nil
}

type uploadFileEntry struct {
	Workspace string `json:"workspace"`
	FilePath  string `json:"file_path"`
	Content   string `json:"content"`
}

func (h *handlers) uploadFiles(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	filesRaw, err := request.RequireString("files")
	if err != nil {
		return missingParamResult(err)
	}

	var entries []uploadFileEntry
	if err := json.Unmarshal([]byte(filesRaw), &entries); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("invalid files JSON: %v", err)), nil
	}

	if len(entries) == 0 {
		return mcp.NewToolResultError("files array must not be empty"), nil
	}

	var results []map[string]any
	for i, e := range entries {
		if e.Workspace == "" || e.FilePath == "" || e.Content == "" {
			return mcp.NewToolResultError(fmt.Sprintf("entry %d: workspace, file_path, and content are required", i)), nil
		}

		if err := validateWorkspacePath(e.Workspace, e.FilePath); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("entry %d: %v", i, err)), nil
		}

		decoded, err := base64.StdEncoding.DecodeString(e.Content)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("entry %d: invalid base64 content: %v", i, err)), nil
		}

		if len(decoded) > maxUploadSizeBytes {
			return mcp.NewToolResultError(fmt.Sprintf("entry %d: file exceeds %d MB size limit", i, maxUploadSizeBytes>>20)), nil
		}

		target := filepath.Join(h.workspaceRoot(), filepath.Clean(e.Workspace), filepath.Clean(e.FilePath))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("entry %d: create directory: %v", i, err)), nil
		}
		if err := os.WriteFile(target, decoded, 0o644); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("entry %d: write file: %v", i, err)), nil
		}

		results = append(results, map[string]any{
			"workspace": e.Workspace,
			"file_path": e.FilePath,
			"size":      len(decoded),
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

func (h *handlers) deleteWorkspace(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	workspace, err := request.RequireString("workspace")
	if err != nil {
		return missingParamResult(err)
	}

	if err := validateWorkspacePath(workspace, ""); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	wsDir := filepath.Join(h.workspaceRoot(), filepath.Clean(workspace))

	if _, err := os.Stat(wsDir); os.IsNotExist(err) {
		return mcp.NewToolResultError(fmt.Sprintf("workspace %q not found", workspace)), nil
	}

	if err := os.RemoveAll(wsDir); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("delete workspace: %v", err)), nil
	}

	delResult := map[string]any{
		"status":    "deleted",
		"workspace": workspace,
	}
	jsonStr, _ := marshalJSON(delResult)
	return mcp.NewToolResultText(jsonStr), nil
}
