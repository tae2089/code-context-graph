//go:build fts5

package searchsql

import (
	"fmt"
	"testing"

	"gorm.io/gorm"

	requestctx "github.com/tae2089/code-context-graph/internal/ctx"
	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

// seedReasoned creates one declaration and writes the given reason tags on it,
// in the order they are passed.
func seedReasoned(t *testing.T, db *gorm.DB, name string, tags ...graph.DocTag) graph.Node {
	t.Helper()
	node := graph.Node{
		QualifiedName: "reasons." + name,
		Kind:          graph.NodeKindFunction,
		Name:          name,
		FilePath:      "reasons/" + name + ".go",
		Language:      "go",
	}
	if err := db.Create(&node).Error; err != nil {
		t.Fatalf("seed node %s: %v", name, err)
	}
	if len(tags) == 0 {
		return node
	}
	annotation := graph.Annotation{NodeID: node.ID}
	if err := db.Create(&annotation).Error; err != nil {
		t.Fatalf("seed annotation %s: %v", name, err)
	}
	for i := range tags {
		tags[i].AnnotationID = annotation.ID
		tags[i].Ordinal = i
		if err := db.Create(&tags[i]).Error; err != nil {
			t.Fatalf("seed tag %d on %s: %v", i, name, err)
		}
	}
	return node
}

// countIntentFTSRows reports how many rows the SQLite intent index holds for the
// default namespace.
func countIntentFTSRows(t *testing.T, db *gorm.DB) int {
	t.Helper()
	var rows int
	if err := db.Raw("SELECT count(*) FROM "+sqliteIntentFTSTable+" WHERE namespace = ?", requestctx.DefaultNamespace).Scan(&rows).Error; err != nil {
		t.Fatalf("count intent index rows: %v", err)
	}
	return rows
}

// Each reason tag has to reach the index as its own document. Joining them is
// what charged a node's @intent for the length of every @domainRule beside it,
// and from the outside that is invisible: the joined node still answers, just
// lower, so nothing fails and the wrong file is on top.
func TestIntentIndex_HoldsOneDocumentPerReasonTag(t *testing.T) {
	db := setupTestDB(t)
	seedReasoned(t, db, "admitRepo",
		graph.DocTag{Kind: graph.TagIntent, Value: "decide which push may trigger a build"},
		graph.DocTag{Kind: graph.TagDomainRule, Value: "only allowlisted repositories are admitted"},
		graph.DocTag{Kind: graph.TagDomainRule, Value: "a branch outside the filter is ignored"},
		graph.DocTag{Kind: graph.TagDomainRule, Value: "a tag push never starts a build"},
		// Not a reason: it says what the code does, which is the name index's job.
		graph.DocTag{Kind: graph.TagSideEffect, Value: "writes an admission record"},
	)
	buildIntentIndex(t, db)

	if got := countIntentFTSRows(t, db); got != 4 {
		t.Errorf("intent index holds %d documents, want 4 — one per reason tag", got)
	}
}

// A declaration whose author wrote three domain rules must not be pushed below
// one that wrote none, when the question matches the @intent both of them share.
// Under the joined index it was: the three rules lengthened the one document and
// BM25 charges length.
func TestQueryIntent_ExtraRulesDoNotSinkTheDeclarationThatWroteThem(t *testing.T) {
	db := setupTestDB(t)
	const shared = "verify the signature so a push from anywhere else is rejected"
	// The loaded declaration is seeded first so it holds the lower node id. Ties
	// break on id, so on equal footing it must come first — and under the joined
	// index it cannot, because its three rules lengthened its one document.
	loaded := seedReasoned(t, db, "verifyLoaded",
		graph.DocTag{Kind: graph.TagIntent, Value: shared},
		graph.DocTag{Kind: graph.TagDomainRule, Value: "the shared secret is read from the environment and never logged"},
		graph.DocTag{Kind: graph.TagDomainRule, Value: "an unsigned request is refused before the body is parsed"},
		graph.DocTag{Kind: graph.TagDomainRule, Value: "a signature that does not compare in constant time is a defect"},
	)
	bare := seedReasoned(t, db, "verifyBare", graph.DocTag{Kind: graph.TagIntent, Value: shared})
	backend := buildIntentIndex(t, db)
	ctx := requestctx.WithNamespace(t.Context(), requestctx.DefaultNamespace)

	result, err := NewReader(db, backend).QueryIntent(ctx, "why do we verify the signature on a push", 10)
	if err != nil {
		t.Fatalf("QueryIntent: %v", err)
	}
	scores := map[uint]int{}
	for i, hit := range result.Hits {
		scores[hit.Node.ID] = i
	}
	bareRank, bareSeen := scores[bare.ID]
	loadedRank, loadedSeen := scores[loaded.ID]
	if !bareSeen || !loadedSeen {
		t.Fatalf("both declarations must answer; got %v", answeringNodes(result))
	}
	// Same reason, same words, so neither may be ranked below the other. Ties
	// break on node id, which is the only order the two can legitimately differ in.
	if (bareRank < loadedRank) != (bare.ID < loaded.ID) {
		t.Errorf("rank order (%d before %d) does not follow the tiebreak; the three domain rules moved the score",
			bareRank, loadedRank)
	}
}

// One declaration is one answer. It arrives as several documents now, and if
// that leaked out the caller would see the same file three times and read it as
// three findings.
func TestQueryIntent_AnswersOnceForADeclarationWithSeveralReasons(t *testing.T) {
	db := setupTestDB(t)
	node := seedReasoned(t, db, "drainQueue",
		graph.DocTag{Kind: graph.TagIntent, Value: "keep the queue draining under backpressure"},
		graph.DocTag{Kind: graph.TagDomainRule, Value: "a drained queue never blocks the producer"},
	)
	backend := buildIntentIndex(t, db)
	ctx := requestctx.WithNamespace(t.Context(), requestctx.DefaultNamespace)

	result, err := NewReader(db, backend).QueryIntent(ctx, "what keeps the queue draining", 10)
	if err != nil {
		t.Fatalf("QueryIntent: %v", err)
	}
	seen := 0
	for _, hit := range result.Hits {
		if hit.Node.ID == node.ID {
			seen++
		}
	}
	if seen != 1 {
		t.Errorf("declaration %d appears %d times, want 1", node.ID, seen)
	}
}

// When the question's words are split across two reasons, the answer has to name
// both of them. Reporting only the words of the single best-scoring reason would
// tell the reader the question half-matched when it fully matched.
func TestQueryIntent_ReportsTermsCombinedAcrossReasons(t *testing.T) {
	db := setupTestDB(t)
	node := seedReasoned(t, db, "rotateSecret",
		graph.DocTag{Kind: graph.TagIntent, Value: "rotate the webhook secret without dropping deliveries"},
		graph.DocTag{Kind: graph.TagDomainRule, Value: "the previous secret stays valid for one overlap window"},
	)
	backend := buildIntentIndex(t, db)
	ctx := requestctx.WithNamespace(t.Context(), requestctx.DefaultNamespace)

	result, err := NewReader(db, backend).QueryIntent(ctx, "rotate secret overlap window", 10)
	if err != nil {
		t.Fatalf("QueryIntent: %v", err)
	}
	if len(result.Hits) != 1 || result.Hits[0].Node.ID != node.ID {
		t.Fatalf("expected the one declaration to answer, got %v", answeringNodes(result))
	}
	got := map[string]bool{}
	for _, term := range result.Hits[0].Terms {
		got[term] = true
	}
	// "rotate" is only in the @intent; "overlap" and "window" are only in the rule.
	for _, want := range []string{"rotate", "overlap", "window"} {
		if !got[want] {
			t.Errorf("matched terms %v missing %q; terms from every matching reason must be reported together",
				result.Hits[0].Terms, want)
		}
	}
}

// The caller asks for a number of declarations, not a number of reasons. If
// reasons spent the slots, a node whose author wrote three of them would shorten
// the page for everybody else.
func TestQueryIntent_LimitCountsDeclarationsNotReasons(t *testing.T) {
	db := setupTestDB(t)
	for i := range 6 {
		seedReasoned(t, db, fmt.Sprintf("drain%02d", i),
			graph.DocTag{Kind: graph.TagIntent, Value: "keep the queue draining under backpressure"},
			graph.DocTag{Kind: graph.TagDomainRule, Value: "a draining queue never blocks the producer"},
			graph.DocTag{Kind: graph.TagDomainRule, Value: "the queue drains oldest first"},
		)
	}
	backend := buildIntentIndex(t, db)
	ctx := requestctx.WithNamespace(t.Context(), requestctx.DefaultNamespace)

	result, err := NewReader(db, backend).QueryIntent(ctx, "what keeps the queue draining", 4)
	if err != nil {
		t.Fatalf("QueryIntent: %v", err)
	}
	if len(result.Hits) != 4 {
		t.Errorf("limit 4 returned %d declarations, want 4", len(result.Hits))
	}
	seen := map[uint]bool{}
	for _, hit := range result.Hits {
		if seen[hit.Node.ID] {
			t.Errorf("declaration %d filled two of the four slots", hit.Node.ID)
		}
		seen[hit.Node.ID] = true
	}
}

// The corpus the scorer divides by is a count of reasons, because one reason is
// one document. A later coverage question — how many declarations recorded a
// reason at all — is a different number, and storing node_id on every row is what
// keeps it answerable.
func TestIntentCorpus_CountsReasonsAndStillAllowsCountingDeclarations(t *testing.T) {
	db := setupTestDB(t)
	seedReasoned(t, db, "one", graph.DocTag{Kind: graph.TagIntent, Value: "keep the queue draining under backpressure"})
	seedReasoned(t, db, "three",
		graph.DocTag{Kind: graph.TagIntent, Value: "decide which push may trigger a build"},
		graph.DocTag{Kind: graph.TagDomainRule, Value: "only allowlisted repositories are admitted"},
		graph.DocTag{Kind: graph.TagDomainRule, Value: "a tag push never starts a build"},
	)
	seedReasoned(t, db, "silent")
	buildIntentIndex(t, db)
	ctx := requestctx.WithNamespace(t.Context(), requestctx.DefaultNamespace)

	reader := NewReader(db, NewSQLiteBackend())
	corpus, err := reader.intentCorpusSize(ctx)
	if err != nil {
		t.Fatalf("intentCorpusSize: %v", err)
	}
	if corpus != 4 {
		t.Errorf("corpus = %d, want 4 recorded reasons", corpus)
	}

	var declarations int64
	if err := db.WithContext(ctx).Model(&graph.SearchReason{}).
		Where("namespace = ?", requestctx.DefaultNamespace).
		Distinct("node_id").Count(&declarations).Error; err != nil {
		t.Fatalf("count declarations with a reason: %v", err)
	}
	if declarations != 2 {
		t.Errorf("declarations carrying a reason = %d, want 2", declarations)
	}
}

// An incremental update has to reload every reason row of the nodes it touches,
// not just the first. A node that gained a domain rule and kept its @intent must
// end up with both in the index, and a node that lost one must lose it.
func TestRebuildNodes_ReloadsEveryReasonOfTheScopedNodes(t *testing.T) {
	db := setupTestDB(t)
	node := seedReasoned(t, db, "admitRepo",
		graph.DocTag{Kind: graph.TagIntent, Value: "decide which push may trigger a build"},
		graph.DocTag{Kind: graph.TagDomainRule, Value: "only allowlisted repositories are admitted"},
	)
	backend := buildIntentIndex(t, db)
	ctx := requestctx.WithNamespace(t.Context(), requestctx.DefaultNamespace)
	if got := countIntentFTSRows(t, db); got != 2 {
		t.Fatalf("intent index holds %d documents before the update, want 2", got)
	}

	if err := db.Where("kind = ?", string(graph.TagDomainRule)).Delete(&graph.DocTag{}).Error; err != nil {
		t.Fatalf("drop the domain rule: %v", err)
	}
	if _, err := RefreshSearchDocumentsFor(ctx, db, []uint{node.ID}); err != nil {
		t.Fatalf("RefreshSearchDocumentsFor: %v", err)
	}
	if err := backend.RebuildNodes(ctx, db, []uint{node.ID}); err != nil {
		t.Fatalf("RebuildNodes: %v", err)
	}
	if got := countIntentFTSRows(t, db); got != 1 {
		t.Errorf("intent index holds %d documents after the rule was removed, want 1", got)
	}
}
