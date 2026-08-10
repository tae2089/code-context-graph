// The golden harness lives in the external test package because it replays the
// list search actually returns — the whole service pipeline, including the
// intent merge and the evidence cut — and the packages that make it up import
// this one, so an in-package test could not reach them.
package rank_test

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"
	"testing"

	searchapp "github.com/tae2089/code-context-graph/internal/app/search"
	"github.com/tae2089/code-context-graph/internal/app/search/evidence"
	intentapp "github.com/tae2089/code-context-graph/internal/app/search/intent"
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
	Query  string `json:"query"`
	Bucket string `json:"bucket"`
	// Relevant judges at node granularity, as kind:qualified@path labels.
	Relevant []string `json:"relevant"`
	// RelevantFiles judges at file granularity. The questions migrated from the
	// intent golden set were judged as "somewhere the reader can start walking",
	// and the file is the unit a reader picks from, so re-judging them down to
	// declarations would invent precision the judgment never had.
	RelevantFiles []string `json:"relevant_files,omitempty"`
	Why           string   `json:"why"`
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

// goldenIntentAnswer is everything the intent index said about one golden
// query, as captured through the production intent query path: the ranked hits,
// every scored term with its reason count, and the corpus size. The terms are
// captured because membership is gated on them — a replay without them would
// score a search that thinks every question is answerable.
type goldenIntentAnswer struct {
	Corpus int                `json:"corpus,omitempty"`
	Terms  []goldenIntentTerm `json:"terms,omitempty"`
	Hits   []goldenIntentHit  `json:"hits,omitempty"`
}

// goldenIntentTerm is one scored term of the question and how many recorded
// reasons in the whole index hold it.
type goldenIntentTerm struct {
	Text      string `json:"text"`
	InReasons int    `json:"in_reasons"`
}

// goldenIntentHit is one candidate the intent index answered a golden query
// with, as captured through the production intent query path.
type goldenIntentHit struct {
	ID            uint   `json:"id"`
	Name          string `json:"name"`
	QualifiedName string `json:"qualified_name"`
	Kind          string `json:"kind"`
	FilePath      string `json:"file_path"`
	Intent        string `json:"intent,omitempty"`
	// Reason is the recorded reason the index matched — the @intent, or the
	// @domainRule when the node has no @intent of its own.
	Reason string `json:"reason,omitempty"`
	// Terms are the query terms the intent scorer counted in Reason.
	Terms []string `json:"terms,omitempty"`
}

// intentHitOf rebuilds the hit the intent query hands the service, with enough
// of the annotation restored that RecordedReason reads the captured reason back.
func intentHitOf(h goldenIntentHit) intentapp.Hit {
	n := graph.Node{
		ID:            h.ID,
		Name:          h.Name,
		QualifiedName: h.QualifiedName,
		Kind:          graph.NodeKind(h.Kind),
		FilePath:      h.FilePath,
	}
	tags := make([]graph.DocTag, 0, 2)
	if h.Intent != "" {
		tags = append(tags, graph.DocTag{Kind: graph.TagIntent, Value: h.Intent})
	}
	if h.Reason != "" && h.Reason != h.Intent {
		tags = append(tags, graph.DocTag{Kind: graph.TagDomainRule, Value: h.Reason})
	}
	if len(tags) > 0 {
		n.Annotation = &graph.Annotation{Tags: tags}
	}
	return intentapp.Hit{Node: n, Terms: h.Terms}
}

// fixtureSearcher answers the service's two fetches from the frozen captures,
// so the only thing that can move a result is the search code itself.
type fixtureSearcher struct {
	named  map[string][]goldenCandidate
	intent map[string]goldenIntentAnswer
}

func (f fixtureSearcher) Query(_ context.Context, query string, _ int) ([]graph.Node, error) {
	captured := f.named[query]
	nodes := make([]graph.Node, len(captured))
	for i, c := range captured {
		nodes[i] = nodeOf(c)
	}
	return nodes, nil
}

func (f fixtureSearcher) QueryIntent(_ context.Context, query string, _ int) (intentapp.Result, error) {
	captured := f.intent[query]
	hits := make([]intentapp.Hit, len(captured.Hits))
	for i, h := range captured.Hits {
		hits[i] = intentHitOf(h)
	}
	terms := make([]intentapp.Term, len(captured.Terms))
	for i, term := range captured.Terms {
		terms[i] = intentapp.Term{Text: term.Text, InReasons: term.InReasons}
	}
	return intentapp.Result{Hits: hits, Terms: terms, Corpus: captured.Corpus}, nil
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

func label(n graph.Node) string {
	return string(n.Kind) + ":" + n.QualifiedName + "@" + n.FilePath
}

func loadGolden(t *testing.T) (goldenSet, fixtureSearcher) {
	t.Helper()
	var set goldenSet
	readJSON(t, "testdata/queries.json", &set)
	searcher := fixtureSearcher{
		named:  map[string][]goldenCandidate{},
		intent: map[string]goldenIntentAnswer{},
	}
	readJSON(t, "testdata/candidates.json", &searcher.named)
	readJSON(t, "testdata/intent_candidates.json", &searcher.intent)
	for _, q := range set.Queries {
		if _, ok := searcher.named[q.Query]; !ok {
			t.Fatalf("query %q has no captured candidates; re-run the capture", q.Query)
		}
		if _, ok := searcher.intent[q.Query]; !ok {
			t.Fatalf("query %q has no captured intent candidates; re-run the capture", q.Query)
		}
	}
	return set, searcher
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

// relevance is one query's judgment, at whichever granularity it was made.
type relevance struct {
	nodes map[string]bool // kind:qualified@path labels
	files map[string]bool // file paths
}

func relevanceOf(q goldenQuery) relevance {
	rel := relevance{nodes: map[string]bool{}, files: map[string]bool{}}
	for _, r := range q.Relevant {
		rel.nodes[r] = true
	}
	for _, f := range q.RelevantFiles {
		rel.files[f] = true
	}
	return rel
}

func (rel relevance) count() int { return len(rel.nodes) + len(rel.files) }

// retrieved reports whether any judged node or file is anywhere in either
// captured pool — the ceiling no ranking or filtering change can lift.
func (rel relevance) retrieved(named []goldenCandidate, intent goldenIntentAnswer) bool {
	for _, c := range named {
		if rel.nodes[label(nodeOf(c))] || rel.files[c.FilePath] {
			return true
		}
	}
	for _, h := range intent.Hits {
		if rel.nodes[label(intentHitOf(h).Node)] || rel.files[h.FilePath] {
			return true
		}
	}
	return false
}

// score counts the judged nodes and files the shown list contains, and the
// 1-based file position of the first. A judged file counts once however many
// hits it answered with; a judged node counts wherever its label appears.
func (rel relevance) score(list evidence.List) (found, rank int) {
	for i, f := range list.Files {
		fileHit := rel.files[f.FilePath]
		if fileHit {
			found++
		}
		for _, r := range f.Hits {
			if rel.nodes[label(r.Node)] {
				found++
				fileHit = true
			}
		}
		if fileHit && rank == 0 {
			rank = i + 1
		}
	}
	return found, rank
}

// runGolden replays every golden query through the production pipeline — both
// index fetches, the intent merge, the rerank, and the evidence cut — against
// its frozen candidate lists. No database is opened, so the only thing that can
// move a result is the search code itself.
func runGolden(t *testing.T) []outcome {
	t.Helper()
	set, searcher := loadGolden(t)
	svc := searchapp.New(searcher)
	outcomes := make([]outcome, 0, len(set.Queries))
	for _, q := range set.Queries {
		rel := relevanceOf(q)
		result := outcome{
			Query:      q.Query,
			Bucket:     q.Bucket,
			Negative:   rel.count() == 0,
			OutOfScope: q.declinedBy("search"),
			Retrieved:  rel.retrieved(searcher.named[q.Query], searcher.intent[q.Query]),
			Relevant:   rel.count(),
		}
		list, err := svc.Search(context.Background(), searchapp.Params{Query: q.Query, Limit: goldenLimit})
		if err != nil {
			t.Fatalf("%q: %v", q.Query, err)
		}
		result.Returned = len(list.Hits())
		result.WeakFiltered = list.WeakFiltered
		result.Found, result.Rank = rel.score(list)
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
			// The right answer is nothing. Some negatives still come back with
			// intent hits, because their words are ordinary codebase vocabulary
			// and the reasons "speak their language" without answering them —
			// the same debt the retired intent golden set carried. The baseline
			// holds the measured count rather than asserting zero, so growth
			// fails, and a negative that reaches zero must be recorded so the
			// improvement cannot silently regress.
			if got.Returned > prev.Returned {
				t.Errorf("%q: nothing about this is recorded and the answer grew from %d to %d results", got.Query, prev.Returned, got.Returned)
			}
			if got.Returned == 0 && prev.Returned > 0 {
				t.Errorf("%q: now answers with nothing, as it should — record it with -update-golden", got.Query)
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
	set, searcher := loadGolden(t)
	svc := searchapp.New(searcher)
	for _, q := range set.Queries {
		rel := relevanceOf(q)
		if rel.count() == 0 {
			continue
		}

		asShipped, err := svc.Search(context.Background(), searchapp.Params{Query: q.Query, Limit: goldenLimit})
		if err != nil {
			t.Fatalf("%q: %v", q.Query, err)
		}
		weakKept, err := svc.Search(context.Background(), searchapp.Params{Query: q.Query, Limit: goldenLimit, IncludeWeak: true})
		if err != nil {
			t.Fatalf("%q: %v", q.Query, err)
		}
		shown := shownRelevant(asShipped, rel)
		withWeak := shownRelevant(weakKept, rel)

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

// shownRelevant collects the judged nodes and files this list shows, each under
// the name its judgment used — a node label, or a bare file path.
func shownRelevant(list evidence.List, rel relevance) map[string]bool {
	out := map[string]bool{}
	for _, f := range list.Files {
		if rel.files[f.FilePath] {
			out[f.FilePath] = true
		}
		for _, r := range f.Hits {
			if name := label(r.Node); rel.nodes[name] {
				out[name] = true
			}
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
		case o.Negative && o.Returned > 0:
			t.Logf("  negative noise: %-28q (%s) — %d results for a question whose right answer is nothing", o.Query, o.Bucket, o.Returned)
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
