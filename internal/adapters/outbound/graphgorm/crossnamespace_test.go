package graphgorm

import (
	"context"
	"testing"

	requestctx "github.com/tae2089/code-context-graph/internal/ctx"
	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

func seedCrossNamespaceGraph(t *testing.T, s *Store) (webLogin, authValidate, authAudit uint) {
	t.Helper()
	webLogin = seedOneNode(t, s, "web", graph.Node{
		QualifiedName: "web.Login", Kind: graph.NodeKindFunction, Name: "Login",
		FilePath: "internal/web/login.go", StartLine: 5, EndLine: 25,
	})
	authValidate = seedOneNode(t, s, "auth-svc", graph.Node{
		QualifiedName: "auth.ValidateToken", Kind: graph.NodeKindFunction, Name: "ValidateToken",
		FilePath: "internal/auth/token.go", StartLine: 10, EndLine: 20,
	})
	authAudit = seedOneNode(t, s, "auth-svc", graph.Node{
		QualifiedName: "auth.RecordAudit", Kind: graph.NodeKindFunction, Name: "RecordAudit",
		FilePath: "internal/audit/audit.go", StartLine: 3, EndLine: 9,
	})

	authCtx := requestctx.WithNamespace(context.Background(), "auth-svc")
	if err := s.UpsertEdges(authCtx, []graph.Edge{{
		FromNodeID: authValidate, ToNodeID: authAudit, Kind: graph.EdgeKindCalls,
		FilePath: "internal/auth/token.go", Line: 15, Fingerprint: "calls:internal/auth/token.go:RecordAudit:15",
	}}); err != nil {
		t.Fatalf("seed auth edge: %v", err)
	}

	resolved := authValidate
	if err := s.ReplaceCrossRefsFrom(context.Background(), "web", []graph.CrossRef{{
		FromNamespace: "web", FromNodeID: webLogin, Raw: "ccg://auth-svc/internal/auth/token.go#ValidateToken",
		ToNamespace: "auth-svc", ToPath: "internal/auth/token.go", ToSymbol: "ValidateToken",
		ResolvedNodeID: &resolved, Status: graph.CrossRefStatusResolved, Source: graph.CrossRefSourceAnnotation,
	}}); err != nil {
		t.Fatalf("seed cross ref: %v", err)
	}
	return webLogin, authValidate, authAudit
}

func seedOneNode(t *testing.T, s *Store, namespace string, node graph.Node) uint {
	t.Helper()
	ctx := requestctx.WithNamespace(context.Background(), namespace)
	if err := s.UpsertNodes(ctx, []graph.Node{node}); err != nil {
		t.Fatalf("seed node: %v", err)
	}
	stored, err := s.GetNode(ctx, node.QualifiedName)
	if err != nil || stored == nil {
		t.Fatalf("load node %s: %v", node.QualifiedName, err)
	}
	return stored.ID
}

func TestCrossNamespaceReader_MergesCrossRefEdges(t *testing.T) {
	s := setupTestDB(t)
	webLogin, authValidate, _ := seedCrossNamespaceGraph(t, s)
	reader := s.CrossNamespaceReader()
	ctx := requestctx.WithNamespace(context.Background(), "web")

	edges, err := reader.GetEdgesFromNodes(ctx, []uint{webLogin})
	if err != nil {
		t.Fatalf("GetEdgesFromNodes: %v", err)
	}
	found := false
	for _, e := range edges {
		if e.Kind == graph.EdgeKindCrossRef && e.ToNodeID == authValidate {
			found = true
		}
	}
	if !found {
		t.Fatalf("edges from web.Login = %+v, want synthetic cross_ref edge to node %d", edges, authValidate)
	}

	inbound, err := reader.GetEdgesToNodes(ctx, []uint{authValidate})
	if err != nil {
		t.Fatalf("GetEdgesToNodes: %v", err)
	}
	foundInbound := false
	for _, e := range inbound {
		if e.Kind == graph.EdgeKindCrossRef && e.FromNodeID == webLogin {
			foundInbound = true
		}
	}
	if !foundInbound {
		t.Fatalf("edges to auth.ValidateToken = %+v, want reverse cross_ref edge from %d", inbound, webLogin)
	}
}

func TestCrossNamespaceReader_ReadsNodesAcrossNamespaces(t *testing.T) {
	s := setupTestDB(t)
	webLogin, authValidate, _ := seedCrossNamespaceGraph(t, s)
	reader := s.CrossNamespaceReader()
	// Context carries the web namespace, but reads must cross it.
	ctx := requestctx.WithNamespace(context.Background(), "web")

	node, err := reader.GetNodeByID(ctx, authValidate)
	if err != nil || node == nil {
		t.Fatalf("GetNodeByID across namespaces: node=%v err=%v", node, err)
	}
	if node.Namespace != "auth-svc" {
		t.Fatalf("node namespace = %q, want auth-svc", node.Namespace)
	}

	nodes, err := reader.GetNodesByIDs(ctx, []uint{webLogin, authValidate})
	if err != nil {
		t.Fatalf("GetNodesByIDs: %v", err)
	}
	if len(nodes) != 2 {
		t.Fatalf("GetNodesByIDs returned %d nodes, want 2 across namespaces", len(nodes))
	}
}

func TestCrossNamespaceReader_FollowsRegularEdgesInTargetNamespace(t *testing.T) {
	s := setupTestDB(t)
	_, authValidate, authAudit := seedCrossNamespaceGraph(t, s)
	reader := s.CrossNamespaceReader()
	ctx := requestctx.WithNamespace(context.Background(), "web")

	edges, err := reader.GetEdgesFromNodes(ctx, []uint{authValidate})
	if err != nil {
		t.Fatalf("GetEdgesFromNodes: %v", err)
	}
	found := false
	for _, e := range edges {
		if e.Kind == graph.EdgeKindCalls && e.ToNodeID == authAudit {
			found = true
		}
	}
	if !found {
		t.Fatalf("edges = %+v, want auth-svc internal call edge despite web context", edges)
	}
}
