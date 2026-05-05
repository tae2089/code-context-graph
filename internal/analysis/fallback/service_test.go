package fallback

import (
	"context"
	"testing"

	"github.com/tae2089/code-context-graph/internal/model"
	"github.com/tae2089/code-context-graph/internal/store/gormstore"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

func setupFallbackDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	st := gormstore.New(db)
	if err := st.AutoMigrate(); err != nil {
		t.Fatal(err)
	}
	return db
}

func boolPtr(v bool) *bool {
	return &v
}

func TestSuspectFallbackEdges_FlagsDisjointIntentAndDomainRule(t *testing.T) {
	db := setupFallbackDB(t)
	ctx := context.Background()
	store := gormstore.New(db)

	source := model.Node{QualifiedName: "pkg.Authenticate", Kind: model.NodeKindFunction, Name: "Authenticate", FilePath: "auth.go", StartLine: 1, EndLine: 2, Language: "go"}
	target := model.Node{QualifiedName: "pkg.RenderInvoice", Kind: model.NodeKindFunction, Name: "RenderInvoice", FilePath: "invoice.go", StartLine: 1, EndLine: 2, Language: "go"}
	if err := store.UpsertNodes(ctx, []model.Node{source, target}); err != nil {
		t.Fatal(err)
	}
	sourceNode, _ := store.GetNode(ctx, "pkg.Authenticate")
	targetNode, _ := store.GetNode(ctx, "pkg.RenderInvoice")
	if err := store.UpsertEdges(ctx, []model.Edge{{FromNodeID: sourceNode.ID, ToNodeID: targetNode.ID, Kind: model.EdgeKindFallbackCalls, Fingerprint: "auth-invoice-fallback"}}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertAnnotation(ctx, &model.Annotation{NodeID: sourceNode.ID, Summary: "auth", Tags: []model.DocTag{{Kind: model.TagIntent, Value: "verify credentials", Ordinal: 0}, {Kind: model.TagDomainRule, Value: "lock account after repeated failures", Ordinal: 1}}}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertAnnotation(ctx, &model.Annotation{NodeID: targetNode.ID, Summary: "billing", Tags: []model.DocTag{{Kind: model.TagIntent, Value: "render invoice PDF", Ordinal: 0}, {Kind: model.TagDomainRule, Value: "apply VAT before final export", Ordinal: 1}}}); err != nil {
		t.Fatal(err)
	}

	results, err := New(db, store).FindSuspects(ctx, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 suspect fallback edge, got %d", len(results))
	}
	if results[0].Source.QualifiedName != "pkg.Authenticate" || results[0].Target.QualifiedName != "pkg.RenderInvoice" {
		t.Fatalf("unexpected suspect edge: %+v", results[0])
	}
	if !results[0].Suspect {
		t.Fatal("expected fallback edge to be marked suspect")
	}
}

func TestSuspectFallbackEdges_IgnoresOverlappingAnnotationContext(t *testing.T) {
	db := setupFallbackDB(t)
	ctx := context.Background()
	store := gormstore.New(db)

	source := model.Node{QualifiedName: "pkg.VerifySession", Kind: model.NodeKindFunction, Name: "VerifySession", FilePath: "session.go", StartLine: 1, EndLine: 2, Language: "go"}
	target := model.Node{QualifiedName: "pkg.CreateSession", Kind: model.NodeKindFunction, Name: "CreateSession", FilePath: "session_create.go", StartLine: 1, EndLine: 2, Language: "go"}
	if err := store.UpsertNodes(ctx, []model.Node{source, target}); err != nil {
		t.Fatal(err)
	}
	sourceNode, _ := store.GetNode(ctx, "pkg.VerifySession")
	targetNode, _ := store.GetNode(ctx, "pkg.CreateSession")
	if err := store.UpsertEdges(ctx, []model.Edge{{FromNodeID: sourceNode.ID, ToNodeID: targetNode.ID, Kind: model.EdgeKindFallbackCalls, Fingerprint: "session-fallback"}}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertAnnotation(ctx, &model.Annotation{NodeID: sourceNode.ID, Summary: "session auth", Tags: []model.DocTag{{Kind: model.TagIntent, Value: "verify session token", Ordinal: 0}, {Kind: model.TagDomainRule, Value: "session token expires after inactivity", Ordinal: 1}}}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertAnnotation(ctx, &model.Annotation{NodeID: targetNode.ID, Summary: "session auth", Tags: []model.DocTag{{Kind: model.TagIntent, Value: "create session token", Ordinal: 0}, {Kind: model.TagDomainRule, Value: "session token expires after inactivity", Ordinal: 1}}}); err != nil {
		t.Fatal(err)
	}

	results, err := New(db, store).FindSuspects(ctx, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatalf("expected overlapping annotation context to suppress suspect edge, got %d results", len(results))
	}
}
