//go:build fts5

package searchsql

import (
	"slices"
	"testing"

	intentapp "github.com/tae2089/code-context-graph/internal/app/search/intent"
	requestctx "github.com/tae2089/code-context-graph/internal/ctx"
)

// The same corpus, indexed in a different sequence, has to answer the same
// question the same way.
//
// Recorded reasons are one sentence long, so exact score ties are the normal
// case rather than the edge case, and whatever breaks those ties decides the
// answer. Breaking them by node id makes the answer a function of the order the
// rows happened to be written: re-index a repository from a clean checkout, or
// let a webhook rebuild arrive in a different order, and the same question comes
// back with a different top hit. Nobody can act on an answer like that, and
// nobody can measure one either.
//
// The two orders here are the same twelve declarations, seeded ascending and
// descending, so every declaration's id is different between the two databases
// while its name, path and reason are identical.
func TestQueryIntent_SameCorpusSeededEitherWayGivesTheSameAnswer(t *testing.T) {
	const count = 12
	ascending := tiedIntentNames(count)
	descending := slices.Clone(ascending)
	slices.Reverse(descending)

	forward := answerTiedIntent(t, ascending, count)
	backward := answerTiedIntent(t, descending, count)

	if len(forward) != count || len(backward) != count {
		t.Fatalf("got %d and %d answers, want %d each", len(forward), len(backward), count)
	}
	for i := range forward {
		if forward[i] != backward[i] {
			t.Fatalf("row %d is %s when the corpus is seeded ascending and %s when it is seeded descending;"+
				" the answer depends on insertion order", i, forward[i], backward[i])
		}
	}
}

// Asking for one more row has to extend the answer, not reshuffle it — the
// promise the tie-break exists to keep. This is checked against a corpus seeded
// backwards, because a tie-break that happens to agree with insertion order
// keeps that promise for the wrong reason.
func TestQueryIntent_ExtendsRatherThanReshufflesOnABackwardsSeededCorpus(t *testing.T) {
	const count = 12
	descending := slices.Clone(tiedIntentNames(count))
	slices.Reverse(descending)

	short := answerTiedIntent(t, descending, 4)
	long := answerTiedIntent(t, descending, count)

	if len(short) != 4 || len(long) != count {
		t.Fatalf("got %d and %d answers, want 4 and %d", len(short), len(long), count)
	}
	for i, name := range short {
		if long[i] != name {
			t.Fatalf("row %d is %s at limit 4 but %s at limit %d", i, name, long[i], count)
		}
	}
}

// answerTiedIntent seeds one database in the given sequence and returns the
// answer as qualified names, which are the same in every seeding while the ids
// are not.
func answerTiedIntent(t *testing.T, indexes []int, limit int) []string {
	t.Helper()
	db := setupTestDB(t)
	seedTiedIntentNodes(t, db, indexes)
	backend := buildIntentIndex(t, db)
	ctx := requestctx.WithNamespace(t.Context(), requestctx.DefaultNamespace)

	result, err := NewReader(db, backend).QueryIntent(ctx, "what keeps the queue draining", limit)
	if err != nil {
		t.Fatalf("QueryIntent: %v", err)
	}
	return qualifiedNames(result)
}

func qualifiedNames(result intentapp.Result) []string {
	names := make([]string, 0, len(result.Hits))
	for _, hit := range result.Hits {
		names = append(names, hit.Node.QualifiedName)
	}
	return names
}
