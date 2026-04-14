package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/imtaebin/code-context-graph/internal/analysis/changes"
	"github.com/imtaebin/code-context-graph/internal/model"
	"github.com/imtaebin/code-context-graph/internal/store/gormstore"
	"github.com/imtaebin/code-context-graph/internal/store/search"
)

var promptTestDBSeq atomic.Int64

func setupPromptTestDeps(t *testing.T) (*Deps, *gorm.DB) {
	t.Helper()
	dsn := fmt.Sprintf("file:prompttest%d?mode=memory&cache=shared", promptTestDBSeq.Add(1))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	st := gormstore.New(db)
	if err := st.AutoMigrate(); err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.SearchDocument{}); err != nil {
		t.Fatal(err)
	}
	sb := search.NewSQLiteBackend()
	if err := sb.Migrate(db); err != nil {
		if errors.Is(err, search.ErrFTS5NotAvailable) {
			t.Skip("fts5 module not available, skipping test")
		}
		t.Fatal(err)
	}

	deps := &Deps{
		Store:         st,
		DB:            db,
		SearchBackend: sb,
	}
	return deps, db
}

func callPrompt(t *testing.T, deps *Deps, promptName string, args map[string]string) string {
	t.Helper()
	srv := NewServer(deps)

	params := map[string]any{
		"name": promptName,
	}
	if args != nil {
		params["arguments"] = args
	}

	msg, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "prompts/get",
		"params":  params,
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
	return string(resultJSON)
}

func getPromptText(t *testing.T, resultJSON string) string {
	t.Helper()
	var result map[string]any
	if err := json.Unmarshal([]byte(resultJSON), &result); err != nil {
		t.Fatalf("result not JSON: %s", resultJSON)
	}
	messages, ok := result["messages"].([]any)
	if !ok || len(messages) == 0 {
		t.Fatal("expected at least 1 message")
	}
	msg0 := messages[0].(map[string]any)
	content := msg0["content"].(map[string]any)
	return content["text"].(string)
}

// ============================================================
// 10.0 Infrastructure tests
// ============================================================

func TestNewServer_WithPromptCapabilities(t *testing.T) {
	deps := &Deps{}
	srv := NewServer(deps)

	msg, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "prompts/list",
	})

	resp := srv.HandleMessage(context.Background(), msg)
	rpcResp, ok := resp.(mcp.JSONRPCResponse)
	if !ok {
		t.Fatalf("expected JSONRPCResponse, got %T", resp)
	}

	resultJSON, _ := json.Marshal(rpcResp.Result)
	var result map[string]any
	if err := json.Unmarshal(resultJSON, &result); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}

	promptsList, ok := result["prompts"].([]any)
	if !ok || len(promptsList) == 0 {
		t.Fatal("expected prompts to be registered")
	}

	registered := map[string]bool{}
	for _, p := range promptsList {
		pm := p.(map[string]any)
		registered[pm["name"].(string)] = true
	}

	expectedPrompts := []string{
		"review_changes",
		"architecture_map",
		"debug_issue",
		"onboard_developer",
		"pre_merge_check",
	}
	for _, name := range expectedPrompts {
		if !registered[name] {
			t.Errorf("prompt %q not registered", name)
		}
	}
}

func TestPromptHandlers_DepsNilSafe(t *testing.T) {
	// All analysis deps are nil, but server should still create without panic
	deps := &Deps{}
	srv := NewServer(deps)
	if srv == nil {
		t.Fatal("expected non-nil server even with nil deps")
	}
}

// ============================================================
// 10.1 review_changes tests
// ============================================================

func TestReviewChanges_ReturnsRiskEntries(t *testing.T) {
	deps, db := setupPromptTestDeps(t)
	ctx := context.Background()

	// Create nodes with risk
	db.Create(&model.Node{QualifiedName: "pkg.FuncA", Kind: model.NodeKindFunction, Name: "FuncA", FilePath: "a.go", StartLine: 1, EndLine: 20, Language: "go"})
	db.Create(&model.Node{QualifiedName: "pkg.FuncB", Kind: model.NodeKindFunction, Name: "FuncB", FilePath: "b.go", StartLine: 1, EndLine: 10, Language: "go"})

	// Mock GitClient for changes
	deps.ChangesGitClient = &mockGitClient{
		changedFiles: []string{"a.go", "b.go"},
		hunks: []changes.Hunk{
			{FilePath: "a.go", StartLine: 5, EndLine: 10},
			{FilePath: "b.go", StartLine: 3, EndLine: 7},
		},
	}

	resultJSON := callPrompt(t, deps, "review_changes", map[string]string{
		"repo_root": "/tmp/repo",
		"base":      "HEAD~1",
	})
	text := getPromptText(t, resultJSON)

	if !strings.Contains(text, "FuncA") {
		t.Error("expected FuncA in risk entries")
	}
	if !strings.Contains(text, "FuncB") {
		t.Error("expected FuncB in risk entries")
	}

	_ = ctx
}

func TestReviewChanges_IncludesTestGaps(t *testing.T) {
	deps, db := setupPromptTestDeps(t)

	// Create function without test coverage
	db.Create(&model.Node{QualifiedName: "pkg.Untested", Kind: model.NodeKindFunction, Name: "Untested", FilePath: "untested.go", StartLine: 1, EndLine: 10, Language: "go"})

	deps.ChangesGitClient = &mockGitClient{
		changedFiles: []string{"untested.go"},
		hunks:        []changes.Hunk{{FilePath: "untested.go", StartLine: 1, EndLine: 10}},
	}

	resultJSON := callPrompt(t, deps, "review_changes", map[string]string{
		"repo_root": "/tmp/repo",
		"base":      "HEAD~1",
	})
	text := getPromptText(t, resultJSON)

	if !strings.Contains(text, "테스트") || !strings.Contains(text, "0") {
		t.Error("expected test gap information in response")
	}
}

func TestReviewChanges_EmptyChanges(t *testing.T) {
	deps, _ := setupPromptTestDeps(t)

	deps.ChangesGitClient = &mockGitClient{
		changedFiles: []string{},
		hunks:        []changes.Hunk{},
	}

	resultJSON := callPrompt(t, deps, "review_changes", map[string]string{
		"repo_root": "/tmp/repo",
	})
	text := getPromptText(t, resultJSON)

	if !strings.Contains(text, "변경사항이 없습니다") {
		t.Errorf("expected empty changes message, got: %s", text)
	}
}

func TestReviewChanges_DefaultBase(t *testing.T) {
	deps, db := setupPromptTestDeps(t)

	db.Create(&model.Node{QualifiedName: "pkg.Fn", Kind: model.NodeKindFunction, Name: "Fn", FilePath: "fn.go", StartLine: 1, EndLine: 5, Language: "go"})

	mock := &mockGitClient{
		changedFiles: []string{"fn.go"},
		hunks:        []changes.Hunk{{FilePath: "fn.go", StartLine: 1, EndLine: 5}},
	}
	deps.ChangesGitClient = mock

	// No base parameter — should default to HEAD~1
	callPrompt(t, deps, "review_changes", map[string]string{
		"repo_root": "/tmp/repo",
	})

	if mock.lastBaseRef != "HEAD~1" {
		t.Errorf("expected default base HEAD~1, got %q", mock.lastBaseRef)
	}
}

// ============================================================
// 10.2 architecture_map tests
// ============================================================

func TestArchitectureMap_ReturnsCommunities(t *testing.T) {
	deps, db := setupPromptTestDeps(t)

	db.Create(&model.Community{Key: "internal/api", Label: "internal/api", Strategy: "directory"})
	db.Create(&model.Community{Key: "internal/store", Label: "internal/store", Strategy: "directory"})

	resultJSON := callPrompt(t, deps, "architecture_map", nil)
	text := getPromptText(t, resultJSON)

	if !strings.Contains(text, "internal/api") {
		t.Error("expected internal/api community in output")
	}
	if !strings.Contains(text, "internal/store") {
		t.Error("expected internal/store community in output")
	}
}

func TestArchitectureMap_IncludesCoupling(t *testing.T) {
	deps, db := setupPromptTestDeps(t)

	// Create 2 communities with nodes and cross-community edges
	c1 := model.Community{Key: "mod_a", Label: "mod_a", Strategy: "directory"}
	c2 := model.Community{Key: "mod_b", Label: "mod_b", Strategy: "directory"}
	db.Create(&c1)
	db.Create(&c2)

	n1 := model.Node{QualifiedName: "mod_a.F1", Kind: model.NodeKindFunction, Name: "F1", FilePath: "mod_a/f1.go", StartLine: 1, EndLine: 5, Language: "go"}
	n2 := model.Node{QualifiedName: "mod_b.F2", Kind: model.NodeKindFunction, Name: "F2", FilePath: "mod_b/f2.go", StartLine: 1, EndLine: 5, Language: "go"}
	db.Create(&n1)
	db.Create(&n2)

	db.Create(&model.CommunityMembership{CommunityID: c1.ID, NodeID: n1.ID})
	db.Create(&model.CommunityMembership{CommunityID: c2.ID, NodeID: n2.ID})

	db.Create(&model.Edge{FromNodeID: n1.ID, ToNodeID: n2.ID, Kind: model.EdgeKindCalls, Fingerprint: "calls-f1-f2"})

	resultJSON := callPrompt(t, deps, "architecture_map", nil)
	text := getPromptText(t, resultJSON)

	if !strings.Contains(text, "결합도") || !strings.Contains(text, "mod_a") {
		t.Errorf("expected coupling info, got: %s", text)
	}
}

func TestArchitectureMap_NoCommunities(t *testing.T) {
	deps, _ := setupPromptTestDeps(t)

	resultJSON := callPrompt(t, deps, "architecture_map", nil)
	text := getPromptText(t, resultJSON)

	if !strings.Contains(text, "커뮤니티가 없습니다") {
		t.Errorf("expected no communities message, got: %s", text)
	}
}

// ============================================================
// 10.3 debug_issue tests
// ============================================================

func TestDebugIssue_ReturnsSearchResults(t *testing.T) {
	deps, db := setupPromptTestDeps(t)
	ctx := context.Background()

	db.Create(&model.Node{QualifiedName: "pkg.HandleLogin", Kind: model.NodeKindFunction, Name: "HandleLogin", FilePath: "login.go", StartLine: 1, EndLine: 10, Language: "go"})
	var node model.Node
	db.First(&node, "qualified_name = ?", "pkg.HandleLogin")

	db.Create(&model.SearchDocument{NodeID: node.ID, Content: "HandleLogin login authentication handler", Language: "go"})
	deps.SearchBackend.Rebuild(ctx, deps.DB)

	resultJSON := callPrompt(t, deps, "debug_issue", map[string]string{
		"description": "login authentication fails",
	})
	text := getPromptText(t, resultJSON)

	if !strings.Contains(text, "HandleLogin") {
		t.Error("expected HandleLogin in search results")
	}
}

func TestDebugIssue_IncludesCallGraph(t *testing.T) {
	deps, db := setupPromptTestDeps(t)
	ctx := context.Background()

	n1 := model.Node{QualifiedName: "pkg.Caller", Kind: model.NodeKindFunction, Name: "Caller", FilePath: "a.go", StartLine: 1, EndLine: 5, Language: "go"}
	n2 := model.Node{QualifiedName: "pkg.Target", Kind: model.NodeKindFunction, Name: "Target", FilePath: "b.go", StartLine: 1, EndLine: 5, Language: "go"}
	db.Create(&n1)
	db.Create(&n2)
	db.Create(&model.Edge{FromNodeID: n1.ID, ToNodeID: n2.ID, Kind: model.EdgeKindCalls, Fingerprint: "calls-c-t"})

	db.Create(&model.SearchDocument{NodeID: n2.ID, Content: "Target function handler", Language: "go"})
	deps.SearchBackend.Rebuild(ctx, deps.DB)

	resultJSON := callPrompt(t, deps, "debug_issue", map[string]string{
		"description": "Target handler error",
	})
	text := getPromptText(t, resultJSON)

	if !strings.Contains(text, "호출") {
		t.Error("expected call graph section in output")
	}
}

func TestDebugIssue_NoResults(t *testing.T) {
	deps, _ := setupPromptTestDeps(t)

	resultJSON := callPrompt(t, deps, "debug_issue", map[string]string{
		"description": "nonexistent xyz issue",
	})
	text := getPromptText(t, resultJSON)

	if !strings.Contains(text, "찾을 수 없습니다") {
		t.Errorf("expected no results message, got: %s", text)
	}
}

// ============================================================
// 10.4 onboard_developer tests
// ============================================================

func TestOnboardDeveloper_ReturnsStats(t *testing.T) {
	deps, db := setupPromptTestDeps(t)

	db.Create(&model.Node{QualifiedName: "pkg.A", Kind: model.NodeKindFunction, Name: "A", FilePath: "a.go", StartLine: 1, EndLine: 5, Language: "go"})
	db.Create(&model.Node{QualifiedName: "pkg.B", Kind: model.NodeKindFunction, Name: "B", FilePath: "b.go", StartLine: 1, EndLine: 5, Language: "python"})
	db.Create(&model.Edge{FromNodeID: 1, ToNodeID: 2, Kind: model.EdgeKindCalls, Fingerprint: "e1"})

	resultJSON := callPrompt(t, deps, "onboard_developer", nil)
	text := getPromptText(t, resultJSON)

	if !strings.Contains(text, "2") { // 2 nodes
		t.Error("expected node count in stats")
	}
	if !strings.Contains(text, "go") {
		t.Error("expected go language in stats")
	}
}

func TestOnboardDeveloper_IncludesCommunities(t *testing.T) {
	deps, db := setupPromptTestDeps(t)

	c := model.Community{Key: "core", Label: "core", Strategy: "directory"}
	db.Create(&c)
	n := model.Node{QualifiedName: "core.Main", Kind: model.NodeKindFunction, Name: "Main", FilePath: "core/main.go", StartLine: 1, EndLine: 5, Language: "go"}
	db.Create(&n)
	db.Create(&model.CommunityMembership{CommunityID: c.ID, NodeID: n.ID})

	resultJSON := callPrompt(t, deps, "onboard_developer", nil)
	text := getPromptText(t, resultJSON)

	if !strings.Contains(text, "core") {
		t.Error("expected community label in output")
	}
}

func TestOnboardDeveloper_IncludesLargeFunctions(t *testing.T) {
	deps, db := setupPromptTestDeps(t)

	db.Create(&model.Node{QualifiedName: "pkg.BigFunc", Kind: model.NodeKindFunction, Name: "BigFunc", FilePath: "big.go", StartLine: 1, EndLine: 100, Language: "go"})

	resultJSON := callPrompt(t, deps, "onboard_developer", nil)
	text := getPromptText(t, resultJSON)

	if !strings.Contains(text, "BigFunc") {
		t.Error("expected BigFunc in large functions section")
	}
}

func TestOnboardDeveloper_EmptyProject(t *testing.T) {
	deps, _ := setupPromptTestDeps(t)

	resultJSON := callPrompt(t, deps, "onboard_developer", nil)
	text := getPromptText(t, resultJSON)

	if !strings.Contains(text, "비어있습니다") {
		t.Errorf("expected empty project message, got: %s", text)
	}
}

// ============================================================
// 10.5 pre_merge_check tests
// ============================================================

func TestPreMergeCheck_ReturnsRiskAndCoverage(t *testing.T) {
	deps, db := setupPromptTestDeps(t)

	db.Create(&model.Node{QualifiedName: "pkg.Handler", Kind: model.NodeKindFunction, Name: "Handler", FilePath: "handler.go", StartLine: 1, EndLine: 20, Language: "go"})

	deps.ChangesGitClient = &mockGitClient{
		changedFiles: []string{"handler.go"},
		hunks:        []changes.Hunk{{FilePath: "handler.go", StartLine: 5, EndLine: 15}},
	}

	resultJSON := callPrompt(t, deps, "pre_merge_check", map[string]string{
		"repo_root": "/tmp/repo",
		"base":      "HEAD~1",
	})
	text := getPromptText(t, resultJSON)

	if !strings.Contains(text, "리스크") {
		t.Error("expected risk section")
	}
	if !strings.Contains(text, "커버리지") {
		t.Error("expected coverage section")
	}
}

func TestPreMergeCheck_IncludesDeadCode(t *testing.T) {
	deps, db := setupPromptTestDeps(t)

	// Dead code: function with no incoming edges
	db.Create(&model.Node{QualifiedName: "pkg.Unused", Kind: model.NodeKindFunction, Name: "Unused", FilePath: "unused.go", StartLine: 1, EndLine: 5, Language: "go"})

	deps.ChangesGitClient = &mockGitClient{
		changedFiles: []string{},
		hunks:        []changes.Hunk{},
	}

	resultJSON := callPrompt(t, deps, "pre_merge_check", map[string]string{
		"repo_root": "/tmp/repo",
	})
	text := getPromptText(t, resultJSON)

	if !strings.Contains(text, "미사용") || !strings.Contains(text, "Unused") {
		t.Errorf("expected dead code section with Unused, got: %s", text)
	}
}

func TestPreMergeCheck_IncludesLargeFunctions(t *testing.T) {
	deps, db := setupPromptTestDeps(t)

	db.Create(&model.Node{QualifiedName: "pkg.Huge", Kind: model.NodeKindFunction, Name: "Huge", FilePath: "huge.go", StartLine: 1, EndLine: 200, Language: "go"})

	deps.ChangesGitClient = &mockGitClient{
		changedFiles: []string{},
		hunks:        []changes.Hunk{},
	}

	resultJSON := callPrompt(t, deps, "pre_merge_check", map[string]string{
		"repo_root": "/tmp/repo",
	})
	text := getPromptText(t, resultJSON)

	if !strings.Contains(text, "Huge") {
		t.Error("expected Huge in large functions section")
	}
}

func TestPreMergeCheck_EmptyChanges(t *testing.T) {
	deps, _ := setupPromptTestDeps(t)

	deps.ChangesGitClient = &mockGitClient{
		changedFiles: []string{},
		hunks:        []changes.Hunk{},
	}

	resultJSON := callPrompt(t, deps, "pre_merge_check", map[string]string{
		"repo_root": "/tmp/repo",
	})
	text := getPromptText(t, resultJSON)

	if !strings.Contains(text, "변경사항이 없습니다") {
		t.Errorf("expected empty changes message, got: %s", text)
	}
}

// ============================================================
// Mock types
// ============================================================

type mockGitClient struct {
	changedFiles []string
	hunks        []changes.Hunk
	lastBaseRef  string
}

func (m *mockGitClient) ChangedFiles(ctx context.Context, repoDir, baseRef string) ([]string, error) {
	m.lastBaseRef = baseRef
	return m.changedFiles, nil
}

func (m *mockGitClient) DiffHunks(ctx context.Context, repoDir, baseRef string, paths []string) ([]changes.Hunk, error) {
	m.lastBaseRef = baseRef
	return m.hunks, nil
}
