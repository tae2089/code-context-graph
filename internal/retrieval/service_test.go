package retrieval_test

import (
	"context"
	"errors"
	"os"
	"slices"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/tae2089/code-context-graph/internal/model"
	"github.com/tae2089/code-context-graph/internal/retrieval"
)

type stubSearchBackend struct {
	nodes []model.Node
	err   error
	calls int
	limit int
}

func (s *stubSearchBackend) Migrate(*gorm.DB) error                               { return nil }
func (s *stubSearchBackend) Rebuild(context.Context, *gorm.DB) error              { return nil }
func (s *stubSearchBackend) RebuildNodes(context.Context, *gorm.DB, []uint) error { return nil }
func (s *stubSearchBackend) PurgeNamespace(context.Context, *gorm.DB) error       { return nil }
func (s *stubSearchBackend) Query(_ context.Context, _ *gorm.DB, _ string, limit int) ([]model.Node, error) {
	s.calls++
	s.limit = limit
	return s.nodes, s.err
}

func TestServiceFromDB_searchBackendSuccessUsesBackendAndContent(t *testing.T) {
	db := newRetrievalDB(t)
	node := createNode(t, db, model.Node{Namespace: "default", QualifiedName: "pkg.Auth", Kind: model.NodeKindFunction, Name: "Auth", FilePath: "pkg/auth.go", StartLine: 1, EndLine: 2, Language: "go"})
	createAnnotation(t, db, node.ID, "auth summary", model.DocTag{Kind: model.TagIntent, Value: "auth intent"})
	backend := &stubSearchBackend{nodes: []model.Node{node}}
	service := retrieval.Service{DB: db, SearchBackend: backend}

	response, err := service.FromDB(context.Background(), "default", "auth", 5, 4, func(_ context.Context, namespace, docPath string, limit int) (string, bool, error) {
		if namespace != "" {
			t.Fatalf("default namespace should read shared docs namespace, got %q", namespace)
		}
		if docPath != "docs/pkg/auth.go.md" {
			t.Fatalf("unexpected doc path %q", docPath)
		}
		if limit != 4 {
			t.Fatalf("unexpected content limit %d", limit)
		}
		return "auth", true, nil
	})
	if err != nil {
		t.Fatalf("FromDB returned error: %v", err)
	}
	if backend.calls != 1 {
		t.Fatalf("expected backend called once, got %d", backend.calls)
	}
	if backend.limit != retrieval.DBCandidateLimit(5) {
		t.Fatalf("unexpected backend candidate limit %d", backend.limit)
	}
	if len(response.Results) != 1 {
		t.Fatalf("expected one result, got %d", len(response.Results))
	}
	result := response.Results[0]
	if result.ID != "file:pkg/auth.go" || result.Content != "auth" || !result.ContentTruncated {
		t.Fatalf("unexpected result: %+v", result)
	}
	if result.Summary != "auth summary" {
		t.Fatalf("expected annotation summary, got %q", result.Summary)
	}
	if !containsString(result.MatchedFields, "annotation_text") || !containsString(result.MatchedFields, "intent") {
		t.Fatalf("expected annotation fields, got %v", result.MatchedFields)
	}
}

func TestServiceFromDB_nilSearchFallsBackToDBScanWithNamespaceIsolation(t *testing.T) {
	db := newRetrievalDB(t)
	keep := createNode(t, db, model.Node{Namespace: "team-a", QualifiedName: "pkg.Payment", Kind: model.NodeKindFunction, Name: "Payment", FilePath: "a/payment.go", StartLine: 1, EndLine: 2, Language: "go"})
	createAnnotation(t, db, keep.ID, "handles billing", model.DocTag{Kind: model.TagDomainRule, Value: "payment belongs to team a"})
	other := createNode(t, db, model.Node{Namespace: "team-b", QualifiedName: "pkg.Payment", Kind: model.NodeKindFunction, Name: "Payment", FilePath: "b/payment.go", StartLine: 1, EndLine: 2, Language: "go"})
	createAnnotation(t, db, other.ID, "handles billing", model.DocTag{Kind: model.TagDomainRule, Value: "payment belongs to team b"})
	service := retrieval.Service{DB: db}

	response, err := service.FromDB(context.Background(), "team-a", "payment", 5, 0, nil)
	if err != nil {
		t.Fatalf("FromDB returned error: %v", err)
	}
	if len(response.Results) != 1 {
		t.Fatalf("expected one namespace-scoped result, got %d", len(response.Results))
	}
	if response.Results[0].ID != "file:a/payment.go" {
		t.Fatalf("unexpected result ID %q", response.Results[0].ID)
	}
	if response.Results[0].Content != "" {
		t.Fatalf("content should be omitted when contentLimit is zero")
	}
}

func TestServiceFromDB_failedSearchFallsBackToAnnotationScan(t *testing.T) {
	db := newRetrievalDB(t)
	node := createNode(t, db, model.Node{Namespace: "default", QualifiedName: "pkg.Cache", Kind: model.NodeKindFunction, Name: "Cache", FilePath: "pkg/cache.go", StartLine: 1, EndLine: 2, Language: "go"})
	createAnnotation(t, db, node.ID, "stores session tokens", model.DocTag{Kind: model.TagIntent, Value: "token cache"})
	backend := &stubSearchBackend{err: errors.New("fts unavailable")}
	service := retrieval.Service{DB: db, SearchBackend: backend}

	response, err := service.FromDB(context.Background(), "default", "session", 5, 0, nil)
	if err != nil {
		t.Fatalf("FromDB returned error: %v", err)
	}
	if backend.calls != 1 {
		t.Fatalf("expected backend called once, got %d", backend.calls)
	}
	if len(response.Results) != 1 || response.Results[0].ID != "file:pkg/cache.go" {
		t.Fatalf("unexpected fallback response: %+v", response.Results)
	}
}

func TestServiceFromDB_scanFallbackSkipsPackageNodes(t *testing.T) {
	db := newRetrievalDB(t)
	pkg := createNode(t, db, model.Node{Namespace: "default", QualifiedName: "internal/mcp", Kind: model.NodeKindPackage, Name: "mcp", FilePath: "internal/mcp", StartLine: 1, EndLine: 1, Language: "go"})
	createAnnotation(t, db, pkg.ID, "mcp package")
	fn := createNode(t, db, model.Node{Namespace: "default", QualifiedName: "mcp.Handler", Kind: model.NodeKindFunction, Name: "Handler", FilePath: "internal/mcp/handler.go", StartLine: 1, EndLine: 2, Language: "go"})
	createAnnotation(t, db, fn.ID, "mcp handler")
	service := retrieval.Service{DB: db}

	response, err := service.FromDB(context.Background(), "default", "mcp", 5, 0, nil)
	if err != nil {
		t.Fatalf("FromDB returned error: %v", err)
	}
	if len(response.Results) != 1 {
		t.Fatalf("expected one non-package result, got %d: %+v", len(response.Results), response.Results)
	}
	if response.Results[0].ID != "file:internal/mcp/handler.go" {
		t.Fatalf("expected function file result, got %q", response.Results[0].ID)
	}
	if response.Results[0].DocPath == "docs/internal/mcp.md" {
		t.Fatalf("package path should not be converted into fake doc path")
	}
}

func TestServiceFromDB_matchesUseBatchLoadedAnnotations(t *testing.T) {
	db := newRetrievalDB(t)
	node := createNode(t, db, model.Node{Namespace: "default", QualifiedName: "pkg.Auth", Kind: model.NodeKindFunction, Name: "Auth", FilePath: "pkg/auth.go", StartLine: 1, EndLine: 2, Language: "go"})
	createAnnotation(t, db, node.ID, "batch auth summary")
	backendNode := model.Node{ID: node.ID, Namespace: node.Namespace, QualifiedName: node.QualifiedName, Kind: node.Kind, Name: node.Name, FilePath: node.FilePath, StartLine: node.StartLine, EndLine: node.EndLine, Language: node.Language}
	service := retrieval.Service{DB: db, SearchBackend: &stubSearchBackend{nodes: []model.Node{backendNode}}}

	response, err := service.FromDB(context.Background(), "default", "auth", 5, 0, nil)
	if err != nil {
		t.Fatalf("FromDB returned error: %v", err)
	}
	if len(response.Results) != 1 {
		t.Fatalf("expected one result, got %d", len(response.Results))
	}
	if len(response.Results[0].Matches) != 1 {
		t.Fatalf("expected one match, got %d", len(response.Results[0].Matches))
	}
	if response.Results[0].Matches[0].Summary != "batch auth summary" {
		t.Fatalf("expected batch annotation match summary, got %q", response.Results[0].Matches[0].Summary)
	}
}

func TestServiceFromDB_emptySearchGroupsFallsBackToDBScan(t *testing.T) {
	db := newRetrievalDB(t)
	node := createNode(t, db, model.Node{Namespace: "default", QualifiedName: "pkg.Queue", Kind: model.NodeKindFunction, Name: "Queue", FilePath: "pkg/queue.go", StartLine: 1, EndLine: 2, Language: "go"})
	createAnnotation(t, db, node.ID, "dispatches jobs")
	backend := &stubSearchBackend{}
	service := retrieval.Service{DB: db, SearchBackend: backend}

	response, err := service.FromDB(context.Background(), "default", "jobs", 5, 0, nil)
	if err != nil {
		t.Fatalf("FromDB returned error: %v", err)
	}
	if len(response.Results) != 1 || response.Results[0].ID != "file:pkg/queue.go" {
		t.Fatalf("unexpected fallback response: %+v", response.Results)
	}
}

func TestServiceFromDB_supplementsPartialSearchResultsWithDBScan(t *testing.T) {
	db := newRetrievalDB(t)
	ftsNode := createNode(t, db, model.Node{Namespace: "default", QualifiedName: "pkg.Auth", Kind: model.NodeKindFunction, Name: "Auth", FilePath: "a.go", StartLine: 1, EndLine: 2, Language: "go"})
	createAnnotation(t, db, ftsNode.ID, "auth backend")
	scanNode := createNode(t, db, model.Node{Namespace: "default", QualifiedName: "pkg.Upload", Kind: model.NodeKindFunction, Name: "Upload", FilePath: "b.go", StartLine: 1, EndLine: 2, Language: "go"})
	createAnnotation(t, db, scanNode.ID, "supplemental docs", model.DocTag{Kind: model.TagIntent, Value: "auth upload workflow"})
	service := retrieval.Service{DB: db, SearchBackend: &stubSearchBackend{nodes: []model.Node{ftsNode}}}

	response, err := service.FromDB(context.Background(), "default", "auth upload", 2, 0, nil)
	if err != nil {
		t.Fatalf("FromDB returned error: %v", err)
	}
	if len(response.Results) != 2 {
		t.Fatalf("expected DB scan to fill second file result, got %d: %+v", len(response.Results), response.Results)
	}
	gotIDs := []string{response.Results[0].ID, response.Results[1].ID}
	if !slices.Contains(gotIDs, "file:a.go") || !slices.Contains(gotIDs, "file:b.go") {
		t.Fatalf("expected backend and supplemental scan files, got %v", gotIDs)
	}
}

func TestServiceFromDB_limitAppliesToFileGroups(t *testing.T) {
	db := newRetrievalDB(t)
	nodeA := createNode(t, db, model.Node{Namespace: "default", QualifiedName: "pkg.A", Kind: model.NodeKindFunction, Name: "NeedleA", FilePath: "a.go", StartLine: 1, EndLine: 2, Language: "go"})
	nodeB := createNode(t, db, model.Node{Namespace: "default", QualifiedName: "pkg.B", Kind: model.NodeKindFunction, Name: "NeedleB", FilePath: "b.go", StartLine: 1, EndLine: 2, Language: "go"})
	nodeC := createNode(t, db, model.Node{Namespace: "default", QualifiedName: "pkg.C", Kind: model.NodeKindFunction, Name: "NeedleC", FilePath: "c.go", StartLine: 1, EndLine: 2, Language: "go"})
	service := retrieval.Service{DB: db, SearchBackend: &stubSearchBackend{nodes: []model.Node{nodeA, nodeB, nodeC}}}

	response, err := service.FromDB(context.Background(), "default", "needle", 2, 0, nil)
	if err != nil {
		t.Fatalf("FromDB returned error: %v", err)
	}
	if len(response.Results) != 2 {
		t.Fatalf("expected two limited results, got %d", len(response.Results))
	}
	if response.Results[0].ID != "file:a.go" || response.Results[1].ID != "file:b.go" {
		t.Fatalf("unexpected result order: %+v", response.Results)
	}
}

func TestServiceFromDB_scoresBeforeApplyingResultLimit(t *testing.T) {
	db := newRetrievalDB(t)
	weak := createNode(t, db, model.Node{Namespace: "default", QualifiedName: "pkg.Weak", Kind: model.NodeKindFunction, Name: "Weak", FilePath: "a.go", StartLine: 1, EndLine: 2, Language: "go"})
	createAnnotation(t, db, weak.ID, "needle")
	strong := createNode(t, db, model.Node{Namespace: "default", QualifiedName: "pkg.Needle", Kind: model.NodeKindFunction, Name: "Needle", FilePath: "z.go", StartLine: 1, EndLine: 2, Language: "go"})
	createAnnotation(t, db, strong.ID, "needle", model.DocTag{Kind: model.TagIntent, Value: "needle"})
	service := retrieval.Service{DB: db}

	response, err := service.FromDB(context.Background(), "default", "needle", 1, 0, nil)
	if err != nil {
		t.Fatalf("FromDB returned error: %v", err)
	}
	if len(response.Results) != 1 {
		t.Fatalf("expected one limited result, got %d", len(response.Results))
	}
	if response.Results[0].ID != "file:z.go" {
		t.Fatalf("expected stronger late-path candidate to win before limit, got %+v", response.Results[0])
	}
}

func TestServiceFromDB_scoreOutranksBroadLowSignalTermCoverage(t *testing.T) {
	db := newRetrievalDB(t)
	broad := createNode(t, db, model.Node{Namespace: "default", QualifiedName: "pkg.Broad", Kind: model.NodeKindFunction, Name: "Broad", FilePath: "a.go", StartLine: 1, EndLine: 2, Language: "go"})
	createAnnotation(t, db, broad.ID, "alpha beta gamma delta")
	strong := createNode(t, db, model.Node{Namespace: "default", QualifiedName: "pkg.Needle", Kind: model.NodeKindFunction, Name: "Needle", FilePath: "needle.go", StartLine: 1, EndLine: 2, Language: "go"})
	createAnnotation(t, db, strong.ID, "needle",
		model.DocTag{Kind: model.TagIntent, Value: "needle"},
		model.DocTag{Kind: model.TagDomainRule, Value: "needle"},
		model.DocTag{Kind: model.TagRequires, Value: "needle"},
		model.DocTag{Kind: model.TagEnsures, Value: "needle"},
		model.DocTag{Kind: model.TagSideEffect, Value: "needle"},
		model.DocTag{Kind: model.TagMutates, Value: "needle"},
		model.DocTag{Kind: model.TagSee, Value: "needle"},
	)
	service := retrieval.Service{DB: db}

	response, err := service.FromDB(context.Background(), "default", "needle alpha beta gamma delta", 1, 0, nil)
	if err != nil {
		t.Fatalf("FromDB returned error: %v", err)
	}
	if len(response.Results) != 1 {
		t.Fatalf("expected one limited result, got %d", len(response.Results))
	}
	if response.Results[0].ID != "file:needle.go" {
		t.Fatalf("expected strongest structured result to win before broad term coverage, got %+v", response.Results[0])
	}
}

func TestServiceFromDB_missingContentKeepsResultEmpty(t *testing.T) {
	db := newRetrievalDB(t)
	node := createNode(t, db, model.Node{Namespace: "team-a", QualifiedName: "pkg.Doc", Kind: model.NodeKindFunction, Name: "Doc", FilePath: "doc.go", StartLine: 1, EndLine: 2, Language: "go"})
	service := retrieval.Service{DB: db, SearchBackend: &stubSearchBackend{nodes: []model.Node{node}}}

	response, err := service.FromDB(context.Background(), "team-a", "doc", 5, 10, func(_ context.Context, namespace, _ string, _ int) (string, bool, error) {
		if namespace != "team-a" {
			t.Fatalf("expected explicit namespace for content reader, got %q", namespace)
		}
		return "", false, os.ErrNotExist
	})
	if err != nil {
		t.Fatalf("FromDB returned error: %v", err)
	}
	if len(response.Results) != 1 {
		t.Fatalf("expected one result, got %d", len(response.Results))
	}
	if response.Results[0].Content != "" || response.Results[0].ContentTruncated {
		t.Fatalf("missing content should keep empty content fields: %+v", response.Results[0])
	}
}

func TestServiceFromDB_contentReaderErrorPropagates(t *testing.T) {
	db := newRetrievalDB(t)
	node := createNode(t, db, model.Node{Namespace: "default", QualifiedName: "pkg.Doc", Kind: model.NodeKindFunction, Name: "Doc", FilePath: "doc.go", StartLine: 1, EndLine: 2, Language: "go"})
	service := retrieval.Service{DB: db, SearchBackend: &stubSearchBackend{nodes: []model.Node{node}}}
	wantErr := errors.New("boom")

	_, err := service.FromDB(context.Background(), "default", "doc", 5, 10, func(context.Context, string, string, int) (string, bool, error) {
		return "", false, wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected reader error, got %v", err)
	}
}

func newRetrievalDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&model.Node{}, &model.Annotation{}, &model.DocTag{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func createNode(t *testing.T, db *gorm.DB, node model.Node) model.Node {
	t.Helper()
	if err := db.Create(&node).Error; err != nil {
		t.Fatalf("create node: %v", err)
	}
	return node
}

func createAnnotation(t *testing.T, db *gorm.DB, nodeID uint, summary string, tags ...model.DocTag) model.Annotation {
	t.Helper()
	ann := model.Annotation{NodeID: nodeID, Summary: summary, Tags: tags}
	if err := db.Create(&ann).Error; err != nil {
		t.Fatalf("create annotation: %v", err)
	}
	return ann
}

func containsString(values []string, target string) bool {
	return slices.Contains(values, target)
}
