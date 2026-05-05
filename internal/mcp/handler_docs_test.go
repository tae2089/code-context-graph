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
	deps.RagIndexDir = t.TempDir()

	content := "# Test Doc\nHello world"
	docPath := filepath.Join(deps.RagIndexDir, "docs", "test-doc.md")
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

func TestGetDocContent_NoWorkspaceRejectsOutsideRagIndexDir(t *testing.T) {
	deps := setupTestDeps(t)
	tmpDir := t.TempDir()
	deps.RagIndexDir = filepath.Join(tmpDir, ".ccg")
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
		t.Fatal("expected no-workspace get_doc_content to reject paths outside RagIndexDir")
	}
}

func TestGetDocContent_NoWorkspaceRejectsSymlinkEscape(t *testing.T) {
	deps := setupTestDeps(t)
	tmpDir := t.TempDir()
	deps.RagIndexDir = filepath.Join(tmpDir, ".ccg")
	outside := t.TempDir()
	if err := os.MkdirAll(deps.RagIndexDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "secret.md"), []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(deps.RagIndexDir, "link")); err != nil {
		t.Fatal(err)
	}

	result := callTool(t, deps, "get_doc_content", map[string]any{
		"file_path": "link/secret.md",
	})
	if !result.IsError {
		t.Fatal("expected get_doc_content to reject symlink escape under RagIndexDir")
	}
}

func TestGetRagTree_InvalidNodeID(t *testing.T) {
	deps := setupTestDeps(t)
	deps.RagIndexDir = t.TempDir()

	buildResult := callTool(t, deps, "build_rag_index", map[string]any{})
	if buildResult.IsError {
		t.Fatalf("build_rag_index error: %v", buildResult.Content)
	}

	result := callTool(t, deps, "get_rag_tree", map[string]any{
		"node_id": "package:missing",
	})
	if !result.IsError {
		t.Fatal("expected error for nonexistent node_id")
	}
}

func TestGetRagTree_CommunityIDAlias(t *testing.T) {
	deps := setupTestDeps(t)
	deps.RagIndexDir = t.TempDir()

	buildResult := callTool(t, deps, "build_rag_index", map[string]any{})
	if buildResult.IsError {
		t.Fatalf("build_rag_index error: %v", buildResult.Content)
	}

	result := callTool(t, deps, "get_rag_tree", map[string]any{
		"community_id": "root",
	})
	if result.IsError {
		t.Fatalf("get_rag_tree community_id alias error: %v", getTextContent(result))
	}
}

func TestGetRagTree_RejectsInvalidWorkspace(t *testing.T) {
	deps := setupTestDeps(t)
	result := callTool(t, deps, "get_rag_tree", map[string]any{"workspace": "../outside"})
	if !result.IsError {
		t.Fatal("expected get_rag_tree to reject invalid workspace")
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

func TestRetrieveDocs_ReturnsDocumentContentAndEvidence(t *testing.T) {
	deps := setupTestDeps(t)
	tmpDir := t.TempDir()
	docsDir := filepath.Join(tmpDir, "docs")
	deps.RagIndexDir = filepath.Join(tmpDir, ".ccg")

	comm := model.Community{Key: "analysis", Label: "Analysis", Description: "analysis tools"}
	if err := deps.DB.Create(&comm).Error; err != nil {
		t.Fatalf("create community: %v", err)
	}
	findPage := model.Node{QualifiedName: "deadcode.Service.FindPage", Kind: model.NodeKindFunction, Name: "FindPage", FilePath: "internal/analysis/deadcode/service.go", StartLine: 1, EndLine: 20, Language: "go"}
	normalize := model.Node{QualifiedName: "deadcode.normalizePathPrefix", Kind: model.NodeKindFunction, Name: "normalizePathPrefix", FilePath: "internal/analysis/deadcode/service.go", StartLine: 22, EndLine: 30, Language: "go"}
	if err := deps.DB.Create(&findPage).Error; err != nil {
		t.Fatalf("create findPage: %v", err)
	}
	if err := deps.DB.Create(&normalize).Error; err != nil {
		t.Fatalf("create normalize: %v", err)
	}
	for _, node := range []model.Node{findPage, normalize} {
		if err := deps.DB.Create(&model.CommunityMembership{CommunityID: comm.ID, NodeID: node.ID}).Error; err != nil {
			t.Fatalf("create membership: %v", err)
		}
		ann := model.Annotation{NodeID: node.ID}
		if err := deps.DB.Create(&ann).Error; err != nil {
			t.Fatalf("create annotation: %v", err)
		}
		if err := deps.DB.Create(&model.DocTag{AnnotationID: ann.ID, Kind: model.TagIntent, Value: node.Name + " intent", Ordinal: 0}).Error; err != nil {
			t.Fatalf("create doc tag: %v", err)
		}
	}

	docPath := filepath.Join(docsDir, "internal/analysis/deadcode/service.go.md")
	if err := os.MkdirAll(filepath.Dir(docPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(docPath, []byte("# service.go\n\nFindPage calls normalizePathPrefix for path filtering.\n"), 0644); err != nil {
		t.Fatal(err)
	}

	b := &ragindex.Builder{DB: deps.DB, IndexDir: deps.RagIndexDir, OutDir: docsDir}
	if _, _, err := b.Build(context.Background()); err != nil {
		t.Fatalf("Build: %v", err)
	}

	result := callTool(t, deps, "retrieve_docs", map[string]any{
		"query":         "FindPage normalizePathPrefix",
		"limit":         float64(5),
		"content_limit": float64(2000),
	})
	if result.IsError {
		t.Fatalf("retrieve_docs error: %v", getTextContent(result))
	}

	var response retrieveDocsResponse
	if err := json.Unmarshal([]byte(getTextContent(result)), &response); err != nil {
		t.Fatalf("unmarshal retrieve response: %v", err)
	}
	if len(response.Results) != 1 {
		t.Fatalf("results = %d, want 1: %#v", len(response.Results), response.Results)
	}
	got := response.Results[0]
	if !strings.Contains(got.Content, "FindPage calls normalizePathPrefix") {
		t.Fatalf("content missing expected text: %q", got.Content)
	}
	if len(got.MatchedTerms) != 2 {
		t.Fatalf("matched_terms = %#v, want both terms", got.MatchedTerms)
	}
	if len(got.Matches) < 2 {
		t.Fatalf("expected evidence matches for both symbols, got %#v", got.Matches)
	}
}

func TestRetrieveDocs_ContentLimitZeroOmitsContent(t *testing.T) {
	deps := setupTestDeps(t)
	tmpDir := t.TempDir()
	docsDir := filepath.Join(tmpDir, "docs")
	deps.RagIndexDir = filepath.Join(tmpDir, ".ccg")

	comm := model.Community{Key: "auth", Label: "Auth"}
	if err := deps.DB.Create(&comm).Error; err != nil {
		t.Fatalf("create community: %v", err)
	}
	node := model.Node{QualifiedName: "auth.Login", Kind: model.NodeKindFunction, Name: "Login", FilePath: "auth/login.go", StartLine: 1, EndLine: 10, Language: "go"}
	if err := deps.DB.Create(&node).Error; err != nil {
		t.Fatalf("create node: %v", err)
	}
	if err := deps.DB.Create(&model.CommunityMembership{CommunityID: comm.ID, NodeID: node.ID}).Error; err != nil {
		t.Fatalf("create membership: %v", err)
	}
	docPath := filepath.Join(docsDir, "auth/login.go.md")
	if err := os.MkdirAll(filepath.Dir(docPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(docPath, []byte("# login\n"), 0644); err != nil {
		t.Fatal(err)
	}
	b := &ragindex.Builder{DB: deps.DB, IndexDir: deps.RagIndexDir, OutDir: docsDir}
	if _, _, err := b.Build(context.Background()); err != nil {
		t.Fatalf("Build: %v", err)
	}

	result := callTool(t, deps, "retrieve_docs", map[string]any{"query": "login", "content_limit": float64(0)})
	if result.IsError {
		t.Fatalf("retrieve_docs error: %v", getTextContent(result))
	}
	if strings.Contains(getTextContent(result), "# login") {
		t.Fatalf("content should be omitted when content_limit=0: %s", getTextContent(result))
	}
}

func TestRetrieveDocs_RejectsLimitAboveMax(t *testing.T) {
	deps := setupTestDeps(t)
	result := callTool(t, deps, "retrieve_docs", map[string]any{"query": "auth", "limit": float64(51)})
	if !result.IsError {
		t.Fatal("expected retrieve_docs to reject limit above max")
	}
}

func TestSearchDocs_RejectsInvalidWorkspace(t *testing.T) {
	deps := setupTestDeps(t)
	result := callTool(t, deps, "search_docs", map[string]any{"query": "auth", "workspace": "../outside"})
	if !result.IsError {
		t.Fatal("expected search_docs to reject invalid workspace")
	}
}

func TestSearchDocs_RejectsLimitAboveMax(t *testing.T) {
	deps := setupTestDeps(t)
	result := callTool(t, deps, "search_docs", map[string]any{"query": "auth", "limit": float64(501)})
	if !result.IsError {
		t.Fatal("expected search_docs to reject limit above max")
	}
	if !strings.Contains(getTextContent(result), "limit must be <= 500") {
		t.Fatalf("unexpected error: %s", getTextContent(result))
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

func TestRetrieveDocs_WithWorkspaceReadsWorkspaceRelativeDocPath(t *testing.T) {
	deps := setupTestDeps(t)
	tmpDir := t.TempDir()
	deps.WorkspaceRoot = filepath.Join(tmpDir, "workspaces")
	deps.RagIndexDir = filepath.Join(tmpDir, ".ccg")

	ws := "my-service"
	wsDir := filepath.Join(deps.WorkspaceRoot, ws)
	docPath := filepath.Join(wsDir, "docs", "service.go.md")
	if err := os.MkdirAll(filepath.Dir(docPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(docPath, []byte("# service.go\n\nadmin audit trail docs\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	comm := model.Community{Namespace: ws, Key: "svc", Label: "Service"}
	if err := deps.DB.Create(&comm).Error; err != nil {
		t.Fatalf("create community: %v", err)
	}
	node := model.Node{
		Namespace:     ws,
		QualifiedName: "service.Check",
		Kind:          model.NodeKindFunction,
		Name:          "Check",
		FilePath:      "service.go",
		StartLine:     1,
		EndLine:       10,
		Language:      "go",
	}
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
	if err := deps.DB.Create(&model.DocTag{AnnotationID: ann.ID, Kind: model.TagDomainRule, Value: "admin audit", Ordinal: 0}).Error; err != nil {
		t.Fatalf("create doc tag: %v", err)
	}

	build := callTool(t, deps, "build_rag_index", map[string]any{"workspace": ws})
	if build.IsError {
		t.Fatalf("build_rag_index with workspace error: %v", getTextContent(build))
	}

	treeResult := callTool(t, deps, "get_rag_tree", map[string]any{"workspace": ws})
	if treeResult.IsError {
		t.Fatalf("get_rag_tree with workspace error: %v", getTextContent(treeResult))
	}
	var root ragindex.TreeNode
	if err := json.Unmarshal([]byte(getTextContent(treeResult)), &root); err != nil {
		t.Fatalf("unmarshal tree: %v", err)
	}
	fileNode := ragindex.FindNode(&root, "file:service.go")
	if fileNode == nil {
		t.Fatal("expected file node")
	}
	if fileNode.DocPath != "docs/service.go.md" {
		t.Fatalf("doc_path = %q, want docs/service.go.md", fileNode.DocPath)
	}

	result := callTool(t, deps, "retrieve_docs", map[string]any{
		"workspace":     ws,
		"query":         "admin audit",
		"limit":         float64(5),
		"content_limit": float64(2000),
	})
	if result.IsError {
		t.Fatalf("retrieve_docs with workspace error: %v", getTextContent(result))
	}
	var response retrieveDocsResponse
	if err := json.Unmarshal([]byte(getTextContent(result)), &response); err != nil {
		t.Fatalf("unmarshal retrieve response: %v", err)
	}
	if len(response.Results) != 1 {
		t.Fatalf("results = %d, want 1", len(response.Results))
	}
	if !strings.Contains(response.Results[0].Content, "admin audit trail docs") {
		t.Fatalf("content = %q", response.Results[0].Content)
	}
}

func TestBuildRagIndex_NoWorkspaceRejectsIndexDirOutsideSafeRoot(t *testing.T) {
	deps := setupTestDeps(t)
	tmpDir := t.TempDir()
	deps.RagIndexDir = filepath.Join(tmpDir, ".ccg")
	outside := filepath.Join(tmpDir, "outside-index")

	result := callTool(t, deps, "build_rag_index", map[string]any{"index_dir": outside})
	if !result.IsError {
		t.Fatal("expected build_rag_index to reject index_dir outside RagIndexDir")
	}
}

func TestBuildRagIndex_WorkspaceRejectsIndexDirOutsideSafeRoot(t *testing.T) {
	deps := setupTestDeps(t)
	tmpDir := t.TempDir()
	deps.WorkspaceRoot = filepath.Join(tmpDir, "workspaces")
	deps.RagIndexDir = filepath.Join(tmpDir, ".ccg")
	if err := os.MkdirAll(filepath.Join(deps.WorkspaceRoot, "my-service"), 0o755); err != nil {
		t.Fatal(err)
	}

	result := callTool(t, deps, "build_rag_index", map[string]any{
		"workspace": "my-service",
		"index_dir": filepath.Join(tmpDir, "outside-index"),
	})
	if !result.IsError {
		t.Fatal("expected workspace build_rag_index to reject index_dir outside RagIndexDir")
	}
}

func TestBuildRagIndex_RejectsIndexDirSymlinkEscape(t *testing.T) {
	deps := setupTestDeps(t)
	tmpDir := t.TempDir()
	deps.RagIndexDir = filepath.Join(tmpDir, ".ccg")
	outside := t.TempDir()
	if err := os.MkdirAll(deps.RagIndexDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(deps.RagIndexDir, "link")); err != nil {
		t.Fatal(err)
	}

	result := callTool(t, deps, "build_rag_index", map[string]any{"index_dir": "link"})
	if !result.IsError {
		t.Fatal("expected build_rag_index to reject symlink escape under RagIndexDir")
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
