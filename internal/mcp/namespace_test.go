package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gorm.io/gorm"

	"github.com/tae2089/code-context-graph/internal/analysis/query"
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

func TestMCPHandler_WorkspaceToNamespace(t *testing.T) {
	deps := setupTestDeps(t)
	seedNodeWithNamespace(t, deps.DB, "ns-a", "pkg.Foo", "function", "a/foo.go")
	seedNodeWithNamespace(t, deps.DB, "ns-b", "pkg.Foo", "function", "b/foo.go")

	result := callTool(t, deps, "get_node", map[string]any{"qualified_name": "pkg.Foo", "workspace": "ns-a"})
	if result.IsError {
		t.Fatalf("get_node with workspace ns-a returned error: %v", getTextContent(result))
	}
	text := getTextContent(result)
	if !strings.Contains(text, "a/foo.go") || strings.Contains(text, "b/foo.go") {
		t.Errorf("unexpected ns-a result: %s", text)
	}

	result2 := callTool(t, deps, "get_node", map[string]any{"qualified_name": "pkg.Foo", "workspace": "ns-b"})
	if result2.IsError {
		t.Fatalf("get_node with workspace ns-b returned error: %v", getTextContent(result2))
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
	if err := deps.SearchBackend.Rebuild(context.Background(), deps.DB); err != nil {
		t.Fatalf("rebuild search index: %v", err)
	}

	result := callTool(t, deps, "search", map[string]any{"query": "SearchMe", "workspace": "ns-a"})
	if result.IsError {
		t.Fatalf("search with workspace ns-a returned error: %v", getTextContent(result))
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

	result := callTool(t, deps, "list_graph_stats", map[string]any{"workspace": "ns-a"})
	if result.IsError {
		t.Fatalf("list_graph_stats with workspace ns-a returned error: %v", getTextContent(result))
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

	result := callTool(t, deps, "query_graph", map[string]any{"pattern": "callees_of", "target": "pkg.Caller", "workspace": "ns-a"})
	if result.IsError {
		t.Fatalf("query_graph with workspace ns-a returned error: %v", getTextContent(result))
	}
	text := getTextContent(result)
	if !strings.Contains(text, "pkg.Callee") {
		t.Errorf("expected callee 'pkg.Callee' in ns-a results, got: %s", text)
	}
}

func TestBuildRagIndex_WritesToWorkspaceIndexDir(t *testing.T) {
	deps := setupTestDeps(t)
	tmpDir := t.TempDir()
	deps.RagIndexDir = tmpDir

	wsDir := filepath.Join(tmpDir, "workspaces", "my-service", "docs")
	if err := os.MkdirAll(wsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	deps.WorkspaceRoot = filepath.Join(tmpDir, "workspaces")

	result := callTool(t, deps, "build_rag_index", map[string]any{"workspace": "my-service"})
	if result.IsError {
		t.Fatalf("build_rag_index with workspace error: %v", getTextContent(result))
	}

	wsIndexPath := filepath.Join(tmpDir, "my-service", "doc-index.json")
	if _, err := os.Stat(wsIndexPath); os.IsNotExist(err) {
		t.Errorf("expected workspace index at %s, but not found", wsIndexPath)
	}
}
