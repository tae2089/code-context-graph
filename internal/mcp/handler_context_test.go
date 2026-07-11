package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/tae2089/code-context-graph/internal/analysis/changes"
	"github.com/tae2089/code-context-graph/internal/ctxns"
	"github.com/tae2089/code-context-graph/internal/model"
)

func TestGetMinimalContext_EmptyGraph(t *testing.T) {
	deps := setupTestDeps(t)

	result := callTool(t, deps, "get_minimal_context", map[string]any{})
	if result.IsError {
		t.Fatalf("get_minimal_context returned error: %s", getTextContent(result))
	}

	text := getTextContent(result)
	if text == "" {
		t.Fatal("expected non-empty result text")
	}

	var data map[string]any
	if err := json.Unmarshal([]byte(text), &data); err != nil {
		t.Fatalf("invalid JSON response: %v", err)
	}

	summary, ok := data["summary"].(string)
	if !ok {
		t.Fatal("missing summary field")
	}
	if summary != "0 nodes, 0 edges, 0 files" {
		t.Errorf("summary = %q, want %q", summary, "0 nodes, 0 edges, 0 files")
	}

	risk, ok := data["risk"].(string)
	if !ok {
		t.Fatal("missing risk field")
	}
	if risk != "unknown" {
		t.Errorf("risk = %q, want %q", risk, "unknown")
	}

	riskScore, ok := data["risk_score"].(float64)
	if !ok {
		t.Fatal("missing risk_score field")
	}
	if riskScore != 0 {
		t.Errorf("risk_score = %v, want 0", riskScore)
	}

	entities, ok := data["key_entities"].([]any)
	if !ok {
		t.Fatal("missing key_entities field")
	}
	if len(entities) != 0 {
		t.Errorf("key_entities length = %d, want 0", len(entities))
	}

	testGaps, ok := data["test_gaps"].(float64)
	if !ok {
		t.Fatal("missing test_gaps field")
	}
	if testGaps != 0 {
		t.Errorf("test_gaps = %v, want 0", testGaps)
	}

	communities, ok := data["top_communities"].([]any)
	if !ok {
		t.Fatal("missing top_communities field")
	}
	if len(communities) != 0 {
		t.Errorf("top_communities length = %d, want 0", len(communities))
	}

	flows, ok := data["top_flows"].([]any)
	if !ok {
		t.Fatal("missing top_flows field")
	}
	if len(flows) != 0 {
		t.Errorf("top_flows length = %d, want 0", len(flows))
	}

	tools, ok := data["suggested_tools"].([]any)
	if !ok {
		t.Fatal("missing suggested_tools field")
	}
	if len(tools) == 0 {
		t.Error("suggested_tools should not be empty")
	}
}

func TestGetMinimalContext_WithNodesAndEdges(t *testing.T) {
	deps := setupTestDeps(t)

	deps.DB.Create(&model.Node{QualifiedName: "pkg.FuncA", Kind: model.NodeKindFunction, Name: "FuncA", FilePath: "a.go", StartLine: 1, EndLine: 5, Language: "go"})
	deps.DB.Create(&model.Node{QualifiedName: "pkg.FuncB", Kind: model.NodeKindFunction, Name: "FuncB", FilePath: "b.go", StartLine: 1, EndLine: 10, Language: "go"})
	deps.DB.Create(&model.Node{QualifiedName: "pkg.FuncC", Kind: model.NodeKindFunction, Name: "FuncC", FilePath: "a.go", StartLine: 7, EndLine: 15, Language: "go"})

	deps.DB.Create(&model.Edge{FromNodeID: 1, ToNodeID: 2, Kind: model.EdgeKindCalls, Fingerprint: "e1"})
	deps.DB.Create(&model.Edge{FromNodeID: 2, ToNodeID: 3, Kind: model.EdgeKindCalls, Fingerprint: "e2"})

	result := callTool(t, deps, "get_minimal_context", map[string]any{})
	if result.IsError {
		t.Fatalf("get_minimal_context returned error: %s", getTextContent(result))
	}

	var data map[string]any
	if err := json.Unmarshal([]byte(getTextContent(result)), &data); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	// 3 nodes, 2 edges, 2 distinct files (a.go, b.go)
	want := "3 nodes, 2 edges, 2 files"
	if got := data["summary"].(string); got != want {
		t.Errorf("summary = %q, want %q", got, want)
	}
}

func TestGetMinimalContext_CommunitiesAndFlows(t *testing.T) {
	deps := setupTestDeps(t)

	var nodeIDs []uint
	for i := 1; i <= 20; i++ {
		n := model.Node{
			QualifiedName: fmt.Sprintf("pkg.Func%d", i),
			Kind:          model.NodeKindFunction,
			Name:          fmt.Sprintf("Func%d", i),
			FilePath:      "x.go",
			StartLine:     i * 10,
			EndLine:       i*10 + 5,
			Language:      "go",
		}
		deps.DB.Create(&n)
		nodeIDs = append(nodeIDs, n.ID)
	}

	// 4 communities: sizes 10, 5, 3, 1 → top 3 should be 10, 5, 3
	nodeIdx := 0
	for _, c := range []struct {
		label string
		size  int
	}{
		{"internal/auth", 10},
		{"internal/api", 5},
		{"internal/db", 3},
		{"internal/util", 1},
	} {
		comm := model.Community{Key: c.label, Label: c.label, Strategy: "directory"}
		deps.DB.Create(&comm)
		for j := 0; j < c.size; j++ {
			deps.DB.Create(&model.CommunityMembership{CommunityID: comm.ID, NodeID: nodeIDs[nodeIdx%len(nodeIDs)]})
			nodeIdx++
		}
	}

	// 4 flows: sizes 8, 6, 4, 2 → top 3 should be 8, 6, 4
	for _, f := range []struct {
		name string
		size int
	}{
		{"login_flow", 8},
		{"checkout_flow", 6},
		{"signup_flow", 4},
		{"logout_flow", 2},
	} {
		flow := model.Flow{Name: f.name}
		deps.DB.Create(&flow)
		for j := 0; j < f.size; j++ {
			deps.DB.Create(&model.FlowMembership{FlowID: flow.ID, NodeID: nodeIDs[j%len(nodeIDs)], Ordinal: j})
		}
	}

	result := callTool(t, deps, "get_minimal_context", map[string]any{})
	if result.IsError {
		t.Fatalf("returned error: %s", getTextContent(result))
	}

	var data map[string]any
	if err := json.Unmarshal([]byte(getTextContent(result)), &data); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	comms, ok := data["top_communities"].([]any)
	if !ok {
		t.Fatal("missing top_communities")
	}
	if len(comms) != 3 {
		t.Fatalf("top_communities count = %d, want 3", len(comms))
	}
	firstComm := comms[0].(map[string]any)
	if firstComm["label"] != "internal/auth" {
		t.Errorf("first community label = %v, want internal/auth", firstComm["label"])
	}
	if firstComm["node_count"].(float64) != 10 {
		t.Errorf("first community node_count = %v, want 10", firstComm["node_count"])
	}

	fl, ok := data["top_flows"].([]any)
	if !ok {
		t.Fatal("missing top_flows")
	}
	if len(fl) != 3 {
		t.Fatalf("top_flows count = %d, want 3", len(fl))
	}
	firstFlow := fl[0].(map[string]any)
	if firstFlow["name"] != "login_flow" {
		t.Errorf("first flow name = %v, want login_flow", firstFlow["name"])
	}
	if firstFlow["node_count"].(float64) != 8 {
		t.Errorf("first flow node_count = %v, want 8", firstFlow["node_count"])
	}
	if comms[1].(map[string]any)["label"] != "internal/api" || comms[2].(map[string]any)["label"] != "internal/db" {
		t.Fatalf("unexpected community ordering: %v", comms)
	}
	if fl[1].(map[string]any)["name"] != "checkout_flow" || fl[2].(map[string]any)["name"] != "signup_flow" {
		t.Fatalf("unexpected flow ordering: %v", fl)
	}
}

func TestGetMinimalContext_CommunitiesAndFlows_StableTieBreakByID(t *testing.T) {
	deps := setupTestDeps(t)

	for i := 1; i <= 6; i++ {
		deps.DB.Create(&model.Node{
			QualifiedName: fmt.Sprintf("pkg.Tie%d", i),
			Kind:          model.NodeKindFunction,
			Name:          fmt.Sprintf("Tie%d", i),
			FilePath:      "tie.go",
			StartLine:     i,
			EndLine:       i,
			Language:      "go",
		})
	}

	var communities []model.Community
	for _, label := range []string{"comm-a", "comm-b", "comm-c", "comm-d"} {
		comm := model.Community{Label: label, Key: label, Strategy: "directory"}
		deps.DB.Create(&comm)
		communities = append(communities, comm)
	}
	for i, comm := range communities {
		deps.DB.Create(&model.CommunityMembership{CommunityID: comm.ID, NodeID: uint(i + 1)})
	}

	var flows []model.Flow
	for _, name := range []string{"flow-a", "flow-b", "flow-c", "flow-d"} {
		flow := model.Flow{Name: name}
		deps.DB.Create(&flow)
		flows = append(flows, flow)
	}
	for i, flow := range flows {
		deps.DB.Create(&model.FlowMembership{FlowID: flow.ID, NodeID: uint(i + 1), Ordinal: 0})
	}

	result := callTool(t, deps, "get_minimal_context", map[string]any{})
	if result.IsError {
		t.Fatalf("returned error: %s", getTextContent(result))
	}

	var data map[string]any
	if err := json.Unmarshal([]byte(getTextContent(result)), &data); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	comms := data["top_communities"].([]any)
	if got := []string{comms[0].(map[string]any)["label"].(string), comms[1].(map[string]any)["label"].(string), comms[2].(map[string]any)["label"].(string)}; fmt.Sprint(got) != fmt.Sprint([]string{"comm-a", "comm-b", "comm-c"}) {
		t.Fatalf("unexpected tied community order: %v", got)
	}
	fl := data["top_flows"].([]any)
	if got := []string{fl[0].(map[string]any)["name"].(string), fl[1].(map[string]any)["name"].(string), fl[2].(map[string]any)["name"].(string)}; fmt.Sprint(got) != fmt.Sprint([]string{"flow-a", "flow-b", "flow-c"}) {
		t.Fatalf("unexpected tied flow order: %v", got)
	}
}

func TestGetMinimalContext_RespectsFlowNamespace(t *testing.T) {
	deps := setupTestDeps(t)
	ctx := ctxns.WithNamespace(context.Background(), "alpha")
	if err := deps.Store.UpsertNodes(ctx, []model.Node{{QualifiedName: "pkg.N1", Kind: model.NodeKindFunction, Name: "N1", FilePath: "a.go", StartLine: 1, EndLine: 5, Language: "go"}}); err != nil {
		t.Fatal(err)
	}
	node, _ := deps.Store.GetNode(ctx, "pkg.N1")
	alpha := model.Flow{Namespace: "alpha", Name: "alpha_flow"}
	beta := model.Flow{Namespace: "beta", Name: "beta_flow"}
	deps.DB.Create(&alpha)
	deps.DB.Create(&beta)
	deps.DB.Create(&model.FlowMembership{Namespace: "alpha", FlowID: alpha.ID, NodeID: node.ID, Ordinal: 0})
	deps.DB.Create(&model.FlowMembership{Namespace: "beta", FlowID: beta.ID, NodeID: node.ID, Ordinal: 0})

	result := callTool(t, deps, "get_minimal_context", map[string]any{"namespace": "alpha"})
	if result.IsError {
		t.Fatalf("returned error: %s", getTextContent(result))
	}
	var data map[string]any
	if err := json.Unmarshal([]byte(getTextContent(result)), &data); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	flows := data["top_flows"].([]any)
	if len(flows) != 1 {
		t.Fatalf("top_flows count = %d, want 1", len(flows))
	}
	if flows[0].(map[string]any)["name"] != "alpha_flow" {
		t.Fatalf("top flow name = %v, want alpha_flow", flows[0].(map[string]any)["name"])
	}
}

type contextMockGitClient struct {
	changedFiles []string
	hunks        []changes.Hunk
}

func (m *contextMockGitClient) ChangedFiles(_ context.Context, _, _ string) ([]string, error) {
	return m.changedFiles, nil
}

func (m *contextMockGitClient) DiffHunks(_ context.Context, _, _ string, _ []string) ([]changes.Hunk, error) {
	return m.hunks, nil
}

func TestGetMinimalContext_WithRepoRoot(t *testing.T) {
	deps := setupTestDeps(t)

	deps.DB.Create(&model.Node{QualifiedName: "pkg.Login", Kind: model.NodeKindFunction, Name: "Login", FilePath: "auth.go", StartLine: 1, EndLine: 20, Language: "go"})
	deps.DB.Create(&model.Node{QualifiedName: "pkg.Logout", Kind: model.NodeKindFunction, Name: "Logout", FilePath: "auth.go", StartLine: 22, EndLine: 30, Language: "go"})
	deps.DB.Create(&model.Edge{FromNodeID: 1, ToNodeID: 2, Kind: model.EdgeKindCalls, Fingerprint: "e1"})

	mock := &contextMockGitClient{
		changedFiles: []string{"auth.go"},
		hunks: []changes.Hunk{
			{FilePath: "auth.go", StartLine: 5, EndLine: 15},
		},
	}
	deps.ChangesGitClient = mock
	repoRoot := t.TempDir()
	deps.RepoRoot = repoRoot

	result := callTool(t, deps, "get_minimal_context", map[string]any{
		"repo_root": repoRoot,
		"base":      "HEAD~1",
	})
	if result.IsError {
		t.Fatalf("returned error: %s", getTextContent(result))
	}

	var data map[string]any
	if err := json.Unmarshal([]byte(getTextContent(result)), &data); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	risk, ok := data["risk"].(string)
	if !ok {
		t.Fatal("missing risk field")
	}
	if risk == "unknown" {
		t.Error("risk should not be 'unknown' when repo_root is provided with changes")
	}

	entities, ok := data["key_entities"].([]any)
	if !ok {
		t.Fatal("missing key_entities")
	}
	if len(entities) == 0 {
		t.Error("key_entities should not be empty when changes detected")
	}

	testGaps, ok := data["test_gaps"].(float64)
	if !ok {
		t.Fatal("missing test_gaps")
	}
	if testGaps == 0 {
		t.Error("test_gaps should be > 0 for untested changed nodes")
	}
}

func TestGetMinimalContext_RejectsRepoRootOutsideConfiguredRoot(t *testing.T) {
	deps := setupTestDeps(t)
	deps.ChangesGitClient = &contextMockGitClient{}
	deps.RepoRoot = t.TempDir()

	result := callTool(t, deps, "get_minimal_context", map[string]any{
		"repo_root": t.TempDir(),
	})
	if !result.IsError {
		t.Fatal("expected get_minimal_context to reject repo_root outside configured root")
	}
}
