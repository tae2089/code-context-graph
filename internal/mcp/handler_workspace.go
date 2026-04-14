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

// workspaceRoot returns the filesystem root used for workspace storage.
// @intent 파일 업로드 도구들이 동일한 작업공간 루트를 사용하게 한다.
// @return 설정값이 없으면 기본 workspaces 디렉터리를 반환한다.
func (h *handlers) workspaceRoot() string {
	root := h.deps.WorkspaceRoot
	if root == "" {
		root = "workspaces"
	}
	return root
}

// validateWorkspacePath validates workspace and file paths against traversal.
// @intent 작업공간 파일 조작 도구에서 경로 순회 공격을 차단한다.
// @param workspace 작업공간 이름 또는 상대 경로 세그먼트다.
// @param filePath 작업공간 내부 상대 파일 경로다.
// @domainRule workspace와 file_path는 절대 경로나 상위 디렉터리 이동을 포함할 수 없다.
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

// uploadFile writes one base64-encoded file into a workspace.
// @intent 단일 파일을 서버 작업공간에 업로드해 후속 분석 또는 문서 작업에 활용하게 한다.
// @param request content는 base64 인코딩된 파일 바이트다.
// @requires workspace와 file_path는 안전한 상대 경로여야 한다.
// @ensures 성공 시 저장된 파일 경로와 크기를 반환한다.
// @domainRule 업로드 파일은 10MB를 초과할 수 없다.
// @sideEffect 디렉터리 생성과 파일 쓰기를 수행한다.
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

// listWorkspaces lists available workspace directories.
// @intent 서버에 존재하는 작업공간 이름을 조회해 업로드 대상을 선택하게 한다.
// @ensures 성공 시 작업공간 이름 배열을 반환한다.
// @sideEffect 파일 시스템 디렉터리 읽기를 수행한다.
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

// listFiles lists all files stored inside a workspace.
// @intent 특정 작업공간의 현재 파일 구성을 확인하게 한다.
// @param request workspace는 조회할 작업공간 이름이다.
// @requires workspace는 안전한 상대 경로여야 한다.
// @ensures 성공 시 작업공간 내부 상대 파일 경로 배열을 반환한다.
// @sideEffect 파일 시스템 순회를 수행한다.
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

// deleteFile removes one file from a workspace.
// @intent 더 이상 필요 없는 작업공간 파일을 개별적으로 정리할 수 있게 한다.
// @param request workspace와 file_path로 삭제 대상을 지정한다.
// @requires 대상 파일이 해당 작업공간에 존재해야 한다.
// @ensures 성공 시 삭제된 파일 정보를 반환한다.
// @sideEffect 파일 시스템에서 실제 파일을 삭제한다.
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

// uploadFileEntry describes one file payload for bulk workspace uploads.
// @intent 다중 파일 업로드 요청의 각 항목을 역직렬화한다.
type uploadFileEntry struct {
	Workspace string `json:"workspace"`
	FilePath  string `json:"file_path"`
	Content   string `json:"content"`
}

// uploadFiles writes multiple base64-encoded files in one request.
// @intent 여러 작업공간 파일을 한 번의 MCP 호출로 업로드해 왕복 비용을 줄인다.
// @param request files는 uploadFileEntry 배열을 담은 JSON 문자열이다.
// @requires files 배열은 비어 있지 않아야 하며 각 항목이 유효해야 한다.
// @ensures 성공 시 업로드된 파일 수와 각 파일 정보를 반환한다.
// @domainRule 각 파일은 10MB를 초과할 수 없고 모든 경로는 안전해야 한다.
// @sideEffect 디렉터리 생성과 다중 파일 쓰기를 수행한다.
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

// deleteWorkspace removes an entire workspace directory tree.
// @intent 작업공간 단위로 업로드된 파일 집합을 한 번에 정리하게 한다.
// @param request workspace는 삭제할 작업공간 이름이다.
// @requires workspace는 안전한 상대 경로이며 실제로 존재해야 한다.
// @ensures 성공 시 삭제된 작업공간 이름을 반환한다.
// @sideEffect 파일 시스템에서 작업공간 디렉터리를 재귀 삭제한다.
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
