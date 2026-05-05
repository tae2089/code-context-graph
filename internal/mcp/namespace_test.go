package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gorm.io/gorm"

	"github.com/tae2089/code-context-graph/internal/analysis/query"
	"github.com/tae2089/code-context-graph/internal/ctxns"
	"github.com/tae2089/code-context-graph/internal/model"
)

func seedNodeWithNamespace(t *testing.T, db *gorm.DB, ns, qn, kind, filePath string) {
	t.Helper()
	node := model.Node{
		Namespace:     ns,
		QualifiedName: qn,
		Kind:          model.NodeKind(kind),
		Name:          qn,
		FilePath:      filePath,
		StartLine:     1,
		EndLine:       10,
		Language:      "go",
	}
	if err := db.Create(&node).Error; err != nil {
		t.Fatalf("seedNodeWithNamespace: %v", err)
	}
}

func TestMCPHandler_NamespaceFiltersGraph(t *testing.T) {
	deps := setupTestDeps(t)
	seedNodeWithNamespace(t, deps.DB, "ns-a", "pkg.Foo", "function", "a/foo.go")
	seedNodeWithNamespace(t, deps.DB, "ns-b", "pkg.Foo", "function", "b/foo.go")

	result := callTool(t, deps, "get_node", map[string]any{"qualified_name": "pkg.Foo", "namespace": "ns-a"})
	if result.IsError {
		t.Fatalf("get_node with namespace ns-a returned error: %v", getTextContent(result))
	}
	text := getTextContent(result)
	if !strings.Contains(text, "a/foo.go") || strings.Contains(text, "b/foo.go") {
		t.Errorf("unexpected ns-a result: %s", text)
	}

	result2 := callTool(t, deps, "get_node", map[string]any{"qualified_name": "pkg.Foo", "namespace": "ns-b"})
	if result2.IsError {
		t.Fatalf("get_node with namespace ns-b returned error: %v", getTextContent(result2))
	}
	text2 := getTextContent(result2)
	if !strings.Contains(text2, "b/foo.go") {
		t.Errorf("expected file_path 'b/foo.go' for ns-b, got: %s", text2)
	}
}

func TestMCPHandler_SearchWithNamespace(t *testing.T) {
	deps := setupTestDeps(t)
	seedNodeWithNamespace(t, deps.DB, "ns-a", "pkg.SearchMe", "function", "a/search.go")
	seedNodeWithNamespace(t, deps.DB, "ns-b", "pkg.SearchMe", "function", "b/search.go")

	for _, ns := range []string{"ns-a", "ns-b"} {
		var node model.Node
		deps.DB.Where("namespace = ? AND qualified_name = ?", ns, "pkg.SearchMe").First(&node)
		doc := model.SearchDocument{Namespace: ns, NodeID: node.ID, Content: "SearchMe function implementation", Language: "go"}
		if err := deps.DB.Create(&doc).Error; err != nil {
			t.Fatalf("create SearchDocument for %s: %v", ns, err)
		}
	}
	for _, ns := range []string{"ns-a", "ns-b"} {
		ctx := ctxns.WithNamespace(context.Background(), ns)
		if err := deps.SearchBackend.Rebuild(ctx, deps.DB); err != nil {
			t.Fatalf("rebuild search index for %s: %v", ns, err)
		}
	}

	result := callTool(t, deps, "search", map[string]any{"query": "SearchMe", "namespace": "ns-a"})
	if result.IsError {
		t.Fatalf("search with namespace ns-a returned error: %v", getTextContent(result))
	}
	text := getTextContent(result)
	if !strings.Contains(text, "a/search.go") || strings.Contains(text, "b/search.go") {
		t.Errorf("unexpected ns-a search result: %s", text)
	}
}

func TestMCPHandler_GraphWithNamespace(t *testing.T) {
	deps := setupTestDeps(t)
	seedNodeWithNamespace(t, deps.DB, "ns-a", "pkg.Alpha", "function", "a/alpha.go")
	seedNodeWithNamespace(t, deps.DB, "ns-a", "pkg.Beta", "function", "a/beta.go")
	seedNodeWithNamespace(t, deps.DB, "ns-b", "pkg.Gamma", "function", "b/gamma.go")

	result := callTool(t, deps, "list_graph_stats", map[string]any{"namespace": "ns-a"})
	if result.IsError {
		t.Fatalf("list_graph_stats with namespace ns-a returned error: %v", getTextContent(result))
	}
	text := getTextContent(result)

	var stats map[string]any
	if err := json.Unmarshal([]byte(text), &stats); err != nil {
		t.Fatalf("unmarshal stats: %v", err)
	}
	totalNodes, ok := stats["total_nodes"].(float64)
	if !ok || int(totalNodes) != 2 {
		t.Errorf("expected 2 nodes for ns-a, got %v", stats["total_nodes"])
	}
}

func TestMCPHandler_ListGraphStats_EmptyNamespaceDoesNotIncludeOtherNamespaces(t *testing.T) {
	deps := setupTestDeps(t)
	seedNodeWithNamespace(t, deps.DB, "default", "pkg.Default", "function", "default.go")
	seedNodeWithNamespace(t, deps.DB, "ns-a", "pkg.Other", "function", "other.go")

	result := callTool(t, deps, "list_graph_stats", map[string]any{})
	if result.IsError {
		t.Fatalf("list_graph_stats returned error: %v", getTextContent(result))
	}
	text := getTextContent(result)

	var stats map[string]any
	if err := json.Unmarshal([]byte(text), &stats); err != nil {
		t.Fatalf("unmarshal stats: %v", err)
	}
	totalNodes, ok := stats["total_nodes"].(float64)
	if !ok || int(totalNodes) != 1 {
		t.Fatalf("expected 1 default-namespace node, got %v", stats["total_nodes"])
	}
}

func TestResolveNamespace_EmptyNamespaceFallsBackToDefault(t *testing.T) {
	got := resolveNamespace(context.Background(), "")
	if got != "default" {
		t.Fatalf("resolveNamespace() = %q, want %q", got, "default")
	}
}

func TestMCPHandler_QueryWithNamespace(t *testing.T) {
	deps := setupTestDeps(t)
	deps.QueryService = query.New(deps.DB)
	seedNodeWithNamespace(t, deps.DB, "ns-a", "pkg.Caller", "function", "a/caller.go")
	seedNodeWithNamespace(t, deps.DB, "ns-a", "pkg.Callee", "function", "a/callee.go")
	seedNodeWithNamespace(t, deps.DB, "ns-b", "pkg.Caller", "function", "b/caller.go")

	var callerA, calleeA model.Node
	deps.DB.Where("namespace = ? AND qualified_name = ?", "ns-a", "pkg.Caller").First(&callerA)
	deps.DB.Where("namespace = ? AND qualified_name = ?", "ns-a", "pkg.Callee").First(&calleeA)
	edge := model.Edge{FromNodeID: callerA.ID, ToNodeID: calleeA.ID, Kind: "calls", Fingerprint: fmt.Sprintf("calls:%d:%d", callerA.ID, calleeA.ID)}
	edge.Namespace = "ns-a"
	if err := deps.DB.Create(&edge).Error; err != nil {
		t.Fatalf("create edge: %v", err)
	}

	result := callTool(t, deps, "query_graph", map[string]any{"pattern": "callees_of", "target": "pkg.Caller", "namespace": "ns-a"})
	if result.IsError {
		t.Fatalf("query_graph with namespace ns-a returned error: %v", getTextContent(result))
	}
	text := getTextContent(result)
	if !strings.Contains(text, "pkg.Callee") {
		t.Errorf("expected callee 'pkg.Callee' in ns-a results, got: %s", text)
	}
}

func TestMCPHandler_QueryWithNamespace_IncludesNamespaceEvidence(t *testing.T) {
	deps := setupTestDeps(t)
	deps.QueryService = query.New(deps.DB)
	deps.NamespaceRoot = t.TempDir()

	ns := "ns-evidence"
	namespaceDir := filepath.Join(deps.NamespaceRoot, ns)
	if err := os.MkdirAll(namespaceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = namespaceDir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=ccg", "GIT_AUTHOR_EMAIL=ccg@example.com", "GIT_COMMITTER_NAME=ccg", "GIT_COMMITTER_EMAIL=ccg@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, out)
		}
	}
	runGit("init")
	runGit("add", "-A")
	if err := os.WriteFile(filepath.Join(namespaceDir, "stub.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit("add", "stub.go")
	runGit("commit", "-m", "init")

	seedNodeWithNamespace(t, deps.DB, ns, "pkg.Caller", "function", "a/caller.go")
	seedNodeWithNamespace(t, deps.DB, ns, "pkg.Callee", "function", "a/callee.go")
	caller, callee := model.Node{}, model.Node{}
	deps.DB.Where("namespace = ? AND qualified_name = ?", ns, "pkg.Caller").First(&caller)
	deps.DB.Where("namespace = ? AND qualified_name = ?", ns, "pkg.Callee").First(&callee)
	if err := deps.DB.Create(&model.Edge{
		FromNodeID:  caller.ID,
		ToNodeID:    callee.ID,
		Kind:        "calls",
		Fingerprint: "calls:caller:callee",
		Namespace:   ns,
	}).Error; err != nil {
		t.Fatal(err)
	}

	result := callTool(t, deps, "query_graph", map[string]any{"pattern": "callees_of", "target": "pkg.Caller", "namespace": ns})
	if result.IsError {
		t.Fatalf("query_graph with namespace ns returned error: %v", getTextContent(result))
	}

	var body map[string]any
	if err := json.Unmarshal([]byte(getTextContent(result)), &body); err != nil {
		t.Fatalf("expected JSON response: %v", err)
	}
	evidence, ok := body["evidence"].(map[string]any)
	if !ok {
		t.Fatal("expected evidence block in query_graph response")
	}
	if evidence["namespace"] != ns {
		t.Fatalf("expected evidence namespace %q, got %v", ns, evidence["namespace"])
	}
	if evidence["namespace_path"] != namespaceDir {
		t.Fatalf("expected namespace path %q, got %v", namespaceDir, evidence["namespace_path"])
	}
	gitInfo, ok := evidence["git"].(map[string]any)
	if !ok {
		t.Fatal("expected git evidence when namespace is git worktree")
	}
	if gitInfo["branch"] == nil {
		t.Fatal("expected branch in git evidence")
	}
}

func TestBuildRagIndex_WritesToNamespaceIndexDir(t *testing.T) {
	deps := setupTestDeps(t)
	tmpDir := t.TempDir()
	deps.RagIndexDir = tmpDir

	nsDir := filepath.Join(tmpDir, "namespaces", "my-service", "docs")
	if err := os.MkdirAll(nsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	deps.NamespaceRoot = filepath.Join(tmpDir, "namespaces")

	result := callTool(t, deps, "build_rag_index", map[string]any{"namespace": "my-service"})
	if result.IsError {
		t.Fatalf("build_rag_index with namespace error: %v", getTextContent(result))
	}

	wsIndexPath := filepath.Join(tmpDir, "my-service", "doc-index.json")
	if _, err := os.Stat(wsIndexPath); os.IsNotExist(err) {
		t.Errorf("expected namespace index at %s, but not found", wsIndexPath)
	}
}
