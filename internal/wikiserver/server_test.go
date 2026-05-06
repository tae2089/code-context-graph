package wikiserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/tae2089/code-context-graph/internal/ctxns"
	"github.com/tae2089/code-context-graph/internal/model"
	"github.com/tae2089/code-context-graph/internal/ragindex"
	"github.com/tae2089/code-context-graph/internal/retrieval"
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
	authIdx := &ragindex.Index{
		Version: 1,
		BuiltAt: time.Date(2026, 5, 5, 0, 0, 0, 0, time.UTC),
		Root: &ragindex.TreeNode{
			ID: "root", Label: "Root",
			Children: []*ragindex.TreeNode{
				{
					ID: "folder:internal", Label: "internal", Kind: "folder",
					Children: []*ragindex.TreeNode{
						{
							ID: "folder:internal/auth", Label: "auth", Kind: "folder",
							Children: []*ragindex.TreeNode{
								{
									ID: "file:internal/auth/token.go", Label: "token.go", Kind: "file", DocPath: "docs/internal/auth/token.go.md", Summary: "Token file",
									Children: []*ragindex.TreeNode{
										{
											ID: "symbol:auth.ValidateToken", Label: "ValidateToken", Kind: "function", Summary: "validates tokens",
											Details: &ragindex.NodeDetails{
												QualifiedName: "auth.ValidateToken",
												FilePath:      "internal/auth/token.go",
												StartLine:     10,
												EndLine:       24,
												Language:      "go",
											},
											Children: []*ragindex.TreeNode{},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}
	authData, _ := json.Marshal(authIdx)
	if err := os.MkdirAll(filepath.Join(ragDir, "auth-svc"), 0o755); err != nil {
		t.Fatalf("create auth namespace index dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(ragDir, "auth-svc", "wiki-index.json"), authData, 0o644); err != nil {
		t.Fatalf("write auth wiki index json: %v", err)
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
	if err := db.AutoMigrate(&model.Node{}, &model.Edge{}, &model.Annotation{}, &model.DocTag{}); err != nil {
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
	authNode := model.Node{Namespace: "auth-svc", QualifiedName: "auth.ValidateToken", Kind: model.NodeKindFunction, Name: "ValidateToken", FilePath: "internal/auth/token.go", StartLine: 10, EndLine: 24, Language: "go"}
	if err := db.Create(&authNode).Error; err != nil {
		t.Fatalf("create auth node: %v", err)
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

	srv, err := New(Config{StaticDir: staticDir, RagIndexDir: ragDir, NamespaceRoot: filepath.Join(root, "namespaces"), DB: db})
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

	searchReq := httptest.NewRequest(http.MethodGet, "/wiki/api/search?q=Run&namespace=default", nil)
	searchRec := httptest.NewRecorder()
	srv.APIHandler().ServeHTTP(searchRec, searchReq)
	if searchRec.Code != http.StatusOK {
		t.Fatalf("search status = %d body=%s", searchRec.Code, searchRec.Body.String())
	}
	if !strings.Contains(searchRec.Body.String(), "main.go") {
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

func TestAPI_RetrieveUsesDBWhenAvailable(t *testing.T) {
	srv := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/wiki/api/retrieve?q=Run&namespace=default&limit=5", nil)
	rec := httptest.NewRecorder()
	srv.APIHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("retrieve status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "main.go") || !strings.Contains(rec.Body.String(), "matched_terms") {
		t.Fatalf("expected DB retrieve result with evidence, got %s", rec.Body.String())
	}
}

func TestAPI_RetrieveFallsBackToDBWhenDocIndexMissing(t *testing.T) {
	srv := newDBFallbackTestServer(t)
	seedWikiRetrieveNode(t, srv.db, ctxns.DefaultNamespace, "billing.Processor", "Processor", "internal/billing/processor.go", model.TagIntent, "payment settlement workflow")
	writeWikiRetrieveDoc(t, "docs/internal/billing/processor.go.md", "# processor.go\n\npayment settlement workflow docs")

	req := httptest.NewRequest(http.MethodGet, "/wiki/api/retrieve?q=payment+settlement&namespace=default&limit=5&content_limit=2000", nil)
	rec := httptest.NewRecorder()
	srv.APIHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("retrieve fallback status = %d body=%s", rec.Code, rec.Body.String())
	}
	var got struct {
		Namespace string             `json:"namespace"`
		Results   []retrieval.Result `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode retrieve fallback: %v", err)
	}
	if got.Namespace != ctxns.DefaultNamespace {
		t.Fatalf("namespace = %q", got.Namespace)
	}
	if len(got.Results) != 1 {
		t.Fatalf("results = %d, want 1: %#v", len(got.Results), got.Results)
	}
	if got.Results[0].DocPath != "docs/internal/billing/processor.go.md" {
		t.Fatalf("doc_path = %q", got.Results[0].DocPath)
	}
	if !strings.Contains(got.Results[0].Content, "payment settlement workflow docs") {
		t.Fatalf("content = %q", got.Results[0].Content)
	}
}

func TestAPI_RetrieveFallbackReturnsEmptyWhenDBHasNoMatch(t *testing.T) {
	srv := newDBFallbackTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/wiki/api/retrieve?q=absent&namespace=default&limit=5", nil)
	rec := httptest.NewRecorder()
	srv.APIHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("retrieve fallback empty status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"results":[]`) {
		t.Fatalf("expected empty results, got %s", rec.Body.String())
	}
}

func TestAPI_RetrieveFallbackTruncatesContent(t *testing.T) {
	srv := newDBFallbackTestServer(t)
	seedWikiRetrieveNode(t, srv.db, ctxns.DefaultNamespace, "docs.Long", "Long", "internal/docs/long.go", model.TagIntent, "longcontent marker")
	writeWikiRetrieveDoc(t, "docs/internal/docs/long.go.md", "0123456789abcdef")

	req := httptest.NewRequest(http.MethodGet, "/wiki/api/retrieve?q=longcontent&namespace=default&limit=5&content_limit=5", nil)
	rec := httptest.NewRecorder()
	srv.APIHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("retrieve fallback truncate status = %d body=%s", rec.Code, rec.Body.String())
	}
	var got struct {
		Results []retrieval.Result `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode truncate response: %v", err)
	}
	if len(got.Results) != 1 {
		t.Fatalf("results = %d, want 1", len(got.Results))
	}
	if got.Results[0].Content != "01234" || !got.Results[0].ContentTruncated {
		t.Fatalf("content/truncated = %q/%v", got.Results[0].Content, got.Results[0].ContentTruncated)
	}
}

func TestAPI_RetrieveFallbackIsNamespaceScoped(t *testing.T) {
	srv := newDBFallbackTestServer(t)
	seedWikiRetrieveNode(t, srv.db, "alpha", "alpha.Checkout", "Checkout", "checkout.go", model.TagIntent, "sharedtenant alpha checkout")
	seedWikiRetrieveNode(t, srv.db, "beta", "beta.Checkout", "Checkout", "checkout.go", model.TagIntent, "sharedtenant beta checkout")
	writeWikiRetrieveDoc(t, filepath.Join("namespaces", "alpha", "docs", "checkout.go.md"), "# checkout\n\nalpha checkout docs")
	writeWikiRetrieveDoc(t, filepath.Join("namespaces", "beta", "docs", "checkout.go.md"), "# checkout\n\nbeta checkout docs")

	req := httptest.NewRequest(http.MethodGet, "/wiki/api/retrieve?q=sharedtenant+checkout&namespace=alpha&limit=5&content_limit=2000", nil)
	rec := httptest.NewRecorder()
	srv.APIHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("retrieve fallback namespace status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "alpha checkout docs") || strings.Contains(rec.Body.String(), "beta checkout docs") {
		t.Fatalf("namespace-scoped response leaked or missed docs: %s", rec.Body.String())
	}
}

func TestAPI_RetrieveFallbackNamedNamespaceDoesNotReadSharedDocs(t *testing.T) {
	srv := newDBFallbackTestServer(t)
	seedWikiRetrieveNode(t, srv.db, "alpha", "alpha.Checkout", "Checkout", "checkout.go", model.TagIntent, "sharedtenant alpha checkout")
	writeWikiRetrieveDoc(t, filepath.Join("docs", "checkout.go.md"), "# checkout\n\nshared docs must not leak")

	req := httptest.NewRequest(http.MethodGet, "/wiki/api/retrieve?q=sharedtenant+checkout&namespace=alpha&limit=5&content_limit=2000", nil)
	rec := httptest.NewRecorder()
	srv.APIHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("retrieve fallback namespace leak status = %d body=%s", rec.Code, rec.Body.String())
	}
	var got struct {
		Results []retrieval.Result `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode retrieve fallback response: %v", err)
	}
	if len(got.Results) != 1 {
		t.Fatalf("results = %d, want 1: %#v", len(got.Results), got.Results)
	}
	if got.Results[0].Content != "" || strings.Contains(rec.Body.String(), "shared docs must not leak") {
		t.Fatalf("named namespace read shared docs content: %#v body=%s", got.Results[0], rec.Body.String())
	}
}

func TestAPI_TreeFallsBackToDBWhenWikiIndexMissing(t *testing.T) {
	srv := newDBFallbackTestServer(t)
	seedWikiRetrieveNode(t, srv.db, ctxns.DefaultNamespace, "billing.Processor", "Processor", "internal/billing/processor.go", model.TagIntent, "payment settlement workflow")

	req := httptest.NewRequest(http.MethodGet, "/wiki/api/tree?namespace=default", nil)
	rec := httptest.NewRecorder()
	srv.APIHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("tree fallback status = %d body=%s", rec.Code, rec.Body.String())
	}
	var got struct {
		Namespace string             `json:"namespace"`
		BuiltAt   time.Time          `json:"built_at"`
		Root      *ragindex.TreeNode `json:"root"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode tree fallback: %v", err)
	}
	if got.Namespace != ctxns.DefaultNamespace || got.Root == nil {
		t.Fatalf("response = %#v", got)
	}
	if findTreeNode(got.Root, "symbol:billing.Processor") == nil {
		t.Fatalf("expected DB symbol in tree: %#v", got.Root)
	}
	if _, err := os.Stat(filepath.Join(srv.ragIndexDir, "wiki-index.json")); !os.IsNotExist(err) {
		t.Fatalf("fallback wrote or found wiki-index.json: %v", err)
	}
	if got.BuiltAt.IsZero() {
		t.Fatalf("built_at should be populated for response stability")
	}
}

func TestAPI_TreeFallbackSupportsLazyNodeExpansion(t *testing.T) {
	srv := newDBFallbackTestServer(t)
	seedWikiRetrieveNode(t, srv.db, ctxns.DefaultNamespace, "billing.Processor", "Processor", "internal/billing/processor.go", model.TagIntent, "payment settlement workflow")

	req := httptest.NewRequest(http.MethodGet, "/wiki/api/tree?namespace=default&depth=1", nil)
	rec := httptest.NewRecorder()
	srv.APIHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("tree lazy root status = %d body=%s", rec.Code, rec.Body.String())
	}
	var got struct {
		Root *ragindex.TreeNode `json:"root"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode lazy root: %v", err)
	}
	internal := findTreeNode(got.Root, "folder:internal")
	if internal == nil || !internal.HasChildren || len(internal.Children) != 0 {
		t.Fatalf("lazy root internal = %#v", internal)
	}
	if strings.Contains(rec.Body.String(), "symbol:billing.Processor") {
		t.Fatalf("lazy root should not include deep symbol: %s", rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/wiki/api/tree?namespace=default&node_id=file:internal/billing/processor.go&depth=1", nil)
	rec = httptest.NewRecorder()
	srv.APIHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("tree lazy file status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "symbol:billing.Processor") {
		t.Fatalf("lazy file expansion should include symbol: %s", rec.Body.String())
	}
}

func TestAPI_TreePrefersDBWhenWikiIndexPresent(t *testing.T) {
	srv := newTestServer(t)
	seedWikiRetrieveNode(t, srv.db, ctxns.DefaultNamespace, "billing.Processor", "Processor", "internal/billing/processor.go", model.TagIntent, "payment settlement workflow")

	req := httptest.NewRequest(http.MethodGet, "/wiki/api/tree?namespace=default", nil)
	rec := httptest.NewRecorder()
	srv.APIHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("tree status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "billing.Processor") {
		t.Fatalf("tree should prefer DB over wiki-index snapshot: %s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "core.md") {
		t.Fatalf("tree used wiki-index despite DB availability: %s", rec.Body.String())
	}
}

func TestAPI_SearchFallsBackToDBWhenWikiIndexMissing(t *testing.T) {
	srv := newDBFallbackTestServer(t)
	seedWikiRetrieveNode(t, srv.db, ctxns.DefaultNamespace, "billing.Processor", "Processor", "internal/billing/processor.go", model.TagIntent, "payment settlement workflow")

	req := httptest.NewRequest(http.MethodGet, "/wiki/api/search?q=settlement&namespace=default&limit=5", nil)
	rec := httptest.NewRecorder()
	srv.APIHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("search fallback status = %d body=%s", rec.Code, rec.Body.String())
	}
	var got struct {
		Results []ragindex.SearchResult `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode search fallback: %v", err)
	}
	if len(got.Results) == 0 || got.Results[0].ID != "symbol:billing.Processor" {
		t.Fatalf("results = %#v", got.Results)
	}
}

func TestAPI_SearchFallbackIsNamespaceScoped(t *testing.T) {
	srv := newDBFallbackTestServer(t)
	seedWikiRetrieveNode(t, srv.db, "alpha", "alpha.Checkout", "Checkout", "checkout.go", model.TagIntent, "sharedtenant alpha checkout")
	seedWikiRetrieveNode(t, srv.db, "beta", "beta.Checkout", "Checkout", "checkout.go", model.TagIntent, "sharedtenant beta checkout")

	req := httptest.NewRequest(http.MethodGet, "/wiki/api/search?q=sharedtenant&namespace=alpha&limit=10", nil)
	rec := httptest.NewRecorder()
	srv.APIHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("search fallback namespace status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "alpha.Checkout") || strings.Contains(rec.Body.String(), "beta.Checkout") {
		t.Fatalf("namespace-scoped search leaked or missed result: %s", rec.Body.String())
	}
}

func TestAPI_DocFallsBackToDBTreeSummaryWhenWikiIndexMissing(t *testing.T) {
	srv := newDBFallbackTestServer(t)
	seedWikiRetrieveNode(t, srv.db, ctxns.DefaultNamespace, "billing.Processor", "Processor", "internal/billing/processor.go", model.TagIntent, "payment settlement workflow")

	req := httptest.NewRequest(http.MethodGet, "/wiki/api/doc?namespace=default&path=docs/internal/billing/processor.go.md", nil)
	rec := httptest.NewRecorder()
	srv.APIHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("doc fallback status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "payment settlement workflow") ||
		!strings.Contains(rec.Body.String(), `"generated":false`) ||
		!strings.Contains(rec.Body.String(), "generated-by: code-context-graph docs") ||
		!strings.Contains(rec.Body.String(), "## Functions") {
		t.Fatalf("expected DB summary fallback, got %s", rec.Body.String())
	}
}

func TestAPI_ContextFallsBackToDBTreeSummaryWhenWikiIndexMissing(t *testing.T) {
	srv := newDBFallbackTestServer(t)
	seedWikiRetrieveNode(t, srv.db, ctxns.DefaultNamespace, "billing.Processor", "Processor", "internal/billing/processor.go", model.TagIntent, "payment settlement workflow")

	req := httptest.NewRequest(http.MethodPost, "/wiki/api/context", strings.NewReader(`{"namespace":"default","paths":["docs/internal/billing/processor.go.md"]}`))
	rec := httptest.NewRecorder()
	srv.APIHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("context fallback status = %d body=%s", rec.Code, rec.Body.String())
	}
	var got contextResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode context fallback: %v", err)
	}
	if len(got.Items) != 1 || !got.Items[0].Found || !strings.Contains(got.Markdown, "payment settlement workflow") {
		t.Fatalf("context fallback = %#v", got)
	}
}

func TestAPI_RefFallsBackToDBTreeWhenWikiIndexMissing(t *testing.T) {
	srv := newDBFallbackTestServer(t)
	seedWikiRetrieveNode(t, srv.db, "billing", "billing.Processor", "Processor", "internal/billing/processor.go", model.TagIntent, "payment settlement workflow")
	rawRef := "ccg://billing/internal/billing/processor.go#Processor"

	req := httptest.NewRequest(http.MethodGet, "/wiki/api/ref?ref="+url.QueryEscape(rawRef), nil)
	rec := httptest.NewRecorder()
	srv.APIHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("ref fallback status = %d body=%s", rec.Code, rec.Body.String())
	}
	var got refResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode ref fallback: %v", err)
	}
	if got.Target.Label != "Processor" || got.Target.DocPath == "" || got.Target.GraphNodeID == "" {
		t.Fatalf("target = %#v", got.Target)
	}
}

func TestAPI_TreeFallbackIsNamespaceScoped(t *testing.T) {
	srv := newDBFallbackTestServer(t)
	seedWikiRetrieveNode(t, srv.db, "alpha", "alpha.Checkout", "Checkout", "checkout.go", model.TagIntent, "sharedtenant alpha checkout")
	seedWikiRetrieveNode(t, srv.db, "beta", "beta.Checkout", "Checkout", "checkout.go", model.TagIntent, "sharedtenant beta checkout")

	req := httptest.NewRequest(http.MethodGet, "/wiki/api/tree?namespace=alpha", nil)
	rec := httptest.NewRecorder()
	srv.APIHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("tree fallback namespace status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "alpha.Checkout") || strings.Contains(rec.Body.String(), "beta.Checkout") {
		t.Fatalf("namespace-scoped tree leaked or missed result: %s", rec.Body.String())
	}
}

func TestAPI_CorruptWikiIndexDoesNotBlockDBTree(t *testing.T) {
	srv := newDBFallbackTestServer(t)
	seedWikiRetrieveNode(t, srv.db, ctxns.DefaultNamespace, "billing.Processor", "Processor", "internal/billing/processor.go", model.TagIntent, "payment settlement workflow")
	if err := os.WriteFile(filepath.Join(srv.ragIndexDir, "wiki-index.json"), []byte("{"), 0o644); err != nil {
		t.Fatalf("write corrupt index: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/wiki/api/tree?namespace=default", nil)
	rec := httptest.NewRecorder()
	srv.APIHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "billing.Processor") {
		t.Fatalf("corrupt wiki-index should not block DB tree: %s", rec.Body.String())
	}
}

func TestAPI_ContextReturnsUnavailableWithoutDB(t *testing.T) {
	srv := newTestServer(t)
	srv.db = nil
	req := httptest.NewRequest(http.MethodPost, "/wiki/api/context", strings.NewReader(`{"namespace":"default","paths":["docs/core.md","docs/missing.md"]}`))
	rec := httptest.NewRecorder()

	srv.APIHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("context status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "graph database is not configured") {
		t.Fatalf("context body = %s", rec.Body.String())
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

func TestAPI_RefResolvesWikiAndGraphTarget(t *testing.T) {
	srv := newTestServer(t)
	rawRef := "ccg://auth-svc/internal/auth/token.go#ValidateToken"
	req := httptest.NewRequest(http.MethodGet, "/wiki/api/ref?ref="+url.QueryEscape(rawRef), nil)
	rec := httptest.NewRecorder()

	srv.APIHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("ref status = %d body=%s", rec.Code, rec.Body.String())
	}
	var got refResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode ref: %v", err)
	}
	if got.Namespace != "auth-svc" {
		t.Fatalf("namespace = %q", got.Namespace)
	}
	if got.Target.Label != "ValidateToken" || got.Target.Kind != "function" {
		t.Fatalf("target = %#v", got.Target)
	}
	if got.Target.GraphNodeID == "" {
		t.Fatalf("expected graph node id in target: %#v", got.Target)
	}
	if got.Target.Details == nil || got.Target.Details.FilePath != "internal/auth/token.go" {
		t.Fatalf("details = %#v", got.Target.Details)
	}
}

func TestAPI_RefReturnsNotFoundForMissingTarget(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/wiki/api/ref?ref="+url.QueryEscape("ccg://auth-svc/internal/auth/token.go#Missing"), nil)
	rec := httptest.NewRecorder()

	srv.APIHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
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

func TestAPI_DocReturnsNotFoundWithoutDBFallback(t *testing.T) {
	srv := newTestServer(t)
	srv.db = nil
	req := httptest.NewRequest(http.MethodGet, "/wiki/api/doc?namespace=default&path=docs/missing.md", nil)
	rec := httptest.NewRecorder()

	srv.APIHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "file does not exist") {
		t.Fatalf("expected file missing, got %s", rec.Body.String())
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
	return slices.Contains(items, want)
}

func findTreeNode(root *ragindex.TreeNode, id string) *ragindex.TreeNode {
	if root == nil {
		return nil
	}
	if root.ID == id {
		return root
	}
	for _, child := range root.Children {
		if found := findTreeNode(child, id); found != nil {
			return found
		}
	}
	return nil
}

func newDBFallbackTestServer(t *testing.T) *Server {
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
	if err := os.MkdirAll(filepath.Join(root, "namespaces"), 0o755); err != nil {
		t.Fatalf("create namespaces dir: %v", err)
	}
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.Node{}, &model.Edge{}, &model.Annotation{}, &model.DocTag{}); err != nil {
		t.Fatalf("migrate db: %v", err)
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
	srv, err := New(Config{StaticDir: staticDir, RagIndexDir: ragDir, NamespaceRoot: filepath.Join(root, "namespaces"), DB: db})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	return srv
}

func seedWikiRetrieveNode(t *testing.T, db *gorm.DB, namespace, qualifiedName, name, filePath string, tagKind model.TagKind, tagValue string) {
	t.Helper()
	node := model.Node{Namespace: namespace, QualifiedName: qualifiedName, Kind: model.NodeKindFunction, Name: name, FilePath: filePath, StartLine: 1, EndLine: 10, Language: "go"}
	if err := db.Create(&node).Error; err != nil {
		t.Fatalf("create retrieve node: %v", err)
	}
	ann := model.Annotation{NodeID: node.ID, Summary: tagValue}
	if err := db.Create(&ann).Error; err != nil {
		t.Fatalf("create retrieve annotation: %v", err)
	}
	if err := db.Create(&model.DocTag{AnnotationID: ann.ID, Kind: tagKind, Value: tagValue, Ordinal: 0}).Error; err != nil {
		t.Fatalf("create retrieve doc tag: %v", err)
	}
}

func writeWikiRetrieveDoc(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create retrieve doc dir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write retrieve doc: %v", err)
	}
}
