package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/imtaebin/code-context-graph/internal/analysis/changes"
	"github.com/imtaebin/code-context-graph/internal/analysis/community"
	"github.com/imtaebin/code-context-graph/internal/analysis/coupling"
	"github.com/imtaebin/code-context-graph/internal/analysis/coverage"
	"github.com/imtaebin/code-context-graph/internal/analysis/deadcode"
	"github.com/imtaebin/code-context-graph/internal/analysis/flows"
	"github.com/imtaebin/code-context-graph/internal/analysis/impact"
	"github.com/imtaebin/code-context-graph/internal/analysis/incremental"
	"github.com/imtaebin/code-context-graph/internal/analysis/query"
	"github.com/imtaebin/code-context-graph/internal/model"
	"github.com/imtaebin/code-context-graph/internal/ragindex"
	"github.com/imtaebin/code-context-graph/internal/store/gormstore"
	"github.com/imtaebin/code-context-graph/internal/store/search"
)

// simpleGoParser is a minimal Go parser for testing. It extracts package-level
// function declarations from simple Go files without depending on tree-sitter.
type simpleGoParser struct{}

func (p *simpleGoParser) Parse(filePath string, content []byte) ([]model.Node, []model.Edge, error) {
	var nodes []model.Node
	lines := strings.Split(string(content), "\n")

	var pkgName string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "package ") {
			pkgName = strings.TrimPrefix(trimmed, "package ")
			break
		}
	}

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "func ") {
			// Extract function name
			rest := strings.TrimPrefix(trimmed, "func ")
			parenIdx := strings.Index(rest, "(")
			if parenIdx > 0 {
				name := rest[:parenIdx]
				qn := pkgName + "." + name
				// Find end of function (next closing brace)
				endLine := i + 1
				braceCount := 0
				for j := i; j < len(lines); j++ {
					for _, ch := range lines[j] {
						if ch == '{' {
							braceCount++
						} else if ch == '}' {
							braceCount--
							if braceCount == 0 {
								endLine = j + 1
								goto done
							}
						}
					}
				}
			done:
				nodes = append(nodes, model.Node{
					QualifiedName: qn,
					Kind:          model.NodeKindFunction,
					Name:          name,
					FilePath:      filePath,
					StartLine:     i + 1,
					EndLine:       endLine,
					Language:      "go",
				})
			}
		}
	}
	return nodes, nil, nil
}

var handlerTestDBSeq atomic.Int64

func setupTestDeps(t *testing.T) *Deps {
	t.Helper()
	dsn := fmt.Sprintf("file:handlertest%d?mode=memory&cache=shared", handlerTestDBSeq.Add(1))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	st := gormstore.New(db)
	if err := st.AutoMigrate(); err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.SearchDocument{}, &model.Flow{}, &model.FlowMembership{}); err != nil {
		t.Fatal(err)
	}
	sb := search.NewSQLiteBackend()
	if err := sb.Migrate(db); err != nil {
		if strings.Contains(err.Error(), "no such module: fts5") {
			t.Skip("fts5 module not available, skipping test")
		}
		t.Fatal(err)
	}

	return &Deps{
		Store:          st,
		DB:             db,
		Parser:         &simpleGoParser{},
		SearchBackend:  sb,
		ImpactAnalyzer: impact.New(st),
		FlowTracer:     flows.New(st),
	}
}

func callTool(t *testing.T, deps *Deps, toolName string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	srv := NewServer(deps)

	argsJSON, _ := json.Marshal(args)
	msg, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      toolName,
			"arguments": json.RawMessage(argsJSON),
		},
	})

	resp := srv.HandleMessage(context.Background(), msg)
	rpcResp, ok := resp.(mcp.JSONRPCResponse)
	if !ok {
		errResp, isErr := resp.(mcp.JSONRPCError)
		if isErr {
			t.Fatalf("JSON-RPC error: code=%d msg=%s", errResp.Error.Code, errResp.Error.Message)
		}
		t.Fatalf("unexpected response type: %T", resp)
	}

	resultJSON, err := json.Marshal(rpcResp.Result)
	if err != nil {
		t.Fatal(err)
	}
	var result mcp.CallToolResult
	if err := json.Unmarshal(resultJSON, &result); err != nil {
		t.Fatal(err)
	}
	return &result
}

func getTextContent(result *mcp.CallToolResult) string {
	if len(result.Content) == 0 {
		return ""
	}
	tc, ok := result.Content[0].(mcp.TextContent)
	if !ok {
		return ""
	}
	return tc.Text
}

func TestHandler_ParseProject(t *testing.T) {
	deps := setupTestDeps(t)

	dir := t.TempDir()
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
	var nodes []map[string]any
	if err := json.Unmarshal([]byte(text), &nodes); err != nil {
		t.Fatalf("expected JSON array, got: %s", text)
	}
	if len(nodes) < 2 {
		t.Errorf("expected at least 2 nodes in impact radius, got %d", len(nodes))
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
		t.Error("expected Incremental.Sync to be called for incremental build")
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

	result := callTool(t, deps, "detect_changes", map[string]any{"repo_root": "/tmp/repo"})
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

	callTool(t, deps, "detect_changes", map[string]any{"repo_root": "/tmp/repo"})

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

	result := callTool(t, deps, "detect_changes", map[string]any{"repo_root": "/tmp/repo"})
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

	result := callTool(t, deps, "get_affected_flows", map[string]any{"repo_root": "/tmp/repo"})
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

	result := callTool(t, deps, "get_affected_flows", map[string]any{"repo_root": "/tmp/repo"})
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

	result := callTool(t, deps, "get_affected_flows", map[string]any{"repo_root": "/tmp/repo"})
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

// ============================================================
// Mock types for Phase 11
// ============================================================

type mockQueryService struct {
	callersOfCalled    bool
	calleesOfCalled    bool
	importsOfCalled    bool
	importersOfCalled  bool
	childrenOfCalled   bool
	testsForCalled     bool
	inheritorsOfCalled bool
	fileSummaryCalled  bool
	result             []model.Node
	fileSummaryResult  *query.FileSummary
	err                error
}

func (m *mockQueryService) CallersOf(ctx context.Context, nodeID uint) ([]model.Node, error) {
	m.callersOfCalled = true
	return m.result, m.err
}
func (m *mockQueryService) CalleesOf(ctx context.Context, nodeID uint) ([]model.Node, error) {
	m.calleesOfCalled = true
	return m.result, m.err
}
func (m *mockQueryService) ImportsOf(ctx context.Context, nodeID uint) ([]model.Node, error) {
	m.importsOfCalled = true
	return m.result, m.err
}
func (m *mockQueryService) ImportersOf(ctx context.Context, nodeID uint) ([]model.Node, error) {
	m.importersOfCalled = true
	return m.result, m.err
}
func (m *mockQueryService) ChildrenOf(ctx context.Context, nodeID uint) ([]model.Node, error) {
	m.childrenOfCalled = true
	return m.result, m.err
}
func (m *mockQueryService) TestsFor(ctx context.Context, nodeID uint) ([]model.Node, error) {
	m.testsForCalled = true
	return m.result, m.err
}
func (m *mockQueryService) InheritorsOf(ctx context.Context, nodeID uint) ([]model.Node, error) {
	m.inheritorsOfCalled = true
	return m.result, m.err
}
func (m *mockQueryService) FileSummaryOf(ctx context.Context, filePath string) (*query.FileSummary, error) {
	m.fileSummaryCalled = true
	return m.fileSummaryResult, m.err
}

type mockLargefuncAnalyzer struct {
	findCalled bool
	result     []model.Node
	err        error
}

func (m *mockLargefuncAnalyzer) Find(ctx context.Context, threshold int) ([]model.Node, error) {
	m.findCalled = true
	return m.result, m.err
}

type mockDeadcodeAnalyzer struct {
	findCalled bool
	result     []model.Node
	err        error
}

func (m *mockDeadcodeAnalyzer) Find(ctx context.Context, opts deadcode.Options) ([]model.Node, error) {
	m.findCalled = true
	return m.result, m.err
}

type mockCouplingAnalyzer struct {
	analyzeCalled bool
	result        []coupling.CouplingPair
	err           error
}

func (m *mockCouplingAnalyzer) Analyze(ctx context.Context) ([]coupling.CouplingPair, error) {
	m.analyzeCalled = true
	return m.result, m.err
}

type mockCoverageAnalyzer struct {
	byFileCalled    bool
	byCommunCalled  bool
	fileResult      *coverage.FileCoverage
	communityResult *coverage.CommunityCoverage
	err             error
}

func (m *mockCoverageAnalyzer) ByFile(ctx context.Context, filePath string) (*coverage.FileCoverage, error) {
	m.byFileCalled = true
	return m.fileResult, m.err
}
func (m *mockCoverageAnalyzer) ByCommunity(ctx context.Context, communityID uint) (*coverage.CommunityCoverage, error) {
	m.byCommunCalled = true
	return m.communityResult, m.err
}

type mockCommunityBuilder struct {
	rebuildCalled bool
	result        []community.Stats
	err           error
}

func (m *mockCommunityBuilder) Rebuild(ctx context.Context, cfg community.Config) ([]community.Stats, error) {
	m.rebuildCalled = true
	return m.result, m.err
}

type mockIncrementalSyncer struct {
	syncCalled bool
	result     *incremental.SyncStats
	err        error
}

func (m *mockIncrementalSyncer) Sync(ctx context.Context, files map[string]incremental.FileInfo) (*incremental.SyncStats, error) {
	m.syncCalled = true
	return m.result, m.err
}

// ============================================================
// Cache helper
// ============================================================

func openTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	return db
}

func makeToolRequest(toolName string, args map[string]any) mcp.CallToolRequest {
	var req mcp.CallToolRequest
	req.Params.Name = toolName
	req.Params.Arguments = args
	return req
}

// ============================================================
// 캐시 패턴 테스트
// ============================================================

func TestGetNode_CacheHit(t *testing.T) {
	db := openTestDB(t)
	st := gormstore.New(db)
	if err := st.AutoMigrate(); err != nil {
		t.Fatal(err)
	}

	node := model.Node{
		QualifiedName: "pkg.CacheHitFunc",
		Name:          "CacheHitFunc",
		Kind:          model.NodeKindFunction,
		FilePath:      "pkg/cache_hit.go",
		Language:      "go",
	}
	if err := db.Create(&node).Error; err != nil {
		t.Fatal(err)
	}

	cache := NewCache(5 * time.Minute)
	h := &handlers{
		deps:  &Deps{Store: st, DB: db},
		cache: cache,
	}

	req := makeToolRequest("get_node", map[string]any{
		"qualified_name": "pkg.CacheHitFunc",
	})

	// 1차 호출: DB에서 가져와 캐시에 저장
	res1, err := h.getNode(context.Background(), req)
	if err != nil {
		t.Fatalf("first call error: %v", err)
	}
	if res1.IsError {
		t.Fatal("first call: unexpected error result")
	}

	// DB에서 노드 삭제 (캐시 히트 검증)
	if err := db.Unscoped().Delete(&model.Node{}, node.ID).Error; err != nil {
		t.Fatal(err)
	}

	// 2차 호출: 캐시에서 응답해야 함 (DB에 없어도 성공)
	res2, err := h.getNode(context.Background(), req)
	if err != nil {
		t.Fatalf("second call error: %v", err)
	}
	if res2.IsError {
		t.Fatal("second call (cache hit): unexpected error result")
	}
}

func TestGetNode_NoCache(t *testing.T) {
	db := openTestDB(t)
	st := gormstore.New(db)
	if err := st.AutoMigrate(); err != nil {
		t.Fatal(err)
	}

	h := &handlers{
		deps:  &Deps{Store: st, DB: db},
		cache: nil,
	}

	req := makeToolRequest("get_node", map[string]any{
		"qualified_name": "pkg.NotExist",
	})

	res, err := h.getNode(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("expected error response for missing node without cache")
	}
}

func TestBuildOrUpdateGraph_FlushesCache(t *testing.T) {
	deps := setupTestDeps(t)
	cache := NewCache(5 * time.Minute)
	cache.Set(`get_node:{"qualified_name":"pkg.Foo"}`, `{"id":1}`)

	h := &handlers{
		deps:  deps,
		cache: cache,
	}

	dir := t.TempDir()
	goFile := filepath.Join(dir, "test.go")
	os.WriteFile(goFile, []byte(`package main

func TestFunc() {
	return
}
`), 0644)

	req := makeToolRequest("build_or_update_graph", map[string]any{
		"path":         dir,
		"full_rebuild": true,
		"postprocess":  "none",
	})

	_, err := h.buildOrUpdateGraph(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}

	if _, ok := cache.Get(`get_node:{"qualified_name":"pkg.Foo"}`); ok {
		t.Fatal("expected cache to be flushed after buildOrUpdateGraph")
	}
}

func TestRunPostprocess_FlushesCache(t *testing.T) {
	deps := setupTestDeps(t)
	cache := NewCache(5 * time.Minute)
	cache.Set(`get_node:{"qualified_name":"pkg.Foo"}`, `{"id":1}`)

	h := &handlers{
		deps:  deps,
		cache: cache,
	}

	req := makeToolRequest("run_postprocess", map[string]any{
		"flows":       false,
		"communities": false,
		"fts":         false,
	})

	_, err := h.runPostprocess(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}

	if _, ok := cache.Get(`get_node:{"qualified_name":"pkg.Foo"}`); ok {
		t.Fatal("expected cache to be flushed after runPostprocess")
	}
}

func TestBuildRagIndex_ReturnsCount(t *testing.T) {
	deps := setupTestDeps(t)
	result := callTool(t, deps, "build_rag_index", map[string]any{})
	if result.IsError {
		t.Fatalf("build_rag_index error: %v", result.Content)
	}
	content := getTextContent(result)
	if !strings.Contains(content, "Built doc-index:") {
		t.Errorf("expected 'Built doc-index:' in output, got: %s", content)
	}
}

func TestGetRagTree_AfterBuild(t *testing.T) {
	deps := setupTestDeps(t)

	// First build the index
	buildResult := callTool(t, deps, "build_rag_index", map[string]any{})
	if buildResult.IsError {
		t.Fatalf("build_rag_index error: %v", buildResult.Content)
	}

	// Then get the tree (no community_id = full tree)
	result := callTool(t, deps, "get_rag_tree", map[string]any{})
	if result.IsError {
		t.Fatalf("get_rag_tree error: %v", result.Content)
	}
	content := getTextContent(result)
	if content == "" {
		t.Error("expected non-empty tree result")
	}
}

func TestGetDocContent_PathTraversal(t *testing.T) {
	deps := setupTestDeps(t)

	cases := []struct {
		name string
		path string
	}{
		{"relative traversal", "../../etc/passwd"},
		{"absolute path", "/etc/passwd"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := callTool(t, deps, "get_doc_content", map[string]any{
				"file_path": tc.path,
			})
			if !result.IsError {
				t.Fatalf("expected error for path %q, got success", tc.path)
			}
		})
	}
}

func TestGetDocContent_NotFound(t *testing.T) {
	deps := setupTestDeps(t)
	result := callTool(t, deps, "get_doc_content", map[string]any{
		"file_path": "docs/nonexistent.go.md",
	})
	if !result.IsError {
		t.Fatal("expected error for nonexistent file")
	}
}

func TestGetDocContent_HappyPath(t *testing.T) {
	deps := setupTestDeps(t)

	// Create a temp file with known content
	tmpFile, err := os.CreateTemp(".", "test-doc-*.md")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())
	content := "# Test Doc\nHello world"
	if _, err := tmpFile.WriteString(content); err != nil {
		t.Fatal(err)
	}
	tmpFile.Close()

	result := callTool(t, deps, "get_doc_content", map[string]any{
		"file_path": tmpFile.Name(), // relative name since created in "."
	})
	if result.IsError {
		t.Fatalf("unexpected error: %v", result.Content)
	}
	got := getTextContent(result)
	if got != content {
		t.Errorf("want %q, got %q", content, got)
	}
}

func TestGetRagTree_InvalidCommunityID(t *testing.T) {
	deps := setupTestDeps(t)

	// Build index first
	buildResult := callTool(t, deps, "build_rag_index", map[string]any{})
	if buildResult.IsError {
		t.Fatalf("build_rag_index error: %v", buildResult.Content)
	}

	// Request nonexistent community
	result := callTool(t, deps, "get_rag_tree", map[string]any{
		"community_id": "community:99999",
	})
	if !result.IsError {
		t.Fatal("expected error for nonexistent community_id")
	}
}

func TestGetRagTree_DepthLimitsChildren(t *testing.T) {
	deps := setupTestDeps(t)

	// 임시 인덱스 디렉토리 설정
	tmpDir := t.TempDir()
	deps.RagIndexDir = filepath.Join(tmpDir, ".ccg")

	// DB에 community + node + CommunityMembership 생성
	community := model.Community{Key: "auth", Label: "Auth Community", Strategy: "auto"}
	if err := deps.DB.Create(&community).Error; err != nil {
		t.Fatalf("create community: %v", err)
	}

	node := model.Node{
		QualifiedName: "auth.Login",
		Kind:          model.NodeKindFunction,
		Name:          "Login",
		FilePath:      "internal/auth/login.go",
		StartLine:     1,
		EndLine:       10,
		Language:      "go",
	}
	if err := deps.DB.Create(&node).Error; err != nil {
		t.Fatalf("create node: %v", err)
	}

	membership := model.CommunityMembership{
		CommunityID: community.ID,
		NodeID:      node.ID,
	}
	if err := deps.DB.Create(&membership).Error; err != nil {
		t.Fatalf("create membership: %v", err)
	}

	// ragindex.Builder로 인덱스 빌드
	b := &ragindex.Builder{
		DB:       deps.DB,
		OutDir:   filepath.Join(tmpDir, "docs"),
		IndexDir: deps.RagIndexDir,
	}
	if _, _, err := b.Build(); err != nil {
		t.Fatalf("Build: %v", err)
	}

	// depth=1로 get_rag_tree 호출: community 노드는 있지만 파일 노드는 없어야 함
	result := callTool(t, deps, "get_rag_tree", map[string]any{
		"depth": float64(1),
	})
	if result.IsError {
		t.Fatalf("get_rag_tree error: %v", getTextContent(result))
	}

	var treeNode ragindex.TreeNode
	if err := json.Unmarshal([]byte(getTextContent(result)), &treeNode); err != nil {
		t.Fatalf("unmarshal tree: %v", err)
	}

	if len(treeNode.Children) == 0 {
		t.Fatal("expected community nodes at depth=1, got none")
	}
	communityNode := treeNode.Children[0]
	if len(communityNode.Children) != 0 {
		t.Fatalf("expected 0 file children at depth=1, got %d", len(communityNode.Children))
	}
}
