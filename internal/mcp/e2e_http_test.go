package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/imtaebin/code-context-graph/internal/model"
)

func newHTTPTestClient(t *testing.T, srv *server.MCPServer) (*client.Client, func()) {
	t.Helper()

	testSrv := server.NewTestStreamableHTTPServer(srv)

	httpClient, err := client.NewStreamableHttpClient(testSrv.URL + "/mcp")
	if err != nil {
		testSrv.Close()
		t.Fatalf("create HTTP client: %v", err)
	}

	ctx := context.Background()
	if err := httpClient.Start(ctx); err != nil {
		httpClient.Close()
		testSrv.Close()
		t.Fatalf("start HTTP client: %v", err)
	}

	initReq := mcp.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{Name: "e2e-test", Version: "1.0.0"}

	if _, err := httpClient.Initialize(ctx, initReq); err != nil {
		httpClient.Close()
		testSrv.Close()
		t.Fatalf("initialize: %v", err)
	}

	cleanup := func() {
		httpClient.Close()
		testSrv.Close()
	}
	return httpClient, cleanup
}

func TestE2EHTTP_ListTools(t *testing.T) {
	deps := setupE2EDeps(t)
	srv := NewServer(deps)

	httpClient, cleanup := newHTTPTestClient(t, srv)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := httpClient.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}

	if len(result.Tools) == 0 {
		t.Fatal("expected at least 1 tool, got 0")
	}

	toolNames := make(map[string]bool)
	for _, tool := range result.Tools {
		toolNames[tool.Name] = true
	}

	required := []string{"parse_project", "get_node", "search", "list_graph_stats", "find_dead_code"}
	for _, name := range required {
		if !toolNames[name] {
			t.Errorf("missing tool: %s", name)
		}
	}
}

func TestE2EHTTP_ParseAndGetNode(t *testing.T) {
	deps := setupE2EDeps(t)
	srv := NewServer(deps)

	httpClient, cleanup := newHTTPTestClient(t, srv)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	dir := t.TempDir()
	writeGoFile(t, dir, "svc.go", `package svc

func Start() {}
func Stop() {}
`)

	parseReq := mcp.CallToolRequest{}
	parseReq.Params.Name = "parse_project"
	parseReq.Params.Arguments = map[string]any{"path": dir}

	parseResult, err := httpClient.CallTool(ctx, parseReq)
	if err != nil {
		t.Fatalf("CallTool parse_project: %v", err)
	}
	if parseResult.IsError {
		t.Fatalf("parse_project error: %v", parseResult.Content)
	}

	nodeReq := mcp.CallToolRequest{}
	nodeReq.Params.Name = "get_node"
	nodeReq.Params.Arguments = map[string]any{"qualified_name": "svc.Start"}

	nodeResult, err := httpClient.CallTool(ctx, nodeReq)
	if err != nil {
		t.Fatalf("CallTool get_node: %v", err)
	}
	if nodeResult.IsError {
		t.Fatalf("get_node error: %v", nodeResult.Content)
	}

	text := extractText(nodeResult)
	var node map[string]any
	if err := json.Unmarshal([]byte(text), &node); err != nil {
		t.Fatalf("parse node JSON: %v — raw: %s", err, text)
	}
	if node["name"] != "Start" {
		t.Errorf("expected name=Start, got %v", node["name"])
	}
}

func TestE2EHTTP_FullTextSearch(t *testing.T) {
	deps := setupE2EDeps(t)
	srv := NewServer(deps)

	httpClient, cleanup := newHTTPTestClient(t, srv)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	dir := t.TempDir()
	writeGoFile(t, dir, "repo.go", `package repo

func FindUserByEmail(email string) error { return nil }
func FindUserByID(id int) error { return nil }
func DeleteExpiredSessions() error { return nil }
`)

	parseReq := mcp.CallToolRequest{}
	parseReq.Params.Name = "parse_project"
	parseReq.Params.Arguments = map[string]any{"path": dir}

	if _, err := httpClient.CallTool(ctx, parseReq); err != nil {
		t.Fatalf("parse_project: %v", err)
	}

	nodesInFile, err := deps.Store.GetNodesByFile(ctx, filepath.Join(dir, "repo.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range nodesInFile {
		if n.Kind == "function" {
			deps.DB.Create(&model.SearchDocument{
				NodeID:   n.ID,
				Content:  n.Name + " " + n.QualifiedName,
				Language: n.Language,
			})
		}
	}
	deps.SearchBackend.Rebuild(ctx, deps.DB)

	searchReq := mcp.CallToolRequest{}
	searchReq.Params.Name = "search"
	searchReq.Params.Arguments = map[string]any{"query": "FindUser", "limit": 10}

	searchResult, err := httpClient.CallTool(ctx, searchReq)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if searchResult.IsError {
		t.Fatalf("search error: %v", searchResult.Content)
	}

	text := extractText(searchResult)
	var results []map[string]any
	if err := json.Unmarshal([]byte(text), &results); err != nil {
		t.Fatalf("parse search JSON: %v — raw: %s", err, text)
	}
	if len(results) < 2 {
		t.Errorf("expected >=2 results for 'FindUser', got %d", len(results))
	}
}

func TestE2EHTTP_ImpactRadius(t *testing.T) {
	deps := setupE2EDeps(t)
	srv := NewServer(deps)

	httpClient, cleanup := newHTTPTestClient(t, srv)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	dir := t.TempDir()
	writeGoFile(t, dir, "chain.go", `package chain

func A() { B() }
func B() { C() }
func C() {}
`)

	parseReq := mcp.CallToolRequest{}
	parseReq.Params.Name = "parse_project"
	parseReq.Params.Arguments = map[string]any{"path": dir}

	if _, err := httpClient.CallTool(ctx, parseReq); err != nil {
		t.Fatalf("parse_project: %v", err)
	}

	nodeA, _ := deps.Store.GetNode(ctx, "chain.A")
	nodeB, _ := deps.Store.GetNode(ctx, "chain.B")
	nodeC, _ := deps.Store.GetNode(ctx, "chain.C")
	if nodeA == nil || nodeB == nil || nodeC == nil {
		t.Fatal("expected all 3 nodes")
	}

	deps.Store.UpsertEdges(ctx, []model.Edge{
		{FromNodeID: nodeA.ID, ToNodeID: nodeB.ID, Kind: model.EdgeKindCalls, Fingerprint: "a-b"},
		{FromNodeID: nodeB.ID, ToNodeID: nodeC.ID, Kind: model.EdgeKindCalls, Fingerprint: "b-c"},
	})

	irReq := mcp.CallToolRequest{}
	irReq.Params.Name = "get_impact_radius"
	irReq.Params.Arguments = map[string]any{"qualified_name": "chain.A", "depth": 2}

	irResult, err := httpClient.CallTool(ctx, irReq)
	if err != nil {
		t.Fatalf("get_impact_radius: %v", err)
	}
	if irResult.IsError {
		t.Fatalf("impact_radius error: %v", irResult.Content)
	}

	text := extractText(irResult)
	var nodes []map[string]any
	if err := json.Unmarshal([]byte(text), &nodes); err != nil {
		t.Fatalf("parse impact JSON: %v — raw: %s", err, text)
	}
	if len(nodes) < 3 {
		t.Errorf("expected >=3 nodes in impact radius depth 2, got %d", len(nodes))
	}
}

func TestE2EHTTP_ConcurrentClients(t *testing.T) {
	deps := setupE2EDeps(t)
	srv := NewServer(deps)

	testSrv := server.NewTestStreamableHTTPServer(srv)
	defer testSrv.Close()

	dir := t.TempDir()
	writeGoFile(t, dir, "conc.go", `package conc

func Alpha() {}
func Beta() {}
`)

	directParseResult := callTool(t, deps, "parse_project", map[string]any{"path": dir})
	if directParseResult.IsError {
		t.Fatalf("parse_project: %s", getTextContent(directParseResult))
	}

	const numClients = 5
	errCh := make(chan error, numClients)

	for i := 0; i < numClients; i++ {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			c, err := client.NewStreamableHttpClient(testSrv.URL + "/mcp")
			if err != nil {
				errCh <- err
				return
			}
			defer c.Close()

			if err := c.Start(ctx); err != nil {
				errCh <- err
				return
			}

			initReq := mcp.InitializeRequest{}
			initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
			initReq.Params.ClientInfo = mcp.Implementation{Name: "conc-test", Version: "1.0.0"}

			if _, err := c.Initialize(ctx, initReq); err != nil {
				errCh <- err
				return
			}

			nodeReq := mcp.CallToolRequest{}
			nodeReq.Params.Name = "get_node"
			nodeReq.Params.Arguments = map[string]any{"qualified_name": "conc.Alpha"}

			result, err := c.CallTool(ctx, nodeReq)
			if err != nil {
				errCh <- err
				return
			}
			if result.IsError {
				errCh <- fmt.Errorf("get_node returned error")
				return
			}
			errCh <- nil
		}()
	}

	for i := 0; i < numClients; i++ {
		if err := <-errCh; err != nil {
			t.Errorf("client %d: %v", i, err)
		}
	}
}

func TestE2EHTTP_GraphStats(t *testing.T) {
	deps := setupE2EDeps(t)
	srv := NewServer(deps)

	httpClient, cleanup := newHTTPTestClient(t, srv)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	dir := t.TempDir()
	writeGoFile(t, dir, "multi.go", `package multi

func One() {}
func Two() {}
func Three() {}
`)

	parseReq := mcp.CallToolRequest{}
	parseReq.Params.Name = "parse_project"
	parseReq.Params.Arguments = map[string]any{"path": dir}

	if _, err := httpClient.CallTool(ctx, parseReq); err != nil {
		t.Fatalf("parse_project: %v", err)
	}

	statsReq := mcp.CallToolRequest{}
	statsReq.Params.Name = "list_graph_stats"
	statsReq.Params.Arguments = map[string]any{}

	statsResult, err := httpClient.CallTool(ctx, statsReq)
	if err != nil {
		t.Fatalf("list_graph_stats: %v", err)
	}
	if statsResult.IsError {
		t.Fatalf("list_graph_stats error: %v", statsResult.Content)
	}

	text := extractText(statsResult)
	var resp map[string]any
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("parse stats JSON: %v — raw: %s", err, text)
	}
	if resp["total_nodes"].(float64) < 3 {
		t.Errorf("expected >=3 nodes, got %v", resp["total_nodes"])
	}
}

func extractText(result *mcp.CallToolResult) string {
	if len(result.Content) == 0 {
		return ""
	}
	tc, ok := result.Content[0].(mcp.TextContent)
	if !ok {
		return ""
	}
	return tc.Text
}
