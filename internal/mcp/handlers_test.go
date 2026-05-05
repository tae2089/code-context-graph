package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	oteltrace "go.opentelemetry.io/otel/trace"
	"gorm.io/gorm"

	"github.com/tae2089/code-context-graph/internal/analysis/changes"
	"github.com/tae2089/code-context-graph/internal/analysis/community"
	"github.com/tae2089/code-context-graph/internal/analysis/coupling"
	"github.com/tae2089/code-context-graph/internal/analysis/coverage"
	fallbackanalysis "github.com/tae2089/code-context-graph/internal/analysis/fallback"
	"github.com/tae2089/code-context-graph/internal/analysis/flows"
	"github.com/tae2089/code-context-graph/internal/analysis/incremental"
	"github.com/tae2089/code-context-graph/internal/analysis/query"
	"github.com/tae2089/code-context-graph/internal/ctxns"
	"github.com/tae2089/code-context-graph/internal/model"
	"github.com/tae2089/code-context-graph/internal/obs"
	postprocesspolicy "github.com/tae2089/code-context-graph/internal/postprocess/policy"
)

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

func TestParseProject_AllowsNamespacePathWhenRepoRootIsAlsoConfigured(t *testing.T) {
	deps := setupTestDeps(t)
	deps.RepoRoot = t.TempDir()
	deps.NamespaceRoot = t.TempDir()
	namespaceDir := filepath.Join(deps.NamespaceRoot, "sample-ns")
	if err := os.MkdirAll(namespaceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeGoFile(t, namespaceDir, "main.go", `package main
func Hello() {}
`)

	result := callTool(t, deps, "parse_project", map[string]any{"path": namespaceDir, "namespace": "sample-ns"})
	if result.IsError {
		t.Fatalf("expected namespace path to be allowed, got error: %s", getTextContent(result))
	}
}

func TestParseProject_FailsClosedWithoutConfiguredRoot(t *testing.T) {
	deps := setupTestDeps(t)
	deps.RepoRoot = ""
	deps.NamespaceRoot = ""
	dir := t.TempDir()
	writeGoFile(t, dir, "main.go", `package main
func Hello() {}
`)

	result := callTool(t, deps, "parse_project", map[string]any{"path": dir})
	if !result.IsError {
		t.Fatal("expected parse_project to fail closed without RepoRoot or NamespaceRoot")
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

func TestHandler_GetImpactRadius_LogsTraceFields(t *testing.T) {
	deps := setupTestDeps(t)
	var logBuf bytes.Buffer
	deps.Logger = slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	tel, err := obs.Setup(context.Background(), obs.Config{ServiceName: "ccg-test", Mode: "test"})
	if err != nil {
		t.Fatalf("setup telemetry: %v", err)
	}
	obs.SetGlobal(tel)
	defer func() {
		_ = tel.Shutdown(context.Background())
		obs.SetGlobal(nil)
	}()
	ctx, span := obs.StartSpan(context.Background(), "test")
	defer span.End()
	sc := oteltrace.SpanContextFromContext(ctx)

	deps.Store.UpsertNodes(context.Background(), []model.Node{{QualifiedName: "pkg.A", Kind: model.NodeKindFunction, Name: "A", FilePath: "a.go", StartLine: 1, EndLine: 5, Language: "go"}})
	result := callToolWithContext(t, ctx, deps, "get_impact_radius", map[string]any{"qualified_name": "pkg.A", "depth": 1})
	if result.IsError {
		t.Fatalf("get_impact_radius returned error: %s", getTextContent(result))
	}
	logs := logBuf.String()
	if !strings.Contains(logs, sc.TraceID().String()) {
		t.Fatalf("expected trace id in logs, got %q", logs)
	}
	if !strings.Contains(logs, sc.SpanID().String()) {
		t.Fatalf("expected span id in logs, got %q", logs)
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

func TestHandler_Search_PathFilter_CapsInternalFetchLimit(t *testing.T) {
	deps := setupTestDeps(t)
	backend := &failSearchBackend{}
	deps.SearchBackend = backend

	result := callTool(t, deps, "search", map[string]any{"query": "handle", "path": "internal/api", "limit": 500})
	if result.IsError {
		t.Fatalf("search returned error: %s", getTextContent(result))
	}
	if backend.queryLimit != 500 {
		t.Fatalf("Query limit = %d, want 500 cap", backend.queryLimit)
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

// P2-c follow-up: verify that the DocTag.Type field is included in the MCP response.
// The parser extracts and stores type from YARD/JSDoc, but if MCP serialization drops it,
// external consumers cannot receive the type information.
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

func TestHandler_GetAnnotation_ExposesCCGSeeRef(t *testing.T) {
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
			{Kind: model.TagSee, Value: "ccg://auth-svc/internal/auth/token.go#ValidateToken", Ordinal: 0},
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
	tags := ann["tags"].([]any)
	tag := tags[0].(map[string]any)
	ref, ok := tag["ref"].(map[string]any)
	if !ok {
		t.Fatalf("expected parsed ref, got %v", tag)
	}
	if ref["namespace"] != "auth-svc" || ref["path"] != "internal/auth/token.go" || ref["symbol"] != "ValidateToken" {
		t.Fatalf("unexpected ref: %v", ref)
	}
}

// ============================================================
// 11.0 Structural change (Tidy First)
// ============================================================

func TestDeps_NewInterfaces(t *testing.T) {
	// The existing six tools must still work even when the new interface fields are nil.
	deps := setupTestDepsMinimal(t)
	ctx := context.Background()

	// Set up the data required by the existing tools.
	deps.Store.UpsertNodes(ctx, []model.Node{
		{QualifiedName: "pkg.Func1", Kind: model.NodeKindFunction, Name: "Func1", FilePath: "func1.go", StartLine: 1, EndLine: 5, Language: "go"},
	})

	// Call the existing tools while every new interface field is nil.
	// QueryService, LargefuncAnalyzer, DeadcodeAnalyzer, CouplingAnalyzer,
	// CoverageAnalyzer, CommunityBuilder, and Incremental are all nil.
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
	if deps.FlowBuilder != nil {
		t.Error("expected FlowBuilder to be nil")
	}
	if deps.Incremental != nil {
		t.Error("expected Incremental to be nil")
	}

	// Verify that the existing six tools still work.
	result := callTool(t, deps, "get_node", map[string]any{"qualified_name": "pkg.Func1"})
	if result.IsError {
		t.Fatalf("get_node should work with nil new interfaces: %s", getTextContent(result))
	}
}

func TestPrompts_UsesDepsInterfaces(t *testing.T) {
	// Keep the existing five prompt tests after refactoring prompts.go to use Deps fields.
	// When QueryService, LargefuncAnalyzer, and others are set on Deps, prompts.go must use them.
	deps := setupTestDeps(t)
	ctx := context.Background()

	// Set Deps fields with mock implementations.
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

	// Set up test data.
	deps.Store.UpsertNodes(ctx, []model.Node{
		{QualifiedName: "pkg.TestFunc", Kind: model.NodeKindFunction, Name: "TestFunc", FilePath: "test.go", StartLine: 1, EndLine: 100, Language: "go"},
	})

	// Call the onboard_developer prompt; it must use LargefuncAnalyzer from Deps when present.
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

	// Check that mockLF.findCalled is true to verify that Deps.LargefuncAnalyzer was used.
	if !mockLF.findPageCalled {
		t.Error("expected prompts.go to use Deps.LargefuncAnalyzer.FindPage instead of inline creation")
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

func TestTraceFlow_ReturnsFallbackMetadata(t *testing.T) {
	deps := setupGraphOnlyTestDeps(t)
	ctx := context.Background()
	if err := deps.Store.UpsertNodes(ctx, []model.Node{
		{QualifiedName: "pkg.StartFallback", Kind: model.NodeKindFunction, Name: "StartFallback", FilePath: "s.go", StartLine: 1, EndLine: 5, Language: "go"},
		{QualifiedName: "pkg.StrictNext", Kind: model.NodeKindFunction, Name: "StrictNext", FilePath: "n.go", StartLine: 1, EndLine: 5, Language: "go"},
		{QualifiedName: "pkg.FallbackNext", Kind: model.NodeKindFunction, Name: "FallbackNext", FilePath: "f.go", StartLine: 1, EndLine: 5, Language: "go"},
	}); err != nil {
		t.Fatal(err)
	}
	start, _ := deps.Store.GetNode(ctx, "pkg.StartFallback")
	strict, _ := deps.Store.GetNode(ctx, "pkg.StrictNext")
	fallback, _ := deps.Store.GetNode(ctx, "pkg.FallbackNext")
	if err := deps.Store.UpsertEdges(ctx, []model.Edge{
		{FromNodeID: start.ID, ToNodeID: strict.ID, Kind: model.EdgeKindCalls, Fingerprint: "strict-edge"},
		{FromNodeID: start.ID, ToNodeID: fallback.ID, Kind: model.EdgeKindFallbackCalls, Fingerprint: "fallback-edge"},
	}); err != nil {
		t.Fatal(err)
	}

	result := callTool(t, deps, "trace_flow", map[string]any{"qualified_name": "pkg.StartFallback"})
	if result.IsError {
		t.Fatalf("trace_flow returned error: %s", getTextContent(result))
	}

	var flow map[string]any
	if err := json.Unmarshal([]byte(getTextContent(result)), &flow); err != nil {
		t.Fatalf("expected JSON response, got: %s", getTextContent(result))
	}
	metadata := flow["metadata"].(map[string]any)
	if metadata["contains_fallback_calls"] != true {
		t.Fatalf("expected contains_fallback_calls=true, got: %v", metadata)
	}
	if metadata["fallback_edges_count"].(float64) != 1 {
		t.Fatalf("expected fallback_edges_count=1, got: %v", metadata)
	}
}

func TestTraceFlow_RespectsIncludeFallbackCalls(t *testing.T) {
	deps := setupGraphOnlyTestDeps(t)
	ctx := context.Background()
	if err := deps.Store.UpsertNodes(ctx, []model.Node{
		{QualifiedName: "pkg.StrictStart", Kind: model.NodeKindFunction, Name: "StrictStart", FilePath: "s.go", StartLine: 1, EndLine: 5, Language: "go"},
		{QualifiedName: "pkg.StrictOnly", Kind: model.NodeKindFunction, Name: "StrictOnly", FilePath: "n.go", StartLine: 1, EndLine: 5, Language: "go"},
		{QualifiedName: "pkg.FallbackOnly", Kind: model.NodeKindFunction, Name: "FallbackOnly", FilePath: "f.go", StartLine: 1, EndLine: 5, Language: "go"},
	}); err != nil {
		t.Fatal(err)
	}
	start, _ := deps.Store.GetNode(ctx, "pkg.StrictStart")
	strict, _ := deps.Store.GetNode(ctx, "pkg.StrictOnly")
	fallback, _ := deps.Store.GetNode(ctx, "pkg.FallbackOnly")
	if err := deps.Store.UpsertEdges(ctx, []model.Edge{
		{FromNodeID: start.ID, ToNodeID: strict.ID, Kind: model.EdgeKindCalls, Fingerprint: "strict-edge"},
		{FromNodeID: start.ID, ToNodeID: fallback.ID, Kind: model.EdgeKindFallbackCalls, Fingerprint: "fallback-edge"},
	}); err != nil {
		t.Fatal(err)
	}

	result := callTool(t, deps, "trace_flow", map[string]any{
		"qualified_name":         "pkg.StrictStart",
		"include_fallback_calls": false,
	})
	if result.IsError {
		t.Fatalf("trace_flow returned error: %s", getTextContent(result))
	}

	var flow map[string]any
	if err := json.Unmarshal([]byte(getTextContent(result)), &flow); err != nil {
		t.Fatalf("expected JSON response, got: %s", getTextContent(result))
	}
	members := flow["members"].([]any)
	if len(members) != 2 {
		t.Fatalf("expected strict trace to keep 2 members, got %d", len(members))
	}
	metadata := flow["metadata"].(map[string]any)
	if metadata["contains_fallback_calls"] != false {
		t.Fatalf("expected contains_fallback_calls=false, got: %v", metadata)
	}
	if metadata["fallback_edges_count"].(float64) != 0 {
		t.Fatalf("expected fallback_edges_count=0, got: %v", metadata)
	}
}

func TestTraceFlow_CacheKeyIncludesIncludeFallbackCalls(t *testing.T) {
	deps := setupGraphOnlyTestDeps(t)
	ctx := context.Background()
	if err := deps.Store.UpsertNodes(ctx, []model.Node{
		{QualifiedName: "pkg.CacheTarget", Kind: model.NodeKindFunction, Name: "CacheTarget", FilePath: "cache.go", StartLine: 1, EndLine: 5, Language: "go"},
	}); err != nil {
		t.Fatal(err)
	}

	tracer := &mockFlowTracer{}
	deps.FlowTracer = tracer
	deps.Cache = NewCache(5 * time.Minute)

	result := callTool(t, deps, "trace_flow", map[string]any{
		"qualified_name":         "pkg.CacheTarget",
		"include_fallback_calls": false,
	})
	if result.IsError {
		t.Fatalf("trace_flow returned error: %s", getTextContent(result))
	}
	if tracer.calls != 1 {
		t.Fatalf("expected first trace_flow call to execute tracer once, got %d", tracer.calls)
	}

	result = callTool(t, deps, "trace_flow", map[string]any{
		"qualified_name":         "pkg.CacheTarget",
		"include_fallback_calls": false,
	})
	if result.IsError {
		t.Fatalf("trace_flow returned error: %s", getTextContent(result))
	}
	if tracer.calls != 1 {
		t.Fatalf("expected cache hit for identical include_fallback_calls, got calls=%d", tracer.calls)
	}

	result = callTool(t, deps, "trace_flow", map[string]any{
		"qualified_name":         "pkg.CacheTarget",
		"include_fallback_calls": true,
	})
	if result.IsError {
		t.Fatalf("trace_flow returned error: %s", getTextContent(result))
	}
	if tracer.calls != 2 {
		t.Fatalf("expected different include_fallback_calls to execute tracer again, got calls=%d", tracer.calls)
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

	// The parsed node must exist.
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
		"namespace":     "svc",
		"include_paths": []string{"src/api"},
	})

	if !mockSync.syncWithExisting {
		t.Fatal("expected Incremental.SyncWithExisting to be called")
	}
	if len(mockSync.existingCalls) == 0 {
		t.Fatal("expected at least one SyncWithExisting call")
	}
	firstExisting := mockSync.existingCalls[0]
	if len(firstExisting) != 2 {
		t.Fatalf("expected default replace semantics to pass all namespace files, got %v", firstExisting)
	}
	if !containsStringInSlice(firstExisting, filepath.Join("src", "other", "other.go")) {
		t.Fatalf("expected existingFiles to include out-of-scope file under default replace semantics, got %v", firstExisting)
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
		"namespace":     "svc",
		"include_paths": []string{"src/api"},
		"replace":       false,
	})

	if !mockSync.syncWithExisting {
		t.Fatal("expected Incremental.SyncWithExisting to be called")
	}
	if len(mockSync.existingCalls) == 0 {
		t.Fatal("expected at least one SyncWithExisting call")
	}
	firstExisting := mockSync.existingCalls[0]
	if containsStringInSlice(firstExisting, filepath.Join("src", "other", "other.go")) {
		t.Fatalf("expected replace=false to exclude out-of-scope file from existingFiles, got %v", firstExisting)
	}
	if !containsStringInSlice(firstExisting, filepath.Join("src", "api", "handler.go")) {
		t.Fatalf("expected replace=false to keep in-scope file, got %v", firstExisting)
	}
}

func TestBuildOrUpdateGraph_IncrementalSkipsUnreadableFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("broken symlink unreadable path scenario is unix-specific")
	}

	deps := setupTestDeps(t)
	mockSync := &mockIncrementalSyncer{
		result: &incremental.SyncStats{Added: 1, Modified: 0, Skipped: 0, Deleted: 0},
	}
	deps.Incremental = mockSync

	dir := t.TempDir()
	writeGoFile(t, dir, "keep.go", `package keep
func Keep() {}
`)
	writeGoFile(t, dir, "temp.go", `package keep
func Temp() {}
`)
	if err := os.Remove(filepath.Join(dir, "temp.go")); err != nil {
		t.Fatalf("remove file: %v", err)
	}
	if err := os.Symlink(filepath.Join(dir, "missing.go"), filepath.Join(dir, "temp.go")); err != nil {
		t.Fatalf("create broken symlink: %v", err)
	}

	result := callTool(t, deps, "build_or_update_graph", map[string]any{
		"path":         dir,
		"full_rebuild": false,
		"postprocess":  "none",
	})
	if result.IsError {
		t.Fatalf("expected unreadable file to be skipped during incremental build, got: %s", getTextContent(result))
	}
	if !mockSync.syncWithExisting {
		t.Fatal("expected Incremental.SyncWithExisting to be called")
	}
	if len(mockSync.files) != 1 {
		t.Fatalf("expected only readable file to be synced, got %v", mockSync.files)
	}
	if _, ok := mockSync.files["keep.go"]; !ok {
		t.Fatalf("expected keep.go in sync files, got %v", mockSync.files)
	}
	if _, ok := mockSync.files["temp.go"]; ok {
		t.Fatalf("expected unreadable temp.go to be skipped, got %v", mockSync.files)
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
	mockFlow := &mockFlowBuilder{}
	deps.CommunityBuilder = mockComm
	deps.FlowBuilder = mockFlow

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
	if !mockFlow.rebuildCalled {
		t.Error("expected FlowBuilder.Rebuild to be called for postprocess=full")
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

func TestBuildOrUpdateGraph_EmptyFailedStepsIsNullJSON(t *testing.T) {
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

	var resp struct {
		FailedSteps  json.RawMessage `json:"failed_steps"`
		SkippedSteps json.RawMessage `json:"skipped_steps"`
		Status       string          `json:"status"`
	}
	text := getTextContent(result)
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("expected JSON, got: %s", text)
	}
	if string(resp.FailedSteps) != "null" {
		t.Fatalf("expected failed_steps=null, got %s", string(resp.FailedSteps))
	}
	if string(resp.SkippedSteps) == "null" || len(resp.SkippedSteps) == 0 {
		t.Fatalf("expected skipped_steps array, got %s", string(resp.SkippedSteps))
	}

	var skipped []any
	if err := json.Unmarshal(resp.SkippedSteps, &skipped); err != nil {
		t.Fatalf("expected skipped_steps array, got %s", string(resp.SkippedSteps))
	}
	if !containsString(skipped, "communities") || !containsString(skipped, "flows") {
		t.Fatalf("expected skipped_steps to contain communities and flows, got %v", skipped)
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

func TestBuildOrUpdateGraph_FailClosedOnCommunityFailure(t *testing.T) {
	deps := setupTestDeps(t)
	deps.CommunityBuilder = &mockCommunityBuilder{err: errors.New("community rebuild boom")}

	dir := t.TempDir()
	writeGoFile(t, dir, "svc.go", `package svc

func Run() {}
`)

	result := callTool(t, deps, "build_or_update_graph", map[string]any{
		"path":               dir,
		"full_rebuild":       true,
		"postprocess":        "full",
		"postprocess_policy": "fail_closed",
	})
	if !result.IsError {
		t.Fatalf("expected fail_closed community failure to return tool error, got: %s", getTextContent(result))
	}
	if !strings.Contains(getTextContent(result), "community rebuild boom") {
		t.Fatalf("unexpected error: %s", getTextContent(result))
	}
}

func TestBuildOrUpdateGraph_DegradedOnSearchDocumentRefreshFailure(t *testing.T) {
	deps := setupTestDeps(t)
	backend := &failSearchBackend{}
	deps.SearchBackend = backend
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
	if containsString(failedSteps, "fts") {
		t.Fatalf("expected refresh failure to skip fts rebuild, got %v", failedSteps)
	}
	if backend.rebuildCalls != 0 {
		t.Fatalf("expected no fts rebuild after refresh failure, got %d calls", backend.rebuildCalls)
	}
}

func TestBuildOrUpdateGraph_MinimalSkipsFTSRebuildOnSearchDocumentRefreshFailure(t *testing.T) {
	deps := setupTestDeps(t)
	backend := &failSearchBackend{}
	deps.SearchBackend = backend
	origRefresh := refreshSearchDocuments
	defer func() { refreshSearchDocuments = origRefresh }()
	refreshSearchDocuments = func(ctx context.Context, db *gorm.DB) (int, error) {
		return 0, errors.New("search document refresh boom")
	}

	dir := t.TempDir()
	writeGoFile(t, dir, "svc.go", "package svc\n\nfunc Run() {}\n")

	result := callTool(t, deps, "build_or_update_graph", map[string]any{
		"path":         dir,
		"full_rebuild": true,
		"postprocess":  "minimal",
	})
	if result.IsError {
		t.Fatalf("build_or_update_graph should not return tool error, got: %s", getTextContent(result))
	}

	var resp map[string]any
	if err := json.Unmarshal([]byte(getTextContent(result)), &resp); err != nil {
		t.Fatalf("expected JSON, got: %s", getTextContent(result))
	}
	failedSteps, ok := resp["failed_steps"].([]any)
	if !ok {
		t.Fatalf("expected failed_steps array, got %v", resp["failed_steps"])
	}
	if !containsString(failedSteps, "search_documents") {
		t.Fatalf("expected search_documents in failed_steps, got %v", failedSteps)
	}
	if containsString(failedSteps, "fts") {
		t.Fatalf("expected refresh failure to skip fts rebuild, got %v", failedSteps)
	}
	if backend.rebuildCalls != 0 {
		t.Fatalf("expected no fts rebuild after refresh failure, got %d calls", backend.rebuildCalls)
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

func TestBuildOrUpdateGraph_RejectsInvalidPostprocessPolicy(t *testing.T) {
	deps := setupTestDeps(t)
	dir := t.TempDir()
	writeGoFile(t, dir, "svc.go", `package svc

func Run() {}
`)

	result := callTool(t, deps, "build_or_update_graph", map[string]any{
		"path":               dir,
		"full_rebuild":       true,
		"postprocess":        "none",
		"postprocess_policy": "strict",
	})
	if !result.IsError {
		t.Fatalf("expected tool error for invalid postprocess_policy, got: %s", getTextContent(result))
	}
	if !strings.Contains(getTextContent(result), "postprocess_policy must be degraded or fail_closed") {
		t.Fatalf("unexpected error: %s", getTextContent(result))
	}
}

func TestBuildOrUpdateGraph_UsesAutomaticPolicyWhenNotExplicitlyProvided(t *testing.T) {
	deps := setupTestDeps(t)
	stub := &stubPostprocessPolicy{resolvedPolicy: "fail_closed", resolvedSource: "auto"}
	deps.PostprocessPolicy = stub
	deps.CommunityBuilder = &mockCommunityBuilder{err: errors.New("community rebuild boom")}

	dir := t.TempDir()
	writeGoFile(t, dir, "svc.go", "package svc\nfunc Run() {}\n")

	result := callTool(t, deps, "build_or_update_graph", map[string]any{
		"path":         dir,
		"full_rebuild": true,
		"postprocess":  "full",
	})
	if !result.IsError {
		t.Fatalf("expected auto fail_closed policy to return tool error, got: %s", getTextContent(result))
	}
	if len(stub.resolvedInputs) != 1 {
		t.Fatalf("resolve calls = %d, want 1", len(stub.resolvedInputs))
	}
	if stub.resolvedInputs[0].ExplicitPolicy != "" {
		t.Fatalf("explicit policy = %q, want empty", stub.resolvedInputs[0].ExplicitPolicy)
	}
}

func TestBuildOrUpdateGraph_PassesExplicitPolicyToResolverAndRecordsRun(t *testing.T) {
	deps := setupTestDeps(t)
	stub := &stubPostprocessPolicy{resolvedPolicy: "degraded", resolvedSource: "explicit"}
	deps.PostprocessPolicy = stub
	deps.CommunityBuilder = &mockCommunityBuilder{err: errors.New("community rebuild boom")}

	dir := t.TempDir()
	writeGoFile(t, dir, "svc.go", "package svc\nfunc Run() {}\n")

	result := callTool(t, deps, "build_or_update_graph", map[string]any{
		"path":               dir,
		"full_rebuild":       true,
		"postprocess":        "full",
		"postprocess_policy": "degraded",
	})
	if result.IsError {
		t.Fatalf("expected explicit degraded resolver result not to error, got: %s", getTextContent(result))
	}
	if got := len(stub.resolvedInputs); got != 1 {
		t.Fatalf("resolve calls = %d, want 1", got)
	}
	if stub.resolvedInputs[0].ExplicitPolicy != "degraded" {
		t.Fatalf("explicit policy = %q, want degraded", stub.resolvedInputs[0].ExplicitPolicy)
	}
	if got := len(stub.recordedRuns); got != 1 {
		t.Fatalf("recorded runs = %d, want 1", got)
	}
	if stub.recordedRuns[0].Policy != "degraded" {
		t.Fatalf("recorded policy = %q, want degraded", stub.recordedRuns[0].Policy)
	}
	if stub.recordedRuns[0].Source != "explicit" {
		t.Fatalf("recorded source = %q, want explicit", stub.recordedRuns[0].Source)
	}
	if stub.recordedRuns[0].Tool != "build_or_update_graph" {
		t.Fatalf("recorded tool = %q, want build_or_update_graph", stub.recordedRuns[0].Tool)
	}

	var resp map[string]any
	if err := json.Unmarshal([]byte(getTextContent(result)), &resp); err != nil {
		t.Fatalf("expected JSON, got: %s", getTextContent(result))
	}
	if resp["postprocess_policy"] != "degraded" {
		t.Fatalf("response postprocess_policy = %v, want degraded", resp["postprocess_policy"])
	}
	if resp["policy_source"] != "explicit" {
		t.Fatalf("response policy_source = %v, want explicit", resp["policy_source"])
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
	mockFlow := &mockFlowBuilder{result: []flows.Stats{{NodeCount: 2}}}
	deps.CommunityBuilder = mockComm
	deps.FlowBuilder = mockFlow

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
	if !mockFlow.rebuildCalled {
		t.Error("expected FlowBuilder.Rebuild to be called")
	}
	if got := resp["flows_count"]; got != float64(1) {
		t.Errorf("expected flows_count=1 after flow rebuild, got %v", got)
	}
	skipped, ok := resp["skipped_steps"].([]any)
	if !ok {
		t.Fatalf("expected skipped_steps array, got %v", resp["skipped_steps"])
	}
	if containsString(skipped, "flows") {
		t.Fatalf("expected skipped_steps to omit flows when builder is configured, got %v", resp["skipped_steps"])
	}
}

func TestRunPostprocess_FlowsSkippedWhenBuilderNil(t *testing.T) {
	deps := setupTestDeps(t)
	deps.FlowBuilder = nil

	result := callTool(t, deps, "run_postprocess", map[string]any{
		"flows":       true,
		"communities": false,
		"fts":         false,
	})
	if result.IsError {
		t.Fatalf("run_postprocess error: %s", getTextContent(result))
	}

	var resp map[string]any
	if err := json.Unmarshal([]byte(getTextContent(result)), &resp); err != nil {
		t.Fatalf("expected JSON, got: %s", getTextContent(result))
	}
	if got := resp["flows_count"]; got != float64(0) {
		t.Fatalf("expected flows_count=0 when builder missing, got %v", got)
	}
	skipped, ok := resp["skipped_steps"].([]any)
	if !ok || !containsString(skipped, "flows") {
		t.Fatalf("expected skipped_steps to contain flows, got %v", resp["skipped_steps"])
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

func TestRunPostprocess_EmptyFailedStepsIsNullJSON(t *testing.T) {
	deps := setupTestDeps(t)

	result := callTool(t, deps, "run_postprocess", map[string]any{
		"flows":       false,
		"communities": false,
		"fts":         false,
	})
	if result.IsError {
		t.Fatalf("run_postprocess error: %s", getTextContent(result))
	}

	var resp struct {
		FailedSteps  json.RawMessage `json:"failed_steps"`
		SkippedSteps json.RawMessage `json:"skipped_steps"`
		Status       string          `json:"status"`
	}
	text := getTextContent(result)
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("expected JSON, got: %s", text)
	}
	if string(resp.FailedSteps) != "null" {
		t.Fatalf("expected failed_steps=null, got %s", string(resp.FailedSteps))
	}
	if string(resp.SkippedSteps) == "null" || len(resp.SkippedSteps) == 0 {
		t.Fatalf("expected skipped_steps array, got %s", string(resp.SkippedSteps))
	}

	var skipped []any
	if err := json.Unmarshal(resp.SkippedSteps, &skipped); err != nil {
		t.Fatalf("expected skipped_steps array, got %s", string(resp.SkippedSteps))
	}
	for _, want := range []string{"flows", "communities", "search_documents", "fts"} {
		if !containsString(skipped, want) {
			t.Fatalf("expected skipped_steps to contain %s, got %v", want, skipped)
		}
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

func TestRunPostprocess_UsesAutomaticPolicyWhenNotExplicitlyProvided(t *testing.T) {
	deps := setupTestDeps(t)
	stub := &stubPostprocessPolicy{resolvedPolicy: "fail_closed", resolvedSource: "auto"}
	deps.PostprocessPolicy = stub
	deps.CommunityBuilder = &mockCommunityBuilder{err: errors.New("community rebuild boom")}

	result := callTool(t, deps, "run_postprocess", map[string]any{
		"communities": true,
		"fts":         false,
		"flows":       false,
	})
	if !result.IsError {
		t.Fatalf("expected auto fail_closed policy to return tool error, got: %s", getTextContent(result))
	}
	if len(stub.resolvedInputs) != 1 {
		t.Fatalf("resolve calls = %d, want 1", len(stub.resolvedInputs))
	}
	if stub.resolvedInputs[0].Tool != "run_postprocess" {
		t.Fatalf("resolver tool = %q, want run_postprocess", stub.resolvedInputs[0].Tool)
	}
	if stub.resolvedInputs[0].ExplicitPolicy != "" {
		t.Fatalf("explicit policy = %q, want empty", stub.resolvedInputs[0].ExplicitPolicy)
	}
}

func TestRunPostprocess_PassesExplicitPolicyToResolverAndRecordsRun(t *testing.T) {
	deps := setupTestDeps(t)
	stub := &stubPostprocessPolicy{resolvedPolicy: "degraded", resolvedSource: "explicit"}
	deps.PostprocessPolicy = stub
	deps.SearchBackend = &failSearchBackend{err: errors.New("fts rebuild boom")}

	result := callTool(t, deps, "run_postprocess", map[string]any{
		"communities":        false,
		"fts":                true,
		"flows":              false,
		"postprocess_policy": "degraded",
	})
	if result.IsError {
		t.Fatalf("expected explicit degraded resolver result not to error, got: %s", getTextContent(result))
	}
	if got := len(stub.resolvedInputs); got != 1 {
		t.Fatalf("resolve calls = %d, want 1", got)
	}
	if stub.resolvedInputs[0].ExplicitPolicy != "degraded" {
		t.Fatalf("explicit policy = %q, want degraded", stub.resolvedInputs[0].ExplicitPolicy)
	}
	if got := len(stub.recordedRuns); got != 1 {
		t.Fatalf("recorded runs = %d, want 1", got)
	}
	if stub.recordedRuns[0].Tool != "run_postprocess" {
		t.Fatalf("recorded tool = %q, want run_postprocess", stub.recordedRuns[0].Tool)
	}
	if stub.recordedRuns[0].Policy != "degraded" {
		t.Fatalf("recorded policy = %q, want degraded", stub.recordedRuns[0].Policy)
	}

	var resp map[string]any
	if err := json.Unmarshal([]byte(getTextContent(result)), &resp); err != nil {
		t.Fatalf("expected JSON, got: %s", getTextContent(result))
	}
	if resp["postprocess_policy"] != "degraded" {
		t.Fatalf("response postprocess_policy = %v, want degraded", resp["postprocess_policy"])
	}
	if resp["policy_source"] != "explicit" {
		t.Fatalf("response policy_source = %v, want explicit", resp["policy_source"])
	}
}

func TestRunPostprocess_AutoEscalatesAfterThreeFailuresWithRealPolicyStore(t *testing.T) {
	deps := setupTestDepsWithRealPostprocessPolicy(t)
	deps.CommunityBuilder = &mockCommunityBuilder{err: errors.New("community rebuild boom")}

	for i := 0; i < 3; i++ {
		result := callTool(t, deps, "run_postprocess", map[string]any{
			"communities": true,
			"fts":         false,
			"flows":       false,
		})
		if result.IsError {
			t.Fatalf("attempt %d should be degraded before escalation, got: %s", i+1, getTextContent(result))
		}
		var resp map[string]any
		if err := json.Unmarshal([]byte(getTextContent(result)), &resp); err != nil {
			t.Fatalf("expected JSON, got: %s", getTextContent(result))
		}
		if resp["postprocess_policy"] != "degraded" {
			t.Fatalf("attempt %d policy = %v, want degraded", i+1, resp["postprocess_policy"])
		}
	}

	result := callTool(t, deps, "run_postprocess", map[string]any{
		"communities": true,
		"fts":         false,
		"flows":       false,
	})
	if !result.IsError {
		t.Fatalf("expected fourth attempt to fail_closed, got: %s", getTextContent(result))
	}

	policyStore := postprocesspolicy.NewStore(deps.DB)
	count, err := policyStore.ConsecutiveFailures(context.Background(), postprocesspolicy.ToolRunPostprocess, 10)
	if err != nil {
		t.Fatalf("consecutive failures: %v", err)
	}
	if count != 4 {
		t.Fatalf("consecutive failures = %d, want 4", count)
	}
	state, err := policyStore.GetState(context.Background(), postprocesspolicy.ToolRunPostprocess)
	if err != nil {
		t.Fatalf("get state: %v", err)
	}
	if state == nil || state.Policy != postprocesspolicy.PolicyFailClosed {
		t.Fatalf("state policy = %v, want fail_closed", state)
	}
}

func TestRunPostprocess_RealPolicyStoreIsolatedByNamespaceAndTool(t *testing.T) {
	deps := setupTestDepsWithRealPostprocessPolicy(t)
	deps.CommunityBuilder = &mockCommunityBuilder{err: errors.New("community rebuild boom")}

	for i := 0; i < 3; i++ {
		result := callToolWithNamespace(t, deps, "ns-a", "run_postprocess", map[string]any{
			"communities": true,
			"fts":         false,
			"flows":       false,
		})
		if result.IsError {
			t.Fatalf("ns-a attempt %d should be degraded, got: %s", i+1, getTextContent(result))
		}
	}

	dir := t.TempDir()
	writeGoFile(t, dir, "svc.go", "package svc\nfunc Run() {}\n")
	buildResult := callToolWithNamespace(t, deps, "ns-b", "build_or_update_graph", map[string]any{
		"path":         dir,
		"full_rebuild": true,
		"postprocess":  "none",
	})
	if buildResult.IsError {
		t.Fatalf("ns-b build should not be affected by ns-a run_postprocess failures, got: %s", getTextContent(buildResult))
	}

	fourth := callToolWithNamespace(t, deps, "ns-a", "run_postprocess", map[string]any{
		"communities": true,
		"fts":         false,
		"flows":       false,
	})
	if !fourth.IsError {
		t.Fatalf("expected ns-a fourth run_postprocess attempt to fail_closed, got: %s", getTextContent(fourth))
	}
}

func TestGetPostprocessPolicy_UsesPolicyStatusSummary(t *testing.T) {
	deps := setupTestDeps(t)
	deps.PostprocessPolicy = &stubPostprocessPolicy{
		statusSummary: &postprocesspolicy.StatusSummary{
			Status: postprocesspolicy.StatusDegraded,
			FailClosed: []postprocesspolicy.StateSnapshot{{
				Namespace:           ctxns.DefaultNamespace,
				Tool:                postprocesspolicy.ToolRunPostprocess,
				Policy:              postprocesspolicy.PolicyFailClosed,
				ConsecutiveFailures: 3,
			}},
		},
	}

	result := callTool(t, deps, "get_postprocess_policy", map[string]any{"tool": "run_postprocess", "recent_limit": 3})
	if result.IsError {
		t.Fatalf("get_postprocess_policy error: %s", getTextContent(result))
	}
	stub := deps.PostprocessPolicy.(*stubPostprocessPolicy)
	if len(stub.statusInputs) != 1 {
		t.Fatalf("status inputs = %d, want 1", len(stub.statusInputs))
	}
	if stub.statusInputs[0].Tool != postprocesspolicy.ToolRunPostprocess {
		t.Fatalf("status tool = %q, want run_postprocess", stub.statusInputs[0].Tool)
	}
	var resp map[string]any
	if err := json.Unmarshal([]byte(getTextContent(result)), &resp); err != nil {
		t.Fatalf("expected JSON, got: %s", getTextContent(result))
	}
	if resp["status"] != "degraded" {
		t.Fatalf("status = %v, want degraded", resp["status"])
	}
}

func TestResetPostprocessPolicy_RecordsResetForTool(t *testing.T) {
	deps := setupTestDeps(t)
	deps.PostprocessPolicy = &stubPostprocessPolicy{}

	result := callTool(t, deps, "reset_postprocess_policy", map[string]any{"tool": "run_postprocess"})
	if result.IsError {
		t.Fatalf("reset_postprocess_policy error: %s", getTextContent(result))
	}
	stub := deps.PostprocessPolicy.(*stubPostprocessPolicy)
	if len(stub.resetTools) != 1 {
		t.Fatalf("reset tools = %d, want 1", len(stub.resetTools))
	}
	if stub.resetTools[0] != postprocesspolicy.ToolRunPostprocess {
		t.Fatalf("reset tool = %q, want run_postprocess", stub.resetTools[0])
	}
}

func TestResetPostprocessPolicy_RejectsInvalidTool(t *testing.T) {
	deps := setupTestDeps(t)
	deps.PostprocessPolicy = &stubPostprocessPolicy{}

	result := callTool(t, deps, "reset_postprocess_policy", map[string]any{"tool": "other"})
	if !result.IsError {
		t.Fatalf("expected invalid tool to fail, got: %s", getTextContent(result))
	}
	if !strings.Contains(getTextContent(result), "tool must be build_or_update_graph or run_postprocess") {
		t.Fatalf("unexpected error: %s", getTextContent(result))
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
	if !mockQ.callersPageCalled {
		t.Error("expected CallersOfPage to be called")
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
	if !mockQ.calleesPageCalled {
		t.Error("expected CalleesOfPage to be called")
	}
}

func TestQueryGraph_CalleesOf_RespectsIncludeFallbackCalls(t *testing.T) {
	deps := setupGraphOnlyTestDeps(t)
	ctx := context.Background()

	mockQ := &mockQueryService{result: []model.Node{}}
	deps.QueryService = mockQ

	deps.Store.UpsertNodes(ctx, []model.Node{
		{QualifiedName: "pkg.Func", Kind: model.NodeKindFunction, Name: "Func", FilePath: "func.go", StartLine: 1, EndLine: 5, Language: "go"},
	})

	result := callTool(t, deps, "query_graph", map[string]any{
		"pattern":                "callees_of",
		"target":                 "pkg.Func",
		"include_fallback_calls": false,
	})
	if result.IsError {
		t.Fatalf("query_graph error: %s", getTextContent(result))
	}
	if !mockQ.calleesPageCalled {
		t.Fatal("expected CalleesOfPage to be called for query_graph")
	}
	if mockQ.calleesPageOpts.IncludeFallbackCalls == nil {
		t.Fatal("expected include_fallback_calls option to be set")
	}
	if *mockQ.calleesPageOpts.IncludeFallbackCalls {
		t.Fatal("expected include_fallback_calls=false to be forwarded to QueryService")
	}
}

func TestQueryGraph_CalleesOf_ReturnsTentativeProvenance(t *testing.T) {
	deps := setupGraphOnlyTestDeps(t)
	ctx := context.Background()

	if err := deps.Store.UpsertNodes(ctx, []model.Node{
		{QualifiedName: "pkg.Source", Kind: model.NodeKindFunction, Name: "Source", FilePath: "source.go", StartLine: 1, EndLine: 5, Language: "go"},
		{QualifiedName: "pkg.Strict", Kind: model.NodeKindFunction, Name: "Strict", FilePath: "strict.go", StartLine: 1, EndLine: 5, Language: "go"},
		{QualifiedName: "pkg.Fallback", Kind: model.NodeKindFunction, Name: "Fallback", FilePath: "fallback.go", StartLine: 1, EndLine: 5, Language: "go"},
	}); err != nil {
		t.Fatal(err)
	}
	source, _ := deps.Store.GetNode(ctx, "pkg.Source")
	strict, _ := deps.Store.GetNode(ctx, "pkg.Strict")
	fallback, _ := deps.Store.GetNode(ctx, "pkg.Fallback")
	if err := deps.Store.UpsertEdges(ctx, []model.Edge{
		{FromNodeID: source.ID, ToNodeID: strict.ID, Kind: model.EdgeKindCalls, FilePath: "pkg/source.go", Line: 11, Fingerprint: "source-strict"},
		{FromNodeID: source.ID, ToNodeID: fallback.ID, Kind: model.EdgeKindFallbackCalls, FilePath: "pkg/fallback.go", Line: 22, Fingerprint: "source-fallback"},
	}); err != nil {
		t.Fatal(err)
	}
	deps.QueryService = query.New(deps.DB)

	result := callTool(t, deps, "query_graph", map[string]any{"pattern": "callees_of", "target": "pkg.Source"})
	if result.IsError {
		t.Fatalf("query_graph error: %s", getTextContent(result))
	}

	var resp struct {
		Results  []map[string]any `json:"results"`
		Metadata map[string]any   `json:"metadata"`
	}
	if err := json.Unmarshal([]byte(getTextContent(result)), &resp); err != nil {
		t.Fatalf("expected JSON response, got: %s", getTextContent(result))
	}
	if len(resp.Results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(resp.Results))
	}

	byName := map[string]map[string]any{}
	for _, item := range resp.Results {
		byName[item["qualified_name"].(string)] = item
	}
	if byName["pkg.Strict"]["confidence"] != "strict" {
		t.Fatalf("expected pkg.Strict confidence=strict, got %v", byName["pkg.Strict"]["confidence"])
	}
	if byName["pkg.Strict"]["edge_kind"] != string(model.EdgeKindCalls) {
		t.Fatalf("expected pkg.Strict edge_kind=calls, got %v", byName["pkg.Strict"]["edge_kind"])
	}
	if byName["pkg.Strict"]["evidence"] == nil {
		t.Fatal("expected strict item evidence")
	}
	strictEvidence := byName["pkg.Strict"]["evidence"].(map[string]any)
	if strictEvidence["fingerprint"] != "source-strict" {
		t.Fatalf("expected strict evidence fingerprint=source-strict, got %v", strictEvidence["fingerprint"])
	}
	if strictEvidence["line"] != float64(11) {
		t.Fatalf("expected strict evidence line=11, got %v", strictEvidence["line"])
	}

	if byName["pkg.Fallback"]["confidence"] != "tentative" {
		t.Fatalf("expected pkg.Fallback confidence=tentative, got %v", byName["pkg.Fallback"]["confidence"])
	}
	if byName["pkg.Fallback"]["edge_kind"] != string(model.EdgeKindFallbackCalls) {
		t.Fatalf("expected pkg.Fallback edge_kind=fallback_calls, got %v", byName["pkg.Fallback"]["edge_kind"])
	}
	if byName["pkg.Fallback"]["evidence"] == nil {
		t.Fatal("expected tentative item evidence")
	}
	fallbackEvidence := byName["pkg.Fallback"]["evidence"].(map[string]any)
	if fallbackEvidence["fingerprint"] != "source-fallback" {
		t.Fatalf("expected tentative evidence fingerprint=source-fallback, got %v", fallbackEvidence["fingerprint"])
	}
	if fallbackEvidence["line"] != float64(22) {
		t.Fatalf("expected tentative evidence line=22, got %v", fallbackEvidence["line"])
	}

	metadata := resp.Metadata
	if metadata["strict_count"] != float64(1) {
		t.Fatalf("expected strict_count=1, got %v", metadata["strict_count"])
	}
	if metadata["tentative_count"] != float64(1) {
		t.Fatalf("expected tentative_count=1, got %v", metadata["tentative_count"])
	}
}

func TestQueryGraph_CalleesOf_PaginationMetadata(t *testing.T) {
	deps := setupGraphOnlyTestDeps(t)
	ctx := context.Background()
	deps.QueryService = query.New(deps.DB)

	if err := deps.Store.UpsertNodes(ctx, []model.Node{
		{QualifiedName: "pkg.Source", Kind: model.NodeKindFunction, Name: "Source", FilePath: "source.go", StartLine: 1, EndLine: 5, Language: "go"},
		{QualifiedName: "pkg.C2", Kind: model.NodeKindFunction, Name: "C2", FilePath: "a.go", StartLine: 20, EndLine: 25, Language: "go"},
		{QualifiedName: "pkg.C1", Kind: model.NodeKindFunction, Name: "C1", FilePath: "a.go", StartLine: 10, EndLine: 15, Language: "go"},
		{QualifiedName: "pkg.C3", Kind: model.NodeKindFunction, Name: "C3", FilePath: "b.go", StartLine: 5, EndLine: 10, Language: "go"},
	}); err != nil {
		t.Fatal(err)
	}
	source, err := deps.Store.GetNode(ctx, "pkg.Source")
	if err != nil {
		t.Fatal(err)
	}
	c1, err := deps.Store.GetNode(ctx, "pkg.C1")
	if err != nil {
		t.Fatal(err)
	}
	c2, err := deps.Store.GetNode(ctx, "pkg.C2")
	if err != nil {
		t.Fatal(err)
	}
	c3, err := deps.Store.GetNode(ctx, "pkg.C3")
	if err != nil {
		t.Fatal(err)
	}
	if err := deps.Store.UpsertEdges(ctx, []model.Edge{
		{FromNodeID: source.ID, ToNodeID: c2.ID, Kind: model.EdgeKindCalls, Fingerprint: "c2"},
		{FromNodeID: source.ID, ToNodeID: c1.ID, Kind: model.EdgeKindCalls, Fingerprint: "c1"},
		{FromNodeID: source.ID, ToNodeID: c3.ID, Kind: model.EdgeKindCalls, Fingerprint: "c3"},
	}); err != nil {
		t.Fatal(err)
	}

	result := callTool(t, deps, "query_graph", map[string]any{"pattern": "callees_of", "target": "pkg.Source", "limit": 2, "offset": 0})
	if result.IsError {
		t.Fatalf("query_graph error: %s", getTextContent(result))
	}

	var resp struct {
		Results  []map[string]any `json:"results"`
		Metadata map[string]any   `json:"metadata"`
	}
	if err := json.Unmarshal([]byte(getTextContent(result)), &resp); err != nil {
		t.Fatalf("expected JSON response, got: %s", getTextContent(result))
	}
	if len(resp.Results) != 2 {
		t.Fatalf("expected 2 paged results, got %d", len(resp.Results))
	}
	if resp.Metadata["returned_count"].(float64) != 2 {
		t.Fatalf("expected returned_count=2, got %v", resp.Metadata["returned_count"])
	}
	if resp.Metadata["total_count"].(float64) != 3 {
		t.Fatalf("expected total_count=3, got %v", resp.Metadata["total_count"])
	}
	if resp.Metadata["truncated"] != true {
		t.Fatal("expected truncated=true")
	}
	if resp.Metadata["next_offset"].(float64) != 2 {
		t.Fatalf("expected next_offset=2, got %v", resp.Metadata["next_offset"])
	}
}

func TestQueryGraph_CacheKeyIncludesFallbackFlag(t *testing.T) {
	deps := setupGraphOnlyTestDeps(t)
	ctx := context.Background()

	mockQ := &mockQueryService{
		result: []model.Node{
			{QualifiedName: "pkg.Caller", Kind: model.NodeKindFunction, Name: "Caller", FilePath: "caller.go"},
		},
	}
	deps.QueryService = mockQ

	deps.Store.UpsertNodes(ctx, []model.Node{
		{QualifiedName: "pkg.Func", Kind: model.NodeKindFunction, Name: "Func", FilePath: "func.go", StartLine: 1, EndLine: 5, Language: "go"},
		{QualifiedName: "pkg.Caller", Kind: model.NodeKindFunction, Name: "Caller", FilePath: "caller.go", StartLine: 10, EndLine: 20, Language: "go"},
	})

	result := callTool(t, deps, "query_graph", map[string]any{
		"pattern":                "callees_of",
		"target":                 "pkg.Func",
		"include_fallback_calls": false,
	})
	if result.IsError {
		t.Fatalf("query_graph error: %s", getTextContent(result))
	}
	result = callTool(t, deps, "query_graph", map[string]any{
		"pattern":                "callees_of",
		"target":                 "pkg.Func",
		"include_fallback_calls": true,
	})
	if result.IsError {
		t.Fatalf("query_graph error: %s", getTextContent(result))
	}

	if mockQ.calleesPageCalls != 3 {
		t.Fatalf("expected 3 callee page calls (false:1, true:2), got %d", mockQ.calleesPageCalls)
	}
}

func TestQueryGraph_ErrorsWhenLimitExceedsMax(t *testing.T) {
	deps := setupGraphOnlyTestDeps(t)
	ctx := context.Background()

	deps.Store.UpsertNodes(ctx, []model.Node{
		{QualifiedName: "pkg.Source", Kind: model.NodeKindFunction, Name: "Source", FilePath: "source.go", StartLine: 1, EndLine: 5, Language: "go"},
	})
	deps.QueryService = query.New(deps.DB)

	result := callTool(t, deps, "query_graph", map[string]any{
		"pattern": "callees_of",
		"target":  "pkg.Source",
		"limit":   501,
	})
	if !result.IsError {
		t.Fatalf("expected query_graph error for limit > 500, got: %s", getTextContent(result))
	}
	if !strings.Contains(getTextContent(result), "limit must be <= 500") {
		t.Fatalf("unexpected error: %s", getTextContent(result))
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
	if !mockQ.importsOfPageCalled {
		t.Error("expected ImportsOfPage to be called")
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
	if !mockQ.importersOfPageCalled {
		t.Error("expected ImportersOfPage to be called")
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
	if !mockQ.childrenOfPageCalled {
		t.Error("expected ChildrenOfPage to be called")
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
	if !mockQ.testsForPageCalled {
		t.Error("expected TestsForPage to be called")
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
	if !mockQ.inheritorsOfPageCalled {
		t.Error("expected InheritorsOfPage to be called")
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
		// target not found returns an empty result rather than an error.
		text := getTextContent(result)
		if !strings.Contains(text, "not found") {
			t.Errorf("expected not found message, got: %s", text)
		}
	}
}

func TestQueryGraph_TargetFallbackAutoSelectsSingleShortNameMatch(t *testing.T) {
	deps := setupTestDeps(t)
	ctx := context.Background()
	mockQ := &mockQueryService{
		result:      []model.Node{{QualifiedName: "pkg.Caller", Kind: model.NodeKindFunction, Name: "Caller", FilePath: "caller.go"}},
		matchResult: []query.CandidateMatch{{QualifiedName: "pkg.runPostprocess", Kind: model.NodeKindFunction, FilePath: "query.go", StartLine: 10}},
	}
	deps.QueryService = mockQ
	deps.Store.UpsertNodes(ctx, []model.Node{{QualifiedName: "pkg.runPostprocess", Kind: model.NodeKindFunction, Name: "runPostprocess", FilePath: "query.go", StartLine: 10, EndLine: 20, Language: "go"}})

	result := callTool(t, deps, "query_graph", map[string]any{"pattern": "callers_of", "target": "runPostprocess"})
	if result.IsError {
		t.Fatalf("query_graph error: %s", getTextContent(result))
	}
	if !mockQ.findMatchesCalled {
		t.Fatal("expected FindExactNameMatches to be called for short-name fallback")
	}
	if !mockQ.callersPageCalled {
		t.Fatal("expected CallersOf to be called for short-name fallback")
	}
}

func TestQueryGraph_TargetFallbackReturnsAmbiguousCandidates(t *testing.T) {
	deps := setupTestDeps(t)
	deps.QueryService = &mockQueryService{result: []model.Node{}, matchResult: []query.CandidateMatch{
		{QualifiedName: "pkg.runPostprocess", Kind: model.NodeKindFunction, FilePath: "a.go", StartLine: 10},
		{QualifiedName: "other.runPostprocess", Kind: model.NodeKindFunction, FilePath: "b.go", StartLine: 5},
	}}

	result := callTool(t, deps, "query_graph", map[string]any{"pattern": "callers_of", "target": "runPostprocess"})
	if !result.IsError {
		t.Fatal("expected ambiguity error")
	}
	text := getTextContent(result)
	if !strings.Contains(text, "ambiguous") || !strings.Contains(text, "pkg.runPostprocess") || !strings.Contains(text, "other.runPostprocess") {
		t.Fatalf("expected compact ambiguity candidates, got: %s", text)
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
	if !mockLF.findPageCalled {
		t.Error("expected FindPage to be called")
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
	deps.NamespaceRoot = t.TempDir()
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

	// Create a flow.
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

func TestGetAffectedFlows_RespectsNamespace(t *testing.T) {
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
	result := callTool(t, deps, "get_affected_flows", map[string]any{"repo_root": repoRoot, "namespace": "alpha"})
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

func TestListFlows_RespectsNamespace(t *testing.T) {
	deps := setupTestDeps(t)
	deps.DB.Create(&model.Flow{Namespace: "alpha", Name: "alpha-flow"})
	deps.DB.Create(&model.Flow{Namespace: "beta", Name: "beta-flow"})

	result := callTool(t, deps, "list_flows", map[string]any{"namespace": "alpha"})
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

func TestListCommunities_NamespaceScopesResults(t *testing.T) {
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

	result := callTool(t, deps, "list_communities", map[string]any{"namespace": "alpha"})
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
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatal(err)
	}
	if resp["label"] != "core" {
		t.Errorf("expected label=core, got %v", resp["label"])
	}
	if resp["node_count"].(float64) != 1 {
		t.Errorf("expected node_count=1, got %v", resp["node_count"])
	}
	if _, ok := resp["members"]; ok {
		t.Fatalf("expected members to be omitted, got %v", resp["members"])
	}
	if _, ok := resp["members_pagination"]; ok {
		t.Fatalf("expected members_pagination to be omitted, got %v", resp["members_pagination"])
	}
	if _, ok := resp["coverage"]; ok {
		t.Fatalf("expected coverage to be omitted, got %v", resp["coverage"])
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
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatal(err)
	}
	members, ok := resp["members"].([]any)
	if !ok {
		t.Fatalf("expected members array, got %T", resp["members"])
	}
	if len(members) != 1 {
		t.Errorf("expected 1 member, got %d", len(members))
	}
	if _, ok := resp["members_pagination"]; !ok {
		t.Fatal("expected members_pagination to be present")
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
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatal(err)
	}
	coverage, ok := resp["coverage"].(float64)
	if !ok {
		t.Fatalf("expected coverage float, got %T", resp["coverage"])
	}
	if coverage != 0.7 {
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

func TestGetCommunity_NamespaceRejectsForeignCommunity(t *testing.T) {
	deps := setupTestDeps(t)
	community := model.Community{Namespace: "beta", Key: "beta/core", Label: "beta/core", Strategy: "directory"}
	deps.DB.Create(&community)

	result := callTool(t, deps, "get_community", map[string]any{"community_id": community.ID, "namespace": "alpha"})
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

	if !mockDC.findPageCalled {
		t.Error("expected FindPage to be called")
	}
}

func TestFindDeadCode_FilterByFilePattern(t *testing.T) {
	deps := setupTestDeps(t)

	mockDC := &mockDeadcodeAnalyzer{result: []model.Node{}}
	deps.DeadcodeAnalyzer = mockDC

	callTool(t, deps, "find_dead_code", map[string]any{
		"path": "internal/",
	})

	if !mockDC.findPageCalled {
		t.Error("expected FindPage to be called")
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

func TestFindSuspectFallbackEdges_ReturnsSuspects(t *testing.T) {
	deps := setupTestDeps(t)
	deps.FallbackAnalyzer = &mockFallbackAnalyzer{
		result: []fallbackanalysis.SuspectEdge{{
			Edge:    model.Edge{Kind: model.EdgeKindFallbackCalls, Fingerprint: "auth-invoice-fallback"},
			Source:  model.Node{QualifiedName: "pkg.Authenticate", FilePath: "auth.go"},
			Target:  model.Node{QualifiedName: "pkg.RenderInvoice", FilePath: "invoice.go"},
			Suspect: true,
		}},
	}

	result := callTool(t, deps, "find_suspect_fallback_edges", map[string]any{})
	if result.IsError {
		t.Fatalf("find_suspect_fallback_edges error: %s", getTextContent(result))
	}

	var resp map[string]any
	if err := json.Unmarshal([]byte(getTextContent(result)), &resp); err != nil {
		t.Fatalf("expected JSON response, got: %s", getTextContent(result))
	}
	if resp["count"].(float64) != 1 {
		t.Fatalf("expected 1 suspect fallback edge, got %v", resp["count"])
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
		"namespace":    "ns-a",
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
	err          error
	rebuildCalls int
	queryLimit   int
}

func (f *failSearchBackend) Rebuild(ctx context.Context, db *gorm.DB) error {
	f.rebuildCalls++
	return f.err
}

func (f *failSearchBackend) RebuildNodes(ctx context.Context, db *gorm.DB, nodeIDs []uint) error {
	return f.err
}

func (f *failSearchBackend) PurgeNamespace(ctx context.Context, db *gorm.DB) error {
	return f.err
}

func (f *failSearchBackend) Migrate(db *gorm.DB) error { return nil }

func (f *failSearchBackend) Query(ctx context.Context, db *gorm.DB, query string, limit int) ([]model.Node, error) {
	f.queryLimit = limit
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
	deps.FlowBuilder = nil

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

func TestRunPostprocess_SkipsSearchStepsWhenDBMissing(t *testing.T) {
	deps := setupTestDeps(t)
	backend := &failSearchBackend{}
	deps.SearchBackend = backend
	deps.DB = nil

	result := callTool(t, deps, "run_postprocess", map[string]any{
		"communities": false,
		"fts":         true,
		"flows":       false,
	})
	if result.IsError {
		t.Fatalf("run_postprocess should not return tool error, got: %s", getTextContent(result))
	}

	var resp map[string]any
	if err := json.Unmarshal([]byte(getTextContent(result)), &resp); err != nil {
		t.Fatalf("expected JSON, got: %s", getTextContent(result))
	}
	skipped, ok := resp["skipped_steps"].([]any)
	if !ok {
		t.Fatalf("expected skipped_steps array, got %v", resp["skipped_steps"])
	}
	if !containsString(skipped, "search_documents") || !containsString(skipped, "fts") {
		t.Fatalf("expected search steps to be skipped when DB is nil, got %v", skipped)
	}
	if backend.rebuildCalls != 0 {
		t.Fatalf("expected no rebuild when DB is nil, got %d calls", backend.rebuildCalls)
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

	var resp map[string]any
	if err := json.Unmarshal([]byte(getTextContent(result)), &resp); err != nil {
		t.Fatalf("expected JSON, got: %s", getTextContent(result))
	}
	skipped, ok := resp["skipped_steps"].([]any)
	if !ok {
		t.Fatalf("expected skipped_steps array, got %v", resp["skipped_steps"])
	}
	if containsString(skipped, "flows") {
		t.Fatalf("expected build_or_update_graph postprocess=full not to report flows as skipped when builder is configured, got %v", resp["skipped_steps"])
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

func TestBuildOrUpdateGraph_PostprocessNone_DoesNotRefreshSearchDocuments(t *testing.T) {
	deps := setupTestDeps(t)

	dir := t.TempDir()
	writeGoFile(t, dir, "svc.go", `package svc
func NotIndexedYet() {}
`)

	result := callTool(t, deps, "build_or_update_graph", map[string]any{
		"path":         dir,
		"full_rebuild": true,
		"postprocess":  "none",
	})
	if result.IsError {
		t.Fatalf("build_or_update_graph returned error: %s", getTextContent(result))
	}

	var count int64
	if err := deps.DB.Model(&model.SearchDocument{}).Count(&count).Error; err != nil {
		t.Fatalf("failed to count search_documents: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected postprocess=none not to refresh search_documents, got %d", count)
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
