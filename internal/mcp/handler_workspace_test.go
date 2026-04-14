package mcp

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func workspaceHandlers(t *testing.T) (*handlers, string) {
	t.Helper()
	root := t.TempDir()
	h := &handlers{
		deps: &Deps{
			WorkspaceRoot: root,
		},
	}
	return h, root
}

func TestUploadFile_Basic(t *testing.T) {
	h, root := workspaceHandlers(t)

	content := "# Hello World\nThis is a test."
	encoded := base64.StdEncoding.EncodeToString([]byte(content))

	req := makeCallToolRequest(t, map[string]any{
		"workspace": "my-service",
		"file_path": "docs/readme.md",
		"content":   encoded,
	})

	result, err := h.uploadFile(t.Context(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertToolResultNotError(t, result)

	written, err := os.ReadFile(filepath.Join(root, "my-service", "docs", "readme.md"))
	if err != nil {
		t.Fatalf("file not written: %v", err)
	}
	if string(written) != content {
		t.Errorf("content mismatch: got %q, want %q", string(written), content)
	}
}

func TestUploadFile_PathTraversal(t *testing.T) {
	h, _ := workspaceHandlers(t)

	encoded := base64.StdEncoding.EncodeToString([]byte("malicious"))

	tests := []struct {
		name      string
		workspace string
		filePath  string
	}{
		{"dotdot in workspace", "../evil", "file.md"},
		{"dotdot in file_path", "ok", "../../etc/passwd"},
		{"absolute workspace", "/etc", "passwd"},
		{"absolute file_path", "ok", "/etc/passwd"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := makeCallToolRequest(t, map[string]any{
				"workspace": tt.workspace,
				"file_path": tt.filePath,
				"content":   encoded,
			})
			result, err := h.uploadFile(t.Context(), req)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			assertToolResultIsError(t, result)
		})
	}
}

func TestUploadFile_InvalidBase64(t *testing.T) {
	h, _ := workspaceHandlers(t)

	req := makeCallToolRequest(t, map[string]any{
		"workspace": "my-service",
		"file_path": "file.md",
		"content":   "not-valid-base64!!!",
	})

	result, err := h.uploadFile(t.Context(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertToolResultIsError(t, result)
}

func TestListWorkspaces_Empty(t *testing.T) {
	h, _ := workspaceHandlers(t)

	req := makeCallToolRequest(t, map[string]any{})
	result, err := h.listWorkspaces(t.Context(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertToolResultNotError(t, result)

	text := extractText(result)
	var workspaces []string
	if err := json.Unmarshal([]byte(text), &workspaces); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if len(workspaces) != 0 {
		t.Errorf("expected empty list, got %v", workspaces)
	}
}

func TestListWorkspaces_WithData(t *testing.T) {
	h, root := workspaceHandlers(t)

	os.MkdirAll(filepath.Join(root, "service-a"), 0o755)
	os.MkdirAll(filepath.Join(root, "service-b"), 0o755)

	req := makeCallToolRequest(t, map[string]any{})
	result, err := h.listWorkspaces(t.Context(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text := extractText(result)
	var workspaces []string
	if err := json.Unmarshal([]byte(text), &workspaces); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if len(workspaces) != 2 {
		t.Errorf("expected 2 workspaces, got %d: %v", len(workspaces), workspaces)
	}
}

func TestListFiles_Basic(t *testing.T) {
	h, root := workspaceHandlers(t)

	wsDir := filepath.Join(root, "my-service")
	os.MkdirAll(filepath.Join(wsDir, "docs"), 0o755)
	os.WriteFile(filepath.Join(wsDir, "readme.md"), []byte("hello"), 0o644)
	os.WriteFile(filepath.Join(wsDir, "docs", "api.md"), []byte("api"), 0o644)

	req := makeCallToolRequest(t, map[string]any{
		"workspace": "my-service",
	})
	result, err := h.listFiles(t.Context(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertToolResultNotError(t, result)

	text := extractText(result)
	var files []string
	if err := json.Unmarshal([]byte(text), &files); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if len(files) != 2 {
		t.Errorf("expected 2 files, got %d: %v", len(files), files)
	}
}

func TestListFiles_PathTraversal(t *testing.T) {
	h, _ := workspaceHandlers(t)

	req := makeCallToolRequest(t, map[string]any{
		"workspace": "../evil",
	})
	result, err := h.listFiles(t.Context(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertToolResultIsError(t, result)
}

func TestDeleteFile_Basic(t *testing.T) {
	h, root := workspaceHandlers(t)

	wsDir := filepath.Join(root, "my-service")
	os.MkdirAll(wsDir, 0o755)
	filePath := filepath.Join(wsDir, "to-delete.md")
	os.WriteFile(filePath, []byte("bye"), 0o644)

	req := makeCallToolRequest(t, map[string]any{
		"workspace": "my-service",
		"file_path": "to-delete.md",
	})
	result, err := h.deleteFile(t.Context(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertToolResultNotError(t, result)

	if _, err := os.Stat(filePath); !os.IsNotExist(err) {
		t.Errorf("file should have been deleted")
	}
}

func TestDeleteFile_NotFound(t *testing.T) {
	h, _ := workspaceHandlers(t)

	req := makeCallToolRequest(t, map[string]any{
		"workspace": "my-service",
		"file_path": "nonexistent.md",
	})
	result, err := h.deleteFile(t.Context(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertToolResultIsError(t, result)
}

func TestDeleteFile_PathTraversal(t *testing.T) {
	h, _ := workspaceHandlers(t)

	req := makeCallToolRequest(t, map[string]any{
		"workspace": "ok",
		"file_path": "../../etc/passwd",
	})
	result, err := h.deleteFile(t.Context(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertToolResultIsError(t, result)
}

func makeCallToolRequest(t *testing.T, args map[string]any) mcp.CallToolRequest {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	return req
}

func assertToolResultNotError(t *testing.T, result *mcp.CallToolResult) {
	t.Helper()
	if result.IsError {
		t.Errorf("expected success, got error: %v", result.Content)
	}
}

func assertToolResultIsError(t *testing.T, result *mcp.CallToolResult) {
	t.Helper()
	if !result.IsError {
		t.Errorf("expected error result, got success: %v", result.Content)
	}
}
