package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/imtaebin/code-context-graph/internal/analysis/community"
	"github.com/imtaebin/code-context-graph/internal/analysis/deadcode"
	"github.com/imtaebin/code-context-graph/internal/analysis/flows"
	"github.com/imtaebin/code-context-graph/internal/analysis/impact"
	"github.com/imtaebin/code-context-graph/internal/analysis/incremental"
	"github.com/imtaebin/code-context-graph/internal/analysis/query"
	"github.com/imtaebin/code-context-graph/internal/model"
	"github.com/imtaebin/code-context-graph/internal/store/gormstore"
	"github.com/imtaebin/code-context-graph/internal/store/search"
)

var e2eTestDBSeq atomic.Int64

func setupE2EDeps(t *testing.T) *Deps {
	t.Helper()
	dsn := fmt.Sprintf("file:e2etest%d?mode=memory&cache=shared", e2eTestDBSeq.Add(1))
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

func writeGoFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	fp := filepath.Join(dir, name)
	if err := os.WriteFile(fp, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return fp
}

func TestE2E_ParseAndQuery(t *testing.T) {
	deps := setupE2EDeps(t)

	dir := t.TempDir()
	writeGoFile(t, dir, "user.go", `package user

func CreateUser(name string) error {
	return nil
}

func DeleteUser(id int) error {
	return nil
}
`)

	// Step 1: Parse project
	parseResult := callTool(t, deps, "parse_project", map[string]any{"path": dir})
	if parseResult.IsError {
		t.Fatalf("parse_project error: %s", getTextContent(parseResult))
	}
	text := getTextContent(parseResult)
	var parsed map[string]any
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		t.Fatalf("parse result not JSON: %s", text)
	}
	if parsed["parsed"].(float64) < 1 {
		t.Fatalf("expected at least 1 parsed file, got: %s", text)
	}

	// Step 2: Query a node
	nodeResult := callTool(t, deps, "get_node", map[string]any{"qualified_name": "user.CreateUser"})
	if nodeResult.IsError {
		t.Fatalf("get_node error: %s", getTextContent(nodeResult))
	}
	nodeText := getTextContent(nodeResult)
	var node map[string]any
	if err := json.Unmarshal([]byte(nodeText), &node); err != nil {
		t.Fatalf("node result not JSON: %s", nodeText)
	}
	if node["name"] != "CreateUser" {
		t.Errorf("expected name=CreateUser, got %v", node["name"])
	}
	if node["kind"] != string(model.NodeKindFunction) {
		t.Errorf("expected kind=function, got %v", node["kind"])
	}
}

func TestE2E_ParseWithAnnotation(t *testing.T) {
	deps := setupE2EDeps(t)
	ctx := context.Background()

	dir := t.TempDir()
	writeGoFile(t, dir, "auth.go", `package auth

// AuthenticateUser validates user credentials.
// Called from login handler before session creation.
//
// @param username the user login ID
// @param password plaintext password
// @return JWT token on success
// @intent verify credentials before session creation
// @domainRule lock account after 5 consecutive failures
func AuthenticateUser(username, password string) (string, error) {
	return "", nil
}
`)

	// Step 1: Parse project
	parseResult := callTool(t, deps, "parse_project", map[string]any{"path": dir})
	if parseResult.IsError {
		t.Fatalf("parse_project error: %s", getTextContent(parseResult))
	}

	// Step 2: Bind annotation (walker doesn't auto-bind; simulating binder/service layer)
	node, err := deps.Store.GetNode(ctx, "auth.AuthenticateUser")
	if err != nil || node == nil {
		t.Fatalf("node auth.AuthenticateUser not found: %v", err)
	}
	deps.Store.UpsertAnnotation(ctx, &model.Annotation{
		NodeID:  node.ID,
		Summary: "AuthenticateUser validates user credentials.",
		Context: "Called from login handler before session creation.",
		Tags: []model.DocTag{
			{Kind: model.TagParam, Name: "username", Value: "the user login ID", Ordinal: 0},
			{Kind: model.TagParam, Name: "password", Value: "plaintext password", Ordinal: 1},
			{Kind: model.TagReturn, Value: "JWT token on success", Ordinal: 0},
			{Kind: model.TagIntent, Value: "verify credentials before session creation", Ordinal: 0},
			{Kind: model.TagDomainRule, Value: "lock account after 5 consecutive failures", Ordinal: 0},
		},
	})

	// Step 3: Get annotation via MCP tool
	annResult := callTool(t, deps, "get_annotation", map[string]any{"qualified_name": "auth.AuthenticateUser"})
	if annResult.IsError {
		t.Fatalf("get_annotation error: %s", getTextContent(annResult))
	}
	annText := getTextContent(annResult)
	var ann map[string]any
	if err := json.Unmarshal([]byte(annText), &ann); err != nil {
		t.Fatalf("annotation result not JSON: %s", annText)
	}
	if ann["summary"] != "AuthenticateUser validates user credentials." {
		t.Errorf("unexpected summary: %v", ann["summary"])
	}
	tags, ok := ann["tags"].([]any)
	if !ok || len(tags) < 5 {
		t.Fatalf("expected at least 5 tags, got %v", ann["tags"])
	}
}

func TestE2E_IncrementalReparse(t *testing.T) {
	deps := setupE2EDeps(t)
	ctx := context.Background()

	syncer := incremental.New(deps.Store, deps.Parser)

	// Step 1: Initial parse
	originalCode := `package calc

func Add(a, b int) int {
	return a + b
}
`
	files := map[string]incremental.FileInfo{
		"calc.go": {Hash: "hash1", Content: []byte(originalCode)},
	}
	stats, err := syncer.Sync(ctx, files)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Added != 1 {
		t.Errorf("expected 1 added, got %d", stats.Added)
	}

	// Verify node exists
	node, err := deps.Store.GetNode(ctx, "calc.Add")
	if err != nil || node == nil {
		t.Fatalf("node calc.Add not found after initial parse: %v", err)
	}

	// Step 2: Modify file — add a new function
	modifiedCode := `package calc

func Add(a, b int) int {
	return a + b
}

func Subtract(a, b int) int {
	return a - b
}
`
	files2 := map[string]incremental.FileInfo{
		"calc.go": {Hash: "hash2", Content: []byte(modifiedCode)},
	}
	stats2, err := syncer.Sync(ctx, files2)
	if err != nil {
		t.Fatal(err)
	}
	if stats2.Modified != 1 {
		t.Errorf("expected 1 modified, got %d", stats2.Modified)
	}

	// Verify new node exists
	newNode, err := deps.Store.GetNode(ctx, "calc.Subtract")
	if err != nil || newNode == nil {
		t.Fatalf("node calc.Subtract not found after reparse: %v", err)
	}
}

func TestE2E_BlastRadius(t *testing.T) {
	deps := setupE2EDeps(t)
	ctx := context.Background()

	dir := t.TempDir()
	writeGoFile(t, dir, "service.go", `package service

func HandleRequest() {
	ValidateInput()
	ProcessData()
}

func ValidateInput() {
}

func ProcessData() {
	SaveToDatabase()
}

func SaveToDatabase() {
}
`)

	parseResult := callTool(t, deps, "parse_project", map[string]any{"path": dir})
	if parseResult.IsError {
		t.Fatalf("parse_project error: %s", getTextContent(parseResult))
	}

	// Walker produces CALLS edges without FromNodeID/ToNodeID (resolved later).
	// Manually wire edges to test blast-radius E2E.
	handleReq, _ := deps.Store.GetNode(ctx, "service.HandleRequest")
	validate, _ := deps.Store.GetNode(ctx, "service.ValidateInput")
	process, _ := deps.Store.GetNode(ctx, "service.ProcessData")
	saveToDB, _ := deps.Store.GetNode(ctx, "service.SaveToDatabase")

	if handleReq == nil || validate == nil || process == nil || saveToDB == nil {
		t.Fatal("expected all 4 function nodes to exist after parsing")
	}

	deps.Store.UpsertEdges(ctx, []model.Edge{
		{FromNodeID: handleReq.ID, ToNodeID: validate.ID, Kind: model.EdgeKindCalls, FilePath: "service.go", Line: 4, Fingerprint: "calls:hr:vi"},
		{FromNodeID: handleReq.ID, ToNodeID: process.ID, Kind: model.EdgeKindCalls, FilePath: "service.go", Line: 5, Fingerprint: "calls:hr:pd"},
		{FromNodeID: process.ID, ToNodeID: saveToDB.ID, Kind: model.EdgeKindCalls, FilePath: "service.go", Line: 12, Fingerprint: "calls:pd:sd"},
	})

	irResult := callTool(t, deps, "get_impact_radius", map[string]any{
		"qualified_name": "service.HandleRequest",
		"depth":          2,
	})
	if irResult.IsError {
		t.Fatalf("get_impact_radius error: %s", getTextContent(irResult))
	}
	irText := getTextContent(irResult)
	var nodes []map[string]any
	if err := json.Unmarshal([]byte(irText), &nodes); err != nil {
		t.Fatalf("impact radius result not JSON array: %s", irText)
	}
	// depth 2: HandleRequest → ValidateInput + ProcessData (depth 1) → SaveToDatabase (depth 2) = 4 nodes
	if len(nodes) < 4 {
		t.Errorf("expected at least 4 nodes in impact radius (depth 2), got %d", len(nodes))
	}
}

func TestE2E_FullTextSearch(t *testing.T) {
	deps := setupE2EDeps(t)
	ctx := context.Background()

	dir := t.TempDir()
	writeGoFile(t, dir, "repository.go", `package repository

func FindUserByEmail(email string) error {
	return nil
}

func FindUserByID(id int) error {
	return nil
}

func DeleteAllExpiredSessions() error {
	return nil
}
`)

	// Step 1: Parse project
	parseResult := callTool(t, deps, "parse_project", map[string]any{"path": dir})
	if parseResult.IsError {
		t.Fatalf("parse_project error: %s", getTextContent(parseResult))
	}

	// Step 2: Create search documents for parsed nodes
	nodesInFile, err := deps.Store.GetNodesByFile(ctx, filepath.Join(dir, "repository.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range nodesInFile {
		if n.Kind == model.NodeKindFunction {
			deps.DB.Create(&model.SearchDocument{
				NodeID:   n.ID,
				Content:  n.Name + " " + n.QualifiedName,
				Language: n.Language,
			})
		}
	}
	deps.SearchBackend.Rebuild(ctx, deps.DB)

	// Step 3: Search via MCP tool
	searchResult := callTool(t, deps, "search", map[string]any{"query": "FindUser", "limit": 10})
	if searchResult.IsError {
		t.Fatalf("search error: %s", getTextContent(searchResult))
	}
	searchText := getTextContent(searchResult)
	var results []map[string]any
	if err := json.Unmarshal([]byte(searchText), &results); err != nil {
		t.Fatalf("search result not JSON array: %s", searchText)
	}
	if len(results) < 2 {
		t.Errorf("expected at least 2 search results for 'FindUser', got %d", len(results))
	}

	// Verify the right functions were found
	names := make(map[string]bool)
	for _, r := range results {
		if name, ok := r["name"].(string); ok {
			names[name] = true
		}
	}
	if !names["FindUserByEmail"] {
		t.Error("expected FindUserByEmail in search results")
	}
	if !names["FindUserByID"] {
		t.Error("expected FindUserByID in search results")
	}
}

// ============================================================
// Phase 11 E2E Tests
// ============================================================

func TestE2E_BuildAndQueryGraph(t *testing.T) {
	deps := setupE2EDeps(t)
	ctx := context.Background()

	dir := t.TempDir()
	writeGoFile(t, dir, "caller.go", `package caller

func Main() {
	Helper()
}

func Helper() {
}
`)

	// Step 1: build_or_update_graph
	buildResult := callTool(t, deps, "build_or_update_graph", map[string]any{
		"path":         dir,
		"full_rebuild": true,
		"postprocess":  "none",
	})
	if buildResult.IsError {
		t.Fatalf("build_or_update_graph error: %s", getTextContent(buildResult))
	}

	// Step 2: Setup edges manually (simple parser doesn't extract calls)
	nodeMain, _ := deps.Store.GetNode(ctx, "caller.Main")
	nodeHelper, _ := deps.Store.GetNode(ctx, "caller.Helper")
	if nodeMain == nil || nodeHelper == nil {
		t.Fatal("expected both nodes to exist after build")
	}
	deps.Store.UpsertEdges(ctx, []model.Edge{
		{FromNodeID: nodeMain.ID, ToNodeID: nodeHelper.ID, Kind: model.EdgeKindCalls, Fingerprint: "calls-main-helper"},
	})

	// Step 3: Setup QueryService and query_graph
	queryService := query.New(deps.DB)
	deps.QueryService = queryService

	qResult := callTool(t, deps, "query_graph", map[string]any{
		"pattern": "callers_of",
		"target":  "caller.Helper",
	})
	if qResult.IsError {
		t.Fatalf("query_graph error: %s", getTextContent(qResult))
	}

	text := getTextContent(qResult)
	var resp map[string]any
	json.Unmarshal([]byte(text), &resp)
	results := resp["results"].([]any)
	if len(results) == 0 {
		t.Error("expected at least 1 caller of Helper")
	}
}

func TestE2E_BuildAndStats(t *testing.T) {
	deps := setupE2EDeps(t)

	dir := t.TempDir()
	writeGoFile(t, dir, "stats.go", `package stats

func Alpha() {}
func Beta() {}
func Gamma() {}
`)

	buildResult := callTool(t, deps, "build_or_update_graph", map[string]any{
		"path":         dir,
		"full_rebuild": true,
		"postprocess":  "none",
	})
	if buildResult.IsError {
		t.Fatalf("build_or_update_graph error: %s", getTextContent(buildResult))
	}

	statsResult := callTool(t, deps, "list_graph_stats", map[string]any{})
	if statsResult.IsError {
		t.Fatalf("list_graph_stats error: %s", getTextContent(statsResult))
	}

	text := getTextContent(statsResult)
	var resp map[string]any
	json.Unmarshal([]byte(text), &resp)
	if resp["total_nodes"].(float64) < 3 {
		t.Errorf("expected at least 3 nodes, got %v", resp["total_nodes"])
	}
}

func TestE2E_BuildAndCommunities(t *testing.T) {
	deps := setupE2EDeps(t)

	commBuilder := community.New(deps.DB)
	deps.CommunityBuilder = commBuilder

	dir := t.TempDir()
	writeGoFile(t, dir, "svc.go", `package svc

func Run() {}
func Stop() {}
`)

	buildResult := callTool(t, deps, "build_or_update_graph", map[string]any{
		"path":         dir,
		"full_rebuild": true,
		"postprocess":  "none",
	})
	if buildResult.IsError {
		t.Fatalf("build_or_update_graph error: %s", getTextContent(buildResult))
	}

	ppResult := callTool(t, deps, "run_postprocess", map[string]any{
		"flows":       false,
		"communities": true,
		"fts":         false,
	})
	if ppResult.IsError {
		t.Fatalf("run_postprocess error: %s", getTextContent(ppResult))
	}

	lcResult := callTool(t, deps, "list_communities", map[string]any{})
	if lcResult.IsError {
		t.Fatalf("list_communities error: %s", getTextContent(lcResult))
	}

	text := getTextContent(lcResult)
	var resp map[string]any
	json.Unmarshal([]byte(text), &resp)
	comms := resp["communities"].([]any)
	if len(comms) == 0 {
		t.Error("expected at least 1 community after rebuild")
	}
}

func TestE2E_BuildAndDeadCode(t *testing.T) {
	deps := setupE2EDeps(t)

	dcAnalyzer := deadcode.New(deps.DB)
	deps.DeadcodeAnalyzer = dcAnalyzer

	dir := t.TempDir()
	writeGoFile(t, dir, "dead.go", `package dead

func UsedFunc() {}
func UnusedFunc() {}
`)

	buildResult := callTool(t, deps, "build_or_update_graph", map[string]any{
		"path":         dir,
		"full_rebuild": true,
		"postprocess":  "none",
	})
	if buildResult.IsError {
		t.Fatalf("build_or_update_graph error: %s", getTextContent(buildResult))
	}

	// Make UsedFunc have an incoming edge
	ctx := context.Background()
	usedNode, _ := deps.Store.GetNode(ctx, "dead.UsedFunc")
	unusedNode, _ := deps.Store.GetNode(ctx, "dead.UnusedFunc")
	if usedNode == nil || unusedNode == nil {
		t.Fatal("expected both nodes to exist")
	}
	deps.Store.UpsertEdges(ctx, []model.Edge{
		{FromNodeID: unusedNode.ID, ToNodeID: usedNode.ID, Kind: model.EdgeKindCalls, Fingerprint: "calls-unused-used"},
	})

	dcResult := callTool(t, deps, "find_dead_code", map[string]any{})
	if dcResult.IsError {
		t.Fatalf("find_dead_code error: %s", getTextContent(dcResult))
	}

	text := getTextContent(dcResult)
	var resp map[string]any
	json.Unmarshal([]byte(text), &resp)
	deadCode := resp["dead_code"].([]any)
	// UnusedFunc has no incoming edge, so it should appear as dead code
	found := false
	for _, dc := range deadCode {
		entry := dc.(map[string]any)
		if entry["name"] == "dead.UnusedFunc" {
			found = true
		}
	}
	if !found {
		t.Error("expected dead.UnusedFunc in dead code results")
	}
}
