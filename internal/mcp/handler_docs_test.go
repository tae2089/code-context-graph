package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tae2089/code-context-graph/internal/model"
	"github.com/tae2089/code-context-graph/internal/ragindex"
)

func TestBuildRagIndex_ReturnsCount(t *testing.T) {
	deps := setupTestDeps(t)
	deps.RagIndexDir = t.TempDir()
	result := callTool(t, deps, "build_rag_index", map[string]any{})
	if result.IsError {
		t.Fatalf("build_rag_index error: %v", result.Content)
	}
	content := getTextContent(result)
	if !strings.Contains(content, "Built doc-index:") {
		t.Errorf("expected 'Built doc-index:' in output, got: %s", content)
	}
}

func TestGetRagTree_AfterBuild(t *testing.T) {
	deps := setupTestDeps(t)
	deps.RagIndexDir = t.TempDir()

	buildResult := callTool(t, deps, "build_rag_index", map[string]any{})
	if buildResult.IsError {
		t.Fatalf("build_rag_index error: %v", buildResult.Content)
	}

	result := callTool(t, deps, "get_rag_tree", map[string]any{})
	if result.IsError {
		t.Fatalf("get_rag_tree error: %v", result.Content)
	}
	content := getTextContent(result)
	if content == "" {
		t.Error("expected non-empty tree result")
	}
}

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

	tmpFile, err := os.CreateTemp(".", "test-doc-*.md")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())
	content := "# Test Doc\nHello world"
	if _, err := tmpFile.WriteString(content); err != nil {
		t.Fatal(err)
	}
	tmpFile.Close()

	result := callTool(t, deps, "get_doc_content", map[string]any{
		"file_path": tmpFile.Name(),
	})
	if result.IsError {
		t.Fatalf("unexpected error: %v", result.Content)
	}
	got := getTextContent(result)
	if got != content {
		t.Errorf("want %q, got %q", content, got)
	}
}

func TestGetRagTree_InvalidCommunityID(t *testing.T) {
	deps := setupTestDeps(t)
	deps.RagIndexDir = t.TempDir()

	buildResult := callTool(t, deps, "build_rag_index", map[string]any{})
	if buildResult.IsError {
		t.Fatalf("build_rag_index error: %v", buildResult.Content)
	}

	result := callTool(t, deps, "get_rag_tree", map[string]any{
		"community_id": "community:99999",
	})
	if !result.IsError {
		t.Fatal("expected error for nonexistent community_id")
	}
}

func TestGetRagTree_DepthLimitsChildren(t *testing.T) {
	deps := setupTestDeps(t)

	tmpDir := t.TempDir()
	deps.RagIndexDir = filepath.Join(tmpDir, ".ccg")

	community := model.Community{Key: "auth", Label: "Auth Community", Strategy: "auto"}
	if err := deps.DB.Create(&community).Error; err != nil {
		t.Fatalf("create community: %v", err)
	}

	node := model.Node{
		QualifiedName: "auth.Login",
		Kind:          model.NodeKindFunction,
		Name:          "Login",
		FilePath:      "internal/auth/login.go",
		StartLine:     1,
		EndLine:       10,
		Language:      "go",
	}
	if err := deps.DB.Create(&node).Error; err != nil {
		t.Fatalf("create node: %v", err)
	}

	membership := model.CommunityMembership{CommunityID: community.ID, NodeID: node.ID}
	if err := deps.DB.Create(&membership).Error; err != nil {
		t.Fatalf("create membership: %v", err)
	}

	b := &ragindex.Builder{DB: deps.DB, OutDir: filepath.Join(tmpDir, "docs"), IndexDir: deps.RagIndexDir}
	if _, _, err := b.Build(context.Background()); err != nil {
		t.Fatalf("Build: %v", err)
	}

	result := callTool(t, deps, "get_rag_tree", map[string]any{"depth": float64(1)})
	if result.IsError {
		t.Fatalf("get_rag_tree error: %v", getTextContent(result))
	}

	var treeNode ragindex.TreeNode
	if err := json.Unmarshal([]byte(getTextContent(result)), &treeNode); err != nil {
		t.Fatalf("unmarshal tree: %v", err)
	}

	if len(treeNode.Children) == 0 {
		t.Fatal("expected community nodes at depth=1, got none")
	}
	communityNode := treeNode.Children[0]
	if len(communityNode.Children) != 0 {
		t.Fatalf("expected 0 file children at depth=1, got %d", len(communityNode.Children))
	}
}

func TestSearchDocs_ReturnsMatches(t *testing.T) {
	deps := setupTestDeps(t)
	tmpDir := t.TempDir()
	deps.RagIndexDir = tmpDir

	comm := model.Community{Key: "auth", Label: "Auth Service", Description: "인증 레이어"}
	if err := deps.DB.Create(&comm).Error; err != nil {
		t.Fatalf("create community: %v", err)
	}
	node := model.Node{QualifiedName: "auth/handler.go/Login", Kind: model.NodeKindFunction, Name: "Login", FilePath: "auth/handler.go", StartLine: 1, EndLine: 20, Language: "go"}
	if err := deps.DB.Create(&node).Error; err != nil {
		t.Fatalf("create node: %v", err)
	}
	if err := deps.DB.Create(&model.CommunityMembership{CommunityID: comm.ID, NodeID: node.ID}).Error; err != nil {
		t.Fatalf("create membership: %v", err)
	}
	ann := model.Annotation{NodeID: node.ID}
	if err := deps.DB.Create(&ann).Error; err != nil {
		t.Fatalf("create annotation: %v", err)
	}
	if err := deps.DB.Create(&model.DocTag{AnnotationID: ann.ID, Kind: model.TagIndex, Value: "Auth 서비스 핸들러", Ordinal: 0}).Error; err != nil {
		t.Fatalf("create doc tag: %v", err)
	}

	b := &ragindex.Builder{DB: deps.DB, IndexDir: tmpDir, OutDir: filepath.Join(tmpDir, "docs")}
	if _, _, err := b.Build(context.Background()); err != nil {
		t.Fatalf("Build: %v", err)
	}

	result := callTool(t, deps, "search_docs", map[string]any{"query": "auth", "limit": float64(10)})
	if result.IsError {
		t.Fatalf("search_docs error: %v", getTextContent(result))
	}

	var results []ragindex.SearchResult
	if err := json.Unmarshal([]byte(getTextContent(result)), &results); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least 1 search result")
	}
}

func TestSearchDocs_MissingQuery(t *testing.T) {
	deps := setupTestDeps(t)
	result := callTool(t, deps, "search_docs", map[string]any{})
	if !result.IsError {
		t.Fatal("expected error for missing query")
	}
}

func TestSearchDocs_NoIndex(t *testing.T) {
	deps := setupTestDeps(t)
	deps.RagIndexDir = t.TempDir()
	result := callTool(t, deps, "search_docs", map[string]any{"query": "something"})
	if !result.IsError {
		t.Fatal("expected error when index file missing")
	}
}

func TestBuildRagIndex_WithWorkspace(t *testing.T) {
	deps := setupTestDeps(t)
	tmpDir := t.TempDir()
	deps.WorkspaceRoot = filepath.Join(tmpDir, "workspaces")
	deps.RagIndexDir = filepath.Join(tmpDir, ".ccg")

	wsDocsDir := filepath.Join(tmpDir, "workspaces", "my-service")
	if err := os.MkdirAll(wsDocsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	result := callTool(t, deps, "build_rag_index", map[string]any{"workspace": "my-service"})
	if result.IsError {
		t.Fatalf("build_rag_index with workspace error: %v", getTextContent(result))
	}
	content := getTextContent(result)
	if !strings.Contains(content, "Built doc-index:") {
		t.Errorf("expected 'Built doc-index:' in output, got: %s", content)
	}
}

func TestGetDocContent_WithWorkspace(t *testing.T) {
	deps := setupTestDeps(t)
	tmpDir := t.TempDir()
	deps.WorkspaceRoot = filepath.Join(tmpDir, "workspaces")

	wsDir := filepath.Join(tmpDir, "workspaces", "my-service")
	docsDir := filepath.Join(wsDir, "docs", "internal")
	if err := os.MkdirAll(docsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	docContent := "# Handler Docs\nThis is workspace-aware doc content."
	docPath := filepath.Join(docsDir, "handler.go.md")
	if err := os.WriteFile(docPath, []byte(docContent), 0o644); err != nil {
		t.Fatal(err)
	}

	result := callTool(t, deps, "get_doc_content", map[string]any{"workspace": "my-service", "file_path": "docs/internal/handler.go.md"})
	if result.IsError {
		t.Fatalf("get_doc_content with workspace error: %v", getTextContent(result))
	}
	got := getTextContent(result)
	if got != docContent {
		t.Errorf("want %q, got %q", docContent, got)
	}
}

func TestGetDocContent_WorkspacePathTraversal(t *testing.T) {
	deps := setupTestDeps(t)
	tmpDir := t.TempDir()
	deps.WorkspaceRoot = filepath.Join(tmpDir, "workspaces")

	cases := []struct {
		name      string
		workspace string
		filePath  string
	}{
		{"workspace traversal", "../evil", "file.md"},
		{"file_path traversal", "my-service", "../../etc/passwd"},
		{"absolute workspace", "/etc", "passwd"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := callTool(t, deps, "get_doc_content", map[string]any{"workspace": tc.workspace, "file_path": tc.filePath})
			if !result.IsError {
				t.Fatalf("expected error for workspace=%q file_path=%q", tc.workspace, tc.filePath)
			}
		})
	}
}

func TestSearchDocs_WithWorkspace(t *testing.T) {
	deps := setupTestDeps(t)
	tmpDir := t.TempDir()
	deps.RagIndexDir = tmpDir

	wsIndexDir := filepath.Join(tmpDir, "my-service")
	if err := os.MkdirAll(wsIndexDir, 0o755); err != nil {
		t.Fatal(err)
	}

	idx := &ragindex.Index{Root: &ragindex.TreeNode{ID: "root", Label: "project", Children: []*ragindex.TreeNode{{ID: "community:auth", Label: "auth", Summary: "authentication module"}}}}
	idxBytes, _ := json.Marshal(idx)
	if err := os.WriteFile(filepath.Join(wsIndexDir, "doc-index.json"), idxBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	result := callTool(t, deps, "search_docs", map[string]any{"query": "auth", "workspace": "my-service"})
	if result.IsError {
		t.Fatalf("search_docs with workspace error: %v", getTextContent(result))
	}
	got := getTextContent(result)
	if !strings.Contains(got, "auth") {
		t.Errorf("expected result containing 'auth', got %q", got)
	}
}

func TestGetRagTree_WithWorkspace(t *testing.T) {
	deps := setupTestDeps(t)
	tmpDir := t.TempDir()
	deps.RagIndexDir = tmpDir

	wsIndexDir := filepath.Join(tmpDir, "my-service")
	if err := os.MkdirAll(wsIndexDir, 0o755); err != nil {
		t.Fatal(err)
	}

	idx := &ragindex.Index{Root: &ragindex.TreeNode{ID: "root", Label: "project", Children: []*ragindex.TreeNode{{ID: "community:payments", Label: "payments", Summary: "payment processing"}}}}
	idxBytes, _ := json.Marshal(idx)
	if err := os.WriteFile(filepath.Join(wsIndexDir, "doc-index.json"), idxBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	result := callTool(t, deps, "get_rag_tree", map[string]any{"workspace": "my-service"})
	if result.IsError {
		t.Fatalf("get_rag_tree with workspace error: %v", getTextContent(result))
	}
	got := getTextContent(result)
	if !strings.Contains(got, "payments") {
		t.Errorf("expected result containing 'payments', got %q", got)
	}
}
