package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"gorm.io/gorm"

	"github.com/tae2089/code-context-graph/internal/analysis/changes"
	"github.com/tae2089/code-context-graph/internal/analysis/community"
	"github.com/tae2089/code-context-graph/internal/analysis/coupling"
	"github.com/tae2089/code-context-graph/internal/analysis/coverage"
	"github.com/tae2089/code-context-graph/internal/analysis/incremental"
	"github.com/tae2089/code-context-graph/internal/analysis/query"
	"github.com/tae2089/code-context-graph/internal/ctxns"
	"github.com/tae2089/code-context-graph/internal/model"
	"github.com/tae2089/code-context-graph/internal/parse/treesitter"
	"github.com/tae2089/code-context-graph/internal/store"
)

type recordingGraphStore struct {
	ops       []string
	nextID    uint
	nodesByFP map[string][]model.Node
}

func newRecordingGraphStore() *recordingGraphStore {
	return &recordingGraphStore{nodesByFP: make(map[string][]model.Node)}
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

func TestMarshalJSON(t *testing.T) {
	data := map[string]any{"key": "value", "num": 42}
	result, err := marshalJSON(data)
	if err != nil {
		t.Fatalf("marshalJSON returned error: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(result), &out); err != nil {
		t.Fatalf("marshalJSON produced invalid JSON: %v", err)
	}
}

func TestMarshalJSON_ErrorOnUnserializable(t *testing.T) {
	_, err := marshalJSON(make(chan int)) // channel은 json.Marshal 불가
	if err == nil {
		t.Fatal("marshalJSON should return error on unserializable value")
	}
}

func TestHandler_ParseProject(t *testing.T) {
	deps := setupTestDeps(t)

	dir := t.TempDir()
	deps.RepoRoot = dir
	goFile := filepath.Join(dir, "main.go")
	os.WriteFile(goFile, []byte(`package main

func Hello() string {
	return "hello"
}

`), 0644)

	result := callTool(t, deps, "parse_project", map[string]any{"path": dir})
	if result.IsError {
		t.Fatalf("parse_project returned error: %s", getTextContent(result))
	}

	text := getTextContent(result)
	if text == "" {
		t.Fatal("expected non-empty result text")
	}

	node, err := deps.Store.GetNode(context.Background(), "main.Hello")
	if err != nil {
		t.Fatal(err)
	}
	if node == nil {
		t.Fatal("expected node main.Hello to exist after parsing")
	}
}

type orderingCommentGoParser struct{}

func (p *orderingCommentGoParser) Parse(filePath string, content []byte) ([]model.Node, []model.Edge, error) {
	nodes, edges, _, err := p.ParseWithComments(context.Background(), filePath, content)
	return nodes, edges, err
}

func (p *orderingCommentGoParser) ParseWithContext(ctx context.Context, filePath string, content []byte) ([]model.Node, []model.Edge, error) {
	nodes, edges, _, err := p.ParseWithComments(ctx, filePath, content)
	return nodes, edges, err
}

func (p *orderingCommentGoParser) ParseWithComments(ctx context.Context, filePath string, content []byte) ([]model.Node, []model.Edge, []treesitter.CommentBlock, error) {
	nodes := []model.Node{{
		QualifiedName: "sample.Keep",
		Kind:          model.NodeKindFunction,
		Name:          "Keep",
		FilePath:      filepath.Base(filePath),
		StartLine:     4,
		EndLine:       5,
		Language:      "go",
	}}
	edges := []model.Edge{{
		Kind:        model.EdgeKindCalls,
		FilePath:     filepath.Base(filePath),
		Line:         5,
		Fingerprint:  "calls:sample.Keep:sample.Helper",
	}}
	comments := []treesitter.CommentBlock{{
		StartLine:      2,
		EndLine:        2,
		Text:           "// @intent keep track",
		IsDocstring:    true,
		OwnerStartLine: 4,
	}}
	return nodes, edges, comments, nil
}

func (p *orderingCommentGoParser) Language() string { return "go" }

func TestParseProject_RejectsPathOutsideConfiguredRoot(t *testing.T) {
	deps := setupTestDeps(t)
	deps.RepoRoot = t.TempDir()
	outside := t.TempDir()
	writeGoFile(t, outside, "main.go", `package main
func Hello() {}
`)

	result := callTool(t, deps, "parse_project", map[string]any{"path": outside})
	if !result.IsError {
		t.Fatal("expected parse_project to reject path outside configured root")
	}
}

func TestParseProject_FailsClosedWithoutConfiguredRoot(t *testing.T) {
	deps := setupTestDeps(t)
	deps.RepoRoot = ""
	deps.WorkspaceRoot = ""
	dir := t.TempDir()
	writeGoFile(t, dir, "main.go", `package main
func Hello() {}
`)

	result := callTool(t, deps, "parse_project", map[string]any{"path": dir})
	if !result.IsError {
		t.Fatal("expected parse_project to fail closed without RepoRoot or WorkspaceRoot")
	}
}

func TestNewParsedWalkEdgeBatch_DoesNotRetainNodeSideState(t *testing.T) {
	typ := reflect.TypeFor[parsedWalkEdgeBatch]()
	for _, name := range []string{"nodes", "comments", "sourceLines"} {
		if _, ok := typ.FieldByName(name); ok {
			t.Fatalf("parsedWalkEdgeBatch must not retain %s", name)
		}
	}
}

func TestNewParsedWalkNodeBatch_DropsRawContentAndOnlyBuildsSourceLinesWhenNeeded(t *testing.T) {
	typ := reflect.TypeFor[parsedWalkNodeBatch]()
	if _, ok := typ.FieldByName("content"); ok {
		t.Fatal("parsedWalkNodeBatch must not retain raw content")
	}

	content := []byte("package main\n\nfunc Hello() {}\n")
	parsedWithoutComments := newParsedWalkNodeBatch("main.go", content, nil, nil)
	if parsedWithoutComments.sourceLines != nil {
		t.Fatalf("expected no sourceLines without comments, got %v", parsedWithoutComments.sourceLines)
	}

	comments := []treesitter.CommentBlock{{StartLine: 3, EndLine: 3, Text: "Hello", IsDocstring: true, OwnerStartLine: 3}}
	parsedWithComments := newParsedWalkNodeBatch("main.go", content, nil, comments)
	if got, want := parsedWithComments.sourceLines, strings.Split(string(content), "\n"); len(got) != len(want) {
		t.Fatalf("expected sourceLines length %d, got %d", len(want), len(got))
	} else {
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("expected sourceLines[%d] = %q, got %q", i, want[i], got[i])
			}
		}
	}
}

func TestHandler_walkAndParse_OrderingSeam(t *testing.T) {
	deps := setupGraphOnlyTestDeps(t)
	fakeStore := newRecordingGraphStore()
	deps.Store = fakeStore
	parser := &orderingCommentGoParser{}
	deps.Parser = parser
	deps.Walkers = map[string]Parser{".go": parser}
	h := &handlers{deps: deps}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "sample.go"), []byte(`package sample

// @intent keep track
func Keep() {}
`), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	if _, err := h.walkAndParse(context.Background(), dir); err != nil {
		t.Fatalf("walkAndParse: %v", err)
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

func TestHandler_GetNode(t *testing.T) {
	deps := setupTestDeps(t)
	ctx := context.Background()

	deps.Store.UpsertNodes(ctx, []model.Node{
		{QualifiedName: "pkg.Foo", Kind: model.NodeKindFunction, Name: "Foo", FilePath: "foo.go", StartLine: 1, EndLine: 5, Language: "go"},
	})

	result := callTool(t, deps, "get_node", map[string]any{"qualified_name": "pkg.Foo"})
	if result.IsError {
		t.Fatalf("get_node returned error: %s", getTextContent(result))
	}

	text := getTextContent(result)
	if text == "" {
		t.Fatal("expected non-empty result")
	}

	var nodeData map[string]any
	if err := json.Unmarshal([]byte(text), &nodeData); err != nil {
		t.Fatalf("expected JSON response, got: %s", text)
	}
	if nodeData["qualified_name"] != "pkg.Foo" {
		t.Errorf("expected qualified_name=pkg.Foo, got %v", nodeData["qualified_name"])
	}
}

func TestHandler_GetImpactRadius(t *testing.T) {
	deps := setupTestDeps(t)
	ctx := context.Background()

	deps.Store.UpsertNodes(ctx, []model.Node{
		{QualifiedName: "pkg.A", Kind: model.NodeKindFunction, Name: "A", FilePath: "a.go", StartLine: 1, EndLine: 5, Language: "go"},
		{QualifiedName: "pkg.B", Kind: model.NodeKindFunction, Name: "B", FilePath: "b.go", StartLine: 1, EndLine: 5, Language: "go"},
	})
	nodeA, _ := deps.Store.GetNode(ctx, "pkg.A")
	nodeB, _ := deps.Store.GetNode(ctx, "pkg.B")

	deps.Store.UpsertEdges(ctx, []model.Edge{
		{FromNodeID: nodeA.ID, ToNodeID: nodeB.ID, Kind: model.EdgeKindCalls, Fingerprint: "calls-a-b"},
	})

	result := callTool(t, deps, "get_impact_radius", map[string]any{
		"qualified_name": "pkg.A",
		"depth":          1,
	})
	if result.IsError {
		t.Fatalf("get_impact_radius returned error: %s", getTextContent(result))
	}

	text := getTextContent(result)
	var resp map[string]any
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("expected JSON response, got: %s", text)
	}
	nodes := resp["nodes"].([]any)
	if len(nodes) < 2 {
		t.Errorf("expected at least 2 nodes in impact radius, got %d", len(nodes))
	}
	metadata := resp["metadata"].(map[string]any)
	if metadata["returned_nodes"].(float64) != float64(len(nodes)) {
		t.Fatalf("returned_nodes metadata mismatch: %v", metadata)
	}
}

func TestHandler_GetImpactRadius_BoundsResults(t *testing.T) {
	deps := setupTestDeps(t)
	ctx := context.Background()
	deps.Store.UpsertNodes(ctx, []model.Node{
		{QualifiedName: "pkg.A", Kind: model.NodeKindFunction, Name: "A", FilePath: "a.go", StartLine: 1, EndLine: 5, Language: "go"},
		{QualifiedName: "pkg.B", Kind: model.NodeKindFunction, Name: "B", FilePath: "b.go", StartLine: 1, EndLine: 5, Language: "go"},
		{QualifiedName: "pkg.C", Kind: model.NodeKindFunction, Name: "C", FilePath: "c.go", StartLine: 1, EndLine: 5, Language: "go"},
	})
	a, _ := deps.Store.GetNode(ctx, "pkg.A")
	b, _ := deps.Store.GetNode(ctx, "pkg.B")
	c, _ := deps.Store.GetNode(ctx, "pkg.C")
	deps.Store.UpsertEdges(ctx, []model.Edge{{FromNodeID: a.ID, ToNodeID: b.ID, Kind: model.EdgeKindCalls, Fingerprint: "a-b"}, {FromNodeID: b.ID, ToNodeID: c.ID, Kind: model.EdgeKindCalls, Fingerprint: "b-c"}})

	result := callTool(t, deps, "get_impact_radius", map[string]any{"qualified_name": "pkg.A", "depth": 5, "max_depth": 1, "max_nodes": 2})
	if result.IsError {
		t.Fatalf("get_impact_radius returned error: %s", getTextContent(result))
	}
	var resp map[string]any
	json.Unmarshal([]byte(getTextContent(result)), &resp)
	metadata := resp["metadata"].(map[string]any)
	if metadata["max_depth"].(float64) != 1 || metadata["max_nodes"].(float64) != 2 {
		t.Fatalf("unexpected metadata: %v", metadata)
	}
	if resp["nodes"] == nil {
		t.Fatal("expected bounded nodes response")
	}
}

func TestHandler_Search(t *testing.T) {
	deps := setupTestDeps(t)
	ctx := context.Background()

	deps.Store.UpsertNodes(ctx, []model.Node{
		{QualifiedName: "pkg.AuthenticateUser", Kind: model.NodeKindFunction, Name: "AuthenticateUser", FilePath: "auth.go", StartLine: 1, EndLine: 10, Language: "go"},
	})
	node, _ := deps.Store.GetNode(ctx, "pkg.AuthenticateUser")

	deps.DB.Create(&model.SearchDocument{
		NodeID: node.ID, Content: "AuthenticateUser authenticates user credentials", Language: "go",
	})
	deps.SearchBackend.Rebuild(ctx, deps.DB)

	result := callTool(t, deps, "search", map[string]any{"query": "authenticate", "limit": 10})
	if result.IsError {
		t.Fatalf("search returned error: %s", getTextContent(result))
	}

	text := getTextContent(result)
	var nodes []map[string]any
	if err := json.Unmarshal([]byte(text), &nodes); err != nil {
		t.Fatalf("expected JSON array, got: %s", text)
	}
	if len(nodes) == 0 {
		t.Fatal("expected at least 1 search result")
	}
}

func TestHandler_Search_PathFilter(t *testing.T) {
	deps := setupTestDeps(t)
	ctx := context.Background()

	deps.Store.UpsertNodes(ctx, []model.Node{
		{QualifiedName: "internal/auth/login.go::Login", Kind: model.NodeKindFunction, Name: "Login", FilePath: "internal/auth/login.go", StartLine: 1, EndLine: 10, Language: "go"},
		{QualifiedName: "internal/payment/pay.go::Pay", Kind: model.NodeKindFunction, Name: "Pay", FilePath: "internal/payment/pay.go", StartLine: 1, EndLine: 10, Language: "go"},
	})
	loginNode, _ := deps.Store.GetNode(ctx, "internal/auth/login.go::Login")
	payNode, _ := deps.Store.GetNode(ctx, "internal/payment/pay.go::Pay")

	deps.DB.Create(&model.SearchDocument{NodeID: loginNode.ID, Content: "handle user request", Language: "go"})
	deps.DB.Create(&model.SearchDocument{NodeID: payNode.ID, Content: "handle payment request", Language: "go"})
	deps.SearchBackend.Rebuild(ctx, deps.DB)

	// Search with path filter — only auth results
	result := callTool(t, deps, "search", map[string]any{"query": "handle", "path": "internal/auth"})
	if result.IsError {
		t.Fatalf("search returned error: %s", getTextContent(result))
	}

	text := getTextContent(result)
	var nodes []map[string]any
	if err := json.Unmarshal([]byte(text), &nodes); err != nil {
		t.Fatalf("expected JSON array, got: %s", text)
	}

	for _, n := range nodes {
		fp, _ := n["file_path"].(string)
		if !strings.HasPrefix(fp, "internal/auth") {
			t.Errorf("expected only auth paths, got: %s", fp)
		}
	}
	if len(nodes) == 0 {
		t.Fatal("expected at least 1 result for auth path")
	}
}

func TestHandler_Search_PathFilter_RespectsPathBoundary(t *testing.T) {
	deps := setupTestDeps(t)
	ctx := context.Background()

	deps.Store.UpsertNodes(ctx, []model.Node{
		{QualifiedName: "internal/api/handler.go::Handle", Kind: model.NodeKindFunction, Name: "Handle", FilePath: "internal/api/handler.go", StartLine: 1, EndLine: 10, Language: "go"},
		{QualifiedName: "internal/api2/handler.go::Handle2", Kind: model.NodeKindFunction, Name: "Handle2", FilePath: "internal/api2/handler.go", StartLine: 1, EndLine: 10, Language: "go"},
	})
	apiNode, _ := deps.Store.GetNode(ctx, "internal/api/handler.go::Handle")
	api2Node, _ := deps.Store.GetNode(ctx, "internal/api2/handler.go::Handle2")

	deps.DB.Create(&model.SearchDocument{NodeID: apiNode.ID, Content: "handle api request", Language: "go"})
	deps.DB.Create(&model.SearchDocument{NodeID: api2Node.ID, Content: "handle api request", Language: "go"})
	deps.SearchBackend.Rebuild(ctx, deps.DB)

	result := callTool(t, deps, "search", map[string]any{"query": "handle", "path": "internal/api"})
	if result.IsError {
		t.Fatalf("search returned error: %s", getTextContent(result))
	}

	var nodes []map[string]any
	if err := json.Unmarshal([]byte(getTextContent(result)), &nodes); err != nil {
		t.Fatalf("expected JSON array, got: %s", getTextContent(result))
	}
	if len(nodes) != 1 {
		t.Fatalf("expected 1 boundary-safe result, got %d", len(nodes))
	}
	if got := nodes[0]["file_path"]; got != "internal/api/handler.go" {
		t.Fatalf("unexpected file_path: %v", got)
	}
}

func TestHandler_GetAnnotation(t *testing.T) {
	deps := setupTestDeps(t)
	ctx := context.Background()

	deps.Store.UpsertNodes(ctx, []model.Node{
		{QualifiedName: "pkg.Login", Kind: model.NodeKindFunction, Name: "Login", FilePath: "login.go", StartLine: 1, EndLine: 10, Language: "go"},
	})
	node, _ := deps.Store.GetNode(ctx, "pkg.Login")

	deps.Store.UpsertAnnotation(ctx, &model.Annotation{
		NodeID:  node.ID,
		Summary: "Handles user login",
		Context: "Called from HTTP handler",
		RawText: "Handles user login\nCalled from HTTP handler",
		Tags: []model.DocTag{
			{Kind: model.TagIntent, Value: "Authenticate user", Ordinal: 0},
		},
	})

	result := callTool(t, deps, "get_annotation", map[string]any{"qualified_name": "pkg.Login"})
	if result.IsError {
		t.Fatalf("get_annotation returned error: %s", getTextContent(result))
	}

	text := getTextContent(result)
	var ann map[string]any
	if err := json.Unmarshal([]byte(text), &ann); err != nil {
		t.Fatalf("expected JSON response, got: %s", text)
	}
	if ann["summary"] != "Handles user login" {
		t.Errorf("expected summary='Handles user login', got %v", ann["summary"])
	}
}

// P2-c 후속: DocTag.Type 필드가 MCP 응답에 포함되는지 검증.
// 파서는 YARD/JSDoc에서 type을 추출해 저장하지만, MCP 직렬화에서 빠지면
// 외부 사용자가 type 정보를 받을 수 없다.
func TestHandler_GetAnnotation_ExposesDocTagTypeField(t *testing.T) {
	deps := setupTestDeps(t)
	ctx := context.Background()

	deps.Store.UpsertNodes(ctx, []model.Node{
		{QualifiedName: "pkg.Fn", Kind: model.NodeKindFunction, Name: "Fn", FilePath: "f.go", StartLine: 1, EndLine: 3, Language: "go"},
	})
	node, _ := deps.Store.GetNode(ctx, "pkg.Fn")

	deps.Store.UpsertAnnotation(ctx, &model.Annotation{
		NodeID:  node.ID,
		Summary: "s",
		Tags: []model.DocTag{
			{Kind: model.TagParam, Type: "string", Name: "id", Value: "identifier", Ordinal: 0},
		},
	})

	result := callTool(t, deps, "get_annotation", map[string]any{"qualified_name": "pkg.Fn"})
	if result.IsError {
		t.Fatalf("get_annotation error: %s", getTextContent(result))
	}

	var ann map[string]any
	if err := json.Unmarshal([]byte(getTextContent(result)), &ann); err != nil {
		t.Fatalf("expected JSON, got: %s", getTextContent(result))
	}
	tags, ok := ann["tags"].([]any)
	if !ok || len(tags) != 1 {
		t.Fatalf("expected 1 tag, got: %v", ann["tags"])
	}
	tag, ok := tags[0].(map[string]any)
	if !ok {
		t.Fatalf("tag not an object: %v", tags[0])
	}
	if tag["type"] != "string" {
		t.Errorf("tag.type = %v, want \"string\" (MCP response must expose DocTag.Type)", tag["type"])
	}
	if tag["name"] != "id" {
		t.Errorf("tag.name = %v", tag["name"])
	}
}

// ============================================================
// 11.0 구조적 변경 (Tidy First)
// ============================================================

func TestDeps_NewInterfaces(t *testing.T) {
	// 신규 인터페이스 필드가 nil이어도 기존 6개 도구가 정상 동작해야 함
	deps := setupTestDeps(t)
	ctx := context.Background()

	// 기존 도구들에 필요한 데이터 셋업
	deps.Store.UpsertNodes(ctx, []model.Node{
		{QualifiedName: "pkg.Func1", Kind: model.NodeKindFunction, Name: "Func1", FilePath: "func1.go", StartLine: 1, EndLine: 5, Language: "go"},
	})

	// 신규 인터페이스 필드가 모두 nil인 상태에서 기존 도구 호출
	// QueryService, LargefuncAnalyzer, DeadcodeAnalyzer, CouplingAnalyzer,
	// CoverageAnalyzer, CommunityBuilder, Incremental 이 모두 nil
	if deps.QueryService != nil {
		t.Error("expected QueryService to be nil")
	}
	if deps.LargefuncAnalyzer != nil {
		t.Error("expected LargefuncAnalyzer to be nil")
	}
	if deps.DeadcodeAnalyzer != nil {
		t.Error("expected DeadcodeAnalyzer to be nil")
	}
	if deps.CouplingAnalyzer != nil {
		t.Error("expected CouplingAnalyzer to be nil")
	}
	if deps.CoverageAnalyzer != nil {
		t.Error("expected CoverageAnalyzer to be nil")
	}
	if deps.CommunityBuilder != nil {
		t.Error("expected CommunityBuilder to be nil")
	}
	if deps.Incremental != nil {
		t.Error("expected Incremental to be nil")
	}

	// 기존 6개 도구가 정상 동작하는지 확인
	result := callTool(t, deps, "get_node", map[string]any{"qualified_name": "pkg.Func1"})
	if result.IsError {
		t.Fatalf("get_node should work with nil new interfaces: %s", getTextContent(result))
	}
}

func TestPrompts_UsesDepsInterfaces(t *testing.T) {
	// prompts.go가 Deps 필드를 사용하도록 리팩터링 후 기존 5개 프롬프트 테스트 유지
	// Deps에 QueryService, LargefuncAnalyzer 등을 설정하면 prompts.go가 이를 사용해야 함
	deps := setupTestDeps(t)
	ctx := context.Background()

	// mock 구현으로 Deps 필드 설정
	mockQuery := &mockQueryService{}
	mockLF := &mockLargefuncAnalyzer{}
	mockDC := &mockDeadcodeAnalyzer{}
	mockCoup := &mockCouplingAnalyzer{}
	mockCov := &mockCoverageAnalyzer{}

	deps.QueryService = mockQuery
	deps.LargefuncAnalyzer = mockLF
	deps.DeadcodeAnalyzer = mockDC
	deps.CouplingAnalyzer = mockCoup
	deps.CoverageAnalyzer = mockCov

	// 데이터 셋업
	deps.Store.UpsertNodes(ctx, []model.Node{
		{QualifiedName: "pkg.TestFunc", Kind: model.NodeKindFunction, Name: "TestFunc", FilePath: "test.go", StartLine: 1, EndLine: 100, Language: "go"},
	})

	// onboard_developer 프롬프트 호출 — LargefuncAnalyzer가 Deps에 있으면 이를 사용해야 함
	srv := NewServer(deps)
	msg, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "prompts/get",
		"params":  map[string]any{"name": "onboard_developer"},
	})
	resp := srv.HandleMessage(ctx, msg)
	rpcResp, ok := resp.(mcp.JSONRPCResponse)
	if !ok {
		t.Fatalf("expected JSONRPCResponse, got %T", resp)
	}
	_ = rpcResp

	// mockLF.findCalled가 true인지 확인 — Deps.LargefuncAnalyzer를 사용했는지 검증
	if !mockLF.findCalled {
		t.Error("expected prompts.go to use Deps.LargefuncAnalyzer instead of inline creation")
	}
}

func TestHandler_TraceFlow(t *testing.T) {
	deps := setupTestDeps(t)
	ctx := context.Background()

	deps.Store.UpsertNodes(ctx, []model.Node{
		{QualifiedName: "pkg.Start", Kind: model.NodeKindFunction, Name: "Start", FilePath: "start.go", StartLine: 1, EndLine: 5, Language: "go"},
		{QualifiedName: "pkg.Middle", Kind: model.NodeKindFunction, Name: "Middle", FilePath: "mid.go", StartLine: 1, EndLine: 5, Language: "go"},
		{QualifiedName: "pkg.End", Kind: model.NodeKindFunction, Name: "End", FilePath: "end.go", StartLine: 1, EndLine: 5, Language: "go"},
	})
	start, _ := deps.Store.GetNode(ctx, "pkg.Start")
	mid, _ := deps.Store.GetNode(ctx, "pkg.Middle")

	deps.Store.UpsertEdges(ctx, []model.Edge{
		{FromNodeID: start.ID, ToNodeID: mid.ID, Kind: model.EdgeKindCalls, Fingerprint: "calls-s-m"},
		{FromNodeID: mid.ID, ToNodeID: mid.ID + 1, Kind: model.EdgeKindCalls, Fingerprint: "calls-m-e"},
	})

	result := callTool(t, deps, "trace_flow", map[string]any{"qualified_name": "pkg.Start"})
	if result.IsError {
		t.Fatalf("trace_flow returned error: %s", getTextContent(result))
	}

	text := getTextContent(result)
	var flow map[string]any
	if err := json.Unmarshal([]byte(text), &flow); err != nil {
		t.Fatalf("expected JSON response, got: %s", text)
	}
	members, ok := flow["members"].([]any)
	if !ok || len(members) < 2 {
		t.Errorf("expected at least 2 flow members, got %v", flow["members"])
	}
	metadata := flow["metadata"].(map[string]any)
	if metadata["returned_nodes"].(float64) != float64(len(members)) {
		t.Fatalf("returned_nodes metadata mismatch: %v", metadata)
	}
}

func TestHandler_TraceFlow_BoundsMembers(t *testing.T) {
	deps := setupTestDeps(t)
	ctx := context.Background()
	deps.Store.UpsertNodes(ctx, []model.Node{
		{QualifiedName: "pkg.Start2", Kind: model.NodeKindFunction, Name: "Start2", FilePath: "s.go", StartLine: 1, EndLine: 5, Language: "go"},
		{QualifiedName: "pkg.Next2", Kind: model.NodeKindFunction, Name: "Next2", FilePath: "n.go", StartLine: 1, EndLine: 5, Language: "go"},
	})
	s, _ := deps.Store.GetNode(ctx, "pkg.Start2")
	n, _ := deps.Store.GetNode(ctx, "pkg.Next2")
	deps.Store.UpsertEdges(ctx, []model.Edge{{FromNodeID: s.ID, ToNodeID: n.ID, Kind: model.EdgeKindCalls, Fingerprint: "s-n"}})

	result := callTool(t, deps, "trace_flow", map[string]any{"qualified_name": "pkg.Start2", "max_nodes": 1})
	if result.IsError {
		t.Fatalf("trace_flow returned error: %s", getTextContent(result))
	}
	var flow map[string]any
	json.Unmarshal([]byte(getTextContent(result)), &flow)
	members := flow["members"].([]any)
	if len(members) != 1 {
		t.Fatalf("members=%d, want 1", len(members))
	}
	metadata := flow["metadata"].(map[string]any)
	if metadata["truncated"] != true || metadata["max_nodes"].(float64) != 1 {
		t.Fatalf("unexpected metadata: %v", metadata)
	}
}

// ============================================================
// 11.1 build_or_update_graph
// ============================================================

func TestBuildOrUpdateGraph_MissingPath(t *testing.T) {
	deps := setupTestDeps(t)
	result := callTool(t, deps, "build_or_update_graph", map[string]any{})
	if !result.IsError {
		t.Fatal("expected error when path is missing")
	}
	text := getTextContent(result)
	if !strings.Contains(text, "missing") && !strings.Contains(text, "path") {
		t.Errorf("expected error about missing path, got: %s", text)
	}
}

func TestBuildOrUpdateGraph_FullRebuild(t *testing.T) {
	deps := setupTestDeps(t)

	dir := t.TempDir()
	deps.RepoRoot = dir
	writeGoFile(t, dir, "hello.go", `package hello

func Hello() string {
	return "hello"
}

`)

	result := callTool(t, deps, "build_or_update_graph", map[string]any{
		"path":         dir,
		"full_rebuild": true,
		"postprocess":  "none",
	})
	if result.IsError {
		t.Fatalf("build_or_update_graph error: %s", getTextContent(result))
	}

	text := getTextContent(result)
	var resp map[string]any
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("expected JSON response, got: %s", text)
	}
	if resp["status"] != "ok" {
		t.Errorf("expected status=ok, got %v", resp["status"])
	}

	// 파싱된 노드가 존재해야 함
	node, err := deps.Store.GetNode(context.Background(), "hello.Hello")
	if err != nil || node == nil {
		t.Fatal("expected node hello.Hello to exist after full rebuild")
	}
}

func TestBuildOrUpdateGraph_RejectsPathOutsideConfiguredRoot(t *testing.T) {
	deps := setupTestDeps(t)
	deps.RepoRoot = t.TempDir()
	outside := t.TempDir()
	writeGoFile(t, outside, "hello.go", `package hello
func Hello() {}
`)

	result := callTool(t, deps, "build_or_update_graph", map[string]any{
		"path":         outside,
		"full_rebuild": true,
		"postprocess":  "none",
	})
	if !result.IsError {
		t.Fatal("expected build_or_update_graph to reject path outside configured root")
	}
}

func TestBuildOrUpdateGraph_Incremental(t *testing.T) {
	deps := setupTestDeps(t)

	mockSync := &mockIncrementalSyncer{
		result: &incremental.SyncStats{Added: 2, Modified: 0, Skipped: 0, Deleted: 0},
	}
	deps.Incremental = mockSync

	dir := t.TempDir()
	writeGoFile(t, dir, "calc.go", `package calc

func Add(a, b int) int {
	return a + b
}
`)

	result := callTool(t, deps, "build_or_update_graph", map[string]any{
		"path":         dir,
		"full_rebuild": false,
		"postprocess":  "none",
	})
	if result.IsError {
		t.Fatalf("build_or_update_graph error: %s", getTextContent(result))
	}

	if !mockSync.syncCalled {
		if !mockSync.syncWithExisting {
			t.Error("expected Incremental.SyncWithExisting to be called for incremental build")
		}
	}
}

func TestBuildOrUpdateGraph_IncrementalIncludePaths(t *testing.T) {
	deps := setupTestDeps(t)

	mockSync := &mockIncrementalSyncer{
		result: &incremental.SyncStats{Added: 1, Modified: 0, Skipped: 0, Deleted: 0},
	}
	deps.Incremental = mockSync

	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "src", "api"), 0o755)
	os.MkdirAll(filepath.Join(dir, "src", "other"), 0o755)
	writeGoFile(t, filepath.Join(dir, "src", "api"), "handler.go", `package api
func Handler() {}
`)
	writeGoFile(t, filepath.Join(dir, "src", "other"), "other.go", `package other
func Other() {}
`)

	callTool(t, deps, "build_or_update_graph", map[string]any{
		"path":          dir,
		"full_rebuild":  false,
		"postprocess":   "none",
		"include_paths": []string{"src/api"},
	})

	if !mockSync.syncWithExisting {
		t.Fatal("expected Incremental.SyncWithExisting to be called")
	}

	for fp := range mockSync.files {
		if !strings.HasPrefix(filepath.ToSlash(fp), "src/api/") {
			t.Errorf("incremental sync received file outside include_paths: %s", fp)
		}
	}

	if len(mockSync.files) == 0 {
		t.Error("expected at least 1 file in incremental sync")
	}
}

func TestBuildOrUpdateGraph_IncrementalIncludePaths_ReplacesPreviousGraphState(t *testing.T) {
	deps := setupTestDeps(t)

	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "src", "api"), 0o755)
	os.MkdirAll(filepath.Join(dir, "src", "other"), 0o755)
	writeGoFile(t, filepath.Join(dir, "src", "api"), "handler.go", `package api
func Handler() {}
`)
	writeGoFile(t, filepath.Join(dir, "src", "other"), "other.go", `package other
func Other() {}
`)

	result := callTool(t, deps, "build_or_update_graph", map[string]any{
		"path":         dir,
		"full_rebuild": true,
		"postprocess":  "none",
	})
	if result.IsError {
		t.Fatalf("initial full build error: %s", getTextContent(result))
	}

	result = callTool(t, deps, "build_or_update_graph", map[string]any{
		"path":          dir,
		"full_rebuild":  false,
		"postprocess":   "none",
		"include_paths": []string{"src/api"},
	})
	if result.IsError {
		t.Fatalf("incremental scoped build error: %s", getTextContent(result))
	}

	node, err := deps.Store.GetNode(context.Background(), "api.Handler")
	if err != nil || node == nil {
		t.Fatal("expected node api.Handler to exist after incremental include_paths build")
	}

	otherNode, _ := deps.Store.GetNode(context.Background(), "other.Other")
	if otherNode != nil {
		t.Error("expected other.Other NOT to exist after incremental include_paths replace semantics")
	}
}

func TestBuildOrUpdateGraph_IncrementalIncludePaths_DefaultsToReplace(t *testing.T) {
	deps := setupGraphOnlyTestDeps(t)

	ctx := ctxns.WithNamespace(context.Background(), "svc")
	if err := deps.Store.UpsertNodes(ctx, []model.Node{
		{QualifiedName: "api.Handler", Kind: model.NodeKindFunction, Name: "Handler", FilePath: filepath.Join("src", "api", "handler.go"), StartLine: 1, EndLine: 2, Language: "go"},
		{QualifiedName: "other.Other", Kind: model.NodeKindFunction, Name: "Other", FilePath: filepath.Join("src", "other", "other.go"), StartLine: 1, EndLine: 2, Language: "go"},
	}); err != nil {
		t.Fatal(err)
	}

	mockSync := &mockIncrementalSyncer{result: &incremental.SyncStats{}}
	deps.Incremental = mockSync

	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "src", "api"), 0o755)
	os.MkdirAll(filepath.Join(dir, "src", "other"), 0o755)
	writeGoFile(t, filepath.Join(dir, "src", "api"), "handler.go", `package api
func Handler() {}
`)
	writeGoFile(t, filepath.Join(dir, "src", "other"), "other.go", `package other
func Other() {}
`)

	callTool(t, deps, "build_or_update_graph", map[string]any{
		"path":          dir,
		"full_rebuild":  false,
		"postprocess":   "none",
		"workspace":     "svc",
		"include_paths": []string{"src/api"},
	})

	if !mockSync.syncWithExisting {
		t.Fatal("expected Incremental.SyncWithExisting to be called")
	}
	if len(mockSync.existingFiles) != 2 {
		t.Fatalf("expected default replace semantics to pass all namespace files, got %v", mockSync.existingFiles)
	}
	if !containsStringInSlice(mockSync.existingFiles, filepath.Join("src", "other", "other.go")) {
		t.Fatalf("expected existingFiles to include out-of-scope file under default replace semantics, got %v", mockSync.existingFiles)
	}
}

func TestBuildOrUpdateGraph_IncrementalIncludePaths_ReplaceFalsePreservesOutOfScopeFiles(t *testing.T) {
	deps := setupGraphOnlyTestDeps(t)

	ctx := ctxns.WithNamespace(context.Background(), "svc")
	if err := deps.Store.UpsertNodes(ctx, []model.Node{
		{QualifiedName: "api.Handler", Kind: model.NodeKindFunction, Name: "Handler", FilePath: filepath.Join("src", "api", "handler.go"), StartLine: 1, EndLine: 2, Language: "go"},
		{QualifiedName: "other.Other", Kind: model.NodeKindFunction, Name: "Other", FilePath: filepath.Join("src", "other", "other.go"), StartLine: 1, EndLine: 2, Language: "go"},
	}); err != nil {
		t.Fatal(err)
	}

	mockSync := &mockIncrementalSyncer{result: &incremental.SyncStats{}}
	deps.Incremental = mockSync

	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "src", "api"), 0o755)
	os.MkdirAll(filepath.Join(dir, "src", "other"), 0o755)
	writeGoFile(t, filepath.Join(dir, "src", "api"), "handler.go", `package api
func Handler() {}
`)
	writeGoFile(t, filepath.Join(dir, "src", "other"), "other.go", `package other
func Other() {}
`)

	callTool(t, deps, "build_or_update_graph", map[string]any{
		"path":          dir,
		"full_rebuild":  false,
		"postprocess":   "none",
		"workspace":     "svc",
		"include_paths": []string{"src/api"},
		"replace":       false,
	})

	if !mockSync.syncWithExisting {
		t.Fatal("expected Incremental.SyncWithExisting to be called")
	}
	if containsStringInSlice(mockSync.existingFiles, filepath.Join("src", "other", "other.go")) {
		t.Fatalf("expected replace=false to exclude out-of-scope file from existingFiles, got %v", mockSync.existingFiles)
	}
	if !containsStringInSlice(mockSync.existingFiles, filepath.Join("src", "api", "handler.go")) {
		t.Fatalf("expected replace=false to keep in-scope file, got %v", mockSync.existingFiles)
	}
}

func TestParseProject_MissingRoot_DoesNotDeleteExistingGraph(t *testing.T) {
	deps := setupTestDeps(t)

	dir := t.TempDir()
	writeGoFile(t, dir, "calc.go", `package calc

func Add(a, b int) int { return a + b }
`)

	result := callTool(t, deps, "parse_project", map[string]any{"path": dir})
	if result.IsError {
		t.Fatalf("initial parse_project error: %s", getTextContent(result))
	}

	missingDir := filepath.Join(dir, "missing-root")
	h := &handlers{deps: deps, cache: NewCache(5 * time.Minute)}
	_, err := h.parseProject(context.Background(), makeToolRequest("parse_project", map[string]any{"path": missingDir}))
	if err == nil {
		t.Fatal("expected parse_project on missing root to fail")
	}

	node, err := deps.Store.GetNode(context.Background(), "calc.Add")
	if err != nil || node == nil {
		t.Fatal("expected existing graph to remain after missing-root parse_project failure")
	}
}

func TestParseProject_ReadFailure_PreservesExistingGraph(t *testing.T) {
	deps := setupTestDeps(t)

	dir := t.TempDir()
	writeGoFile(t, dir, "calc.go", `package calc

func Add(a, b int) int { return a + b }
`)

	result := callTool(t, deps, "parse_project", map[string]any{"path": dir})
	if result.IsError {
		t.Fatalf("initial parse_project error: %s", getTextContent(result))
	}

	if err := os.Remove(filepath.Join(dir, "calc.go")); err != nil {
		t.Fatalf("remove file: %v", err)
	}
	if err := os.Symlink(filepath.Join(dir, "missing.go"), filepath.Join(dir, "calc.go")); err != nil {
		t.Fatalf("create broken symlink: %v", err)
	}

	h := &handlers{deps: deps, cache: NewCache(5 * time.Minute)}
	_, err := h.parseProject(context.Background(), makeToolRequest("parse_project", map[string]any{"path": dir}))
	if err == nil {
		t.Fatal("expected parse_project to fail on unreadable file")
	}

	node, err := deps.Store.GetNode(context.Background(), "calc.Add")
	if err != nil || node == nil {
		t.Fatal("expected existing graph to remain after read failure")
	}
}

func TestParseProject_MaxFileBytesPreservesExistingGraph(t *testing.T) {
	deps := setupTestDeps(t)
	deps.MaxFileBytes = 32

	dir := t.TempDir()
	writeGoFile(t, dir, "calc.go", `package calc

func Add(a, b int) int { return a + b }
`)

	deps.MaxFileBytes = 0
	result := callTool(t, deps, "parse_project", map[string]any{"path": dir})
	if result.IsError {
		t.Fatalf("initial parse_project error: %s", getTextContent(result))
	}

	deps.MaxFileBytes = 32
	writeGoFile(t, dir, "large.go", `package calc

func ThisFileIsTooLargeForTheConfiguredParseLimit() string { return "oversized" }
`)

	h := &handlers{deps: deps, cache: NewCache(5 * time.Minute)}
	_, err := h.parseProject(context.Background(), makeToolRequest("parse_project", map[string]any{"path": dir}))
	if err == nil {
		t.Fatal("expected parse_project to fail on max file bytes")
	}
	if !strings.Contains(err.Error(), "exceeds max file bytes") {
		t.Fatalf("expected max file bytes error, got %v", err)
	}

	node, err := deps.Store.GetNode(context.Background(), "calc.Add")
	if err != nil || node == nil {
		t.Fatal("expected existing graph to remain after max file bytes failure")
	}
}

func TestParseProject_ContextCanceledPreservesExistingGraph(t *testing.T) {
	deps := setupTestDeps(t)

	dir := t.TempDir()
	writeGoFile(t, dir, "calc.go", `package calc

func Add(a, b int) int { return a + b }
`)

	result := callTool(t, deps, "parse_project", map[string]any{"path": dir})
	if result.IsError {
		t.Fatalf("initial parse_project error: %s", getTextContent(result))
	}

	writeGoFile(t, dir, "calc.go", `package calc

func Replaced() {}
`)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()

	h := &handlers{deps: deps, cache: NewCache(5 * time.Minute)}
	_, err := h.parseProject(canceled, makeToolRequest("parse_project", map[string]any{"path": dir}))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}

	node, err := deps.Store.GetNode(context.Background(), "calc.Add")
	if err != nil || node == nil {
		t.Fatal("expected existing graph to remain after cancellation")
	}
}

func TestBuildOrUpdateGraph_PostprocessFull(t *testing.T) {
	deps := setupTestDeps(t)

	mockComm := &mockCommunityBuilder{
		result: []community.Stats{},
	}
	deps.CommunityBuilder = mockComm

	dir := t.TempDir()
	writeGoFile(t, dir, "svc.go", `package svc

func Run() {}
`)

	result := callTool(t, deps, "build_or_update_graph", map[string]any{
		"path":         dir,
		"full_rebuild": true,
		"postprocess":  "full",
	})
	if result.IsError {
		t.Fatalf("build_or_update_graph error: %s", getTextContent(result))
	}

	if !mockComm.rebuildCalled {
		t.Error("expected CommunityBuilder.Rebuild to be called for postprocess=full")
	}
}

func TestBuildOrUpdateGraph_PostprocessNone(t *testing.T) {
	deps := setupTestDeps(t)

	mockComm := &mockCommunityBuilder{}
	deps.CommunityBuilder = mockComm

	dir := t.TempDir()
	writeGoFile(t, dir, "svc.go", `package svc

func Run() {}
`)

	result := callTool(t, deps, "build_or_update_graph", map[string]any{
		"path":         dir,
		"full_rebuild": true,
		"postprocess":  "none",
	})
	if result.IsError {
		t.Fatalf("build_or_update_graph error: %s", getTextContent(result))
	}

	if mockComm.rebuildCalled {
		t.Error("expected CommunityBuilder.Rebuild NOT to be called for postprocess=none")
	}
}

func TestBuildOrUpdateGraph_DegradedOnCommunityFailure(t *testing.T) {
	deps := setupTestDeps(t)
	deps.CommunityBuilder = &mockCommunityBuilder{err: errors.New("community rebuild boom")}

	dir := t.TempDir()
	writeGoFile(t, dir, "svc.go", `package svc

func Run() {}
`)

	result := callTool(t, deps, "build_or_update_graph", map[string]any{
		"path":         dir,
		"full_rebuild": true,
		"postprocess":  "full",
	})
	if result.IsError {
		t.Fatalf("build_or_update_graph should not return tool error, got: %s", getTextContent(result))
	}

	var resp map[string]any
	if err := json.Unmarshal([]byte(getTextContent(result)), &resp); err != nil {
		t.Fatalf("expected JSON, got: %s", getTextContent(result))
	}
	if resp["status"] != "degraded" {
		t.Fatalf("expected status=degraded, got %v", resp["status"])
	}
	failedSteps, ok := resp["failed_steps"].([]any)
	if !ok || len(failedSteps) == 0 {
		t.Fatalf("expected failed_steps to be non-empty, got %v", resp["failed_steps"])
	}
	if !containsString(failedSteps, "communities") {
		t.Fatalf("expected communities in failed_steps, got %v", failedSteps)
	}
}

func TestBuildOrUpdateGraph_DegradedOnSearchDocumentRefreshFailure(t *testing.T) {
	deps := setupTestDeps(t)
	origRefresh := refreshSearchDocuments
	defer func() { refreshSearchDocuments = origRefresh }()
	refreshSearchDocuments = func(ctx context.Context, db *gorm.DB) (int, error) {
		return 0, errors.New("search document refresh boom")
	}

	dir := t.TempDir()
	writeGoFile(t, dir, "svc.go", `package svc

func Run() {}
`)

	result := callTool(t, deps, "build_or_update_graph", map[string]any{
		"path":         dir,
		"full_rebuild": true,
		"postprocess":  "full",
	})
	if result.IsError {
		t.Fatalf("build_or_update_graph should not return tool error, got: %s", getTextContent(result))
	}

	var resp map[string]any
	if err := json.Unmarshal([]byte(getTextContent(result)), &resp); err != nil {
		t.Fatalf("expected JSON, got: %s", getTextContent(result))
	}
	if resp["status"] != "degraded" {
		t.Fatalf("expected status=degraded, got %v", resp["status"])
	}
	failedSteps, ok := resp["failed_steps"].([]any)
	if !ok || len(failedSteps) == 0 {
		t.Fatalf("expected failed_steps to be non-empty, got %v", resp["failed_steps"])
	}
	if !containsString(failedSteps, "search_documents") {
		t.Fatalf("expected search_documents in failed_steps, got %v", failedSteps)
	}
}

func TestBuildOrUpdateGraph_DegradedOnFTSFailure(t *testing.T) {
	deps := setupTestDeps(t)
	deps.SearchBackend = &failSearchBackend{err: errors.New("fts rebuild boom")}

	dir := t.TempDir()
	writeGoFile(t, dir, "svc.go", `package svc

func Run() {}
`)

	result := callTool(t, deps, "build_or_update_graph", map[string]any{
		"path":         dir,
		"full_rebuild": true,
		"postprocess":  "full",
	})
	if result.IsError {
		t.Fatalf("build_or_update_graph should not return tool error, got: %s", getTextContent(result))
	}

	var resp map[string]any
	if err := json.Unmarshal([]byte(getTextContent(result)), &resp); err != nil {
		t.Fatalf("expected JSON, got: %s", getTextContent(result))
	}
	if resp["status"] != "degraded" {
		t.Fatalf("expected status=degraded, got %v", resp["status"])
	}
	failedSteps, ok := resp["failed_steps"].([]any)
	if !ok || len(failedSteps) == 0 {
		t.Fatalf("expected failed_steps to be non-empty, got %v", resp["failed_steps"])
	}
	if !containsString(failedSteps, "fts") {
		t.Fatalf("expected fts in failed_steps, got %v", failedSteps)
	}
}

func containsString(values []any, target string) bool {
	for _, v := range values {
		if s, ok := v.(string); ok && s == target {
			return true
		}
	}
	return false
}

func containsStringInSlice(values []string, target string) bool {
	return slices.Contains(values, target)
}

func TestBuildOrUpdateGraph_IncludePaths(t *testing.T) {
	deps := setupTestDeps(t)

	dir := t.TempDir()

	apiDir := filepath.Join(dir, "src", "api")
	os.MkdirAll(apiDir, 0755)
	writeGoFile(t, apiDir, "handler.go", `package api
func Handler() {}
`)

	otherDir := filepath.Join(dir, "src", "other")
	os.MkdirAll(otherDir, 0755)
	writeGoFile(t, otherDir, "other.go", `package other
func Other() {}
`)

	result := callTool(t, deps, "build_or_update_graph", map[string]any{
		"path":          dir,
		"full_rebuild":  true,
		"postprocess":   "none",
		"include_paths": []string{"src/api"},
	})
	if result.IsError {
		t.Fatalf("build_or_update_graph error: %s", getTextContent(result))
	}

	node, err := deps.Store.GetNode(context.Background(), "api.Handler")
	if err != nil || node == nil {
		t.Fatal("expected node api.Handler to exist (in include_paths)")
	}

	otherNode, _ := deps.Store.GetNode(context.Background(), "other.Other")
	if otherNode != nil {
		t.Error("expected other.Other NOT to exist (not in include_paths)")
	}
}

func TestParseProject_IncludePaths(t *testing.T) {
	deps := setupTestDeps(t)

	dir := t.TempDir()

	apiDir := filepath.Join(dir, "src", "api")
	os.MkdirAll(apiDir, 0755)
	writeGoFile(t, apiDir, "handler.go", `package api
func Handler() {}
`)

	otherDir := filepath.Join(dir, "src", "other")
	os.MkdirAll(otherDir, 0755)
	writeGoFile(t, otherDir, "other.go", `package other
func Other() {}
`)

	result := callTool(t, deps, "parse_project", map[string]any{
		"path":          dir,
		"include_paths": []string{"src/api"},
	})
	if result.IsError {
		t.Fatalf("parse_project error: %s", getTextContent(result))
	}

	node, err := deps.Store.GetNode(context.Background(), "api.Handler")
	if err != nil || node == nil {
		t.Fatal("expected node api.Handler to exist (in include_paths)")
	}

	otherNode, _ := deps.Store.GetNode(context.Background(), "other.Other")
	if otherNode != nil {
		t.Error("expected other.Other NOT to exist (not in include_paths)")
	}
}

// ============================================================
// 11.2 run_postprocess
// ============================================================

func TestRunPostprocess_AllEnabled(t *testing.T) {
	deps := setupTestDeps(t)

	mockComm := &mockCommunityBuilder{result: []community.Stats{}}
	deps.CommunityBuilder = mockComm

	result := callTool(t, deps, "run_postprocess", map[string]any{
		"flows":       true,
		"communities": true,
		"fts":         true,
	})
	if result.IsError {
		t.Fatalf("run_postprocess error: %s", getTextContent(result))
	}

	text := getTextContent(result)
	var resp map[string]any
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("expected JSON, got: %s", text)
	}
	if resp["status"] != "ok" {
		t.Errorf("expected status=ok, got %v", resp["status"])
	}
	if !mockComm.rebuildCalled {
		t.Error("expected CommunityBuilder.Rebuild to be called")
	}
}

func TestRunPostprocess_OnlyFTS(t *testing.T) {
	deps := setupTestDeps(t)

	mockComm := &mockCommunityBuilder{}
	deps.CommunityBuilder = mockComm

	result := callTool(t, deps, "run_postprocess", map[string]any{
		"flows":       false,
		"communities": false,
		"fts":         true,
	})
	if result.IsError {
		t.Fatalf("run_postprocess error: %s", getTextContent(result))
	}

	if mockComm.rebuildCalled {
		t.Error("expected CommunityBuilder.Rebuild NOT to be called")
	}
}

func TestRunPostprocess_NoneEnabled(t *testing.T) {
	deps := setupTestDeps(t)

	result := callTool(t, deps, "run_postprocess", map[string]any{
		"flows":       false,
		"communities": false,
		"fts":         false,
	})
	if result.IsError {
		t.Fatalf("run_postprocess error: %s", getTextContent(result))
	}

	text := getTextContent(result)
	var resp map[string]any
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("expected JSON, got: %s", text)
	}
	if resp["status"] != "ok" {
		t.Errorf("expected status=ok, got %v", resp["status"])
	}
}

func TestRunPostprocess_RejectsInvalidCommunityDepth(t *testing.T) {
	for _, depth := range []int{0, 9} {
		t.Run(fmt.Sprintf("depth-%d", depth), func(t *testing.T) {
			deps := setupTestDeps(t)
			mockComm := &mockCommunityBuilder{}
			deps.CommunityBuilder = mockComm

			result := callTool(t, deps, "run_postprocess", map[string]any{
				"communities":     true,
				"fts":             false,
				"community_depth": depth,
			})
			if !result.IsError {
				t.Fatalf("expected community_depth=%d to be rejected", depth)
			}
			if mockComm.rebuildCalled {
				t.Fatal("community rebuild should not run for invalid depth")
			}
		})
	}
}

// ============================================================
// 11.3 query_graph
// ============================================================

func TestQueryGraph_CallersOf(t *testing.T) {
	deps := setupTestDeps(t)
	ctx := context.Background()

	mockQ := &mockQueryService{
		result: []model.Node{
			{QualifiedName: "pkg.Caller", Kind: model.NodeKindFunction, Name: "Caller", FilePath: "caller.go"},
		},
	}
	deps.QueryService = mockQ

	deps.Store.UpsertNodes(ctx, []model.Node{
		{QualifiedName: "pkg.Func", Kind: model.NodeKindFunction, Name: "Func", FilePath: "func.go", StartLine: 1, EndLine: 5, Language: "go"},
	})

	result := callTool(t, deps, "query_graph", map[string]any{"pattern": "callers_of", "target": "pkg.Func"})
	if result.IsError {
		t.Fatalf("query_graph error: %s", getTextContent(result))
	}
	if !mockQ.callersOfCalled {
		t.Error("expected CallersOf to be called")
	}
}

func TestQueryGraph_CalleesOf(t *testing.T) {
	deps := setupTestDeps(t)
	ctx := context.Background()

	mockQ := &mockQueryService{
		result: []model.Node{
			{QualifiedName: "pkg.Callee", Kind: model.NodeKindFunction, Name: "Callee", FilePath: "callee.go"},
		},
	}
	deps.QueryService = mockQ

	deps.Store.UpsertNodes(ctx, []model.Node{
		{QualifiedName: "pkg.Func", Kind: model.NodeKindFunction, Name: "Func", FilePath: "func.go", StartLine: 1, EndLine: 5, Language: "go"},
	})

	result := callTool(t, deps, "query_graph", map[string]any{"pattern": "callees_of", "target": "pkg.Func"})
	if result.IsError {
		t.Fatalf("query_graph error: %s", getTextContent(result))
	}
	if !mockQ.calleesOfCalled {
		t.Error("expected CalleesOf to be called")
	}
}

func TestQueryGraph_ImportsOf(t *testing.T) {
	deps := setupTestDeps(t)
	ctx := context.Background()

	mockQ := &mockQueryService{result: []model.Node{}}
	deps.QueryService = mockQ

	deps.Store.UpsertNodes(ctx, []model.Node{
		{QualifiedName: "pkg.Func", Kind: model.NodeKindFunction, Name: "Func", FilePath: "func.go", StartLine: 1, EndLine: 5, Language: "go"},
	})

	result := callTool(t, deps, "query_graph", map[string]any{"pattern": "imports_of", "target": "pkg.Func"})
	if result.IsError {
		t.Fatalf("query_graph error: %s", getTextContent(result))
	}
	if !mockQ.importsOfCalled {
		t.Error("expected ImportsOf to be called")
	}
}

func TestQueryGraph_ImportersOf(t *testing.T) {
	deps := setupTestDeps(t)
	ctx := context.Background()

	mockQ := &mockQueryService{result: []model.Node{}}
	deps.QueryService = mockQ

	deps.Store.UpsertNodes(ctx, []model.Node{
		{QualifiedName: "pkg.Func", Kind: model.NodeKindFunction, Name: "Func", FilePath: "func.go", StartLine: 1, EndLine: 5, Language: "go"},
	})

	result := callTool(t, deps, "query_graph", map[string]any{"pattern": "importers_of", "target": "pkg.Func"})
	if result.IsError {
		t.Fatalf("query_graph error: %s", getTextContent(result))
	}
	if !mockQ.importersOfCalled {
		t.Error("expected ImportersOf to be called")
	}
}

func TestQueryGraph_ChildrenOf(t *testing.T) {
	deps := setupTestDeps(t)
	ctx := context.Background()

	mockQ := &mockQueryService{result: []model.Node{}}
	deps.QueryService = mockQ

	deps.Store.UpsertNodes(ctx, []model.Node{
		{QualifiedName: "pkg.Class", Kind: model.NodeKindClass, Name: "Class", FilePath: "class.go", StartLine: 1, EndLine: 50, Language: "go"},
	})

	result := callTool(t, deps, "query_graph", map[string]any{"pattern": "children_of", "target": "pkg.Class"})
	if result.IsError {
		t.Fatalf("query_graph error: %s", getTextContent(result))
	}
	if !mockQ.childrenOfCalled {
		t.Error("expected ChildrenOf to be called")
	}
}

func TestQueryGraph_TestsFor(t *testing.T) {
	deps := setupTestDeps(t)
	ctx := context.Background()

	mockQ := &mockQueryService{result: []model.Node{}}
	deps.QueryService = mockQ

	deps.Store.UpsertNodes(ctx, []model.Node{
		{QualifiedName: "pkg.Func", Kind: model.NodeKindFunction, Name: "Func", FilePath: "func.go", StartLine: 1, EndLine: 5, Language: "go"},
	})

	result := callTool(t, deps, "query_graph", map[string]any{"pattern": "tests_for", "target": "pkg.Func"})
	if result.IsError {
		t.Fatalf("query_graph error: %s", getTextContent(result))
	}
	if !mockQ.testsForCalled {
		t.Error("expected TestsFor to be called")
	}
}

func TestQueryGraph_InheritorsOf(t *testing.T) {
	deps := setupTestDeps(t)
	ctx := context.Background()

	mockQ := &mockQueryService{result: []model.Node{}}
	deps.QueryService = mockQ

	deps.Store.UpsertNodes(ctx, []model.Node{
		{QualifiedName: "pkg.Base", Kind: model.NodeKindClass, Name: "Base", FilePath: "base.go", StartLine: 1, EndLine: 10, Language: "go"},
	})

	result := callTool(t, deps, "query_graph", map[string]any{"pattern": "inheritors_of", "target": "pkg.Base"})
	if result.IsError {
		t.Fatalf("query_graph error: %s", getTextContent(result))
	}
	if !mockQ.inheritorsOfCalled {
		t.Error("expected InheritorsOf to be called")
	}
}

func TestQueryGraph_FileSummary(t *testing.T) {
	deps := setupTestDeps(t)

	mockQ := &mockQueryService{
		fileSummaryResult: &query.FileSummary{
			FilePath: "path/file.go", Functions: 3, Classes: 1, Total: 4,
		},
	}
	deps.QueryService = mockQ

	result := callTool(t, deps, "query_graph", map[string]any{"pattern": "file_summary", "target": "path/file.go"})
	if result.IsError {
		t.Fatalf("query_graph error: %s", getTextContent(result))
	}
	if !mockQ.fileSummaryCalled {
		t.Error("expected FileSummaryOf to be called")
	}

	text := getTextContent(result)
	var resp map[string]any
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("expected JSON, got: %s", text)
	}
	if resp["pattern"] != "file_summary" {
		t.Errorf("expected pattern=file_summary, got %v", resp["pattern"])
	}
}

func TestQueryGraph_InvalidPattern(t *testing.T) {
	deps := setupTestDeps(t)

	result := callTool(t, deps, "query_graph", map[string]any{"pattern": "invalid_xyz", "target": "pkg.Func"})
	if !result.IsError {
		t.Fatal("expected error for invalid pattern")
	}
	text := getTextContent(result)
	if !strings.Contains(text, "unknown pattern") {
		t.Errorf("expected unknown pattern error, got: %s", text)
	}
}

func TestQueryGraph_TargetNotFound(t *testing.T) {
	deps := setupTestDeps(t)

	mockQ := &mockQueryService{result: []model.Node{}}
	deps.QueryService = mockQ

	result := callTool(t, deps, "query_graph", map[string]any{"pattern": "callers_of", "target": "nonexistent.Func"})
	if result.IsError {
		// target not found은 에러가 아니라 빈 결과를 반환
		text := getTextContent(result)
		if !strings.Contains(text, "not found") {
			t.Errorf("expected not found message, got: %s", text)
		}
	}
}

// ============================================================
// 11.4 list_graph_stats
// ============================================================

func TestListGraphStats_ReturnsAllCounts(t *testing.T) {
	deps := setupTestDeps(t)
	ctx := context.Background()

	deps.Store.UpsertNodes(ctx, []model.Node{
		{QualifiedName: "pkg.Func1", Kind: model.NodeKindFunction, Name: "Func1", FilePath: "a.go", StartLine: 1, EndLine: 5, Language: "go"},
		{QualifiedName: "pkg.Func2", Kind: model.NodeKindFunction, Name: "Func2", FilePath: "a.go", StartLine: 10, EndLine: 20, Language: "go"},
		{QualifiedName: "pkg.Class1", Kind: model.NodeKindClass, Name: "Class1", FilePath: "b.py", StartLine: 1, EndLine: 30, Language: "python"},
	})
	n1, _ := deps.Store.GetNode(ctx, "pkg.Func1")
	n2, _ := deps.Store.GetNode(ctx, "pkg.Func2")
	deps.Store.UpsertEdges(ctx, []model.Edge{
		{FromNodeID: n1.ID, ToNodeID: n2.ID, Kind: model.EdgeKindCalls, Fingerprint: "c-f1-f2"},
	})

	result := callTool(t, deps, "list_graph_stats", map[string]any{})
	if result.IsError {
		t.Fatalf("list_graph_stats error: %s", getTextContent(result))
	}

	text := getTextContent(result)
	var resp map[string]any
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("expected JSON, got: %s", text)
	}
	if resp["total_nodes"].(float64) != 3 {
		t.Errorf("expected 3 nodes, got %v", resp["total_nodes"])
	}
	if resp["total_edges"].(float64) != 1 {
		t.Errorf("expected 1 edge, got %v", resp["total_edges"])
	}
	nbk := resp["nodes_by_kind"].(map[string]any)
	if nbk["function"].(float64) != 2 {
		t.Errorf("expected 2 functions, got %v", nbk["function"])
	}
	nbl := resp["nodes_by_language"].(map[string]any)
	if nbl["go"].(float64) != 2 {
		t.Errorf("expected 2 go nodes, got %v", nbl["go"])
	}
}

func TestListGraphStats_EmptyDB(t *testing.T) {
	deps := setupTestDeps(t)

	result := callTool(t, deps, "list_graph_stats", map[string]any{})
	if result.IsError {
		t.Fatalf("list_graph_stats error: %s", getTextContent(result))
	}

	text := getTextContent(result)
	var resp map[string]any
	json.Unmarshal([]byte(text), &resp)
	if resp["total_nodes"].(float64) != 0 {
		t.Errorf("expected 0 nodes, got %v", resp["total_nodes"])
	}
}

// ============================================================
// 11.5 find_large_functions
// ============================================================

func TestFindLargeFunctions_DefaultThreshold(t *testing.T) {
	deps := setupTestDeps(t)

	mockLF := &mockLargefuncAnalyzer{
		result: []model.Node{
			{QualifiedName: "pkg.Big", Kind: model.NodeKindFunction, Name: "Big", FilePath: "big.go", StartLine: 1, EndLine: 100},
		},
	}
	deps.LargefuncAnalyzer = mockLF

	result := callTool(t, deps, "find_large_functions", map[string]any{})
	if result.IsError {
		t.Fatalf("find_large_functions error: %s", getTextContent(result))
	}
	if !mockLF.findCalled {
		t.Error("expected Find to be called")
	}
}

func TestFindLargeFunctions_CustomThreshold(t *testing.T) {
	deps := setupTestDeps(t)

	mockLF := &mockLargefuncAnalyzer{
		result: []model.Node{
			{QualifiedName: "pkg.Medium", Kind: model.NodeKindFunction, Name: "Medium", FilePath: "med.go", StartLine: 1, EndLine: 40},
		},
	}
	deps.LargefuncAnalyzer = mockLF

	result := callTool(t, deps, "find_large_functions", map[string]any{"min_lines": 30})
	if result.IsError {
		t.Fatalf("find_large_functions error: %s", getTextContent(result))
	}

	text := getTextContent(result)
	var resp map[string]any
	json.Unmarshal([]byte(text), &resp)
	if resp["count"].(float64) != 1 {
		t.Errorf("expected 1 result, got %v", resp["count"])
	}
}

func TestFindLargeFunctions_Limit(t *testing.T) {
	deps := setupTestDeps(t)

	mockLF := &mockLargefuncAnalyzer{
		result: []model.Node{
			{QualifiedName: "pkg.A", Kind: model.NodeKindFunction, Name: "A", FilePath: "a.go", StartLine: 1, EndLine: 100},
			{QualifiedName: "pkg.B", Kind: model.NodeKindFunction, Name: "B", FilePath: "b.go", StartLine: 1, EndLine: 80},
			{QualifiedName: "pkg.C", Kind: model.NodeKindFunction, Name: "C", FilePath: "c.go", StartLine: 1, EndLine: 60},
			{QualifiedName: "pkg.D", Kind: model.NodeKindFunction, Name: "D", FilePath: "d.go", StartLine: 1, EndLine: 55},
		},
	}
	deps.LargefuncAnalyzer = mockLF

	result := callTool(t, deps, "find_large_functions", map[string]any{"limit": 3})
	if result.IsError {
		t.Fatalf("find_large_functions error: %s", getTextContent(result))
	}

	text := getTextContent(result)
	var resp map[string]any
	json.Unmarshal([]byte(text), &resp)
	if resp["count"].(float64) != 3 {
		t.Errorf("expected 3 results (limit), got %v", resp["count"])
	}
}

func TestFindLargeFunctions_NoResults(t *testing.T) {
	deps := setupTestDeps(t)

	mockLF := &mockLargefuncAnalyzer{result: []model.Node{}}
	deps.LargefuncAnalyzer = mockLF

	result := callTool(t, deps, "find_large_functions", map[string]any{"min_lines": 1000})
	if result.IsError {
		t.Fatalf("find_large_functions error: %s", getTextContent(result))
	}

	text := getTextContent(result)
	var resp map[string]any
	json.Unmarshal([]byte(text), &resp)
	if resp["count"].(float64) != 0 {
		t.Errorf("expected 0 results, got %v", resp["count"])
	}
}

func TestFindLargeFunctions_InvalidLimit(t *testing.T) {
	deps := setupTestDeps(t)

	result := callTool(t, deps, "find_large_functions", map[string]any{"limit": 0})
	if !result.IsError {
		t.Fatal("expected invalid limit to return tool error")
	}
	if !strings.Contains(getTextContent(result), "limit must be > 0") {
		t.Fatalf("unexpected error: %s", getTextContent(result))
	}
}

func TestFindLargeFunctions_PathFilter_RespectsPathBoundary(t *testing.T) {
	deps := setupTestDeps(t)
	deps.LargefuncAnalyzer = &mockLargefuncAnalyzer{result: []model.Node{
		{QualifiedName: "pkg.API", Kind: model.NodeKindFunction, Name: "API", FilePath: "internal/api/handler.go", StartLine: 1, EndLine: 100},
		{QualifiedName: "pkg.API2", Kind: model.NodeKindFunction, Name: "API2", FilePath: "internal/api2/handler.go", StartLine: 1, EndLine: 120},
	}}

	result := callTool(t, deps, "find_large_functions", map[string]any{"path": "internal/api"})
	if result.IsError {
		t.Fatalf("find_large_functions error: %s", getTextContent(result))
	}

	var resp map[string]any
	if err := json.Unmarshal([]byte(getTextContent(result)), &resp); err != nil {
		t.Fatalf("expected JSON, got: %s", getTextContent(result))
	}
	if resp["count"].(float64) != 1 {
		t.Fatalf("expected 1 boundary-safe result, got %v", resp["count"])
	}
	results := resp["results"].([]any)
	entry := results[0].(map[string]any)
	if entry["file"] != "internal/api/handler.go" {
		t.Fatalf("unexpected file: %v", entry["file"])
	}
}

// ============================================================
// 11.6 detect_changes
// ============================================================

func TestDetectChanges_ReturnsRiskEntries(t *testing.T) {
	deps := setupTestDeps(t)
	ctx := context.Background()

	deps.Store.UpsertNodes(ctx, []model.Node{
		{QualifiedName: "pkg.Changed", Kind: model.NodeKindFunction, Name: "Changed", FilePath: "changed.go", StartLine: 1, EndLine: 20, Language: "go"},
	})

	deps.ChangesGitClient = &mockGitClient{
		changedFiles: []string{"changed.go"},
		hunks:        []changes.Hunk{{FilePath: "changed.go", StartLine: 5, EndLine: 15}},
	}
	repoRoot := t.TempDir()
	deps.RepoRoot = repoRoot

	result := callTool(t, deps, "detect_changes", map[string]any{"repo_root": repoRoot})
	if result.IsError {
		t.Fatalf("detect_changes error: %s", getTextContent(result))
	}

	text := getTextContent(result)
	var resp map[string]any
	json.Unmarshal([]byte(text), &resp)
	entries := resp["entries"].([]any)
	if len(entries) == 0 {
		t.Error("expected at least 1 risk entry")
	}
}

func TestDetectChanges_DefaultBase(t *testing.T) {
	deps := setupTestDeps(t)

	mock := &mockGitClient{
		changedFiles: []string{},
		hunks:        []changes.Hunk{},
	}
	deps.ChangesGitClient = mock
	repoRoot := t.TempDir()
	deps.RepoRoot = repoRoot

	callTool(t, deps, "detect_changes", map[string]any{"repo_root": repoRoot})

	if mock.lastBaseRef != "HEAD~1" {
		t.Errorf("expected default base HEAD~1, got %q", mock.lastBaseRef)
	}
}

func TestDetectChanges_EmptyDiff(t *testing.T) {
	deps := setupTestDeps(t)

	deps.ChangesGitClient = &mockGitClient{
		changedFiles: []string{},
		hunks:        []changes.Hunk{},
	}
	repoRoot := t.TempDir()
	deps.RepoRoot = repoRoot

	result := callTool(t, deps, "detect_changes", map[string]any{"repo_root": repoRoot})
	if result.IsError {
		t.Fatalf("detect_changes error: %s", getTextContent(result))
	}

	text := getTextContent(result)
	var resp map[string]any
	json.Unmarshal([]byte(text), &resp)
	entries := resp["entries"].([]any)
	if len(entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(entries))
	}
}

func TestDetectChanges_MissingRepoRoot(t *testing.T) {
	deps := setupTestDeps(t)

	result := callTool(t, deps, "detect_changes", map[string]any{})
	if !result.IsError {
		t.Fatal("expected error when repo_root is missing")
	}
}

func TestDetectChanges_RejectsRepoRootOutsideConfiguredRoot(t *testing.T) {
	deps := setupTestDeps(t)
	deps.ChangesGitClient = &mockGitClient{}
	deps.RepoRoot = t.TempDir()
	outside := t.TempDir()

	result := callTool(t, deps, "detect_changes", map[string]any{"repo_root": outside})
	if !result.IsError {
		t.Fatal("expected outside repo_root to return tool error")
	}
	if !strings.Contains(getTextContent(result), "outside configured analysis root") {
		t.Fatalf("unexpected error: %s", getTextContent(result))
	}
}

// ============================================================
// 11.7 get_affected_flows
// ============================================================

func TestGetAffectedFlows_ReturnsFlows(t *testing.T) {
	deps := setupTestDeps(t)
	ctx := context.Background()

	deps.Store.UpsertNodes(ctx, []model.Node{
		{QualifiedName: "pkg.FnA", Kind: model.NodeKindFunction, Name: "FnA", FilePath: "a.go", StartLine: 1, EndLine: 10, Language: "go"},
	})
	nodeA, _ := deps.Store.GetNode(ctx, "pkg.FnA")

	// Flow 생성
	deps.DB.Create(&model.Flow{Name: "login-flow", Description: "Login flow"})
	var flow model.Flow
	deps.DB.First(&flow)
	deps.DB.Create(&model.FlowMembership{FlowID: flow.ID, NodeID: nodeA.ID, Ordinal: 0})

	deps.ChangesGitClient = &mockGitClient{
		changedFiles: []string{"a.go"},
		hunks:        []changes.Hunk{{FilePath: "a.go", StartLine: 1, EndLine: 10}},
	}
	repoRoot := t.TempDir()
	deps.RepoRoot = repoRoot

	result := callTool(t, deps, "get_affected_flows", map[string]any{"repo_root": repoRoot})
	if result.IsError {
		t.Fatalf("get_affected_flows error: %s", getTextContent(result))
	}

	text := getTextContent(result)
	var resp map[string]any
	json.Unmarshal([]byte(text), &resp)
	flows := resp["affected_flows"].([]any)
	if len(flows) == 0 {
		t.Error("expected at least 1 affected flow")
	}
}

func TestGetAffectedFlows_RespectsWorkspaceNamespace(t *testing.T) {
	deps := setupTestDeps(t)
	ctx := ctxns.WithNamespace(context.Background(), "alpha")

	if err := deps.Store.UpsertNodes(ctx, []model.Node{{QualifiedName: "pkg.FnA", Kind: model.NodeKindFunction, Name: "FnA", FilePath: "a.go", StartLine: 1, EndLine: 10, Language: "go"}}); err != nil {
		t.Fatal(err)
	}
	nodeA, _ := deps.Store.GetNode(ctx, "pkg.FnA")
	deps.DB.Create(&model.Flow{Namespace: "alpha", Name: "alpha-flow"})
	deps.DB.Create(&model.Flow{Namespace: "beta", Name: "beta-flow"})
	var alphaFlow, betaFlow model.Flow
	deps.DB.First(&alphaFlow, "name = ?", "alpha-flow")
	deps.DB.First(&betaFlow, "name = ?", "beta-flow")
	deps.DB.Create(&model.FlowMembership{Namespace: "alpha", FlowID: alphaFlow.ID, NodeID: nodeA.ID, Ordinal: 0})
	deps.DB.Create(&model.FlowMembership{Namespace: "beta", FlowID: betaFlow.ID, NodeID: nodeA.ID, Ordinal: 0})

	deps.ChangesGitClient = &mockGitClient{changedFiles: []string{"a.go"}, hunks: []changes.Hunk{{FilePath: "a.go", StartLine: 1, EndLine: 10}}}
	repoRoot := t.TempDir()
	deps.RepoRoot = repoRoot
	result := callTool(t, deps, "get_affected_flows", map[string]any{"repo_root": repoRoot, "workspace": "alpha"})
	if result.IsError {
		t.Fatalf("get_affected_flows error: %s", getTextContent(result))
	}
	var resp map[string]any
	json.Unmarshal([]byte(getTextContent(result)), &resp)
	flows := resp["affected_flows"].([]any)
	if len(flows) != 1 {
		t.Fatalf("expected 1 affected flow, got %d", len(flows))
	}
	if flows[0].(map[string]any)["name"] != "alpha-flow" {
		t.Fatalf("affected flow = %v, want alpha-flow", flows[0])
	}
}

func TestGetAffectedFlows_NoFlows(t *testing.T) {
	deps := setupTestDeps(t)
	ctx := context.Background()

	deps.Store.UpsertNodes(ctx, []model.Node{
		{QualifiedName: "pkg.FnB", Kind: model.NodeKindFunction, Name: "FnB", FilePath: "b.go", StartLine: 1, EndLine: 10, Language: "go"},
	})

	deps.ChangesGitClient = &mockGitClient{
		changedFiles: []string{"b.go"},
		hunks:        []changes.Hunk{{FilePath: "b.go", StartLine: 1, EndLine: 10}},
	}
	repoRoot := t.TempDir()
	deps.RepoRoot = repoRoot

	result := callTool(t, deps, "get_affected_flows", map[string]any{"repo_root": repoRoot})
	if result.IsError {
		t.Fatalf("get_affected_flows error: %s", getTextContent(result))
	}

	text := getTextContent(result)
	var resp map[string]any
	json.Unmarshal([]byte(text), &resp)
	flows := resp["affected_flows"].([]any)
	if len(flows) != 0 {
		t.Errorf("expected 0 affected flows, got %d", len(flows))
	}
}

func TestGetAffectedFlows_EmptyChanges(t *testing.T) {
	deps := setupTestDeps(t)

	deps.ChangesGitClient = &mockGitClient{
		changedFiles: []string{},
		hunks:        []changes.Hunk{},
	}
	repoRoot := t.TempDir()
	deps.RepoRoot = repoRoot

	result := callTool(t, deps, "get_affected_flows", map[string]any{"repo_root": repoRoot})
	if result.IsError {
		t.Fatalf("get_affected_flows error: %s", getTextContent(result))
	}

	text := getTextContent(result)
	var resp map[string]any
	json.Unmarshal([]byte(text), &resp)
	flows := resp["affected_flows"].([]any)
	if len(flows) != 0 {
		t.Errorf("expected 0 affected flows, got %d", len(flows))
	}
}

// ============================================================
// 11.8 list_flows
// ============================================================

func TestListFlows_SortByName(t *testing.T) {
	deps := setupTestDeps(t)

	deps.DB.Create(&model.Flow{Name: "beta-flow"})
	deps.DB.Create(&model.Flow{Name: "alpha-flow"})

	result := callTool(t, deps, "list_flows", map[string]any{"sort_by": "name"})
	if result.IsError {
		t.Fatalf("list_flows error: %s", getTextContent(result))
	}

	text := getTextContent(result)
	var resp map[string]any
	json.Unmarshal([]byte(text), &resp)
	flows := resp["flows"].([]any)
	if len(flows) != 2 {
		t.Fatalf("expected 2 flows, got %d", len(flows))
	}
	first := flows[0].(map[string]any)
	if first["name"] != "alpha-flow" {
		t.Errorf("expected first flow to be alpha-flow, got %v", first["name"])
	}
}

func TestListFlows_SortByNodeCount(t *testing.T) {
	deps := setupTestDeps(t)
	ctx := context.Background()

	deps.DB.Create(&model.Flow{Name: "small-flow"})
	deps.DB.Create(&model.Flow{Name: "big-flow"})
	var small, big model.Flow
	deps.DB.First(&small, "name = ?", "small-flow")
	deps.DB.First(&big, "name = ?", "big-flow")

	deps.Store.UpsertNodes(ctx, []model.Node{
		{QualifiedName: "pkg.N1", Kind: model.NodeKindFunction, Name: "N1", FilePath: "n.go", StartLine: 1, EndLine: 5, Language: "go"},
		{QualifiedName: "pkg.N2", Kind: model.NodeKindFunction, Name: "N2", FilePath: "n.go", StartLine: 10, EndLine: 15, Language: "go"},
		{QualifiedName: "pkg.N3", Kind: model.NodeKindFunction, Name: "N3", FilePath: "n.go", StartLine: 20, EndLine: 25, Language: "go"},
	})
	n1, _ := deps.Store.GetNode(ctx, "pkg.N1")
	n2, _ := deps.Store.GetNode(ctx, "pkg.N2")
	n3, _ := deps.Store.GetNode(ctx, "pkg.N3")

	deps.DB.Create(&model.FlowMembership{FlowID: small.ID, NodeID: n1.ID, Ordinal: 0})
	deps.DB.Create(&model.FlowMembership{FlowID: big.ID, NodeID: n2.ID, Ordinal: 0})
	deps.DB.Create(&model.FlowMembership{FlowID: big.ID, NodeID: n3.ID, Ordinal: 1})

	result := callTool(t, deps, "list_flows", map[string]any{"sort_by": "node_count"})
	if result.IsError {
		t.Fatalf("list_flows error: %s", getTextContent(result))
	}

	text := getTextContent(result)
	var resp map[string]any
	json.Unmarshal([]byte(text), &resp)
	flows := resp["flows"].([]any)
	first := flows[0].(map[string]any)
	if first["name"] != "big-flow" {
		t.Errorf("expected big-flow first (most nodes), got %v", first["name"])
	}
}

func TestListFlows_Limit(t *testing.T) {
	deps := setupTestDeps(t)

	deps.DB.Create(&model.Flow{Name: "flow-1"})
	deps.DB.Create(&model.Flow{Name: "flow-2"})
	deps.DB.Create(&model.Flow{Name: "flow-3"})

	result := callTool(t, deps, "list_flows", map[string]any{"limit": 2})
	if result.IsError {
		t.Fatalf("list_flows error: %s", getTextContent(result))
	}

	text := getTextContent(result)
	var resp map[string]any
	json.Unmarshal([]byte(text), &resp)
	flows := resp["flows"].([]any)
	if len(flows) != 2 {
		t.Errorf("expected 2 flows (limit), got %d", len(flows))
	}
}

func TestListFlows_Empty(t *testing.T) {
	deps := setupTestDeps(t)

	result := callTool(t, deps, "list_flows", map[string]any{})
	if result.IsError {
		t.Fatalf("list_flows error: %s", getTextContent(result))
	}

	text := getTextContent(result)
	var resp map[string]any
	json.Unmarshal([]byte(text), &resp)
	flows := resp["flows"].([]any)
	if len(flows) != 0 {
		t.Errorf("expected 0 flows, got %d", len(flows))
	}
	derived := resp["derived_state"].(map[string]any)
	flowState := derived["flows"].(map[string]any)
	if flowState["freshness"] != "unknown" {
		t.Fatalf("expected flows freshness=unknown, got %v", flowState["freshness"])
	}
}

func TestListFlows_RespectsWorkspaceNamespace(t *testing.T) {
	deps := setupTestDeps(t)
	deps.DB.Create(&model.Flow{Namespace: "alpha", Name: "alpha-flow"})
	deps.DB.Create(&model.Flow{Namespace: "beta", Name: "beta-flow"})

	result := callTool(t, deps, "list_flows", map[string]any{"workspace": "alpha"})
	if result.IsError {
		t.Fatalf("list_flows error: %s", getTextContent(result))
	}
	var resp map[string]any
	json.Unmarshal([]byte(getTextContent(result)), &resp)
	flows := resp["flows"].([]any)
	if len(flows) != 1 {
		t.Fatalf("expected 1 flow, got %d", len(flows))
	}
	if flows[0].(map[string]any)["name"] != "alpha-flow" {
		t.Fatalf("flow name = %v, want alpha-flow", flows[0].(map[string]any)["name"])
	}
}

func TestListFlows_InvalidLimit(t *testing.T) {
	deps := setupTestDeps(t)

	result := callTool(t, deps, "list_flows", map[string]any{"limit": 0})
	if !result.IsError {
		t.Fatal("expected invalid limit to return tool error")
	}
	if !strings.Contains(getTextContent(result), "limit must be > 0") {
		t.Fatalf("unexpected error: %s", getTextContent(result))
	}
}

// ============================================================
// 11.9 list_communities
// ============================================================

func TestListCommunities_SortBySize(t *testing.T) {
	deps := setupTestDeps(t)
	ctx := context.Background()

	c1 := model.Community{Key: "small", Label: "small", Strategy: "directory"}
	c2 := model.Community{Key: "big", Label: "big", Strategy: "directory"}
	deps.DB.Create(&c1)
	deps.DB.Create(&c2)

	deps.Store.UpsertNodes(ctx, []model.Node{
		{QualifiedName: "s.N1", Kind: model.NodeKindFunction, Name: "N1", FilePath: "s.go", StartLine: 1, EndLine: 5, Language: "go"},
		{QualifiedName: "b.N1", Kind: model.NodeKindFunction, Name: "N1", FilePath: "b.go", StartLine: 1, EndLine: 5, Language: "go"},
		{QualifiedName: "b.N2", Kind: model.NodeKindFunction, Name: "N2", FilePath: "b.go", StartLine: 10, EndLine: 15, Language: "go"},
	})
	sn1, _ := deps.Store.GetNode(ctx, "s.N1")
	bn1, _ := deps.Store.GetNode(ctx, "b.N1")
	bn2, _ := deps.Store.GetNode(ctx, "b.N2")

	deps.DB.Create(&model.CommunityMembership{CommunityID: c1.ID, NodeID: sn1.ID})
	deps.DB.Create(&model.CommunityMembership{CommunityID: c2.ID, NodeID: bn1.ID})
	deps.DB.Create(&model.CommunityMembership{CommunityID: c2.ID, NodeID: bn2.ID})

	result := callTool(t, deps, "list_communities", map[string]any{"sort_by": "size"})
	if result.IsError {
		t.Fatalf("list_communities error: %s", getTextContent(result))
	}

	text := getTextContent(result)
	var resp map[string]any
	json.Unmarshal([]byte(text), &resp)
	comms := resp["communities"].([]any)
	first := comms[0].(map[string]any)
	if first["label"] != "big" {
		t.Errorf("expected big first (most nodes), got %v", first["label"])
	}
}

func TestListCommunities_SortByName(t *testing.T) {
	deps := setupTestDeps(t)

	deps.DB.Create(&model.Community{Key: "zulu", Label: "zulu", Strategy: "directory"})
	deps.DB.Create(&model.Community{Key: "alpha", Label: "alpha", Strategy: "directory"})

	result := callTool(t, deps, "list_communities", map[string]any{"sort_by": "name"})
	if result.IsError {
		t.Fatalf("list_communities error: %s", getTextContent(result))
	}

	text := getTextContent(result)
	var resp map[string]any
	json.Unmarshal([]byte(text), &resp)
	comms := resp["communities"].([]any)
	first := comms[0].(map[string]any)
	if first["label"] != "alpha" {
		t.Errorf("expected alpha first, got %v", first["label"])
	}
}

func TestListCommunities_MinSize(t *testing.T) {
	deps := setupTestDeps(t)
	ctx := context.Background()

	c1 := model.Community{Key: "tiny", Label: "tiny", Strategy: "directory"}
	c2 := model.Community{Key: "large", Label: "large", Strategy: "directory"}
	deps.DB.Create(&c1)
	deps.DB.Create(&c2)

	deps.Store.UpsertNodes(ctx, []model.Node{
		{QualifiedName: "t.N1", Kind: model.NodeKindFunction, Name: "N1", FilePath: "t.go", StartLine: 1, EndLine: 5, Language: "go"},
		{QualifiedName: "l.N1", Kind: model.NodeKindFunction, Name: "N1", FilePath: "l.go", StartLine: 1, EndLine: 5, Language: "go"},
		{QualifiedName: "l.N2", Kind: model.NodeKindFunction, Name: "N2", FilePath: "l.go", StartLine: 10, EndLine: 15, Language: "go"},
		{QualifiedName: "l.N3", Kind: model.NodeKindFunction, Name: "N3", FilePath: "l.go", StartLine: 20, EndLine: 25, Language: "go"},
	})
	tn1, _ := deps.Store.GetNode(ctx, "t.N1")
	ln1, _ := deps.Store.GetNode(ctx, "l.N1")
	ln2, _ := deps.Store.GetNode(ctx, "l.N2")
	ln3, _ := deps.Store.GetNode(ctx, "l.N3")

	deps.DB.Create(&model.CommunityMembership{CommunityID: c1.ID, NodeID: tn1.ID})
	deps.DB.Create(&model.CommunityMembership{CommunityID: c2.ID, NodeID: ln1.ID})
	deps.DB.Create(&model.CommunityMembership{CommunityID: c2.ID, NodeID: ln2.ID})
	deps.DB.Create(&model.CommunityMembership{CommunityID: c2.ID, NodeID: ln3.ID})

	result := callTool(t, deps, "list_communities", map[string]any{"min_size": 3})
	if result.IsError {
		t.Fatalf("list_communities error: %s", getTextContent(result))
	}

	text := getTextContent(result)
	var resp map[string]any
	json.Unmarshal([]byte(text), &resp)
	comms := resp["communities"].([]any)
	if len(comms) != 1 {
		t.Errorf("expected 1 community with min_size=3, got %d", len(comms))
	}
}

func TestListCommunities_Empty(t *testing.T) {
	deps := setupTestDeps(t)

	result := callTool(t, deps, "list_communities", map[string]any{})
	if result.IsError {
		t.Fatalf("list_communities error: %s", getTextContent(result))
	}

	text := getTextContent(result)
	var resp map[string]any
	json.Unmarshal([]byte(text), &resp)
	comms := resp["communities"].([]any)
	if len(comms) != 0 {
		t.Errorf("expected 0 communities, got %d", len(comms))
	}
	derived := resp["derived_state"].(map[string]any)
	communityState := derived["communities"].(map[string]any)
	if communityState["freshness"] != "unknown" {
		t.Fatalf("expected communities freshness=unknown, got %v", communityState["freshness"])
	}
}

func TestListCommunities_WorkspaceScopesResults(t *testing.T) {
	deps := setupTestDeps(t)
	ctxAlpha := ctxns.WithNamespace(context.Background(), "alpha")
	ctxBeta := ctxns.WithNamespace(context.Background(), "beta")

	alphaCommunity := model.Community{Namespace: "alpha", Key: "alpha/core", Label: "alpha/core", Strategy: "directory"}
	betaCommunity := model.Community{Namespace: "beta", Key: "beta/core", Label: "beta/core", Strategy: "directory"}
	deps.DB.Create(&alphaCommunity)
	deps.DB.Create(&betaCommunity)

	if err := deps.Store.UpsertNodes(ctxAlpha, []model.Node{{QualifiedName: "alpha.Fn", Kind: model.NodeKindFunction, Name: "Fn", FilePath: "alpha.go", StartLine: 1, EndLine: 5, Language: "go"}}); err != nil {
		t.Fatal(err)
	}
	if err := deps.Store.UpsertNodes(ctxBeta, []model.Node{{QualifiedName: "beta.Fn", Kind: model.NodeKindFunction, Name: "Fn", FilePath: "beta.go", StartLine: 1, EndLine: 5, Language: "go"}}); err != nil {
		t.Fatal(err)
	}
	alphaNode, _ := deps.Store.GetNode(ctxAlpha, "alpha.Fn")
	betaNode, _ := deps.Store.GetNode(ctxBeta, "beta.Fn")
	deps.DB.Create(&model.CommunityMembership{CommunityID: alphaCommunity.ID, NodeID: alphaNode.ID})
	deps.DB.Create(&model.CommunityMembership{CommunityID: betaCommunity.ID, NodeID: betaNode.ID})

	result := callTool(t, deps, "list_communities", map[string]any{"workspace": "alpha"})
	if result.IsError {
		t.Fatalf("list_communities error: %s", getTextContent(result))
	}

	text := getTextContent(result)
	if strings.Contains(text, "beta/core") {
		t.Fatalf("unexpected beta community leak: %s", text)
	}
	if !strings.Contains(text, "alpha/core") {
		t.Fatalf("expected alpha community in scoped result: %s", text)
	}
}

// ============================================================
// 11.10 get_community
// ============================================================

func TestGetCommunity_Basic(t *testing.T) {
	deps := setupTestDeps(t)
	ctx := context.Background()

	c := model.Community{Key: "core", Label: "core", Strategy: "directory"}
	deps.DB.Create(&c)

	deps.Store.UpsertNodes(ctx, []model.Node{
		{QualifiedName: "core.Fn", Kind: model.NodeKindFunction, Name: "Fn", FilePath: "core.go", StartLine: 1, EndLine: 5, Language: "go"},
	})
	node, _ := deps.Store.GetNode(ctx, "core.Fn")
	deps.DB.Create(&model.CommunityMembership{CommunityID: c.ID, NodeID: node.ID})

	result := callTool(t, deps, "get_community", map[string]any{"community_id": c.ID})
	if result.IsError {
		t.Fatalf("get_community error: %s", getTextContent(result))
	}

	text := getTextContent(result)
	var resp map[string]any
	json.Unmarshal([]byte(text), &resp)
	if resp["label"] != "core" {
		t.Errorf("expected label=core, got %v", resp["label"])
	}
	if resp["node_count"].(float64) != 1 {
		t.Errorf("expected node_count=1, got %v", resp["node_count"])
	}
}

func TestGetCommunity_WithMembers(t *testing.T) {
	deps := setupTestDeps(t)
	ctx := context.Background()

	c := model.Community{Key: "api", Label: "api", Strategy: "directory"}
	deps.DB.Create(&c)

	deps.Store.UpsertNodes(ctx, []model.Node{
		{QualifiedName: "api.Handler", Kind: model.NodeKindFunction, Name: "Handler", FilePath: "api.go", StartLine: 1, EndLine: 10, Language: "go"},
	})
	node, _ := deps.Store.GetNode(ctx, "api.Handler")
	deps.DB.Create(&model.CommunityMembership{CommunityID: c.ID, NodeID: node.ID})

	result := callTool(t, deps, "get_community", map[string]any{
		"community_id":    c.ID,
		"include_members": true,
	})
	if result.IsError {
		t.Fatalf("get_community error: %s", getTextContent(result))
	}

	text := getTextContent(result)
	var resp map[string]any
	json.Unmarshal([]byte(text), &resp)
	members := resp["members"].([]any)
	if len(members) != 1 {
		t.Errorf("expected 1 member, got %d", len(members))
	}
}

func TestGetCommunity_WithCoverage(t *testing.T) {
	deps := setupTestDeps(t)
	ctx := context.Background()

	mockCov := &mockCoverageAnalyzer{
		communityResult: &coverage.CommunityCoverage{
			CommunityID: 1, Label: "core", Total: 10, Tested: 7, Ratio: 0.7,
		},
	}
	deps.CoverageAnalyzer = mockCov

	c := model.Community{Key: "core2", Label: "core2", Strategy: "directory"}
	deps.DB.Create(&c)
	deps.Store.UpsertNodes(ctx, []model.Node{
		{QualifiedName: "core2.Fn", Kind: model.NodeKindFunction, Name: "Fn", FilePath: "core2.go", StartLine: 1, EndLine: 5, Language: "go"},
	})
	node, _ := deps.Store.GetNode(ctx, "core2.Fn")
	deps.DB.Create(&model.CommunityMembership{CommunityID: c.ID, NodeID: node.ID})

	result := callTool(t, deps, "get_community", map[string]any{"community_id": c.ID})
	if result.IsError {
		t.Fatalf("get_community error: %s", getTextContent(result))
	}

	text := getTextContent(result)
	var resp map[string]any
	json.Unmarshal([]byte(text), &resp)
	if resp["coverage"].(float64) != 0.7 {
		t.Errorf("expected coverage=0.7, got %v", resp["coverage"])
	}
}

func TestGetCommunity_NotFound(t *testing.T) {
	deps := setupTestDeps(t)

	result := callTool(t, deps, "get_community", map[string]any{"community_id": 999})
	if !result.IsError {
		t.Fatal("expected error for nonexistent community")
	}
}

func TestGetCommunity_WorkspaceRejectsForeignCommunity(t *testing.T) {
	deps := setupTestDeps(t)
	community := model.Community{Namespace: "beta", Key: "beta/core", Label: "beta/core", Strategy: "directory"}
	deps.DB.Create(&community)

	result := callTool(t, deps, "get_community", map[string]any{"community_id": community.ID, "workspace": "alpha"})
	if !result.IsError {
		t.Fatalf("expected scoped lookup to reject foreign community: %s", getTextContent(result))
	}
}

// ============================================================
// 11.11 get_architecture_overview
// ============================================================

func TestArchitectureOverview_ReturnsCommunities2(t *testing.T) {
	deps := setupTestDeps(t)

	deps.DB.Create(&model.Community{Key: "mod_a", Label: "mod_a", Strategy: "directory"})
	deps.DB.Create(&model.Community{Key: "mod_b", Label: "mod_b", Strategy: "directory"})

	result := callTool(t, deps, "get_architecture_overview", map[string]any{})
	if result.IsError {
		t.Fatalf("get_architecture_overview error: %s", getTextContent(result))
	}

	text := getTextContent(result)
	var resp map[string]any
	json.Unmarshal([]byte(text), &resp)
	comms := resp["communities"].([]any)
	if len(comms) != 2 {
		t.Errorf("expected 2 communities, got %d", len(comms))
	}
}

func TestArchitectureOverview_ReturnsCoupling2(t *testing.T) {
	deps := setupTestDeps(t)

	mockCoup := &mockCouplingAnalyzer{
		result: []coupling.CouplingPair{
			{FromCommunity: "a", ToCommunity: "b", EdgeCount: 5, Strength: 0.5},
		},
	}
	deps.CouplingAnalyzer = mockCoup

	deps.DB.Create(&model.Community{Key: "a", Label: "a", Strategy: "directory"})

	result := callTool(t, deps, "get_architecture_overview", map[string]any{})
	if result.IsError {
		t.Fatalf("get_architecture_overview error: %s", getTextContent(result))
	}

	text := getTextContent(result)
	var resp map[string]any
	json.Unmarshal([]byte(text), &resp)
	cp := resp["coupling"].([]any)
	if len(cp) != 1 {
		t.Errorf("expected 1 coupling pair, got %d", len(cp))
	}
}

func TestArchitectureOverview_Warnings(t *testing.T) {
	deps := setupTestDeps(t)

	mockCoup := &mockCouplingAnalyzer{
		result: []coupling.CouplingPair{
			{FromCommunity: "x", ToCommunity: "y", EdgeCount: 100, Strength: 0.95},
		},
	}
	deps.CouplingAnalyzer = mockCoup

	deps.DB.Create(&model.Community{Key: "x", Label: "x", Strategy: "directory"})

	result := callTool(t, deps, "get_architecture_overview", map[string]any{})
	if result.IsError {
		t.Fatalf("get_architecture_overview error: %s", getTextContent(result))
	}

	text := getTextContent(result)
	var resp map[string]any
	json.Unmarshal([]byte(text), &resp)
	warnings := resp["warnings"].([]any)
	if len(warnings) == 0 {
		t.Error("expected warnings for high coupling")
	}
}

func TestArchitectureOverview_Empty2(t *testing.T) {
	deps := setupTestDeps(t)

	result := callTool(t, deps, "get_architecture_overview", map[string]any{})
	if result.IsError {
		t.Fatalf("get_architecture_overview error: %s", getTextContent(result))
	}

	text := getTextContent(result)
	var resp map[string]any
	json.Unmarshal([]byte(text), &resp)
	warnings := resp["warnings"].([]any)
	if len(warnings) == 0 {
		t.Error("expected warning message when no communities")
	}
}

// ============================================================
// 11.12 find_dead_code
// ============================================================

func TestFindDeadCode_ReturnsUnusedFunctions(t *testing.T) {
	deps := setupTestDeps(t)

	mockDC := &mockDeadcodeAnalyzer{
		result: []model.Node{
			{QualifiedName: "pkg.Unused", Kind: model.NodeKindFunction, Name: "Unused", FilePath: "unused.go", StartLine: 1},
		},
	}
	deps.DeadcodeAnalyzer = mockDC

	result := callTool(t, deps, "find_dead_code", map[string]any{})
	if result.IsError {
		t.Fatalf("find_dead_code error: %s", getTextContent(result))
	}

	text := getTextContent(result)
	var resp map[string]any
	json.Unmarshal([]byte(text), &resp)
	if resp["count"].(float64) != 1 {
		t.Errorf("expected 1 dead code, got %v", resp["count"])
	}
}

func TestFindDeadCode_FilterByKind(t *testing.T) {
	deps := setupTestDeps(t)

	mockDC := &mockDeadcodeAnalyzer{result: []model.Node{}}
	deps.DeadcodeAnalyzer = mockDC

	callTool(t, deps, "find_dead_code", map[string]any{
		"kinds": []any{"function"},
	})

	if !mockDC.findCalled {
		t.Error("expected Find to be called")
	}
}

func TestFindDeadCode_FilterByFilePattern(t *testing.T) {
	deps := setupTestDeps(t)

	mockDC := &mockDeadcodeAnalyzer{result: []model.Node{}}
	deps.DeadcodeAnalyzer = mockDC

	callTool(t, deps, "find_dead_code", map[string]any{
		"path": "internal/",
	})

	if !mockDC.findCalled {
		t.Error("expected Find to be called")
	}
}

func TestFindDeadCode_NoDeadCode(t *testing.T) {
	deps := setupTestDeps(t)

	mockDC := &mockDeadcodeAnalyzer{result: []model.Node{}}
	deps.DeadcodeAnalyzer = mockDC

	result := callTool(t, deps, "find_dead_code", map[string]any{})
	if result.IsError {
		t.Fatalf("find_dead_code error: %s", getTextContent(result))
	}

	text := getTextContent(result)
	var resp map[string]any
	json.Unmarshal([]byte(text), &resp)
	if resp["count"].(float64) != 0 {
		t.Errorf("expected 0 dead code, got %v", resp["count"])
	}
}

func TestBuildOrUpdateGraph_Incremental_ExistingFilesNamespaceScoped(t *testing.T) {
	deps := setupTestDeps(t)

	ctxA := ctxns.WithNamespace(context.Background(), "ns-a")
	ctxB := ctxns.WithNamespace(context.Background(), "ns-b")

	deps.Store.UpsertNodes(ctxA, []model.Node{
		{QualifiedName: "a.F", Kind: model.NodeKindFunction, Name: "F", FilePath: "/ns-a-only/file.go", StartLine: 1, EndLine: 2, Language: "go"},
	})
	deps.Store.UpsertNodes(ctxB, []model.Node{
		{QualifiedName: "b.G", Kind: model.NodeKindFunction, Name: "G", FilePath: "/ns-b-only/file.go", StartLine: 1, EndLine: 2, Language: "go"},
	})

	mockSync := &mockIncrementalSyncer{
		result: &incremental.SyncStats{},
	}
	deps.Incremental = mockSync

	dir := t.TempDir()
	writeGoFile(t, dir, "svc.go", `package svc
func Svc() {}
`)

	callTool(t, deps, "build_or_update_graph", map[string]any{
		"path":         dir,
		"full_rebuild": false,
		"postprocess":  "none",
		"workspace":    "ns-a",
	})

	for _, fp := range mockSync.existingFiles {
		if fp == "/ns-b-only/file.go" {
			t.Errorf("existingFiles must not include files from namespace ns-b, got: %s", fp)
		}
	}

	found := false
	for _, fp := range mockSync.existingFiles {
		if fp == "/ns-a-only/file.go" {
			found = true
		}
	}
	if !found {
		t.Error("existingFiles must include ns-a file path")
	}
}

type failSearchBackend struct {
	err error
}

func (f *failSearchBackend) Rebuild(ctx context.Context, db *gorm.DB) error {
	return f.err
}

func (f *failSearchBackend) PurgeNamespace(ctx context.Context, db *gorm.DB) error {
	return f.err
}

func (f *failSearchBackend) Migrate(db *gorm.DB) error { return nil }

func (f *failSearchBackend) Query(ctx context.Context, db *gorm.DB, query string, limit int) ([]model.Node, error) {
	return nil, nil
}

func TestRunPostprocess_DegradedOnCommunityFailure(t *testing.T) {
	deps := setupTestDeps(t)
	deps.CommunityBuilder = &mockCommunityBuilder{
		err: errors.New("community rebuild boom"),
	}

	result := callTool(t, deps, "run_postprocess", map[string]any{
		"communities": true,
		"fts":         false,
		"flows":       false,
	})
	if result.IsError {
		t.Fatalf("run_postprocess should not return tool error, got: %s", getTextContent(result))
	}

	text := getTextContent(result)
	var resp map[string]any
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("expected JSON, got: %s", text)
	}

	if resp["status"] != "degraded" {
		t.Errorf("expected status=degraded, got %v", resp["status"])
	}

	failedSteps, ok := resp["failed_steps"].([]any)
	if !ok || len(failedSteps) == 0 {
		t.Fatalf("expected failed_steps to be non-empty, got %v", resp["failed_steps"])
	}
	found := false
	for _, s := range failedSteps {
		if s == "communities" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected 'communities' in failed_steps, got %v", failedSteps)
	}
}

func TestRunPostprocess_DegradedOnSearchFailure(t *testing.T) {
	deps := setupTestDeps(t)
	deps.SearchBackend = &failSearchBackend{err: errors.New("fts rebuild boom")}

	result := callTool(t, deps, "run_postprocess", map[string]any{
		"communities": false,
		"fts":         true,
		"flows":       false,
	})
	if result.IsError {
		t.Fatalf("run_postprocess should not return tool error, got: %s", getTextContent(result))
	}

	text := getTextContent(result)
	var resp map[string]any
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("expected JSON, got: %s", text)
	}

	if resp["status"] != "degraded" {
		t.Errorf("expected status=degraded, got %v", resp["status"])
	}

	failedSteps, ok := resp["failed_steps"].([]any)
	if !ok || len(failedSteps) == 0 {
		t.Fatalf("expected failed_steps to be non-empty, got %v", resp["failed_steps"])
	}
	found := false
	for _, s := range failedSteps {
		if s == "fts" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected 'fts' in failed_steps, got %v", failedSteps)
	}
}

func TestRunPostprocess_RefreshesSearchDocumentsBeforeFTS(t *testing.T) {
	deps := setupTestDeps(t)

	dir := t.TempDir()
	writeGoFile(t, dir, "svc.go", `package svc
func FreshSearch() {}
`)
	build := callTool(t, deps, "build_or_update_graph", map[string]any{
		"path":         dir,
		"full_rebuild": true,
		"postprocess":  "none",
	})
	if build.IsError {
		t.Fatalf("build_or_update_graph returned error: %s", getTextContent(build))
	}

	result := callTool(t, deps, "run_postprocess", map[string]any{
		"communities": false,
		"fts":         true,
		"flows":       false,
	})
	if result.IsError {
		t.Fatalf("run_postprocess returned error: %s", getTextContent(result))
	}

	searchResult := callTool(t, deps, "search", map[string]any{"query": "FreshSearch", "limit": 10})
	if searchResult.IsError {
		t.Fatalf("search returned error: %s", getTextContent(searchResult))
	}
	var nodes []map[string]any
	if err := json.Unmarshal([]byte(getTextContent(searchResult)), &nodes); err != nil {
		t.Fatalf("expected JSON array, got: %s", getTextContent(searchResult))
	}
	if len(nodes) == 0 {
		t.Fatal("expected at least 1 search result after run_postprocess refreshed search documents")
	}
}

func TestRunPostprocess_ReportsSkippedFlowRebuild(t *testing.T) {
	deps := setupTestDeps(t)

	result := callTool(t, deps, "run_postprocess", map[string]any{
		"communities": false,
		"fts":         false,
		"flows":       true,
	})
	if result.IsError {
		t.Fatalf("run_postprocess returned error: %s", getTextContent(result))
	}

	var resp map[string]any
	if err := json.Unmarshal([]byte(getTextContent(result)), &resp); err != nil {
		t.Fatalf("expected JSON, got: %s", getTextContent(result))
	}
	skipped := resp["skipped_steps"].([]any)
	if !containsString(skipped, "flows") {
		t.Fatalf("expected skipped_steps to contain flows, got %v", skipped)
	}
}

func TestBuildOrUpdateGraph_RefreshesSearchDocumentsBeforeFTS(t *testing.T) {
	deps := setupTestDeps(t)

	dir := t.TempDir()
	writeGoFile(t, dir, "svc.go", `package svc
func MyService() {}
`)

	result := callTool(t, deps, "build_or_update_graph", map[string]any{
		"path":         dir,
		"full_rebuild": true,
		"postprocess":  "full",
	})
	if result.IsError {
		t.Fatalf("build_or_update_graph returned error: %s", getTextContent(result))
	}

	var docs []model.SearchDocument
	if err := deps.DB.Find(&docs).Error; err != nil {
		t.Fatalf("failed to query search_documents: %v", err)
	}
	if len(docs) == 0 {
		t.Fatal("expected search_documents to be populated after build_or_update_graph with postprocess=full")
	}

	searchResult := callTool(t, deps, "search", map[string]any{"query": "MyService", "limit": 10})
	if searchResult.IsError {
		t.Fatalf("search returned error: %s", getTextContent(searchResult))
	}
	var nodes []map[string]any
	if err := json.Unmarshal([]byte(getTextContent(searchResult)), &nodes); err != nil {
		t.Fatalf("expected JSON array, got: %s", getTextContent(searchResult))
	}
	if len(nodes) == 0 {
		t.Fatal("expected at least 1 search result after build_or_update_graph refreshed search documents")
	}
}

// ============================================================
// MCP build path normalization & annotation binding parity
// ============================================================

// TestBuildOrUpdateGraph_FullRebuild_StoresRelativePaths verifies that
// full rebuild stores repo-relative file_path values (not absolute paths).
func TestBuildOrUpdateGraph_FullRebuild_StoresRelativePaths(t *testing.T) {
	deps := setupTestDeps(t)

	dir := t.TempDir()
	writeGoFile(t, dir, "hello.go", `package hello

func Hello() string {
	return "hello"
}
`)

	result := callTool(t, deps, "build_or_update_graph", map[string]any{
		"path":         dir,
		"full_rebuild": true,
		"postprocess":  "none",
	})
	if result.IsError {
		t.Fatalf("build_or_update_graph error: %s", getTextContent(result))
	}

	node, err := deps.Store.GetNode(context.Background(), "hello.Hello")
	if err != nil || node == nil {
		t.Fatal("expected node hello.Hello to exist after full rebuild")
	}

	if filepath.IsAbs(node.FilePath) {
		t.Errorf("expected repo-relative file_path, got absolute path: %s", node.FilePath)
	}
	if node.FilePath != "hello.go" {
		t.Errorf("expected file_path=hello.go, got %s", node.FilePath)
	}
}

// TestBuildOrUpdateGraph_Incremental_UsesRelativePathKeys verifies that
// incremental build passes repo-relative path keys to the incremental syncer.
func TestBuildOrUpdateGraph_Incremental_UsesRelativePathKeys(t *testing.T) {
	deps := setupTestDeps(t)

	mockSync := &mockIncrementalSyncer{
		result: &incremental.SyncStats{Added: 1},
	}
	deps.Incremental = mockSync

	dir := t.TempDir()
	writeGoFile(t, dir, "calc.go", `package calc

func Add(a, b int) int {
	return a + b
}
`)

	result := callTool(t, deps, "build_or_update_graph", map[string]any{
		"path":         dir,
		"full_rebuild": false,
		"postprocess":  "none",
	})
	if result.IsError {
		t.Fatalf("build_or_update_graph error: %s", getTextContent(result))
	}

	if !mockSync.syncWithExisting {
		t.Fatal("expected SyncWithExisting to be called")
	}

	for fp := range mockSync.files {
		if filepath.IsAbs(fp) {
			t.Errorf("incremental syncer received absolute path key: %s (want repo-relative)", fp)
		}
	}
}

// TestBuildOrUpdateGraph_FullRebuild_AnnotationBinding verifies that
// full rebuild binds annotations so get_annotation works without manual seeding.
func TestBuildOrUpdateGraph_FullRebuild_AnnotationBinding(t *testing.T) {
	deps := setupTestDepsWithComments(t)

	dir := t.TempDir()
	writeGoFile(t, dir, "svc.go", `package svc

// DoWork does the work.
// @intent perform the main service operation
func DoWork() {}
`)

	result := callTool(t, deps, "build_or_update_graph", map[string]any{
		"path":         dir,
		"full_rebuild": true,
		"postprocess":  "none",
	})
	if result.IsError {
		t.Fatalf("build_or_update_graph error: %s", getTextContent(result))
	}

	annResult := callTool(t, deps, "get_annotation", map[string]any{"qualified_name": "svc.DoWork"})
	if annResult.IsError {
		t.Fatalf("get_annotation error: %s", getTextContent(annResult))
	}

	text := getTextContent(annResult)
	var ann map[string]any
	if err := json.Unmarshal([]byte(text), &ann); err != nil {
		t.Fatalf("expected JSON, got: %s", text)
	}

	tags, _ := ann["tags"].([]any)
	found := false
	for _, tag := range tags {
		tm, ok := tag.(map[string]any)
		if !ok {
			continue
		}
		if tm["kind"] == "intent" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected @intent tag to be bound after full rebuild, got annotation: %v", ann)
	}
}
