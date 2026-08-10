package searchsql

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"testing"

	"gorm.io/gorm"

	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

// The rank-order fixture is a corpus built so that one property decides the
// answer's order: how many times the searched word was written. Every document
// matches the query, every document is padded to the same token count, and no
// node name, file path, or filler word can match the term. What is left is a
// ladder — one document wrote the word ten times, the next nine, down to one —
// and the only right answer is that ladder, best first.
//
// Term frequency is the signal on purpose, because it is the one signal every
// relevance function agrees on. Both deployed ones do: PostgreSQL's ts_rank
// grows with the number of positions recorded for a lexeme, and SQLite's bm25
// grows with the number of matching instances. Swapping in a third ranking
// function does not change the expected order, which is what makes these tests
// a guard on relevance rather than a snapshot of one formula's arithmetic.
const (
	// rankOrderTerm is a nonsense word so nothing in the fixture matches it by
	// accident, and so no node is named it — an exact name match would be
	// promoted to the front and hide the backend's own ordering.
	rankOrderTerm = "reticulator"

	// rankOrderNamespace keeps the fixture out of the way of every other corpus.
	rankOrderNamespace = "rank-order"

	// rankOrderDocs is how many documents the fixture seeds, and therefore how
	// many rungs the ladder has.
	rankOrderDocs = 10

	// rankOrderLimit is the limit the truncated-pool tests query with. It is
	// deliberately smaller than rankOrderDocs, because the SQL carries a LIMIT:
	// reverse the sort and the answer is not merely re-sequenced, the quota fills
	// with the worst matches and the best ones are cut off before anything
	// downstream can rerank them. A fixture that fit inside the limit would only
	// prove the sequence moved.
	rankOrderLimit = 4

	// rankOrderContentTokens is the token count every document is padded to, so
	// document length cannot separate two documents. bm25 divides by length and
	// ts_rank does not; holding it constant is what makes the two backends agree
	// on this fixture instead of disagreeing for a legitimate reason.
	rankOrderContentTokens = 14
)

// rankOrderFiller supplies padding tokens. Greek letter names, because none of
// them is a prefix of the search term or of each other, so padding can never
// turn into a match.
var rankOrderFiller = []string{
	"alpha", "beta", "gamma", "delta", "epsilon", "zeta", "eta",
	"theta", "iota", "kappa", "lambda", "mu", "nu", "xi",
}

// rankOrderDoc is one rung of the ladder: the node to write, and the indexed
// content that decides where the backend should place it.
type rankOrderDoc struct {
	name          string
	qualifiedName string
	filePath      string
	termCount     int
	content       string
}

// rankOrderLadder builds the fixture in the order the backends are expected to
// return it: the document that wrote the term most often comes first.
func rankOrderLadder() []rankOrderDoc {
	ladder := make([]rankOrderDoc, 0, rankOrderDocs)
	for termCount := rankOrderDocs; termCount >= 1; termCount-- {
		tokens := make([]string, 0, rankOrderContentTokens)
		for range termCount {
			tokens = append(tokens, rankOrderTerm)
		}
		for len(tokens) < rankOrderContentTokens {
			tokens = append(tokens, rankOrderFiller[len(tokens)%len(rankOrderFiller)])
		}
		label := strconv.Itoa(termCount)
		if termCount < 10 {
			label = "0" + label
		}
		ladder = append(ladder, rankOrderDoc{
			name:          "Rung" + label,
			qualifiedName: "rankorder.Rung" + label,
			filePath:      "rankorder/rung" + label + ".go",
			termCount:     termCount,
			content:       strings.Join(tokens, " "),
		})
	}
	return ladder
}

// TestRankOrderFixture_IsALadderOfEqualLengthDocuments checks the fixture's own
// premises, because the backend tests are only meaningful while they hold: term
// frequency has to be the single thing separating two documents, the counts have
// to be strictly decreasing so there is exactly one correct order, and there
// have to be more documents than the limit asks for so a reversed order changes
// which rows come back rather than only their sequence.
func TestRankOrderFixture_IsALadderOfEqualLengthDocuments(t *testing.T) {
	ladder := rankOrderLadder()
	if len(ladder) <= rankOrderLimit {
		t.Fatalf("fixture must hold more documents than the limit fetches, got %d for limit %d", len(ladder), rankOrderLimit)
	}
	for i, doc := range ladder {
		if got := len(strings.Fields(doc.content)); got != rankOrderContentTokens {
			t.Errorf("%s: expected %d tokens so length cannot separate documents, got %d", doc.qualifiedName, rankOrderContentTokens, got)
		}
		if got := strings.Count(doc.content, rankOrderTerm); got != doc.termCount {
			t.Errorf("%s: expected the term %d times, content has it %d times", doc.qualifiedName, doc.termCount, got)
		}
		if i > 0 && doc.termCount >= ladder[i-1].termCount {
			t.Errorf("%s: term counts must strictly decrease, %d follows %d", doc.qualifiedName, doc.termCount, ladder[i-1].termCount)
		}
	}
}

// seedRankOrderCorpus writes the ladder into one database as the indexer would —
// a node and the search document derived from it — and then refreshes the
// backend's own index for the fixture namespace.
func seedRankOrderCorpus(t *testing.T, ctx context.Context, db *gorm.DB, backend Backend) {
	t.Helper()
	for _, doc := range rankOrderLadder() {
		node := graph.Node{
			Namespace:     rankOrderNamespace,
			Name:          doc.name,
			QualifiedName: doc.qualifiedName,
			Kind:          graph.NodeKindFunction,
			FilePath:      doc.filePath,
			StartLine:     1,
			EndLine:       2,
			Language:      "go",
		}
		if err := db.Create(&node).Error; err != nil {
			t.Fatalf("seed node %s: %v", doc.qualifiedName, err)
		}
		row := graph.SearchDocument{
			Namespace: rankOrderNamespace,
			NodeID:    node.ID,
			Content:   doc.content,
			Language:  "go",
		}
		if err := db.Create(&row).Error; err != nil {
			t.Fatalf("seed doc %s: %v", doc.qualifiedName, err)
		}
	}
	if err := backend.Rebuild(ctx, db); err != nil {
		t.Fatalf("rebuild index: %v", err)
	}
}

// rankOrderExpected is the answer the ladder demands, best first, cut to the
// same length the query's limit would cut it to.
func rankOrderExpected(limit int) []string {
	ladder := rankOrderLadder()
	if limit > len(ladder) {
		limit = len(ladder)
	}
	want := make([]string, 0, limit)
	for _, doc := range ladder[:limit] {
		want = append(want, doc.qualifiedName)
	}
	return want
}

// queryRankOrder runs the fixture's query against one backend and returns the
// qualified names in the order the backend produced them.
//
// Names rather than scores, because a score is one ranking function's
// arithmetic and the order is the promise. A test reading ts_rank or bm25
// numbers would have to be rewritten the day either is replaced, and would
// still not say whether the good answers came first.
func queryRankOrder(t *testing.T, ctx context.Context, db *gorm.DB, backend Backend, limit int) []string {
	t.Helper()
	nodes, err := backend.Query(ctx, db, rankOrderTerm, limit)
	if err != nil {
		t.Fatalf("query %q: %v", rankOrderTerm, err)
	}
	got := make([]string, 0, len(nodes))
	for _, node := range nodes {
		got = append(got, node.QualifiedName)
	}
	return got
}

// requireRankOrder fails with both sequences spelled out, because the useful
// thing to read from a broken ordering is where the two lists diverge.
func requireRankOrder(t *testing.T, got, want []string) {
	t.Helper()
	if slices.Equal(got, want) {
		return
	}
	t.Errorf("wrong result order\n  want: %s\n  got:  %s", strings.Join(want, " "), strings.Join(got, " "))
}
