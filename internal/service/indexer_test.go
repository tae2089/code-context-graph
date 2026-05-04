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

	"github.com/tae2089/code-context-graph/internal/analysis/flows"
	"github.com/tae2089/code-context-graph/internal/analysis/incremental"
	querypkg "github.com/tae2089/code-context-graph/internal/analysis/query"
	"github.com/tae2089/code-context-graph/internal/ctxns"
	"github.com/tae2089/code-context-graph/internal/edgeresolve"
	"github.com/tae2089/code-context-graph/internal/model"
	"github.com/tae2089/code-context-graph/internal/parse/treesitter"
	"github.com/tae2089/code-context-graph/internal/store"
	"github.com/tae2089/code-context-graph/internal/store/gormstore"
	storesearch "github.com/tae2089/code-context-graph/internal/store/search"
)

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

func TestBuildSearchDocuments_IndexesFileBaseAndLanguageTokens(t *testing.T) {
	tests := []struct {
		name     string
		node     model.Node
		contains []string
	}{
		{
			name:     "java file includes base and language",
			node:     model.Node{Name: "UserService", QualifiedName: "UserService", Kind: model.NodeKindClass, FilePath: "java/Sample.java", Language: "java"},
			contains: []string{"userservice", "sample", "java"},
		},
		{
			name:     "rust file includes alias",
			node:     model.Node{Name: "get_user", QualifiedName: "get_user", Kind: model.NodeKindFunction, FilePath: "rust/sample.rs", Language: "rust"},
			contains: []string{"get_user", "sample", "rs", "rust"},
		},
		{
			name:     "javascript file includes alias",
			node:     model.Node{Name: "getUser", QualifiedName: "UserService.getUser", Kind: model.NodeKindFunction, FilePath: "javascript/sample.js", Language: "javascript"},
			contains: []string{"getuser", "sample", "js", "javascript"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			content := buildSearchContent(tt.node, nil)
			for _, want := range tt.contains {
				if !strings.Contains(strings.ToLower(content), want) {
					t.Fatalf("content %q missing token %q", content, want)
				}
			}
		})
	}
}

type recordingGraphStore struct {
	t             *testing.T
	ops           []string
	nextID        uint
	nodesByFP     map[string][]model.Node
	edges         []model.Edge
	upsertedEdges [][]model.Edge
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
	batch := append([]model.Edge(nil), edges...)
	r.upsertedEdges = append(r.upsertedEdges, batch)
	r.edges = append(r.edges, batch...)
	return nil
}

func (r *recordingGraphStore) GetNode(ctx context.Context, qualifiedName string) (*model.Node, error) {
	return nil, nil
}

func (r *recordingGraphStore) GetNodeByID(ctx context.Context, id uint) (*model.Node, error) {
	return nil, nil
}

func (r *recordingGraphStore) GetNodesByIDs(ctx context.Context, ids []uint) ([]model.Node, error) {
	set := make(map[uint]bool, len(ids))
	for _, id := range ids {
		set[id] = true
	}
	var result []model.Node
	for _, nodes := range r.nodesByFP {
		for _, n := range nodes {
			if set[n.ID] {
				result = append(result, n)
			}
		}
	}
	return result, nil
}

func (r *recordingGraphStore) GetNodesByQualifiedNames(ctx context.Context, names []string) (map[string][]model.Node, error) {
	set := make(map[string]bool, len(names))
	for _, name := range names {
		set[name] = true
	}
	result := make(map[string][]model.Node)
	for _, nodes := range r.nodesByFP {
		for _, n := range nodes {
			if set[n.QualifiedName] {
				result[n.QualifiedName] = append(result[n.QualifiedName], n)
			}
		}
	}
	return result, nil
}

func (r *recordingGraphStore) GetNodesByFiles(ctx context.Context, filePaths []string) (map[string][]model.Node, error) {
	set := make(map[string]bool, len(filePaths))
	for _, fp := range filePaths {
		set[fp] = true
	}
	result := make(map[string][]model.Node)
	for fp, nodes := range r.nodesByFP {
		if set[fp] {
			out := make([]model.Node, len(nodes))
			copy(out, nodes)
			result[fp] = out
		}
	}
	return result, nil
}

func (r *recordingGraphStore) GetFileNodesByPathSuffix(ctx context.Context, suffix string) ([]model.Node, error) {
	suffix = strings.Trim(path.Clean(strings.TrimSpace(suffix)), "/")
	if suffix == "" || suffix == "." {
		return nil, nil
	}
	var out []model.Node
	bestDepth := -1
	for _, nodes := range r.nodesByFP {
		for _, node := range nodes {
			if node.Kind != model.NodeKindFile {
				continue
			}
			dir := strings.Trim(path.Dir(node.FilePath), "/")
			if dir == "." || dir == "" {
				continue
			}
			if suffix == dir {
				return []model.Node{node}, nil
			}
			if depth := serviceCommonPathSuffixDepth(suffix, dir); depth > 0 {
				if depth > bestDepth {
					bestDepth = depth
					out = []model.Node{node}
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
	set := make(map[uint]bool, len(nodeIDs))
	for _, id := range nodeIDs {
		set[id] = true
	}
	var result []model.Edge
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

func (r *recordingGraphStore) GetAnnotation(ctx context.Context, nodeID uint) (*model.Annotation, error) {
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

func TestFlushBuildEdges_ResolvesAndUpsertsBoundedBatches(t *testing.T) {
	ctx := context.Background()
	st := newRecordingGraphStore(t)
	st.nodesByFP["cmd/main.go"] = []model.Node{
		{ID: 10, QualifiedName: "cmd/main.go", Name: "cmd/main.go", Kind: model.NodeKindFile, FilePath: "cmd/main.go", Language: "go"},
		{ID: 1, QualifiedName: "main.Run", Name: "Run", Kind: model.NodeKindFunction, FilePath: "cmd/main.go", StartLine: 1, EndLine: 100, Language: "go"},
	}
	st.nodesByFP["mcp/deps.go"] = []model.Node{
		{ID: 20, QualifiedName: "mcp/deps.go", Name: "mcp/deps.go", Kind: model.NodeKindFile, FilePath: "mcp/deps.go", Language: "go"},
		{ID: 2, QualifiedName: "mcp.FlowTracer", Name: "FlowTracer", Kind: model.NodeKindType, FilePath: "mcp/deps.go", StartLine: 3, EndLine: 5, Language: "go"},
	}
	st.nodesByFP["flows/tracer.go"] = []model.Node{
		{ID: 30, QualifiedName: "flows/tracer.go", Name: "flows/tracer.go", Kind: model.NodeKindFile, FilePath: "flows/tracer.go", Language: "go"},
		{ID: 3, QualifiedName: "flows.Tracer", Name: "Tracer", Kind: model.NodeKindClass, FilePath: "flows/tracer.go", StartLine: 7, EndLine: 7, Language: "go"},
		{ID: 4, QualifiedName: "flows.Tracer.TraceFlow", Name: "TraceFlow", Kind: model.NodeKindFunction, FilePath: "flows/tracer.go", StartLine: 9, EndLine: 11, Language: "go"},
	}

	var resolveSizes []int
	oldResolve := resolveBuildEdges
	resolveBuildEdges = func(ctx context.Context, lookup edgeresolve.NodeLookup, edges []model.Edge, options edgeresolve.ResolveOptions) ([]model.Edge, error) {
		resolveSizes = append(resolveSizes, len(edges))
		if len(edges) > buildEdgeResolveChunkSize {
			t.Fatalf("resolve batch exceeded limit: got %d want <= %d", len(edges), buildEdgeResolveChunkSize)
		}
		return oldResolve(ctx, lookup, edges, options)
	}
	t.Cleanup(func() { resolveBuildEdges = oldResolve })

	batches := []parsedBuildEdgeBatch{
		{relPath: "cmd/main.go", edges: []model.Edge{{Kind: model.EdgeKindImportsFrom, FilePath: "cmd/main.go", Line: 1, Fingerprint: "imports_from:cmd/main.go:github.com/example/project/mcp:1"}, {Kind: model.EdgeKindCalls, FilePath: "cmd/main.go", Line: 2, Fingerprint: "calls:cmd/main.go:h.deps.FlowTracer.TraceFlow:2"}}},
		{relPath: "flows/tracer.go", edges: []model.Edge{{Kind: model.EdgeKindImplements, FilePath: "flows/tracer.go", Line: 7, Fingerprint: "implements:flows/tracer.go:flows.Tracer:mcp.FlowTracer"}}},
	}

	svc := &GraphService{}
	if err := svc.flushBuildEdges(ctx, st, batches, nil, edgeresolve.ResolveOptions{}); err != nil {
		t.Fatalf("flushBuildEdges: %v", err)
	}
	if got, want := resolveSizes, []int{1, 3}; !reflect.DeepEqual(got, want) {
		t.Fatalf("resolve sizes: got=%v want=%v", got, want)
	}
	if len(st.upsertedEdges) != 2 {
		t.Fatalf("expected 2 upserted edge batches, got %d", len(st.upsertedEdges))
	}
	if st.upsertedEdges[0][0].Kind != model.EdgeKindImplements || st.upsertedEdges[0][0].FromNodeID != 3 || st.upsertedEdges[0][0].ToNodeID != 2 {
		t.Fatalf("implements edge mismatch: %+v", st.upsertedEdges[0][0])
	}
	if len(st.upsertedEdges[1]) != 2 {
		t.Fatalf("expected import+call edges in second batch, got %d", len(st.upsertedEdges[1]))
	}
	call := st.upsertedEdges[1][1]
	if call.Kind != model.EdgeKindCalls || call.FromNodeID != 1 || call.ToNodeID != 4 {
		t.Fatalf("call edge mismatch: %+v", call)
	}
}

func TestFlushBuildEdges_ResolvesImplementsOnlyOnce(t *testing.T) {
	ctx := context.Background()
	st := newRecordingGraphStore(t)
	st.nodesByFP["cmd/main.go"] = []model.Node{{ID: 10, QualifiedName: "cmd/main.go", Name: "cmd/main.go", Kind: model.NodeKindFile, FilePath: "cmd/main.go", Language: "go"}, {ID: 1, QualifiedName: "main.Run", Name: "Run", Kind: model.NodeKindFunction, FilePath: "cmd/main.go", StartLine: 1, EndLine: 100, Language: "go"}}
	st.nodesByFP["mcp/deps.go"] = []model.Node{{ID: 20, QualifiedName: "mcp/deps.go", Name: "mcp/deps.go", Kind: model.NodeKindFile, FilePath: "mcp/deps.go", Language: "go"}, {ID: 2, QualifiedName: "mcp.FlowTracer", Name: "FlowTracer", Kind: model.NodeKindType, FilePath: "mcp/deps.go", StartLine: 3, EndLine: 5, Language: "go"}}
	st.nodesByFP["flows/tracer.go"] = []model.Node{{ID: 30, QualifiedName: "flows/tracer.go", Name: "flows/tracer.go", Kind: model.NodeKindFile, FilePath: "flows/tracer.go", Language: "go"}, {ID: 3, QualifiedName: "flows.Tracer", Name: "Tracer", Kind: model.NodeKindClass, FilePath: "flows/tracer.go", StartLine: 7, EndLine: 7, Language: "go"}, {ID: 4, QualifiedName: "flows.Tracer.TraceFlow", Name: "TraceFlow", Kind: model.NodeKindFunction, FilePath: "flows/tracer.go", StartLine: 9, EndLine: 11, Language: "go"}}

	var implementsSeen []int
	oldResolve := resolveBuildEdges
	resolveBuildEdges = func(ctx context.Context, lookup edgeresolve.NodeLookup, edges []model.Edge, options edgeresolve.ResolveOptions) ([]model.Edge, error) {
		count := 0
		for _, edge := range edges {
			if edge.Kind == model.EdgeKindImplements {
				count++
			}
		}
		implementsSeen = append(implementsSeen, count)
		return oldResolve(ctx, lookup, edges, options)
	}
	t.Cleanup(func() { resolveBuildEdges = oldResolve })

	batches := []parsedBuildEdgeBatch{
		{relPath: "flows/tracer.go", edges: []model.Edge{{Kind: model.EdgeKindImplements, FilePath: "flows/tracer.go", Line: 7, Fingerprint: "implements:flows/tracer.go:flows.Tracer:mcp.FlowTracer"}}},
		{relPath: "cmd/main.go", edges: []model.Edge{{Kind: model.EdgeKindImportsFrom, FilePath: "cmd/main.go", Line: 1, Fingerprint: "imports_from:cmd/main.go:github.com/example/project/mcp:1"}, {Kind: model.EdgeKindCalls, FilePath: "cmd/main.go", Line: 2, Fingerprint: "calls:cmd/main.go:h.deps.FlowTracer.TraceFlow:2"}}},
		{relPath: "cmd/main.go", edges: []model.Edge{{Kind: model.EdgeKindContains, FilePath: "cmd/main.go", Line: 1, Fingerprint: "contains:cmd/main.go:main.Run"}}},
	}

	svc := &GraphService{}
	if err := svc.flushBuildEdges(ctx, st, batches, nil, edgeresolve.ResolveOptions{}); err != nil {
		t.Fatalf("flushBuildEdges: %v", err)
	}
	if got, want := implementsSeen, []int{1, 0, 0}; !reflect.DeepEqual(got, want) {
		t.Fatalf("implements counts per resolve call: got=%v want=%v", got, want)
	}
}

func TestFlushBuildEdges_WarmsImportsAcrossChunkBoundaries(t *testing.T) {
	ctx := context.Background()
	st := newRecordingGraphStore(t)
	st.nodesByFP["cmd/main.go"] = []model.Node{{ID: 10, QualifiedName: "cmd/main.go", Name: "cmd/main.go", Kind: model.NodeKindFile, FilePath: "cmd/main.go", Language: "go"}, {ID: 1, QualifiedName: "main.Run", Name: "Run", Kind: model.NodeKindFunction, FilePath: "cmd/main.go", StartLine: 1, EndLine: 1000, Language: "go"}}
	st.nodesByFP["mcp/deps.go"] = []model.Node{{ID: 20, QualifiedName: "mcp/deps.go", Name: "mcp/deps.go", Kind: model.NodeKindFile, FilePath: "mcp/deps.go", Language: "go"}, {ID: 2, QualifiedName: "mcp.FlowTracer", Name: "FlowTracer", Kind: model.NodeKindType, FilePath: "mcp/deps.go", StartLine: 3, EndLine: 5, Language: "go"}}
	st.nodesByFP["flows/tracer.go"] = []model.Node{{ID: 30, QualifiedName: "flows/tracer.go", Name: "flows/tracer.go", Kind: model.NodeKindFile, FilePath: "flows/tracer.go", Language: "go"}, {ID: 3, QualifiedName: "flows.Tracer", Name: "Tracer", Kind: model.NodeKindClass, FilePath: "flows/tracer.go", StartLine: 7, EndLine: 7, Language: "go"}, {ID: 4, QualifiedName: "flows.Tracer.TraceFlow", Name: "TraceFlow", Kind: model.NodeKindFunction, FilePath: "flows/tracer.go", StartLine: 9, EndLine: 11, Language: "go"}}

	batches := []parsedBuildEdgeBatch{
		{relPath: "flows/tracer.go", edges: []model.Edge{{Kind: model.EdgeKindImplements, FilePath: "flows/tracer.go", Line: 7, Fingerprint: "implements:flows/tracer.go:flows.Tracer:mcp.FlowTracer"}}},
		{relPath: "cmd/main.go", edges: append([]model.Edge{{Kind: model.EdgeKindImportsFrom, FilePath: "cmd/main.go", Line: 1, Fingerprint: "imports_from:cmd/main.go:github.com/example/project/mcp:1"}}, repeatedCallEdges("cmd/main.go", buildEdgeResolveChunkSize)...)},
	}

	svc := &GraphService{}
	if err := svc.flushBuildEdges(ctx, st, batches, nil, edgeresolve.ResolveOptions{}); err != nil {
		t.Fatalf("flushBuildEdges: %v", err)
	}
	if len(st.upsertedEdges) < 3 {
		t.Fatalf("expected multiple upsert batches, got %d", len(st.upsertedEdges))
	}
	lastBatch := st.upsertedEdges[len(st.upsertedEdges)-1]
	call := lastBatch[len(lastBatch)-1]
	if call.Kind != model.EdgeKindCalls || call.ToNodeID != 4 {
		t.Fatalf("expected warmed call edge to resolve after chunk split, got %+v", call)
	}
}

func repeatedCallEdges(filePath string, count int) []model.Edge {
	edges := make([]model.Edge, 0, count)
	for i := 0; i < count; i++ {
		edges = append(edges, model.Edge{Kind: model.EdgeKindCalls, FilePath: filePath, Line: i + 2, Fingerprint: fmt.Sprintf("calls:%s:h.deps.FlowTracer.TraceFlow:%d", filePath, i+2)})
	}
	return edges
}

func TestBuild_UsesRepoLocalPackageClauseForGoImportAssertions(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	st := gormstore.New(db)
	if err := st.AutoMigrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	svc := &GraphService{Store: st, DB: db, Walkers: map[string]*treesitter.Walker{".go": treesitter.NewWalker(treesitter.GoSpec)}, Logger: slog.Default()}

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
		if edge.Kind == model.EdgeKindImplements && edge.ToNodeID == iface.ID {
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
	st := gormstore.New(db)
	if err := st.AutoMigrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	svc := &GraphService{Store: st, DB: db, Walkers: map[string]*treesitter.Walker{".go": treesitter.NewWalker(treesitter.GoSpec)}, Logger: slog.Default()}

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
		if edge.Kind == model.EdgeKindImplements && edge.ToNodeID == iface.ID {
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
	st := gormstore.New(db)
	if err := st.AutoMigrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	walker := treesitter.NewWalker(treesitter.GoSpec)
	svc := &GraphService{Store: st, DB: db, Walkers: map[string]*treesitter.Walker{".go": walker}, Logger: slog.Default()}

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
		if edge.Kind == model.EdgeKindImplements && edge.ToNodeID == iface.ID {
			return
		}
	}
	t.Fatalf("expected update-time cross-file implements edge from %d to %d, got %+v", impl.ID, iface.ID, edges)
}

func TestUpdate_RemovesStaleCrossFileGoStructuralImplements(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	st := gormstore.New(db)
	if err := st.AutoMigrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	walker := treesitter.NewWalker(treesitter.GoSpec)
	svc := &GraphService{Store: st, DB: db, Walkers: map[string]*treesitter.Walker{".go": walker}, Logger: slog.Default()}

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
		if edge.Kind == model.EdgeKindImplements && edge.ToNodeID == iface.ID {
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
		if edge.Kind == model.EdgeKindImplements && edge.ToNodeID == iface.ID {
			t.Fatalf("expected stale cross-file implements edge to be removed, got %+v", edges)
		}
	}
}

func TestUpdate_ReplacesLegacyInheritsFingerprintWithV2(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	st := gormstore.New(db)
	if err := st.AutoMigrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	walker := treesitter.NewWalker(treesitter.PythonSpec)
	svc := &GraphService{Store: st, DB: db, Walkers: map[string]*treesitter.Walker{".py": walker}, Logger: slog.Default()}

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

	legacy := model.Edge{
		Namespace:   ctxns.DefaultNamespace,
		FromNodeID:  child.ID,
		ToNodeID:    base.ID,
		Kind:        model.EdgeKindInherits,
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
	var inherits []model.Edge
	for _, edge := range edges {
		if edge.Kind == model.EdgeKindInherits {
			inherits = append(inherits, edge)
		}
	}
	if len(inherits) != 1 {
		t.Fatalf("expected exactly one inherits edge after update, got %+v", inherits)
	}
	want := model.BuildInheritsFingerprintV2("models.py", "Child", "Base")
	if inherits[0].Fingerprint != want {
		t.Fatalf("inherits fingerprint = %q, want %q", inherits[0].Fingerprint, want)
	}
	var legacyCount int64
	if err := db.Model(&model.Edge{}).Where("namespace = ? AND fingerprint = ?", ctxns.DefaultNamespace, legacy.Fingerprint).Count(&legacyCount).Error; err != nil {
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
	st := gormstore.New(db)
	if err := st.AutoMigrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	walker := treesitter.NewWalker(treesitter.GoSpec)
	svc := &GraphService{Store: st, DB: db, Walkers: map[string]*treesitter.Walker{".go": walker}, Logger: slog.Default()}

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
	var initial []model.Edge
	for _, edge := range edges {
		if edge.Kind == model.EdgeKindImplements && edge.ToNodeID == iface.ID {
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
	var implEdges []model.Edge
	for _, edge := range edges {
		if edge.Kind == model.EdgeKindImplements && edge.ToNodeID == iface.ID {
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
	st := gormstore.New(db)
	if err := st.AutoMigrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	svc := &GraphService{Store: st, DB: db, Walkers: map[string]*treesitter.Walker{".go": treesitter.NewWalker(treesitter.GoSpec)}, Logger: slog.Default()}

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
		if edge.Kind == model.EdgeKindImplements && (edge.ToNodeID == iface.ID || edge.ToNodeID == otherIface.ID) {
			t.Fatalf("expected conflicting package clauses to suppress alias correction, got implements edge %+v", edge)
		}
	}
}

func TestBuild_ImportsFromTargetsPackageNodeForMultiFileGoPackage(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	st := gormstore.New(db)
	if err := st.AutoMigrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	svc := &GraphService{Store: st, DB: db, Walkers: map[string]*treesitter.Walker{".go": treesitter.NewWalker(treesitter.GoSpec)}, Logger: slog.Default()}

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
	if pkgNode.Kind != model.NodeKindPackage {
		t.Fatalf("package node kind=%q, want %q", pkgNode.Kind, model.NodeKindPackage)
	}

	qs := querypkg.New(db)
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
	st := gormstore.New(db)
	if err := st.AutoMigrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	svc := &GraphService{Store: st, DB: db, Walkers: map[string]*treesitter.Walker{".py": treesitter.NewWalker(treesitter.PythonSpec)}, Logger: slog.Default()}

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
	if pkgNode.Kind != model.NodeKindPackage {
		t.Fatalf("package node kind=%q, want %q", pkgNode.Kind, model.NodeKindPackage)
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
		if edge.Kind != model.EdgeKindInherits {
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
	st := gormstore.New(db)
	if err := st.AutoMigrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	svc := &GraphService{Store: st, DB: db, Walkers: map[string]*treesitter.Walker{".ts": treesitter.NewWalker(treesitter.TypeScriptSpec)}, Logger: slog.Default()}

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
	qs := querypkg.New(db)
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
	st := gormstore.New(db)
	if err := st.AutoMigrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	svc := &GraphService{Store: st, DB: db, Walkers: map[string]*treesitter.Walker{".ts": treesitter.NewWalker(treesitter.TypeScriptSpec)}, Logger: slog.Default()}

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
	qs := querypkg.New(db)
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
	st := gormstore.New(db)
	if err := st.AutoMigrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	svc := &GraphService{Store: st, DB: db, Walkers: map[string]*treesitter.Walker{".ts": treesitter.NewWalker(treesitter.TypeScriptSpec)}, Logger: slog.Default()}

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
	if node.Kind != model.NodeKindFunction {
		t.Fatalf("function node kind=%q, want %q", node.Kind, model.NodeKindFunction)
	}
}

func TestBuild_TypeScriptClassQualifiedNameUsesFilePackageContext(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	st := gormstore.New(db)
	if err := st.AutoMigrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	svc := &GraphService{Store: st, DB: db, Walkers: map[string]*treesitter.Walker{".ts": treesitter.NewWalker(treesitter.TypeScriptSpec)}, Logger: slog.Default()}

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
	if node.Kind != model.NodeKindClass {
		t.Fatalf("class node kind=%q, want %q", node.Kind, model.NodeKindClass)
	}
}

func TestBuild_TypeScriptClassMethodQualifiedNameUsesFilePackageContext(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	st := gormstore.New(db)
	if err := st.AutoMigrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	svc := &GraphService{Store: st, DB: db, Walkers: map[string]*treesitter.Walker{".ts": treesitter.NewWalker(treesitter.TypeScriptSpec)}, Logger: slog.Default()}

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
	if node.Kind != model.NodeKindFunction {
		t.Fatalf("method node kind=%q, want %q", node.Kind, model.NodeKindFunction)
	}
}

func TestBuild_TypeScriptSameFileHeritageUsesQualifiedNames(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	st := gormstore.New(db)
	if err := st.AutoMigrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	svc := &GraphService{Store: st, DB: db, Walkers: map[string]*treesitter.Walker{".ts": treesitter.NewWalker(treesitter.TypeScriptSpec)}, Logger: slog.Default()}

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
		case model.EdgeKindInherits:
			if edge.ToNodeID == 0 {
				continue
			}
			baseNode, err := st.GetNodeByID(ctx, edge.ToNodeID)
			if err == nil && baseNode != nil && baseNode.QualifiedName == "@acme/app/src/models.Base" {
				foundInherits = true
			}
		case model.EdgeKindImplements:
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
	st := gormstore.New(db)
	if err := st.AutoMigrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	svc := &GraphService{Store: st, DB: db, Walkers: map[string]*treesitter.Walker{".ts": treesitter.NewWalker(treesitter.TypeScriptSpec)}, Logger: slog.Default()}

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

	var edges []model.Edge
	if err := db.Where("file_path = ?", "src/models/user.ts").Find(&edges).Error; err != nil {
		t.Fatalf("load raw edges: %v", err)
	}
	var foundInherits, foundImplements bool
	for _, edge := range edges {
		switch edge.Kind {
		case model.EdgeKindInherits:
			child, parent, ok := model.ParseInheritsFingerprint("src/models/user.ts", edge.Fingerprint)
			if ok && child == "@acme/app/src/models.User" && parent == "@acme/app/src/base.Base" {
				foundInherits = true
			}
		case model.EdgeKindImplements:
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
	svc := &GraphService{Walkers: map[string]*treesitter.Walker{".ts": treesitter.NewWalker(treesitter.TypeScriptSpec)}, Logger: slog.Default()}

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
		case model.EdgeKindInherits:
			child, parent, ok := model.ParseInheritsFingerprint("src/models/user.ts", edge.Fingerprint)
			if ok && child == "@acme/app/src/models.User" && parent == "@acme/app/src/base.Base" {
				foundInherits = true
			}
		case model.EdgeKindImplements:
			if edge.Fingerprint == "implements:src/models/user.ts:@acme/app/src/models.User:@acme/app/src/contracts.Authenticated" {
				foundImplements = true
			}
		}
	}
	if !foundInherits || !foundImplements {
		t.Fatalf("expected qualified imported heritage edges in spool record, got %+v", record.Edges)
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
	st := gormstore.New(db)
	if err := st.AutoMigrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	walker := treesitter.NewWalker(treesitter.TypeScriptSpec)
	svc := &GraphService{Store: st, DB: db, Walkers: map[string]*treesitter.Walker{".ts": walker}, Logger: slog.Default()}
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
	if node.Kind != model.NodeKindFunction {
		t.Fatalf("function node kind=%q, want %q", node.Kind, model.NodeKindFunction)
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
	st := gormstore.New(db)
	if err := st.AutoMigrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	svc := &GraphService{Store: st, DB: db, Walkers: map[string]*treesitter.Walker{".java": treesitter.NewWalker(treesitter.JavaSpec)}, Logger: slog.Default()}

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
		if edge.Kind == model.EdgeKindInherits && edge.ToNodeID == base.ID {
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
	if firstEdge <= lastAnn {
		t.Fatalf("expected deferred edge flush after all annotations, got ops=%v", fakeStore.ops)
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

func TestBuild_ResolvesCallEdgesForTraceFlow(t *testing.T) {
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
	if err := db.Model(&model.Edge{}).
		Where("kind = ? AND from_node_id = ? AND to_node_id <> 0", model.EdgeKindCalls, start.ID).
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

func TestForceReparseFiles_IncludesUnchangedInterfaceDispatchCallSite(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.Node{}, &model.Edge{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	ctx := context.Background()
	callSite := model.Node{Namespace: ctxns.DefaultNamespace, QualifiedName: "main.Run", Kind: model.NodeKindFunction, Name: "Run", FilePath: "main.go", StartLine: 10, EndLine: 20, Hash: "same", Language: "go"}
	iface := model.Node{Namespace: ctxns.DefaultNamespace, QualifiedName: "mcp.FlowTracer", Kind: model.NodeKindType, Name: "FlowTracer", FilePath: "mcp.go", StartLine: 3, EndLine: 5, Hash: "old", Language: "go"}
	impl := model.Node{Namespace: ctxns.DefaultNamespace, QualifiedName: "flows.Tracer", Kind: model.NodeKindClass, Name: "Tracer", FilePath: "flows.go", StartLine: 3, EndLine: 3, Hash: "same", Language: "go"}
	for _, node := range []*model.Node{&callSite, &iface, &impl} {
		if err := db.Create(node).Error; err != nil {
			t.Fatalf("seed node: %v", err)
		}
	}
	if err := db.Create(&model.Edge{Namespace: ctxns.DefaultNamespace, FromNodeID: impl.ID, ToNodeID: iface.ID, Kind: model.EdgeKindImplements, FilePath: "main.go", Fingerprint: "implements:main.go:flows.Tracer:mcp.FlowTracer"}).Error; err != nil {
		t.Fatalf("seed implements edge: %v", err)
	}
	if err := db.Create(&model.Edge{Namespace: ctxns.DefaultNamespace, FromNodeID: callSite.ID, ToNodeID: 0, Kind: model.EdgeKindCalls, FilePath: "main.go", Fingerprint: "calls:main.go:h.deps.FlowTracer.TraceFlow:12"}).Error; err != nil {
		t.Fatalf("seed call edge: %v", err)
	}

	_, nodesByFile, err := existingGraphFileState(ctx, db)
	if err != nil {
		t.Fatalf("existing state: %v", err)
	}
	forceFiles, err := forceReparseFiles(ctx, db, nodesByFile, map[string]string{
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

func TestUpdateGraphWithoutTx_DoesNotDeleteForcedFilesDuringNormalSync(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.Node{}, &model.Edge{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	ctx := context.Background()

	sourceHash := sha256.Sum256([]byte("package sample\n\nfunc Source() { Target() }\n"))
	staleTargetHash := sha256.Sum256([]byte("package sample\n\nfunc Target() {}\n"))
	deletedHash := sha256.Sum256([]byte("package sample\n\nfunc Deleted() {}\n"))

	source := model.Node{Namespace: ctxns.DefaultNamespace, QualifiedName: "sample.Source", Kind: model.NodeKindFunction, Name: "Source", FilePath: "source.go", StartLine: 3, EndLine: 3, Hash: hex.EncodeToString(sourceHash[:]), Language: "go"}
	target := model.Node{Namespace: ctxns.DefaultNamespace, QualifiedName: "sample.Target", Kind: model.NodeKindFunction, Name: "Target", FilePath: "target.go", StartLine: 3, EndLine: 3, Hash: hex.EncodeToString(staleTargetHash[:]), Language: "go"}
	deleted := model.Node{Namespace: ctxns.DefaultNamespace, QualifiedName: "sample.Deleted", Kind: model.NodeKindFunction, Name: "Deleted", FilePath: "deleted.go", StartLine: 3, EndLine: 3, Hash: hex.EncodeToString(deletedHash[:]), Language: "go"}
	for _, node := range []*model.Node{&source, &target, &deleted} {
		if err := db.Create(node).Error; err != nil {
			t.Fatalf("seed node %s: %v", node.FilePath, err)
		}
	}
	if err := db.Create(&model.Edge{Namespace: ctxns.DefaultNamespace, FromNodeID: source.ID, ToNodeID: target.ID, Kind: model.EdgeKindCalls, FilePath: "source.go", Fingerprint: "calls:source.go:Target:3"}).Error; err != nil {
		t.Fatalf("seed edge: %v", err)
	}

	svc := &GraphService{
		DB:      db,
		Walkers: map[string]*treesitter.Walker{".go": treesitter.NewWalker(treesitter.GoSpec)},
		Logger:  slog.Default(),
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
	if slices.Contains(first.existingFiles, "source.go") {
		t.Fatalf("expected forced source.go to be excluded from normal sync existingFiles, got %v", first.existingFiles)
	}
	if !slices.Contains(first.existingFiles, "deleted.go") {
		t.Fatalf("expected deleted.go to remain in normal sync existingFiles, got %v", first.existingFiles)
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
	svc := &GraphService{
		Walkers: map[string]*treesitter.Walker{".go": treesitter.NewWalker(treesitter.GoSpec)},
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
	if err := db.AutoMigrate(&model.Node{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	filePaths := make([]string, 0, scopedINQueryChunkSize+1)
	for i := range scopedINQueryChunkSize + 1 {
		filePath := fmt.Sprintf("file-%03d.go", i)
		filePaths = append(filePaths, filePath)
		node := model.Node{Namespace: ctxns.DefaultNamespace, QualifiedName: fmt.Sprintf("pkg.Node%d", i), Kind: model.NodeKindFunction, Name: fmt.Sprintf("Node%d", i), FilePath: filePath, StartLine: 1, EndLine: 1, Language: "go"}
		if err := db.Create(&node).Error; err != nil {
			t.Fatalf("create node %d: %v", i, err)
		}
	}

	ids, err := currentNodeIDsForFiles(context.Background(), db, filePaths)
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

func TestRefreshSearchDocumentsFor_ChunksLargeNodeScopes(t *testing.T) {
	capture := &serviceINQueryCaptureLogger{Interface: gormlogger.Discard, needle: "nodes"}
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: capture})
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

	nodeIDs := make([]uint, 0, scopedINQueryChunkSize+1)
	for i := range scopedINQueryChunkSize + 1 {
		node := model.Node{Namespace: ctxns.DefaultNamespace, QualifiedName: fmt.Sprintf("pkg.Node%d", i), Kind: model.NodeKindFunction, Name: fmt.Sprintf("Node%d", i), FilePath: fmt.Sprintf("node-%d.go", i), StartLine: 1, EndLine: 1, Language: "go"}
		if err := db.Create(&node).Error; err != nil {
			t.Fatalf("create node %d: %v", i, err)
		}
		nodeIDs = append(nodeIDs, node.ID)
		if err := db.Create(&model.SearchDocument{Namespace: ctxns.DefaultNamespace, NodeID: node.ID, Content: "stale", Language: "go"}).Error; err != nil {
			t.Fatalf("seed doc %d: %v", i, err)
		}
	}

	count, err := RefreshSearchDocumentsFor(context.Background(), db, nodeIDs)
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
