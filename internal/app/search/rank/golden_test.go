// The golden harness lives in the external test package because it replays the
// list search actually returns, which means it goes through the evidence cut —
// and the evidence package imports this one, so an in-package test could not
// reach it.
package rank_test

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/tae2089/code-context-graph/internal/app/search/evidence"
	"github.com/tae2089/code-context-graph/internal/app/search/rank"
	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

var updateGolden = flag.Bool("update-golden", false, "rewrite testdata/baseline.json from this run")

// goldenLimit is the number of files the golden run asks for. It matches the
// limit used when candidates.json was captured, so the candidate pool the ranker
// sees here is the pool production would hand it for the same request.
//
// It counts files, not hits, because that is what a search answer is bounded by:
// a shown file arrives whole. So a run can return far more than ten hits, and
// Recall below is "of the relevant nodes, how many were on the page".
const goldenLimit = 10

type goldenQuery struct {
	Query    string   `json:"query"`
	Bucket   string   `json:"bucket"`
	Relevant []string `json:"relevant"`
	Why      string   `json:"why"`
	// OutOfScope names the tools that have decided not to answer this query, so
	// their zero is a recorded decision rather than a defect. Without it those
	// queries sink the headline average and nobody reading it can tell how much
	// of the gap is code and how much is policy. The query stays in the set, and
	// stays red for the tools listed, so anyone reversing the decision inherits
	// the measurements in `why`.
	//
	// It is a list because the two tools decline different things. `search`
	// declines whole questions — measured at 0.200 MRR against `wiki_search`'s
	// 0.600 on the same five — while `wiki_search` answers them and is scored on
	// them. Both decline typos and abbreviations.
	OutOfScope []string `json:"out_of_scope,omitempty"`
}

// declinedBy reports whether the named tool has decided not to answer.
func (q goldenQuery) declinedBy(tool string) bool {
	return slices.Contains(q.OutOfScope, tool)
}

type goldenSet struct {
	Queries []goldenQuery `json:"queries"`
}

type goldenCandidate struct {
	ID            uint   `json:"id"`
	Name          string `json:"name"`
	QualifiedName string `json:"qualified_name"`
	Kind          string `json:"kind"`
	FilePath      string `json:"file_path"`
	// Intent is the node's @intent tag as captured. Search shows it as evidence
	// and keeps a node whose intent shares a word with the query, so a replay
	// without it would score a search that has no annotations.
	Intent string `json:"intent,omitempty"`
}

// nodeOf rebuilds the node the ranker and the evidence cut are handed, with the
// captured @intent hung off it the way the search backend's preload does.
func nodeOf(c goldenCandidate) graph.Node {
	n := graph.Node{
		ID:            c.ID,
		Name:          c.Name,
		QualifiedName: c.QualifiedName,
		Kind:          graph.NodeKind(c.Kind),
		FilePath:      c.FilePath,
	}
	if c.Intent != "" {
		n.Annotation = &graph.Annotation{Tags: []graph.DocTag{{Kind: graph.TagIntent, Value: c.Intent}}}
	}
	return n
}

// outcome is one query's result, and the unit the baseline compares.
//
// Retrieved, Found and Rank answer three different questions that a single
// number would confuse. Retrieved asks whether full-text search returned any
// relevant node at all; when it is false nothing downstream was ever given the
// chance, and no ranking or filtering change can fix that query. Found asks how
// many of the query's relevant nodes survived into the list a reader sees —
// this is the headline, because the list is evidence to judge from, and a
// reader who reads all ten lines is unaffected by which one is third. Rank asks
// where the first relevant node landed, and is kept only as a regression guard.
//
// A query with no relevant nodes is a negative case: the right answer is an
// empty result, so Retrieved=false and Rank=0 mean success there, not failure.
// Returned carries the raw result count so a negative case can still fail.
type outcome struct {
	Query      string `json:"query"`
	Bucket     string `json:"bucket"`
	Negative   bool   `json:"negative,omitempty"`
	OutOfScope bool   `json:"out_of_scope,omitempty"`
	Retrieved  bool   `json:"retrieved"`
	Returned   int    `json:"returned"`
	// Relevant is how many nodes the golden judgment calls relevant, and Found
	// how many of them the shown list contains. Found/Relevant is Recall@10.
	Relevant int `json:"relevant"`
	Found    int `json:"found"`
	// Rank is the 1-based position of the file holding the first relevant node,
	// counted among the files this page shows; 0 means no relevant node is on
	// the page. It moved from hits to files when the answer did: a hit's index
	// in the flattened sequence now depends on how dense the files above it are,
	// which is not a ranking property and would make this a noisy guard.
	Rank int `json:"rank"`
	// WeakFiltered is how many candidates the evidence cut dropped for having
	// nothing in their name, path, or @intent to justify them.
	WeakFiltered int `json:"weak_filtered,omitempty"`
}

func label(c goldenCandidate) string {
	return c.Kind + ":" + c.QualifiedName + "@" + c.FilePath
}

func loadGolden(t *testing.T) (goldenSet, map[string][]goldenCandidate) {
	t.Helper()
	var set goldenSet
	readJSON(t, "testdata/queries.json", &set)
	candidates := map[string][]goldenCandidate{}
	readJSON(t, "testdata/candidates.json", &candidates)
	for _, q := range set.Queries {
		if _, ok := candidates[q.Query]; !ok {
			t.Fatalf("query %q has no captured candidates; re-run the capture", q.Query)
		}
	}
	return set, candidates
}

func readJSON(t *testing.T, path string, into any) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, into); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
}

// runGolden replays every golden query through the same two steps production
// runs — rank, then cut to what can be justified — against its frozen candidate
// list. No database is opened, so the only thing that can move a result is the
// ranking or evidence code itself.
func runGolden(t *testing.T) []outcome {
	t.Helper()
	set, candidates := loadGolden(t)
	outcomes := make([]outcome, 0, len(set.Queries))
	for _, q := range set.Queries {
		captured := candidates[q.Query]
		nodes := make([]graph.Node, len(captured))
		for i, c := range captured {
			nodes[i] = nodeOf(c)
		}

		relevant := make(map[string]bool, len(q.Relevant))
		for _, r := range q.Relevant {
			relevant[r] = true
		}
		byID := make(map[uint]goldenCandidate, len(captured))
		retrieved := false
		for _, c := range captured {
			byID[c.ID] = c
			if relevant[label(c)] {
				retrieved = true
			}
		}

		result := outcome{
			Query:      q.Query,
			Bucket:     q.Bucket,
			Negative:   len(q.Relevant) == 0,
			OutOfScope: q.declinedBy("search"),
			Retrieved:  retrieved,
			Relevant:   len(q.Relevant),
		}
		// Rerank the whole pool and let the evidence cut bound it, exactly as
		// the CLI and the MCP handler do.
		list := evidence.Build(q.Query, rank.Rerank(q.Query, nodes, 0), evidence.Options{Limit: goldenLimit})
		result.Returned = len(list.Hits())
		result.WeakFiltered = list.WeakFiltered
		for i, f := range list.Files {
			for _, r := range f.Hits {
				if !relevant[label(byID[r.Node.ID])] {
					continue
				}
				result.Found++
				if result.Rank == 0 {
					result.Rank = i + 1
				}
			}
		}
		outcomes = append(outcomes, result)
	}
	return outcomes
}

// TestGolden_RankingHasNotRegressed compares this build against the committed
// baseline query by query. It is a ratchet, not a quality bar: it cannot show
// the ranking is good, only that a change made a specific query worse. The
// judgments in queries.json were written by the same author as the ranker, so
// treating an improvement here as proof of quality would be circular.
//
// A query that legitimately changes is resolved by re-reading its "why" in
// queries.json and either fixing the code or recording the new baseline with
// -update-golden — never by relaxing the judgment to make the run pass.
func TestGolden_RankingHasNotRegressed(t *testing.T) {
	outcomes := runGolden(t)

	if *updateGolden {
		blob, err := json.MarshalIndent(outcomes, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile("testdata/baseline.json", append(blob, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Log("baseline.json rewritten")
		return
	}

	var baseline []outcome
	readJSON(t, "testdata/baseline.json", &baseline)
	want := make(map[string]outcome, len(baseline))
	for _, b := range baseline {
		want[b.Query] = b
	}

	for _, got := range outcomes {
		prev, ok := want[got.Query]
		if !ok {
			t.Errorf("%q is new; record it with -update-golden", got.Query)
			continue
		}
		if got.Negative {
			if got.Returned != 0 {
				t.Errorf("%q: nothing should match, got %d results", got.Query, got.Returned)
			}
			continue
		}
		if got.Retrieved != prev.Retrieved {
			t.Errorf("%q: full-text search retrieval changed (was retrieved=%v, now %v); this is a retrieval change, not a ranking one — recapture candidates.json",
				got.Query, prev.Retrieved, got.Retrieved)
			continue
		}
		// The headline guard: the shown list may not lose a relevant node it
		// used to contain. This is what the search is for.
		if got.Found < prev.Found {
			t.Errorf("%q: the list lost relevant nodes — %d of %d shown, was %d", got.Query, got.Found, got.Relevant, prev.Found)
		}
		if prev.Rank == 0 {
			continue // nothing to lose; an improvement is recorded, not enforced
		}
		if got.Rank == 0 {
			t.Errorf("%q: first relevant node fell out of the first %d files (was in file %d)", got.Query, goldenLimit, prev.Rank)
			continue
		}
		if got.Rank > prev.Rank {
			t.Errorf("%q: first relevant node fell from file %d to file %d", got.Query, prev.Rank, got.Rank)
		}
	}
}

// TestGolden_EvidenceCutHidesNoRelevantNode is the price check on the cut.
//
// Dropping candidates nothing can justify is only worth doing if it drops junk.
// This replays every query twice — once as it ships, once with the weak
// candidates kept — and fails if keeping them would have shown a relevant node
// the shipped list does not. A failure here is not automatically a bug in the
// cut: it can equally mean the node's @intent is missing or stale. Either way
// somebody has to look, which is the point.
func TestGolden_EvidenceCutHidesNoRelevantNode(t *testing.T) {
	set, candidates := loadGolden(t)
	for _, q := range set.Queries {
		if len(q.Relevant) == 0 {
			continue
		}
		relevant := make(map[string]bool, len(q.Relevant))
		for _, r := range q.Relevant {
			relevant[r] = true
		}
		captured := candidates[q.Query]
		nodes := make([]graph.Node, len(captured))
		byID := make(map[uint]goldenCandidate, len(captured))
		for i, c := range captured {
			nodes[i] = nodeOf(c)
			byID[c.ID] = c
		}

		ranked := rank.Rerank(q.Query, nodes, 0)
		shown := shownRelevant(evidence.Build(q.Query, ranked, evidence.Options{Limit: goldenLimit}), relevant, byID)
		withWeak := shownRelevant(evidence.Build(q.Query, ranked, evidence.Options{Limit: goldenLimit, IncludeWeak: true}), relevant, byID)

		for name := range withWeak {
			if shown[name] {
				continue
			}
			if _, known := knownHiddenRelevant[q.Query+" | "+name]; known {
				continue
			}
			t.Errorf("%q: the evidence cut hid a relevant node — %s has nothing in its name, path, or @intent to match the query", q.Query, name)
		}
		for key := range knownHiddenRelevant {
			name, ok := strings.CutPrefix(key, q.Query+" | ")
			if ok && shown[name] {
				t.Errorf("%q: %s is no longer hidden; drop it from knownHiddenRelevant", q.Query, name)
			}
		}
	}
}

// knownHiddenRelevant lists the relevant nodes the evidence cut drops today,
// each with the reason it is accepted rather than fixed. Two, out of 39
// answerable queries — that is the whole price of the cut, measured.
//
// It was two. The other was flow.Tracer.TraceFlow on the query "tracer", hidden
// because nameSim could not see a method's receiver type; that was a gap in the
// ranker, not in the cut, and receiverSegment closed it.
//
// An entry is a debt, not a permission: whoever fixes one deletes its line, and
// the test above fails if a listed node starts showing, so the list cannot rot
// into a silent excuse.
var knownHiddenRelevant = map[string]string{
	"fts | file:internal/adapters/outbound/searchsql/sqlite.go@internal/adapters/outbound/searchsql/sqlite.go": "a file node's only surface is its path, and 'fts' is nowhere in internal/adapters/outbound/searchsql/sqlite.go — the acronym lives in the declarations inside it. The reader loses nothing: searchsql.ftsRow is shown and carries this exact file, so the file is on the page under a hit that can explain itself. This entry only became visible when paging moved to files and the with-weak run started reaching this far.",
	"worker pool | function:workflow.Service.parseBuildInputs@internal/app/ingest/workflow/build.go":           "the judgment for this query says outright that the node was chosen by reading build.go, not from anything on its surface: nothing in its name, path, or @intent says 'worker pool'. The cut is doing what it was built to do, and the sibling answer reposync.SyncQueue — whose @intent does say it — is still shown.",
}

func shownRelevant(list evidence.List, relevant map[string]bool, byID map[uint]goldenCandidate) map[string]bool {
	out := map[string]bool{}
	for _, r := range list.Hits() {
		if name := label(byID[r.Node.ID]); relevant[name] {
			out[name] = true
		}
	}
	return out
}

// TestGolden_Report prints the per-bucket scoreboard. It asserts nothing: the
// numbers are for reading, and the ratchet above is what fails a build.
// Run with `go test -run TestGolden_Report -v`.
func TestGolden_Report(t *testing.T) {
	outcomes := runGolden(t)

	type tally struct {
		n, retrieved, top1, top3 int
		relevant, found          int
		rr                       float64
	}
	buckets := map[string]*tally{}
	total := &tally{}
	answerable := &tally{}
	for _, o := range outcomes {
		if o.Negative {
			continue // an empty result is the right answer; averaging it in would be meaningless
		}
		b, ok := buckets[o.Bucket]
		if !ok {
			b = &tally{}
			buckets[o.Bucket] = b
		}
		accs := []*tally{b, total}
		if !o.OutOfScope {
			accs = append(accs, answerable)
		}
		for _, acc := range accs {
			acc.n++
			acc.relevant += o.Relevant
			acc.found += o.Found
			if o.Retrieved {
				acc.retrieved++
			}
			if o.Rank > 0 {
				acc.rr += 1 / float64(o.Rank)
				if o.Rank == 1 {
					acc.top1++
				}
				if o.Rank <= 3 {
					acc.top3++
				}
			}
		}
	}

	names := make([]string, 0, len(buckets))
	for name := range buckets {
		names = append(names, name)
	}
	sort.Strings(names)

	// Recall@10 leads because the result list is meant to be read whole and
	// judged by the reader. MRR and top1 follow as regression guards: they say
	// whether an ordering change quietly buried an answer that used to be first.
	t.Log("bucket           n  retrieved  Recall@10   top1  top3   MRR")
	line := func(name string, acc *tally) string {
		recall := 0.0
		if acc.relevant > 0 {
			recall = float64(acc.found) / float64(acc.relevant)
		}
		return fmt.Sprintf("%-15s %2d   %2d/%-2d      %.3f (%2d/%-3d)  %2d    %2d  %.3f",
			name, acc.n, acc.retrieved, acc.n, recall, acc.found, acc.relevant, acc.top1, acc.top3, acc.rr/float64(acc.n))
	}
	for _, name := range names {
		t.Log(line(name, buckets[name]))
	}
	t.Log(line("ALL", total))
	// ALL carries queries search has decided not to answer, so it can never
	// reach 1.0 however good the ranking gets. ANSWERABLE drops those, and is
	// the number to read when asking how the code is doing.
	t.Log(line("ANSWERABLE", answerable))

	for _, o := range outcomes {
		switch {
		case o.Negative:
		case o.OutOfScope:
			t.Logf("  out of scope:   %-28q (%s) — answering it was decided against, not missed", o.Query, o.Bucket)
		case !o.Retrieved:
			t.Logf("  retrieval miss: %-28q (%s) — full-text search returned nothing relevant", o.Query, o.Bucket)
		case o.Found == 0:
			t.Logf("  shown none:     %-28q (%s) — relevant node retrieved but not in the shown list", o.Query, o.Bucket)
		case o.Found < o.Relevant:
			t.Logf("  partial:        %-28q (%s) — %d of %d relevant nodes shown", o.Query, o.Bucket, o.Found, o.Relevant)
		case o.Rank > 3:
			t.Logf("  buried:         %-28q (%s) — first relevant node at rank %d", o.Query, o.Bucket, o.Rank)
		}
	}
}
