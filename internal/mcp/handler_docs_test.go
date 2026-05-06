package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/tae2089/code-context-graph/internal/ctxns"
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

func TestGetRagTree_DBFallbackRootSuccess(t *testing.T) {
	deps := setupTestDeps(t)
	deps.RagIndexDir = filepath.Join(t.TempDir(), ".ccg")
	seedRagTreeDBFallbackCommunity(t, deps, ctxns.DefaultNamespace, "billing", "Billing", "billing docs", "billing.Processor", "Processor", "internal/billing/processor.go")

	result := callTool(t, deps, "get_rag_tree", map[string]any{})
	if result.IsError {
		t.Fatalf("get_rag_tree DB fallback error: %v", getTextContent(result))
	}

	root := decodeRagTreeNode(t, result)
	if root.ID != "root" || root.Kind != "root" {
		t.Fatalf("unexpected root shape: %#v", root)
	}
	if ragindex.FindNode(root, "community:billing") == nil {
		t.Fatalf("expected DB community node in fallback tree: %#v", root)
	}
	if ragindex.FindNode(root, "file:internal/billing/processor.go") == nil {
		t.Fatalf("expected DB file node in fallback tree: %#v", root)
	}
}

func TestGetRagTree_DBFallbackNodeIDLookup(t *testing.T) {
	deps := setupTestDeps(t)
	deps.RagIndexDir = filepath.Join(t.TempDir(), ".ccg")
	seedRagTreeDBFallbackCommunity(t, deps, ctxns.DefaultNamespace, "billing", "Billing", "billing docs", "billing.Processor", "Processor", "internal/billing/processor.go")

	result := callTool(t, deps, "get_rag_tree", map[string]any{"node_id": "file:internal/billing/processor.go"})
	if result.IsError {
		t.Fatalf("get_rag_tree DB fallback node lookup error: %v", getTextContent(result))
	}

	node := decodeRagTreeNode(t, result)
	if node.ID != "file:internal/billing/processor.go" || node.Kind != "file" || node.DocPath != "docs/internal/billing/processor.go.md" {
		t.Fatalf("unexpected file node: %#v", node)
	}
}

func TestGetRagTree_DBFallbackDepthPruning(t *testing.T) {
	deps := setupTestDeps(t)
	deps.RagIndexDir = filepath.Join(t.TempDir(), ".ccg")
	seedRagTreeDBFallbackCommunity(t, deps, ctxns.DefaultNamespace, "billing", "Billing", "billing docs", "billing.Processor", "Processor", "internal/billing/processor.go")

	result := callTool(t, deps, "get_rag_tree", map[string]any{"depth": float64(1)})
	if result.IsError {
		t.Fatalf("get_rag_tree DB fallback depth error: %v", getTextContent(result))
	}

	root := decodeRagTreeNode(t, result)
	if len(root.Children) != 1 {
		t.Fatalf("root children = %d, want 1: %#v", len(root.Children), root)
	}
	if len(root.Children[0].Children) != 0 {
		t.Fatalf("expected community children pruned at depth=1, got %d", len(root.Children[0].Children))
	}
}

func TestGetRagTree_DBFallbackInvalidNodeID(t *testing.T) {
	deps := setupTestDeps(t)
	deps.RagIndexDir = filepath.Join(t.TempDir(), ".ccg")
	seedRagTreeDBFallbackCommunity(t, deps, ctxns.DefaultNamespace, "billing", "Billing", "billing docs", "billing.Processor", "Processor", "internal/billing/processor.go")

	result := callTool(t, deps, "get_rag_tree", map[string]any{"node_id": "file:missing.go"})
	if !result.IsError {
		t.Fatal("expected DB fallback error for nonexistent node_id")
	}
}

func TestGetRagTree_DBFallbackNamespaceIsolation(t *testing.T) {
	deps := setupTestDeps(t)
	tmpDir := t.TempDir()
	deps.NamespaceRoot = filepath.Join(tmpDir, "namespaces")
	deps.RagIndexDir = filepath.Join(tmpDir, ".ccg")
	seedRagTreeDBFallbackCommunity(t, deps, "alpha-service", "alpha", "Alpha", "alpha docs", "alpha.Checkout", "Checkout", "checkout.go")
	seedRagTreeDBFallbackCommunity(t, deps, "beta-service", "beta", "Beta", "beta docs", "beta.Checkout", "Checkout", "checkout.go")

	result := callTool(t, deps, "get_rag_tree", map[string]any{"namespace": "alpha-service"})
	if result.IsError {
		t.Fatalf("get_rag_tree DB fallback namespace error: %v", getTextContent(result))
	}

	root := decodeRagTreeNode(t, result)
	if ragindex.FindNode(root, "community:alpha") == nil {
		t.Fatalf("expected alpha community: %#v", root)
	}
	if ragindex.FindNode(root, "community:beta") != nil {
		t.Fatalf("beta community leaked into alpha namespace: %#v", root)
	}
}

func TestGetRagTree_DBFallbackCommunityIDAlias(t *testing.T) {
	deps := setupTestDeps(t)
	deps.RagIndexDir = filepath.Join(t.TempDir(), ".ccg")
	seedRagTreeDBFallbackCommunity(t, deps, ctxns.DefaultNamespace, "billing", "Billing", "billing docs", "billing.Processor", "Processor", "internal/billing/processor.go")

	result := callTool(t, deps, "get_rag_tree", map[string]any{"community_id": "community:billing"})
	if result.IsError {
		t.Fatalf("get_rag_tree DB fallback community_id alias error: %v", getTextContent(result))
	}

	node := decodeRagTreeNode(t, result)
	if node.ID != "community:billing" || node.Kind != "community" {
		t.Fatalf("unexpected community alias node: %#v", node)
	}
}

func TestGetRagTree_UsesDBWhenDocIndexFileExists(t *testing.T) {
	deps := setupTestDeps(t)
	tmpDir := t.TempDir()
	deps.RagIndexDir = filepath.Join(tmpDir, ".ccg")
	seedRagTreeDBFallbackCommunity(t, deps, ctxns.DefaultNamespace, "db", "DB", "db docs", "db.Only", "Only", "internal/db/only.go")

	idx := &ragindex.Index{Root: &ragindex.TreeNode{ID: "root", Label: "Root", Kind: "root", Children: []*ragindex.TreeNode{{ID: "community:json", Label: "JSON Wins", Kind: "community", Summary: "json index", Children: []*ragindex.TreeNode{}}}}}
	if err := os.MkdirAll(deps.RagIndexDir, 0o755); err != nil {
		t.Fatal(err)
	}
	idxBytes, err := json.Marshal(idx)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(deps.RagIndexDir, "doc-index.json"), idxBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	result := callTool(t, deps, "get_rag_tree", map[string]any{})
	if result.IsError {
		t.Fatalf("get_rag_tree DB-first tree error: %v", getTextContent(result))
	}

	root := decodeRagTreeNode(t, result)
	if ragindex.FindNode(root, "community:db") == nil {
		t.Fatalf("expected DB community despite doc-index.json: %#v", root)
	}
	if ragindex.FindNode(root, "community:json") != nil {
		t.Fatalf("doc-index community should not be used when DB tree exists: %#v", root)
	}
}

func TestGetRagTree_DefaultNamespaceUsesSharedDBTree(t *testing.T) {
	deps := setupTestDeps(t)
	tmpDir := t.TempDir()
	deps.RagIndexDir = filepath.Join(tmpDir, ".ccg")
	seedRagTreeDBFallbackCommunity(t, deps, ctxns.DefaultNamespace, "db", "DB", "db docs", "db.Only", "Only", "internal/db/only.go")

	idx := &ragindex.Index{Root: &ragindex.TreeNode{ID: "root", Label: "Root", Kind: "root", Children: []*ragindex.TreeNode{{ID: "community:json-default", Label: "JSON Default", Kind: "community", Children: []*ragindex.TreeNode{}}}}}
	if err := os.MkdirAll(deps.RagIndexDir, 0o755); err != nil {
		t.Fatal(err)
	}
	idxBytes, err := json.Marshal(idx)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(deps.RagIndexDir, "doc-index.json"), idxBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	result := callTool(t, deps, "get_rag_tree", map[string]any{"namespace": ctxns.DefaultNamespace})
	if result.IsError {
		t.Fatalf("get_rag_tree default namespace DB tree error: %v", getTextContent(result))
	}

	root := decodeRagTreeNode(t, result)
	if ragindex.FindNode(root, "community:db") == nil {
		t.Fatalf("expected DB tree for explicit default namespace: %#v", root)
	}
	if ragindex.FindNode(root, "community:json-default") != nil {
		t.Fatalf("shared doc-index should not be used when DB tree exists: %#v", root)
	}
}

func TestGetRagTree_DBFallbackReturnsErrorWhenDBMissing(t *testing.T) {
	deps := setupTestDeps(t)
	deps.RagIndexDir = filepath.Join(t.TempDir(), ".ccg")
	deps.DB = nil

	result := callTool(t, deps, "get_rag_tree", map[string]any{})
	if !result.IsError {
		t.Fatal("expected DB fallback error when DB is not configured")
	}
	if !strings.Contains(getTextContent(result), "DB is not configured") {
		t.Fatalf("expected DB is not configured error, got %q", getTextContent(result))
	}
}

func TestGetRagTree_DBFallbackOrdersCommunitiesAndFilesDeterministically(t *testing.T) {
	deps := setupTestDeps(t)
	deps.RagIndexDir = filepath.Join(t.TempDir(), ".ccg")
	seedRagTreeDBFallbackCommunity(t, deps, ctxns.DefaultNamespace, "zeta", "Zeta", "zeta docs", "zeta.Second", "Second", "internal/zeta/z.go")
	seedRagTreeDBFallbackCommunity(t, deps, ctxns.DefaultNamespace, "alpha", "Alpha", "alpha docs", "alpha.Second", "Second", "internal/alpha/z.go")
	seedRagTreeDBFallbackCommunity(t, deps, ctxns.DefaultNamespace, "alpha-extra", "Alpha Extra", "alpha extra docs", "alpha.First", "First", "internal/alpha/a.go")
	filesComm := model.Community{Namespace: ctxns.DefaultNamespace, Key: "files", Label: "Files", Strategy: "test", Description: "file order docs"}
	if err := deps.DB.Create(&filesComm).Error; err != nil {
		t.Fatalf("create files community: %v", err)
	}
	fileZ := seedRetrieveDocsDBFallbackNode(t, deps, ctxns.DefaultNamespace, "files.Z", "Z", "internal/files/z.go", model.TagIntent, "file z docs", "file z docs")
	fileA := seedRetrieveDocsDBFallbackNode(t, deps, ctxns.DefaultNamespace, "files.A", "A", "internal/files/a.go", model.TagIntent, "file a docs", "file a docs")
	for _, node := range []model.Node{fileZ, fileA} {
		if err := deps.DB.Create(&model.CommunityMembership{CommunityID: filesComm.ID, NodeID: node.ID}).Error; err != nil {
			t.Fatalf("create files membership: %v", err)
		}
	}

	result := callTool(t, deps, "get_rag_tree", map[string]any{})
	if result.IsError {
		t.Fatalf("get_rag_tree deterministic order error: %v", getTextContent(result))
	}

	root := decodeRagTreeNode(t, result)
	if len(root.Children) < 4 {
		t.Fatalf("expected at least 3 communities, got %#v", root.Children)
	}
	got := []string{root.Children[0].ID, root.Children[1].ID, root.Children[2].ID, root.Children[3].ID}
	want := []string{"community:alpha", "community:alpha-extra", "community:files", "community:zeta"}
	if !slices.Equal(got, want) {
		t.Fatalf("community order = %#v, want %#v", got, want)
	}
	filesNode := root.Children[2]
	if len(filesNode.Children) != 2 {
		t.Fatalf("files children = %d, want 2: %#v", len(filesNode.Children), filesNode.Children)
	}
	fileOrder := []string{filesNode.Children[0].ID, filesNode.Children[1].ID}
	if wantFiles := []string{"file:internal/files/a.go", "file:internal/files/z.go"}; !slices.Equal(fileOrder, wantFiles) {
		t.Fatalf("file order = %#v, want %#v", fileOrder, wantFiles)
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

func TestGetDocContent_NoNamespaceRejectsOutsideRagIndexDir(t *testing.T) {
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
		t.Fatal("expected no-namespace get_doc_content to reject paths outside RagIndexDir")
	}
}

func TestGetDocContent_NoNamespaceRejectsSymlinkEscape(t *testing.T) {
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

func TestGetRagTree_RejectsInvalidNamespace(t *testing.T) {
	deps := setupTestDeps(t)
	result := callTool(t, deps, "get_rag_tree", map[string]any{"namespace": "../outside"})
	if result.IsError {
		t.Fatalf("unexpected get_rag_tree error for unsupported namespace check: %s", getTextContent(result))
	}
	root := decodeRagTreeNode(t, result)
	if len(root.Children) != 0 {
		t.Fatalf("expected empty tree for non-existent namespace, got %#v", root)
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
	if result.IsError {
		t.Fatalf("search_docs should return DB fallback empty results when index file is missing: %v", getTextContent(result))
	}
	var results []ragindex.SearchResult
	if err := json.Unmarshal([]byte(getTextContent(result)), &results); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("results = %d, want 0: %#v", len(results), results)
	}
}

func TestSearchDocs_DBFallbackSucceedsWithoutDocIndex(t *testing.T) {
	deps := setupTestDeps(t)
	tmpDir := t.TempDir()
	deps.RagIndexDir = filepath.Join(tmpDir, ".ccg")
	seedRetrieveDocsDBFallbackNode(t, deps, ctxns.DefaultNamespace, "billing.Processor", "Processor", "internal/billing/processor.go", model.TagIntent, "payment settlement workflow", "payment settlement workflow")
	rebuildRetrieveDocsSearchBackend(t, deps, ctxns.DefaultNamespace)

	result := callTool(t, deps, "search_docs", map[string]any{"query": "payment settlement", "limit": float64(5)})
	if result.IsError {
		t.Fatalf("search_docs DB fallback error: %v", getTextContent(result))
	}

	var results []ragindex.SearchResult
	if err := json.Unmarshal([]byte(getTextContent(result)), &results); err != nil {
		t.Fatalf("unmarshal search_docs response: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("results = %d, want 1: %#v", len(results), results)
	}
	if results[0].ID == "" || results[0].Label == "" || results[0].Kind == "" || results[0].Summary == "" || len(results[0].Path) == 0 {
		t.Fatalf("unstable DB fallback response shape: %#v", results[0])
	}
}

func TestSearchDocs_DBFallbackIsNamespaceIsolated(t *testing.T) {
	deps := setupTestDeps(t)
	tmpDir := t.TempDir()
	deps.NamespaceRoot = filepath.Join(tmpDir, "namespaces")
	deps.RagIndexDir = filepath.Join(tmpDir, ".ccg")
	alpha := "alpha-service"
	beta := "beta-service"
	seedRetrieveDocsDBFallbackNode(t, deps, alpha, "alpha.Checkout", "Checkout", "checkout.go", model.TagIntent, "sharedtenant alpha checkout", "sharedtenant alpha checkout")
	seedRetrieveDocsDBFallbackNode(t, deps, beta, "beta.Checkout", "Checkout", "checkout.go", model.TagIntent, "sharedtenant beta checkout", "sharedtenant beta checkout")
	rebuildRetrieveDocsSearchBackend(t, deps, alpha)
	rebuildRetrieveDocsSearchBackend(t, deps, beta)

	result := callTool(t, deps, "search_docs", map[string]any{"namespace": alpha, "query": "sharedtenant checkout", "limit": float64(5)})
	if result.IsError {
		t.Fatalf("search_docs namespace DB fallback error: %v", getTextContent(result))
	}

	results := decodeSearchDocsResults(t, result)
	if len(results) != 1 {
		t.Fatalf("results = %d, want 1: %#v", len(results), results)
	}
}

func TestSearchDocs_DBFallbackAnnotationOnlyMatch(t *testing.T) {
	deps := setupTestDeps(t)
	tmpDir := t.TempDir()
	deps.RagIndexDir = filepath.Join(tmpDir, ".ccg")
	seedRetrieveDocsDBFallbackNode(t, deps, ctxns.DefaultNamespace, "policy.Guard", "Guard", "internal/policy/guard.go", model.TagDomainRule, "breakglass approval required", "policy guard searchable")
	rebuildRetrieveDocsSearchBackend(t, deps, ctxns.DefaultNamespace)

	result := callTool(t, deps, "search_docs", map[string]any{"query": "breakglass", "limit": float64(5)})
	if result.IsError {
		t.Fatalf("search_docs annotation-only DB fallback error: %v", getTextContent(result))
	}

	results := decodeSearchDocsResults(t, result)
	if len(results) != 1 {
		t.Fatalf("results = %d, want 1: %#v", len(results), results)
	}
}

func TestSearchDocs_DBFallbackHonorsLimit(t *testing.T) {
	deps := setupTestDeps(t)
	tmpDir := t.TempDir()
	deps.RagIndexDir = filepath.Join(tmpDir, ".ccg")
	seedRetrieveDocsDBFallbackNode(t, deps, ctxns.DefaultNamespace, "alpha.First", "First", "internal/alpha/file.go", model.TagIntent, "groupterm first", "groupterm first")
	seedRetrieveDocsDBFallbackNode(t, deps, ctxns.DefaultNamespace, "alpha.Second", "Second", "internal/alpha/file.go", model.TagIntent, "groupterm second", "groupterm second")
	seedRetrieveDocsDBFallbackNode(t, deps, ctxns.DefaultNamespace, "beta.Only", "Only", "internal/beta/file.go", model.TagIntent, "groupterm beta", "groupterm beta")
	rebuildRetrieveDocsSearchBackend(t, deps, ctxns.DefaultNamespace)

	result := callTool(t, deps, "search_docs", map[string]any{"query": "groupterm", "limit": float64(1)})
	if result.IsError {
		t.Fatalf("search_docs DB fallback limit error: %v", getTextContent(result))
	}

	results := decodeSearchDocsResults(t, result)
	if len(results) != 1 {
		t.Fatalf("results = %d, want exactly 1 result: %#v", len(results), results)
	}
}

func TestSearchDocs_DBFallbackResponseShapeStable(t *testing.T) {
	deps := setupTestDeps(t)
	tmpDir := t.TempDir()
	deps.RagIndexDir = filepath.Join(tmpDir, ".ccg")
	seedRetrieveDocsDBFallbackNode(t, deps, ctxns.DefaultNamespace, "stable.Shape", "Shape", "internal/stable/shape.go", model.TagIntent, "stable shape", "stable shape")
	rebuildRetrieveDocsSearchBackend(t, deps, ctxns.DefaultNamespace)

	result := callTool(t, deps, "search_docs", map[string]any{"query": "stable", "limit": float64(5)})
	if result.IsError {
		t.Fatalf("search_docs DB fallback shape error: %v", getTextContent(result))
	}

	results := decodeSearchDocsResults(t, result)
	if len(results) != 1 {
		t.Fatalf("results = %d, want 1: %#v", len(results), results)
	}
	got := results[0]
	if got.ID == "" || got.Label == "" || got.Kind == "" || got.Summary == "" || len(got.Path) == 0 {
		t.Fatalf("unstable DB fallback response shape: %#v", got)
	}
}

func TestSearchDocs_UsesDBWhenDocIndexFileExists(t *testing.T) {
	deps := setupTestDeps(t)
	tmpDir := t.TempDir()
	deps.RagIndexDir = filepath.Join(tmpDir, ".ccg")
	seedRetrieveDocsDBFallbackNode(t, deps, ctxns.DefaultNamespace, "db.Only", "Only", "internal/db/only.go", model.TagIntent, "precedence db", "precedence db")
	rebuildRetrieveDocsSearchBackend(t, deps, ctxns.DefaultNamespace)

	if err := os.MkdirAll(deps.RagIndexDir, 0o755); err != nil {
		t.Fatal(err)
	}
	idx := &ragindex.Index{Root: &ragindex.TreeNode{ID: "root", Label: "Root", Kind: "root", Children: []*ragindex.TreeNode{{ID: "community:json", Label: "JSON Wins", Kind: "community", Summary: "json index", Children: []*ragindex.TreeNode{}}}}}
	idxBytes, err := json.Marshal(idx)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(deps.RagIndexDir, "doc-index.json"), idxBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	result := callTool(t, deps, "search_docs", map[string]any{"query": "precedence", "limit": float64(5)})
	if result.IsError {
		t.Fatalf("search_docs DB-first query error: %v", getTextContent(result))
	}

	results := decodeSearchDocsResults(t, result)
	if len(results) != 1 || results[0].ID != "file:internal/db/only.go" {
		t.Fatalf("expected DB file result file:internal/db/only.go, got %#v", results)
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
	if len(got.MatchedFields) == 0 {
		t.Fatalf("matched_fields should be populated, got %#v", got.MatchedFields)
	}
	if len(got.Matches) < 2 {
		t.Fatalf("expected evidence matches for both symbols, got %#v", got.Matches)
	}
}

func TestRetrieveDocs_ExposesStructuredMatchedFields(t *testing.T) {
	deps := setupTestDeps(t)
	tmpDir := t.TempDir()
	docsDir := filepath.Join(tmpDir, "docs")
	deps.RagIndexDir = filepath.Join(tmpDir, ".ccg")

	comm := model.Community{Key: "rules", Label: "Rules", Description: "policy rules"}
	if err := deps.DB.Create(&comm).Error; err != nil {
		t.Fatalf("create community: %v", err)
	}
	node := model.Node{QualifiedName: "policy.CheckAccess", Kind: model.NodeKindFunction, Name: "CheckAccess", FilePath: "internal/policy/access.go", StartLine: 1, EndLine: 20, Language: "go"}
	if err := deps.DB.Create(&node).Error; err != nil {
		t.Fatalf("create node: %v", err)
	}
	if err := deps.DB.Create(&model.CommunityMembership{CommunityID: comm.ID, NodeID: node.ID}).Error; err != nil {
		t.Fatalf("create membership: %v", err)
	}
	ann := model.Annotation{NodeID: node.ID, Summary: "access policy"}
	if err := deps.DB.Create(&ann).Error; err != nil {
		t.Fatalf("create annotation: %v", err)
	}
	for i, tag := range []model.DocTag{
		{AnnotationID: ann.ID, Kind: model.TagDomainRule, Value: "admin approval required", Ordinal: 0},
		{AnnotationID: ann.ID, Kind: model.TagSideEffect, Value: "admin audit log written", Ordinal: 1},
	} {
		if err := deps.DB.Create(&tag).Error; err != nil {
			t.Fatalf("create doc tag %d: %v", i, err)
		}
	}

	docPath := filepath.Join(docsDir, "internal/policy/access.go.md")
	if err := os.MkdirAll(filepath.Dir(docPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(docPath, []byte("# access.go\n\nadmin approval and audit docs\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	b := &ragindex.Builder{DB: deps.DB, IndexDir: deps.RagIndexDir, OutDir: docsDir}
	if _, _, err := b.Build(context.Background()); err != nil {
		t.Fatalf("Build: %v", err)
	}

	result := callTool(t, deps, "retrieve_docs", map[string]any{
		"query":         "admin",
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
	got := map[string]bool{}
	for _, field := range response.Results[0].MatchedFields {
		got[field] = true
	}
	for _, want := range []string{"domainRule", "sideEffect"} {
		if !got[want] {
			t.Fatalf("matched_fields missing %q: %#v", want, response.Results[0].MatchedFields)
		}
	}
}

func TestRetrieveDocs_ExplainFlagControlsDiagnostics(t *testing.T) {
	deps := setupTestDeps(t)
	tmpDir := t.TempDir()
	docsDir := filepath.Join(tmpDir, "docs")
	deps.RagIndexDir = filepath.Join(tmpDir, ".ccg")

	comm := model.Community{Key: "billing", Label: "Billing", Description: "billing pipeline"}
	if err := deps.DB.Create(&comm).Error; err != nil {
		t.Fatalf("create community: %v", err)
	}
	node := model.Node{QualifiedName: "billing.PaymentProcessor", Kind: model.NodeKindFunction, Name: "PaymentProcessor", FilePath: "internal/billing/processor.go", StartLine: 1, EndLine: 30, Language: "go"}
	if err := deps.DB.Create(&node).Error; err != nil {
		t.Fatalf("create node: %v", err)
	}
	if err := deps.DB.Create(&model.CommunityMembership{CommunityID: comm.ID, NodeID: node.ID}).Error; err != nil {
		t.Fatalf("create membership: %v", err)
	}
	ann := model.Annotation{NodeID: node.ID, Summary: "payment processor entrypoint"}
	if err := deps.DB.Create(&ann).Error; err != nil {
		t.Fatalf("create annotation: %v", err)
	}
	if err := deps.DB.Create(&model.DocTag{AnnotationID: ann.ID, Kind: model.TagIntent, Value: "payment settlement entrypoint", Ordinal: 0}).Error; err != nil {
		t.Fatalf("create intent tag: %v", err)
	}

	docPath := filepath.Join(docsDir, "internal/billing/processor.go.md")
	if err := os.MkdirAll(filepath.Dir(docPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(docPath, []byte("# processor.go\n\npayment processor docs\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	b := &ragindex.Builder{DB: deps.DB, IndexDir: deps.RagIndexDir, OutDir: docsDir}
	if _, _, err := b.Build(context.Background()); err != nil {
		t.Fatalf("Build: %v", err)
	}

	defaultRes := callTool(t, deps, "retrieve_docs", map[string]any{"query": "payment", "limit": float64(3), "content_limit": float64(0)})
	if defaultRes.IsError {
		t.Fatalf("retrieve_docs default error: %v", getTextContent(defaultRes))
	}
	defaultJSON := getTextContent(defaultRes)
	for _, key := range []string{"expanded_terms", "field_scores", "literal_score", "expansion_score"} {
		if strings.Contains(defaultJSON, key) {
			t.Fatalf("default response must omit %q diagnostic key, got %s", key, defaultJSON)
		}
	}

	explainRes := callTool(t, deps, "retrieve_docs", map[string]any{"query": "payment", "limit": float64(3), "content_limit": float64(0), "explain": true})
	if explainRes.IsError {
		t.Fatalf("retrieve_docs explain error: %v", getTextContent(explainRes))
	}
	explainJSON := getTextContent(explainRes)
	for _, key := range []string{"field_scores", "literal_score"} {
		if !strings.Contains(explainJSON, key) {
			t.Fatalf("explain response must include %q, got %s", key, explainJSON)
		}
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

func TestRetrieveDocs_DBFallbackSucceedsWithoutDocIndex(t *testing.T) {
	deps := setupTestDeps(t)
	tmpDir := t.TempDir()
	docsDir := filepath.Join(tmpDir, "docs")
	deps.RagIndexDir = filepath.Join(tmpDir, ".ccg")
	seedRetrieveDocsDBFallbackNode(t, deps, ctxns.DefaultNamespace, "billing.Processor", "Processor", "internal/billing/processor.go", model.TagIntent, "payment settlement workflow", "payment settlement workflow")
	writeRetrieveDocsMarkdown(t, docsDir, "internal/billing/processor.go", "# processor.go\n\npayment settlement workflow docs\n")
	rebuildRetrieveDocsSearchBackend(t, deps, ctxns.DefaultNamespace)

	result := callTool(t, deps, "retrieve_docs", map[string]any{
		"query":         "payment settlement",
		"limit":         float64(5),
		"content_limit": float64(2000),
	})
	if result.IsError {
		t.Fatalf("retrieve_docs DB fallback error: %v", getTextContent(result))
	}

	response := decodeRetrieveDocsResponse(t, result)
	if len(response.Results) != 1 {
		t.Fatalf("results = %d, want 1: %#v", len(response.Results), response.Results)
	}
	got := response.Results[0]
	if got.DocPath != "docs/internal/billing/processor.go.md" {
		t.Fatalf("doc_path = %q, want docs/internal/billing/processor.go.md", got.DocPath)
	}
	if !strings.Contains(got.Content, "payment settlement workflow docs") {
		t.Fatalf("content = %q", got.Content)
	}
}

func TestRetrieveDocs_DBFallbackIsNamespaceIsolated(t *testing.T) {
	deps := setupTestDeps(t)
	tmpDir := t.TempDir()
	deps.NamespaceRoot = filepath.Join(tmpDir, "namespaces")
	deps.RagIndexDir = filepath.Join(tmpDir, ".ccg")
	alpha := "alpha-service"
	beta := "beta-service"
	seedRetrieveDocsDBFallbackNode(t, deps, alpha, "alpha.Checkout", "Checkout", "checkout.go", model.TagIntent, "sharedtenant alpha checkout", "sharedtenant alpha checkout")
	seedRetrieveDocsDBFallbackNode(t, deps, beta, "beta.Checkout", "Checkout", "checkout.go", model.TagIntent, "sharedtenant beta checkout", "sharedtenant beta checkout")
	writeRetrieveDocsMarkdown(t, filepath.Join(deps.NamespaceRoot, alpha, "docs"), "checkout.go", "# checkout\n\nalpha checkout docs\n")
	writeRetrieveDocsMarkdown(t, filepath.Join(deps.NamespaceRoot, beta, "docs"), "checkout.go", "# checkout\n\nbeta checkout docs\n")
	rebuildRetrieveDocsSearchBackend(t, deps, alpha)
	rebuildRetrieveDocsSearchBackend(t, deps, beta)

	result := callTool(t, deps, "retrieve_docs", map[string]any{
		"namespace":     alpha,
		"query":         "sharedtenant checkout",
		"limit":         float64(5),
		"content_limit": float64(2000),
	})
	if result.IsError {
		t.Fatalf("retrieve_docs namespace DB fallback error: %v", getTextContent(result))
	}

	response := decodeRetrieveDocsResponse(t, result)
	if len(response.Results) != 1 {
		t.Fatalf("results = %d, want 1: %#v", len(response.Results), response.Results)
	}
	if !strings.Contains(response.Results[0].Content, "alpha checkout docs") {
		t.Fatalf("expected alpha docs, got %q", response.Results[0].Content)
	}
	if strings.Contains(response.Results[0].Content, "beta checkout docs") || strings.Contains(response.Results[0].Summary, "beta") {
		t.Fatalf("namespace leaked beta result: %#v", response.Results[0])
	}
}

func TestRetrieveDocs_DBFallbackMissingMarkdownDoesNotFail(t *testing.T) {
	deps := setupTestDeps(t)
	tmpDir := t.TempDir()
	deps.RagIndexDir = filepath.Join(tmpDir, ".ccg")
	seedRetrieveDocsDBFallbackNode(t, deps, ctxns.DefaultNamespace, "docs.Missing", "Missing", "internal/docs/missing.go", model.TagIntent, "missing markdown fallback", "missing markdown fallback")
	rebuildRetrieveDocsSearchBackend(t, deps, ctxns.DefaultNamespace)

	result := callTool(t, deps, "retrieve_docs", map[string]any{
		"query":         "missing markdown",
		"limit":         float64(5),
		"content_limit": float64(2000),
	})
	if result.IsError {
		t.Fatalf("retrieve_docs should not fail when DB fallback markdown is missing: %v", getTextContent(result))
	}

	response := decodeRetrieveDocsResponse(t, result)
	if len(response.Results) != 1 {
		t.Fatalf("results = %d, want 1: %#v", len(response.Results), response.Results)
	}
	if response.Results[0].Content != "" {
		t.Fatalf("missing markdown content = %q, want empty", response.Results[0].Content)
	}
	if response.Results[0].DocPath != "docs/internal/docs/missing.go.md" {
		t.Fatalf("doc_path = %q, want docs/internal/docs/missing.go.md", response.Results[0].DocPath)
	}
}

func TestRetrieveDocs_DBFallbackAnnotationOnlyMatchIncludesAnnotationBucket(t *testing.T) {
	deps := setupTestDeps(t)
	tmpDir := t.TempDir()
	deps.RagIndexDir = filepath.Join(tmpDir, ".ccg")
	seedRetrieveDocsDBFallbackNode(t, deps, ctxns.DefaultNamespace, "policy.Guard", "Guard", "internal/policy/guard.go", model.TagDomainRule, "breakglass approval required", "policy guard searchable")
	rebuildRetrieveDocsSearchBackend(t, deps, ctxns.DefaultNamespace)

	result := callTool(t, deps, "retrieve_docs", map[string]any{"query": "breakglass", "limit": float64(5), "content_limit": float64(0)})
	if result.IsError {
		t.Fatalf("retrieve_docs annotation-only DB fallback error: %v", getTextContent(result))
	}

	response := decodeRetrieveDocsResponse(t, result)
	if len(response.Results) != 1 {
		t.Fatalf("results = %d, want 1: %#v", len(response.Results), response.Results)
	}
	if !retrieveDocsHasField(response.Results[0].MatchedFields, string(model.TagDomainRule)) {
		t.Fatalf("matched_fields missing domainRule annotation bucket: %#v", response.Results[0].MatchedFields)
	}
}

func TestRetrieveDocs_DBFallbackHonorsLimitAfterFileGrouping(t *testing.T) {
	deps := setupTestDeps(t)
	tmpDir := t.TempDir()
	deps.RagIndexDir = filepath.Join(tmpDir, ".ccg")
	seedRetrieveDocsDBFallbackNode(t, deps, ctxns.DefaultNamespace, "alpha.First", "First", "internal/alpha/file.go", model.TagIntent, "groupterm first", "groupterm first")
	seedRetrieveDocsDBFallbackNode(t, deps, ctxns.DefaultNamespace, "alpha.Second", "Second", "internal/alpha/file.go", model.TagIntent, "groupterm second", "groupterm second")
	seedRetrieveDocsDBFallbackNode(t, deps, ctxns.DefaultNamespace, "beta.Only", "Only", "internal/beta/file.go", model.TagIntent, "groupterm beta", "groupterm beta")
	rebuildRetrieveDocsSearchBackend(t, deps, ctxns.DefaultNamespace)

	result := callTool(t, deps, "retrieve_docs", map[string]any{"query": "groupterm", "limit": float64(1), "content_limit": float64(0)})
	if result.IsError {
		t.Fatalf("retrieve_docs DB fallback limit error: %v", getTextContent(result))
	}

	response := decodeRetrieveDocsResponse(t, result)
	if len(response.Results) != 1 {
		t.Fatalf("results = %d, want exactly 1 file group: %#v", len(response.Results), response.Results)
	}
	if len(response.Results[0].Matches) != 2 {
		t.Fatalf("first file group should keep both node matches, got %#v", response.Results[0].Matches)
	}
}

func TestRetrieveDocs_DBFallbackResponseShapeStableByDefault(t *testing.T) {
	deps := setupTestDeps(t)
	tmpDir := t.TempDir()
	deps.RagIndexDir = filepath.Join(tmpDir, ".ccg")
	seedRetrieveDocsDBFallbackNode(t, deps, ctxns.DefaultNamespace, "stable.Shape", "Shape", "internal/stable/shape.go", model.TagIntent, "stable shape", "stable shape")
	rebuildRetrieveDocsSearchBackend(t, deps, ctxns.DefaultNamespace)

	result := callTool(t, deps, "retrieve_docs", map[string]any{"query": "stable", "limit": float64(5), "content_limit": float64(0)})
	if result.IsError {
		t.Fatalf("retrieve_docs DB fallback shape error: %v", getTextContent(result))
	}
	jsonText := getTextContent(result)
	for _, key := range []string{"expanded_terms", "field_scores", "literal_score", "expansion_score"} {
		if strings.Contains(jsonText, key) {
			t.Fatalf("default response must omit %q diagnostic key, got %s", key, jsonText)
		}
	}

	response := decodeRetrieveDocsResponse(t, result)
	if len(response.Results) != 1 {
		t.Fatalf("results = %d, want 1: %#v", len(response.Results), response.Results)
	}
	got := response.Results[0]
	if got.ID == "" || got.Label == "" || got.Kind != "file" || got.DocPath == "" || len(got.Path) == 0 || len(got.MatchedTerms) == 0 || len(got.MatchedFields) == 0 {
		t.Fatalf("unstable DB fallback response shape: %#v", got)
	}
}

func TestRetrieveDocs_DBPrimaryTakesPrecedenceOverDocIndex(t *testing.T) {
	deps := setupTestDeps(t)
	tmpDir := t.TempDir()
	docsDir := filepath.Join(tmpDir, "docs")
	deps.RagIndexDir = filepath.Join(tmpDir, ".ccg")
	seedRetrieveDocsDBFallbackNode(t, deps, ctxns.DefaultNamespace, "db.Only", "Only", "internal/db/only.go", model.TagIntent, "precedence db", "precedence db")
	writeRetrieveDocsMarkdown(t, docsDir, "internal/db/only.go", "# db only\n\nDB fallback content must not be used.\n")
	rebuildRetrieveDocsSearchBackend(t, deps, ctxns.DefaultNamespace)

	comm := model.Community{Key: "json", Label: "JSON"}
	if err := deps.DB.Create(&comm).Error; err != nil {
		t.Fatalf("create json community: %v", err)
	}
	jsonNode := model.Node{QualifiedName: "json.Wins", Kind: model.NodeKindFunction, Name: "Wins", FilePath: "internal/json/wins.go", StartLine: 1, EndLine: 10, Language: "go"}
	if err := deps.DB.Create(&jsonNode).Error; err != nil {
		t.Fatalf("create json node: %v", err)
	}
	if err := deps.DB.Create(&model.CommunityMembership{CommunityID: comm.ID, NodeID: jsonNode.ID}).Error; err != nil {
		t.Fatalf("create json membership: %v", err)
	}
	jsonAnn := model.Annotation{NodeID: jsonNode.ID}
	if err := deps.DB.Create(&jsonAnn).Error; err != nil {
		t.Fatalf("create json annotation: %v", err)
	}
	if err := deps.DB.Create(&model.DocTag{AnnotationID: jsonAnn.ID, Kind: model.TagIntent, Value: "precedence json", Ordinal: 0}).Error; err != nil {
		t.Fatalf("create json doc tag: %v", err)
	}
	writeRetrieveDocsMarkdown(t, docsDir, "internal/json/wins.go", "# json wins\n\nJSON index content wins.\n")
	b := &ragindex.Builder{DB: deps.DB, IndexDir: deps.RagIndexDir, OutDir: docsDir}
	if _, _, err := b.Build(context.Background()); err != nil {
		t.Fatalf("Build: %v", err)
	}

	result := callTool(t, deps, "retrieve_docs", map[string]any{"query": "precedence", "limit": float64(5), "content_limit": float64(2000)})
	if result.IsError {
		t.Fatalf("retrieve_docs DB precedence error: %v", getTextContent(result))
	}

	response := decodeRetrieveDocsResponse(t, result)
	if len(response.Results) != 1 {
		t.Fatalf("results = %d, want 1: %#v", len(response.Results), response.Results)
	}
	if !strings.Contains(response.Results[0].Content, "DB fallback content") || !strings.Contains(response.Results[0].DocPath, "internal/db/only.go") {
		t.Fatalf("expected DB primary result, got %#v", response.Results[0])
	}
	if strings.Contains(response.Results[0].Content, "JSON index content wins") {
		t.Fatalf("doc-index result should not be used when DB retrieval succeeds: %#v", response.Results[0])
	}
}

func TestRetrieveDocs_RejectsLimitAboveMax(t *testing.T) {
	deps := setupTestDeps(t)
	result := callTool(t, deps, "retrieve_docs", map[string]any{"query": "auth", "limit": float64(51)})
	if !result.IsError {
		t.Fatal("expected retrieve_docs to reject limit above max")
	}
}

func TestSearchDocs_RejectsInvalidNamespace(t *testing.T) {
	deps := setupTestDeps(t)
	result := callTool(t, deps, "search_docs", map[string]any{"query": "auth", "namespace": "../outside"})
	if result.IsError {
		t.Fatalf("unexpected search_docs error for unsupported namespace check: %s", getTextContent(result))
	}
	var response []ragindex.SearchResult
	if err := json.Unmarshal([]byte(getTextContent(result)), &response); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if len(response) != 0 {
		t.Fatalf("expected no results for non-existent namespace, got %#v", response)
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

func TestBuildRagIndex_WithNamespace(t *testing.T) {
	deps := setupTestDeps(t)
	tmpDir := t.TempDir()
	deps.NamespaceRoot = filepath.Join(tmpDir, "namespaces")
	deps.RagIndexDir = filepath.Join(tmpDir, ".ccg")

	wsDocsDir := filepath.Join(tmpDir, "namespaces", "my-service")
	if err := os.MkdirAll(wsDocsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	result := callTool(t, deps, "build_rag_index", map[string]any{"namespace": "my-service"})
	if result.IsError {
		t.Fatalf("build_rag_index with namespace error: %v", getTextContent(result))
	}
	content := getTextContent(result)
	if !strings.Contains(content, "Built doc-index:") {
		t.Errorf("expected 'Built doc-index:' in output, got: %s", content)
	}
}

func TestRetrieveDocs_WithNamespaceReadsNamespaceRelativeDocPath(t *testing.T) {
	deps := setupTestDeps(t)
	tmpDir := t.TempDir()
	deps.NamespaceRoot = filepath.Join(tmpDir, "namespaces")
	deps.RagIndexDir = filepath.Join(tmpDir, ".ccg")

	ns := "my-service"
	nsDir := filepath.Join(deps.NamespaceRoot, ns)
	docPath := filepath.Join(nsDir, "docs", "service.go.md")
	if err := os.MkdirAll(filepath.Dir(docPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(docPath, []byte("# service.go\n\nadmin audit trail docs\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	comm := model.Community{Namespace: ns, Key: "svc", Label: "Service"}
	if err := deps.DB.Create(&comm).Error; err != nil {
		t.Fatalf("create community: %v", err)
	}
	node := model.Node{
		Namespace:     ns,
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

	build := callTool(t, deps, "build_rag_index", map[string]any{"namespace": ns})
	if build.IsError {
		t.Fatalf("build_rag_index with namespace error: %v", getTextContent(build))
	}

	treeResult := callTool(t, deps, "get_rag_tree", map[string]any{"namespace": ns})
	if treeResult.IsError {
		t.Fatalf("get_rag_tree with namespace error: %v", getTextContent(treeResult))
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
		"namespace":     ns,
		"query":         "admin audit",
		"limit":         float64(5),
		"content_limit": float64(2000),
	})
	if result.IsError {
		t.Fatalf("retrieve_docs with namespace error: %v", getTextContent(result))
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

func TestBuildRagIndex_NoNamespaceRejectsIndexDirOutsideSafeRoot(t *testing.T) {
	deps := setupTestDeps(t)
	tmpDir := t.TempDir()
	deps.RagIndexDir = filepath.Join(tmpDir, ".ccg")
	outside := filepath.Join(tmpDir, "outside-index")

	result := callTool(t, deps, "build_rag_index", map[string]any{"index_dir": outside})
	if !result.IsError {
		t.Fatal("expected build_rag_index to reject index_dir outside RagIndexDir")
	}
}

func TestBuildRagIndex_NamespaceRejectsIndexDirOutsideSafeRoot(t *testing.T) {
	deps := setupTestDeps(t)
	tmpDir := t.TempDir()
	deps.NamespaceRoot = filepath.Join(tmpDir, "namespaces")
	deps.RagIndexDir = filepath.Join(tmpDir, ".ccg")
	if err := os.MkdirAll(filepath.Join(deps.NamespaceRoot, "my-service"), 0o755); err != nil {
		t.Fatal(err)
	}

	result := callTool(t, deps, "build_rag_index", map[string]any{
		"namespace": "my-service",
		"index_dir": filepath.Join(tmpDir, "outside-index"),
	})
	if !result.IsError {
		t.Fatal("expected namespace build_rag_index to reject index_dir outside RagIndexDir")
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

func TestGetDocContent_WithNamespace(t *testing.T) {
	deps := setupTestDeps(t)
	tmpDir := t.TempDir()
	deps.NamespaceRoot = filepath.Join(tmpDir, "namespaces")

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
	deps.NamespaceRoot = filepath.Join(tmpDir, "namespaces")

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

func TestSearchDocs_WithNamespace(t *testing.T) {
	deps := setupTestDeps(t)
	tmpDir := t.TempDir()
	deps.RagIndexDir = filepath.Join(tmpDir, ".ccg")
	comm := model.Community{Key: "auth", Label: "Auth", Description: "authentication service"}
	if err := deps.DB.Create(&comm).Error; err != nil {
		t.Fatalf("create auth community: %v", err)
	}
	authNode := model.Node{
		Namespace:     "my-service",
		QualifiedName: "auth.Check",
		Kind:          model.NodeKindFunction,
		Name:          "Check",
		FilePath:      "internal/auth/check.go",
		StartLine:     1,
		EndLine:       10,
		Language:      "go",
	}
	if err := deps.DB.Create(&authNode).Error; err != nil {
		t.Fatalf("create auth node: %v", err)
	}
	if err := deps.DB.Create(&model.CommunityMembership{CommunityID: comm.ID, NodeID: authNode.ID}).Error; err != nil {
		t.Fatalf("create auth membership: %v", err)
	}
	authAnn := model.Annotation{NodeID: authNode.ID}
	if err := deps.DB.Create(&authAnn).Error; err != nil {
		t.Fatalf("create auth annotation: %v", err)
	}
	if err := deps.DB.Create(&model.DocTag{AnnotationID: authAnn.ID, Kind: model.TagIntent, Value: "auth check", Ordinal: 0}).Error; err != nil {
		t.Fatalf("create auth doc tag: %v", err)
	}
	rebuildRetrieveDocsSearchBackend(t, deps, "my-service")

	result := callTool(t, deps, "search_docs", map[string]any{"query": "auth", "namespace": "my-service"})
	if result.IsError {
		t.Fatalf("search_docs with namespace error: %v", getTextContent(result))
	}
	var response []ragindex.SearchResult
	if err := json.Unmarshal([]byte(getTextContent(result)), &response); err != nil {
		t.Fatalf("unmarshal search response: %v", err)
	}
	if len(response) != 1 || response[0].ID != "file:internal/auth/check.go" {
		t.Fatalf("expected one result file:internal/auth/check.go, got %#v", response)
	}
}

func TestGetRagTree_WithNamespace(t *testing.T) {
	deps := setupTestDeps(t)
	tmpDir := t.TempDir()
	deps.RagIndexDir = filepath.Join(tmpDir, ".ccg")
	seedRagTreeDBFallbackCommunity(t, deps, "my-service", "payments", "Payments", "payment docs", "payments.Capture", "Capture", "internal/payments/capture.go")

	result := callTool(t, deps, "get_rag_tree", map[string]any{"namespace": "my-service"})
	if result.IsError {
		t.Fatalf("get_rag_tree with namespace error: %v", getTextContent(result))
	}
	root := decodeRagTreeNode(t, result)
	if ragindex.FindNode(root, "community:payments") == nil {
		t.Fatalf("expected community:payments in namespaced tree, got %#v", root)
	}
}

func seedRetrieveDocsDBFallbackNode(t *testing.T, deps *Deps, namespace, qualifiedName, name, filePath string, tagKind model.TagKind, tagValue, searchContent string) model.Node {
	t.Helper()
	node := model.Node{
		Namespace:     namespace,
		QualifiedName: qualifiedName,
		Kind:          model.NodeKindFunction,
		Name:          name,
		FilePath:      filePath,
		StartLine:     1,
		EndLine:       10,
		Language:      "go",
	}
	if err := deps.DB.Create(&node).Error; err != nil {
		t.Fatalf("create node %s: %v", qualifiedName, err)
	}
	ann := model.Annotation{NodeID: node.ID, Summary: tagValue}
	if err := deps.DB.Create(&ann).Error; err != nil {
		t.Fatalf("create annotation %s: %v", qualifiedName, err)
	}
	if err := deps.DB.Create(&model.DocTag{AnnotationID: ann.ID, Kind: tagKind, Value: tagValue, Ordinal: 0}).Error; err != nil {
		t.Fatalf("create doc tag %s: %v", qualifiedName, err)
	}
	if err := deps.DB.Create(&model.SearchDocument{Namespace: namespace, NodeID: node.ID, Content: searchContent, Language: "go"}).Error; err != nil {
		t.Fatalf("create search document %s: %v", qualifiedName, err)
	}
	return node
}

func seedRagTreeDBFallbackCommunity(t *testing.T, deps *Deps, namespace, communityKey, communityLabel, communityDescription, qualifiedName, name, filePath string) model.Node {
	t.Helper()
	comm := model.Community{Namespace: namespace, Key: communityKey, Label: communityLabel, Strategy: "test", Description: communityDescription}
	if err := deps.DB.Create(&comm).Error; err != nil {
		t.Fatalf("create community %s: %v", communityKey, err)
	}
	node := seedRetrieveDocsDBFallbackNode(t, deps, namespace, qualifiedName, name, filePath, model.TagIntent, communityDescription, communityDescription)
	if err := deps.DB.Create(&model.CommunityMembership{CommunityID: comm.ID, NodeID: node.ID}).Error; err != nil {
		t.Fatalf("create membership %s: %v", qualifiedName, err)
	}
	return node
}

func decodeRagTreeNode(t *testing.T, result *mcp.CallToolResult) *ragindex.TreeNode {
	t.Helper()
	var node ragindex.TreeNode
	if err := json.Unmarshal([]byte(getTextContent(result)), &node); err != nil {
		t.Fatalf("unmarshal get_rag_tree response: %v", err)
	}
	return &node
}

func rebuildRetrieveDocsSearchBackend(t *testing.T, deps *Deps, namespace string) {
	t.Helper()
	ctx := ctxns.WithNamespace(context.Background(), namespace)
	if err := deps.SearchBackend.Rebuild(ctx, deps.DB); err != nil {
		t.Fatalf("rebuild search backend for %s: %v", namespace, err)
	}
}

func writeRetrieveDocsMarkdown(t *testing.T, docsDir, filePath, content string) {
	t.Helper()
	docPath := filepath.Join(docsDir, filepath.FromSlash(filePath)+".md")
	if err := os.MkdirAll(filepath.Dir(docPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(docPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func decodeRetrieveDocsResponse(t *testing.T, result *mcp.CallToolResult) retrieveDocsResponse {
	t.Helper()
	var response retrieveDocsResponse
	if err := json.Unmarshal([]byte(getTextContent(result)), &response); err != nil {
		t.Fatalf("unmarshal retrieve response: %v", err)
	}
	return response
}

func decodeSearchDocsResults(t *testing.T, result *mcp.CallToolResult) []ragindex.SearchResult {
	t.Helper()
	var results []ragindex.SearchResult
	if err := json.Unmarshal([]byte(getTextContent(result)), &results); err != nil {
		t.Fatalf("unmarshal search_docs response: %v", err)
	}
	return results
}

func retrieveDocsHasField(fields []string, want string) bool {
	return slices.Contains(fields, want)
}
