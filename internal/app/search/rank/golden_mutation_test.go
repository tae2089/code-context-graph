// This file holds the check on the checkers: it scores every corpus a second
// time with the structural ranker taken out, and requires each of them to
// notice. A corpus that scores the same either way is not guarding anything.
package rank_test

import (
	"context"
	"strconv"
	"testing"

	searchapp "github.com/tae2089/code-context-graph/internal/app/search"
	"github.com/tae2089/code-context-graph/internal/app/search/evidence"
	intentapp "github.com/tae2089/code-context-graph/internal/app/search/intent"
	searchrank "github.com/tae2089/code-context-graph/internal/app/search/rank"
	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

// reranker is the one step this file swaps out: the call
// searchapp.Service.Search makes to rank.Rerank.
type reranker func(query string, nodes []graph.Node, limit int) []graph.Node

// noStructuralRank is the mutant — the whole structural ranker deleted, with
// full-text search's own order handed straight to the evidence cut. This is
// what the ticket describes as "rip the ranker out": nothing reorders the pool,
// so any corpus whose score survives it was never scoring the ranker.
func noStructuralRank(_ string, nodes []graph.Node, _ int) []graph.Node { return nodes }

// searchWith replays one query through the same chain searchapp.Service.Search
// runs — both index fetches, the CanAnswer gate, the intent merge, and the
// evidence cut — with the rerank step supplied by the caller.
//
// It is a copy of the service's body, which is a cost worth naming: production
// code cannot be changed here (the ticket forbids touching the ranker), and
// there is no seam that lets a caller substitute the rerank step. The copy is
// held honest by TestGolden_MutationHarnessMatchesTheService below, which runs
// this with the real ranker and requires the answer to equal the service's, hit
// for hit, on every query of every corpus. If the service grows a step this
// mirror does not have, that test fails.
//
// One difference is deliberate and invisible here: the service orders the pool
// one FetchLimit(Limit) block at a time so a page it already delivered cannot be
// reshuffled by a wider pool, while this calls rerank over the whole pool at
// once. At goldenLimit the block is 50 rows and no fixture pool is longer than
// that, so both take the same single pass. A fixture with more than 50
// candidates for one query would split the service's order and not this one,
// and TestGolden_MutationHarnessMatchesTheService is what would say so.
func searchWith(t *testing.T, rerank reranker, searcher fixtureSearcher, query string) evidence.List {
	t.Helper()
	ctx := context.Background()
	named, err := searcher.Query(ctx, query, searchrank.FetchLimit(goldenLimit))
	if err != nil {
		t.Fatalf("%q: %v", query, err)
	}
	answer, err := searcher.QueryIntent(ctx, query, searchrank.FetchLimit(goldenLimit))
	if err != nil {
		t.Fatalf("%q: %v", query, err)
	}
	hits := answer.Hits
	if !answer.CanAnswer() {
		hits = nil
	}
	merged, intentEvidence := absorbIntentLike(rerank(query, named, 0), hits)
	return evidence.Build(query, merged, evidence.Options{Limit: goldenLimit, Intent: intentEvidence})
}

// absorbIntentLike mirrors the service's unexported absorbIntent: intent hits
// become evidence keyed by node, and a hit the name pool does not already hold
// is appended after it in intent order.
func absorbIntentLike(ranked []graph.Node, hits []intentapp.Hit) ([]graph.Node, map[evidence.NodeRef]evidence.IntentHit) {
	if len(hits) == 0 {
		return ranked, nil
	}
	marks := make(map[evidence.NodeRef]evidence.IntentHit, len(hits))
	present := make(map[evidence.NodeRef]bool, len(ranked))
	for _, n := range ranked {
		present[evidence.NodeRef{Namespace: n.Namespace, ID: n.ID}] = true
	}
	merged := ranked
	for _, h := range hits {
		ref := evidence.NodeRef{Namespace: h.Node.Namespace, ID: h.Node.ID}
		if _, seen := marks[ref]; seen {
			continue
		}
		marks[ref] = evidence.IntentHit{Reason: h.Node.RecordedReason(), Terms: h.Terms}
		if !present[ref] {
			merged = append(merged, h.Node)
			present[ref] = true
		}
	}
	return merged, marks
}

// TestGolden_MutationHarnessMatchesTheService is what makes the copy above
// safe to reason about. With the real ranker plugged in, the mirror must answer
// every golden query exactly as searchapp.Service.Search does.
func TestGolden_MutationHarnessMatchesTheService(t *testing.T) {
	for _, corpus := range goldenCorpora(t) {
		t.Run(corpus.name, func(t *testing.T) {
			set, searcher := loadGolden(t, corpus.dir)
			svc := searchapp.New(searcher)
			for _, q := range set.Queries {
				want, err := svc.Search(context.Background(), searchapp.Params{Query: q.Query, Limit: goldenLimit})
				if err != nil {
					t.Fatalf("%q: %v", q.Query, err)
				}
				got := searchWith(t, searchrank.Rerank, searcher, q.Query)
				if diff := listDiff(want, got); diff != "" {
					t.Errorf("%q: the mutation harness has drifted from the service — %s", q.Query, diff)
				}
			}
		})
	}
}

// listDiff describes the first difference between two answers, or "" when they
// are the same list in the same order with the same evidence.
func listDiff(want, got evidence.List) string {
	if want.WeakFiltered != got.WeakFiltered {
		return "weak_filtered " + strconv.Itoa(want.WeakFiltered) + " != " + strconv.Itoa(got.WeakFiltered)
	}
	if len(want.Files) != len(got.Files) {
		return "file count " + strconv.Itoa(len(want.Files)) + " != " + strconv.Itoa(len(got.Files))
	}
	for i := range want.Files {
		a, b := want.Files[i], got.Files[i]
		if a.FilePath != b.FilePath || a.Namespace != b.Namespace {
			return "file " + strconv.Itoa(i) + " is " + a.FilePath + ", not " + b.FilePath
		}
		if len(a.Hits) != len(b.Hits) {
			return a.FilePath + ": hit count " + strconv.Itoa(len(a.Hits)) + " != " + strconv.Itoa(len(b.Hits))
		}
		for j := range a.Hits {
			if label(a.Hits[j].Node) != label(b.Hits[j].Node) {
				return a.FilePath + ": hit " + strconv.Itoa(j) + " is " + label(a.Hits[j].Node) + ", not " + label(b.Hits[j].Node)
			}
			if a.Hits[j].Reason != b.Hits[j].Reason {
				return label(a.Hits[j].Node) + ": reason differs"
			}
		}
	}
	return ""
}

// TestGolden_EveryCorpusFailsWithoutStructuralRanking is the discrimination
// check the whole anti-overfitting story rests on.
//
// The corpora exist to catch a constant fitted to one codebase's vocabulary.
// That argument only holds if a corpus can tell a good ranking from a bad one.
// The cheapest bad ranking is no ranking at all, so this scores every corpus
// with the structural ranker removed and requires each one to report a
// regression against its own committed baseline. A corpus that still passes is
// not a checkpoint — it is a snapshot taken after the tuning was finished, and
// a maintainer reading "the external corpora passed" learns nothing from it.
//
// Making this green is never done by relaxing what it demands. It is done by
// giving the corpus queries whose answer depends on the order the ranker puts
// the candidate pool in.
func TestGolden_EveryCorpusFailsWithoutStructuralRanking(t *testing.T) {
	for _, corpus := range goldenCorpora(t) {
		t.Run(corpus.name, func(t *testing.T) {
			set, searcher := loadGolden(t, corpus.dir)
			var baseline []outcome
			readJSON(t, corpus.dir+"/baseline.json", &baseline)
			was := make(map[string]outcome, len(baseline))
			for _, b := range baseline {
				was[b.Query] = b
			}

			var regressions []string
			for _, q := range set.Queries {
				prev, ok := was[q.Query]
				if !ok {
					t.Fatalf("%q has no baseline entry; record it with -update-golden", q.Query)
				}
				rel := relevanceOf(q)
				list := searchWith(t, noStructuralRank, searcher, q.Query)
				if why := ratchetViolation(prev, rel, list); why != "" {
					regressions = append(regressions, q.Query+": "+why)
				}
			}

			if len(regressions) == 0 {
				t.Errorf("this corpus scores the same with the structural ranker deleted, so it cannot tell good ranking from none. "+
					"Give it queries whose answer depends on the order of the full-text pool, or judge the ones it has at the depth the ranker actually decides. "+
					"(%d queries replayed against %s/baseline.json)", len(set.Queries), corpus.dir)
				return
			}
			for _, r := range regressions {
				t.Logf("  noticed: %s", r)
			}
		})
	}
}

// ratchetViolation applies the same three rules TestGolden_RankingHasNotRegressed
// enforces — a negative may not grow, the shown list may not lose a relevant
// node, and the first relevant file may not fall — and names the first one this
// run breaks. An empty string means this query would still pass the ratchet.
//
// It restates those rules rather than calling into ratchetCorpus, which reports
// through *testing.T and so cannot be asked "would this have failed?". The two
// are kept in step by TestGolden_MutationRulesMatchTheRatchet below: with the
// real ranker, no query of any corpus may break a rule here, which is exactly
// what the ratchet asserts.
func ratchetViolation(prev outcome, rel relevance, list evidence.List) string {
	returned := len(list.Hits())
	if rel.count() == 0 {
		if returned > prev.Returned {
			return "the answer to a question whose right answer is nothing grew from " + strconv.Itoa(prev.Returned) + " to " + strconv.Itoa(returned) + " results"
		}
		if returned == 0 && prev.Returned > 0 {
			return "a negative that used to return noise now returns nothing"
		}
		return ""
	}
	found, rank := rel.score(list)
	if found < prev.Found {
		return "the list lost relevant nodes — " + strconv.Itoa(found) + " of " + strconv.Itoa(prev.Relevant) + " shown, was " + strconv.Itoa(prev.Found)
	}
	if prev.Rank == 0 {
		return ""
	}
	if rank == 0 {
		return "the first relevant node fell out of the first " + strconv.Itoa(goldenLimit) + " files (was in file " + strconv.Itoa(prev.Rank) + ")"
	}
	if rank > prev.Rank {
		return "the first relevant node fell from file " + strconv.Itoa(prev.Rank) + " to file " + strconv.Itoa(rank)
	}
	return ""
}

// TestGolden_MutationRulesMatchTheRatchet holds ratchetViolation to the same
// standard the shipped ratchet applies: with the real ranker in place, it must
// find nothing to complain about anywhere. If it were stricter than the ratchet
// this fails; if it were looser, the mutation test above would be easier to
// satisfy than the ratchet it claims to speak for.
func TestGolden_MutationRulesMatchTheRatchet(t *testing.T) {
	for _, corpus := range goldenCorpora(t) {
		t.Run(corpus.name, func(t *testing.T) {
			set, searcher := loadGolden(t, corpus.dir)
			var baseline []outcome
			readJSON(t, corpus.dir+"/baseline.json", &baseline)
			was := make(map[string]outcome, len(baseline))
			for _, b := range baseline {
				was[b.Query] = b
			}
			for _, q := range set.Queries {
				prev, ok := was[q.Query]
				if !ok {
					t.Fatalf("%q has no baseline entry; record it with -update-golden", q.Query)
				}
				list := searchWith(t, searchrank.Rerank, searcher, q.Query)
				if why := ratchetViolation(prev, relevanceOf(q), list); why != "" {
					t.Errorf("%q: %s", q.Query, why)
				}
			}
		})
	}
}
