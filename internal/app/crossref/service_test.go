package crossref_test

import (
	"context"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/tae2089/code-context-graph/internal/adapters/outbound/graphgorm"
	"github.com/tae2089/code-context-graph/internal/app/crossref"
	requestctx "github.com/tae2089/code-context-graph/internal/ctx"
	"github.com/tae2089/code-context-graph/internal/domain/graph"
	"github.com/tae2089/code-context-graph/internal/domain/reference"
)

func setupStore(t *testing.T) *graphgorm.Store {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	s := graphgorm.New(db)
	if err := s.AutoMigrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return s
}

func seedNode(t *testing.T, s *graphgorm.Store, namespace string, node graph.Node) uint {
	t.Helper()
	ctx := requestctx.WithNamespace(context.Background(), namespace)
	if err := s.UpsertNodes(ctx, []graph.Node{node}); err != nil {
		t.Fatalf("seed node %s: %v", node.QualifiedName, err)
	}
	stored, err := s.GetNode(ctx, node.QualifiedName)
	if err != nil || stored == nil {
		t.Fatalf("load node %s: %v", node.QualifiedName, err)
	}
	return stored.ID
}

func annotate(t *testing.T, s *graphgorm.Store, namespace string, nodeID uint, seeValues ...string) {
	t.Helper()
	ctx := requestctx.WithNamespace(context.Background(), namespace)
	tags := make([]graph.DocTag, len(seeValues))
	for i, v := range seeValues {
		tags[i] = graph.DocTag{Kind: graph.TagSee, Value: v, Ordinal: i}
	}
	if err := s.UpsertAnnotation(ctx, &graph.Annotation{NodeID: nodeID, Summary: "test", Tags: tags}); err != nil {
		t.Fatalf("annotate node %d: %v", nodeID, err)
	}
}

func syncNamespace(t *testing.T, svc *crossref.Service, namespace string) {
	t.Helper()
	ctx := requestctx.WithNamespace(context.Background(), namespace)
	if err := svc.SyncNamespace(ctx); err != nil {
		t.Fatalf("sync %s: %v", namespace, err)
	}
}

// countingStore counts resolve round-trips so tests can assert per-distinct-target batching.
type countingStore struct {
	*graphgorm.Store
	resolveCalls int
}

func (c *countingStore) ResolveCCGRef(ctx context.Context, ref reference.Ref) (uint, bool, error) {
	c.resolveCalls++
	return c.Store.ResolveCCGRef(ctx, ref)
}

func TestSyncNamespace_ResolvesEachDistinctTargetOnce(t *testing.T) {
	s := setupStore(t)
	counting := &countingStore{Store: s}
	svc := crossref.New(counting)

	seedNode(t, s, "auth-svc", graph.Node{
		QualifiedName: "auth.ValidateToken", Kind: graph.NodeKindFunction, Name: "ValidateToken",
		FilePath: "internal/auth/token.go", StartLine: 10, EndLine: 20,
	})
	loginID := seedNode(t, s, "web", graph.Node{
		QualifiedName: "web.Login", Kind: graph.NodeKindFunction, Name: "Login",
		FilePath: "internal/web/login.go", StartLine: 5, EndLine: 25,
	})
	logoutID := seedNode(t, s, "web", graph.Node{
		QualifiedName: "web.Logout", Kind: graph.NodeKindFunction, Name: "Logout",
		FilePath: "internal/web/login.go", StartLine: 30, EndLine: 40,
	})
	// Two nodes referencing the same target, one of them also via a second identical tag.
	annotate(t, s, "web", loginID,
		"ccg://auth-svc/internal/auth/token.go#ValidateToken",
		"ccg://auth-svc/internal/auth/token.go#ValidateToken",
	)
	annotate(t, s, "web", logoutID, "ccg://auth-svc/internal/auth/token.go#ValidateToken")

	syncNamespace(t, svc, "web")

	if counting.resolveCalls != 1 {
		t.Fatalf("resolve round-trips = %d, want 1 for a single distinct target", counting.resolveCalls)
	}
	rows, err := s.ListOutboundCrossRefs(context.Background(), "web")
	if err != nil {
		t.Fatalf("list outbound: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("outbound rows = %d, want 2 (one per declaring node, dedup of identical tag)", len(rows))
	}
	for _, row := range rows {
		if row.Status != graph.CrossRefStatusResolved || row.ResolvedNodeID == nil {
			t.Fatalf("row = %+v, want resolved", row)
		}
	}

	// Inbound re-resolution of auth-svc: 2 rows, same target — one resolve round-trip.
	counting.resolveCalls = 0
	syncNamespace(t, svc, "auth-svc")
	if counting.resolveCalls != 1 {
		t.Fatalf("inbound resolve round-trips = %d, want 1 for a single distinct target", counting.resolveCalls)
	}
}

func TestSyncNamespace_MaterializesResolvedAndDeadRefs(t *testing.T) {
	s := setupStore(t)
	svc := crossref.New(s)
	ctx := context.Background()

	targetID := seedNode(t, s, "auth-svc", graph.Node{
		QualifiedName: "auth.ValidateToken", Kind: graph.NodeKindFunction, Name: "ValidateToken",
		FilePath: "internal/auth/token.go", StartLine: 10, EndLine: 20,
	})
	callerID := seedNode(t, s, "web", graph.Node{
		QualifiedName: "web.Login", Kind: graph.NodeKindFunction, Name: "Login",
		FilePath: "internal/web/login.go", StartLine: 5, EndLine: 25,
	})
	annotate(t, s, "web", callerID,
		"ccg://auth-svc/internal/auth/token.go#ValidateToken",
		"ccg://billing/#Charge",
		"SessionManager.Create",
	)

	syncNamespace(t, svc, "web")

	rows, err := s.ListOutboundCrossRefs(ctx, "web")
	if err != nil {
		t.Fatalf("list outbound: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("outbound rows = %d, want 2 (local @see ignored)", len(rows))
	}
	byRaw := map[string]graph.CrossRef{}
	for _, r := range rows {
		byRaw[r.Raw] = r
	}
	resolved := byRaw["ccg://auth-svc/internal/auth/token.go#ValidateToken"]
	if resolved.Status != graph.CrossRefStatusResolved || resolved.ResolvedNodeID == nil || *resolved.ResolvedNodeID != targetID {
		t.Fatalf("resolved row = %+v, want resolved to node %d", resolved, targetID)
	}
	if resolved.FromNodeID != callerID || resolved.ToNamespace != "auth-svc" || resolved.ToSymbol != "ValidateToken" {
		t.Fatalf("resolved row identity = %+v", resolved)
	}
	dead := byRaw["ccg://billing/#Charge"]
	if dead.Status != graph.CrossRefStatusDead || dead.ResolvedNodeID != nil {
		t.Fatalf("dead row = %+v, want dead without node", dead)
	}
}

func TestSyncNamespace_SkipsMalformedRefs(t *testing.T) {
	s := setupStore(t)
	svc := crossref.New(s)

	callerID := seedNode(t, s, "web", graph.Node{
		QualifiedName: "web.Login", Kind: graph.NodeKindFunction, Name: "Login",
		FilePath: "internal/web/login.go", StartLine: 5, EndLine: 25,
	})
	annotate(t, s, "web", callerID, "ccg://bad/../escape#X")

	syncNamespace(t, svc, "web")

	rows, err := s.ListOutboundCrossRefs(context.Background(), "web")
	if err != nil {
		t.Fatalf("list outbound: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("outbound rows = %d, want 0 for malformed ref", len(rows))
	}
}

func TestSyncNamespace_ReresolvesInboundAfterTargetRebuild(t *testing.T) {
	s := setupStore(t)
	svc := crossref.New(s)
	ctx := context.Background()
	authCtx := requestctx.WithNamespace(ctx, "auth-svc")

	oldID := seedNode(t, s, "auth-svc", graph.Node{
		QualifiedName: "auth.ValidateToken", Kind: graph.NodeKindFunction, Name: "ValidateToken",
		FilePath: "internal/auth/token.go", StartLine: 10, EndLine: 20,
	})
	callerID := seedNode(t, s, "web", graph.Node{
		QualifiedName: "web.Login", Kind: graph.NodeKindFunction, Name: "Login",
		FilePath: "internal/web/login.go", StartLine: 5, EndLine: 25,
	})
	annotate(t, s, "web", callerID, "ccg://auth-svc/internal/auth/token.go#ValidateToken")
	syncNamespace(t, svc, "web")

	// Simulate a replace-style rebuild of auth-svc: node ids change.
	if err := s.DeleteNodesByFile(authCtx, "internal/auth/token.go"); err != nil {
		t.Fatalf("delete target nodes: %v", err)
	}
	syncNamespace(t, svc, "auth-svc")

	rows, err := s.ListOutboundCrossRefs(ctx, "web")
	if err != nil {
		t.Fatalf("list after delete: %v", err)
	}
	if rows[0].Status != graph.CrossRefStatusDead || rows[0].ResolvedNodeID != nil {
		t.Fatalf("after target deletion row = %+v, want dead", rows[0])
	}

	newID := seedNode(t, s, "auth-svc", graph.Node{
		QualifiedName: "auth.ValidateToken", Kind: graph.NodeKindFunction, Name: "ValidateToken",
		FilePath: "internal/auth/token.go", StartLine: 42, EndLine: 60,
	})
	if newID == oldID {
		t.Fatalf("test setup: expected a fresh node id, got same id %d", newID)
	}
	syncNamespace(t, svc, "auth-svc")

	rows, err = s.ListOutboundCrossRefs(ctx, "web")
	if err != nil {
		t.Fatalf("list after revive: %v", err)
	}
	if rows[0].Status != graph.CrossRefStatusResolved || rows[0].ResolvedNodeID == nil || *rows[0].ResolvedNodeID != newID {
		t.Fatalf("after revive row = %+v, want resolved to remapped node %d", rows[0], newID)
	}
}
