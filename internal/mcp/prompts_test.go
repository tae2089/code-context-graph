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
	"github.com/mark3labs/mcp-go/server"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/tae2089/code-context-graph/internal/analysis/changes"
	"github.com/tae2089/code-context-graph/internal/model"
	"github.com/tae2089/code-context-graph/internal/store/gormstore"
	"github.com/tae2089/code-context-graph/internal/store/search"
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
	return callPromptWithContext(t, srv, context.Background(), promptName, args)
}

func callPromptWithContext(t *testing.T, srv *server.MCPServer, ctx context.Context, promptName string, args map[string]string) string {
	t.Helper()

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

	resp := srv.HandleMessage(ctx, msg)
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

func countOccurrences(text, needle string) int {
	return strings.Count(text, needle)
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
	repoRoot := t.TempDir()
	deps.RepoRoot = repoRoot

	resultJSON := callPrompt(t, deps, "review_changes", map[string]string{
		"repo_root": repoRoot,
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

func TestReviewChanges_EmptyChanges(t *testing.T) {
	deps, _ := setupPromptTestDeps(t)

	deps.ChangesGitClient = &mockGitClient{
		changedFiles: []string{},
		hunks:        []changes.Hunk{},
	}
	repoRoot := t.TempDir()
	deps.RepoRoot = repoRoot

	resultJSON := callPrompt(t, deps, "review_changes", map[string]string{
		"repo_root": repoRoot,
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
	repoRoot := t.TempDir()
	deps.RepoRoot = repoRoot

	// No base parameter — should default to HEAD~1
	callPrompt(t, deps, "review_changes", map[string]string{
		"repo_root": repoRoot,
	})

	if mock.lastBaseRef != "HEAD~1" {
		t.Errorf("expected default base HEAD~1, got %q", mock.lastBaseRef)
	}
}

func TestReviewChanges_RespectsSmallerLimitArgument(t *testing.T) {
	deps, db := setupPromptTestDeps(t)

	changedFiles := make([]string, 0, 5)
	hunks := make([]changes.Hunk, 0, 5)
	for i := 1; i <= 5; i++ {
		filePath := fmt.Sprintf("small%02d.go", i)
		db.Create(&model.Node{QualifiedName: fmt.Sprintf("pkg.Small%02d", i), Kind: model.NodeKindFunction, Name: fmt.Sprintf("Small%02d", i), FilePath: filePath, StartLine: 1, EndLine: 20, Language: "go"})
		changedFiles = append(changedFiles, filePath)
		hunks = append(hunks, changes.Hunk{FilePath: filePath, StartLine: 1, EndLine: 20})
	}

	deps.ChangesGitClient = &mockGitClient{changedFiles: changedFiles, hunks: hunks}
	repoRoot := t.TempDir()
	deps.RepoRoot = repoRoot

	text := getPromptText(t, callPrompt(t, deps, "review_changes", map[string]string{
		"repo_root": repoRoot,
		"limit":     "3",
	}))

	if got := countOccurrences(text, "— 리스크 점수:"); got != 3 {
		t.Fatalf("expected 3 risk entries, got %d\n%s", got, text)
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

func TestDebugIssue_TruncatesPerNodeCallGraph(t *testing.T) {
	deps, db := setupPromptTestDeps(t)
	ctx := context.Background()

	target := model.Node{QualifiedName: "pkg.Target", Kind: model.NodeKindFunction, Name: "Target", FilePath: "target.go", StartLine: 1, EndLine: 10, Language: "go"}
	db.Create(&target)
	db.Create(&model.SearchDocument{NodeID: target.ID, Content: "Target issue handler", Language: "go"})
	if err := deps.SearchBackend.Rebuild(ctx, deps.DB); err != nil {
		t.Fatal(err)
	}

	queryMock := &mockQueryService{}
	for i := 1; i <= 12; i++ {
		queryMock.result = append(queryMock.result, model.Node{QualifiedName: fmt.Sprintf("pkg.Neighbor%02d", i)})
	}
	deps.QueryService = queryMock

	text := getPromptText(t, callPrompt(t, deps, "debug_issue", map[string]string{
		"description": "Target issue",
		"limit":       "25",
	}))

	if got := countOccurrences(text, "← 호출자:"); got != 10 {
		t.Fatalf("expected 10 callers, got %d\n%s", got, text)
	}
	if got := countOccurrences(text, "→ 호출 대상:"); got != 10 {
		t.Fatalf("expected 10 callees, got %d\n%s", got, text)
	}
	if got := countOccurrences(text, "표시: 10건"); got < 2 {
		t.Fatalf("expected truncation markers for callers and callees, got %d\n%s", got, text)
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

func TestOnboardDeveloper_EmptyProject(t *testing.T) {
	deps, _ := setupPromptTestDeps(t)

	resultJSON := callPrompt(t, deps, "onboard_developer", nil)
	text := getPromptText(t, resultJSON)

	if !strings.Contains(text, "비어있습니다") {
		t.Errorf("expected empty project message, got: %s", text)
	}
}

func TestOnboardDeveloper_TruncatesLanguageSection(t *testing.T) {
	deps, db := setupPromptTestDeps(t)

	for i := 1; i <= 12; i++ {
		lang := fmt.Sprintf("lang%02d", i)
		db.Create(&model.Node{QualifiedName: fmt.Sprintf("pkg.Node%02d", i), Kind: model.NodeKindFunction, Name: fmt.Sprintf("Node%02d", i), FilePath: fmt.Sprintf("node%02d.go", i), StartLine: 1, EndLine: 5, Language: lang})
	}

	text := getPromptText(t, callPrompt(t, deps, "onboard_developer", map[string]string{"limit": "25"}))

	if got := countOccurrences(text, "- lang"); got != 10 {
		t.Fatalf("expected 10 language entries, got %d\n%s", got, text)
	}
	if !strings.Contains(text, "표시: 10건") {
		t.Fatalf("expected truncation marker for language section, got\n%s", text)
	}
}

// ============================================================
// 10.5 pre_merge_check tests
// ============================================================

func TestPreMergeCheck_ReturnsRisk(t *testing.T) {
	deps, db := setupPromptTestDeps(t)

	db.Create(&model.Node{QualifiedName: "pkg.Handler", Kind: model.NodeKindFunction, Name: "Handler", FilePath: "handler.go", StartLine: 1, EndLine: 20, Language: "go"})

	deps.ChangesGitClient = &mockGitClient{
		changedFiles: []string{"handler.go"},
		hunks:        []changes.Hunk{{FilePath: "handler.go", StartLine: 5, EndLine: 15}},
	}
	repoRoot := t.TempDir()
	deps.RepoRoot = repoRoot

	resultJSON := callPrompt(t, deps, "pre_merge_check", map[string]string{
		"repo_root": repoRoot,
		"base":      "HEAD~1",
	})
	text := getPromptText(t, resultJSON)

	if !strings.Contains(text, "리스크") {
		t.Error("expected risk section")
	}
	if !strings.Contains(text, "pkg.Handler") {
		t.Error("expected changed function in risk section")
	}
}

func TestPreMergeCheck_EmptyChanges(t *testing.T) {
	deps, _ := setupPromptTestDeps(t)

	deps.ChangesGitClient = &mockGitClient{
		changedFiles: []string{},
		hunks:        []changes.Hunk{},
	}
	repoRoot := t.TempDir()
	deps.RepoRoot = repoRoot

	resultJSON := callPrompt(t, deps, "pre_merge_check", map[string]string{
		"repo_root": repoRoot,
	})
	text := getPromptText(t, resultJSON)

	if !strings.Contains(text, "변경사항이 없습니다") {
		t.Errorf("expected empty changes message, got: %s", text)
	}
}

func TestPreMergeCheck_TruncatesRiskSection(t *testing.T) {
	deps, db := setupPromptTestDeps(t)

	changedFiles := make([]string, 0, 22)
	hunks := make([]changes.Hunk, 0, 22)
	for i := 1; i <= 22; i++ {
		filePath := fmt.Sprintf("merge%02d.go", i)
		db.Create(&model.Node{QualifiedName: fmt.Sprintf("pkg.Merge%02d", i), Kind: model.NodeKindFunction, Name: fmt.Sprintf("Merge%02d", i), FilePath: filePath, StartLine: 1, EndLine: 60, Language: "go"})
		changedFiles = append(changedFiles, filePath)
		hunks = append(hunks, changes.Hunk{FilePath: filePath, StartLine: 1, EndLine: 60})
	}

	deps.ChangesGitClient = &mockGitClient{changedFiles: changedFiles, hunks: hunks}
	repoRoot := t.TempDir()
	deps.RepoRoot = repoRoot

	text := getPromptText(t, callPrompt(t, deps, "pre_merge_check", map[string]string{
		"repo_root": repoRoot,
		"limit":     "25",
	}))

	if got := countOccurrences(text, "리스크 점수:"); got != 20 {
		t.Fatalf("expected 20 risk entries, got %d\n%s", got, text)
	}
	if !strings.Contains(text, "표시: 20건") {
		t.Fatalf("expected risk truncation marker\n%s", text)
	}
}

func TestReviewChanges_RejectsRepoRootOutsideConfiguredRoot(t *testing.T) {
	deps, _ := setupPromptTestDeps(t)
	deps.ChangesGitClient = &mockGitClient{}
	deps.RepoRoot = t.TempDir()

	srv := NewServer(deps)
	params := map[string]any{
		"name": "review_changes",
		"arguments": map[string]string{
			"repo_root": t.TempDir(),
		},
	}
	msg, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "prompts/get", "params": params})
	resp := srv.HandleMessage(context.Background(), msg)
	if _, ok := resp.(mcp.JSONRPCError); !ok {
		t.Fatalf("expected JSONRPCError, got %T", resp)
	}
}

func TestPreMergeCheck_RejectsRepoRootOutsideConfiguredRoot(t *testing.T) {
	deps, _ := setupPromptTestDeps(t)
	deps.ChangesGitClient = &mockGitClient{}
	deps.RepoRoot = t.TempDir()

	srv := NewServer(deps)
	params := map[string]any{
		"name": "pre_merge_check",
		"arguments": map[string]string{
			"repo_root": t.TempDir(),
		},
	}
	msg, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "prompts/get", "params": params})
	resp := srv.HandleMessage(context.Background(), msg)
	if _, ok := resp.(mcp.JSONRPCError); !ok {
		t.Fatalf("expected JSONRPCError, got %T", resp)
	}
}

func TestReviewChanges_AllowsNamespaceRootWhenRepoRootAlsoConfigured(t *testing.T) {
	deps, _ := setupPromptTestDeps(t)
	deps.ChangesGitClient = &mockGitClient{changedFiles: []string{}}
	deps.RepoRoot = t.TempDir()
	deps.NamespaceRoot = t.TempDir()
	namespaceRoot := t.TempDir()
	deps.NamespaceRoot = namespaceRoot

	resultJSON := callPrompt(t, deps, "review_changes", map[string]string{
		"repo_root": namespaceRoot,
		"base":      "HEAD~1",
	})
	text := getPromptText(t, resultJSON)
	if !strings.Contains(text, "변경사항이 없습니다") {
		t.Fatalf("expected namespace-root repo_root to be accepted, got: %s", text)
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
