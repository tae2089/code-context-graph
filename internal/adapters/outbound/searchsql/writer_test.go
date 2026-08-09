//go:build fts5

package searchsql

import (
	"testing"

	"gorm.io/gorm"

	requestctx "github.com/tae2089/code-context-graph/internal/ctx"
	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

// A refresh that fills `content` but leaves `intent_content` empty produces an
// intent index with nothing in it, and the failure is silent: every intent query
// simply returns nothing, which the tool is also allowed to do when no reason was
// recorded. The two cases are indistinguishable from the outside, so the fill has
// to be pinned here.
func TestRefreshSearchDocuments_FillsIntentContentSeparatelyFromName(t *testing.T) {
	db := setupTestDB(t)
	ctx := requestctx.WithNamespace(t.Context(), requestctx.DefaultNamespace)

	withReason := graph.Node{QualifiedName: "webhook.verifySignature", Kind: graph.NodeKindFunction, Name: "verifySignature", FilePath: "webhook/verify.go", Language: "go"}
	withoutReason := graph.Node{QualifiedName: "webhook.parseEvent", Kind: graph.NodeKindFunction, Name: "parseEvent", FilePath: "webhook/parse.go", Language: "go"}
	for _, node := range []*graph.Node{&withReason, &withoutReason} {
		if err := db.Create(node).Error; err != nil {
			t.Fatalf("seed node %s: %v", node.Name, err)
		}
	}
	annotation := graph.Annotation{NodeID: withReason.ID, Summary: "verifySignature checks the HMAC."}
	if err := db.Create(&annotation).Error; err != nil {
		t.Fatalf("seed annotation: %v", err)
	}
	tags := []graph.DocTag{
		{AnnotationID: annotation.ID, Kind: graph.TagIntent, Value: "reject a push that did not come from the configured forge", Ordinal: 0},
		{AnnotationID: annotation.ID, Kind: graph.TagSideEffect, Value: "reads the shared secret from the environment", Ordinal: 1},
	}
	for i := range tags {
		if err := db.Create(&tags[i]).Error; err != nil {
			t.Fatalf("seed tag: %v", err)
		}
	}

	if _, err := RefreshSearchDocuments(ctx, db); err != nil {
		t.Fatalf("RefreshSearchDocuments: %v", err)
	}

	reasoned := loadSearchDocument(t, db, withReason.ID)
	if want := "reject a push that did not come from the configured forge"; reasoned.IntentContent != want {
		t.Errorf("intent_content = %q, want %q", reasoned.IntentContent, want)
	}
	if reasoned.Content == "" {
		t.Error("content is empty; the name index must keep being filled")
	}

	bare := loadSearchDocument(t, db, withoutReason.ID)
	if bare.IntentContent != "" {
		t.Errorf("a node with no recorded reason got intent_content %q; it must stay out of the intent index", bare.IntentContent)
	}
	if bare.Content == "" {
		t.Error("a node with no recorded reason must still be findable by name")
	}
}

func loadSearchDocument(t *testing.T, db *gorm.DB, nodeID uint) graph.SearchDocument {
	t.Helper()
	var doc graph.SearchDocument
	if err := db.Where("node_id = ?", nodeID).First(&doc).Error; err != nil {
		t.Fatalf("load search document for node %d: %v", nodeID, err)
	}
	return doc
}
