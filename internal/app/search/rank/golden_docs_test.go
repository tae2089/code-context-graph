// The `wiki_search` half of the golden set: the Wiki web UI's search box.
//
// It lives beside the ranking harness, in the same package and reading the same
// queries.json, because the two tools answer the same questions from the same
// graph and a reader comparing them has to be sure the questions did not move.
// It replays retrieval.FromDB — the exact call the Wiki web server makes — against
// a frozen fixture, so no database is opened here either.
package rank_test

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/tae2089/code-context-graph/internal/app/search/retrieval"
	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

var updateDocsGolden = flag.Bool("update-docs-golden", false, "rewrite testdata/baseline_docs.json from this run")

// docsProbeLimit asks for far more files than a caller ever would, to answer a
// different question from the scored run: could this query reach the answer at
// all. Its full-text half is bounded by the 100 candidates the fixture holds, so
// it under-reports reachability for a query whose answer sits past that — the
// scan half, which is the whole namespace, is not bounded.
const docsProbeLimit = 500

type docsFixture struct {
	Pool []docsNode        `json:"pool"`
	FTS  map[string][]uint `json:"fts"`
}

type docsNode struct {
	ID            uint            `json:"id"`
	Name          string          `json:"name"`
	QualifiedName string          `json:"qualified_name"`
	Kind          string          `json:"kind"`
	FilePath      string          `json:"file_path"`
	Annotation    *docsAnnotation `json:"annotation,omitempty"`
}

type docsAnnotation struct {
	Summary string       `json:"summary,omitempty"`
	Context string       `json:"context,omitempty"`
	RawText string       `json:"raw_text,omitempty"`
	Tags    []docsDocTag `json:"tags,omitempty"`
}

type docsDocTag struct {
	Kind  string `json:"kind"`
	Name  string `json:"name,omitempty"`
	Value string `json:"value,omitempty"`
}

func (n docsNode) node() graph.Node {
	out := graph.Node{
		ID:            n.ID,
		Name:          n.Name,
		QualifiedName: n.QualifiedName,
		Kind:          graph.NodeKind(n.Kind),
		FilePath:      n.FilePath,
	}
	if n.Annotation == nil {
		return out
	}
	ann := &graph.Annotation{
		NodeID:  n.ID,
		Summary: n.Annotation.Summary,
		Context: n.Annotation.Context,
		RawText: n.Annotation.RawText,
	}
	for _, tag := range n.Annotation.Tags {
		ann.Tags = append(ann.Tags, graph.DocTag{Kind: graph.TagKind(tag.Kind), Name: tag.Name, Value: tag.Value})
	}
	out.Annotation = ann
	return out
}

// docsFixtureRepo serves the frozen namespace snapshot in place of the database.
type docsFixtureRepo struct {
	t    *testing.T
	pool []graph.Node
}

func (r docsFixtureRepo) ScanCandidates(_ context.Context, kinds []graph.NodeKind, limit int) ([]graph.Node, error) {
	if len(r.pool) >= limit {
		r.t.Fatalf("the fixture pool (%d nodes) reaches the scan ceiling (%d); recapture it", len(r.pool), limit)
	}
	want := make(map[graph.NodeKind]bool, len(kinds))
	for _, kind := range kinds {
		want[kind] = true
	}
	out := make([]graph.Node, 0, len(r.pool))
	for _, node := range r.pool {
		if want[node.Kind] {
			out = append(out, node)
		}
	}
	return out, nil
}

func (r docsFixtureRepo) Annotations(_ context.Context, nodeIDs []uint) (map[uint]*graph.Annotation, error) {
	wanted := make(map[uint]bool, len(nodeIDs))
	for _, id := range nodeIDs {
		wanted[id] = true
	}
	out := make(map[uint]*graph.Annotation, len(nodeIDs))
	for _, node := range r.pool {
		if wanted[node.ID] && node.Annotation != nil {
			out[node.ID] = node.Annotation
		}
	}
	return out, nil
}

// docsFixtureSearcher replays the captured full-text candidate order.
type docsFixtureSearcher struct {
	byQuery map[string][]graph.Node
}

func (s docsFixtureSearcher) Query(_ context.Context, query string, limit int) ([]graph.Node, error) {
	nodes := s.byQuery[query]
	if limit > 0 && len(nodes) > limit {
		nodes = nodes[:limit]
	}
	return nodes, nil
}

// docsOutcome is one query's `wiki_search` result.
//
// Reachable is deliberately not called `retrieved`: the ranking harness's
// `retrieved` asks whether full-text alone found the answer, while this asks
// whether either source did, because `wiki_search` has two and the scan is the
// one that carries question-shaped queries.
//
// Relevant counts files, not nodes. `wiki_search` returns one result per file,
// so two relevant nodes in one file are one thing to find, and counting them
// twice would put a ceiling on recall that no code change could lift.
type docsOutcome struct {
	Query      string `json:"query"`
	Bucket     string `json:"bucket"`
	Negative   bool   `json:"negative,omitempty"`
	OutOfScope bool   `json:"out_of_scope,omitempty"`
	Reachable  bool   `json:"reachable"`
	Returned   int    `json:"returned"`
	Relevant   int    `json:"relevant"`
	Found      int    `json:"found"`
	Rank       int    `json:"rank"`
}

func loadDocsFixture(t *testing.T) (goldenSet, *retrieval.Service, docsFixtureRepo) {
	t.Helper()
	var set goldenSet
	readJSON(t, "testdata/queries.json", &set)
	var fixture docsFixture
	readJSON(t, "testdata/docs_candidates.json", &fixture)

	pool := make([]graph.Node, 0, len(fixture.Pool))
	byID := make(map[uint]graph.Node, len(fixture.Pool))
	for _, n := range fixture.Pool {
		node := n.node()
		pool = append(pool, node)
		byID[node.ID] = node
	}

	byQuery := make(map[string][]graph.Node, len(fixture.FTS))
	for query, ids := range fixture.FTS {
		nodes := make([]graph.Node, 0, len(ids))
		for _, id := range ids {
			// An id absent from the pool is a node of a kind retrieval drops
			// before it looks at anything else, so leaving it out changes nothing.
			if node, ok := byID[id]; ok {
				nodes = append(nodes, node)
			}
		}
		byQuery[query] = nodes
	}
	for _, q := range set.Queries {
		if _, ok := fixture.FTS[q.Query]; !ok {
			t.Fatalf("query %q has no captured docs candidates; re-run the capture", q.Query)
		}
	}

	repo := docsFixtureRepo{t: t, pool: pool}
	return set, retrieval.New(docsFixtureSearcher{byQuery: byQuery}, repo), repo
}

// relevantFiles reduces a judgment to the files it names. A `relevant` entry is
// `kind:qualifiedName@filePath`; `search` is scored on the node, `wiki_search`
// on the file that holds it, because a file is what it returns.
func relevantFiles(q goldenQuery) map[string]bool {
	files := make(map[string]bool, len(q.Relevant))
	for _, r := range q.Relevant {
		if _, path, ok := strings.Cut(r, "@"); ok && path != "" {
			files[path] = true
		}
	}
	return files
}

func runDocsGolden(t *testing.T) []docsOutcome {
	t.Helper()
	set, service, _ := loadDocsFixture(t)
	ctx := context.Background()
	outcomes := make([]docsOutcome, 0, len(set.Queries))
	for _, q := range set.Queries {
		files := relevantFiles(q)
		result := docsOutcome{
			Query:      q.Query,
			Bucket:     q.Bucket,
			Negative:   len(q.Relevant) == 0,
			OutOfScope: q.declinedBy("wiki_search"),
			Relevant:   len(files),
		}

		response, err := service.FromDB(ctx, "ccg", q.Query, goldenLimit, 0, nil)
		if err != nil {
			t.Fatalf("%q: %v", q.Query, err)
		}
		result.Returned = len(response.Results)
		for i, r := range response.Results {
			if !files[strings.TrimPrefix(r.ID, "file:")] {
				continue
			}
			result.Found++
			if result.Rank == 0 {
				result.Rank = i + 1
			}
		}

		if len(files) > 0 {
			probe, err := service.FromDB(ctx, "ccg", q.Query, docsProbeLimit, 0, nil)
			if err != nil {
				t.Fatalf("%q probe: %v", q.Query, err)
			}
			for _, r := range probe.Results {
				if files[strings.TrimPrefix(r.ID, "file:")] {
					result.Reachable = true
					break
				}
			}
		}
		outcomes = append(outcomes, result)
	}
	return outcomes
}

// TestGoldenDocs_HasNotRegressed is the ratchet for `wiki_search`.
//
// It differs from the ranking ratchet in one place, and the difference is the
// point of measuring this tool at all: a query with no right answer is not
// required to return nothing here, because `wiki_search` falls back to a scan
// that matches on substrings and will find *something* for almost any word. Its
// result count is recorded instead, and may not grow. That number is the noise
// this tool produces, written down where a change has to answer for it.
func TestGoldenDocs_HasNotRegressed(t *testing.T) {
	outcomes := runDocsGolden(t)

	if *updateDocsGolden {
		blob, err := json.MarshalIndent(outcomes, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile("testdata/baseline_docs.json", append(blob, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Log("baseline_docs.json rewritten")
		return
	}

	var baseline []docsOutcome
	readJSON(t, "testdata/baseline_docs.json", &baseline)
	want := make(map[string]docsOutcome, len(baseline))
	for _, b := range baseline {
		want[b.Query] = b
	}

	for _, got := range outcomes {
		prev, ok := want[got.Query]
		if !ok {
			t.Errorf("%q is new; record it with -update-docs-golden", got.Query)
			continue
		}
		if got.Negative {
			if got.Returned > prev.Returned {
				t.Errorf("%q: nothing matches this, and the noise grew — %d results, was %d", got.Query, got.Returned, prev.Returned)
			}
			continue
		}
		// Every other assertion here asks where the right answer went. This one
		// asks what came back with it. Handing a caller more files without
		// finding more of what they asked for is the whole failure this tool
		// has, and until now only the negative query was watched for it.
		if got.Returned > prev.Returned && got.Found <= prev.Found {
			t.Errorf("%q: the page grew without finding more — %d files, was %d, and still %d of %d relevant",
				got.Query, got.Returned, prev.Returned, got.Found, got.Relevant)
		}
		if got.Found < prev.Found {
			t.Errorf("%q: the page lost relevant files — %d of %d shown, was %d", got.Query, got.Found, got.Relevant, prev.Found)
		}
		if prev.Rank == 0 {
			continue
		}
		if got.Rank == 0 {
			t.Errorf("%q: first relevant file fell off the page of %d (was at %d)", got.Query, goldenLimit, prev.Rank)
			continue
		}
		if got.Rank > prev.Rank {
			t.Errorf("%q: first relevant file fell from %d to %d", got.Query, prev.Rank, got.Rank)
		}
	}
}

// TestGoldenDocs_Report prints the `wiki_search` scoreboard beside the ranking
// one. It asserts nothing.
func TestGoldenDocs_Report(t *testing.T) {
	outcomes := runDocsGolden(t)

	type tally struct {
		n, reachable, top1, top3 int
		relevant, found          int
		returned, ceiling        int
		rr                       float64
	}
	buckets := map[string]*tally{}
	total := &tally{}
	answerable := &tally{}
	for _, o := range outcomes {
		if o.Negative {
			continue
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
			acc.returned += o.Returned
			// The best precision this query could reach without returning fewer
			// files: every relevant file on the page, and no page longer than the
			// judgments can fill.
			acc.ceiling += min(o.Relevant, o.Returned)
			if o.Reachable {
				acc.reachable++
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

	t.Log("wiki_search — one result per file, limit 10")
	t.Log("bucket           n  reachable  Recall@10   top1  top3   MRR")
	line := func(name string, acc *tally) string {
		recall := 0.0
		if acc.relevant > 0 {
			recall = float64(acc.found) / float64(acc.relevant)
		}
		return fmt.Sprintf("%-15s %2d   %2d/%-2d      %.3f (%2d/%-3d)  %2d    %2d  %.3f",
			name, acc.n, acc.reachable, acc.n, recall, acc.found, acc.relevant, acc.top1, acc.top3, acc.rr/float64(acc.n))
	}
	for _, name := range names {
		t.Log(line(name, buckets[name]))
	}
	t.Log(line("ALL", total))
	t.Log(line("ANSWERABLE", answerable))

	// The table above says where the right answer landed. This one says how much
	// else came with it — the number every metric here was blind to, and the one
	// the noise complaints were always about.
	//
	// `precision` is found/returned. Its absolute value means little: the
	// judgments name the files a developer would accept, not every file that
	// could reasonably be on the page, so an unvouched file is not proven junk.
	// `ceiling` is what precision would be if the ranking were perfect and the
	// page stayed the same length. The gap between the two is all the ranking
	// can win; everything below the ceiling is page length, not order.
	t.Log("")
	t.Log("what else came back")
	t.Log("bucket           n  returned  found  unvouched  precision  ceiling")
	noise := func(name string, acc *tally) string {
		ratio := func(part int) float64 {
			if acc.returned == 0 {
				return 0
			}
			return float64(part) / float64(acc.returned)
		}
		return fmt.Sprintf("%-15s %2d   %4d     %3d    %4d        %.3f     %.3f",
			name, acc.n, acc.returned, acc.found, acc.returned-acc.found, ratio(acc.found), ratio(acc.ceiling))
	}
	for _, name := range names {
		t.Log(noise(name, buckets[name]))
	}
	t.Log(noise("ALL", total))
	t.Log(noise("ANSWERABLE", answerable))

	for _, o := range outcomes {
		switch {
		case o.Negative && o.Returned == 0:
			t.Logf("  no right answer: %-42q — said so, returned nothing", o.Query)
		case o.Negative:
			t.Logf("  no right answer: %-42q — returned %d files anyway", o.Query, o.Returned)
		case !o.Reachable:
			t.Logf("  unreachable:     %-42q (%s) — neither full-text nor the scan holds it", o.Query, o.Bucket)
		case o.Found == 0:
			t.Logf("  shown none:      %-42q (%s) — reachable, but not on the page of %d", o.Query, o.Bucket, goldenLimit)
		case o.Found < o.Relevant:
			t.Logf("  partial:         %-42q (%s) — %d of %d relevant files shown", o.Query, o.Bucket, o.Found, o.Relevant)
		case o.Rank > 3:
			t.Logf("  buried:          %-42q (%s) — first relevant file at %d", o.Query, o.Bucket, o.Rank)
		}
	}
}
