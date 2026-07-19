package graphgorm

import (
	"context"
	"testing"

	requestctx "github.com/tae2089/code-context-graph/internal/ctx"
	"github.com/tae2089/code-context-graph/internal/domain/graph"
	"github.com/tae2089/code-context-graph/internal/domain/reference"
)

func seedCrossRefNodes(t *testing.T, s *Store, namespace string, nodes []graph.Node) map[string]uint {
	t.Helper()
	ctx := requestctx.WithNamespace(context.Background(), namespace)
	if err := s.UpsertNodes(ctx, nodes); err != nil {
		t.Fatalf("seed nodes for %s: %v", namespace, err)
	}
	stored, err := s.GetNodesByFiles(ctx, uniqueFilePaths(nodes))
	if err != nil {
		t.Fatalf("load seeded nodes: %v", err)
	}
	ids := map[string]uint{}
	for _, byFile := range stored {
		for _, n := range byFile {
			ids[n.QualifiedName] = n.ID
		}
	}
	return ids
}

func uniqueFilePaths(nodes []graph.Node) []string {
	seen := map[string]bool{}
	paths := []string{}
	for _, n := range nodes {
		if !seen[n.FilePath] {
			seen[n.FilePath] = true
			paths = append(paths, n.FilePath)
		}
	}
	return paths
}

func TestResolveCCGRef_Scopes(t *testing.T) {
	s := setupTestDB(t)
	ids := seedCrossRefNodes(t, s, "auth-svc", []graph.Node{
		{QualifiedName: "internal/auth/token.go", Kind: graph.NodeKindFile, Name: "token.go", FilePath: "internal/auth/token.go", StartLine: 1, EndLine: 1},
		{QualifiedName: "auth.ValidateToken", Kind: graph.NodeKindFunction, Name: "ValidateToken", FilePath: "internal/auth/token.go", StartLine: 10, EndLine: 20},
		{QualifiedName: "auth.RenewToken", Kind: graph.NodeKindFunction, Name: "RenewToken", FilePath: "internal/auth/token.go", StartLine: 30, EndLine: 40},
	})
	ctx := context.Background()

	cases := []struct {
		name   string
		ref    reference.Ref
		wantID uint
		wantOK bool
	}{
		{"path and symbol", reference.Ref{Namespace: "auth-svc", Path: "internal/auth/token.go", Symbol: "ValidateToken"}, ids["auth.ValidateToken"], true},
		{"path only prefers file node", reference.Ref{Namespace: "auth-svc", Path: "internal/auth/token.go"}, ids["internal/auth/token.go"], true},
		{"symbol only", reference.Ref{Namespace: "auth-svc", Symbol: "RenewToken"}, ids["auth.RenewToken"], true},
		{"namespace scope resolves without node", reference.Ref{Namespace: "auth-svc"}, 0, true},
		{"missing symbol", reference.Ref{Namespace: "auth-svc", Symbol: "Nope"}, 0, false},
		{"missing namespace", reference.Ref{Namespace: "ghost"}, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id, ok, err := s.ResolveCCGRef(ctx, tc.ref)
			if err != nil {
				t.Fatalf("ResolveCCGRef: %v", err)
			}
			if ok != tc.wantOK || id != tc.wantID {
				t.Fatalf("ResolveCCGRef = (%d, %v), want (%d, %v)", id, ok, tc.wantID, tc.wantOK)
			}
		})
	}
}

func TestReplaceCrossRefsFrom_ReplacesAndLists(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()
	first := []graph.CrossRef{
		{FromNamespace: "web", FromNodeID: 1, Raw: "ccg://auth-svc/#Old", ToNamespace: "auth-svc", ToSymbol: "Old", Status: graph.CrossRefStatusDead, Source: graph.CrossRefSourceAnnotation},
	}
	if err := s.ReplaceCrossRefsFrom(ctx, "web", first); err != nil {
		t.Fatalf("first replace: %v", err)
	}
	second := []graph.CrossRef{
		{FromNamespace: "web", FromNodeID: 2, Raw: "ccg://auth-svc/#New", ToNamespace: "auth-svc", ToSymbol: "New", Status: graph.CrossRefStatusResolved, Source: graph.CrossRefSourceAnnotation},
		{FromNamespace: "web", FromNodeID: 3, Raw: "ccg://billing/#Charge", ToNamespace: "billing", ToSymbol: "Charge", Status: graph.CrossRefStatusDead, Source: graph.CrossRefSourceAnnotation},
	}
	if err := s.ReplaceCrossRefsFrom(ctx, "web", second); err != nil {
		t.Fatalf("second replace: %v", err)
	}

	inbound, err := s.ListInboundCrossRefs(ctx, "auth-svc")
	if err != nil {
		t.Fatalf("ListInboundCrossRefs: %v", err)
	}
	if len(inbound) != 1 || inbound[0].Raw != "ccg://auth-svc/#New" {
		t.Fatalf("inbound after replace = %+v, want only the new auth-svc ref", inbound)
	}

	outbound, err := s.ListOutboundCrossRefs(ctx, "web")
	if err != nil {
		t.Fatalf("ListOutboundCrossRefs: %v", err)
	}
	if len(outbound) != 2 {
		t.Fatalf("outbound rows = %d, want 2", len(outbound))
	}
}

func TestListInboundCrossRefs_ExcludesSelfNamespace(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()
	rows := []graph.CrossRef{
		{FromNamespace: "auth-svc", FromNodeID: 1, Raw: "ccg://auth-svc/internal#Self", ToNamespace: "auth-svc", ToSymbol: "Self", Status: graph.CrossRefStatusResolved, Source: graph.CrossRefSourceAnnotation},
	}
	if err := s.ReplaceCrossRefsFrom(ctx, "auth-svc", rows); err != nil {
		t.Fatalf("replace: %v", err)
	}
	inbound, err := s.ListInboundCrossRefs(ctx, "auth-svc")
	if err != nil {
		t.Fatalf("ListInboundCrossRefs: %v", err)
	}
	if len(inbound) != 0 {
		t.Fatalf("inbound = %d rows, want 0 (self refs are rebuilt outbound)", len(inbound))
	}
}

func TestUpdateCrossRefResolution_RemapsAndKills(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()
	resolved := uint(42)
	rows := []graph.CrossRef{
		{FromNamespace: "web", FromNodeID: 1, Raw: "ccg://auth-svc/#Fn", ToNamespace: "auth-svc", ToSymbol: "Fn", ResolvedNodeID: &resolved, Status: graph.CrossRefStatusResolved, Source: graph.CrossRefSourceAnnotation},
	}
	if err := s.ReplaceCrossRefsFrom(ctx, "web", rows); err != nil {
		t.Fatalf("replace: %v", err)
	}
	stored, err := s.ListInboundCrossRefs(ctx, "auth-svc")
	if err != nil || len(stored) != 1 {
		t.Fatalf("load stored row: %v (%d rows)", err, len(stored))
	}

	if err := s.UpdateCrossRefResolution(ctx, stored[0].ID, nil, graph.CrossRefStatusDead); err != nil {
		t.Fatalf("kill resolution: %v", err)
	}
	after, err := s.ListInboundCrossRefs(ctx, "auth-svc")
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if after[0].Status != graph.CrossRefStatusDead || after[0].ResolvedNodeID != nil {
		t.Fatalf("after kill = %+v, want dead with nil node", after[0])
	}

	remapped := uint(99)
	if err := s.UpdateCrossRefResolution(ctx, stored[0].ID, &remapped, graph.CrossRefStatusResolved); err != nil {
		t.Fatalf("remap resolution: %v", err)
	}
	final, err := s.ListInboundCrossRefs(ctx, "auth-svc")
	if err != nil {
		t.Fatalf("reload final: %v", err)
	}
	if final[0].Status != graph.CrossRefStatusResolved || final[0].ResolvedNodeID == nil || *final[0].ResolvedNodeID != 99 {
		t.Fatalf("after remap = %+v, want resolved node 99", final[0])
	}
}
