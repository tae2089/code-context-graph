package mcp

import (
	"context"
	"encoding/json"
	"testing"

	requestctx "github.com/tae2089/code-context-graph/internal/ctx"
	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

// seedCrossNamespaceDeps stores web.Login -> (cross ref) -> auth.ValidateToken -> auth.RecordAudit.
func seedCrossNamespaceDeps(t *testing.T, deps *Deps) (webLogin, authValidate, authAudit uint) {
	t.Helper()
	st := testGraphStoreFor(deps)
	seed := func(namespace string, node graph.Node) uint {
		t.Helper()
		ctx := requestctx.WithNamespace(context.Background(), namespace)
		if err := st.UpsertNodes(ctx, []graph.Node{node}); err != nil {
			t.Fatalf("seed node: %v", err)
		}
		stored, err := st.GetNode(ctx, node.QualifiedName)
		if err != nil || stored == nil {
			t.Fatalf("load node %s: %v", node.QualifiedName, err)
		}
		return stored.ID
	}
	webLogin = seed("web", graph.Node{QualifiedName: "web.Login", Kind: graph.NodeKindFunction, Name: "Login", FilePath: "internal/web/login.go", StartLine: 5, EndLine: 25})
	authValidate = seed("auth-svc", graph.Node{QualifiedName: "auth.ValidateToken", Kind: graph.NodeKindFunction, Name: "ValidateToken", FilePath: "internal/auth/token.go", StartLine: 10, EndLine: 20})
	authAudit = seed("auth-svc", graph.Node{QualifiedName: "auth.RecordAudit", Kind: graph.NodeKindFunction, Name: "RecordAudit", FilePath: "internal/audit/audit.go", StartLine: 3, EndLine: 9})

	authCtx := requestctx.WithNamespace(context.Background(), "auth-svc")
	if err := st.UpsertEdges(authCtx, []graph.Edge{{
		FromNodeID: authValidate, ToNodeID: authAudit, Kind: graph.EdgeKindCalls,
		FilePath: "internal/auth/token.go", Line: 15, Fingerprint: "calls:internal/auth/token.go:RecordAudit:15",
	}}); err != nil {
		t.Fatalf("seed edge: %v", err)
	}

	resolved := authValidate
	if err := st.ReplaceCrossRefsFrom(context.Background(), "web", []graph.CrossRef{{
		FromNamespace: "web", FromNodeID: webLogin, Raw: "ccg://auth-svc/internal/auth/token.go#ValidateToken",
		ToNamespace: "auth-svc", ToPath: "internal/auth/token.go", ToSymbol: "ValidateToken",
		ResolvedNodeID: &resolved, Status: graph.CrossRefStatusResolved, Source: graph.CrossRefSourceAnnotation,
	}}); err != nil {
		t.Fatalf("seed cross ref: %v", err)
	}
	return webLogin, authValidate, authAudit
}

func TestGetImpactRadius_CrossNamespace(t *testing.T) {
	deps := setupTestDeps(t)
	seedCrossNamespaceDeps(t, deps)

	result := callTool(t, deps, "get_impact_radius", map[string]any{
		"qualified_name":  "web.Login",
		"namespace":       "web",
		"depth":           2,
		"cross_namespace": true,
	})
	var payload struct {
		Nodes []struct {
			QualifiedName string `json:"qualified_name"`
			Namespace     string `json:"namespace"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal([]byte(resultTextOf(t, result)), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	seen := map[string]string{}
	for _, n := range payload.Nodes {
		seen[n.QualifiedName] = n.Namespace
	}
	if seen["auth.ValidateToken"] != "auth-svc" {
		t.Fatalf("impact nodes = %v, want auth.ValidateToken labeled auth-svc", seen)
	}
	if seen["auth.RecordAudit"] != "auth-svc" {
		t.Fatalf("impact nodes = %v, want depth-2 traversal to continue inside auth-svc", seen)
	}
}

func TestGetImpactRadius_CrossNamespaceReverse(t *testing.T) {
	deps := setupTestDeps(t)
	seedCrossNamespaceDeps(t, deps)

	result := callTool(t, deps, "get_impact_radius", map[string]any{
		"qualified_name":  "auth.ValidateToken",
		"namespace":       "auth-svc",
		"depth":           1,
		"cross_namespace": true,
	})
	var payload struct {
		Nodes []struct {
			QualifiedName string `json:"qualified_name"`
			Namespace     string `json:"namespace"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal([]byte(resultTextOf(t, result)), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	found := false
	for _, n := range payload.Nodes {
		if n.QualifiedName == "web.Login" && n.Namespace == "web" {
			found = true
		}
	}
	if !found {
		t.Fatalf("reverse impact = %+v, want web.Login from web namespace", payload.Nodes)
	}
}

func TestGetImpactRadius_DefaultStaysNamespaceScoped(t *testing.T) {
	deps := setupTestDeps(t)
	seedCrossNamespaceDeps(t, deps)

	result := callTool(t, deps, "get_impact_radius", map[string]any{
		"qualified_name": "web.Login",
		"namespace":      "web",
		"depth":          2,
	})
	var payload struct {
		Nodes []struct {
			QualifiedName string `json:"qualified_name"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal([]byte(resultTextOf(t, result)), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, n := range payload.Nodes {
		if n.QualifiedName == "auth.ValidateToken" {
			t.Fatal("default impact crossed namespaces without cross_namespace flag")
		}
	}
}

func TestTraceFlow_CrossNamespace(t *testing.T) {
	deps := setupTestDeps(t)
	_, authValidate, authAudit := seedCrossNamespaceDeps(t, deps)

	result := callTool(t, deps, "trace_flow", map[string]any{
		"qualified_name":  "web.Login",
		"namespace":       "web",
		"cross_namespace": true,
	})
	var payload struct {
		Members []struct {
			NodeID uint `json:"node_id"`
		} `json:"members"`
	}
	if err := json.Unmarshal([]byte(resultTextOf(t, result)), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	got := map[uint]bool{}
	for _, m := range payload.Members {
		got[m.NodeID] = true
	}
	if !got[authValidate] || !got[authAudit] {
		t.Fatalf("flow members = %+v, want continuation into auth-svc nodes %d and %d", payload.Members, authValidate, authAudit)
	}
}

func TestListCrossRefs_Directions(t *testing.T) {
	deps := setupTestDeps(t)
	webLogin, authValidate, _ := seedCrossNamespaceDeps(t, deps)

	outbound := callTool(t, deps, "list_cross_refs", map[string]any{"namespace": "web", "direction": "outbound"})
	var outPayload struct {
		Refs []struct {
			FromNamespace  string `json:"from_namespace"`
			FromNodeID     uint   `json:"from_node_id"`
			ToNamespace    string `json:"to_namespace"`
			ResolvedNodeID *uint  `json:"resolved_node_id"`
			Status         string `json:"status"`
		} `json:"refs"`
	}
	if err := json.Unmarshal([]byte(resultTextOf(t, outbound)), &outPayload); err != nil {
		t.Fatalf("unmarshal outbound: %v", err)
	}
	if len(outPayload.Refs) != 1 {
		t.Fatalf("outbound refs = %d, want 1", len(outPayload.Refs))
	}
	ref := outPayload.Refs[0]
	if ref.FromNodeID != webLogin || ref.ToNamespace != "auth-svc" || ref.Status != "resolved" || ref.ResolvedNodeID == nil || *ref.ResolvedNodeID != authValidate {
		t.Fatalf("outbound ref = %+v, want resolved web -> auth-svc link", ref)
	}

	inbound := callTool(t, deps, "list_cross_refs", map[string]any{"namespace": "auth-svc", "direction": "inbound"})
	var inPayload struct {
		Refs []struct {
			FromNamespace string `json:"from_namespace"`
		} `json:"refs"`
	}
	if err := json.Unmarshal([]byte(resultTextOf(t, inbound)), &inPayload); err != nil {
		t.Fatalf("unmarshal inbound: %v", err)
	}
	if len(inPayload.Refs) != 1 || inPayload.Refs[0].FromNamespace != "web" {
		t.Fatalf("inbound refs = %+v, want one ref from web", inPayload.Refs)
	}
}
