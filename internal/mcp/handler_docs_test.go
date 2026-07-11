package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/tae2089/code-context-graph/internal/ctxns"
	"github.com/tae2089/code-context-graph/internal/model"
	"github.com/tae2089/code-context-graph/internal/ragindex"
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
	deps.RagIndexDir = t.TempDir()

	content := "# Shared Doc\nfrom shared docs root"
	docPath := filepath.Join(deps.RagIndexDir, "docs", "shared-doc.md")
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

func TestSearchDocs_DBFallbackDomainRuleUsesContextNamespace(t *testing.T) {
	deps := setupTestDeps(t)
	tmpDir := t.TempDir()
	deps.NamespaceRoot = filepath.Join(tmpDir, "namespaces")
	deps.RagIndexDir = filepath.Join(tmpDir, ".ccg")
	alpha := "alpha-service"
	beta := "beta-service"
	seedRetrieveDocsDBFallbackNode(t, deps, alpha, "alpha.Guard", "Guard", "guard.go", model.TagDomainRule, "breakglass alpha approval", "breakglass alpha approval")
	seedRetrieveDocsDBFallbackNode(t, deps, beta, "beta.Guard", "Guard", "guard.go", model.TagDomainRule, "breakglass beta approval", "breakglass beta approval")
	rebuildRetrieveDocsSearchBackend(t, deps, alpha)
	rebuildRetrieveDocsSearchBackend(t, deps, beta)

	result := callToolWithNamespace(t, deps, alpha, "search_docs", map[string]any{"query": "breakglass", "limit": float64(5)})
	if result.IsError {
		t.Fatalf("search_docs context namespace DB fallback error: %v", getTextContent(result))
	}

	results := decodeSearchDocsResults(t, result)
	if len(results) != 1 || results[0].Summary != "breakglass alpha approval" {
		t.Fatalf("expected only alpha domainRule result, got %#v", results)
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

func decodeSearchDocsResults(t *testing.T, result *mcp.CallToolResult) []ragindex.SearchResult {
	t.Helper()
	var results []ragindex.SearchResult
	if err := json.Unmarshal([]byte(getTextContent(result)), &results); err != nil {
		t.Fatalf("unmarshal search_docs response: %v", err)
	}
	return results
}
