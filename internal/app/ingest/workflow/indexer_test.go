// toBinderComments가 Walker의 CommentBlock 메타 필드를
// Binder의 CommentBlock로 누락 없이 옮기는지 검증하는 재발 방지 테스트.
//
// 배경: P0-2에서 추가된 IsDocstring/OwnerStartLine 필드가 초기 indexer 변환
// 루프에서 누락되어 Python docstring 바인딩이 프로덕션 경로에서 동작하지
// 않던 문제가 있었다 (code review에서 발견, 97dfb3b 에서 수정).
//
// 이 테스트는 Walker↔Binder 타입이 분기 진화할 경우 동일한 실수가
// 재발하지 않도록 변환 함수 단위로 필드 전파를 고정한다.
package workflow

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/tae2089/code-context-graph/internal/adapters/outbound/graphgorm"
	"github.com/tae2089/code-context-graph/internal/adapters/outbound/searchsql"
	"github.com/tae2089/code-context-graph/internal/adapters/outbound/treesitter"
	flows "github.com/tae2089/code-context-graph/internal/app/analyze/flow"
	querypkg "github.com/tae2089/code-context-graph/internal/app/analyze/query"
	"github.com/tae2089/code-context-graph/internal/app/ingest"
	"github.com/tae2089/code-context-graph/internal/app/ingest/incremental"
	"github.com/tae2089/code-context-graph/internal/app/ingest/resolve"
	requestctx "github.com/tae2089/code-context-graph/internal/ctx"
	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

type testTransaction struct {
	graph  ingest.GraphStore
	search ingest.SearchWriter
}

func (tx testTransaction) Graph() ingest.GraphStore    { return tx.graph }
func (tx testTransaction) Search() ingest.SearchWriter { return tx.search }

type testUnitOfWork struct {
	graph ingest.GraphStore
}

func (u testUnitOfWork) WithinTransaction(_ context.Context, fn func(ingest.Transaction) error) error {
	return fn(testTransaction{graph: u.graph, search: testSearchWriter{}})
}

type testSearchWriter struct{}

func (testSearchWriter) RebuildAll(context.Context) error           { return nil }
func (testSearchWriter) RebuildNodes(context.Context, []uint) error { return nil }

func newTestUnitOfWork(db *gorm.DB, backend searchsql.Backend) ingest.UnitOfWork {
	return graphgorm.NewUnitOfWork(db, func(tx *gorm.DB) ingest.SearchWriter {
		return searchsql.NewSearchWriter(tx, backend, slog.Default())
	})
}

type serviceINQueryCaptureLogger struct {
	gormlogger.Interface
	needle string
	maxIDs int
	hits   int
}

func (l *serviceINQueryCaptureLogger) LogMode(level gormlogger.LogLevel) gormlogger.Interface {
	return l
}
func (l *serviceINQueryCaptureLogger) Trace(ctx context.Context, begin time.Time, fc func() (string, int64), err error) {
	sql, _ := fc()
	if strings.Contains(sql, l.needle) && strings.Contains(sql, " IN (") {
		l.hits++
		ids := countServiceSQLInList(sql)
		if ids > l.maxIDs {
			l.maxIDs = ids
		}
	}
}

func countServiceSQLInList(sql string) int {
	start := strings.Index(sql, " IN (")
	if start < 0 {
		return 0
	}
	start += len(" IN (")
	end := strings.Index(sql[start:], ")")
	if end < 0 {
		return 0
	}
	list := strings.TrimSpace(sql[start : start+end])
	if list == "" {
		return 0
	}
	return strings.Count(list, ",") + 1
}

type recordingGraphStore struct {
	t                     *testing.T
	ops                   []string
	nextID                uint
	nodesByFP             map[string][]graph.Node
	edges                 []graph.Edge
	upsertedNodeBatches   [][]graph.Node
	upsertedEdges         [][]graph.Edge
	fileSuffixLookupCalls int
	importFileNodeCalls   int
}

func newRecordingGraphStore(t *testing.T) *recordingGraphStore {
	return &recordingGraphStore{t: t, nodesByFP: make(map[string][]graph.Node)}
}

func (r *recordingGraphStore) record(op string) {
	r.ops = append(r.ops, op)
}

func (r *recordingGraphStore) DeleteGraph(ctx context.Context) error {
	r.record("DeleteGraph")
	r.nodesByFP = make(map[string][]graph.Node)
	return nil
}

func (r *recordingGraphStore) UpsertNodes(ctx context.Context, nodes []graph.Node) error {
	r.record("UpsertNodes")
	r.upsertedNodeBatches = append(r.upsertedNodeBatches, append([]graph.Node(nil), nodes...))
	for i := range nodes {
		r.nextID++
		nodes[i].ID = r.nextID
		r.nodesByFP[nodes[i].FilePath] = append(r.nodesByFP[nodes[i].FilePath], nodes[i])
	}
	return nil
}

func (r *recordingGraphStore) GetNodesByFile(ctx context.Context, filePath string) ([]graph.Node, error) {
	r.record("GetNodesByFile")
	nodes := r.nodesByFP[filePath]
	out := make([]graph.Node, len(nodes))
	copy(out, nodes)
	return out, nil
}

func (r *recordingGraphStore) UpsertAnnotation(ctx context.Context, ann *graph.Annotation) error {
	r.record("UpsertAnnotation")
	return nil
}

func (r *recordingGraphStore) UpsertEdges(ctx context.Context, edges []graph.Edge) error {
	r.record("UpsertEdges")
	batch := append([]graph.Edge(nil), edges...)
	r.upsertedEdges = append(r.upsertedEdges, batch)
	r.edges = append(r.edges, batch...)
	return nil
}

func (r *recordingGraphStore) GetNode(ctx context.Context, qualifiedName string) (*graph.Node, error) {
	return nil, nil
}

func (r *recordingGraphStore) GetNodeByID(ctx context.Context, id uint) (*graph.Node, error) {
	return nil, nil
}

func (r *recordingGraphStore) GetNodesByIDs(ctx context.Context, ids []uint) ([]graph.Node, error) {
	set := make(map[uint]bool, len(ids))
	for _, id := range ids {
		set[id] = true
	}
	var result []graph.Node
	for _, nodes := range r.nodesByFP {
		for _, n := range nodes {
			if set[n.ID] {
				result = append(result, n)
			}
		}
	}
	return result, nil
}

func (r *recordingGraphStore) GetNodesByQualifiedNames(ctx context.Context, names []string) (map[string][]graph.Node, error) {
	set := make(map[string]bool, len(names))
	for _, name := range names {
		set[name] = true
	}
	result := make(map[string][]graph.Node)
	for _, nodes := range r.nodesByFP {
		for _, n := range nodes {
			if set[n.QualifiedName] {
				result[n.QualifiedName] = append(result[n.QualifiedName], n)
			}
		}
	}
	return result, nil
}

func (r *recordingGraphStore) GetNodesByFiles(ctx context.Context, filePaths []string) (map[string][]graph.Node, error) {
	r.record("GetNodesByFiles")
	set := make(map[string]bool, len(filePaths))
	for _, fp := range filePaths {
		set[fp] = true
	}
	result := make(map[string][]graph.Node)
	for fp, nodes := range r.nodesByFP {
		if set[fp] {
			out := make([]graph.Node, len(nodes))
			copy(out, nodes)
			result[fp] = out
		}
	}
	return result, nil
}

func (r *recordingGraphStore) ListFileNodes(context.Context) ([]graph.Node, error) {
	var result []graph.Node
	for _, nodes := range r.nodesByFP {
		for _, node := range nodes {
			if node.Kind != graph.NodeKindPackage {
				result = append(result, graph.Node{ID: node.ID, FilePath: node.FilePath, Hash: node.Hash})
			}
		}
	}
	return result, nil
}

func (r *recordingGraphStore) ListImportFileNodes(context.Context) ([]graph.Node, error) {
	r.importFileNodeCalls++
	var result []graph.Node
	for _, nodes := range r.nodesByFP {
		for _, node := range nodes {
			if node.Kind == graph.NodeKindFile {
				result = append(result, node)
			}
		}
	}
	return result, nil
}

func (r *recordingGraphStore) GetFileNodesByPathSuffix(ctx context.Context, suffix string) ([]graph.Node, error) {
	r.fileSuffixLookupCalls++
	suffix = strings.Trim(path.Clean(strings.TrimSpace(suffix)), "/")
	if suffix == "" || suffix == "." {
		return nil, nil
	}
	var out []graph.Node
	bestDepth := -1
	for _, nodes := range r.nodesByFP {
		for _, node := range nodes {
			if node.Kind != graph.NodeKindFile {
				continue
			}
			dir := strings.Trim(path.Dir(node.FilePath), "/")
			if dir == "." || dir == "" {
				continue
			}
			if suffix == dir {
				return []graph.Node{node}, nil
			}
			if depth := serviceCommonPathSuffixDepth(suffix, dir); depth > 0 {
				if depth > bestDepth {
					bestDepth = depth
					out = []graph.Node{node}
					continue
				}
				if depth == bestDepth {
					out = append(out, node)
				}
			}
		}
	}
	return out, nil
}

func TestBuildResolveLookup_IndexesImportFilesOnceAndPreservesSuffixRules(t *testing.T) {
	store := newRecordingGraphStore(t)
	store.nodesByFP["internal/mcp/deps.go"] = []graph.Node{{ID: 1, Kind: graph.NodeKindFile, FilePath: "internal/mcp/deps.go"}}
	store.nodesByFP["pkg/mcp/deps.go"] = []graph.Node{{ID: 2, Kind: graph.NodeKindFile, FilePath: "pkg/mcp/deps.go"}}
	store.nodesByFP["pkg/internal/mcp/deps.go"] = []graph.Node{{ID: 3, Kind: graph.NodeKindFile, FilePath: "pkg/internal/mcp/deps.go"}}
	lookup := newBuildResolveLookup(store)

	exact, err := lookup.GetFileNodesByPathSuffix(context.Background(), "internal/mcp")
	if err != nil {
		t.Fatalf("exact suffix lookup: %v", err)
	}
	if got := nodeFilePaths(exact); !slices.Equal(got, []string{"internal/mcp/deps.go"}) {
		t.Fatalf("exact suffix nodes = %v, want internal/mcp only", got)
	}

	longest, err := lookup.GetFileNodesByPathSuffix(context.Background(), "github.com/example/project/mcp")
	if err != nil {
		t.Fatalf("longest suffix lookup: %v", err)
	}
	if got := nodeFilePaths(longest); !slices.Equal(got, []string{"internal/mcp/deps.go", "pkg/internal/mcp/deps.go", "pkg/mcp/deps.go"}) {
		t.Fatalf("longest suffix nodes = %v, want all mcp directories", got)
	}
	if store.importFileNodeCalls != 1 {
		t.Fatalf("import file node reads = %d, want 1", store.importFileNodeCalls)
	}
	if store.fileSuffixLookupCalls != 0 {
		t.Fatalf("single-suffix store reads = %d, want 0", store.fileSuffixLookupCalls)
	}
}

func nodeFilePaths(nodes []graph.Node) []string {
	paths := make([]string, 0, len(nodes))
	for _, node := range nodes {
		paths = append(paths, node.FilePath)
	}
	slices.Sort(paths)
	return paths
}

func TestBuildResolveLookup_RecordsStoreReadTiming(t *testing.T) {
	store := newRecordingGraphStore(t)
	store.nodesByFP["sample.go"] = []graph.Node{{
		ID:       1,
		Kind:     graph.NodeKindFile,
		FilePath: "sample.go",
	}}
	timing := BuildResolveTiming{}
	lookup := newBuildResolveLookupWithTiming(store, &timing)

	got, err := lookup.GetNodesByFiles(context.Background(), []string{"sample.go"})
	if err != nil {
		t.Fatalf("get nodes by files: %v", err)
	}
	if len(got["sample.go"]) != 1 {
		t.Fatalf("nodes for sample.go = %v, want one node", got)
	}
	if timing.NodesByFiles.Calls != 1 {
		t.Fatalf("GetNodesByFiles calls = %d, want 1", timing.NodesByFiles.Calls)
	}
	if timing.NodesByFiles.MS < 0 {
		t.Fatalf("GetNodesByFiles milliseconds = %d, want non-negative", timing.NodesByFiles.MS)
	}
}

func serviceCommonPathSuffixDepth(a, b string) int {
	a = strings.Trim(a, "/")
	b = strings.Trim(b, "/")
	if a == "" || b == "" {
		return 0
	}
	aParts := strings.Split(a, "/")
	bParts := strings.Split(b, "/")
	depth := 0
	for i, j := len(aParts)-1, len(bParts)-1; i >= 0 && j >= 0; i, j = i-1, j-1 {
		if aParts[i] != bParts[j] {
			break
		}
		depth++
	}
	return depth
}

func (r *recordingGraphStore) GetEdgesFrom(ctx context.Context, nodeID uint) ([]graph.Edge, error) {
	return nil, nil
}

func (r *recordingGraphStore) GetEdgesFromNodes(ctx context.Context, nodeIDs []uint) ([]graph.Edge, error) {
	return nil, nil
}

func (r *recordingGraphStore) GetEdgesTo(ctx context.Context, nodeID uint) ([]graph.Edge, error) {
	return nil, nil
}

func (r *recordingGraphStore) GetEdgesToNodes(ctx context.Context, nodeIDs []uint) ([]graph.Edge, error) {
	set := make(map[uint]bool, len(nodeIDs))
	for _, id := range nodeIDs {
		set[id] = true
	}
	var result []graph.Edge
	for _, e := range r.edges {
		if set[e.ToNodeID] {
			result = append(result, e)
		}
	}
	return result, nil
}

func (r *recordingGraphStore) DeleteNodesByFile(ctx context.Context, filePath string) error {
	return nil
}

func (r *recordingGraphStore) DeleteEdgesByFile(ctx context.Context, filePath string) error {
	return nil
}

func (r *recordingGraphStore) DeletePackageSemanticEdges(context.Context, []string) error {
	return nil
}

func (r *recordingGraphStore) GetAnnotation(ctx context.Context, nodeID uint) (*graph.Annotation, error) {
	return nil, nil
}

type recordingIncrementalSyncer struct {
	files         map[string]incremental.FileInfo
	existingFiles []string
	result        *incremental.SyncStats
	err           error
	calls         []recordingSyncCall
}

type recordingSyncCall struct {
	files         map[string]incremental.FileInfo
	existingFiles []string
}

func (r *recordingIncrementalSyncer) Sync(ctx context.Context, files map[string]incremental.FileInfo) (*incremental.SyncStats, error) {
	panic("unexpected Sync call")
}

func (r *recordingIncrementalSyncer) SyncWithExisting(ctx context.Context, files map[string]incremental.FileInfo, existingFiles []string) (*incremental.SyncStats, error) {
	r.files = files
	r.existingFiles = append([]string(nil), existingFiles...)
	fileCopy := make(map[string]incremental.FileInfo, len(files))
	for k, v := range files {
		fileCopy[k] = v
	}
	r.calls = append(r.calls, recordingSyncCall{files: fileCopy, existingFiles: append([]string(nil), existingFiles...)})
	return r.result, r.err
}

func sortedIncrementalFileKeys(files map[string]incremental.FileInfo) []string {
	keys := make([]string, 0, len(files))
	for k := range files {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
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
func (s *scopedSearchBackendSpy) Query(ctx context.Context, db *gorm.DB, query string, limit int) ([]graph.Node, error) {
	return nil, nil
}

type failingBuildParser struct {
	failPath string
}

func (p failingBuildParser) Parse(filePath string, content []byte) ([]graph.Node, []graph.Edge, error) {
	return p.ParseWithContext(context.Background(), filePath, content)
}

func (p failingBuildParser) ParseWithContext(ctx context.Context, filePath string, content []byte) ([]graph.Node, []graph.Edge, error) {
	if filePath == p.failPath {
		return nil, nil, errors.New("parse boom")
	}
	name := strings.TrimSuffix(filepath.Base(filePath), filepath.Ext(filePath))
	return []graph.Node{{
		QualifiedName: "pkg." + name,
		Kind:          graph.NodeKindFunction,
		Name:          name,
		FilePath:      filePath,
		StartLine:     1,
		EndLine:       1,
		Hash:          string(content),
		Language:      "stub",
	}}, nil, nil
}

type blockingBuildParser struct {
	started chan<- struct{}
	release <-chan struct{}
}

func (p blockingBuildParser) Parse(filePath string, content []byte) ([]graph.Node, []graph.Edge, error) {
	return p.ParseWithContext(context.Background(), filePath, content)
}

func (p blockingBuildParser) ParseWithContext(ctx context.Context, filePath string, content []byte) ([]graph.Node, []graph.Edge, error) {
	select {
	case p.started <- struct{}{}:
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	}
	select {
	case <-p.release:
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	}
	return failingBuildParser{}.ParseWithContext(ctx, filePath, content)
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

func TestFlushBuildEdges_ResolvesAndUpsertsBoundedBatches(t *testing.T) {
	ctx := context.Background()
	st := newRecordingGraphStore(t)
	st.nodesByFP["cmd/main.go"] = []graph.Node{
		{ID: 10, QualifiedName: "cmd/main.go", Name: "cmd/main.go", Kind: graph.NodeKindFile, FilePath: "cmd/main.go", Language: "go"},
		{ID: 1, QualifiedName: "main.Run", Name: "Run", Kind: graph.NodeKindFunction, FilePath: "cmd/main.go", StartLine: 1, EndLine: 100, Language: "go"},
	}
	st.nodesByFP["mcp/deps.go"] = []graph.Node{
		{ID: 20, QualifiedName: "mcp/deps.go", Name: "mcp/deps.go", Kind: graph.NodeKindFile, FilePath: "mcp/deps.go", Language: "go"},
		{ID: 2, QualifiedName: "mcp.FlowTracer", Name: "FlowTracer", Kind: graph.NodeKindType, FilePath: "mcp/deps.go", StartLine: 3, EndLine: 5, Language: "go"},
	}
	st.nodesByFP["flows/tracer.go"] = []graph.Node{
		{ID: 30, QualifiedName: "flows/tracer.go", Name: "flows/tracer.go", Kind: graph.NodeKindFile, FilePath: "flows/tracer.go", Language: "go"},
		{ID: 3, QualifiedName: "flows.Tracer", Name: "Tracer", Kind: graph.NodeKindClass, FilePath: "flows/tracer.go", StartLine: 7, EndLine: 7, Language: "go"},
		{ID: 4, QualifiedName: "flows.Tracer.TraceFlow", Name: "TraceFlow", Kind: graph.NodeKindFunction, FilePath: "flows/tracer.go", StartLine: 9, EndLine: 11, Language: "go"},
	}

	var resolveSizes []int
	resolver := func(ctx context.Context, lookup resolve.NodeLookup, edges []graph.Edge, options resolve.ResolveOptions) ([]graph.Edge, error) {
		resolveSizes = append(resolveSizes, len(edges))
		if len(edges) > buildEdgeResolveChunkSize {
			t.Fatalf("resolve batch exceeded limit: got %d want <= %d", len(edges), buildEdgeResolveChunkSize)
		}
		return resolve.ResolveWithOptions(ctx, lookup, edges, options)
	}

	batches := []parsedBuildEdgeBatch{
		{relPath: "cmd/main.go", edges: []graph.Edge{{Kind: graph.EdgeKindImportsFrom, FilePath: "cmd/main.go", Line: 1, Fingerprint: "imports_from:cmd/main.go:github.com/example/project/mcp:1"}, {Kind: graph.EdgeKindCalls, FilePath: "cmd/main.go", Line: 2, Fingerprint: "calls:cmd/main.go:h.deps.FlowTracer.TraceFlow:2"}}},
		{relPath: "flows/tracer.go", edges: []graph.Edge{{Kind: graph.EdgeKindImplements, FilePath: "flows/tracer.go", Line: 7, Fingerprint: "implements:flows/tracer.go:flows.Tracer:mcp.FlowTracer"}}},
		{relPath: "other/main.go", edges: []graph.Edge{{Kind: graph.EdgeKindContains, FilePath: "other/main.go", Line: 1, Fingerprint: "contains:other/main.go:main.Run"}}},
	}

	svc := &Service{resolveEdges: resolver}
	if err := svc.flushBuildEdges(ctx, st, batches, nil, resolve.ResolveOptions{}); err != nil {
		t.Fatalf("flushBuildEdges: %v", err)
	}
	if got, want := resolveSizes, []int{1, 4}; !reflect.DeepEqual(got, want) {
		t.Fatalf("resolve sizes: got=%v want=%v", got, want)
	}
	if len(st.upsertedEdges) != 2 {
		t.Fatalf("expected 2 upserted edge batches, got %d", len(st.upsertedEdges))
	}
	if st.upsertedEdges[0][0].Kind != graph.EdgeKindImplements || st.upsertedEdges[0][0].FromNodeID != 3 || st.upsertedEdges[0][0].ToNodeID != 2 {
		t.Fatalf("implements edge mismatch: %+v", st.upsertedEdges[0][0])
	}
	if len(st.upsertedEdges[1]) != 2 {
		t.Fatalf("expected import+call edges in second batch, got %d", len(st.upsertedEdges[1]))
	}
	call := st.upsertedEdges[1][1]
	if call.Kind != graph.EdgeKindCalls || call.FromNodeID != 1 || call.ToNodeID != 4 {
		t.Fatalf("call edge mismatch: %+v", call)
	}
}

func TestFlushBuildEdgeSource_ReplaysTwoPassesInImplementsFirstOrder(t *testing.T) {
	ctx := context.Background()
	st := newRecordingGraphStore(t)
	batches := []parsedBuildEdgeBatch{
		{
			relPath: "first.go",
			edges: []graph.Edge{
				{Kind: graph.EdgeKindImplements, FilePath: "first.go", FromNodeID: 1, ToNodeID: 2},
				{Kind: graph.EdgeKindContains, FilePath: "first.go", FromNodeID: 1, ToNodeID: 3},
			},
		},
		{
			relPath: "second.go",
			edges: []graph.Edge{
				{Kind: graph.EdgeKindImplements, FilePath: "second.go", FromNodeID: 4, ToNodeID: 5},
				{Kind: graph.EdgeKindContains, FilePath: "second.go", FromNodeID: 4, ToNodeID: 6},
			},
		},
	}

	passes := 0
	source := buildEdgeBatchSource(func(yield func(parsedBuildEdgeBatch) error) error {
		passes++
		for _, batch := range batches {
			if err := yield(batch); err != nil {
				return err
			}
		}
		return nil
	})
	var resolvedKinds [][]graph.EdgeKind
	resolver := func(_ context.Context, _ resolve.NodeLookup, edges []graph.Edge, _ resolve.ResolveOptions) ([]graph.Edge, error) {
		if len(edges) > buildEdgeResolveChunkSize {
			t.Fatalf("resolve batch exceeded limit: got %d want <= %d", len(edges), buildEdgeResolveChunkSize)
		}
		kinds := make([]graph.EdgeKind, len(edges))
		for i := range edges {
			kinds[i] = edges[i].Kind
		}
		resolvedKinds = append(resolvedKinds, kinds)
		return edges, nil
	}

	svc := &Service{resolveEdges: resolver}
	if err := svc.flushBuildEdgeSourceWithTiming(ctx, st, source, nil, resolve.ResolveOptions{}, nil); err != nil {
		t.Fatalf("flushBuildEdgeSourceWithTiming: %v", err)
	}
	if passes != 2 {
		t.Fatalf("source passes = %d, want 2", passes)
	}
	seenNonImplements := false
	implementsCount := 0
	otherCount := 0
	for _, kinds := range resolvedKinds {
		for _, kind := range kinds {
			if kind == graph.EdgeKindImplements {
				if seenNonImplements {
					t.Fatal("implements edge resolved after non-implements edge")
				}
				implementsCount++
				continue
			}
			seenNonImplements = true
			otherCount++
		}
	}
	if implementsCount != 2 || otherCount != 2 {
		t.Fatalf("resolved counts = implements %d, other %d; want 2 and 2", implementsCount, otherCount)
	}
}

func TestFlushBuildEdgeSource_PropagatesSourceError(t *testing.T) {
	wantErr := errors.New("read edge spool")
	source := buildEdgeBatchSource(func(func(parsedBuildEdgeBatch) error) error {
		return wantErr
	})

	svc := &Service{}
	err := svc.flushBuildEdgeSourceWithTiming(context.Background(), newRecordingGraphStore(t), source, nil, resolve.ResolveOptions{}, nil)
	if !errors.Is(err, wantErr) {
		t.Fatalf("flush error = %v, want %v", err, wantErr)
	}
}

func TestBuildSpoolEdgeBatchSource_ReplaysRecordsAndTrailingBatches(t *testing.T) {
	spool := &buildSpool{dir: t.TempDir()}
	for seq, record := range []spooledBuildRecord{
		{RelPath: "first.go", Edges: []graph.Edge{{Kind: graph.EdgeKindContains, Fingerprint: "first"}}},
		{RelPath: "second.go", Edges: []graph.Edge{{Kind: graph.EdgeKindCalls, Fingerprint: "second"}}},
	} {
		if err := spool.writeRecord(seq, record); err != nil {
			t.Fatalf("write spool record %d: %v", seq, err)
		}
	}
	trailing := []parsedBuildEdgeBatch{{
		relPath: "package.go",
		edges:   []graph.Edge{{Kind: graph.EdgeKindImplements, Fingerprint: "package"}},
	}}

	source := spool.edgeBatchSource(context.Background(), trailing)
	for pass := 1; pass <= 2; pass++ {
		var got []string
		err := source(func(batch parsedBuildEdgeBatch) error {
			for _, edge := range batch.edges {
				got = append(got, batch.relPath+":"+edge.Fingerprint)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("source pass %d: %v", pass, err)
		}
		want := []string{"first.go:first", "second.go:second", "package.go:package"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("source pass %d = %v, want %v", pass, got, want)
		}
	}
}

func TestFlushBuildEdges_RecordsResolverAndUpsertTiming(t *testing.T) {
	ctx := context.Background()
	st := newRecordingGraphStore(t)
	st.nodesByFP["sample.go"] = []graph.Node{
		{ID: 10, QualifiedName: "sample.go", Name: "sample.go", Kind: graph.NodeKindFile, FilePath: "sample.go", Language: "go"},
		{ID: 1, QualifiedName: "sample.Keep", Name: "Keep", Kind: graph.NodeKindFunction, FilePath: "sample.go", StartLine: 1, EndLine: 3, Language: "go"},
	}
	timing := BuildResolveTiming{}
	batches := []parsedBuildEdgeBatch{{
		relPath: "sample.go",
		edges: []graph.Edge{{
			Kind:        graph.EdgeKindContains,
			FilePath:    "sample.go",
			Line:        1,
			Fingerprint: "contains:sample.go:sample.Keep",
		}},
	}}

	svc := &Service{}
	if err := svc.flushBuildEdgesWithTiming(ctx, st, batches, nil, resolve.ResolveOptions{}, &timing); err != nil {
		t.Fatalf("flush build edges: %v", err)
	}
	if timing.Resolver.Calls != 1 {
		t.Fatalf("resolver calls = %d, want 1", timing.Resolver.Calls)
	}
	if timing.UpsertEdges.Calls != len(st.upsertedEdges) {
		t.Fatalf("edge upsert calls = %d, want %d", timing.UpsertEdges.Calls, len(st.upsertedEdges))
	}
	if timing.NodesByFiles.Calls == 0 {
		t.Fatal("expected GetNodesByFiles timing to record resolver lookup")
	}
	if timing.Resolver.MS < 0 || timing.UpsertEdges.MS < 0 || timing.NodesByFiles.MS < 0 {
		t.Fatalf("resolve timing must be non-negative: %+v", timing)
	}
}

func TestFlushBuildEdges_ResolvesImplementsOnlyOnce(t *testing.T) {
	ctx := context.Background()
	st := newRecordingGraphStore(t)
	st.nodesByFP["cmd/main.go"] = []graph.Node{{ID: 10, QualifiedName: "cmd/main.go", Name: "cmd/main.go", Kind: graph.NodeKindFile, FilePath: "cmd/main.go", Language: "go"}, {ID: 1, QualifiedName: "main.Run", Name: "Run", Kind: graph.NodeKindFunction, FilePath: "cmd/main.go", StartLine: 1, EndLine: 100, Language: "go"}}
	st.nodesByFP["mcp/deps.go"] = []graph.Node{{ID: 20, QualifiedName: "mcp/deps.go", Name: "mcp/deps.go", Kind: graph.NodeKindFile, FilePath: "mcp/deps.go", Language: "go"}, {ID: 2, QualifiedName: "mcp.FlowTracer", Name: "FlowTracer", Kind: graph.NodeKindType, FilePath: "mcp/deps.go", StartLine: 3, EndLine: 5, Language: "go"}}
	st.nodesByFP["flows/tracer.go"] = []graph.Node{{ID: 30, QualifiedName: "flows/tracer.go", Name: "flows/tracer.go", Kind: graph.NodeKindFile, FilePath: "flows/tracer.go", Language: "go"}, {ID: 3, QualifiedName: "flows.Tracer", Name: "Tracer", Kind: graph.NodeKindClass, FilePath: "flows/tracer.go", StartLine: 7, EndLine: 7, Language: "go"}, {ID: 4, QualifiedName: "flows.Tracer.TraceFlow", Name: "TraceFlow", Kind: graph.NodeKindFunction, FilePath: "flows/tracer.go", StartLine: 9, EndLine: 11, Language: "go"}}

	var implementsSeen []int
	resolver := func(ctx context.Context, lookup resolve.NodeLookup, edges []graph.Edge, options resolve.ResolveOptions) ([]graph.Edge, error) {
		count := 0
		for _, edge := range edges {
			if edge.Kind == graph.EdgeKindImplements {
				count++
			}
		}
		implementsSeen = append(implementsSeen, count)
		return resolve.ResolveWithOptions(ctx, lookup, edges, options)
	}

	batches := []parsedBuildEdgeBatch{
		{relPath: "flows/tracer.go", edges: []graph.Edge{{Kind: graph.EdgeKindImplements, FilePath: "flows/tracer.go", Line: 7, Fingerprint: "implements:flows/tracer.go:flows.Tracer:mcp.FlowTracer"}}},
		{relPath: "cmd/main.go", edges: []graph.Edge{{Kind: graph.EdgeKindImportsFrom, FilePath: "cmd/main.go", Line: 1, Fingerprint: "imports_from:cmd/main.go:github.com/example/project/mcp:1"}, {Kind: graph.EdgeKindCalls, FilePath: "cmd/main.go", Line: 2, Fingerprint: "calls:cmd/main.go:h.deps.FlowTracer.TraceFlow:2"}}},
		{relPath: "cmd/main.go", edges: []graph.Edge{{Kind: graph.EdgeKindContains, FilePath: "cmd/main.go", Line: 1, Fingerprint: "contains:cmd/main.go:main.Run"}}},
	}

	svc := &Service{resolveEdges: resolver}
	if err := svc.flushBuildEdges(ctx, st, batches, nil, resolve.ResolveOptions{}); err != nil {
		t.Fatalf("flushBuildEdges: %v", err)
	}
	if got, want := implementsSeen, []int{1, 0}; !reflect.DeepEqual(got, want) {
		t.Fatalf("implements counts per resolve call: got=%v want=%v", got, want)
	}
}

func TestFlushBuildEdges_WarmsImportsAcrossChunkBoundaries(t *testing.T) {
	ctx := context.Background()
	st := newRecordingGraphStore(t)
	st.nodesByFP["cmd/main.go"] = []graph.Node{{ID: 10, QualifiedName: "cmd/main.go", Name: "cmd/main.go", Kind: graph.NodeKindFile, FilePath: "cmd/main.go", Language: "go"}, {ID: 1, QualifiedName: "main.Run", Name: "Run", Kind: graph.NodeKindFunction, FilePath: "cmd/main.go", StartLine: 1, EndLine: 1000, Language: "go"}}
	st.nodesByFP["mcp/deps.go"] = []graph.Node{{ID: 20, QualifiedName: "mcp/deps.go", Name: "mcp/deps.go", Kind: graph.NodeKindFile, FilePath: "mcp/deps.go", Language: "go"}, {ID: 2, QualifiedName: "mcp.FlowTracer", Name: "FlowTracer", Kind: graph.NodeKindType, FilePath: "mcp/deps.go", StartLine: 3, EndLine: 5, Language: "go"}}
	st.nodesByFP["flows/tracer.go"] = []graph.Node{{ID: 30, QualifiedName: "flows/tracer.go", Name: "flows/tracer.go", Kind: graph.NodeKindFile, FilePath: "flows/tracer.go", Language: "go"}, {ID: 3, QualifiedName: "flows.Tracer", Name: "Tracer", Kind: graph.NodeKindClass, FilePath: "flows/tracer.go", StartLine: 7, EndLine: 7, Language: "go"}, {ID: 4, QualifiedName: "flows.Tracer.TraceFlow", Name: "TraceFlow", Kind: graph.NodeKindFunction, FilePath: "flows/tracer.go", StartLine: 9, EndLine: 11, Language: "go"}}

	batches := []parsedBuildEdgeBatch{
		{relPath: "flows/tracer.go", edges: []graph.Edge{{Kind: graph.EdgeKindImplements, FilePath: "flows/tracer.go", Line: 7, Fingerprint: "implements:flows/tracer.go:flows.Tracer:mcp.FlowTracer"}}},
		{relPath: "cmd/main.go", edges: append([]graph.Edge{{Kind: graph.EdgeKindImportsFrom, FilePath: "cmd/main.go", Line: 1, Fingerprint: "imports_from:cmd/main.go:github.com/example/project/mcp:1"}}, repeatedCallEdges("cmd/main.go", buildEdgeResolveChunkSize)...)},
	}

	svc := &Service{}
	if err := svc.flushBuildEdges(ctx, st, batches, nil, resolve.ResolveOptions{}); err != nil {
		t.Fatalf("flushBuildEdges: %v", err)
	}
	if len(st.upsertedEdges) < 2 {
		t.Fatalf("expected implements and call edge batches, got %d", len(st.upsertedEdges))
	}
	lastBatch := st.upsertedEdges[len(st.upsertedEdges)-1]
	call := lastBatch[len(lastBatch)-1]
	if call.Kind != graph.EdgeKindCalls || call.ToNodeID != 4 {
		t.Fatalf("expected warmed call edge to resolve after chunk split, got %+v", call)
	}
}

func repeatedCallEdges(filePath string, count int) []graph.Edge {
	edges := make([]graph.Edge, 0, count)
	for i := 0; i < count; i++ {
		edges = append(edges, graph.Edge{Kind: graph.EdgeKindCalls, FilePath: filePath, Line: i + 2, Fingerprint: fmt.Sprintf("calls:%s:h.deps.FlowTracer.TraceFlow:%d", filePath, i+2)})
	}
	return edges
}

func TestBuild_UsesRepoLocalPackageClauseForGoImportAssertions(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	st := graphgorm.New(db)
	if err := st.AutoMigrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	svc := &Service{Store: st, UnitOfWork: newTestUnitOfWork(db, nil), Walkers: map[string]Parser{".go": treesitter.NewWalker(treesitter.GoSpec)}, Logger: slog.Default()}

	tmpDir := t.TempDir()
	mustMkdir := func(rel string) {
		if err := os.MkdirAll(filepath.Join(tmpDir, rel), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
	}
	mustWrite := func(rel, content string) {
		if err := os.WriteFile(filepath.Join(tmpDir, rel), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	mustWrite("go.mod", "module github.com/example/project\n\ngo 1.25.0\n")
	mustMkdir("internal/api")
	mustMkdir("mainpkg")
	mustWrite("internal/api/contracts.go", "package contracts\n\ntype Service interface {\n\tRun()\n}\n")
	mustWrite("internal/api/contracts_test.go", "package contracts_test\n")
	mustWrite("mainpkg/main.go", "package mainpkg\n\nimport dep \"github.com/example/project/internal/api\"\n\ntype MyType struct{}\n\nfunc (MyType) Run() {}\n\nvar _ dep.Service = MyType{}\n")

	ctx := context.Background()
	if _, err := svc.Build(ctx, BuildOptions{Dir: tmpDir}); err != nil {
		t.Fatalf("Build: %v", err)
	}

	impl, err := st.GetNode(ctx, "mainpkg.MyType")
	if err != nil || impl == nil {
		t.Fatalf("GetNode impl: node=%v err=%v", impl, err)
	}
	iface, err := st.GetNode(ctx, "contracts.Service")
	if err != nil || iface == nil {
		t.Fatalf("GetNode iface: node=%v err=%v", iface, err)
	}
	edges, err := st.GetEdgesFrom(ctx, impl.ID)
	if err != nil {
		t.Fatalf("GetEdgesFrom: %v", err)
	}
	for _, edge := range edges {
		if edge.Kind == graph.EdgeKindImplements && edge.ToNodeID == iface.ID {
			return
		}
	}
	t.Fatalf("expected implements edge from %d to contracts.Service %d, got %+v", impl.ID, iface.ID, edges)
}

func TestBuild_EmitsCrossFileGoStructuralImplements(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	st := graphgorm.New(db)
	if err := st.AutoMigrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	svc := &Service{Store: st, UnitOfWork: newTestUnitOfWork(db, nil), Walkers: map[string]Parser{".go": treesitter.NewWalker(treesitter.GoSpec)}, Logger: slog.Default()}

	tmpDir := t.TempDir()
	mustWrite := func(rel, content string) {
		full := filepath.Join(tmpDir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	mustWrite("go.mod", "module github.com/example/project\n\ngo 1.25.0\n")
	mustWrite("mainpkg/iface.go", "package mainpkg\n\ntype Writer interface {\n\tWrite([]byte) error\n}\n")
	mustWrite("mainpkg/impl.go", "package mainpkg\n\ntype FileWriter struct{}\n\nfunc (FileWriter) Write(data []byte) error {\n\treturn nil\n}\n")

	ctx := context.Background()
	if _, err := svc.Build(ctx, BuildOptions{Dir: tmpDir}); err != nil {
		t.Fatalf("Build: %v", err)
	}

	impl, err := st.GetNode(ctx, "mainpkg.FileWriter")
	if err != nil || impl == nil {
		t.Fatalf("GetNode impl: node=%v err=%v", impl, err)
	}
	iface, err := st.GetNode(ctx, "mainpkg.Writer")
	if err != nil || iface == nil {
		t.Fatalf("GetNode iface: node=%v err=%v", iface, err)
	}
	edges, err := st.GetEdgesFrom(ctx, impl.ID)
	if err != nil {
		t.Fatalf("GetEdgesFrom: %v", err)
	}
	for _, edge := range edges {
		if edge.Kind == graph.EdgeKindImplements && edge.ToNodeID == iface.ID {
			return
		}
	}
	t.Fatalf("expected cross-file implements edge from %d to %d, got %+v", impl.ID, iface.ID, edges)
}

func TestUpdate_EmitsCrossFileGoStructuralImplements(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	st := graphgorm.New(db)
	if err := st.AutoMigrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	walker := treesitter.NewWalker(treesitter.GoSpec)
	svc := &Service{Store: st, UnitOfWork: newTestUnitOfWork(db, nil), Walkers: map[string]Parser{".go": walker}, Logger: slog.Default()}

	tmpDir := t.TempDir()
	mustWrite := func(rel, content string) {
		full := filepath.Join(tmpDir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	mustWrite("go.mod", "module github.com/example/project\n\ngo 1.25.0\n")
	mustWrite("mainpkg/iface.go", "package mainpkg\n\ntype Writer interface {\n\tWrite([]byte) error\n}\n")

	ctx := context.Background()
	if _, err := svc.Build(ctx, BuildOptions{Dir: tmpDir}); err != nil {
		t.Fatalf("Build: %v", err)
	}

	mustWrite("mainpkg/impl.go", "package mainpkg\n\ntype FileWriter struct{}\n\nfunc (FileWriter) Write(data []byte) error {\n\treturn nil\n}\n")
	syncer := incremental.NewWithRegistry(st, map[string]incremental.Parser{".go": walker}, incremental.WithLogger(slog.Default()))
	if _, err := svc.Update(ctx, UpdateOptions{BuildOptions: BuildOptions{Dir: tmpDir}, Syncer: syncer}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	impl, err := st.GetNode(ctx, "mainpkg.FileWriter")
	if err != nil || impl == nil {
		t.Fatalf("GetNode impl: node=%v err=%v", impl, err)
	}
	iface, err := st.GetNode(ctx, "mainpkg.Writer")
	if err != nil || iface == nil {
		t.Fatalf("GetNode iface: node=%v err=%v", iface, err)
	}
	edges, err := st.GetEdgesFrom(ctx, impl.ID)
	if err != nil {
		t.Fatalf("GetEdgesFrom: %v", err)
	}
	for _, edge := range edges {
		if edge.Kind == graph.EdgeKindImplements && edge.ToNodeID == iface.ID {
			return
		}
	}
	t.Fatalf("expected update-time cross-file implements edge from %d to %d, got %+v", impl.ID, iface.ID, edges)
}

func TestUpdate_ReconcilesCrossBatchChangedCallEdge(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	st := graphgorm.New(db)
	if err := st.AutoMigrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	walker := treesitter.NewWalker(treesitter.GoSpec)
	svc := &Service{Store: st, UnitOfWork: newTestUnitOfWork(db, nil), Walkers: map[string]Parser{".go": walker}, Logger: slog.Default()}

	tmpDir := t.TempDir()
	mustWrite := func(rel, content string) {
		full := filepath.Join(tmpDir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	mustWrite("a_source.go", "package sample\n\nfunc Source() { Target() }\n")
	for i := range buildFlushFileBatchSize - 1 {
		mustWrite(fmt.Sprintf("m_filler_%03d.go", i), fmt.Sprintf("package sample\n\nfunc Filler%03d() {}\n", i))
	}
	mustWrite("z_target.go", "package sample\n\nfunc Target() {}\n")

	ctx := context.Background()
	if _, err := svc.Build(ctx, BuildOptions{Dir: tmpDir}); err != nil {
		t.Fatalf("Build: %v", err)
	}

	mustWrite("a_source.go", "package sample\n\nfunc Source() { Target() }\n\n// source changed\n")
	mustWrite("z_target.go", "package sample\n\nfunc Target() {}\n\n// target changed\n")
	syncer := incremental.NewWithRegistry(st, map[string]incremental.Parser{".go": walker}, incremental.WithLogger(slog.Default()))
	stats, err := svc.Update(ctx, UpdateOptions{BuildOptions: BuildOptions{Dir: tmpDir}, Syncer: syncer})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if stats.Modified != 2 {
		t.Fatalf("Modified = %d, want 2", stats.Modified)
	}

	source, err := st.GetNode(ctx, "sample.Source")
	if err != nil || source == nil {
		t.Fatalf("GetNode source: node=%v err=%v", source, err)
	}
	target, err := st.GetNode(ctx, "sample.Target")
	if err != nil || target == nil {
		t.Fatalf("GetNode target: node=%v err=%v", target, err)
	}
	edges, err := st.GetEdgesFrom(ctx, source.ID)
	if err != nil {
		t.Fatalf("GetEdgesFrom: %v", err)
	}
	for _, edge := range edges {
		if edge.Kind == graph.EdgeKindCalls && edge.ToNodeID == target.ID {
			return
		}
	}
	t.Fatalf("expected cross-batch call edge from %d to %d, got %+v", source.ID, target.ID, edges)
}

func TestUpdate_ReconcilesExistingCallerAfterTargetFileAdded(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	st := graphgorm.New(db)
	if err := st.AutoMigrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	walker := treesitter.NewWalker(treesitter.GoSpec)
	svc := &Service{Store: st, UnitOfWork: newTestUnitOfWork(db, nil), Walkers: map[string]Parser{".go": walker}, Logger: slog.Default()}

	tmpDir := t.TempDir()
	writeFile := func(rel, content string) {
		full := filepath.Join(tmpDir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	writeFile("source.go", "package sample\n\nfunc Source() { Target() }\n")

	ctx := context.Background()
	if _, err := svc.Build(ctx, BuildOptions{Dir: tmpDir}); err != nil {
		t.Fatalf("Build before target exists: %v", err)
	}
	writeFile("target.go", "package sample\n\nfunc Target() {}\n")

	syncer := incremental.NewWithRegistry(st, map[string]incremental.Parser{".go": walker}, incremental.WithLogger(slog.Default()))
	stats, err := svc.Update(ctx, UpdateOptions{BuildOptions: BuildOptions{Dir: tmpDir}, Syncer: syncer})
	if err != nil {
		t.Fatalf("Update after target add: %v", err)
	}
	if stats.Added != 1 {
		t.Fatalf("Added = %d, want one newly added target", stats.Added)
	}

	source, err := st.GetNode(ctx, "sample.Source")
	if err != nil || source == nil {
		t.Fatalf("GetNode source: node=%v err=%v", source, err)
	}
	target, err := st.GetNode(ctx, "sample.Target")
	if err != nil || target == nil {
		t.Fatalf("GetNode target: node=%v err=%v", target, err)
	}
	edges, err := st.GetEdgesFrom(ctx, source.ID)
	if err != nil {
		t.Fatalf("GetEdgesFrom: %v", err)
	}
	for _, edge := range edges {
		if edge.Kind == graph.EdgeKindCalls && edge.ToNodeID == target.ID {
			return
		}
	}
	t.Fatalf("expected existing caller edge from %d to newly added target %d, got %+v", source.ID, target.ID, edges)
}

func TestUpdate_RemovesStaleCrossFileGoStructuralImplements(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	st := graphgorm.New(db)
	if err := st.AutoMigrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	walker := treesitter.NewWalker(treesitter.GoSpec)
	svc := &Service{Store: st, UnitOfWork: newTestUnitOfWork(db, nil), Walkers: map[string]Parser{".go": walker}, Logger: slog.Default()}

	tmpDir := t.TempDir()
	mustWrite := func(rel, content string) {
		full := filepath.Join(tmpDir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	mustWrite("go.mod", "module github.com/example/project\n\ngo 1.25.0\n")
	mustWrite("mainpkg/iface.go", "package mainpkg\n\ntype Writer interface {\n\tWrite([]byte) error\n}\n")
	mustWrite("mainpkg/type.go", "package mainpkg\n\ntype FileWriter struct{}\n")
	mustWrite("mainpkg/write.go", "package mainpkg\n\nfunc (FileWriter) Write(data []byte) error {\n\treturn nil\n}\n")

	ctx := context.Background()
	if _, err := svc.Build(ctx, BuildOptions{Dir: tmpDir}); err != nil {
		t.Fatalf("Build: %v", err)
	}
	impl, err := st.GetNode(ctx, "mainpkg.FileWriter")
	if err != nil || impl == nil {
		t.Fatalf("GetNode impl after build: node=%v err=%v", impl, err)
	}
	iface, err := st.GetNode(ctx, "mainpkg.Writer")
	if err != nil || iface == nil {
		t.Fatalf("GetNode iface after build: node=%v err=%v", iface, err)
	}
	edges, err := st.GetEdgesFrom(ctx, impl.ID)
	if err != nil {
		t.Fatalf("GetEdgesFrom after build: %v", err)
	}
	found := false
	for _, edge := range edges {
		if edge.Kind == graph.EdgeKindImplements && edge.ToNodeID == iface.ID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected initial build-time cross-file implements edge, got %+v", edges)
	}

	if err := os.Remove(filepath.Join(tmpDir, "mainpkg", "write.go")); err != nil {
		t.Fatalf("remove write.go: %v", err)
	}
	syncer := incremental.NewWithRegistry(st, map[string]incremental.Parser{".go": walker}, incremental.WithLogger(slog.Default()))
	if _, err := svc.Update(ctx, UpdateOptions{BuildOptions: BuildOptions{Dir: tmpDir}, Syncer: syncer}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	impl, err = st.GetNode(ctx, "mainpkg.FileWriter")
	if err != nil || impl == nil {
		t.Fatalf("GetNode impl after update: node=%v err=%v", impl, err)
	}
	edges, err = st.GetEdgesFrom(ctx, impl.ID)
	if err != nil {
		t.Fatalf("GetEdgesFrom after update: %v", err)
	}
	for _, edge := range edges {
		if edge.Kind == graph.EdgeKindImplements && edge.ToNodeID == iface.ID {
			t.Fatalf("expected stale cross-file implements edge to be removed, got %+v", edges)
		}
	}
}

func TestUpdate_ReplacesLegacyInheritsFingerprintWithV2(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	st := graphgorm.New(db)
	if err := st.AutoMigrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	walker := treesitter.NewWalker(treesitter.PythonSpec)
	svc := &Service{Store: st, UnitOfWork: newTestUnitOfWork(db, nil), Walkers: map[string]Parser{".py": walker}, Logger: slog.Default()}

	tmpDir := t.TempDir()
	mustWrite := func(rel, content string) {
		full := filepath.Join(tmpDir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	mustWrite("models.py", "class Base:\n    pass\n\nclass Child(Base):\n    pass\n")

	ctx := context.Background()
	if _, err := svc.Build(ctx, BuildOptions{Dir: tmpDir}); err != nil {
		t.Fatalf("Build: %v", err)
	}
	child, err := st.GetNode(ctx, "Child")
	if err != nil || child == nil {
		t.Fatalf("GetNode child after build: node=%v err=%v", child, err)
	}
	base, err := st.GetNode(ctx, "Base")
	if err != nil || base == nil {
		t.Fatalf("GetNode base after build: node=%v err=%v", base, err)
	}

	legacy := graph.Edge{
		Namespace:   requestctx.DefaultNamespace,
		FromNodeID:  child.ID,
		ToNodeID:    base.ID,
		Kind:        graph.EdgeKindInherits,
		FilePath:    "models.py",
		Line:        3,
		Fingerprint: "inherits:models.py:Child:Base",
	}
	if err := db.Create(&legacy).Error; err != nil {
		t.Fatalf("seed legacy edge: %v", err)
	}
	mustWrite("models.py", "class Base:\n    note = 1\n\nclass Child(Base):\n    pass\n")

	syncer := incremental.NewWithRegistry(st, map[string]incremental.Parser{".py": walker}, incremental.WithLogger(slog.Default()))
	if _, err := svc.Update(ctx, UpdateOptions{BuildOptions: BuildOptions{Dir: tmpDir}, Syncer: syncer}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	child, err = st.GetNode(ctx, "Child")
	if err != nil || child == nil {
		t.Fatalf("GetNode child after update: node=%v err=%v", child, err)
	}
	edges, err := st.GetEdgesFrom(ctx, child.ID)
	if err != nil {
		t.Fatalf("GetEdgesFrom after update: %v", err)
	}
	var inherits []graph.Edge
	for _, edge := range edges {
		if edge.Kind == graph.EdgeKindInherits {
			inherits = append(inherits, edge)
		}
	}
	if len(inherits) != 1 {
		t.Fatalf("expected exactly one inherits edge after update, got %+v", inherits)
	}
	want := graph.BuildInheritsFingerprintV2("models.py", "Child", "Base")
	if inherits[0].Fingerprint != want {
		t.Fatalf("inherits fingerprint = %q, want %q", inherits[0].Fingerprint, want)
	}
	var legacyCount int64
	if err := db.Model(&graph.Edge{}).Where("namespace = ? AND fingerprint = ?", requestctx.DefaultNamespace, legacy.Fingerprint).Count(&legacyCount).Error; err != nil {
		t.Fatalf("count legacy edges: %v", err)
	}
	if legacyCount != 0 {
		t.Fatalf("expected legacy inherits fingerprint to be removed, found %d", legacyCount)
	}
}

func TestUpdate_RemovesStalePackageSemanticEdgesWhenAnchorChanges(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	st := graphgorm.New(db)
	if err := st.AutoMigrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	walker := treesitter.NewWalker(treesitter.GoSpec)
	svc := &Service{Store: st, UnitOfWork: newTestUnitOfWork(db, nil), Walkers: map[string]Parser{".go": walker}, Logger: slog.Default()}

	tmpDir := t.TempDir()
	mustWrite := func(rel, content string) {
		full := filepath.Join(tmpDir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	mustWrite("go.mod", "module github.com/example/project\n\ngo 1.25.0\n")
	mustWrite("mainpkg/b_iface.go", "package mainpkg\n\ntype Writer interface {\n\tWrite([]byte) error\n}\n")
	mustWrite("mainpkg/c_impl.go", "package mainpkg\n\ntype FileWriter struct{}\n\nfunc (FileWriter) Write(data []byte) error {\n\treturn nil\n}\n")

	ctx := context.Background()
	if _, err := svc.Build(ctx, BuildOptions{Dir: tmpDir}); err != nil {
		t.Fatalf("Build: %v", err)
	}

	impl, err := st.GetNode(ctx, "mainpkg.FileWriter")
	if err != nil || impl == nil {
		t.Fatalf("GetNode impl after build: node=%v err=%v", impl, err)
	}
	iface, err := st.GetNode(ctx, "mainpkg.Writer")
	if err != nil || iface == nil {
		t.Fatalf("GetNode iface after build: node=%v err=%v", iface, err)
	}
	edges, err := st.GetEdgesFrom(ctx, impl.ID)
	if err != nil {
		t.Fatalf("GetEdgesFrom after build: %v", err)
	}
	var initial []graph.Edge
	for _, edge := range edges {
		if edge.Kind == graph.EdgeKindImplements && edge.ToNodeID == iface.ID {
			initial = append(initial, edge)
		}
	}
	if len(initial) != 1 {
		t.Fatalf("expected exactly one initial package semantic edge, got %+v", initial)
	}
	if got, want := initial[0].FilePath, filepath.ToSlash(filepath.Join("mainpkg", "b_iface.go")); got != want {
		t.Fatalf("initial anchor mismatch: got %q want %q", got, want)
	}

	mustWrite("mainpkg/a_helper.go", "package mainpkg\n\ntype AnchorHelper struct{}\n")
	syncer := incremental.NewWithRegistry(st, map[string]incremental.Parser{".go": walker}, incremental.WithLogger(slog.Default()))
	if _, err := svc.Update(ctx, UpdateOptions{BuildOptions: BuildOptions{Dir: tmpDir}, Syncer: syncer}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	impl, err = st.GetNode(ctx, "mainpkg.FileWriter")
	if err != nil || impl == nil {
		t.Fatalf("GetNode impl after update: node=%v err=%v", impl, err)
	}
	iface, err = st.GetNode(ctx, "mainpkg.Writer")
	if err != nil || iface == nil {
		t.Fatalf("GetNode iface after update: node=%v err=%v", iface, err)
	}
	edges, err = st.GetEdgesFrom(ctx, impl.ID)
	if err != nil {
		t.Fatalf("GetEdgesFrom after update: %v", err)
	}
	var implEdges []graph.Edge
	for _, edge := range edges {
		if edge.Kind == graph.EdgeKindImplements && edge.ToNodeID == iface.ID {
			implEdges = append(implEdges, edge)
		}
	}
	if len(implEdges) != 1 {
		t.Fatalf("expected exactly one implements edge after anchor change, got %+v", implEdges)
	}
	if got, want := implEdges[0].FilePath, filepath.ToSlash(filepath.Join("mainpkg", "a_helper.go")); got != want {
		t.Fatalf("updated anchor mismatch: got %q want %q", got, want)
	}
}

func TestBuild_SuppressesRepoLocalPackageClauseCorrectionOnConflict(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	st := graphgorm.New(db)
	if err := st.AutoMigrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	svc := &Service{Store: st, UnitOfWork: newTestUnitOfWork(db, nil), Walkers: map[string]Parser{".go": treesitter.NewWalker(treesitter.GoSpec)}, Logger: slog.Default()}

	tmpDir := t.TempDir()
	mustMkdir := func(rel string) {
		if err := os.MkdirAll(filepath.Join(tmpDir, rel), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
	}
	mustWrite := func(rel, content string) {
		if err := os.WriteFile(filepath.Join(tmpDir, rel), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	mustWrite("go.mod", "module github.com/example/project\n\ngo 1.25.0\n")
	mustMkdir("internal/api")
	mustMkdir("mainpkg")
	mustWrite("internal/api/a.go", "package contracts\n\ntype Service interface {\n\tRun()\n}\n")
	mustWrite("internal/api/b.go", "package other\n\ntype Service interface {\n\tRun()\n}\n")
	mustWrite("mainpkg/main.go", "package mainpkg\n\nimport dep \"github.com/example/project/internal/api\"\n\ntype MyType struct{}\n\nfunc (MyType) Run() {}\n\nvar _ dep.Service = MyType{}\n")

	ctx := context.Background()
	if _, err := svc.Build(ctx, BuildOptions{Dir: tmpDir}); err != nil {
		t.Fatalf("Build: %v", err)
	}

	impl, err := st.GetNode(ctx, "mainpkg.MyType")
	if err != nil || impl == nil {
		t.Fatalf("GetNode impl: node=%v err=%v", impl, err)
	}
	iface, err := st.GetNode(ctx, "contracts.Service")
	if err != nil || iface == nil {
		t.Fatalf("GetNode iface: node=%v err=%v", iface, err)
	}
	otherIface, err := st.GetNode(ctx, "other.Service")
	if err != nil || otherIface == nil {
		t.Fatalf("GetNode other iface: node=%v err=%v", otherIface, err)
	}
	edges, err := st.GetEdgesFrom(ctx, impl.ID)
	if err != nil {
		t.Fatalf("GetEdgesFrom: %v", err)
	}
	for _, edge := range edges {
		if edge.Kind == graph.EdgeKindImplements && (edge.ToNodeID == iface.ID || edge.ToNodeID == otherIface.ID) {
			t.Fatalf("expected conflicting package clauses to suppress alias correction, got implements edge %+v", edge)
		}
	}
}

func TestBuild_ImportsFromTargetsPackageNodeForMultiFileGoPackage(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	st := graphgorm.New(db)
	if err := st.AutoMigrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	svc := &Service{Store: st, UnitOfWork: newTestUnitOfWork(db, nil), Walkers: map[string]Parser{".go": treesitter.NewWalker(treesitter.GoSpec)}, Logger: slog.Default()}

	tmpDir := t.TempDir()
	mustMkdir := func(rel string) {
		if err := os.MkdirAll(filepath.Join(tmpDir, rel), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
	}
	mustWrite := func(rel, content string) {
		if err := os.WriteFile(filepath.Join(tmpDir, rel), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	mustWrite("go.mod", "module github.com/example/project\n\ngo 1.25.0\n")
	mustMkdir("cmd")
	mustMkdir("internal/mcp")
	mustWrite("cmd/main.go", "package main\n\nimport \"github.com/example/project/internal/mcp\"\n\nfunc main() { mcp.Run() }\n")
	mustWrite("internal/mcp/a.go", "package mcp\n\nfunc Run() {}\n")
	mustWrite("internal/mcp/b.go", "package mcp\n\nfunc Other() {}\n")

	ctx := context.Background()
	if _, err := svc.Build(ctx, BuildOptions{Dir: tmpDir}); err != nil {
		t.Fatalf("Build: %v", err)
	}

	importer, err := st.GetNode(ctx, "cmd/main.go")
	if err != nil || importer == nil {
		t.Fatalf("GetNode importer: node=%v err=%v", importer, err)
	}
	pkgNode, err := st.GetNode(ctx, "github.com/example/project/internal/mcp")
	if err != nil || pkgNode == nil {
		t.Fatalf("GetNode package: node=%v err=%v", pkgNode, err)
	}
	if pkgNode.Kind != graph.NodeKindPackage {
		t.Fatalf("package node kind=%q, want %q", pkgNode.Kind, graph.NodeKindPackage)
	}

	qs := querypkg.New(graphgorm.New(db))
	imports, err := qs.ImportsOf(ctx, importer.ID)
	if err != nil {
		t.Fatalf("ImportsOf: %v", err)
	}
	if len(imports) != 1 || imports[0].ID != pkgNode.ID {
		t.Fatalf("expected imports_of to return package node %+v, got %+v", pkgNode, imports)
	}
	children, err := qs.ChildrenOf(ctx, pkgNode.ID)
	if err != nil {
		t.Fatalf("ChildrenOf: %v", err)
	}
	childPaths := make([]string, 0, len(children))
	for _, child := range children {
		childPaths = append(childPaths, child.FilePath)
	}
	slices.Sort(childPaths)
	if !slices.Equal(childPaths, []string{"internal/mcp/a.go", "internal/mcp/b.go"}) {
		t.Fatalf("expected package children to be mcp files, got %+v", children)
	}
}

func TestBuild_DiscoversPythonPackagesAndInheritsEdges(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	st := graphgorm.New(db)
	if err := st.AutoMigrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	svc := &Service{Store: st, UnitOfWork: newTestUnitOfWork(db, nil), Walkers: map[string]Parser{".py": treesitter.NewWalker(treesitter.PythonSpec)}, Logger: slog.Default()}

	tmpDir := t.TempDir()
	mustMkdir := func(rel string) {
		if err := os.MkdirAll(filepath.Join(tmpDir, rel), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
	}
	mustWrite := func(rel, content string) {
		if err := os.WriteFile(filepath.Join(tmpDir, rel), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	mustMkdir("pkg")
	mustWrite("pkg/__init__.py", "")
	mustWrite("pkg/base.py", "class Base:\n    pass\n")
	mustWrite("pkg/derived.py", "from .base import Base\n\nclass Derived(Base):\n    pass\n")

	ctx := context.Background()
	if _, err := svc.Build(ctx, BuildOptions{Dir: tmpDir}); err != nil {
		t.Fatalf("Build: %v", err)
	}

	pkgNode, err := st.GetNode(ctx, "pkg")
	if err != nil || pkgNode == nil {
		t.Fatalf("GetNode package: node=%v err=%v", pkgNode, err)
	}
	if pkgNode.Kind != graph.NodeKindPackage {
		t.Fatalf("package node kind=%q, want %q", pkgNode.Kind, graph.NodeKindPackage)
	}
	derived, err := st.GetNode(ctx, "Derived")
	if err != nil || derived == nil {
		t.Fatalf("GetNode derived: node=%v err=%v", derived, err)
	}
	edges, err := st.GetEdgesFrom(ctx, derived.ID)
	if err != nil {
		t.Fatalf("GetEdgesFrom: %v", err)
	}
	for _, edge := range edges {
		if edge.Kind != graph.EdgeKindInherits {
			continue
		}
		if edge.ToNodeID == 0 {
			continue
		}
		baseNode, err := st.GetNodeByID(ctx, edge.ToNodeID)
		if err == nil && baseNode != nil && baseNode.Name == "Base" {
			return
		}
	}
	t.Fatalf("expected inherits edge from Derived to Base, got %+v", edges)
}

func TestBuild_TypeScriptAliasImportTargetsPackageNode(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	st := graphgorm.New(db)
	if err := st.AutoMigrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	svc := &Service{Store: st, UnitOfWork: newTestUnitOfWork(db, nil), Walkers: map[string]Parser{".ts": treesitter.NewWalker(treesitter.TypeScriptSpec)}, Logger: slog.Default()}

	tmpDir := t.TempDir()
	mustMkdir := func(rel string) {
		if err := os.MkdirAll(filepath.Join(tmpDir, rel), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
	}
	mustWrite := func(rel, content string) {
		if err := os.WriteFile(filepath.Join(tmpDir, rel), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	mustMkdir("src/utils")
	mustMkdir("src/app")
	mustWrite("package.json", `{"name":"@acme/app"}`)
	mustWrite("tsconfig.json", `{"compilerOptions":{"baseUrl":".","paths":{"@app/*":["src/*"]}}}`)
	mustWrite("src/utils/math.ts", "export function add(a: number, b: number): number { return a + b; }\n")
	mustWrite("src/app/main.ts", "import { add } from '@app/utils';\nexport function run(): number { return add(1, 2); }\n")

	ctx := context.Background()
	if _, err := svc.Build(ctx, BuildOptions{Dir: tmpDir}); err != nil {
		t.Fatalf("Build: %v", err)
	}

	importer, err := st.GetNode(ctx, "src/app/main.ts")
	if err != nil || importer == nil {
		t.Fatalf("GetNode importer: node=%v err=%v", importer, err)
	}
	pkgNode, err := st.GetNode(ctx, "@app/utils")
	if err != nil || pkgNode == nil {
		t.Fatalf("GetNode package: node=%v err=%v", pkgNode, err)
	}
	qs := querypkg.New(graphgorm.New(db))
	imports, err := qs.ImportsOf(ctx, importer.ID)
	if err != nil {
		t.Fatalf("ImportsOf: %v", err)
	}
	if len(imports) != 1 || imports[0].ID != pkgNode.ID {
		t.Fatalf("expected imports_of to return alias package node %+v, got %+v", pkgNode, imports)
	}
}

func TestBuild_TypeScriptAliasFileImportTargetsPackageNode(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	st := graphgorm.New(db)
	if err := st.AutoMigrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	svc := &Service{Store: st, UnitOfWork: newTestUnitOfWork(db, nil), Walkers: map[string]Parser{".ts": treesitter.NewWalker(treesitter.TypeScriptSpec)}, Logger: slog.Default()}

	tmpDir := t.TempDir()
	mustMkdir := func(rel string) {
		if err := os.MkdirAll(filepath.Join(tmpDir, rel), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
	}
	mustWrite := func(rel, content string) {
		if err := os.WriteFile(filepath.Join(tmpDir, rel), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	mustMkdir("src/utils")
	mustMkdir("src/app")
	mustWrite("package.json", `{"name":"@acme/app"}`)
	mustWrite("tsconfig.json", "{\n  // comment\n  \"compilerOptions\": {\n    \"baseUrl\": \"src\",\n    \"paths\": {\n      \"@app/*\": [\"*\"]\n    }\n  }\n}\n")
	mustWrite("src/utils/math.ts", "export function add(a: number, b: number): number { return a + b; }\n")
	mustWrite("src/app/main.ts", "import { add } from '@app/utils/math';\nexport function run(): number { return add(1, 2); }\n")

	ctx := context.Background()
	if _, err := svc.Build(ctx, BuildOptions{Dir: tmpDir}); err != nil {
		t.Fatalf("Build: %v", err)
	}

	importer, err := st.GetNode(ctx, "src/app/main.ts")
	if err != nil || importer == nil {
		t.Fatalf("GetNode importer: node=%v err=%v", importer, err)
	}
	pkgNode, err := st.GetNode(ctx, "@app/utils/math")
	if err != nil || pkgNode == nil {
		t.Fatalf("GetNode package: node=%v err=%v", pkgNode, err)
	}
	qs := querypkg.New(graphgorm.New(db))
	imports, err := qs.ImportsOf(ctx, importer.ID)
	if err != nil {
		t.Fatalf("ImportsOf: %v", err)
	}
	if len(imports) != 1 || imports[0].ID != pkgNode.ID {
		t.Fatalf("expected imports_of to return alias file package node %+v, got %+v", pkgNode, imports)
	}
}

func TestBuild_TypeScriptFunctionQualifiedNameUsesFilePackageContext(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	st := graphgorm.New(db)
	if err := st.AutoMigrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	svc := &Service{Store: st, UnitOfWork: newTestUnitOfWork(db, nil), Walkers: map[string]Parser{".ts": treesitter.NewWalker(treesitter.TypeScriptSpec)}, Logger: slog.Default()}

	tmpDir := t.TempDir()
	mustMkdir := func(rel string) {
		if err := os.MkdirAll(filepath.Join(tmpDir, rel), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
	}
	mustWrite := func(rel, content string) {
		if err := os.WriteFile(filepath.Join(tmpDir, rel), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	mustMkdir("src/utils")
	mustWrite("package.json", `{"name":"@acme/app"}`)
	mustWrite("tsconfig.json", `{"compilerOptions":{"baseUrl":".","paths":{"@app/*":["src/*"]}}}`)
	mustWrite("src/utils/math.ts", "export function add(a: number, b: number): number { return a + b; }\n")

	ctx := context.Background()
	if _, err := svc.Build(ctx, BuildOptions{Dir: tmpDir}); err != nil {
		t.Fatalf("Build: %v", err)
	}

	node, err := st.GetNode(ctx, "@acme/app/src/utils.add")
	if err != nil || node == nil {
		t.Fatalf("GetNode function: node=%v err=%v", node, err)
	}
	if node.Kind != graph.NodeKindFunction {
		t.Fatalf("function node kind=%q, want %q", node.Kind, graph.NodeKindFunction)
	}
}

func TestBuild_TypeScriptClassQualifiedNameUsesFilePackageContext(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	st := graphgorm.New(db)
	if err := st.AutoMigrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	svc := &Service{Store: st, UnitOfWork: newTestUnitOfWork(db, nil), Walkers: map[string]Parser{".ts": treesitter.NewWalker(treesitter.TypeScriptSpec)}, Logger: slog.Default()}

	tmpDir := t.TempDir()
	mustMkdir := func(rel string) {
		if err := os.MkdirAll(filepath.Join(tmpDir, rel), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
	}
	mustWrite := func(rel, content string) {
		if err := os.WriteFile(filepath.Join(tmpDir, rel), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	mustMkdir("src/utils")
	mustWrite("package.json", `{"name":"@acme/app"}`)
	mustWrite("tsconfig.json", `{"compilerOptions":{"baseUrl":".","paths":{"@app/*":["src/*"]}}}`)
	mustWrite("src/utils/math.ts", "export class Calculator {}\n")

	ctx := context.Background()
	if _, err := svc.Build(ctx, BuildOptions{Dir: tmpDir}); err != nil {
		t.Fatalf("Build: %v", err)
	}

	node, err := st.GetNode(ctx, "@acme/app/src/utils.Calculator")
	if err != nil || node == nil {
		t.Fatalf("GetNode class: node=%v err=%v", node, err)
	}
	if node.Kind != graph.NodeKindClass {
		t.Fatalf("class node kind=%q, want %q", node.Kind, graph.NodeKindClass)
	}
}

func TestBuild_TypeScriptClassMethodQualifiedNameUsesFilePackageContext(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	st := graphgorm.New(db)
	if err := st.AutoMigrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	svc := &Service{Store: st, UnitOfWork: newTestUnitOfWork(db, nil), Walkers: map[string]Parser{".ts": treesitter.NewWalker(treesitter.TypeScriptSpec)}, Logger: slog.Default()}

	tmpDir := t.TempDir()
	mustMkdir := func(rel string) {
		if err := os.MkdirAll(filepath.Join(tmpDir, rel), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
	}
	mustWrite := func(rel, content string) {
		if err := os.WriteFile(filepath.Join(tmpDir, rel), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	mustMkdir("src/utils")
	mustWrite("package.json", `{"name":"@acme/app"}`)
	mustWrite("tsconfig.json", `{"compilerOptions":{"baseUrl":".","paths":{"@app/*":["src/*"]}}}`)
	mustWrite("src/utils/math.ts", "export class Calculator { run(): number { return 1; } }\n")

	ctx := context.Background()
	if _, err := svc.Build(ctx, BuildOptions{Dir: tmpDir}); err != nil {
		t.Fatalf("Build: %v", err)
	}

	node, err := st.GetNode(ctx, "@acme/app/src/utils.Calculator.run")
	if err != nil || node == nil {
		t.Fatalf("GetNode method: node=%v err=%v", node, err)
	}
	if node.Kind != graph.NodeKindFunction {
		t.Fatalf("method node kind=%q, want %q", node.Kind, graph.NodeKindFunction)
	}
}

func TestBuild_TypeScriptSameFileHeritageUsesQualifiedNames(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	st := graphgorm.New(db)
	if err := st.AutoMigrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	svc := &Service{Store: st, UnitOfWork: newTestUnitOfWork(db, nil), Walkers: map[string]Parser{".ts": treesitter.NewWalker(treesitter.TypeScriptSpec)}, Logger: slog.Default()}

	tmpDir := t.TempDir()
	mustMkdir := func(rel string) {
		if err := os.MkdirAll(filepath.Join(tmpDir, rel), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
	}
	mustWrite := func(rel, content string) {
		if err := os.WriteFile(filepath.Join(tmpDir, rel), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	mustMkdir("src/models")
	mustWrite("package.json", `{"name":"@acme/app"}`)
	mustWrite("tsconfig.json", `{"compilerOptions":{"baseUrl":".","paths":{"@app/*":["src/*"]}}}`)
	mustWrite("src/models/user.ts", "export interface Authenticated {}\nexport class Base {}\nexport class User extends Base implements Authenticated {}\n")

	ctx := context.Background()
	if _, err := svc.Build(ctx, BuildOptions{Dir: tmpDir}); err != nil {
		t.Fatalf("Build: %v", err)
	}

	user, err := st.GetNode(ctx, "@acme/app/src/models.User")
	if err != nil || user == nil {
		t.Fatalf("GetNode user: node=%v err=%v", user, err)
	}
	edges, err := st.GetEdgesFrom(ctx, user.ID)
	if err != nil {
		t.Fatalf("GetEdgesFrom: %v", err)
	}

	var foundInherits, foundImplements bool
	for _, edge := range edges {
		switch edge.Kind {
		case graph.EdgeKindInherits:
			if edge.ToNodeID == 0 {
				continue
			}
			baseNode, err := st.GetNodeByID(ctx, edge.ToNodeID)
			if err == nil && baseNode != nil && baseNode.QualifiedName == "@acme/app/src/models.Base" {
				foundInherits = true
			}
		case graph.EdgeKindImplements:
			if edge.ToNodeID == 0 {
				continue
			}
			ifaceNode, err := st.GetNodeByID(ctx, edge.ToNodeID)
			if err == nil && ifaceNode != nil && ifaceNode.QualifiedName == "@acme/app/src/models.Authenticated" {
				foundImplements = true
			}
		}
	}
	if !foundInherits || !foundImplements {
		t.Fatalf("expected qualified inherits and implements edges, got %+v", edges)
	}
}

func TestBuild_TypeScriptImportedHeritageUsesQualifiedNames(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	st := graphgorm.New(db)
	if err := st.AutoMigrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	svc := &Service{Store: st, UnitOfWork: newTestUnitOfWork(db, nil), Walkers: map[string]Parser{".ts": treesitter.NewWalker(treesitter.TypeScriptSpec)}, Logger: slog.Default()}

	tmpDir := t.TempDir()
	mustMkdir := func(rel string) {
		if err := os.MkdirAll(filepath.Join(tmpDir, rel), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
	}
	mustWrite := func(rel, content string) {
		if err := os.WriteFile(filepath.Join(tmpDir, rel), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	mustMkdir("src/base")
	mustMkdir("src/contracts")
	mustMkdir("src/models")
	mustWrite("package.json", `{"name":"@acme/app"}`)
	mustWrite("tsconfig.json", `{"compilerOptions":{"baseUrl":".","paths":{"@app/*":["src/*"]}}}`)
	mustWrite("src/base/index.ts", "export class Base {}\n")
	mustWrite("src/contracts/index.ts", "export interface Authenticated {}\n")
	mustWrite("src/models/user.ts", "import { Base } from '@app/base';\nimport { Authenticated } from '@app/contracts';\nexport class User extends Base implements Authenticated {}\n")

	ctx := context.Background()
	if _, err := svc.Build(ctx, BuildOptions{Dir: tmpDir}); err != nil {
		t.Fatalf("Build: %v", err)
	}

	var edges []graph.Edge
	if err := db.Where("file_path = ?", "src/models/user.ts").Find(&edges).Error; err != nil {
		t.Fatalf("load raw edges: %v", err)
	}
	var foundInherits, foundImplements bool
	for _, edge := range edges {
		switch edge.Kind {
		case graph.EdgeKindInherits:
			child, parent, ok := graph.ParseInheritsFingerprint("src/models/user.ts", edge.Fingerprint)
			if ok && child == "@acme/app/src/models.User" && parent == "@acme/app/src/base.Base" {
				foundInherits = true
			}
		case graph.EdgeKindImplements:
			if edge.Fingerprint == "implements:src/models/user.ts:@acme/app/src/models.User:@acme/app/src/contracts.Authenticated" {
				foundImplements = true
			}
		}
	}
	if !foundInherits || !foundImplements {
		t.Fatalf("expected qualified imported raw heritage edges, got %+v", edges)
	}
}

func TestPrepareBuildSpool_TypeScriptImportedHeritageEdgesPresent(t *testing.T) {
	svc := &Service{Walkers: map[string]Parser{".ts": treesitter.NewWalker(treesitter.TypeScriptSpec)}, Logger: slog.Default()}

	tmpDir := t.TempDir()
	mustMkdir := func(rel string) {
		if err := os.MkdirAll(filepath.Join(tmpDir, rel), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
	}
	mustWrite := func(rel, content string) {
		if err := os.WriteFile(filepath.Join(tmpDir, rel), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	mustMkdir("src/base")
	mustMkdir("src/contracts")
	mustMkdir("src/models")
	mustWrite("package.json", `{"name":"@acme/app"}`)
	mustWrite("tsconfig.json", `{"compilerOptions":{"baseUrl":".","paths":{"@app/*":["src/*"]}}}`)
	mustWrite("src/base/index.ts", "export class Base {}\n")
	mustWrite("src/contracts/index.ts", "export interface Authenticated {}\n")
	mustWrite("src/models/user.ts", "import { Base } from '@app/base';\nimport { Authenticated } from '@app/contracts';\nexport class User extends Base implements Authenticated {}\n")

	ctx := context.Background()
	packages := svc.collectLanguagePackages(ctx, tmpDir, BuildOptions{Dir: tmpDir})
	ctx = svc.withImportPackageContext(ctx, packages)

	spool, err := svc.prepareBuildSpool(ctx, tmpDir, BuildOptions{Dir: tmpDir})
	if err != nil {
		t.Fatalf("prepareBuildSpool: %v", err)
	}
	defer spool.cleanup(slog.Default())

	var record spooledBuildRecord
	foundRecord := false
	for _, recordPath := range spool.records {
		got, err := spool.readRecord(recordPath)
		if err != nil {
			t.Fatalf("readRecord(%s): %v", recordPath, err)
		}
		if got.RelPath == "src/models/user.ts" {
			record = got
			foundRecord = true
			break
		}
	}
	if !foundRecord {
		t.Fatalf("expected spool record for src/models/user.ts, got %v", spool.records)
	}

	var foundInherits, foundImplements bool
	for _, edge := range record.Edges {
		switch edge.Kind {
		case graph.EdgeKindInherits:
			child, parent, ok := graph.ParseInheritsFingerprint("src/models/user.ts", edge.Fingerprint)
			if ok && child == "@acme/app/src/models.User" && parent == "@acme/app/src/base.Base" {
				foundInherits = true
			}
		case graph.EdgeKindImplements:
			if edge.Fingerprint == "implements:src/models/user.ts:@acme/app/src/models.User:@acme/app/src/contracts.Authenticated" {
				foundImplements = true
			}
		}
	}
	if !foundInherits || !foundImplements {
		t.Fatalf("expected qualified imported heritage edges in spool record, got %+v", record.Edges)
	}
}

func TestPrepareBuildSpool_ParsesFilesConcurrentlyAndKeepsOrder(t *testing.T) {
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	released := false
	defer func() {
		if !released {
			close(release)
		}
	}()

	svc := &Service{Parsers: map[string]Parser{
		".stub": blockingBuildParser{started: started, release: release},
	}}
	tmpDir := t.TempDir()
	for _, relPath := range []string{"a.stub", "b.stub"} {
		if err := os.WriteFile(filepath.Join(tmpDir, relPath), []byte(relPath), 0o644); err != nil {
			t.Fatalf("write %s: %v", relPath, err)
		}
	}

	type prepareResult struct {
		spool *buildSpool
		err   error
	}
	resultCh := make(chan prepareResult, 1)
	go func() {
		spool, err := svc.prepareBuildSpool(context.Background(), tmpDir, BuildOptions{Dir: tmpDir})
		resultCh <- prepareResult{spool: spool, err: err}
	}()

	for i := 0; i < 2; i++ {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("expected two files to begin parsing concurrently")
		}
	}
	close(release)
	released = true

	result := <-resultCh
	if result.err != nil {
		t.Fatalf("prepareBuildSpool: %v", result.err)
	}
	defer result.spool.cleanup(slog.Default())
	if len(result.spool.records) != 2 {
		t.Fatalf("spool records = %d, want 2", len(result.spool.records))
	}
	for i, want := range []string{"a.stub", "b.stub"} {
		record, err := result.spool.readRecord(result.spool.records[i])
		if err != nil {
			t.Fatalf("read record %d: %v", i, err)
		}
		if record.RelPath != want {
			t.Fatalf("record %d path = %q, want %q", i, record.RelPath, want)
		}
	}
}

func TestImportPackageContext_TypeScriptAliasUsesCanonicalImportPath(t *testing.T) {
	packages := map[string]languagePackageInfo{
		"@acme/app/src/base": {
			ImportPath: "@acme/app/src/base",
			Name:       "base",
			Dir:        "src/base",
			Language:   "typescript",
			Files:      []string{"src/base/index.ts"},
		},
		"@app/base": {
			ImportPath: "@app/base",
			Name:       "base",
			Dir:        "src/base",
			Language:   "typescript",
			Files:      []string{"src/base/index.ts"},
		},
	}

	got := importPackageContext(packages)
	if got["@app/base"] != "@acme/app/src/base" {
		t.Fatalf("importPackageContext()[@app/base] = %q, want %q", got["@app/base"], "@acme/app/src/base")
	}
}

func TestUpdate_TypeScriptFunctionQualifiedNameUsesFilePackageContext(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	st := graphgorm.New(db)
	if err := st.AutoMigrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	walker := treesitter.NewWalker(treesitter.TypeScriptSpec)
	svc := &Service{Store: st, UnitOfWork: newTestUnitOfWork(db, nil), Walkers: map[string]Parser{".ts": walker}, Logger: slog.Default()}
	syncer := incremental.NewWithRegistry(st, map[string]incremental.Parser{".ts": walker}, incremental.WithLogger(slog.Default()))

	tmpDir := t.TempDir()
	mustMkdir := func(rel string) {
		if err := os.MkdirAll(filepath.Join(tmpDir, rel), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
	}
	mustWrite := func(rel, content string) {
		if err := os.WriteFile(filepath.Join(tmpDir, rel), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	mustMkdir("src/utils")
	mustWrite("package.json", `{"name":"@acme/app"}`)
	mustWrite("tsconfig.json", `{"compilerOptions":{"baseUrl":".","paths":{"@app/*":["src/*"]}}}`)
	mustWrite("src/utils/math.ts", "export function add(a: number, b: number): number { return a + b; }\n")

	ctx := context.Background()
	if _, err := svc.Build(ctx, BuildOptions{Dir: tmpDir}); err != nil {
		t.Fatalf("Build: %v", err)
	}

	mustWrite("src/utils/math.ts", "export function subtract(a: number, b: number): number { return a - b; }\n")
	if _, err := svc.Update(ctx, UpdateOptions{BuildOptions: BuildOptions{Dir: tmpDir}, Syncer: syncer}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	node, err := st.GetNode(ctx, "@acme/app/src/utils.subtract")
	if err != nil || node == nil {
		t.Fatalf("GetNode function: node=%v err=%v", node, err)
	}
	if node.Kind != graph.NodeKindFunction {
		t.Fatalf("function node kind=%q, want %q", node.Kind, graph.NodeKindFunction)
	}
	oldNode, err := st.GetNode(ctx, "@acme/app/src/utils.add")
	if err != nil {
		t.Fatalf("GetNode old function: %v", err)
	}
	if oldNode != nil {
		t.Fatalf("expected old qualified function node removed, got %+v", oldNode)
	}
}

func TestBuild_JavaImportedSuperclassResolvesAcrossPackages(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	st := graphgorm.New(db)
	if err := st.AutoMigrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	svc := &Service{Store: st, UnitOfWork: newTestUnitOfWork(db, nil), Walkers: map[string]Parser{".java": treesitter.NewWalker(treesitter.JavaSpec)}, Logger: slog.Default()}

	tmpDir := t.TempDir()
	mustMkdir := func(rel string) {
		if err := os.MkdirAll(filepath.Join(tmpDir, rel), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
	}
	mustWrite := func(rel, content string) {
		if err := os.WriteFile(filepath.Join(tmpDir, rel), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	mustMkdir("src/main/java/com/example/base")
	mustMkdir("src/main/java/com/example/auth")
	mustWrite("src/main/java/com/example/base/Base.java", "package com.example.base;\npublic class Base {}\n")
	mustWrite("src/main/java/com/example/auth/User.java", "package com.example.auth;\nimport com.example.base.Base;\npublic class User extends Base {}\n")

	ctx := context.Background()
	if _, err := svc.Build(ctx, BuildOptions{Dir: tmpDir}); err != nil {
		t.Fatalf("Build: %v", err)
	}
	user, err := st.GetNode(ctx, "com.example.auth.User")
	if err != nil || user == nil {
		t.Fatalf("GetNode user: node=%v err=%v", user, err)
	}
	base, err := st.GetNode(ctx, "com.example.base.Base")
	if err != nil || base == nil {
		t.Fatalf("GetNode base: node=%v err=%v", base, err)
	}
	edges, err := st.GetEdgesFrom(ctx, user.ID)
	if err != nil {
		t.Fatalf("GetEdgesFrom: %v", err)
	}
	for _, edge := range edges {
		if edge.Kind == graph.EdgeKindInherits && edge.ToNodeID == base.ID {
			return
		}
	}
	t.Fatalf("expected inherited edge from User to imported Base, got %+v", edges)
}

func TestPackageNodesPreservePackageLanguage(t *testing.T) {
	nodes := packageNodes(map[string]languagePackageInfo{
		"example/pkg": {
			ImportPath: "example/pkg",
			Name:       "pkg",
			Dir:        "pkg",
			Language:   "examplelang",
			Files:      []string{"pkg/a.example"},
		},
	})
	if len(nodes) != 1 {
		t.Fatalf("expected one package node, got %+v", nodes)
	}
	if nodes[0].Language != "examplelang" {
		t.Fatalf("expected package language to be preserved, got %q", nodes[0].Language)
	}
}

func TestNewParsedBuildNodeBatch_DropsRawContentAndOnlyBuildsSourceLinesWhenNeeded(t *testing.T) {
	typ := reflect.TypeFor[parsedBuildNodeBatch]()
	if _, ok := typ.FieldByName("content"); ok {
		t.Fatal("parsedBuildNodeBatch must not retain raw content")
	}

	noComments := newParsedBuildNodeBatch("sample.go", []byte("package sample\nfunc Keep() {}\n"), nil, "", nil, nil, "")
	if noComments.sourceLines != nil {
		t.Fatalf("expected no sourceLines without comments, got %#v", noComments.sourceLines)
	}

	withComments := newParsedBuildNodeBatch(
		"sample.go",
		[]byte("package sample\n// hello\nfunc Keep() {}\n"),
		nil,
		"",
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
	st := graphgorm.New(db)
	if err := st.AutoMigrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	svc := &Service{
		Store:      st,
		UnitOfWork: newTestUnitOfWork(db, nil),
		Walkers:    map[string]Parser{".py": treesitter.NewWalker(treesitter.PythonSpec)},
		Logger:     slog.Default(),
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
	st := graphgorm.New(db)
	if err := st.AutoMigrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	svc := &Service{
		Store:      st,
		UnitOfWork: newTestUnitOfWork(db, nil),
		Walkers:    map[string]Parser{".go": treesitter.NewWalker(treesitter.GoSpec)},
		Logger:     slog.Default(),
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
	svc := &Service{
		Store:      fakeStore,
		UnitOfWork: testUnitOfWork{graph: fakeStore},
		Walkers: map[string]Parser{
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

	want := []string{"DeleteGraph", "UpsertNodes", "GetNodesByFiles", "UpsertAnnotation", "UpsertEdges"}
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

func TestFlushBuildBatch_BatchesNodesAndAnnotationLookups(t *testing.T) {
	fakeStore := newRecordingGraphStore(t)
	released := 0
	svc := &Service{
		onBatchRelease: func([]parsedBuildNodeBatch, int) {
			released++
		},
	}
	batch := buildPersistBatch{
		files: 2,
		nodeBatches: []parsedBuildNodeBatch{
			{
				relPath: "first.go",
				nodes: []graph.Node{{
					QualifiedName: "sample.First",
					FilePath:      "first.go",
					Kind:          graph.NodeKindFunction,
					StartLine:     2,
					EndLine:       2,
				}},
				tsComments:  []ingest.CommentBlock{{StartLine: 1, EndLine: 1, Text: "@intent first"}},
				language:    "go",
				sourceLines: []string{"// @intent first", "func First() {}"},
			},
			{
				relPath: "second.go",
				nodes: []graph.Node{{
					QualifiedName: "sample.Second",
					FilePath:      "second.go",
					Kind:          graph.NodeKindFunction,
					StartLine:     2,
					EndLine:       2,
				}},
				tsComments:  []ingest.CommentBlock{{StartLine: 1, EndLine: 1, Text: "@intent second"}},
				language:    "go",
				sourceLines: []string{"// @intent second", "func Second() {}"},
			},
		},
	}

	if err := svc.flushBuildBatch(context.Background(), fakeStore, &batch); err != nil {
		t.Fatalf("flushBuildBatch: %v", err)
	}
	if got := len(fakeStore.upsertedNodeBatches); got != 1 {
		t.Fatalf("node upsert calls = %d, want 1 (ops=%v)", got, fakeStore.ops)
	}
	if got := len(fakeStore.upsertedNodeBatches[0]); got != 2 {
		t.Fatalf("batched node count = %d, want 2", got)
	}
	countOperation := func(want string) int {
		count := 0
		for _, op := range fakeStore.ops {
			if op == want {
				count++
			}
		}
		return count
	}
	if got := countOperation("GetNodesByFiles"); got != 1 {
		t.Fatalf("bulk annotation node lookups = %d, want 1 (ops=%v)", got, fakeStore.ops)
	}
	if got := countOperation("GetNodesByFile"); got != 0 {
		t.Fatalf("per-file annotation node lookups = %d, want 0 (ops=%v)", got, fakeStore.ops)
	}
	if got := countOperation("UpsertAnnotation"); got != 2 {
		t.Fatalf("annotation upserts = %d, want 2 (ops=%v)", got, fakeStore.ops)
	}
	if released != 2 {
		t.Fatalf("released batches = %d, want 2", released)
	}
	if batch.files != 0 || len(batch.nodeBatches) != 0 {
		t.Fatalf("batch was not reset: %+v", batch)
	}
}

func TestBuild_FlushesLargeBuildInBoundedBatches(t *testing.T) {
	fakeStore := newRecordingGraphStore(t)
	svc := &Service{
		Store:      fakeStore,
		UnitOfWork: testUnitOfWork{graph: fakeStore},
		Walkers: map[string]Parser{
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
	if stats.Timing.TotalMS <= 0 {
		t.Fatalf("Timing.TotalMS = %d, want a positive duration", stats.Timing.TotalMS)
	}
	if stats.Timing.ParseMS < 0 || stats.Timing.PersistNodesMS < 0 || stats.Timing.ResolveEdgesMS < 0 || stats.Timing.SearchRebuildMS < 0 {
		t.Fatalf("build stage timings must be non-negative: %+v", stats.Timing)
	}

	edgeFlushes := 0
	for _, op := range fakeStore.ops {
		if op == "UpsertEdges" {
			edgeFlushes++
		}
	}
	if edgeFlushes == 0 {
		t.Fatalf("expected at least 1 edge flush, got %d (ops=%v)", edgeFlushes, fakeStore.ops)
	}
	for _, batch := range fakeStore.upsertedEdges {
		if len(batch) > buildEdgeResolveChunkSize {
			t.Fatalf("edge batch exceeded limit: got %d want <= %d", len(batch), buildEdgeResolveChunkSize)
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
		t.Fatalf("expected deferred edge flush after all annotations, got ops=%v", fakeStore.ops)
	}
}

func TestBuildCompletionLogsTimingOnlyAtDebugLevel(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "source.go"), []byte("package sample\n\nfunc Source() {}\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	newService := func(logger *slog.Logger) *Service {
		store := newRecordingGraphStore(t)
		return &Service{
			Store:      store,
			UnitOfWork: testUnitOfWork{graph: store},
			Walkers: map[string]Parser{
				".go": treesitter.NewWalker(treesitter.GoSpec),
			},
			Logger: logger,
		}
	}
	timingKeys := []string{
		"parse_ms=",
		"persist_nodes_ms=",
		"resolve_edges_ms=",
		"resolver_calls=",
		"resolver_ms=",
		"resolve_nodes_by_ids_calls=",
		"resolve_nodes_by_ids_ms=",
		"resolve_nodes_by_files_calls=",
		"resolve_nodes_by_files_ms=",
		"resolve_nodes_by_qn_calls=",
		"resolve_nodes_by_qn_ms=",
		"resolve_import_file_nodes_calls=",
		"resolve_import_file_nodes_ms=",
		"resolve_edges_to_nodes_calls=",
		"resolve_edges_to_nodes_ms=",
		"resolve_edge_upsert_calls=",
		"resolve_edge_upsert_ms=",
		"search_rebuild_ms=",
		"total_ms=",
	}

	var infoLogs bytes.Buffer
	if _, err := newService(slog.New(slog.NewTextHandler(&infoLogs, &slog.HandlerOptions{Level: slog.LevelInfo}))).Build(context.Background(), BuildOptions{Dir: dir}); err != nil {
		t.Fatalf("info Build: %v", err)
	}
	infoOutput := infoLogs.String()
	if !strings.Contains(infoOutput, `msg="build complete"`) || !strings.Contains(infoOutput, "files=1") {
		t.Fatalf("expected info completion summary, got %q", infoOutput)
	}
	if strings.Contains(infoOutput, `msg="build timing"`) {
		t.Fatalf("info output must not contain debug timing event, got %q", infoOutput)
	}
	for _, key := range timingKeys {
		if strings.Contains(infoOutput, key) {
			t.Fatalf("info completion log must omit %q, got %q", key, infoOutput)
		}
	}

	var debugLogs bytes.Buffer
	stats, err := newService(slog.New(slog.NewTextHandler(&debugLogs, &slog.HandlerOptions{Level: slog.LevelDebug}))).Build(context.Background(), BuildOptions{Dir: dir})
	if err != nil {
		t.Fatalf("debug Build: %v", err)
	}
	if stats.Timing.TotalMS < 0 {
		t.Fatalf("Timing.TotalMS = %d, want non-negative", stats.Timing.TotalMS)
	}
	debugOutput := debugLogs.String()
	if !strings.Contains(debugOutput, `msg="build timing"`) {
		t.Fatalf("expected debug timing event, got %q", debugOutput)
	}
	for _, key := range timingKeys {
		if !strings.Contains(debugOutput, key) {
			t.Fatalf("debug timing log must contain %q, got %q", key, debugOutput)
		}
	}
}

func TestBuild_ReleasesBatchCommentStateAfterBinding(t *testing.T) {
	var snapshots []struct {
		batch         int
		tsCommentsNil bool
		sourceNil     bool
	}
	recordRelease := func(batches []parsedBuildNodeBatch, idx int) {
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

	fakeStore := newRecordingGraphStore(t)
	svc := &Service{
		Store:      fakeStore,
		UnitOfWork: testUnitOfWork{graph: fakeStore},
		Walkers: map[string]Parser{
			".go": treesitter.NewWalker(treesitter.GoSpec),
		},
		Logger:         slog.Default(),
		onBatchRelease: recordRelease,
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
	st := graphgorm.New(db)
	if err := st.AutoMigrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	svc := &Service{
		Store:      st,
		UnitOfWork: newTestUnitOfWork(db, nil),
		Walkers:    map[string]Parser{".go": treesitter.NewWalker(treesitter.GoSpec)},
		Logger:     slog.Default(),
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
	if err := st.UpsertEdges(ctx, []graph.Edge{{
		FromNodeID:  handlerNode.ID,
		ToNodeID:    helperNode.ID,
		Kind:        graph.EdgeKindCalls,
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
	if err := db.Model(&graph.Edge{}).Where("fingerprint = ?", "calls:api.Handler:other.Helper").Count(&manualEdges).Error; err != nil {
		t.Fatalf("count manual edges: %v", err)
	}
	if manualEdges != 0 {
		t.Fatalf("expected manual cross-file edge to be removed with excluded file scope, got %d", manualEdges)
	}
}

func TestBuild_ResolvesCallEdgesForTraceFlow(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	st := graphgorm.New(db)
	if err := st.AutoMigrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	svc := &Service{
		Store:      st,
		UnitOfWork: newTestUnitOfWork(db, nil),
		Walkers:    map[string]Parser{".go": treesitter.NewWalker(treesitter.GoSpec)},
		Logger:     slog.Default(),
	}

	tmpDir := t.TempDir()
	src := `package flows

type Tracer struct{}

func (t *Tracer) TraceFlow() {
	t.TraceFlowBounded()
}

func (t *Tracer) TraceFlowBounded() {}
`
	if err := os.WriteFile(filepath.Join(tmpDir, "flows.go"), []byte(src), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	ctx := context.Background()
	if _, err := svc.Build(ctx, BuildOptions{Dir: tmpDir}); err != nil {
		t.Fatalf("Build: %v", err)
	}

	start, err := st.GetNode(ctx, "flows.Tracer.TraceFlow")
	if err != nil || start == nil {
		t.Fatalf("get TraceFlow node: node=%v err=%v", start, err)
	}
	var resolved int64
	if err := db.Model(&graph.Edge{}).
		Where("kind = ? AND from_node_id = ? AND to_node_id <> 0", graph.EdgeKindCalls, start.ID).
		Count(&resolved).Error; err != nil {
		t.Fatalf("count resolved edge: %v", err)
	}
	if resolved != 1 {
		t.Fatalf("resolved calls from TraceFlow=%d, want 1", resolved)
	}

	flow, err := flows.New(st).TraceFlow(ctx, start.ID)
	if err != nil {
		t.Fatalf("TraceFlow: %v", err)
	}
	if len(flow.Members) != 2 {
		t.Fatalf("flow members=%d, want 2", len(flow.Members))
	}
}

func TestBuild_ResolvesGoInterfaceDispatchForTraceFlow(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	st := graphgorm.New(db)
	if err := st.AutoMigrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	svc := &Service{
		Store:      st,
		UnitOfWork: newTestUnitOfWork(db, nil),
		Walkers:    map[string]Parser{".go": treesitter.NewWalker(treesitter.GoSpec)},
		Logger:     slog.Default(),
	}

	tmpDir := t.TempDir()
	mustMkdir := func(path string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Join(tmpDir, path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", path, err)
		}
	}
	mustWrite := func(path, content string) {
		t.Helper()
		full := filepath.Join(tmpDir, path)
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	mustMkdir("mcp")
	mustMkdir("flows")
	mustMkdir("cmd")
	mustWrite("mcp/deps.go", `package mcp

type FlowTracer interface {
	TraceFlow()
}
`)
	mustWrite("flows/flows.go", `package flows

type Tracer struct{}

func (t *Tracer) TraceFlow() {
	t.TraceFlowBounded()
}

func (t *Tracer) TraceFlowBounded() {}
`)
	mustWrite("cmd/main.go", `package main

import (
	mcpserver "github.com/example/project/mcp"
	"github.com/example/project/flows"
)

var _ mcpserver.FlowTracer = (*flows.Tracer)(nil)

type handler struct {
	FlowTracer mcpserver.FlowTracer
}

func (h *handler) Start() {
	h.FlowTracer.TraceFlow()
}
`)

	ctx := context.Background()
	if _, err := svc.Build(ctx, BuildOptions{Dir: tmpDir}); err != nil {
		t.Fatalf("Build: %v", err)
	}
	start, err := st.GetNode(ctx, "main.handler.Start")
	if err != nil || start == nil {
		t.Fatalf("get Start node: node=%v err=%v", start, err)
	}
	flow, err := flows.New(st).TraceFlow(ctx, start.ID)
	if err != nil {
		t.Fatalf("TraceFlow: %v", err)
	}
	if len(flow.Members) != 3 {
		t.Fatalf("flow members=%d, want 3", len(flow.Members))
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
	st := graphgorm.New(db)
	if err := st.AutoMigrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	svc := &Service{
		Store:      st,
		UnitOfWork: newTestUnitOfWork(db, nil),
		Walkers:    map[string]Parser{".go": treesitter.NewWalker(treesitter.GoSpec)},
		Logger:     slog.Default(),
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
	st := graphgorm.New(db)
	if err := st.AutoMigrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	svc := &Service{
		Store:      st,
		UnitOfWork: newTestUnitOfWork(db, nil),
		Walkers:    map[string]Parser{".go": treesitter.NewWalker(treesitter.GoSpec)},
		Logger:     slog.Default(),
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
	st := graphgorm.New(db)
	if err := st.AutoMigrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	svc := &Service{
		Store:      st,
		UnitOfWork: newTestUnitOfWork(db, nil),
		Walkers:    map[string]Parser{".go": treesitter.NewWalker(treesitter.GoSpec)},
		Logger:     slog.Default(),
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
	st := graphgorm.New(db)
	if err := st.AutoMigrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	svc := &Service{
		Store:      st,
		UnitOfWork: newTestUnitOfWork(db, nil),
		Walkers:    map[string]Parser{".go": treesitter.NewWalker(treesitter.GoSpec)},
		Logger:     slog.Default(),
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
	st := graphgorm.New(db)
	if err := st.AutoMigrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	svc := &Service{
		Store:      st,
		UnitOfWork: newTestUnitOfWork(db, nil),
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
	if err := db.AutoMigrate(&graph.Node{}); err != nil {
		t.Fatalf("migrate nodes: %v", err)
	}

	svc := &Service{
		UnitOfWork: newTestUnitOfWork(db, nil),
		Walkers:    map[string]Parser{".go": treesitter.NewWalker(treesitter.GoSpec)},
		Logger:     slog.Default(),
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
	if err := db.AutoMigrate(&graph.Node{}); err != nil {
		t.Fatalf("migrate nodes: %v", err)
	}

	svc := &Service{
		UnitOfWork: newTestUnitOfWork(db, nil),
		Walkers:    map[string]Parser{".go": treesitter.NewWalker(treesitter.GoSpec)},
		Logger:     slog.Default(),
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
	if err := db.AutoMigrate(&graph.Node{}); err != nil {
		t.Fatalf("migrate nodes: %v", err)
	}

	svc := &Service{
		UnitOfWork: newTestUnitOfWork(db, nil),
		Walkers:    map[string]Parser{".go": treesitter.NewWalker(treesitter.GoSpec)},
		Logger:     slog.Default(),
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

func TestUpdateGraphWithoutTx_DeletesEvenWithNoNormalBatches(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&graph.Node{}); err != nil {
		t.Fatalf("migrate nodes: %v", err)
	}
	if err := db.Create(&graph.Node{Namespace: requestctx.DefaultNamespace, FilePath: "gone.go"}).Error; err != nil {
		t.Fatalf("seed node: %v", err)
	}

	svc := &Service{
		Store:      graphgorm.New(db),
		UnitOfWork: newTestUnitOfWork(db, nil),
		Walkers:    map[string]Parser{".go": treesitter.NewWalker(treesitter.GoSpec)},
		Logger:     slog.Default(),
	}

	// Empty working dir: nothing to parse, so there are no normal batches. The previously
	// stored gone.go must still be deleted (regression: the delete pass was gated on a normal
	// batch having run).
	tmpDir := t.TempDir()
	syncer := &recordingIncrementalSyncer{result: &incremental.SyncStats{}}
	if _, err := svc.Update(context.Background(), UpdateOptions{BuildOptions: BuildOptions{Dir: tmpDir, SkipSearchRebuild: true}, Syncer: syncer}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if len(syncer.calls) != 1 {
		t.Fatalf("expected a single delete pass, got %d calls: %v", len(syncer.calls), syncer.calls)
	}
	if got, want := syncer.calls[0].existingFiles, []string{"gone.go"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("delete pass existingFiles = %v, want %v", got, want)
	}
}

func TestUpdate_IncludePaths_FiltersExistingFilesWhenReplaceFalse(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&graph.Node{}, &graph.Edge{}); err != nil {
		t.Fatalf("migrate nodes: %v", err)
	}
	// Both seeded files are missing from disk. handler.go is inside the include path and must be
	// deleted; helper.go is outside it and must be scoped out of the existing-file set entirely.
	if err := db.Create(&graph.Node{Namespace: requestctx.DefaultNamespace, FilePath: filepath.Join("src", "api", "handler.go")}).Error; err != nil {
		t.Fatalf("seed api node: %v", err)
	}
	if err := db.Create(&graph.Node{Namespace: requestctx.DefaultNamespace, FilePath: filepath.Join("src", "other", "helper.go")}).Error; err != nil {
		t.Fatalf("seed other node: %v", err)
	}

	svc := &Service{
		Store:      graphgorm.New(db),
		UnitOfWork: newTestUnitOfWork(db, nil),
		Walkers:    map[string]Parser{".go": treesitter.NewWalker(treesitter.GoSpec)},
		Logger:     slog.Default(),
	}

	tmpDir := t.TempDir()
	apiDir := filepath.Join(tmpDir, "src", "api")
	if err := os.MkdirAll(apiDir, 0o755); err != nil {
		t.Fatalf("mkdir api: %v", err)
	}
	// A new file under the include path so the update has something to parse; the seeded
	// handler.go is absent from disk and should be detected as a deletion.
	if err := os.WriteFile(filepath.Join(apiDir, "new.go"), []byte("package api\n\nfunc New() {}\n"), 0o644); err != nil {
		t.Fatalf("write new: %v", err)
	}

	syncer := &recordingIncrementalSyncer{result: &incremental.SyncStats{}}
	_, err = svc.Update(context.Background(), UpdateOptions{BuildOptions: BuildOptions{Dir: tmpDir, IncludePaths: []string{filepath.Join("src", "api")}}, Syncer: syncer, Replace: false})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	var deleteCall *recordingSyncCall
	for i := range syncer.calls {
		if len(syncer.calls[i].files) == 0 && len(syncer.calls[i].existingFiles) > 0 {
			deleteCall = &syncer.calls[i]
		}
	}
	if deleteCall == nil {
		t.Fatalf("expected a delete pass for the missing include-path file, calls=%v", syncer.calls)
	}
	if got, want := deleteCall.existingFiles, []string{filepath.Join("src", "api", "handler.go")}; !reflect.DeepEqual(got, want) {
		t.Fatalf("delete pass existingFiles mismatch: got=%v want=%v (helper.go outside include path must be scoped out)", got, want)
	}
}

func TestUpdate_ExcludePatterns_LeavesMatchingFilesOutOfSync(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&graph.Node{}); err != nil {
		t.Fatalf("migrate nodes: %v", err)
	}

	svc := &Service{
		UnitOfWork: newTestUnitOfWork(db, nil),
		Walkers:    map[string]Parser{".go": treesitter.NewWalker(treesitter.GoSpec)},
		Logger:     slog.Default(),
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
	if err := db.AutoMigrate(&graph.Node{}); err != nil {
		t.Fatalf("migrate nodes: %v", err)
	}

	svc := &Service{
		UnitOfWork: newTestUnitOfWork(db, nil),
		Walkers:    map[string]Parser{".go": treesitter.NewWalker(treesitter.GoSpec)},
		Logger:     slog.Default(),
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
	if err := db.AutoMigrate(&graph.Node{}, &graph.Edge{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	st := graphgorm.New(db)
	ctx := context.Background()
	source := graph.Node{Namespace: requestctx.DefaultNamespace, QualifiedName: "pkg.Source", Kind: graph.NodeKindFunction, Name: "Source", FilePath: "source.go", StartLine: 1, EndLine: 2, Hash: "same", Language: "go"}
	target := graph.Node{Namespace: requestctx.DefaultNamespace, QualifiedName: "pkg.Target", Kind: graph.NodeKindFunction, Name: "Target", FilePath: "target.go", StartLine: 1, EndLine: 2, Hash: "old", Language: "go"}
	if err := db.Create(&source).Error; err != nil {
		t.Fatalf("seed source: %v", err)
	}
	if err := db.Create(&target).Error; err != nil {
		t.Fatalf("seed target: %v", err)
	}
	if err := db.Create(&graph.Edge{Namespace: requestctx.DefaultNamespace, FromNodeID: source.ID, ToNodeID: target.ID, Kind: graph.EdgeKindCalls, FilePath: "source.go", Fingerprint: "source-target"}).Error; err != nil {
		t.Fatalf("seed edge: %v", err)
	}

	_, nodesByFile, err := existingGraphFileState(ctx, st)
	if err != nil {
		t.Fatalf("existing state: %v", err)
	}
	forceFiles, err := forceReparseFiles(ctx, st, nodesByFile, map[string]string{
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

func TestForceReparseFiles_IncludesUnchangedInterfaceDispatchCallSite(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&graph.Node{}, &graph.Edge{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	st := graphgorm.New(db)
	ctx := context.Background()
	callSite := graph.Node{Namespace: requestctx.DefaultNamespace, QualifiedName: "main.Run", Kind: graph.NodeKindFunction, Name: "Run", FilePath: "main.go", StartLine: 10, EndLine: 20, Hash: "same", Language: "go"}
	iface := graph.Node{Namespace: requestctx.DefaultNamespace, QualifiedName: "mcp.FlowTracer", Kind: graph.NodeKindType, Name: "FlowTracer", FilePath: "mcp.go", StartLine: 3, EndLine: 5, Hash: "old", Language: "go"}
	impl := graph.Node{Namespace: requestctx.DefaultNamespace, QualifiedName: "flows.Tracer", Kind: graph.NodeKindClass, Name: "Tracer", FilePath: "flows.go", StartLine: 3, EndLine: 3, Hash: "same", Language: "go"}
	for _, node := range []*graph.Node{&callSite, &iface, &impl} {
		if err := db.Create(node).Error; err != nil {
			t.Fatalf("seed node: %v", err)
		}
	}
	if err := db.Create(&graph.Edge{Namespace: requestctx.DefaultNamespace, FromNodeID: impl.ID, ToNodeID: iface.ID, Kind: graph.EdgeKindImplements, FilePath: "main.go", Fingerprint: "implements:main.go:flows.Tracer:mcp.FlowTracer"}).Error; err != nil {
		t.Fatalf("seed implements edge: %v", err)
	}
	if err := db.Create(&graph.Edge{Namespace: requestctx.DefaultNamespace, FromNodeID: callSite.ID, ToNodeID: 0, Kind: graph.EdgeKindCalls, FilePath: "main.go", Fingerprint: "calls:main.go:h.deps.FlowTracer.TraceFlow:12"}).Error; err != nil {
		t.Fatalf("seed call edge: %v", err)
	}

	_, nodesByFile, err := existingGraphFileState(ctx, st)
	if err != nil {
		t.Fatalf("existing state: %v", err)
	}
	forceFiles, err := forceReparseFiles(ctx, st, nodesByFile, map[string]string{
		"main.go":  "same",
		"mcp.go":   "new",
		"flows.go": "same",
	})
	if err != nil {
		t.Fatalf("force files: %v", err)
	}
	if _, ok := forceFiles["main.go"]; !ok {
		t.Fatalf("expected unchanged interface dispatch call site to be forced, got %v", forceFiles)
	}
	if _, ok := forceFiles["mcp.go"]; ok {
		t.Fatalf("did not expect changed interface file to be forced, got %v", forceFiles)
	}
}

func TestExistingGraphFileState_LoadsOnlyForceReparseFields(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&graph.Node{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	st := graphgorm.New(db)
	seed := graph.Node{
		Namespace:     requestctx.DefaultNamespace,
		QualifiedName: "pkg.Keep",
		Kind:          graph.NodeKindFunction,
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

	files, nodesByFile, err := existingGraphFileState(context.Background(), st)
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
	if err := db.AutoMigrate(&graph.Node{}, &graph.Edge{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	st := graphgorm.New(db)
	ctx := context.Background()
	existingNodesByFile := make(map[string][]graph.Node)
	currentHashes := map[string]string{"source.go": "same"}
	var firstChangedID uint
	for i := range forceReparseEdgeChunkSize + 1 {
		filePath := fmt.Sprintf("target-%03d.go", i)
		node := graph.Node{Namespace: requestctx.DefaultNamespace, FilePath: filePath, Hash: "old"}
		if err := db.Create(&node).Error; err != nil {
			t.Fatalf("seed changed node %d: %v", i, err)
		}
		if i == 0 {
			firstChangedID = node.ID
		}
		existingNodesByFile[filePath] = []graph.Node{{ID: node.ID, FilePath: filePath, Hash: "old"}}
		currentHashes[filePath] = "new"
	}
	source := graph.Node{Namespace: requestctx.DefaultNamespace, FilePath: "source.go", Hash: "same"}
	if err := db.Create(&source).Error; err != nil {
		t.Fatalf("seed source: %v", err)
	}
	existingNodesByFile["source.go"] = []graph.Node{{ID: source.ID, FilePath: "source.go", Hash: "same"}}
	if err := db.Create(&graph.Edge{Namespace: requestctx.DefaultNamespace, FromNodeID: source.ID, ToNodeID: firstChangedID, Kind: graph.EdgeKindCalls, FilePath: "source.go", Fingerprint: "source-target"}).Error; err != nil {
		t.Fatalf("seed edge: %v", err)
	}

	forceFiles, err := forceReparseFiles(ctx, st, existingNodesByFile, currentHashes)
	if err != nil {
		t.Fatalf("force files: %v", err)
	}
	if _, ok := forceFiles["source.go"]; !ok {
		t.Fatalf("expected unchanged source.go to be forced across chunks, got %v", forceFiles)
	}
}

func TestUpdateGraphWithoutTx_DoesNotDeleteForcedFilesDuringNormalSync(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&graph.Node{}, &graph.Edge{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	ctx := context.Background()

	sourceHash := sha256.Sum256([]byte("package sample\n\nfunc Source() { Target() }\n"))
	staleTargetHash := sha256.Sum256([]byte("package sample\n\nfunc Target() {}\n"))
	deletedHash := sha256.Sum256([]byte("package sample\n\nfunc Deleted() {}\n"))

	source := graph.Node{Namespace: requestctx.DefaultNamespace, QualifiedName: "sample.Source", Kind: graph.NodeKindFunction, Name: "Source", FilePath: "source.go", StartLine: 3, EndLine: 3, Hash: hex.EncodeToString(sourceHash[:]), Language: "go"}
	target := graph.Node{Namespace: requestctx.DefaultNamespace, QualifiedName: "sample.Target", Kind: graph.NodeKindFunction, Name: "Target", FilePath: "target.go", StartLine: 3, EndLine: 3, Hash: hex.EncodeToString(staleTargetHash[:]), Language: "go"}
	deleted := graph.Node{Namespace: requestctx.DefaultNamespace, QualifiedName: "sample.Deleted", Kind: graph.NodeKindFunction, Name: "Deleted", FilePath: "deleted.go", StartLine: 3, EndLine: 3, Hash: hex.EncodeToString(deletedHash[:]), Language: "go"}
	for _, node := range []*graph.Node{&source, &target, &deleted} {
		if err := db.Create(node).Error; err != nil {
			t.Fatalf("seed node %s: %v", node.FilePath, err)
		}
	}
	if err := db.Create(&graph.Edge{Namespace: requestctx.DefaultNamespace, FromNodeID: source.ID, ToNodeID: target.ID, Kind: graph.EdgeKindCalls, FilePath: "source.go", Fingerprint: "calls:source.go:Target:3"}).Error; err != nil {
		t.Fatalf("seed edge: %v", err)
	}

	svc := &Service{
		Store:      graphgorm.New(db),
		UnitOfWork: newTestUnitOfWork(db, nil),
		Walkers:    map[string]Parser{".go": treesitter.NewWalker(treesitter.GoSpec)},
		Logger:     slog.Default(),
	}

	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, "source.go"), []byte("package sample\n\nfunc Source() { Target() }\n"), 0o644); err != nil {
		t.Fatalf("write source.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "target.go"), []byte("package sample\n\nfunc Target() { Source() }\n"), 0o644); err != nil {
		t.Fatalf("write target.go: %v", err)
	}

	syncer := &recordingIncrementalSyncer{result: &incremental.SyncStats{}}
	if _, err := svc.Update(ctx, UpdateOptions{BuildOptions: BuildOptions{Dir: tmpDir, SkipSearchRebuild: true}, Syncer: syncer}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got := len(syncer.calls); got != 3 {
		t.Fatalf("expected normal sync, delete sync, and forced sync, got %d calls", got)
	}
	first := syncer.calls[0]
	if _, ok := first.files["source.go"]; ok {
		t.Fatalf("expected forced source.go to be excluded from normal sync files, got %v", sortedIncrementalFileKeys(first.files))
	}
	if _, ok := first.files["target.go"]; !ok {
		t.Fatalf("expected changed target.go in normal sync files, got %v", sortedIncrementalFileKeys(first.files))
	}
	// Normal sync passes no existingFiles: deletions are handled by the separate delete pass,
	// so a normal batch can never delete files that belong to other batches (mirrors the tx path).
	if len(first.existingFiles) != 0 {
		t.Fatalf("expected normal sync to pass no existingFiles, got %v", first.existingFiles)
	}
	second := syncer.calls[1]
	if len(second.files) != 0 {
		t.Fatalf("expected delete sync to receive no files, got %v", sortedIncrementalFileKeys(second.files))
	}
	if len(second.existingFiles) != 1 || second.existingFiles[0] != "deleted.go" {
		t.Fatalf("expected delete sync to receive only deleted.go, got %v", second.existingFiles)
	}
	third := syncer.calls[2]
	if _, ok := third.files["source.go"]; !ok {
		t.Fatalf("expected forced sync to include source.go, got %v", sortedIncrementalFileKeys(third.files))
	}
	if len(third.existingFiles) != 0 {
		t.Fatalf("expected forced sync to receive nil existingFiles, got %v", third.existingFiles)
	}
}

func TestUpdateGraphWithoutTx_ReplaysSpoolBatchesWithoutLoadingAllFilesIntoOneSyncCall(t *testing.T) {
	ctx := context.Background()
	svc := &Service{
		Walkers: map[string]Parser{".go": treesitter.NewWalker(treesitter.GoSpec)},
		Logger:  slog.Default(),
	}
	tmpDir := t.TempDir()
	for i := range buildFlushFileBatchSize + 1 {
		name := fmt.Sprintf("file-%03d.go", i)
		content := fmt.Sprintf("package sample\n\nfunc F%03d() {}\n", i)
		if err := os.WriteFile(filepath.Join(tmpDir, name), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	syncer := &recordingIncrementalSyncer{result: &incremental.SyncStats{}}
	if _, err := svc.Update(ctx, UpdateOptions{BuildOptions: BuildOptions{Dir: tmpDir, SkipSearchRebuild: true}, Syncer: syncer}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got := len(syncer.calls); got != 2 {
		t.Fatalf("expected two non-transactional sync batches, got %d", got)
	}
	if got := len(syncer.calls[0].files); got != buildFlushFileBatchSize {
		t.Fatalf("first batch files = %d, want %d", got, buildFlushFileBatchSize)
	}
	if got := len(syncer.calls[1].files); got != 1 {
		t.Fatalf("second batch files = %d, want 1", got)
	}
	if len(syncer.calls[0].existingFiles) != 0 {
		t.Fatalf("first batch existingFiles = %v, want nil", syncer.calls[0].existingFiles)
	}
	if len(syncer.calls[1].existingFiles) != 0 {
		t.Fatalf("second batch existingFiles = %v, want nil", syncer.calls[1].existingFiles)
	}
}

func TestCurrentNodeIDsForFiles_ChunksLargePathScopes(t *testing.T) {
	capture := &serviceINQueryCaptureLogger{Interface: gormlogger.Discard, needle: "file_path"}
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: capture})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&graph.Node{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	st := graphgorm.New(db)
	filePaths := make([]string, 0, scopedINQueryChunkSize+1)
	for i := range scopedINQueryChunkSize + 1 {
		filePath := fmt.Sprintf("file-%03d.go", i)
		filePaths = append(filePaths, filePath)
		node := graph.Node{Namespace: requestctx.DefaultNamespace, QualifiedName: fmt.Sprintf("pkg.Node%d", i), Kind: graph.NodeKindFunction, Name: fmt.Sprintf("Node%d", i), FilePath: filePath, StartLine: 1, EndLine: 1, Language: "go"}
		if err := db.Create(&node).Error; err != nil {
			t.Fatalf("create node %d: %v", i, err)
		}
	}

	ids, err := currentNodeIDsForFiles(context.Background(), st, filePaths)
	if err != nil {
		t.Fatalf("current node ids: %v", err)
	}
	if len(ids) != len(filePaths) {
		t.Fatalf("expected %d ids, got %d", len(filePaths), len(ids))
	}
	if capture.maxIDs > scopedINQueryChunkSize {
		t.Fatalf("expected file_path IN queries to be chunked to <= %d paths, got %d", scopedINQueryChunkSize, capture.maxIDs)
	}
	if capture.hits < 2 {
		t.Fatalf("expected multiple file_path IN queries, got %d", capture.hits)
	}
}

func TestUpdate_SearchRefreshIsScopedToAffectedNodes(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:?_pragma=journal_mode(WAL)"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	st := graphgorm.New(db)
	if err := st.AutoMigrate(); err != nil {
		t.Fatalf("migrate store: %v", err)
	}
	if err := db.AutoMigrate(&graph.SearchDocument{}); err != nil {
		t.Fatalf("migrate search docs: %v", err)
	}

	changed := graph.Node{Namespace: requestctx.DefaultNamespace, QualifiedName: "pkg.Changed", Kind: graph.NodeKindFunction, Name: "Changed", FilePath: "changed.stub", StartLine: 1, EndLine: 1, Hash: "old", Language: "stub"}
	untouched := graph.Node{Namespace: requestctx.DefaultNamespace, QualifiedName: "pkg.Untouched", Kind: graph.NodeKindFunction, Name: "Untouched", FilePath: "untouched.stub", StartLine: 1, EndLine: 1, Hash: "same", Language: "stub"}
	for _, node := range []*graph.Node{&changed, &untouched} {
		if err := db.Create(node).Error; err != nil {
			t.Fatalf("create node: %v", err)
		}
	}
	if err := db.Create(&graph.SearchDocument{Namespace: requestctx.DefaultNamespace, NodeID: changed.ID, Content: "stale changed", Language: "stub"}).Error; err != nil {
		t.Fatalf("seed changed doc: %v", err)
	}
	if err := db.Create(&graph.SearchDocument{Namespace: requestctx.DefaultNamespace, NodeID: untouched.ID, Content: "keep untouched", Language: "stub"}).Error; err != nil {
		t.Fatalf("seed untouched doc: %v", err)
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "changed.stub"), []byte("new"), 0o644); err != nil {
		t.Fatalf("write changed: %v", err)
	}
	if err := db.Model(&graph.Node{}).Where("id = ?", changed.ID).Update("hash", "old").Error; err != nil {
		t.Fatalf("reset changed hash: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "untouched.stub"), []byte("same"), 0o644); err != nil {
		t.Fatalf("write untouched: %v", err)
	}
	untouchedHash := sha256.Sum256([]byte("same"))
	if err := db.Model(&graph.Node{}).Where("id = ?", untouched.ID).Update("hash", hex.EncodeToString(untouchedHash[:])).Error; err != nil {
		t.Fatalf("update untouched hash: %v", err)
	}
	backend := &scopedSearchBackendSpy{}
	svc := &Service{
		Store:      st,
		UnitOfWork: newTestUnitOfWork(db, backend),
		Parsers:    map[string]Parser{".stub": failingBuildParser{}},
		Logger:     slog.Default(),
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

	changedHash := sha256.Sum256([]byte("new"))
	var newChanged graph.Node
	if err := db.Where("file_path = ? AND hash = ?", "changed.stub", hex.EncodeToString(changedHash[:])).First(&newChanged).Error; err != nil {
		t.Fatalf("load new changed node: %v", err)
	}
	if slices.Contains(backend.nodeIDs, untouched.ID) {
		t.Fatalf("expected untouched node not to be scoped, got %v", backend.nodeIDs)
	}
	if !slices.Contains(backend.nodeIDs, changed.ID) || !slices.Contains(backend.nodeIDs, newChanged.ID) {
		t.Fatalf("expected old and new changed node ids in scope, got %v old=%d new=%d", backend.nodeIDs, changed.ID, newChanged.ID)
	}

	var untouchedDoc graph.SearchDocument
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
	st := graphgorm.New(db)
	if err := st.AutoMigrate(); err != nil {
		t.Fatalf("migrate store: %v", err)
	}
	if err := db.AutoMigrate(&graph.SearchDocument{}); err != nil {
		t.Fatalf("migrate search docs: %v", err)
	}
	node := graph.Node{Namespace: requestctx.DefaultNamespace, QualifiedName: "pkg.Keep", Kind: graph.NodeKindFunction, Name: "Keep", FilePath: "keep.stub", StartLine: 1, EndLine: 1, Hash: "same", Language: "stub"}
	if err := db.Create(&node).Error; err != nil {
		t.Fatalf("create node: %v", err)
	}
	if err := db.Create(&graph.SearchDocument{Namespace: requestctx.DefaultNamespace, NodeID: node.ID, Content: "keep doc", Language: "stub"}).Error; err != nil {
		t.Fatalf("seed doc: %v", err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "keep.stub"), []byte("same"), 0o644); err != nil {
		t.Fatalf("write keep: %v", err)
	}
	keepHash := sha256.Sum256([]byte("same"))
	if err := db.Model(&graph.Node{}).Where("id = ?", node.ID).Update("hash", hex.EncodeToString(keepHash[:])).Error; err != nil {
		t.Fatalf("update keep hash: %v", err)
	}
	backend := &scopedSearchBackendSpy{}
	svc := &Service{
		Store:      st,
		UnitOfWork: newTestUnitOfWork(db, backend),
		Parsers:    map[string]Parser{".stub": failingBuildParser{}},
		Logger:     slog.Default(),
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
	st := graphgorm.New(db)
	if err := st.AutoMigrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	svc := &Service{
		Store:      st,
		UnitOfWork: newTestUnitOfWork(db, nil),
		Walkers:    map[string]Parser{".go": treesitter.NewWalker(treesitter.GoSpec)},
		Logger:     slog.Default(),
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
	st := graphgorm.New(db)
	if err := st.AutoMigrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := db.AutoMigrate(&graph.SearchDocument{}); err != nil {
		t.Fatalf("migrate search docs: %v", err)
	}
	backend := searchsql.NewSQLiteBackend()
	if err := backend.Migrate(db); err != nil {
		if errors.Is(err, searchsql.ErrFTS5NotAvailable) {
			t.Skip("fts5 module not available, skipping test")
		}
		t.Fatalf("migrate fts: %v", err)
	}

	otherNode := graph.Node{Namespace: "ns-b", QualifiedName: "pkg.Other", Kind: graph.NodeKindFunction, Name: "Other", FilePath: "other.go", StartLine: 1, EndLine: 2, Language: "go"}
	if err := db.Create(&otherNode).Error; err != nil {
		t.Fatalf("seed node: %v", err)
	}
	if err := db.Create(&graph.SearchDocument{Namespace: "ns-b", NodeID: otherNode.ID, Content: "other namespace doc", Language: "go"}).Error; err != nil {
		t.Fatalf("seed doc: %v", err)
	}

	svc := &Service{
		Store:      st,
		UnitOfWork: newTestUnitOfWork(db, backend),
		Walkers:    map[string]Parser{".go": treesitter.NewWalker(treesitter.GoSpec)},
		Logger:     slog.Default(),
	}

	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, "sample.go"), []byte("package sample\n\nfunc Keep() {}\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	ctx := requestctx.WithNamespace(context.Background(), "ns-a")
	if _, err := svc.Build(ctx, BuildOptions{Dir: tmpDir}); err != nil {
		t.Fatalf("build: %v", err)
	}

	var count int64
	if err := db.Model(&graph.SearchDocument{}).Where("namespace = ?", "ns-b").Count(&count).Error; err != nil {
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
func (f *failSearchBackend) Query(ctx context.Context, db *gorm.DB, query string, limit int) ([]graph.Node, error) {
	return nil, nil
}

func TestBuild_PropagatesSearchBackendRebuildError(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:?_pragma=journal_mode(WAL)"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	st := graphgorm.New(db)
	if err := st.AutoMigrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := db.AutoMigrate(&graph.SearchDocument{}); err != nil {
		t.Fatalf("migrate search docs: %v", err)
	}

	seedNode := graph.Node{Namespace: requestctx.DefaultNamespace, QualifiedName: "pkg.Seed", Kind: graph.NodeKindFunction, Name: "Seed", FilePath: "seed.go", StartLine: 1, EndLine: 2, Language: "go"}
	if err := db.Create(&seedNode).Error; err != nil {
		t.Fatalf("seed node: %v", err)
	}
	if err := db.Create(&graph.SearchDocument{Namespace: requestctx.DefaultNamespace, NodeID: seedNode.ID, Content: "seed searchable", Language: "go"}).Error; err != nil {
		t.Fatalf("seed search doc: %v", err)
	}

	svc := &Service{
		Store:      st,
		UnitOfWork: newTestUnitOfWork(db, &failSearchBackend{err: errors.New("fts rebuild boom")}),
		Walkers:    map[string]Parser{".go": treesitter.NewWalker(treesitter.GoSpec)},
		Logger:     slog.Default(),
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
	if err := db.Model(&graph.Node{}).Where("qualified_name = ?", "pkg.Seed").Count(&keptSeed).Error; err != nil {
		t.Fatalf("count seed node: %v", err)
	}
	if err := db.Model(&graph.Node{}).Where("qualified_name = ?", "sample.Keep").Count(&createdKeep).Error; err != nil {
		t.Fatalf("count new node: %v", err)
	}
	if keptSeed != 1 || createdKeep != 0 {
		t.Fatalf("expected graph rollback after search rebuild failure, seed=%d new=%d", keptSeed, createdKeep)
	}

	var docCount int64
	if err := db.Model(&graph.SearchDocument{}).Where("content = ?", "seed searchable").Count(&docCount).Error; err != nil {
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
	st := graphgorm.New(db)
	if err := st.AutoMigrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := db.AutoMigrate(&graph.SearchDocument{}); err != nil {
		t.Fatalf("migrate search docs: %v", err)
	}

	seedNode := graph.Node{Namespace: requestctx.DefaultNamespace, QualifiedName: "pkg.Seed", Kind: graph.NodeKindFunction, Name: "Seed", FilePath: "seed.go", StartLine: 1, EndLine: 2, Language: "go"}
	if err := db.Create(&seedNode).Error; err != nil {
		t.Fatalf("seed node: %v", err)
	}
	if err := db.Create(&graph.SearchDocument{Namespace: requestctx.DefaultNamespace, NodeID: seedNode.ID, Content: "seed searchable", Language: "go"}).Error; err != nil {
		t.Fatalf("seed search doc: %v", err)
	}
	if err := db.Exec("CREATE TRIGGER fail_search_docs_insert BEFORE INSERT ON search_documents BEGIN SELECT RAISE(ABORT, 'search doc boom'); END;").Error; err != nil {
		t.Fatalf("create trigger: %v", err)
	}

	svc := &Service{
		Store:      st,
		UnitOfWork: newTestUnitOfWork(db, &failSearchBackend{}),
		Walkers:    map[string]Parser{".go": treesitter.NewWalker(treesitter.GoSpec)},
		Logger:     slog.Default(),
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
	if err := db.Model(&graph.Node{}).Where("qualified_name = ?", "pkg.Seed").Count(&keptSeed).Error; err != nil {
		t.Fatalf("count seed node: %v", err)
	}
	if err := db.Model(&graph.Node{}).Where("qualified_name = ?", "sample.Keep").Count(&createdKeep).Error; err != nil {
		t.Fatalf("count new node: %v", err)
	}
	if keptSeed != 1 || createdKeep != 0 {
		t.Fatalf("expected graph rollback after search document refresh failure, seed=%d new=%d", keptSeed, createdKeep)
	}

	var docCount int64
	if err := db.Model(&graph.SearchDocument{}).Where("content = ?", "seed searchable").Count(&docCount).Error; err != nil {
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
	st := graphgorm.New(db)
	if err := st.AutoMigrate(); err != nil {
		t.Fatalf("migrate store: %v", err)
	}
	if err := db.AutoMigrate(&graph.SearchDocument{}); err != nil {
		t.Fatalf("migrate search docs: %v", err)
	}

	defaultNode := graph.Node{Namespace: requestctx.DefaultNamespace, QualifiedName: "pkg.Default", Kind: graph.NodeKindFunction, Name: "Default", FilePath: "default.go", StartLine: 1, EndLine: 2, Language: "go"}
	otherNode := graph.Node{Namespace: "tenant-a", QualifiedName: "pkg.Other", Kind: graph.NodeKindFunction, Name: "Other", FilePath: "other.go", StartLine: 1, EndLine: 2, Language: "go"}
	if err := db.Create(&defaultNode).Error; err != nil {
		t.Fatalf("create default node: %v", err)
	}
	if err := db.Create(&otherNode).Error; err != nil {
		t.Fatalf("create other node: %v", err)
	}
	if err := db.Create(&graph.SearchDocument{Namespace: requestctx.DefaultNamespace, NodeID: defaultNode.ID, Content: "stale default", Language: "go"}).Error; err != nil {
		t.Fatalf("seed default doc: %v", err)
	}
	if err := db.Create(&graph.SearchDocument{Namespace: "tenant-a", NodeID: otherNode.ID, Content: "keep tenant-a", Language: "go"}).Error; err != nil {
		t.Fatalf("seed tenant doc: %v", err)
	}

	count, err := searchsql.RefreshSearchDocuments(context.Background(), db)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected only default namespace docs rebuilt, got %d", count)
	}

	var otherCount int64
	if err := db.Model(&graph.SearchDocument{}).Where("namespace = ?", "tenant-a").Count(&otherCount).Error; err != nil {
		t.Fatalf("count tenant docs: %v", err)
	}
	if otherCount != 1 {
		t.Fatalf("expected tenant-a docs preserved, got %d", otherCount)
	}

	var defaultCount int64
	if err := db.Model(&graph.SearchDocument{}).Where("namespace = ?", requestctx.DefaultNamespace).Count(&defaultCount).Error; err != nil {
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
	st := graphgorm.New(db)
	if err := st.AutoMigrate(); err != nil {
		t.Fatalf("migrate store: %v", err)
	}
	if err := db.AutoMigrate(&graph.SearchDocument{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	node := graph.Node{QualifiedName: "pkg.TooLong", Kind: graph.NodeKindFunction, Name: "TooLong", FilePath: "too_long.go", StartLine: 1, EndLine: 2, Language: "go"}
	if err := db.Create(&node).Error; err != nil {
		t.Fatalf("create node: %v", err)
	}
	seed := graph.SearchDocument{Namespace: requestctx.DefaultNamespace, NodeID: 9999, Content: "seed", Language: "go"}
	if err := db.Create(&seed).Error; err != nil {
		t.Fatalf("seed search doc: %v", err)
	}
	if err := db.Exec("CREATE TRIGGER fail_search_docs_insert BEFORE INSERT ON search_documents BEGIN SELECT RAISE(ABORT, 'boom'); END;").Error; err != nil {
		t.Fatalf("create trigger: %v", err)
	}

	_, err = searchsql.RefreshSearchDocuments(context.Background(), db)
	if err == nil {
		t.Fatal("expected refresh to fail")
	}

	var count int64
	if err := db.Model(&graph.SearchDocument{}).Where("node_id = ?", seed.NodeID).Count(&count).Error; err != nil {
		t.Fatalf("count docs: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected original search document to survive rollback, got %d", count)
	}
}

func TestRefreshSearchDocuments_RejectsNilDB(t *testing.T) {
	_, err := searchsql.RefreshSearchDocuments(context.Background(), nil)
	if err == nil {
		t.Fatal("expected nil DB error")
	}
	if !strings.Contains(err.Error(), "requires db handle") {
		t.Fatalf("expected requires db handle error, got %v", err)
	}

	_, err = searchsql.RefreshSearchDocumentsFor(context.Background(), nil, []uint{1})
	if err == nil {
		t.Fatal("expected scoped nil DB error")
	}
	if !strings.Contains(err.Error(), "requires db handle") {
		t.Fatalf("expected requires db handle error, got %v", err)
	}
}

func TestRefreshSearchDocumentsFor_RefreshesOnlyScopedNodes(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	st := graphgorm.New(db)
	if err := st.AutoMigrate(); err != nil {
		t.Fatalf("migrate store: %v", err)
	}
	if err := db.AutoMigrate(&graph.SearchDocument{}); err != nil {
		t.Fatalf("migrate search docs: %v", err)
	}

	changed := graph.Node{Namespace: requestctx.DefaultNamespace, QualifiedName: "pkg.Changed", Kind: graph.NodeKindFunction, Name: "Changed", FilePath: "changed.go", StartLine: 1, EndLine: 2, Language: "go"}
	untouched := graph.Node{Namespace: requestctx.DefaultNamespace, QualifiedName: "pkg.Untouched", Kind: graph.NodeKindFunction, Name: "Untouched", FilePath: "untouched.go", StartLine: 1, EndLine: 2, Language: "go"}
	foreign := graph.Node{Namespace: "tenant-a", QualifiedName: "pkg.Foreign", Kind: graph.NodeKindFunction, Name: "Foreign", FilePath: "foreign.go", StartLine: 1, EndLine: 2, Language: "go"}
	for _, node := range []*graph.Node{&changed, &untouched, &foreign} {
		if err := db.Create(node).Error; err != nil {
			t.Fatalf("create node: %v", err)
		}
	}
	if err := db.Create(&graph.SearchDocument{Namespace: requestctx.DefaultNamespace, NodeID: changed.ID, Content: "stale changed", Language: "go"}).Error; err != nil {
		t.Fatalf("seed changed doc: %v", err)
	}
	if err := db.Create(&graph.SearchDocument{Namespace: requestctx.DefaultNamespace, NodeID: untouched.ID, Content: "keep untouched", Language: "go"}).Error; err != nil {
		t.Fatalf("seed untouched doc: %v", err)
	}
	if err := db.Create(&graph.SearchDocument{Namespace: "tenant-a", NodeID: foreign.ID, Content: "keep foreign", Language: "go"}).Error; err != nil {
		t.Fatalf("seed foreign doc: %v", err)
	}

	count, err := searchsql.RefreshSearchDocumentsFor(context.Background(), db, []uint{changed.ID, foreign.ID})
	if err != nil {
		t.Fatalf("refresh scoped: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected one default namespace doc refreshed, got %d", count)
	}

	var changedDoc, untouchedDoc, foreignDoc graph.SearchDocument
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
	st := graphgorm.New(db)
	if err := st.AutoMigrate(); err != nil {
		t.Fatalf("migrate store: %v", err)
	}
	if err := db.AutoMigrate(&graph.SearchDocument{}); err != nil {
		t.Fatalf("migrate search docs: %v", err)
	}
	node := graph.Node{Namespace: requestctx.DefaultNamespace, QualifiedName: "pkg.Keep", Kind: graph.NodeKindFunction, Name: "Keep", FilePath: "keep.go", StartLine: 1, EndLine: 2, Language: "go"}
	if err := db.Create(&node).Error; err != nil {
		t.Fatalf("create node: %v", err)
	}
	if err := db.Create(&graph.SearchDocument{Namespace: requestctx.DefaultNamespace, NodeID: node.ID, Content: "stale keep", Language: "go"}).Error; err != nil {
		t.Fatalf("seed doc: %v", err)
	}

	count, err := searchsql.RefreshSearchDocumentsFor(context.Background(), db, nil)
	if err != nil {
		t.Fatalf("refresh empty scope: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected empty scope no-op count, got %d", count)
	}

	var doc graph.SearchDocument
	if err := db.Where("node_id = ?", node.ID).First(&doc).Error; err != nil {
		t.Fatalf("load doc: %v", err)
	}
	if doc.Content != "stale keep" {
		t.Fatalf("expected stale doc preserved for empty scope, got %q", doc.Content)
	}
}

func TestRefreshSearchDocumentsFor_ChunksLargeNodeScopes(t *testing.T) {
	capture := &serviceINQueryCaptureLogger{Interface: gormlogger.Discard, needle: "nodes"}
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: capture})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	st := graphgorm.New(db)
	if err := st.AutoMigrate(); err != nil {
		t.Fatalf("migrate store: %v", err)
	}
	if err := db.AutoMigrate(&graph.SearchDocument{}); err != nil {
		t.Fatalf("migrate search docs: %v", err)
	}

	nodeIDs := make([]uint, 0, scopedINQueryChunkSize+1)
	for i := range scopedINQueryChunkSize + 1 {
		node := graph.Node{Namespace: requestctx.DefaultNamespace, QualifiedName: fmt.Sprintf("pkg.Node%d", i), Kind: graph.NodeKindFunction, Name: fmt.Sprintf("Node%d", i), FilePath: fmt.Sprintf("node-%d.go", i), StartLine: 1, EndLine: 1, Language: "go"}
		if err := db.Create(&node).Error; err != nil {
			t.Fatalf("create node %d: %v", i, err)
		}
		nodeIDs = append(nodeIDs, node.ID)
		if err := db.Create(&graph.SearchDocument{Namespace: requestctx.DefaultNamespace, NodeID: node.ID, Content: "stale", Language: "go"}).Error; err != nil {
			t.Fatalf("seed doc %d: %v", i, err)
		}
	}

	count, err := searchsql.RefreshSearchDocumentsFor(context.Background(), db, nodeIDs)
	if err != nil {
		t.Fatalf("refresh scoped: %v", err)
	}
	if count != len(nodeIDs) {
		t.Fatalf("expected %d docs refreshed, got %d", len(nodeIDs), count)
	}
	if capture.maxIDs > scopedINQueryChunkSize {
		t.Fatalf("expected scoped node IN queries to be chunked to <= %d IDs, got %d", scopedINQueryChunkSize, capture.maxIDs)
	}
	if capture.hits < 2 {
		t.Fatalf("expected multiple scoped node IN queries, got %d", capture.hits)
	}
}

func TestRefreshSearchDocuments_RebuildsPerBatchWithoutAccumulatingGlobalSlice(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	st := graphgorm.New(db)
	if err := st.AutoMigrate(); err != nil {
		t.Fatalf("migrate store: %v", err)
	}
	if err := db.AutoMigrate(&graph.SearchDocument{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	for i := range 550 {
		node := graph.Node{
			QualifiedName: "pkg.Node" + strconv.Itoa(i),
			Kind:          graph.NodeKindFunction,
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

	count, err := searchsql.RefreshSearchDocuments(context.Background(), db)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if count != 550 {
		t.Fatalf("expected 550 search docs, got %d", count)
	}

	var persisted int64
	if err := db.Model(&graph.SearchDocument{}).Count(&persisted).Error; err != nil {
		t.Fatalf("count docs: %v", err)
	}
	if persisted != 550 {
		t.Fatalf("expected 550 persisted search docs, got %d", persisted)
	}
}

func assertFunctionNamesByFile(t *testing.T, st *graphgorm.Store, ctx context.Context, filePath string, want []string) {
	t.Helper()

	nodes, err := st.GetNodesByFile(ctx, filePath)
	if err != nil {
		t.Fatalf("GetNodesByFile(%q): %v", filePath, err)
	}

	got := make([]string, 0, len(nodes))
	for _, node := range nodes {
		if node.Kind == graph.NodeKindFunction {
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
	if err := db.AutoMigrate(&graph.Node{}); err != nil {
		t.Fatalf("migrate nodes: %v", err)
	}

	svc := &Service{
		UnitOfWork: newTestUnitOfWork(db, nil),
		Walkers:    map[string]Parser{".go": treesitter.NewWalker(treesitter.GoSpec)},
		Logger:     slog.Default(),
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
		BuildOptions:     BuildOptions{Dir: tmpDir},
		Syncer:           syncer,
		FailOnUnreadable: true,
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
	if err := db.AutoMigrate(&graph.Node{}); err != nil {
		t.Fatalf("migrate nodes: %v", err)
	}

	svc := &Service{
		UnitOfWork: newTestUnitOfWork(db, nil),
		Walkers:    map[string]Parser{".go": treesitter.NewWalker(treesitter.GoSpec)},
		Logger:     slog.Default(),
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
