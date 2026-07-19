package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func seedFederatedNamespaces(t *testing.T, deps *Deps) {
	t.Helper()
	write := func(dir, rel, content string) string {
		t.Helper()
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		return full
	}
	alphaDir := t.TempDir()
	betaDir := t.TempDir()
	deps.Runtime.RepoRoot = filepath.Dir(alphaDir)
	write(alphaDir, "pay.go", "package alpha\n\nfunc PaymentProcess() string {\n\treturn \"payment\"\n}\n")
	write(betaDir, "refund.go", "package beta\n\nfunc PaymentRefund() string {\n\treturn \"payment\"\n}\n")

	deps.Runtime.RepoRoot = alphaDir
	if res := callTool(t, deps, "build_or_update_graph", map[string]any{"path": alphaDir, "namespace": "alpha"}); res.IsError {
		t.Fatalf("parse alpha failed: %+v", res)
	}
	deps.Runtime.RepoRoot = betaDir
	if res := callTool(t, deps, "build_or_update_graph", map[string]any{"path": betaDir, "namespace": "beta"}); res.IsError {
		t.Fatalf("parse beta failed: %+v", res)
	}
}

func resultTextOf(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()
	if result.IsError {
		t.Fatalf("tool returned error: %+v", result.Content)
	}
	for _, content := range result.Content {
		if text, ok := content.(mcp.TextContent); ok {
			return text.Text
		}
		if text, ok := content.(*mcp.TextContent); ok {
			return text.Text
		}
	}
	t.Fatal("no text content in result")
	return ""
}

func TestSearch_FederatesAcrossNamespaces(t *testing.T) {
	deps := setupTestDeps(t)
	seedFederatedNamespaces(t, deps)

	result := callTool(t, deps, "search", map[string]any{
		"query":      "Payment",
		"namespaces": []string{"alpha", "beta"},
	})
	var items []struct {
		QualifiedName string `json:"qualified_name"`
		Namespace     string `json:"namespace"`
	}
	if err := json.Unmarshal([]byte(resultTextOf(t, result)), &items); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	seen := map[string]string{}
	for _, item := range items {
		seen[item.QualifiedName] = item.Namespace
	}
	if seen["alpha.PaymentProcess"] != "alpha" || seen["beta.PaymentRefund"] != "beta" {
		t.Fatalf("federated search items = %v, want hits from both namespaces with labels", seen)
	}
}

func TestSearch_SingleNamespaceResponseUnchanged(t *testing.T) {
	deps := setupTestDeps(t)
	seedFederatedNamespaces(t, deps)

	result := callTool(t, deps, "search", map[string]any{"query": "Payment", "namespace": "alpha"})
	text := resultTextOf(t, result)
	var raw []map[string]any
	if err := json.Unmarshal([]byte(text), &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(raw) == 0 {
		t.Fatal("single-namespace search returned no results")
	}
	for _, item := range raw {
		if _, exists := item["namespace"]; exists {
			t.Fatalf("single-namespace response gained a namespace field: %v", item)
		}
	}
}

func TestListGraphStats_FederatesAcrossNamespaces(t *testing.T) {
	deps := setupTestDeps(t)
	seedFederatedNamespaces(t, deps)

	result := callTool(t, deps, "list_graph_stats", map[string]any{"namespaces": []string{"alpha", "beta"}})
	var payload struct {
		Namespaces []struct {
			Namespace  string `json:"namespace"`
			TotalNodes int64  `json:"total_nodes"`
		} `json:"namespaces"`
	}
	if err := json.Unmarshal([]byte(resultTextOf(t, result)), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(payload.Namespaces) != 2 {
		t.Fatalf("stats groups = %d, want 2", len(payload.Namespaces))
	}
	for _, group := range payload.Namespaces {
		if group.TotalNodes == 0 {
			t.Fatalf("namespace %q has zero nodes in federated stats", group.Namespace)
		}
	}
}

func TestQueryGraph_FederatesAcrossNamespaces(t *testing.T) {
	deps := setupTestDeps(t)
	seedFederatedNamespaces(t, deps)

	result := callTool(t, deps, "query_graph", map[string]any{
		"pattern":    "callers_of",
		"target":     "alpha.PaymentProcess",
		"namespaces": []string{"alpha", "beta"},
	})
	var payload struct {
		Pattern    string `json:"pattern"`
		Namespaces []struct {
			Namespace string          `json:"namespace"`
			Response  json.RawMessage `json:"response,omitempty"`
			Error     string          `json:"error,omitempty"`
		} `json:"namespaces"`
	}
	if err := json.Unmarshal([]byte(resultTextOf(t, result)), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(payload.Namespaces) != 2 {
		t.Fatalf("query groups = %d, want 2", len(payload.Namespaces))
	}
	byNS := map[string]json.RawMessage{}
	errsByNS := map[string]string{}
	for _, group := range payload.Namespaces {
		byNS[group.Namespace] = group.Response
		errsByNS[group.Namespace] = group.Error
	}
	if len(byNS["alpha"]) == 0 {
		t.Fatalf("alpha response missing: %+v", payload)
	}
	if errsByNS["beta"] == "" {
		t.Fatalf("beta should report a per-namespace error for missing file, got %+v", payload)
	}
}

func TestSearchDocs_FederatesAcrossNamespaces(t *testing.T) {
	deps := setupTestDeps(t)
	seedFederatedNamespaces(t, deps)

	result := callTool(t, deps, "search_docs", map[string]any{
		"query":      "Payment",
		"namespaces": []string{"alpha", "beta"},
	})
	var payload struct {
		Namespaces []struct {
			Namespace string `json:"namespace"`
			Results   []struct {
				Label string `json:"label"`
			} `json:"results"`
		} `json:"namespaces"`
	}
	if err := json.Unmarshal([]byte(resultTextOf(t, result)), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(payload.Namespaces) != 2 {
		t.Fatalf("docs groups = %d, want 2", len(payload.Namespaces))
	}
	for _, group := range payload.Namespaces {
		if len(group.Results) == 0 {
			t.Fatalf("namespace %q returned no doc candidates", group.Namespace)
		}
	}
}
