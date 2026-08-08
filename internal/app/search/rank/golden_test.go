package rank

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"testing"

	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

var updateGolden = flag.Bool("update-golden", false, "rewrite testdata/baseline.json from this run")

// goldenLimit is the result count the golden run asks for. It matches the limit
// used when candidates.json was captured, so the candidate pool the ranker sees
// here is the pool production would hand it for the same request.
const goldenLimit = 10

type goldenQuery struct {
	Query    string   `json:"query"`
	Bucket   string   `json:"bucket"`
	Relevant []string `json:"relevant"`
	Why      string   `json:"why"`
	// OutOfScope marks a query search has decided not to answer, so its zero is
	// a recorded decision rather than a defect. Without the flag those queries
	// sink the headline average and nobody reading it can tell how much of the
	// gap is code and how much is policy. The query stays in the set, and stays
	// red, so anyone reversing the decision inherits the measurements in `why`.
	OutOfScope bool `json:"out_of_scope,omitempty"`
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
}

// outcome is one query's result, and the unit the baseline compares.
//
// Retrieved and Rank answer two different questions that the old single-number
// view confused. Retrieved asks whether full-text search returned any relevant
// node at all; when it is false the ranker was never given the chance, and no
// ranking change can fix that query. Rank asks where the ranker put the first
// relevant node once it had one. Keeping them apart is what stops a retrieval
// regression from being read as a ranking win, or the reverse.
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
	Rank       int    `json:"rank"` // 1-based rank of the first relevant node; 0 means none in the top goldenLimit
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

// runGolden replays every golden query through Rerank against its frozen
// candidate list. No database is opened, so the only thing that can move a
// result is the ranking code itself.
func runGolden(t *testing.T) []outcome {
	t.Helper()
	set, candidates := loadGolden(t)
	outcomes := make([]outcome, 0, len(set.Queries))
	for _, q := range set.Queries {
		captured := candidates[q.Query]
		nodes := make([]graph.Node, len(captured))
		for i, c := range captured {
			nodes[i] = graph.Node{
				ID:            c.ID,
				Name:          c.Name,
				QualifiedName: c.QualifiedName,
				Kind:          graph.NodeKind(c.Kind),
				FilePath:      c.FilePath,
			}
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
			OutOfScope: q.OutOfScope,
			Retrieved:  retrieved,
		}
		ranked := Rerank(q.Query, nodes, goldenLimit)
		result.Returned = len(ranked)
		for i, n := range ranked {
			if relevant[label(byID[n.ID])] {
				result.Rank = i + 1
				break
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
// A query that legitimately changes rank is resolved by re-reading its "why"
// in queries.json and either fixing the ranking or recording the new baseline
// with -update-golden — never by relaxing the judgment to make the run pass.
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
		if prev.Rank == 0 {
			continue // nothing to lose; an improvement is recorded, not enforced
		}
		if got.Rank == 0 {
			t.Errorf("%q: first relevant node fell out of the top %d (was rank %d)", got.Query, goldenLimit, prev.Rank)
			continue
		}
		if got.Rank > prev.Rank {
			t.Errorf("%q: first relevant node fell from rank %d to rank %d", got.Query, prev.Rank, got.Rank)
		}
	}
}

// TestGolden_Report prints the per-bucket scoreboard. It asserts nothing: the
// numbers are for reading, and the ratchet above is what fails a build.
// Run with `go test -run TestGolden_Report -v`.
func TestGolden_Report(t *testing.T) {
	outcomes := runGolden(t)

	type tally struct {
		n, retrieved, top1, top3 int
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

	t.Log("bucket           n  retrieved  top1  top3   MRR")
	line := func(name string, acc *tally) string {
		return fmt.Sprintf("%-15s %2d   %2d/%-2d     %2d    %2d  %.3f",
			name, acc.n, acc.retrieved, acc.n, acc.top1, acc.top3, acc.rr/float64(acc.n))
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
		case o.Rank == 0:
			t.Logf("  ranking miss:   %-28q (%s) — relevant node retrieved but not in the top %d", o.Query, o.Bucket, goldenLimit)
		case o.Rank > 3:
			t.Logf("  buried:         %-28q (%s) — first relevant node at rank %d", o.Query, o.Bucket, o.Rank)
		}
	}
}
