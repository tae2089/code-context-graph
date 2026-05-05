package fallback

import (
	"context"
	"strings"
	"testing"

	"github.com/tae2089/code-context-graph/internal/model"
	"github.com/tae2089/code-context-graph/internal/paging"
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

func TestSuspectFallbackEdges_FindPageBoundsFallbackEdgeAnalysis(t *testing.T) {
	db := setupFallbackDB(t)
	ctx := context.Background()
	store := gormstore.New(db)

	for _, suffix := range []string{"A", "B", "C", "D"} {
		source := model.Node{QualifiedName: "pkg.Source" + suffix, Kind: model.NodeKindFunction, Name: "Source", FilePath: "src.go", StartLine: 1, EndLine: 2, Language: "go"}
		target := model.Node{QualifiedName: "pkg.Target" + suffix, Kind: model.NodeKindFunction, Name: "Target", FilePath: "target.go", StartLine: 1, EndLine: 2, Language: "go"}
		if err := store.UpsertNodes(ctx, []model.Node{source, target}); err != nil {
			t.Fatal(err)
		}
		sourceNode, _ := store.GetNode(ctx, source.QualifiedName)
		targetNode, _ := store.GetNode(ctx, target.QualifiedName)
		if err := store.UpsertEdges(ctx, []model.Edge{{FromNodeID: sourceNode.ID, ToNodeID: targetNode.ID, Kind: model.EdgeKindFallbackCalls, Fingerprint: "fallback-" + strings.ToLower(suffix)}}); err != nil {
			t.Fatal(err)
		}
		if err := store.UpsertAnnotation(ctx, &model.Annotation{NodeID: sourceNode.ID, Tags: []model.DocTag{{Kind: model.TagIntent, Value: "source auth", Ordinal: 0}}}); err != nil {
			t.Fatal(err)
		}
		if err := store.UpsertAnnotation(ctx, &model.Annotation{NodeID: targetNode.ID, Tags: []model.DocTag{{Kind: model.TagIntent, Value: "invoice render", Ordinal: 0}}}); err != nil {
			t.Fatal(err)
		}
	}

	got, err := New(db, store).FindSuspectsPage(ctx, Options{Page: paging.Request{Limit: 2, Offset: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Items) != 2 {
		t.Fatalf("expected 2 bounded suspect edges, got %d", len(got.Items))
	}
	if got.Items[0].Edge.Fingerprint != "fallback-b" || got.Items[1].Edge.Fingerprint != "fallback-c" {
		t.Fatalf("unexpected bounded edge page: %s, %s", got.Items[0].Edge.Fingerprint, got.Items[1].Edge.Fingerprint)
	}
	if got.Pagination.Limit != 2 || got.Pagination.Offset != 1 || got.Pagination.Returned != 2 || !got.Pagination.HasMore {
		t.Fatalf("unexpected pagination: %+v", got.Pagination)
	}
}

func TestSuspectFallbackEdges_FindPageCanReturnFewerSuspectsThanLimitWhileHasMoreRemains(t *testing.T) {
	db := setupFallbackDB(t)
	ctx := context.Background()
	store := gormstore.New(db)

	// First fallback edge is suspect.
	{
		source := model.Node{QualifiedName: "pkg.SourceA", Kind: model.NodeKindFunction, Name: "SourceA", FilePath: "src.go", StartLine: 1, EndLine: 2, Language: "go"}
		target := model.Node{QualifiedName: "pkg.TargetA", Kind: model.NodeKindFunction, Name: "TargetA", FilePath: "target.go", StartLine: 1, EndLine: 2, Language: "go"}
		if err := store.UpsertNodes(ctx, []model.Node{source, target}); err != nil {
			t.Fatal(err)
		}
		sourceNode, _ := store.GetNode(ctx, source.QualifiedName)
		targetNode, _ := store.GetNode(ctx, target.QualifiedName)
		if err := store.UpsertEdges(ctx, []model.Edge{{FromNodeID: sourceNode.ID, ToNodeID: targetNode.ID, Kind: model.EdgeKindFallbackCalls, Fingerprint: "fallback-a"}}); err != nil {
			t.Fatal(err)
		}
		if err := store.UpsertAnnotation(ctx, &model.Annotation{NodeID: sourceNode.ID, Tags: []model.DocTag{{Kind: model.TagIntent, Value: "source auth", Ordinal: 0}}}); err != nil {
			t.Fatal(err)
		}
		if err := store.UpsertAnnotation(ctx, &model.Annotation{NodeID: targetNode.ID, Tags: []model.DocTag{{Kind: model.TagIntent, Value: "invoice render", Ordinal: 0}}}); err != nil {
			t.Fatal(err)
		}
	}

	// Second fallback edge is suppressed by overlapping annotation context.
	{
		source := model.Node{QualifiedName: "pkg.SourceB", Kind: model.NodeKindFunction, Name: "SourceB", FilePath: "src.go", StartLine: 1, EndLine: 2, Language: "go"}
		target := model.Node{QualifiedName: "pkg.TargetB", Kind: model.NodeKindFunction, Name: "TargetB", FilePath: "target.go", StartLine: 1, EndLine: 2, Language: "go"}
		if err := store.UpsertNodes(ctx, []model.Node{source, target}); err != nil {
			t.Fatal(err)
		}
		sourceNode, _ := store.GetNode(ctx, source.QualifiedName)
		targetNode, _ := store.GetNode(ctx, target.QualifiedName)
		if err := store.UpsertEdges(ctx, []model.Edge{{FromNodeID: sourceNode.ID, ToNodeID: targetNode.ID, Kind: model.EdgeKindFallbackCalls, Fingerprint: "fallback-b"}}); err != nil {
			t.Fatal(err)
		}
		if err := store.UpsertAnnotation(ctx, &model.Annotation{NodeID: sourceNode.ID, Tags: []model.DocTag{{Kind: model.TagIntent, Value: "session token", Ordinal: 0}}}); err != nil {
			t.Fatal(err)
		}
		if err := store.UpsertAnnotation(ctx, &model.Annotation{NodeID: targetNode.ID, Tags: []model.DocTag{{Kind: model.TagIntent, Value: "session token refresh", Ordinal: 0}}}); err != nil {
			t.Fatal(err)
		}
	}

	// Third fallback edge is suspect and keeps HasMore true even when the page returns only one item.
	{
		source := model.Node{QualifiedName: "pkg.SourceC", Kind: model.NodeKindFunction, Name: "SourceC", FilePath: "src.go", StartLine: 1, EndLine: 2, Language: "go"}
		target := model.Node{QualifiedName: "pkg.TargetC", Kind: model.NodeKindFunction, Name: "TargetC", FilePath: "target.go", StartLine: 1, EndLine: 2, Language: "go"}
		if err := store.UpsertNodes(ctx, []model.Node{source, target}); err != nil {
			t.Fatal(err)
		}
		sourceNode, _ := store.GetNode(ctx, source.QualifiedName)
		targetNode, _ := store.GetNode(ctx, target.QualifiedName)
		if err := store.UpsertEdges(ctx, []model.Edge{{FromNodeID: sourceNode.ID, ToNodeID: targetNode.ID, Kind: model.EdgeKindFallbackCalls, Fingerprint: "fallback-c"}}); err != nil {
			t.Fatal(err)
		}
		if err := store.UpsertAnnotation(ctx, &model.Annotation{NodeID: sourceNode.ID, Tags: []model.DocTag{{Kind: model.TagIntent, Value: "source auth c", Ordinal: 0}}}); err != nil {
			t.Fatal(err)
		}
		if err := store.UpsertAnnotation(ctx, &model.Annotation{NodeID: targetNode.ID, Tags: []model.DocTag{{Kind: model.TagIntent, Value: "invoice render c", Ordinal: 0}}}); err != nil {
			t.Fatal(err)
		}
	}

	got, err := New(db, store).FindSuspectsPage(ctx, Options{Page: paging.Request{Limit: 1, Offset: 0}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Items) != 1 {
		t.Fatalf("expected 1 suspect from the first scanned fallback edge, got %d", len(got.Items))
	}
	if got.Items[0].Edge.Fingerprint != "fallback-a" {
		t.Fatalf("unexpected suspect edge: %s", got.Items[0].Edge.Fingerprint)
	}
	if got.Pagination.Limit != 1 || got.Pagination.Offset != 0 || got.Pagination.Returned != 1 || !got.Pagination.HasMore {
		t.Fatalf("unexpected pagination: %+v", got.Pagination)
	}

	got, err = New(db, store).FindSuspectsPage(ctx, Options{Page: paging.Request{Limit: 1, Offset: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Items) != 0 {
		t.Fatalf("expected 0 suspects from a page containing only suppressed fallback edges, got %d", len(got.Items))
	}
	if got.Pagination.Limit != 1 || got.Pagination.Offset != 1 || got.Pagination.Returned != 0 || !got.Pagination.HasMore {
		t.Fatalf("unexpected pagination for empty suspect page: %+v", got.Pagination)
	}
}
