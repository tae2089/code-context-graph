package wikiserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/tae2089/code-context-graph/internal/ctxns"
	"github.com/tae2089/code-context-graph/internal/model"
	"github.com/tae2089/code-context-graph/internal/ragindex"
)

func newTestServer(t *testing.T) *Server {
	t.Helper()
	root := t.TempDir()
	staticDir := filepath.Join(root, "dist")
	if err := os.MkdirAll(staticDir, 0o755); err != nil {
		t.Fatalf("create static dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(staticDir, "index.html"), []byte("<div id=\"root\"></div>"), 0o644); err != nil {
		t.Fatalf("write index: %v", err)
	}
	ragDir := filepath.Join(root, ".ccg")
	if err := os.MkdirAll(ragDir, 0o755); err != nil {
		t.Fatalf("create rag dir: %v", err)
	}
	idx := &ragindex.Index{
		Version: 1,
		BuiltAt: time.Date(2026, 5, 5, 0, 0, 0, 0, time.UTC),
		Root: &ragindex.TreeNode{
			ID: "root", Label: "Root",
			Children: []*ragindex.TreeNode{
				{
					ID: "community:core", Label: "Core", Summary: "Core API",
					Children: []*ragindex.TreeNode{
						{ID: "file:docs/core.md", Label: "core.md", Summary: "Authentication docs", DocPath: "docs/core.md"},
						{ID: "file:docs/missing.md", Label: "missing.md", Summary: "Summary only", DocPath: "docs/missing.md"},
					},
				},
			},
		},
	}
	data, _ := json.Marshal(idx)
	if err := os.WriteFile(filepath.Join(ragDir, "wiki-index.json"), data, 0o644); err != nil {
		t.Fatalf("write index json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(ragDir, "doc-index.json"), data, 0o644); err != nil {
		t.Fatalf("write rag index json: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatalf("create docs dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "core.md"), []byte("# Core\n\nAuthentication docs"), 0o644); err != nil {
		t.Fatalf("write doc: %v", err)
	}

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.Node{}, &model.Edge{}); err != nil {
		t.Fatalf("migrate nodes: %v", err)
	}
	fileNode := model.Node{Namespace: ctxns.DefaultNamespace, QualifiedName: "main.go", Kind: model.NodeKindFile, Name: "main.go", FilePath: "main.go", StartLine: 1, EndLine: 20, Language: "go"}
	if err := db.Create(&fileNode).Error; err != nil {
		t.Fatalf("create file node: %v", err)
	}
	funcNode := model.Node{Namespace: ctxns.DefaultNamespace, QualifiedName: "main.Run", Kind: model.NodeKindFunction, Name: "Run", FilePath: "main.go", StartLine: 3, EndLine: 8, Language: "go"}
	if err := db.Create(&funcNode).Error; err != nil {
		t.Fatalf("create function node: %v", err)
	}
	if err := db.Create(&model.Edge{Namespace: ctxns.DefaultNamespace, FromNodeID: fileNode.ID, ToNodeID: funcNode.ID, Kind: model.EdgeKindContains, FilePath: "main.go", Line: 3, Fingerprint: "default-contains-run"}).Error; err != nil {
		t.Fatalf("create edge: %v", err)
	}
	if err := db.Create(&model.Node{Namespace: "sample-go", QualifiedName: "main.Run", Kind: model.NodeKindFunction, Name: "Run", FilePath: "main.go"}).Error; err != nil {
		t.Fatalf("create node: %v", err)
	}

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("get cwd: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(cwd); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	})
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	srv, err := New(Config{StaticDir: staticDir, RagIndexDir: ragDir, NamespaceRoot: filepath.Join(root, "workspaces"), DB: db})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	return srv
}

func TestStaticHandler_FallsBackToIndex(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/wiki/some/client/route", nil)
	rec := httptest.NewRecorder()

	srv.StaticHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "root") {
		t.Fatalf("expected index body, got %q", rec.Body.String())
	}
}

func TestAPI_NamespacesMergesDatabaseAndIndex(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/wiki/api/namespaces", nil)
	rec := httptest.NewRecorder()

	srv.APIHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var got struct {
		Namespaces []string `json:"namespaces"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !contains(got.Namespaces, ctxns.DefaultNamespace) || !contains(got.Namespaces, "sample-go") {
		t.Fatalf("namespaces = %#v", got.Namespaces)
	}
}

func TestAPI_SearchAndDoc(t *testing.T) {
	srv := newTestServer(t)

	searchReq := httptest.NewRequest(http.MethodGet, "/wiki/api/search?q=auth&namespace=default", nil)
	searchRec := httptest.NewRecorder()
	srv.APIHandler().ServeHTTP(searchRec, searchReq)
	if searchRec.Code != http.StatusOK {
		t.Fatalf("search status = %d body=%s", searchRec.Code, searchRec.Body.String())
	}
	if !strings.Contains(searchRec.Body.String(), "core.md") {
		t.Fatalf("expected search result, got %s", searchRec.Body.String())
	}

	docReq := httptest.NewRequest(http.MethodGet, "/wiki/api/doc?namespace=default&path=docs/core.md", nil)
	docRec := httptest.NewRecorder()
	srv.APIHandler().ServeHTTP(docRec, docReq)
	if docRec.Code != http.StatusOK {
		t.Fatalf("doc status = %d body=%s", docRec.Code, docRec.Body.String())
	}
	if !strings.Contains(docRec.Body.String(), "Authentication docs") {
		t.Fatalf("expected doc content, got %s", docRec.Body.String())
	}
}

func TestAPI_RetrieveUsesDocIndex(t *testing.T) {
	srv := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/wiki/api/retrieve?q=auth&namespace=default&limit=5", nil)
	rec := httptest.NewRecorder()
	srv.APIHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("retrieve status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "core.md") || !strings.Contains(rec.Body.String(), "matched_terms") {
		t.Fatalf("expected retrieve result with evidence, got %s", rec.Body.String())
	}
}

func TestAPI_ContextReturnsPerItemMarkdown(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/wiki/api/context", strings.NewReader(`{"namespace":"default","paths":["docs/core.md","docs/missing.md"]}`))
	rec := httptest.NewRecorder()

	srv.APIHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("context status = %d body=%s", rec.Code, rec.Body.String())
	}
	var got contextResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode context: %v", err)
	}
	if len(got.Items) != 2 {
		t.Fatalf("items = %d, want 2", len(got.Items))
	}
	if !strings.Contains(got.Items[0].Markdown, "Authentication docs") {
		t.Fatalf("first item markdown = %q", got.Items[0].Markdown)
	}
	if !strings.Contains(got.Items[1].Markdown, "Summary only") {
		t.Fatalf("second item markdown = %q", got.Items[1].Markdown)
	}
	if !strings.Contains(got.Markdown, "docs/core.md") || !strings.Contains(got.Markdown, "missing.md") {
		t.Fatalf("combined markdown = %q", got.Markdown)
	}
}

func TestAPI_GraphReturnsNodesAndEdges(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/wiki/api/graph?namespace=default&edge_kinds=contains", nil)
	rec := httptest.NewRecorder()

	srv.APIHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("graph status = %d body=%s", rec.Code, rec.Body.String())
	}
	var got graphResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode graph: %v", err)
	}
	if got.Namespace != ctxns.DefaultNamespace {
		t.Fatalf("namespace = %q", got.Namespace)
	}
	if len(got.Nodes) != 2 {
		t.Fatalf("nodes = %d, want 2", len(got.Nodes))
	}
	if len(got.Edges) != 1 {
		t.Fatalf("edges = %d, want 1", len(got.Edges))
	}
	if got.Edges[0].Kind != string(model.EdgeKindContains) {
		t.Fatalf("edge kind = %q", got.Edges[0].Kind)
	}
}

func TestAPI_DocAllowsAbsolutePathUnderRoot(t *testing.T) {
	srv := newTestServer(t)
	absPath, err := filepath.Abs(filepath.Join("docs", "core.md"))
	if err != nil {
		t.Fatalf("abs path: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/wiki/api/doc?namespace=default&path="+absPath, nil)
	rec := httptest.NewRecorder()

	srv.APIHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Authentication docs") {
		t.Fatalf("expected doc content, got %s", rec.Body.String())
	}
}

func TestAPI_DocFallsBackToTreeSummaryWhenFileMissing(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/wiki/api/doc?namespace=default&path=docs/missing.md", nil)
	rec := httptest.NewRecorder()

	srv.APIHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Summary only") {
		t.Fatalf("expected summary fallback, got %s", rec.Body.String())
	}
}

func TestAPI_DocRejectsTraversal(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/wiki/api/doc?namespace=default&path=../secret.md", nil)
	rec := httptest.NewRecorder()

	srv.APIHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func contains(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
