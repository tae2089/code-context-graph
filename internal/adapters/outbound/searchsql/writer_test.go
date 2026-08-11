//go:build fts5

package searchsql

import (
	"slices"
	"testing"

	"gorm.io/gorm"

	requestctx "github.com/tae2089/code-context-graph/internal/ctx"
	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

// A refresh that fills `content` but records no reason produces an intent index
// with nothing in it, and the failure is silent: every intent query simply
// returns nothing, which the tool is also allowed to do when no reason was
// recorded. The two cases are indistinguishable from the outside, so the fill has
// to be pinned here.
func TestRefreshSearchDocuments_RecordsReasonsSeparatelyFromName(t *testing.T) {
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

	reasons := loadSearchReasons(t, db, withReason.ID)
	want := []string{"reject a push that did not come from the configured forge"}
	if !slices.Equal(reasons, want) {
		t.Errorf("recorded reasons = %v, want %v", reasons, want)
	}
	if loadSearchDocument(t, db, withReason.ID).Content == "" {
		t.Error("content is empty; the name index must keep being filled")
	}

	if bare := loadSearchReasons(t, db, withoutReason.ID); len(bare) != 0 {
		t.Errorf("a node with no recorded reason got reasons %v; it must stay out of the intent index", bare)
	}
	if loadSearchDocument(t, db, withoutReason.ID).Content == "" {
		t.Error("a node with no recorded reason must still be findable by name")
	}
}

// One reason tag is one row, so a node that wrote several of them is several
// documents. Joining them back into one is exactly the length penalty this
// storage exists to remove, and nothing else in the pipeline would notice.
func TestRefreshSearchDocuments_GivesEveryReasonTagItsOwnRow(t *testing.T) {
	db := setupTestDB(t)
	ctx := requestctx.WithNamespace(t.Context(), requestctx.DefaultNamespace)

	node := graph.Node{QualifiedName: "webhook.admitRepo", Kind: graph.NodeKindFunction, Name: "admitRepo", FilePath: "webhook/admit.go", Language: "go"}
	if err := db.Create(&node).Error; err != nil {
		t.Fatalf("seed node: %v", err)
	}
	annotation := graph.Annotation{NodeID: node.ID, Summary: "admitRepo decides."}
	if err := db.Create(&annotation).Error; err != nil {
		t.Fatalf("seed annotation: %v", err)
	}
	tags := []graph.DocTag{
		{AnnotationID: annotation.ID, Kind: graph.TagIntent, Value: "decide which push may trigger a build", Ordinal: 0},
		{AnnotationID: annotation.ID, Kind: graph.TagDomainRule, Value: "only allowlisted repositories are admitted", Ordinal: 1},
		{AnnotationID: annotation.ID, Kind: graph.TagDomainRule, Value: "a branch outside the filter is ignored", Ordinal: 2},
		{AnnotationID: annotation.ID, Kind: graph.TagDomainRule, Value: "a tag push never starts a build", Ordinal: 3},
	}
	for i := range tags {
		if err := db.Create(&tags[i]).Error; err != nil {
			t.Fatalf("seed tag: %v", err)
		}
	}

	if _, err := RefreshSearchDocuments(ctx, db); err != nil {
		t.Fatalf("RefreshSearchDocuments: %v", err)
	}

	want := []string{
		"decide which push may trigger a build",
		"only allowlisted repositories are admitted",
		"a branch outside the filter is ignored",
		"a tag push never starts a build",
	}
	if got := loadSearchReasons(t, db, node.ID); !slices.Equal(got, want) {
		t.Errorf("recorded reasons = %v, want %v", got, want)
	}
}

// A full rebuild and a node-scoped one are two separate queries over the same
// node table, and they select the kinds they index independently. Nothing
// downstream compares them, so a kind one path indexes and the other does not is
// silent: the full build writes the document and the next incremental update
// deletes it without writing it back. The node set survives one full rebuild
// followed by a scoped rebuild over every node, or the two paths disagree.
func TestRefreshSearchDocuments_ScopedRefreshKeepsWhatTheFullRebuildIndexed(t *testing.T) {
	db := setupTestDB(t)
	ctx := requestctx.WithNamespace(t.Context(), requestctx.DefaultNamespace)

	nodes := []graph.Node{
		{QualifiedName: "example/internal/mcp", Kind: graph.NodeKindPackage, Name: "mcp", FilePath: "internal/mcp", StartLine: 1, EndLine: 1, Language: "go"},
		{QualifiedName: "example/internal/mcp/server.go", Kind: graph.NodeKindFile, Name: "server.go", FilePath: "internal/mcp/server.go", StartLine: 1, EndLine: 40, Language: "go"},
		{QualifiedName: "mcp.Serve", Kind: graph.NodeKindFunction, Name: "Serve", FilePath: "internal/mcp/server.go", StartLine: 10, EndLine: 20, Language: "go"},
		{QualifiedName: "mcp.Server", Kind: graph.NodeKindClass, Name: "Server", FilePath: "internal/mcp/server.go", StartLine: 5, EndLine: 8, Language: "go"},
		{QualifiedName: "mcp.Options", Kind: graph.NodeKindType, Name: "Options", FilePath: "internal/mcp/server.go", StartLine: 22, EndLine: 25, Language: "go"},
		{QualifiedName: "mcp.TestServe", Kind: graph.NodeKindTest, Name: "TestServe", FilePath: "internal/mcp/server_test.go", StartLine: 1, EndLine: 9, Language: "go"},
	}
	nodeIDs := make([]uint, len(nodes))
	for i := range nodes {
		if err := db.Create(&nodes[i]).Error; err != nil {
			t.Fatalf("seed node %s: %v", nodes[i].Name, err)
		}
		nodeIDs[i] = nodes[i].ID
	}

	if _, err := RefreshSearchDocuments(ctx, db); err != nil {
		t.Fatalf("RefreshSearchDocuments: %v", err)
	}
	full := indexedNodeIDs(t, db)

	if _, err := RefreshSearchDocumentsFor(ctx, db, nodeIDs); err != nil {
		t.Fatalf("RefreshSearchDocumentsFor: %v", err)
	}
	scoped := indexedNodeIDs(t, db)

	if !slices.Equal(full, scoped) {
		t.Errorf("full rebuild indexed nodes %v, scoped rebuild left %v; the two paths must select the same node kinds", full, scoped)
	}
}

func indexedNodeIDs(t *testing.T, db *gorm.DB) []uint {
	t.Helper()
	var rows []graph.SearchDocument
	if err := db.Order("node_id").Find(&rows).Error; err != nil {
		t.Fatalf("load search documents: %v", err)
	}
	ids := make([]uint, len(rows))
	for i, row := range rows {
		ids[i] = row.NodeID
	}
	return ids
}

func loadSearchReasons(t *testing.T, db *gorm.DB, nodeID uint) []string {
	t.Helper()
	var rows []graph.SearchReason
	if err := db.Where("node_id = ?", nodeID).Order("id").Find(&rows).Error; err != nil {
		t.Fatalf("load search reasons for node %d: %v", nodeID, err)
	}
	out := make([]string, len(rows))
	for i, row := range rows {
		out[i] = row.Content
	}
	return out
}

func loadSearchDocument(t *testing.T, db *gorm.DB, nodeID uint) graph.SearchDocument {
	t.Helper()
	var doc graph.SearchDocument
	if err := db.Where("node_id = ?", nodeID).First(&doc).Error; err != nil {
		t.Fatalf("load search document for node %d: %v", nodeID, err)
	}
	return doc
}
