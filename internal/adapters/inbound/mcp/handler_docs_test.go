package mcp

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGetDocContent_PathTraversal(t *testing.T) {
	deps := setupTestDeps(t)

	cases := []struct {
		name string
		path string
	}{
		{"relative traversal", "../../etc/passwd"},
		{"absolute path", "/etc/passwd"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := callTool(t, deps, "get_doc_content", map[string]any{
				"file_path": tc.path,
			})
			if !result.IsError {
				t.Fatalf("expected error for path %q, got success", tc.path)
			}
		})
	}
}

func TestGetDocContent_DefaultNamespaceReadsSharedDocs(t *testing.T) {
	deps := setupTestDeps(t)
	deps.Runtime.RagIndexDir = t.TempDir()

	content := "# Shared Doc\nfrom shared docs root"
	docPath := filepath.Join(deps.Runtime.RagIndexDir, "docs", "shared-doc.md")
	if err := os.MkdirAll(filepath.Dir(docPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(docPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	// "default" must resolve to the shared docs root, matching resolvedRagIndexPath
	// and DB-backed documentation search (contentNamespace maps default to shared), not namespaces/default/.
	result := callTool(t, deps, "get_doc_content", map[string]any{
		"namespace": "default",
		"file_path": "docs/shared-doc.md",
	})
	if result.IsError {
		t.Fatalf("default namespace should read shared docs, got error: %v", getTextContent(result))
	}
	if got := getTextContent(result); got != content {
		t.Errorf("want %q, got %q", content, got)
	}
}

func TestGetDocContent_NotFound(t *testing.T) {
	deps := setupTestDeps(t)
	result := callTool(t, deps, "get_doc_content", map[string]any{
		"file_path": "docs/nonexistent.go.md",
	})
	if !result.IsError {
		t.Fatal("expected error for nonexistent file")
	}
}

func TestGetDocContent_HappyPath(t *testing.T) {
	deps := setupTestDeps(t)
	deps.Runtime.RagIndexDir = t.TempDir()

	content := "# Test Doc\nHello world"
	docPath := filepath.Join(deps.Runtime.RagIndexDir, "docs", "test-doc.md")
	if err := os.MkdirAll(filepath.Dir(docPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(docPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	result := callTool(t, deps, "get_doc_content", map[string]any{
		"file_path": "docs/test-doc.md",
	})
	if result.IsError {
		t.Fatalf("unexpected error: %v", result.Content)
	}
	got := getTextContent(result)
	if got != content {
		t.Errorf("want %q, got %q", content, got)
	}
}

func TestGetDocContent_NoNamespaceRejectsOutsideRagIndexDir(t *testing.T) {
	deps := setupTestDeps(t)
	tmpDir := t.TempDir()
	deps.Runtime.RagIndexDir = filepath.Join(tmpDir, ".ccg")
	outside := filepath.Join(tmpDir, "docs", "outside.md")
	if err := os.MkdirAll(filepath.Dir(outside), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(tmpDir)

	result := callTool(t, deps, "get_doc_content", map[string]any{
		"file_path": "docs/outside.md",
	})
	if !result.IsError {
		t.Fatal("expected no-namespace get_doc_content to reject paths outside RagIndexDir")
	}
}

func TestGetDocContent_NoNamespaceRejectsSymlinkEscape(t *testing.T) {
	deps := setupTestDeps(t)
	tmpDir := t.TempDir()
	deps.Runtime.RagIndexDir = filepath.Join(tmpDir, ".ccg")
	outside := t.TempDir()
	if err := os.MkdirAll(deps.Runtime.RagIndexDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "secret.md"), []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(deps.Runtime.RagIndexDir, "link")); err != nil {
		t.Fatal(err)
	}

	result := callTool(t, deps, "get_doc_content", map[string]any{
		"file_path": "link/secret.md",
	})
	if !result.IsError {
		t.Fatal("expected get_doc_content to reject symlink escape under RagIndexDir")
	}
}

func TestGetDocContent_WithNamespace(t *testing.T) {
	deps := setupTestDeps(t)
	tmpDir := t.TempDir()
	deps.Runtime.NamespaceRoot = filepath.Join(tmpDir, "namespaces")

	nsDir := filepath.Join(tmpDir, "namespaces", "my-service")
	docsDir := filepath.Join(nsDir, "docs", "internal")
	if err := os.MkdirAll(docsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	docContent := "# Handler Docs\nThis is namespace-aware doc content."
	docPath := filepath.Join(docsDir, "handler.go.md")
	if err := os.WriteFile(docPath, []byte(docContent), 0o644); err != nil {
		t.Fatal(err)
	}

	result := callTool(t, deps, "get_doc_content", map[string]any{"namespace": "my-service", "file_path": "docs/internal/handler.go.md"})
	if result.IsError {
		t.Fatalf("get_doc_content with namespace error: %v", getTextContent(result))
	}
	got := getTextContent(result)
	if got != docContent {
		t.Errorf("want %q, got %q", docContent, got)
	}
}

func TestGetDocContent_NamespacePathTraversal(t *testing.T) {
	deps := setupTestDeps(t)
	tmpDir := t.TempDir()
	deps.Runtime.NamespaceRoot = filepath.Join(tmpDir, "namespaces")

	cases := []struct {
		name      string
		namespace string
		filePath  string
	}{
		{"namespace traversal", "../evil", "file.md"},
		{"file_path traversal", "my-service", "../../etc/passwd"},
		{"absolute namespace", "/etc", "passwd"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := callTool(t, deps, "get_doc_content", map[string]any{"namespace": tc.namespace, "file_path": tc.filePath})
			if !result.IsError {
				t.Fatalf("expected error for namespace=%q file_path=%q", tc.namespace, tc.filePath)
			}
		})
	}
}
