// toBinderComments가 Walker의 CommentBlock 메타 필드를
// Binder의 CommentBlock로 누락 없이 옮기는지 검증하는 재발 방지 테스트.
//
// 배경: P0-2에서 추가된 IsDocstring/OwnerStartLine 필드가 초기 indexer 변환
// 루프에서 누락되어 Python docstring 바인딩이 프로덕션 경로에서 동작하지
// 않던 문제가 있었다 (code review에서 발견, 97dfb3b 에서 수정).
//
// 이 테스트는 Walker↔Binder 타입이 분기 진화할 경우 동일한 실수가
// 재발하지 않도록 변환 함수 단위로 필드 전파를 고정한다.
package service

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/tae2089/code-context-graph/internal/model"
	"github.com/tae2089/code-context-graph/internal/ctxns"
	"github.com/tae2089/code-context-graph/internal/parse/treesitter"
	"github.com/tae2089/code-context-graph/internal/store/gormstore"
	storesearch "github.com/tae2089/code-context-graph/internal/store/search"
)

func TestToBinderComments_PreservesBasicFields(t *testing.T) {
	in := []treesitter.CommentBlock{
		{StartLine: 3, EndLine: 4, Text: "// hello"},
	}
	got := toBinderComments(in)
	if len(got) != 1 {
		t.Fatalf("len mismatch: got=%d want=1", len(got))
	}
	if got[0].StartLine != 3 || got[0].EndLine != 4 || got[0].Text != "// hello" {
		t.Errorf("basic fields lost: %+v", got[0])
	}
}

func TestToBinderComments_PreservesDocstringFields(t *testing.T) {
	in := []treesitter.CommentBlock{
		{
			StartLine:      5,
			EndLine:        7,
			Text:           `"""module docstring"""`,
			IsDocstring:    true,
			OwnerStartLine: 0,
		},
		{
			StartLine:      10,
			EndLine:        12,
			Text:           `"""func docstring"""`,
			IsDocstring:    true,
			OwnerStartLine: 9,
		},
	}
	got := toBinderComments(in)
	if len(got) != 2 {
		t.Fatalf("len mismatch: got=%d want=2", len(got))
	}

	if !got[0].IsDocstring || got[0].OwnerStartLine != 0 {
		t.Errorf("module docstring fields lost: IsDocstring=%v OwnerStartLine=%d",
			got[0].IsDocstring, got[0].OwnerStartLine)
	}
	if !got[1].IsDocstring || got[1].OwnerStartLine != 9 {
		t.Errorf("func docstring fields lost: IsDocstring=%v OwnerStartLine=%d",
			got[1].IsDocstring, got[1].OwnerStartLine)
	}
}

func TestToBinderComments_NonDocstringKeepsDefaults(t *testing.T) {
	in := []treesitter.CommentBlock{
		{StartLine: 1, EndLine: 1, Text: "# plain", IsDocstring: false, OwnerStartLine: 0},
	}
	got := toBinderComments(in)
	if got[0].IsDocstring || got[0].OwnerStartLine != 0 {
		t.Errorf("non-docstring contaminated: %+v", got[0])
	}
}

func TestToBinderComments_EmptyInput(t *testing.T) {
	got := toBinderComments(nil)
	if got == nil {
		t.Error("nil input should return empty (non-nil) slice for consistency")
	}
	if len(got) != 0 {
		t.Errorf("expected empty slice, got len=%d", len(got))
	}
}

// TestBuild_SameQN_DifferentNodes_AnnotationBindsCorrectly verifies that when
// two nodes share the same QualifiedName (e.g. Alpha.save and Beta.save both
// have QN="save"), annotations are bound to the correct node respectively.
//
// This is a regression test for the indexer bug where GetNodesByQualifiedNames
// returns map[string]*Node — same QN key means only one node survives in the
// map, causing annotation binding to the wrong node.
func TestBuild_SameQN_DifferentNodes_AnnotationBindsCorrectly(t *testing.T) {
	// Setup: in-memory SQLite + gormstore + Python walker
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: gormlogger.Discard,
	})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	st := gormstore.New(db)
	if err := st.AutoMigrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	svc := &GraphService{
		Store:   st,
		DB:      db,
		Walkers: map[string]*treesitter.Walker{".py": treesitter.NewWalker(treesitter.PythonSpec)},
		Logger:  slog.Default(),
	}

	// Create temp dir with dup_methods.py
	tmpDir := t.TempDir()
	pyDir := filepath.Join(tmpDir, "python")
	if err := os.MkdirAll(pyDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	dupContent := `class Alpha:
    @classmethod
    def save(cls) -> int:
        """@intent Alpha save"""
        return 1


class Beta:
    @classmethod
    def save(cls) -> int:
        """@intent Beta save"""
        return 2
`
	if err := os.WriteFile(filepath.Join(pyDir, "dup_methods.py"), []byte(dupContent), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	// Build
	ctx := context.Background()
	_, err = svc.Build(ctx, BuildOptions{Dir: tmpDir})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	// Query: find both "save" nodes
	var nodes []struct {
		ID        uint
		StartLine int
	}
	if err := db.Raw(`SELECT id, start_line FROM nodes WHERE qualified_name = 'save' AND kind != 'file' ORDER BY start_line`).Scan(&nodes).Error; err != nil {
		t.Fatalf("query nodes: %v", err)
	}
	if len(nodes) != 2 {
		t.Fatalf("expected 2 'save' nodes, got %d", len(nodes))
	}

	// Verify annotations are bound to the CORRECT node
	// Node at lower start_line = Alpha.save → should have "@intent Alpha save"
	// Node at higher start_line = Beta.save → should have "@intent Beta save"
	alphaAnn, err := st.GetAnnotation(ctx, nodes[0].ID)
	if err != nil {
		t.Fatalf("GetAnnotation(Alpha.save): %v", err)
	}
	if alphaAnn == nil {
		t.Fatal("Alpha.save (first 'save' node) has no annotation — binding failed")
	}

	betaAnn, err := st.GetAnnotation(ctx, nodes[1].ID)
	if err != nil {
		t.Fatalf("GetAnnotation(Beta.save): %v", err)
	}
	if betaAnn == nil {
		t.Fatal("Beta.save (second 'save' node) has no annotation — binding failed")
	}

	// Check that @intent tags have the correct values
	var alphaIntent, betaIntent string
	for _, tag := range alphaAnn.Tags {
		if tag.Kind == "intent" {
			alphaIntent = tag.Value
		}
	}
	for _, tag := range betaAnn.Tags {
		if tag.Kind == "intent" {
			betaIntent = tag.Value
		}
	}

	if alphaIntent != "Alpha save" {
		t.Errorf("Alpha.save @intent: got %q, want %q", alphaIntent, "Alpha save")
	}
	if betaIntent != "Beta save" {
		t.Errorf("Beta.save @intent: got %q, want %q", betaIntent, "Beta save")
	}
}

func TestBuild_IncrementalRebuild_RemovesStaleNodesBeforeUpsert(t *testing.T) {
	// Setup: in-memory SQLite + gormstore + Go walker
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: gormlogger.Discard,
	})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	st := gormstore.New(db)
	if err := st.AutoMigrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	svc := &GraphService{
		Store:   st,
		DB:      db,
		Walkers: map[string]*treesitter.Walker{".go": treesitter.NewWalker(treesitter.GoSpec)},
		Logger:  slog.Default(),
	}

	tmpDir := t.TempDir()
	goPath := filepath.Join(tmpDir, "sample.go")

	initial := `package sample

func Keep() int {
	return 1
}

func Remove() int {
	return 2
}
`
	if err := os.WriteFile(goPath, []byte(initial), 0o644); err != nil {
		t.Fatalf("write initial file: %v", err)
	}

	ctx := context.Background()
	if _, err := svc.Build(ctx, BuildOptions{Dir: tmpDir}); err != nil {
		t.Fatalf("first Build: %v", err)
	}

	assertFunctionNamesByFile(t, st, ctx, "sample.go", []string{"Keep", "Remove"})

	reduced := `package sample

func Keep() int {
	return 1
}
`
	if err := os.WriteFile(goPath, []byte(reduced), 0o644); err != nil {
		t.Fatalf("write reduced file: %v", err)
	}

	if _, err := svc.Build(ctx, BuildOptions{Dir: tmpDir}); err != nil {
		t.Fatalf("second Build: %v", err)
	}

	assertFunctionNamesByFile(t, st, ctx, "sample.go", []string{"Keep"})
}

func TestBuild_IncludePaths_ReplacesPreviousGraphScope(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	st := gormstore.New(db)
	if err := st.AutoMigrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	svc := &GraphService{
		Store:   st,
		DB:      db,
		Walkers: map[string]*treesitter.Walker{".go": treesitter.NewWalker(treesitter.GoSpec)},
		Logger:  slog.Default(),
	}

	tmpDir := t.TempDir()
	apiDir := filepath.Join(tmpDir, "src", "api")
	otherDir := filepath.Join(tmpDir, "src", "other")
	if err := os.MkdirAll(apiDir, 0o755); err != nil {
		t.Fatalf("mkdir api: %v", err)
	}
	if err := os.MkdirAll(otherDir, 0o755); err != nil {
		t.Fatalf("mkdir other: %v", err)
	}
	if err := os.WriteFile(filepath.Join(apiDir, "handler.go"), []byte("package api\n\nfunc Handler() {\n\tHelper()\n}\n"), 0o644); err != nil {
		t.Fatalf("write handler: %v", err)
	}
	if err := os.WriteFile(filepath.Join(otherDir, "helper.go"), []byte("package other\n\nfunc Helper() {}\n"), 0o644); err != nil {
		t.Fatalf("write helper: %v", err)
	}

	ctx := context.Background()
	if _, err := svc.Build(ctx, BuildOptions{Dir: tmpDir}); err != nil {
		t.Fatalf("first Build: %v", err)
	}

	handlerNode, err := st.GetNode(ctx, "api.Handler")
	if err != nil || handlerNode == nil {
		t.Fatalf("expected api.Handler after full build, err=%v", err)
	}
	helperNode, err := st.GetNode(ctx, "other.Helper")
	if err != nil || helperNode == nil {
		t.Fatalf("expected other.Helper after full build, err=%v", err)
	}
	if err := st.UpsertEdges(ctx, []model.Edge{{
		FromNodeID:  handlerNode.ID,
		ToNodeID:    helperNode.ID,
		Kind:        model.EdgeKindCalls,
		FilePath:    filepath.Join("src", "api", "handler.go"),
		Line:        3,
		Fingerprint: "calls:api.Handler:other.Helper",
	}}); err != nil {
		t.Fatalf("seed manual edge: %v", err)
	}

	if _, err := svc.Build(ctx, BuildOptions{Dir: tmpDir, IncludePaths: []string{filepath.Join("src", "api")}}); err != nil {
		t.Fatalf("second Build with include paths: %v", err)
	}

	helperNode, err = st.GetNode(ctx, "other.Helper")
	if err != nil {
		t.Fatalf("get other.Helper after scoped build: %v", err)
	}
	if helperNode != nil {
		t.Fatal("expected other.Helper to be removed after scoped rebuild")
	}

	var manualEdges int64
	if err := db.Model(&model.Edge{}).Where("fingerprint = ?", "calls:api.Handler:other.Helper").Count(&manualEdges).Error; err != nil {
		t.Fatalf("count manual edges: %v", err)
	}
	if manualEdges != 0 {
		t.Fatalf("expected manual cross-file edge to be removed with excluded file scope, got %d", manualEdges)
	}
}

func TestBuild_ReadFailure_PreservesPreviousGraphState(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("broken symlink unreadable path scenario is unix-specific")
	}

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	st := gormstore.New(db)
	if err := st.AutoMigrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	svc := &GraphService{
		Store:   st,
		DB:      db,
		Walkers: map[string]*treesitter.Walker{".go": treesitter.NewWalker(treesitter.GoSpec)},
		Logger:  slog.Default(),
	}

	tmpDir := t.TempDir()
	goPath := filepath.Join(tmpDir, "sample.go")
	if err := os.WriteFile(goPath, []byte("package sample\n\nfunc Keep() {}\n"), 0o644); err != nil {
		t.Fatalf("write initial file: %v", err)
	}

	ctx := context.Background()
	if _, err := svc.Build(ctx, BuildOptions{Dir: tmpDir}); err != nil {
		t.Fatalf("first Build: %v", err)
	}
	assertFunctionNamesByFile(t, st, ctx, "sample.go", []string{"Keep"})

	if err := os.Remove(goPath); err != nil {
		t.Fatalf("remove file: %v", err)
	}
	if err := os.Symlink(filepath.Join(tmpDir, "missing.go"), goPath); err != nil {
		t.Fatalf("create broken symlink: %v", err)
	}

	if _, err := svc.Build(ctx, BuildOptions{Dir: tmpDir}); err == nil {
		t.Fatal("expected second Build to fail on unreadable file")
	}

	assertFunctionNamesByFile(t, st, ctx, "sample.go", []string{"Keep"})
}

func TestBuild_MissingRoot_DoesNotDeleteExistingGraph(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	st := gormstore.New(db)
	if err := st.AutoMigrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	svc := &GraphService{
		Store:   st,
		DB:      db,
		Walkers: map[string]*treesitter.Walker{".go": treesitter.NewWalker(treesitter.GoSpec)},
		Logger:  slog.Default(),
	}

	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, "sample.go"), []byte("package sample\n\nfunc Keep() {}\n"), 0o644); err != nil {
		t.Fatalf("write initial file: %v", err)
	}

	ctx := context.Background()
	if _, err := svc.Build(ctx, BuildOptions{Dir: tmpDir}); err != nil {
		t.Fatalf("first Build: %v", err)
	}
	assertFunctionNamesByFile(t, st, ctx, "sample.go", []string{"Keep"})

	missingDir := filepath.Join(tmpDir, "does-not-exist")
	if _, err := svc.Build(ctx, BuildOptions{Dir: missingDir}); err == nil {
		t.Fatal("expected build on missing root to fail")
	}

	assertFunctionNamesByFile(t, st, ctx, "sample.go", []string{"Keep"})
}

func TestBuild_DoesNotWipeOtherNamespaceSearchDocuments(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:?_pragma=journal_mode(WAL)"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	st := gormstore.New(db)
	if err := st.AutoMigrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := db.AutoMigrate(&model.SearchDocument{}); err != nil {
		t.Fatalf("migrate search docs: %v", err)
	}
	backend := storesearch.NewSQLiteBackend()
	if err := backend.Migrate(db); err != nil {
		if errors.Is(err, storesearch.ErrFTS5NotAvailable) {
			t.Skip("fts5 module not available, skipping test")
		}
		t.Fatalf("migrate fts: %v", err)
	}

	otherNode := model.Node{Namespace: "ns-b", QualifiedName: "pkg.Other", Kind: model.NodeKindFunction, Name: "Other", FilePath: "other.go", StartLine: 1, EndLine: 2, Language: "go"}
	if err := db.Create(&otherNode).Error; err != nil {
		t.Fatalf("seed node: %v", err)
	}
	if err := db.Create(&model.SearchDocument{Namespace: "ns-b", NodeID: otherNode.ID, Content: "other namespace doc", Language: "go"}).Error; err != nil {
		t.Fatalf("seed doc: %v", err)
	}

	svc := &GraphService{
		Store:         st,
		DB:            db,
		SearchBackend: backend,
		Walkers:       map[string]*treesitter.Walker{".go": treesitter.NewWalker(treesitter.GoSpec)},
		Logger:        slog.Default(),
	}

	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, "sample.go"), []byte("package sample\n\nfunc Keep() {}\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	ctx := ctxns.WithNamespace(context.Background(), "ns-a")
	if _, err := svc.Build(ctx, BuildOptions{Dir: tmpDir}); err != nil {
		t.Fatalf("build: %v", err)
	}

	var count int64
	if err := db.Model(&model.SearchDocument{}).Where("namespace = ?", "ns-b").Count(&count).Error; err != nil {
		t.Fatalf("count docs: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected ns-b search docs preserved, got %d", count)
	}
}

type failSearchBackend struct {
	err error
}

func (f *failSearchBackend) Rebuild(ctx context.Context, db *gorm.DB) error { return f.err }
func (f *failSearchBackend) Migrate(db *gorm.DB) error                       { return nil }
func (f *failSearchBackend) Query(ctx context.Context, db *gorm.DB, query string, limit int) ([]model.Node, error) {
	return nil, nil
}

func TestBuild_PropagatesSearchBackendRebuildError(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:?_pragma=journal_mode(WAL)"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	st := gormstore.New(db)
	if err := st.AutoMigrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := db.AutoMigrate(&model.SearchDocument{}); err != nil {
		t.Fatalf("migrate search docs: %v", err)
	}

	svc := &GraphService{
		Store:         st,
		DB:            db,
		SearchBackend: &failSearchBackend{err: errors.New("fts rebuild boom")},
		Walkers:       map[string]*treesitter.Walker{".go": treesitter.NewWalker(treesitter.GoSpec)},
		Logger:        slog.Default(),
	}

	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, "sample.go"), []byte("package sample\n\nfunc Keep() {}\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	_, err = svc.Build(context.Background(), BuildOptions{Dir: tmpDir})
	if err == nil {
		t.Fatal("expected build to fail when search backend rebuild fails")
	}
	if !strings.Contains(err.Error(), "rebuild search index") {
		t.Fatalf("expected rebuild search index error, got %v", err)
	}
}

func TestRefreshSearchDocuments_EmptyNamespace_DoesNotTouchOtherNamespaces(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	st := gormstore.New(db)
	if err := st.AutoMigrate(); err != nil {
		t.Fatalf("migrate store: %v", err)
	}
	if err := db.AutoMigrate(&model.SearchDocument{}); err != nil {
		t.Fatalf("migrate search docs: %v", err)
	}

	defaultNode := model.Node{Namespace: ctxns.DefaultNamespace, QualifiedName: "pkg.Default", Kind: model.NodeKindFunction, Name: "Default", FilePath: "default.go", StartLine: 1, EndLine: 2, Language: "go"}
	otherNode := model.Node{Namespace: "tenant-a", QualifiedName: "pkg.Other", Kind: model.NodeKindFunction, Name: "Other", FilePath: "other.go", StartLine: 1, EndLine: 2, Language: "go"}
	if err := db.Create(&defaultNode).Error; err != nil {
		t.Fatalf("create default node: %v", err)
	}
	if err := db.Create(&otherNode).Error; err != nil {
		t.Fatalf("create other node: %v", err)
	}
	if err := db.Create(&model.SearchDocument{Namespace: ctxns.DefaultNamespace, NodeID: defaultNode.ID, Content: "stale default", Language: "go"}).Error; err != nil {
		t.Fatalf("seed default doc: %v", err)
	}
	if err := db.Create(&model.SearchDocument{Namespace: "tenant-a", NodeID: otherNode.ID, Content: "keep tenant-a", Language: "go"}).Error; err != nil {
		t.Fatalf("seed tenant doc: %v", err)
	}

	count, err := RefreshSearchDocuments(context.Background(), db)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected only default namespace docs rebuilt, got %d", count)
	}

	var otherCount int64
	if err := db.Model(&model.SearchDocument{}).Where("namespace = ?", "tenant-a").Count(&otherCount).Error; err != nil {
		t.Fatalf("count tenant docs: %v", err)
	}
	if otherCount != 1 {
		t.Fatalf("expected tenant-a docs preserved, got %d", otherCount)
	}

	var defaultCount int64
	if err := db.Model(&model.SearchDocument{}).Where("namespace = ?", ctxns.DefaultNamespace).Count(&defaultCount).Error; err != nil {
		t.Fatalf("count default docs: %v", err)
	}
	if defaultCount != 1 {
		t.Fatalf("expected one rebuilt default doc, got %d", defaultCount)
	}
}

func TestRefreshSearchDocuments_TransactionRollsBackOnInsertFailure(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	st := gormstore.New(db)
	if err := st.AutoMigrate(); err != nil {
		t.Fatalf("migrate store: %v", err)
	}
	if err := db.AutoMigrate(&model.SearchDocument{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	node := model.Node{QualifiedName: "pkg.TooLong", Kind: model.NodeKindFunction, Name: "TooLong", FilePath: "too_long.go", StartLine: 1, EndLine: 2, Language: "go"}
	if err := db.Create(&node).Error; err != nil {
		t.Fatalf("create node: %v", err)
	}
	seed := model.SearchDocument{Namespace: ctxns.DefaultNamespace, NodeID: 9999, Content: "seed", Language: "go"}
	if err := db.Create(&seed).Error; err != nil {
		t.Fatalf("seed search doc: %v", err)
	}
	if err := db.Exec("CREATE TRIGGER fail_search_docs_insert BEFORE INSERT ON search_documents BEGIN SELECT RAISE(ABORT, 'boom'); END;").Error; err != nil {
		t.Fatalf("create trigger: %v", err)
	}

	_, err = RefreshSearchDocuments(context.Background(), db)
	if err == nil {
		t.Fatal("expected refresh to fail")
	}

	var count int64
	if err := db.Model(&model.SearchDocument{}).Where("node_id = ?", seed.NodeID).Count(&count).Error; err != nil {
		t.Fatalf("count docs: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected original search document to survive rollback, got %d", count)
	}
}

func TestRefreshSearchDocuments_RebuildsPerBatchWithoutAccumulatingGlobalSlice(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	st := gormstore.New(db)
	if err := st.AutoMigrate(); err != nil {
		t.Fatalf("migrate store: %v", err)
	}
	if err := db.AutoMigrate(&model.SearchDocument{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	for i := 0; i < 550; i++ {
		node := model.Node{
			QualifiedName: "pkg.Node" + strconv.Itoa(i),
			Kind:          model.NodeKindFunction,
			Name:          "Node" + strconv.Itoa(i),
			FilePath:      filepath.Join("pkg", "file"+strconv.Itoa(i)+".go"),
			StartLine:     i + 1,
			EndLine:       i + 1,
			Language:      "go",
		}
		if err := db.Create(&node).Error; err != nil {
			t.Fatalf("create node %d: %v", i, err)
		}
	}

	count, err := RefreshSearchDocuments(context.Background(), db)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if count != 550 {
		t.Fatalf("expected 550 search docs, got %d", count)
	}

	var persisted int64
	if err := db.Model(&model.SearchDocument{}).Count(&persisted).Error; err != nil {
		t.Fatalf("count docs: %v", err)
	}
	if persisted != 550 {
		t.Fatalf("expected 550 persisted search docs, got %d", persisted)
	}
}

func assertFunctionNamesByFile(t *testing.T, st *gormstore.Store, ctx context.Context, filePath string, want []string) {
	t.Helper()

	nodes, err := st.GetNodesByFile(ctx, filePath)
	if err != nil {
		t.Fatalf("GetNodesByFile(%q): %v", filePath, err)
	}

	got := make([]string, 0, len(nodes))
	for _, node := range nodes {
		if node.Kind == model.NodeKindFunction {
			got = append(got, node.Name)
		}
	}

	sort.Strings(got)
	if got == nil {
		got = []string{}
	}
	if want == nil {
		want = []string{}
	}
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("function names in %s: got=%v want=%v", filePath, got, want)
	}
}
