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
	"github.com/tae2089/code-context-graph/internal/app/search/intentrank"
	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

var updateGolden = flag.Bool("update-golden", false, "rewrite each corpus's baseline.json from this run")

// goldenCorpus is one frozen corpus the golden tests replay: a name for the
// subtest and the directory its four fixture files live in.
type goldenCorpus struct {
	name string
	dir  string
}

// goldenCorpora lists every corpus, primary first. The primary set lives in
// testdata/ itself; each extra corpus is a subdirectory of testdata/corpora/,
// named after the codebase it was captured from. The extras exist to catch
// overfitting: a constant tuned to one codebase's vocabulary shows up as a
// regression on a corpus that does not share it.
func goldenCorpora(t *testing.T) []goldenCorpus {
	t.Helper()
	corpora := []goldenCorpus{{name: "ccg", dir: "testdata"}}
	entries, err := os.ReadDir("testdata/corpora")
	if err != nil {
		if os.IsNotExist(err) {
			return corpora
		}
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() {
			corpora = append(corpora, goldenCorpus{name: e.Name(), dir: "testdata/corpora/" + e.Name()})
		}
	}
	return corpora
}

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

// goldenIntentFixture stores each corpus node and indexed reason once, then
// records which reason rows each query matched. Keeping scorer input rather
// than output makes replay exercise the current intentrank.Rank implementation
// without repeating the same corpus text under every query that matched it.
type goldenIntentFixture struct {
	Corpus    int                           `json:"corpus,omitempty"`
	Nodes     map[uint]goldenIntentNode     `json:"nodes,omitempty"`
	Documents map[uint]goldenIntentDocument `json:"documents,omitempty"`
	Queries   map[string][]uint             `json:"queries"`
}

type goldenIntentNode struct {
	Name          string `json:"name"`
	QualifiedName string `json:"qualified_name"`
	Kind          string `json:"kind"`
	FilePath      string `json:"file_path"`
	Namespace     string `json:"namespace,omitempty"`
	StartLine     int    `json:"start_line,omitempty"`
	Intent        string `json:"intent,omitempty"`
	// Reason is what search displays: @intent when present, otherwise the first
	// @domainRule. Content remains the exact indexed reason Rank scores.
	Reason string `json:"reason,omitempty"`
}

type goldenIntentDocument struct {
	NodeID  uint   `json:"node_id"`
	Content string `json:"content"`
}

func (d goldenIntentDocument) rankDoc(node goldenIntentNode) intentrank.Doc {
	return intentrank.Doc{
		NodeID: d.NodeID, Content: d.Content, FilePath: node.FilePath,
		QualifiedName: node.QualifiedName, Kind: graph.NodeKind(node.Kind),
		Namespace: node.Namespace, StartLine: node.StartLine,
	}
}

// intentNodeOf rebuilds the node the intent query hands the service, with enough
// annotation state that RecordedReason reads the captured display reason back.
func intentNodeOf(id uint, c goldenIntentNode) graph.Node {
	n := graph.Node{
		ID:            id,
		Name:          c.Name,
		QualifiedName: c.QualifiedName,
		Kind:          graph.NodeKind(c.Kind),
		FilePath:      c.FilePath,
		StartLine:     c.StartLine,
	}
	tags := make([]graph.DocTag, 0, 2)
	if c.Intent != "" {
		tags = append(tags, graph.DocTag{Kind: graph.TagIntent, Value: c.Intent})
	}
	if c.Reason != "" && c.Reason != c.Intent {
		tags = append(tags, graph.DocTag{Kind: graph.TagDomainRule, Value: c.Reason})
	}
	if len(tags) > 0 {
		n.Annotation = &graph.Annotation{Tags: tags}
	}
	return n
}

// fixtureSearcher answers the service's two fetches from the frozen captures,
// so the only thing that can move a result is the search code itself.
type fixtureSearcher struct {
	named  map[string][]goldenCandidate
	intent goldenIntentFixture
}

func (f fixtureSearcher) Query(_ context.Context, query string, _ int) ([]graph.Node, error) {
	captured := f.named[query]
	nodes := make([]graph.Node, len(captured))
	for i, c := range captured {
		nodes[i] = nodeOf(c)
	}
	return nodes, nil
}

func (f fixtureSearcher) QueryIntent(_ context.Context, query string, limit int) (intentapp.Result, error) {
	refs := f.intent.Queries[query]
	docs := make([]intentrank.Doc, 0, len(refs))
	nodes := make(map[uint]graph.Node, len(refs))
	for _, ref := range refs {
		document := f.intent.Documents[ref]
		node := f.intent.Nodes[document.NodeID]
		docs = append(docs, document.rankDoc(node))
		nodes[document.NodeID] = intentNodeOf(document.NodeID, node)
	}
	ranked := intentrank.Rank(query, docs, f.intent.Corpus, limit)
	hits := make([]intentapp.Hit, 0, len(ranked.Matches))
	for _, match := range ranked.Matches {
		hits = append(hits, intentapp.Hit{Node: nodes[match.NodeID], Terms: match.Terms})
	}
	terms := make([]intentapp.Term, len(ranked.Terms))
	for i, term := range ranked.Terms {
		terms[i] = intentapp.Term{Text: term.Text, InReasons: term.InReasons}
	}
	return intentapp.Result{Hits: hits, Terms: terms, Corpus: ranked.Corpus}, nil
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

func loadGolden(t *testing.T, dir string) (goldenSet, fixtureSearcher) {
	t.Helper()
	var set goldenSet
	readJSON(t, dir+"/queries.json", &set)
	searcher := fixtureSearcher{
		named: map[string][]goldenCandidate{},
	}
	readJSON(t, dir+"/candidates.json", &searcher.named)
	readJSON(t, dir+"/intent_candidates.json", &searcher.intent)
	if err := validateGoldenIntentFixture(searcher.intent); err != nil {
		t.Fatalf("%s/intent_candidates.json: %v", dir, err)
	}
	if err := validateGoldenPoolIdentities(searcher.named, searcher.intent); err != nil {
		t.Fatalf("%s: %v", dir, err)
	}
	for _, q := range set.Queries {
		if _, ok := searcher.named[q.Query]; !ok {
			t.Fatalf("query %q has no captured candidates; re-run the capture", q.Query)
		}
		if _, ok := searcher.intent.Queries[q.Query]; !ok {
			t.Fatalf("query %q has no captured intent candidates; re-run the capture", q.Query)
		}
	}
	return set, searcher
}

type goldenNodeIdentity struct {
	QualifiedName string
	Kind          string
	FilePath      string
}

func validateGoldenPoolIdentities(named map[string][]goldenCandidate, intent goldenIntentFixture) error {
	identities := make(map[uint]goldenNodeIdentity)
	for query, candidates := range named {
		for _, candidate := range candidates {
			got := goldenNodeIdentity{candidate.QualifiedName, candidate.Kind, candidate.FilePath}
			if previous, ok := identities[candidate.ID]; ok && previous != got {
				return fmt.Errorf("named candidates reuse node id %d for different identities at query %q", candidate.ID, query)
			}
			identities[candidate.ID] = got
		}
	}
	for id, node := range intent.Nodes {
		got := goldenNodeIdentity{node.QualifiedName, node.Kind, node.FilePath}
		if previous, ok := identities[id]; ok && previous != got {
			return fmt.Errorf("named and intent candidates give node id %d different identities", id)
		}
	}
	return nil
}

func validateGoldenIntentFixture(fixture goldenIntentFixture) error {
	usedDocuments := make(map[uint]bool, len(fixture.Documents))
	usedNodes := make(map[uint]bool, len(fixture.Nodes))
	for query, refs := range fixture.Queries {
		if !sort.SliceIsSorted(refs, func(i, j int) bool { return refs[i] < refs[j] }) {
			return fmt.Errorf("refs for %q are not in canonical id order", query)
		}
		seen := make(map[uint]bool, len(refs))
		for _, ref := range refs {
			if seen[ref] {
				return fmt.Errorf("refs for %q repeat document id %d", query, ref)
			}
			seen[ref] = true
			document, ok := fixture.Documents[ref]
			if !ok {
				return fmt.Errorf("refs for %q point to missing document id %d", query, ref)
			}
			if _, ok := fixture.Nodes[document.NodeID]; !ok {
				return fmt.Errorf("document id %d points to missing node id %d", ref, document.NodeID)
			}
			usedDocuments[ref] = true
			usedNodes[document.NodeID] = true
		}
	}
	for id := range fixture.Documents {
		if !usedDocuments[id] {
			return fmt.Errorf("document id %d is unreachable from every query", id)
		}
	}
	for id := range fixture.Nodes {
		if !usedNodes[id] {
			return fmt.Errorf("node id %d is unreachable from every query", id)
		}
	}
	return nil
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
func (rel relevance) retrieved(named []goldenCandidate, intent goldenIntentFixture, query string) bool {
	for _, c := range named {
		if rel.nodes[label(nodeOf(c))] || rel.files[c.FilePath] {
			return true
		}
	}
	for _, ref := range intent.Queries[query] {
		document := intent.Documents[ref]
		node := intent.Nodes[document.NodeID]
		if rel.nodes[label(intentNodeOf(document.NodeID, node))] || rel.files[node.FilePath] {
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
func runGolden(t *testing.T, dir string) []outcome {
	t.Helper()
	set, searcher := loadGolden(t, dir)
	svc := searchapp.New(searcher)
	outcomes := make([]outcome, 0, len(set.Queries))
	for _, q := range set.Queries {
		rel := relevanceOf(q)
		result := outcome{
			Query:      q.Query,
			Bucket:     q.Bucket,
			Negative:   rel.count() == 0,
			OutOfScope: q.declinedBy("search"),
			Retrieved:  rel.retrieved(searcher.named[q.Query], searcher.intent, q.Query),
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
	for _, corpus := range goldenCorpora(t) {
		t.Run(corpus.name, func(t *testing.T) {
			ratchetCorpus(t, corpus)
		})
	}
}

func ratchetCorpus(t *testing.T, corpus goldenCorpus) {
	outcomes := runGolden(t, corpus.dir)

	if *updateGolden {
		blob, err := json.MarshalIndent(outcomes, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(corpus.dir+"/baseline.json", append(blob, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("%s/baseline.json rewritten", corpus.dir)
		return
	}

	var baseline []outcome
	readJSON(t, corpus.dir+"/baseline.json", &baseline)
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
	for _, corpus := range goldenCorpora(t) {
		t.Run(corpus.name, func(t *testing.T) {
			evidenceCutCorpus(t, corpus)
		})
	}
}

func evidenceCutCorpus(t *testing.T, corpus goldenCorpus) {
	set, searcher := loadGolden(t, corpus.dir)
	knownHidden := knownHiddenRelevant[corpus.name]
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
			if _, known := knownHidden[q.Query+" | "+name]; known {
				continue
			}
			t.Errorf("%q: the evidence cut hid a relevant node — %s has nothing in its name, path, or @intent to match the query", q.Query, name)
		}
		for key := range knownHidden {
			name, ok := strings.CutPrefix(key, q.Query+" | ")
			if ok && shown[name] {
				t.Errorf("%q: %s is no longer hidden; drop it from knownHiddenRelevant", q.Query, name)
			}
		}
	}
}

// knownHiddenRelevant lists, per corpus, the relevant nodes the evidence cut
// drops today, each with the reason it is accepted rather than fixed. Two on
// the primary corpus, out of 39 answerable queries — that is the whole price
// of the cut, measured.
//
// It was two. The other was flow.Tracer.TraceFlow on the query "tracer", hidden
// because nameSim could not see a method's receiver type; that was a gap in the
// ranker, not in the cut, and receiverSegment closed it.
//
// An entry is a debt, not a permission: whoever fixes one deletes its line, and
// the test above fails if a listed node starts showing, so the list cannot rot
// into a silent excuse.
var knownHiddenRelevant = map[string]map[string]string{
	"ccg": {
		"fts | file:internal/adapters/outbound/searchsql/sqlite.go@internal/adapters/outbound/searchsql/sqlite.go": "a file node's only surface is its path, and 'fts' is nowhere in internal/adapters/outbound/searchsql/sqlite.go — the acronym lives in the declarations inside it. The reader loses nothing: searchsql.ftsRow is shown and carries this exact file, so the file is on the page under a hit that can explain itself. This entry only became visible when paging moved to files and the with-weak run started reaching this far.",
		"worker pool | function:workflow.Service.parseBuildInputs@internal/app/ingest/workflow/build.go":           "the judgment for this query says outright that the node was chosen by reading build.go, not from anything on its surface: nothing in its name, path, or @intent says 'worker pool'. The cut is doing what it was built to do, and the sibling answer reposync.SyncQueue — whose @intent does say it — is still shown.",
	},
	"cobra": {
		"levenshtein | function:cobra.ld@cobra.go": "the first cross-corpus finding, recorded rather than tuned away: cobra.ld is retrieved through its docstring, but the cut justifies a hit on name, path, and @intent only, and an annotation-free corpus has no @intent to speak with. A docstring-retrieved hit on such a corpus therefore dies at the cut. Fixing it means teaching the cut a docstring-match signal, which is a design change to make deliberately, not a constant to nudge here.",
	},
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
	for _, corpus := range goldenCorpora(t) {
		t.Run(corpus.name, func(t *testing.T) {
			reportCorpus(t, corpus)
		})
	}
}

func reportCorpus(t *testing.T, corpus goldenCorpus) {
	outcomes := runGolden(t, corpus.dir)

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
