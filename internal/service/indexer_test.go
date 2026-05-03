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
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/tae2089/code-context-graph/internal/analysis/incremental"
	"github.com/tae2089/code-context-graph/internal/ctxns"
	"github.com/tae2089/code-context-graph/internal/model"
	"github.com/tae2089/code-context-graph/internal/parse/treesitter"
	"github.com/tae2089/code-context-graph/internal/store"
	"github.com/tae2089/code-context-graph/internal/store/gormstore"
	storesearch "github.com/tae2089/code-context-graph/internal/store/search"
)

type recordingGraphStore struct {
	t         *testing.T
	ops       []string
	nextID    uint
	nodesByFP map[string][]model.Node
}

func newRecordingGraphStore(t *testing.T) *recordingGraphStore {
	return &recordingGraphStore{t: t, nodesByFP: make(map[string][]model.Node)}
}

func (r *recordingGraphStore) record(op string) {
	r.ops = append(r.ops, op)
}

func (r *recordingGraphStore) WithTx(ctx context.Context, fn func(store.GraphStore) error) error {
	return fn(r)
}

func (r *recordingGraphStore) AutoMigrate() error { return nil }

func (r *recordingGraphStore) DeleteGraph(ctx context.Context) error {
	r.record("DeleteGraph")
	r.nodesByFP = make(map[string][]model.Node)
	return nil
}

func (r *recordingGraphStore) UpsertNodes(ctx context.Context, nodes []model.Node) error {
	r.record("UpsertNodes")
	for i := range nodes {
		r.nextID++
		nodes[i].ID = r.nextID
		r.nodesByFP[nodes[i].FilePath] = append(r.nodesByFP[nodes[i].FilePath], nodes[i])
	}
	return nil
}

func (r *recordingGraphStore) GetNodesByFile(ctx context.Context, filePath string) ([]model.Node, error) {
	r.record("GetNodesByFile")
	nodes := r.nodesByFP[filePath]
	out := make([]model.Node, len(nodes))
	copy(out, nodes)
	return out, nil
}

func (r *recordingGraphStore) UpsertAnnotation(ctx context.Context, ann *model.Annotation) error {
	r.record("UpsertAnnotation")
	return nil
}

func (r *recordingGraphStore) UpsertEdges(ctx context.Context, edges []model.Edge) error {
	r.record("UpsertEdges")
	return nil
}

func (r *recordingGraphStore) GetNode(ctx context.Context, qualifiedName string) (*model.Node, error) {
	return nil, nil
}

func (r *recordingGraphStore) GetNodeByID(ctx context.Context, id uint) (*model.Node, error) {
	return nil, nil
}

func (r *recordingGraphStore) GetNodesByIDs(ctx context.Context, ids []uint) ([]model.Node, error) {
	return nil, nil
}

func (r *recordingGraphStore) GetNodesByQualifiedNames(ctx context.Context, names []string) (map[string][]model.Node, error) {
	return nil, nil
}

func (r *recordingGraphStore) GetNodesByFiles(ctx context.Context, filePaths []string) (map[string][]model.Node, error) {
	return nil, nil
}

func (r *recordingGraphStore) GetEdgesFrom(ctx context.Context, nodeID uint) ([]model.Edge, error) {
	return nil, nil
}

func (r *recordingGraphStore) GetEdgesFromNodes(ctx context.Context, nodeIDs []uint) ([]model.Edge, error) {
	return nil, nil
}

func (r *recordingGraphStore) GetEdgesTo(ctx context.Context, nodeID uint) ([]model.Edge, error) {
	return nil, nil
}

func (r *recordingGraphStore) GetEdgesToNodes(ctx context.Context, nodeIDs []uint) ([]model.Edge, error) {
	return nil, nil
}

func (r *recordingGraphStore) DeleteNodesByFile(ctx context.Context, filePath string) error {
	return nil
}

func (r *recordingGraphStore) DeleteEdgesByFile(ctx context.Context, filePath string) error {
	return nil
}

func (r *recordingGraphStore) GetAnnotation(ctx context.Context, nodeID uint) (*model.Annotation, error) {
	return nil, nil
}

type recordingIncrementalSyncer struct {
	files         map[string]incremental.FileInfo
	existingFiles []string
	result        *incremental.SyncStats
	err           error
}

func (r *recordingIncrementalSyncer) Sync(ctx context.Context, files map[string]incremental.FileInfo) (*incremental.SyncStats, error) {
	panic("unexpected Sync call")
}

func (r *recordingIncrementalSyncer) SyncWithExisting(ctx context.Context, files map[string]incremental.FileInfo, existingFiles []string) (*incremental.SyncStats, error) {
	r.files = files
	r.existingFiles = append([]string(nil), existingFiles...)
	return r.result, r.err
}

type scopedSearchBackendSpy struct {
	rebuildCalls      int
	rebuildNodesCalls int
	nodeIDs           []uint
}

func (s *scopedSearchBackendSpy) Migrate(db *gorm.DB) error { return nil }
func (s *scopedSearchBackendSpy) Rebuild(ctx context.Context, db *gorm.DB) error {
	s.rebuildCalls++
	return nil
}
func (s *scopedSearchBackendSpy) RebuildNodes(ctx context.Context, db *gorm.DB, nodeIDs []uint) error {
	s.rebuildNodesCalls++
	s.nodeIDs = append([]uint(nil), nodeIDs...)
	return nil
}
func (s *scopedSearchBackendSpy) PurgeNamespace(ctx context.Context, db *gorm.DB) error { return nil }
func (s *scopedSearchBackendSpy) Query(ctx context.Context, db *gorm.DB, query string, limit int) ([]model.Node, error) {
	return nil, nil
}

type failingBuildParser struct {
	failPath string
}

func (p failingBuildParser) Parse(filePath string, content []byte) ([]model.Node, []model.Edge, error) {
	return p.ParseWithContext(context.Background(), filePath, content)
}

func (p failingBuildParser) ParseWithContext(ctx context.Context, filePath string, content []byte) ([]model.Node, []model.Edge, error) {
	if filePath == p.failPath {
		return nil, nil, errors.New("parse boom")
	}
	name := strings.TrimSuffix(filepath.Base(filePath), filepath.Ext(filePath))
	return []model.Node{{
		QualifiedName: "pkg." + name,
		Kind:          model.NodeKindFunction,
		Name:          name,
		FilePath:      filePath,
		StartLine:     1,
		EndLine:       1,
		Hash:          string(content),
		Language:      "stub",
	}}, nil, nil
}

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

func TestNewParsedBuildEdgeBatch_DoesNotRetainNodeSideState(t *testing.T) {
	typ := reflect.TypeFor[parsedBuildEdgeBatch]()
	for _, name := range []string{"nodes", "tsComments", "sourceLines"} {
		if _, ok := typ.FieldByName(name); ok {
			t.Fatalf("parsedBuildEdgeBatch must not retain %s", name)
		}
	}
}

func TestNewParsedBuildNodeBatch_DropsRawContentAndOnlyBuildsSourceLinesWhenNeeded(t *testing.T) {
	typ := reflect.TypeFor[parsedBuildNodeBatch]()
	if _, ok := typ.FieldByName("content"); ok {
		t.Fatal("parsedBuildNodeBatch must not retain raw content")
	}

	noComments := newParsedBuildNodeBatch("sample.go", []byte("package sample\nfunc Keep() {}\n"), nil, nil, "")
	if noComments.sourceLines != nil {
		t.Fatalf("expected no sourceLines without comments, got %#v", noComments.sourceLines)
	}

	withComments := newParsedBuildNodeBatch(
		"sample.go",
		[]byte("package sample\n// hello\nfunc Keep() {}\n"),
		nil,
		[]treesitter.CommentBlock{{StartLine: 2, EndLine: 2, Text: "// hello"}},
		"go",
	)
	if withComments.sourceLines == nil {
		t.Fatal("expected sourceLines when tsComments exist")
	}
	if got, want := len(withComments.sourceLines), 4; got != want {
		t.Fatalf("sourceLines length mismatch: got=%d want=%d", got, want)
	}
	if got, want := withComments.sourceLines[1], "// hello"; got != want {
		t.Fatalf("sourceLines[1] mismatch: got=%q want=%q", got, want)
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

func TestBuild_OrderingSeam_AnnotationBeforeEdges(t *testing.T) {
	fakeStore := newRecordingGraphStore(t)
	svc := &GraphService{
		Store: fakeStore,
		Walkers: map[string]*treesitter.Walker{
			".go": treesitter.NewWalker(treesitter.GoSpec),
		},
		Logger: slog.Default(),
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "sample.go"), []byte(`package sample

// @intent keep track of the function
func Keep() {}
`), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	if _, err := svc.Build(context.Background(), BuildOptions{Dir: dir}); err != nil {
		t.Fatalf("Build: %v", err)
	}

	want := []string{"DeleteGraph", "UpsertNodes", "GetNodesByFile", "UpsertAnnotation", "UpsertEdges"}
	for i, op := range want[:4] {
		if len(fakeStore.ops) <= i {
			t.Fatalf("ops too short: got %v", fakeStore.ops)
		}
		if fakeStore.ops[i] != op {
			t.Fatalf("op[%d]=%q want %q (all=%v)", i, fakeStore.ops[i], op, fakeStore.ops)
		}
	}
	firstEdge := slices.Index(fakeStore.ops, "UpsertEdges")
	lastAnn := -1
	for i := len(fakeStore.ops) - 1; i >= 0; i-- {
		if fakeStore.ops[i] == "UpsertAnnotation" {
			lastAnn = i
			break
		}
	}
	if firstEdge == -1 || lastAnn == -1 {
		t.Fatalf("expected annotations and edges in ops: %v", fakeStore.ops)
	}
	if firstEdge <= lastAnn {
		t.Fatalf("expected UpsertEdges after all UpsertAnnotation calls, got %v", fakeStore.ops)
	}
}

func TestBuild_FlushesLargeBuildInBoundedBatches(t *testing.T) {
	fakeStore := newRecordingGraphStore(t)
	svc := &GraphService{
		Store: fakeStore,
		Walkers: map[string]*treesitter.Walker{
			".go": treesitter.NewWalker(treesitter.GoSpec),
		},
		Logger: slog.Default(),
	}

	dir := t.TempDir()
	for i := range buildFlushFileBatchSize + 1 {
		content := `package sample

// @intent keep track of the function
func Keep` + strconv.Itoa(i) + `() {}
`
		name := "file" + strconv.Itoa(i) + ".go"
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	stats, err := svc.Build(context.Background(), BuildOptions{Dir: dir})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if stats.TotalFiles != buildFlushFileBatchSize+1 {
		t.Fatalf("TotalFiles = %d, want %d", stats.TotalFiles, buildFlushFileBatchSize+1)
	}

	edgeFlushes := 0
	for _, op := range fakeStore.ops {
		if op == "UpsertEdges" {
			edgeFlushes++
		}
	}
	if edgeFlushes < 2 {
		t.Fatalf("expected at least 2 edge flushes, got %d (ops=%v)", edgeFlushes, fakeStore.ops)
	}

	firstEdge := slices.Index(fakeStore.ops, "UpsertEdges")
	lastAnn := -1
	for i := len(fakeStore.ops) - 1; i >= 0; i-- {
		if fakeStore.ops[i] == "UpsertAnnotation" {
			lastAnn = i
			break
		}
	}
	if firstEdge == -1 || lastAnn == -1 {
		t.Fatalf("expected annotations and edges in ops: %v", fakeStore.ops)
	}
	if firstEdge >= lastAnn {
		t.Fatalf("expected batch flushing to allow an edge flush before the final annotation, got ops=%v", fakeStore.ops)
	}
}

func TestBuild_ReleasesBatchCommentStateAfterBinding(t *testing.T) {
	var snapshots []struct {
		batch         int
		tsCommentsNil bool
		sourceNil     bool
	}
	prevHook := testBuildBatchReleaseHook
	testBuildBatchReleaseHook = func(batches []parsedBuildNodeBatch, idx int) {
		snapshots = append(snapshots, struct {
			batch         int
			tsCommentsNil bool
			sourceNil     bool
		}{
			batch:         idx,
			tsCommentsNil: batches[idx].tsComments == nil,
			sourceNil:     batches[idx].sourceLines == nil,
		})
	}
	defer func() { testBuildBatchReleaseHook = prevHook }()

	fakeStore := newRecordingGraphStore(t)
	svc := &GraphService{
		Store: fakeStore,
		Walkers: map[string]*treesitter.Walker{
			".go": treesitter.NewWalker(treesitter.GoSpec),
		},
		Logger: slog.Default(),
	}

	dir := t.TempDir()
	for _, name := range []string{"alpha.go", "beta.go"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(`package sample

// @intent keep track of the function
func Keep() {}
`), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	if _, err := svc.Build(context.Background(), BuildOptions{Dir: dir}); err != nil {
		t.Fatalf("Build: %v", err)
	}

	if len(snapshots) != 2 {
		t.Fatalf("expected 2 batch release snapshots, got %d", len(snapshots))
	}
	for _, snap := range snapshots {
		if !snap.tsCommentsNil || !snap.sourceNil {
			t.Fatalf("expected batch %d comment state released, got tsCommentsNil=%v sourceNil=%v", snap.batch, snap.tsCommentsNil, snap.sourceNil)
		}
	}
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

func TestBuild_MaxFileBytesRejectsLargeFileAndPreservesPreviousGraph(t *testing.T) {
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

	tooLarge := "package sample\n\nfunc Oversized() {}\n"
	if err := os.WriteFile(goPath, []byte(tooLarge), 0o644); err != nil {
		t.Fatalf("write oversized file: %v", err)
	}

	_, err = svc.Build(ctx, BuildOptions{Dir: tmpDir, MaxFileBytes: int64(len(tooLarge) - 1)})
	if err == nil {
		t.Fatal("expected Build to reject file larger than MaxFileBytes")
	}
	if !strings.Contains(err.Error(), "exceeds max file bytes") {
		t.Fatalf("expected max file bytes error, got %v", err)
	}

	assertFunctionNamesByFile(t, st, ctx, "sample.go", []string{"Keep"})
}

func TestBuild_MaxTotalParsedBytesRejectsBeforeMutation(t *testing.T) {
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
	if err := os.WriteFile(filepath.Join(tmpDir, "keep.go"), []byte("package sample\n\nfunc Keep() {}\n"), 0o644); err != nil {
		t.Fatalf("write keep file: %v", err)
	}

	ctx := context.Background()
	if _, err := svc.Build(ctx, BuildOptions{Dir: tmpDir}); err != nil {
		t.Fatalf("first Build: %v", err)
	}
	assertFunctionNamesByFile(t, st, ctx, "keep.go", []string{"Keep"})

	if err := os.WriteFile(filepath.Join(tmpDir, "other.go"), []byte("package sample\n\nfunc Other() {}\n"), 0o644); err != nil {
		t.Fatalf("write other file: %v", err)
	}

	_, err = svc.Build(ctx, BuildOptions{Dir: tmpDir, MaxTotalParsedBytes: 1})
	if err == nil {
		t.Fatal("expected Build to reject total parsed bytes limit")
	}
	if !strings.Contains(err.Error(), "exceeds max total parsed bytes") {
		t.Fatalf("expected max total parsed bytes error, got %v", err)
	}

	assertFunctionNamesByFile(t, st, ctx, "keep.go", []string{"Keep"})
	assertFunctionNamesByFile(t, st, ctx, "other.go", nil)
}

func TestBuild_ParseFailureRollsBackStreamedFlushes(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	st := gormstore.New(db)
	if err := st.AutoMigrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	svc := &GraphService{
		Store: st,
		DB:    db,
		Parsers: map[string]Parser{
			".stub": failingBuildParser{failPath: "fail.stub"},
		},
		Logger: slog.Default(),
	}

	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, "keep.stub"), []byte("keep-v1"), 0o644); err != nil {
		t.Fatalf("write keep.stub: %v", err)
	}

	ctx := context.Background()
	if _, err := svc.Build(ctx, BuildOptions{Dir: tmpDir}); err != nil {
		t.Fatalf("first Build: %v", err)
	}
	assertFunctionNamesByFile(t, st, ctx, "keep.stub", []string{"keep"})

	if err := os.WriteFile(filepath.Join(tmpDir, "fail.stub"), []byte("fail"), 0o644); err != nil {
		t.Fatalf("write fail.stub: %v", err)
	}
	_, err = svc.Build(ctx, BuildOptions{Dir: tmpDir})
	if err == nil {
		t.Fatal("expected parse failure")
	}
	if !strings.Contains(err.Error(), "parse boom") {
		t.Fatalf("expected parse boom, got %v", err)
	}

	assertFunctionNamesByFile(t, st, ctx, "keep.stub", []string{"keep"})
	assertFunctionNamesByFile(t, st, ctx, "fail.stub", nil)
}

func TestUpdate_SkipsUnreadableFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("broken symlink unreadable path scenario is unix-specific")
	}

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.Node{}); err != nil {
		t.Fatalf("migrate nodes: %v", err)
	}

	svc := &GraphService{
		DB:      db,
		Walkers: map[string]*treesitter.Walker{".go": treesitter.NewWalker(treesitter.GoSpec)},
		Logger:  slog.Default(),
	}

	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, "keep.go"), []byte("package sample\n\nfunc Keep() {}\n"), 0o644); err != nil {
		t.Fatalf("write keep file: %v", err)
	}
	if err := os.Symlink(filepath.Join(tmpDir, "missing.go"), filepath.Join(tmpDir, "broken.go")); err != nil {
		t.Fatalf("create broken symlink: %v", err)
	}

	syncer := &recordingIncrementalSyncer{result: &incremental.SyncStats{}}
	_, err = svc.Update(context.Background(), UpdateOptions{BuildOptions: BuildOptions{Dir: tmpDir}, Syncer: syncer})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if _, ok := syncer.files["keep.go"]; !ok {
		t.Fatalf("expected keep.go to be synced, got files=%v", syncer.files)
	}
	if _, ok := syncer.files["broken.go"]; ok {
		t.Fatalf("expected unreadable broken.go to be skipped, got files=%v", syncer.files)
	}
}

func TestUpdate_MaxFileBytesRejectsLargeFile(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.Node{}); err != nil {
		t.Fatalf("migrate nodes: %v", err)
	}

	svc := &GraphService{
		DB:      db,
		Walkers: map[string]*treesitter.Walker{".go": treesitter.NewWalker(treesitter.GoSpec)},
		Logger:  slog.Default(),
	}

	tmpDir := t.TempDir()
	tooLarge := []byte("package sample\n\nfunc Oversized() {}\n")
	goPath := filepath.Join(tmpDir, "oversized.go")
	if err := os.WriteFile(goPath, tooLarge, 0o644); err != nil {
		t.Fatalf("write oversized file: %v", err)
	}

	syncer := &recordingIncrementalSyncer{result: &incremental.SyncStats{}}
	_, err = svc.Update(context.Background(), UpdateOptions{BuildOptions: BuildOptions{Dir: tmpDir, MaxFileBytes: int64(len(tooLarge) - 1)}, Syncer: syncer})
	if err == nil {
		t.Fatal("expected Update to reject file larger than MaxFileBytes")
	}
	if !strings.Contains(err.Error(), "exceeds max file bytes") {
		t.Fatalf("expected max file bytes error, got %v", err)
	}
	if syncer.files != nil {
		t.Fatalf("expected syncer not to run on max file bytes error, got files=%v", syncer.files)
	}
}

func TestUpdate_MaxTotalParsedBytesRejectsBeforeSync(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.Node{}); err != nil {
		t.Fatalf("migrate nodes: %v", err)
	}

	svc := &GraphService{
		DB:      db,
		Walkers: map[string]*treesitter.Walker{".go": treesitter.NewWalker(treesitter.GoSpec)},
		Logger:  slog.Default(),
	}

	tmpDir := t.TempDir()
	first := []byte("package sample\n\nfunc Keep() {}\n")
	second := []byte("package sample\n\nfunc Other() {}\n")
	if err := os.WriteFile(filepath.Join(tmpDir, "keep.go"), first, 0o644); err != nil {
		t.Fatalf("write keep file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "other.go"), second, 0o644); err != nil {
		t.Fatalf("write other file: %v", err)
	}

	syncer := &recordingIncrementalSyncer{result: &incremental.SyncStats{}}
	_, err = svc.Update(context.Background(), UpdateOptions{BuildOptions: BuildOptions{Dir: tmpDir, MaxTotalParsedBytes: int64(len(first))}, Syncer: syncer})
	if err == nil {
		t.Fatal("expected Update to reject total parsed bytes limit")
	}
	if !strings.Contains(err.Error(), "exceeds max total parsed bytes") {
		t.Fatalf("expected max total parsed bytes error, got %v", err)
	}
	if syncer.files != nil {
		t.Fatalf("expected syncer not to run on max total parsed bytes error, got files=%v", syncer.files)
	}
}

func TestUpdate_IncludePaths_FiltersExistingFilesWhenReplaceFalse(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.Node{}); err != nil {
		t.Fatalf("migrate nodes: %v", err)
	}
	if err := db.Create(&model.Node{Namespace: ctxns.DefaultNamespace, FilePath: filepath.Join("src", "api", "handler.go")}).Error; err != nil {
		t.Fatalf("seed api node: %v", err)
	}
	if err := db.Create(&model.Node{Namespace: ctxns.DefaultNamespace, FilePath: filepath.Join("src", "other", "helper.go")}).Error; err != nil {
		t.Fatalf("seed other node: %v", err)
	}

	svc := &GraphService{
		DB:      db,
		Walkers: map[string]*treesitter.Walker{".go": treesitter.NewWalker(treesitter.GoSpec)},
		Logger:  slog.Default(),
	}

	tmpDir := t.TempDir()
	apiDir := filepath.Join(tmpDir, "src", "api")
	if err := os.MkdirAll(apiDir, 0o755); err != nil {
		t.Fatalf("mkdir api: %v", err)
	}
	if err := os.WriteFile(filepath.Join(apiDir, "handler.go"), []byte("package api\n\nfunc Handler() {}\n"), 0o644); err != nil {
		t.Fatalf("write handler: %v", err)
	}

	syncer := &recordingIncrementalSyncer{result: &incremental.SyncStats{}}
	_, err = svc.Update(context.Background(), UpdateOptions{BuildOptions: BuildOptions{Dir: tmpDir, IncludePaths: []string{filepath.Join("src", "api")}}, Syncer: syncer, Replace: false})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got, want := syncer.existingFiles, []string{filepath.Join("src", "api", "handler.go")}; !reflect.DeepEqual(got, want) {
		t.Fatalf("existingFiles mismatch: got=%v want=%v", got, want)
	}
}

func TestUpdate_ExcludePatterns_LeavesMatchingFilesOutOfSync(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.Node{}); err != nil {
		t.Fatalf("migrate nodes: %v", err)
	}

	svc := &GraphService{
		DB:      db,
		Walkers: map[string]*treesitter.Walker{".go": treesitter.NewWalker(treesitter.GoSpec)},
		Logger:  slog.Default(),
	}

	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, "keep.go"), []byte("package sample\n\nfunc Keep() {}\n"), 0o644); err != nil {
		t.Fatalf("write keep file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "skip.gen.go"), []byte("package sample\n\nfunc Skip() {}\n"), 0o644); err != nil {
		t.Fatalf("write skip file: %v", err)
	}

	syncer := &recordingIncrementalSyncer{result: &incremental.SyncStats{}}
	_, err = svc.Update(context.Background(), UpdateOptions{
		BuildOptions: BuildOptions{Dir: tmpDir, ExcludePatterns: []string{"*.gen.go"}},
		Syncer:       syncer,
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if _, ok := syncer.files["keep.go"]; !ok {
		t.Fatalf("expected keep.go to be synced, got files=%v", syncer.files)
	}
	if _, ok := syncer.files["skip.gen.go"]; ok {
		t.Fatalf("expected skip.gen.go to be excluded, got files=%v", syncer.files)
	}
}

func TestUpdate_NoRecursive_SkipsNestedFilesFromSync(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.Node{}); err != nil {
		t.Fatalf("migrate nodes: %v", err)
	}

	svc := &GraphService{
		DB:      db,
		Walkers: map[string]*treesitter.Walker{".go": treesitter.NewWalker(treesitter.GoSpec)},
		Logger:  slog.Default(),
	}

	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, "root.go"), []byte("package sample\n\nfunc Root() {}\n"), 0o644); err != nil {
		t.Fatalf("write root file: %v", err)
	}
	nestedDir := filepath.Join(tmpDir, "nested")
	if err := os.MkdirAll(nestedDir, 0o755); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}
	if err := os.WriteFile(filepath.Join(nestedDir, "nested.go"), []byte("package sample\n\nfunc Nested() {}\n"), 0o644); err != nil {
		t.Fatalf("write nested file: %v", err)
	}

	syncer := &recordingIncrementalSyncer{result: &incremental.SyncStats{}}
	_, err = svc.Update(context.Background(), UpdateOptions{
		BuildOptions: BuildOptions{Dir: tmpDir, NoRecursive: true},
		Syncer:       syncer,
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if _, ok := syncer.files["root.go"]; !ok {
		t.Fatalf("expected root.go to be synced, got files=%v", syncer.files)
	}
	if _, ok := syncer.files[filepath.Join("nested", "nested.go")]; ok {
		t.Fatalf("expected nested/nested.go to be skipped, got files=%v", syncer.files)
	}
}

func TestForceReparseFiles_IncludesUnchangedEdgeSourceForChangedTarget(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.Node{}, &model.Edge{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	ctx := context.Background()
	source := model.Node{Namespace: ctxns.DefaultNamespace, QualifiedName: "pkg.Source", Kind: model.NodeKindFunction, Name: "Source", FilePath: "source.go", StartLine: 1, EndLine: 2, Hash: "same", Language: "go"}
	target := model.Node{Namespace: ctxns.DefaultNamespace, QualifiedName: "pkg.Target", Kind: model.NodeKindFunction, Name: "Target", FilePath: "target.go", StartLine: 1, EndLine: 2, Hash: "old", Language: "go"}
	if err := db.Create(&source).Error; err != nil {
		t.Fatalf("seed source: %v", err)
	}
	if err := db.Create(&target).Error; err != nil {
		t.Fatalf("seed target: %v", err)
	}
	if err := db.Create(&model.Edge{Namespace: ctxns.DefaultNamespace, FromNodeID: source.ID, ToNodeID: target.ID, Kind: model.EdgeKindCalls, FilePath: "source.go", Fingerprint: "source-target"}).Error; err != nil {
		t.Fatalf("seed edge: %v", err)
	}

	_, nodesByFile, err := existingGraphFileState(ctx, db)
	if err != nil {
		t.Fatalf("existing state: %v", err)
	}
	forceFiles, err := forceReparseFiles(ctx, db, nodesByFile, map[string]string{
		"source.go": "same",
		"target.go": "new",
	})
	if err != nil {
		t.Fatalf("force files: %v", err)
	}
	if _, ok := forceFiles["source.go"]; !ok {
		t.Fatalf("expected unchanged edge source to be forced, got %v", forceFiles)
	}
	if _, ok := forceFiles["target.go"]; ok {
		t.Fatalf("did not expect changed target file to be forced, got %v", forceFiles)
	}
}

func TestExistingGraphFileState_LoadsOnlyForceReparseFields(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.Node{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	seed := model.Node{
		Namespace:     ctxns.DefaultNamespace,
		QualifiedName: "pkg.Keep",
		Kind:          model.NodeKindFunction,
		Name:          "Keep",
		FilePath:      "keep.go",
		StartLine:     7,
		EndLine:       9,
		Hash:          "same",
		Language:      "go",
	}
	if err := db.Create(&seed).Error; err != nil {
		t.Fatalf("seed node: %v", err)
	}

	files, nodesByFile, err := existingGraphFileState(context.Background(), db)
	if err != nil {
		t.Fatalf("existing state: %v", err)
	}
	if got, want := files, []string{"keep.go"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("files mismatch: got=%v want=%v", got, want)
	}
	nodes := nodesByFile["keep.go"]
	if len(nodes) != 1 {
		t.Fatalf("expected one node, got %v", nodes)
	}
	if nodes[0].ID != seed.ID || nodes[0].FilePath != "keep.go" || nodes[0].Hash != "same" {
		t.Fatalf("minimal fields mismatch: %+v", nodes[0])
	}
	if nodes[0].QualifiedName != "" || nodes[0].Name != "" || nodes[0].StartLine != 0 || nodes[0].Language != "" {
		t.Fatalf("unexpected non-minimal fields loaded: %+v", nodes[0])
	}
}

func TestForceReparseFiles_ChunksLargeChangedNodeLookup(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.Node{}, &model.Edge{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	ctx := context.Background()
	existingNodesByFile := make(map[string][]model.Node)
	currentHashes := map[string]string{"source.go": "same"}
	var firstChangedID uint
	for i := range forceReparseEdgeChunkSize + 1 {
		filePath := fmt.Sprintf("target-%03d.go", i)
		node := model.Node{Namespace: ctxns.DefaultNamespace, FilePath: filePath, Hash: "old"}
		if err := db.Create(&node).Error; err != nil {
			t.Fatalf("seed changed node %d: %v", i, err)
		}
		if i == 0 {
			firstChangedID = node.ID
		}
		existingNodesByFile[filePath] = []model.Node{{ID: node.ID, FilePath: filePath, Hash: "old"}}
		currentHashes[filePath] = "new"
	}
	source := model.Node{Namespace: ctxns.DefaultNamespace, FilePath: "source.go", Hash: "same"}
	if err := db.Create(&source).Error; err != nil {
		t.Fatalf("seed source: %v", err)
	}
	existingNodesByFile["source.go"] = []model.Node{{ID: source.ID, FilePath: "source.go", Hash: "same"}}
	if err := db.Create(&model.Edge{Namespace: ctxns.DefaultNamespace, FromNodeID: source.ID, ToNodeID: firstChangedID, Kind: model.EdgeKindCalls, FilePath: "source.go", Fingerprint: "source-target"}).Error; err != nil {
		t.Fatalf("seed edge: %v", err)
	}

	forceFiles, err := forceReparseFiles(ctx, db, existingNodesByFile, currentHashes)
	if err != nil {
		t.Fatalf("force files: %v", err)
	}
	if _, ok := forceFiles["source.go"]; !ok {
		t.Fatalf("expected unchanged source.go to be forced across chunks, got %v", forceFiles)
	}
}

func TestUpdate_SearchRefreshIsScopedToAffectedNodes(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:?_pragma=journal_mode(WAL)"), &gorm.Config{Logger: gormlogger.Discard})
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

	changed := model.Node{Namespace: ctxns.DefaultNamespace, QualifiedName: "pkg.Changed", Kind: model.NodeKindFunction, Name: "Changed", FilePath: "changed.stub", StartLine: 1, EndLine: 1, Hash: "old", Language: "stub"}
	untouched := model.Node{Namespace: ctxns.DefaultNamespace, QualifiedName: "pkg.Untouched", Kind: model.NodeKindFunction, Name: "Untouched", FilePath: "untouched.stub", StartLine: 1, EndLine: 1, Hash: "same", Language: "stub"}
	for _, node := range []*model.Node{&changed, &untouched} {
		if err := db.Create(node).Error; err != nil {
			t.Fatalf("create node: %v", err)
		}
	}
	if err := db.Create(&model.SearchDocument{Namespace: ctxns.DefaultNamespace, NodeID: changed.ID, Content: "stale changed", Language: "stub"}).Error; err != nil {
		t.Fatalf("seed changed doc: %v", err)
	}
	if err := db.Create(&model.SearchDocument{Namespace: ctxns.DefaultNamespace, NodeID: untouched.ID, Content: "keep untouched", Language: "stub"}).Error; err != nil {
		t.Fatalf("seed untouched doc: %v", err)
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "changed.stub"), []byte("new"), 0o644); err != nil {
		t.Fatalf("write changed: %v", err)
	}
	if err := db.Model(&model.Node{}).Where("id = ?", changed.ID).Update("hash", "old").Error; err != nil {
		t.Fatalf("reset changed hash: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "untouched.stub"), []byte("same"), 0o644); err != nil {
		t.Fatalf("write untouched: %v", err)
	}
	untouchedHash := sha256.Sum256([]byte("same"))
	if err := db.Model(&model.Node{}).Where("id = ?", untouched.ID).Update("hash", hex.EncodeToString(untouchedHash[:])).Error; err != nil {
		t.Fatalf("update untouched hash: %v", err)
	}
	backend := &scopedSearchBackendSpy{}
	svc := &GraphService{
		Store:         st,
		DB:            db,
		SearchBackend: backend,
		Parsers:       map[string]Parser{".stub": failingBuildParser{}},
		Logger:        slog.Default(),
	}
	syncer := incremental.NewWithRegistry(st, map[string]incremental.Parser{".stub": failingBuildParser{}}, incremental.WithLogger(slog.Default()))

	stats, err := svc.Update(context.Background(), UpdateOptions{BuildOptions: BuildOptions{Dir: dir}, Syncer: syncer})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if stats.Modified != 1 || stats.Skipped != 1 {
		t.Fatalf("expected one modified and one skipped file, got %+v", stats)
	}
	if backend.rebuildCalls != 0 {
		t.Fatalf("expected no full search rebuild, got %d", backend.rebuildCalls)
	}
	if backend.rebuildNodesCalls != 1 {
		t.Fatalf("expected one scoped search rebuild, got %d", backend.rebuildNodesCalls)
	}

	var newChanged model.Node
	if err := db.Where("file_path = ? AND hash = ?", "changed.stub", "new").First(&newChanged).Error; err != nil {
		t.Fatalf("load new changed node: %v", err)
	}
	if slices.Contains(backend.nodeIDs, untouched.ID) {
		t.Fatalf("expected untouched node not to be scoped, got %v", backend.nodeIDs)
	}
	if !slices.Contains(backend.nodeIDs, changed.ID) || !slices.Contains(backend.nodeIDs, newChanged.ID) {
		t.Fatalf("expected old and new changed node ids in scope, got %v old=%d new=%d", backend.nodeIDs, changed.ID, newChanged.ID)
	}

	var untouchedDoc model.SearchDocument
	if err := db.Where("node_id = ?", untouched.ID).First(&untouchedDoc).Error; err != nil {
		t.Fatalf("load untouched doc: %v", err)
	}
	if untouchedDoc.Content != "keep untouched" {
		t.Fatalf("expected skipped file search doc preserved, got %q", untouchedDoc.Content)
	}
}

func TestUpdate_SearchRefreshEmptyScopeIsNoOp(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:?_pragma=journal_mode(WAL)"), &gorm.Config{Logger: gormlogger.Discard})
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
	node := model.Node{Namespace: ctxns.DefaultNamespace, QualifiedName: "pkg.Keep", Kind: model.NodeKindFunction, Name: "Keep", FilePath: "keep.stub", StartLine: 1, EndLine: 1, Hash: "same", Language: "stub"}
	if err := db.Create(&node).Error; err != nil {
		t.Fatalf("create node: %v", err)
	}
	if err := db.Create(&model.SearchDocument{Namespace: ctxns.DefaultNamespace, NodeID: node.ID, Content: "keep doc", Language: "stub"}).Error; err != nil {
		t.Fatalf("seed doc: %v", err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "keep.stub"), []byte("same"), 0o644); err != nil {
		t.Fatalf("write keep: %v", err)
	}
	keepHash := sha256.Sum256([]byte("same"))
	if err := db.Model(&model.Node{}).Where("id = ?", node.ID).Update("hash", hex.EncodeToString(keepHash[:])).Error; err != nil {
		t.Fatalf("update keep hash: %v", err)
	}
	backend := &scopedSearchBackendSpy{}
	svc := &GraphService{
		Store:         st,
		DB:            db,
		SearchBackend: backend,
		Parsers:       map[string]Parser{".stub": failingBuildParser{}},
		Logger:        slog.Default(),
	}
	syncer := incremental.NewWithRegistry(st, map[string]incremental.Parser{".stub": failingBuildParser{}}, incremental.WithLogger(slog.Default()))

	stats, err := svc.Update(context.Background(), UpdateOptions{BuildOptions: BuildOptions{Dir: dir}, Syncer: syncer})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if stats.Skipped != 1 {
		t.Fatalf("expected file skipped, got %+v", stats)
	}
	if backend.rebuildCalls != 0 || backend.rebuildNodesCalls != 0 {
		t.Fatalf("expected empty search scope no-op, full=%d scoped=%d", backend.rebuildCalls, backend.rebuildNodesCalls)
	}
}

func TestBuild_ContextCanceledBeforeMutationPreservesPreviousGraph(t *testing.T) {
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

	if err := os.WriteFile(goPath, []byte("package sample\n\nfunc Replaced() {}\n"), 0o644); err != nil {
		t.Fatalf("write replacement file: %v", err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()

	_, err = svc.Build(canceled, BuildOptions{Dir: tmpDir})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
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
func (f *failSearchBackend) RebuildNodes(ctx context.Context, db *gorm.DB, nodeIDs []uint) error {
	return f.err
}
func (f *failSearchBackend) PurgeNamespace(ctx context.Context, db *gorm.DB) error {
	return f.err
}
func (f *failSearchBackend) Migrate(db *gorm.DB) error { return nil }
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

	seedNode := model.Node{Namespace: ctxns.DefaultNamespace, QualifiedName: "pkg.Seed", Kind: model.NodeKindFunction, Name: "Seed", FilePath: "seed.go", StartLine: 1, EndLine: 2, Language: "go"}
	if err := db.Create(&seedNode).Error; err != nil {
		t.Fatalf("seed node: %v", err)
	}
	if err := db.Create(&model.SearchDocument{Namespace: ctxns.DefaultNamespace, NodeID: seedNode.ID, Content: "seed searchable", Language: "go"}).Error; err != nil {
		t.Fatalf("seed search doc: %v", err)
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

	var keptSeed, createdKeep int64
	if err := db.Model(&model.Node{}).Where("qualified_name = ?", "pkg.Seed").Count(&keptSeed).Error; err != nil {
		t.Fatalf("count seed node: %v", err)
	}
	if err := db.Model(&model.Node{}).Where("qualified_name = ?", "sample.Keep").Count(&createdKeep).Error; err != nil {
		t.Fatalf("count new node: %v", err)
	}
	if keptSeed != 1 || createdKeep != 0 {
		t.Fatalf("expected graph rollback after search rebuild failure, seed=%d new=%d", keptSeed, createdKeep)
	}

	var docCount int64
	if err := db.Model(&model.SearchDocument{}).Where("content = ?", "seed searchable").Count(&docCount).Error; err != nil {
		t.Fatalf("count seed doc: %v", err)
	}
	if docCount != 1 {
		t.Fatalf("expected seed search document to survive rollback, got %d", docCount)
	}
}

func TestBuild_SearchDocumentRefreshFailureRollsBackGraphAndDocs(t *testing.T) {
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

	seedNode := model.Node{Namespace: ctxns.DefaultNamespace, QualifiedName: "pkg.Seed", Kind: model.NodeKindFunction, Name: "Seed", FilePath: "seed.go", StartLine: 1, EndLine: 2, Language: "go"}
	if err := db.Create(&seedNode).Error; err != nil {
		t.Fatalf("seed node: %v", err)
	}
	if err := db.Create(&model.SearchDocument{Namespace: ctxns.DefaultNamespace, NodeID: seedNode.ID, Content: "seed searchable", Language: "go"}).Error; err != nil {
		t.Fatalf("seed search doc: %v", err)
	}
	if err := db.Exec("CREATE TRIGGER fail_search_docs_insert BEFORE INSERT ON search_documents BEGIN SELECT RAISE(ABORT, 'search doc boom'); END;").Error; err != nil {
		t.Fatalf("create trigger: %v", err)
	}

	svc := &GraphService{
		Store:         st,
		DB:            db,
		SearchBackend: &failSearchBackend{},
		Walkers:       map[string]*treesitter.Walker{".go": treesitter.NewWalker(treesitter.GoSpec)},
		Logger:        slog.Default(),
	}

	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, "sample.go"), []byte("package sample\n\nfunc Keep() {}\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	_, err = svc.Build(context.Background(), BuildOptions{Dir: tmpDir})
	if err == nil {
		t.Fatal("expected build to fail when search document refresh fails")
	}
	if !strings.Contains(err.Error(), "search doc boom") {
		t.Fatalf("expected search doc boom, got %v", err)
	}

	var keptSeed, createdKeep int64
	if err := db.Model(&model.Node{}).Where("qualified_name = ?", "pkg.Seed").Count(&keptSeed).Error; err != nil {
		t.Fatalf("count seed node: %v", err)
	}
	if err := db.Model(&model.Node{}).Where("qualified_name = ?", "sample.Keep").Count(&createdKeep).Error; err != nil {
		t.Fatalf("count new node: %v", err)
	}
	if keptSeed != 1 || createdKeep != 0 {
		t.Fatalf("expected graph rollback after search document refresh failure, seed=%d new=%d", keptSeed, createdKeep)
	}

	var docCount int64
	if err := db.Model(&model.SearchDocument{}).Where("content = ?", "seed searchable").Count(&docCount).Error; err != nil {
		t.Fatalf("count seed doc: %v", err)
	}
	if docCount != 1 {
		t.Fatalf("expected seed search document to survive rollback, got %d", docCount)
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

func TestRefreshSearchDocumentsFor_RefreshesOnlyScopedNodes(t *testing.T) {
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

	changed := model.Node{Namespace: ctxns.DefaultNamespace, QualifiedName: "pkg.Changed", Kind: model.NodeKindFunction, Name: "Changed", FilePath: "changed.go", StartLine: 1, EndLine: 2, Language: "go"}
	untouched := model.Node{Namespace: ctxns.DefaultNamespace, QualifiedName: "pkg.Untouched", Kind: model.NodeKindFunction, Name: "Untouched", FilePath: "untouched.go", StartLine: 1, EndLine: 2, Language: "go"}
	foreign := model.Node{Namespace: "tenant-a", QualifiedName: "pkg.Foreign", Kind: model.NodeKindFunction, Name: "Foreign", FilePath: "foreign.go", StartLine: 1, EndLine: 2, Language: "go"}
	for _, node := range []*model.Node{&changed, &untouched, &foreign} {
		if err := db.Create(node).Error; err != nil {
			t.Fatalf("create node: %v", err)
		}
	}
	if err := db.Create(&model.SearchDocument{Namespace: ctxns.DefaultNamespace, NodeID: changed.ID, Content: "stale changed", Language: "go"}).Error; err != nil {
		t.Fatalf("seed changed doc: %v", err)
	}
	if err := db.Create(&model.SearchDocument{Namespace: ctxns.DefaultNamespace, NodeID: untouched.ID, Content: "keep untouched", Language: "go"}).Error; err != nil {
		t.Fatalf("seed untouched doc: %v", err)
	}
	if err := db.Create(&model.SearchDocument{Namespace: "tenant-a", NodeID: foreign.ID, Content: "keep foreign", Language: "go"}).Error; err != nil {
		t.Fatalf("seed foreign doc: %v", err)
	}

	count, err := RefreshSearchDocumentsFor(context.Background(), db, []uint{changed.ID, foreign.ID})
	if err != nil {
		t.Fatalf("refresh scoped: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected one default namespace doc refreshed, got %d", count)
	}

	var changedDoc, untouchedDoc, foreignDoc model.SearchDocument
	if err := db.Where("node_id = ?", changed.ID).First(&changedDoc).Error; err != nil {
		t.Fatalf("load changed doc: %v", err)
	}
	if err := db.Where("node_id = ?", untouched.ID).First(&untouchedDoc).Error; err != nil {
		t.Fatalf("load untouched doc: %v", err)
	}
	if err := db.Where("node_id = ?", foreign.ID).First(&foreignDoc).Error; err != nil {
		t.Fatalf("load foreign doc: %v", err)
	}
	if changedDoc.Content == "stale changed" || !strings.Contains(changedDoc.Content, "pkg.Changed") {
		t.Fatalf("expected changed doc rebuilt, got %q", changedDoc.Content)
	}
	if untouchedDoc.Content != "keep untouched" {
		t.Fatalf("expected untouched doc preserved, got %q", untouchedDoc.Content)
	}
	if foreignDoc.Content != "keep foreign" {
		t.Fatalf("expected foreign doc preserved, got %q", foreignDoc.Content)
	}
}

func TestRefreshSearchDocumentsFor_EmptyScopeIsNoOp(t *testing.T) {
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
	node := model.Node{Namespace: ctxns.DefaultNamespace, QualifiedName: "pkg.Keep", Kind: model.NodeKindFunction, Name: "Keep", FilePath: "keep.go", StartLine: 1, EndLine: 2, Language: "go"}
	if err := db.Create(&node).Error; err != nil {
		t.Fatalf("create node: %v", err)
	}
	if err := db.Create(&model.SearchDocument{Namespace: ctxns.DefaultNamespace, NodeID: node.ID, Content: "stale keep", Language: "go"}).Error; err != nil {
		t.Fatalf("seed doc: %v", err)
	}

	count, err := RefreshSearchDocumentsFor(context.Background(), db, nil)
	if err != nil {
		t.Fatalf("refresh empty scope: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected empty scope no-op count, got %d", count)
	}

	var doc model.SearchDocument
	if err := db.Where("node_id = ?", node.ID).First(&doc).Error; err != nil {
		t.Fatalf("load doc: %v", err)
	}
	if doc.Content != "stale keep" {
		t.Fatalf("expected stale doc preserved for empty scope, got %q", doc.Content)
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

	for i := range 550 {
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

func TestUpdate_FailOnUnreadable_FailsFastWithTypedError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("broken symlink unreadable path scenario is unix-specific")
	}

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.Node{}); err != nil {
		t.Fatalf("migrate nodes: %v", err)
	}

	svc := &GraphService{
		DB:      db,
		Walkers: map[string]*treesitter.Walker{".go": treesitter.NewWalker(treesitter.GoSpec)},
		Logger:  slog.Default(),
	}

	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, "keep.go"), []byte("package sample\n\nfunc Keep() {}\n"), 0o644); err != nil {
		t.Fatalf("write keep file: %v", err)
	}
	if err := os.Symlink(filepath.Join(tmpDir, "missing.go"), filepath.Join(tmpDir, "broken.go")); err != nil {
		t.Fatalf("create broken symlink: %v", err)
	}

	syncer := &recordingIncrementalSyncer{result: &incremental.SyncStats{}}
	_, err = svc.Update(context.Background(), UpdateOptions{
		BuildOptions:      BuildOptions{Dir: tmpDir},
		Syncer:            syncer,
		FailOnUnreadable:  true,
	})
	if err == nil {
		t.Fatal("expected fail-fast error on unreadable file")
	}
	var unreadable *UnreadableFilesError
	if !errors.As(err, &unreadable) {
		t.Fatalf("expected *UnreadableFilesError, got %T: %v", err, err)
	}
	if len(unreadable.Files) == 0 {
		t.Fatal("expected at least one unreadable file in error")
	}
	foundBroken := false
	for _, f := range unreadable.Files {
		if f == "broken.go" {
			foundBroken = true
		}
	}
	if !foundBroken {
		t.Fatalf("expected broken.go in unreadable files, got %v", unreadable.Files)
	}
	if syncer.files != nil {
		t.Fatalf("expected syncer not to run when failing fast, got files=%v", syncer.files)
	}
}

func TestUpdate_FailOnUnreadable_DefaultStillWarnsAndSkips(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("broken symlink unreadable path scenario is unix-specific")
	}

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.Node{}); err != nil {
		t.Fatalf("migrate nodes: %v", err)
	}

	svc := &GraphService{
		DB:      db,
		Walkers: map[string]*treesitter.Walker{".go": treesitter.NewWalker(treesitter.GoSpec)},
		Logger:  slog.Default(),
	}

	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, "keep.go"), []byte("package sample\n\nfunc Keep() {}\n"), 0o644); err != nil {
		t.Fatalf("write keep file: %v", err)
	}
	if err := os.Symlink(filepath.Join(tmpDir, "missing.go"), filepath.Join(tmpDir, "broken.go")); err != nil {
		t.Fatalf("create broken symlink: %v", err)
	}

	syncer := &recordingIncrementalSyncer{result: &incremental.SyncStats{}}
	_, err = svc.Update(context.Background(), UpdateOptions{
		BuildOptions: BuildOptions{Dir: tmpDir},
		Syncer:       syncer,
	})
	if err != nil {
		t.Fatalf("expected default warn-and-skip, got error: %v", err)
	}
	if _, ok := syncer.files["keep.go"]; !ok {
		t.Fatalf("expected keep.go to be synced under default policy, got %v", syncer.files)
	}
}
