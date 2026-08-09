//go:build fts5

package searchsql

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

	"gorm.io/gorm"

	intentapp "github.com/tae2089/code-context-graph/internal/app/search/intent"
	requestctx "github.com/tae2089/code-context-graph/internal/ctx"
	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

var updateIntentBaseline = flag.Bool("update-intent", false, "rewrite testdata/intent_baseline.json from this run")

// intentQuestion is one golden judgment: a question somebody would actually ask
// during an incident, and the files that count as a place to start.
//
// Accept holds files rather than symbols on purpose. The success criterion for
// find_by_intent is not "the exactly right declaration" but "somewhere the
// reader can start walking the graph", and the answer is grouped by file, so the
// file is the unit the reader picks from. A question with an empty Accept is a
// negative case: the right answer is nothing at all.
type intentQuestion struct {
	Question string   `json:"question"`
	Bucket   string   `json:"bucket"`
	Accept   []string `json:"accept"`
	Why      string   `json:"why"`
}

type intentQuestionSet struct {
	Questions []intentQuestion `json:"questions"`
}

// intentOutcome is one question's result and the unit the baseline compares.
//
// Answered and Rank say different things. Answered asks whether the index
// returned anything at all; when it is false no ranking change can help, and
// the reader has to be told that nobody recorded a reason rather than that the
// code is absent. Rank asks where the first acceptable file landed among the
// files the answer shows.
//
// DeadEnds counts returned declarations with no edge in either direction. An
// entry point that cannot be walked from fails the criterion this tool is
// measured by even when it is topically correct, so it is counted separately
// instead of being folded into the hit rate.
// Evidence is what the answer rested on, for the report to print. It is not
// persisted and not compared: it explains a bad row rather than scoring it, and
// writing it into the baseline would make every wording change a diff.
type intentOutcome struct {
	Question string `json:"question"`
	Bucket   string `json:"bucket"`
	Negative bool   `json:"negative,omitempty"`
	Answered bool   `json:"answered"`
	Files    int    `json:"files"`
	Entries  int    `json:"entries"`
	Rank     int    `json:"rank"`
	DeadEnds int    `json:"dead_ends"`
	Evidence string `json:"-"`
}

// summarizeIntentTerms renders the words that did the matching, commonest first,
// which is the order that explains a bad answer fastest.
func summarizeIntentTerms(answer intentapp.Answer) string {
	terms := slices.Clone(answer.Terms)
	slices.SortStableFunc(terms, func(a, b intentapp.Term) int { return b.InReasons - a.InReasons })
	parts := make([]string, 0, len(terms))
	for _, term := range terms {
		parts = append(parts, fmt.Sprintf("%s %d", term.Text, term.InReasons))
	}
	if len(parts) == 0 {
		return "no term of the question is written in any recorded reason"
	}
	return fmt.Sprintf("of %d reasons: %s", answer.ReasonsSearched, strings.Join(parts, ", "))
}

// runIntentGolden rebuilds a real SQLite intent index from the frozen corpus and
// asks every golden question through the production service.
func runIntentGolden(t *testing.T) ([]intentOutcome, intentapp.Coverage, intentCorpus) {
	t.Helper()
	return runIntentGoldenOn(t, setupTestDB(t), NewSQLiteBackend())
}

// runIntentGoldenOn is the same run against whichever backend is handed in.
//
// It takes the backend because the two of them do not score the same question
// the same way. SQLite orders by FTS5's rank, which is bm25 and therefore
// discounts a word that appears in many recorded reasons. PostgreSQL orders by
// ts_rank, which reads one document at a time and never sees how common a word
// is across the corpus. A score measured on SQLite says nothing about the
// backend a deployed server actually runs, so both are measured.
//
// Nothing here is stubbed below the service: the index, the sanitizer, the
// ordering, and the file grouping all run. The corpus is what is frozen, so a
// result can only move when the intent code moves.
func runIntentGoldenOn(t *testing.T, db *gorm.DB, backend Backend) ([]intentOutcome, intentapp.Coverage, intentCorpus) {
	t.Helper()
	corpus := loadIntentCorpus(t)
	set := loadIntentQuestions(t)

	seedIntentCorpus(t, db, corpus)
	ctx := requestctx.WithNamespace(context.Background(), corpus.Namespace)
	if err := backend.Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if _, err := RefreshSearchDocuments(ctx, db); err != nil {
		t.Fatalf("RefreshSearchDocuments: %v", err)
	}
	if err := backend.Rebuild(ctx, db); err != nil {
		t.Fatalf("Rebuild: %v", err)
	}

	edges := make(map[uint]int, len(corpus.Nodes))
	for _, n := range corpus.Nodes {
		edges[n.ID] = n.OutEdges + n.InEdges
	}

	reader := NewReader(db, backend)
	service := intentapp.New(reader, reader)

	outcomes := make([]intentOutcome, 0, len(set.Questions))
	var coverage intentapp.Coverage
	for _, q := range set.Questions {
		answer, err := service.Find(ctx, q.Question, intentapp.DefaultLimit)
		if err != nil {
			t.Fatalf("%q: %v", q.Question, err)
		}
		coverage = answer.Coverage

		accept := make(map[string]bool, len(q.Accept))
		for _, path := range q.Accept {
			accept[path] = true
		}
		result := intentOutcome{
			Question: q.Question,
			Bucket:   q.Bucket,
			Negative: len(q.Accept) == 0,
			Answered: len(answer.Files) > 0,
			Files:    len(answer.Files),
			Evidence: summarizeIntentTerms(answer),
		}
		for i, file := range answer.Files {
			result.Entries += len(file.Entries)
			for _, entry := range file.Entries {
				if edges[entry.NodeID] == 0 {
					result.DeadEnds++
				}
			}
			if accept[file.FilePath] && result.Rank == 0 {
				result.Rank = i + 1
			}
		}
		outcomes = append(outcomes, result)
	}
	return outcomes, coverage, corpus
}

// seedIntentCorpus reloads the frozen declarations, keeping the captured node
// ids so an answer can be matched back to the edge counts captured with them.
func seedIntentCorpus(t *testing.T, db *gorm.DB, corpus intentCorpus) {
	t.Helper()
	nodes := make([]graph.Node, 0, len(corpus.Nodes))
	for _, n := range corpus.Nodes {
		nodes = append(nodes, graph.Node{
			ID:            n.ID,
			Namespace:     corpus.Namespace,
			QualifiedName: n.QualifiedName,
			Kind:          graph.NodeKind(n.Kind),
			Name:          n.Name,
			FilePath:      n.FilePath,
			StartLine:     n.StartLine,
			Language:      n.Language,
		})
	}
	if err := db.CreateInBatches(&nodes, 200).Error; err != nil {
		t.Fatalf("seed nodes: %v", err)
	}

	for _, n := range corpus.Nodes {
		if len(n.Tags) == 0 {
			continue
		}
		annotation := graph.Annotation{NodeID: n.ID}
		if err := db.Create(&annotation).Error; err != nil {
			t.Fatalf("seed annotation for node %d: %v", n.ID, err)
		}
		tags := make([]graph.DocTag, 0, len(n.Tags))
		for _, tag := range n.Tags {
			tags = append(tags, graph.DocTag{
				AnnotationID: annotation.ID,
				Kind:         graph.TagKind(tag.Kind),
				Value:        tag.Value,
				Ordinal:      tag.Ordinal,
			})
		}
		if err := db.CreateInBatches(&tags, 100).Error; err != nil {
			t.Fatalf("seed tags for node %d: %v", n.ID, err)
		}
	}
}

func loadIntentCorpus(t *testing.T) intentCorpus {
	t.Helper()
	var corpus intentCorpus
	readIntentJSON(t, intentDir+"intent_corpus.json", &corpus)
	if len(corpus.Nodes) == 0 {
		t.Fatal("intent_corpus.json is empty; recapture it with -capture-intent")
	}
	return corpus
}

func loadIntentQuestions(t *testing.T) intentQuestionSet {
	t.Helper()
	var set intentQuestionSet
	readIntentJSON(t, intentDir+"intent_questions.json", &set)
	return set
}

func readIntentJSON(t *testing.T, path string, into any) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, into); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
}

// TestGoldenIntent_HasNotRegressed compares this build against the committed
// baseline question by question.
//
// It is a ratchet, not a quality bar. The judgments were written by the author
// of the tool, so a good score here proves nothing; a dropped answer proves
// something broke. A question that legitimately changes is resolved by
// re-reading its "why" in intent_questions.json and either fixing the code or
// recording the new baseline with -update-intent — never by widening the accept
// list until the run passes.
func TestGoldenIntent_HasNotRegressed(t *testing.T) {
	outcomes, _, _ := runIntentGolden(t)
	checkIntentBaseline(t, outcomes, "intent_baseline.json")
}

// checkIntentBaseline compares one backend's run against its own baseline file.
// Each backend keeps a separate one because they rank differently, and holding
// PostgreSQL to a score measured on SQLite would report a difference between
// two ranking functions as a regression in the code.
func checkIntentBaseline(t *testing.T, outcomes []intentOutcome, baselineFile string) {
	t.Helper()
	if *updateIntentBaseline {
		blob, err := json.MarshalIndent(outcomes, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(intentDir+baselineFile, append(blob, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("%s rewritten", baselineFile)
		return
	}

	var baseline []intentOutcome
	readIntentJSON(t, intentDir+baselineFile, &baseline)
	want := make(map[string]intentOutcome, len(baseline))
	for _, b := range baseline {
		want[b.Question] = b
	}

	for _, got := range outcomes {
		prev, ok := want[got.Question]
		if !ok {
			t.Errorf("%q is new; record it with -update-intent", got.Question)
			continue
		}
		if got.Negative {
			// A question about something nobody wrote a reason for should come
			// back with nothing, and since short Latin terms stopped matching by
			// prefix it does. The baseline still holds the measured count rather
			// than asserting zero, so that if any-term matching starts leaking
			// again the count is what fails, and so that a backend that has not
			// closed the leak yet can record what it does today.
			if got.Files > prev.Files {
				t.Errorf("%q: nothing was recorded about this and the answer grew from %d files to %d", got.Question, prev.Files, got.Files)
			}
			if got.Files == 0 && prev.Files > 0 {
				t.Errorf("%q: now answers with nothing, as it should — record it with -update-intent and drop the debt note from intent_questions.json", got.Question)
			}
			continue
		}
		if prev.Rank == 0 {
			if got.Rank > 0 {
				continue // an improvement is recorded, not enforced
			}
			continue
		}
		if got.Rank == 0 {
			t.Errorf("%q: no acceptable entry point left in the answer (was file %d)", got.Question, prev.Rank)
			continue
		}
		if got.Rank > prev.Rank {
			t.Errorf("%q: first acceptable entry point fell from file %d to file %d", got.Question, prev.Rank, got.Rank)
		}
		if got.DeadEnds > prev.DeadEnds {
			t.Errorf("%q: %d returned declarations have no edge to walk, was %d", got.Question, got.DeadEnds, prev.DeadEnds)
		}
	}
}

// TestGoldenIntent_Report prints the scoreboard. It asserts nothing; the ratchet
// above is what fails a build.
// Run with `go test -tags fts5 -run TestGoldenIntent_Report -v`.
func TestGoldenIntent_Report(t *testing.T) {
	outcomes, coverage, corpus := runIntentGolden(t)
	reportIntentGolden(t, outcomes, coverage, corpus)
}

// reportIntentGolden prints one backend's scoreboard.
func reportIntentGolden(t *testing.T, outcomes []intentOutcome, coverage intentapp.Coverage, corpus intentCorpus) {
	t.Helper()
	type tally struct {
		n, answered, hit, top1, top3 int
		entries, deadEnds            int
		rr                           float64
	}
	buckets := map[string]*tally{}
	total := &tally{}
	for _, o := range outcomes {
		if o.Negative {
			continue // an empty answer is the right answer; averaging it in would say nothing
		}
		b, ok := buckets[o.Bucket]
		if !ok {
			b = &tally{}
			buckets[o.Bucket] = b
		}
		for _, acc := range []*tally{b, total} {
			acc.n++
			acc.entries += o.Entries
			acc.deadEnds += o.DeadEnds
			if o.Answered {
				acc.answered++
			}
			if o.Rank > 0 {
				acc.hit++
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

	// Entry-point hit rate leads because that is the promise: one of the files
	// handed back is somewhere worth starting. Dead-end rate follows because a
	// topically right file the reader cannot walk from has not delivered on it.
	t.Log("bucket       n  answered   hit   top1  top3   MRR    dead-end")
	line := func(name string, acc *tally) string {
		deadRate := 0.0
		if acc.entries > 0 {
			deadRate = float64(acc.deadEnds) / float64(acc.entries)
		}
		return fmt.Sprintf("%-11s %2d   %2d/%-2d    %2d/%-2d  %2d    %2d  %.3f  %.3f (%d/%d)",
			name, acc.n, acc.answered, acc.n, acc.hit, acc.n, acc.top1, acc.top3, acc.rr/float64(acc.n), deadRate, acc.deadEnds, acc.entries)
	}
	for _, name := range names {
		t.Log(line(name, buckets[name]))
	}
	t.Log(line("ALL", total))
	t.Logf("coverage: %d of %d searchable declarations carry a recorded reason (%.1f%%)",
		coverage.NodesWithReason, coverage.NodesTotal, 100*float64(coverage.NodesWithReason)/float64(max(coverage.NodesTotal, 1)))

	// Without this line a dead-end rate of zero reads as a good score. On this
	// corpus it is closer to a fact about the graph: almost nothing that carries
	// a reason is unreachable, so the metric has almost nothing to find. It
	// still earns its place as a guard — if edge resolution regresses, answers
	// start pointing at declarations nobody can walk from, and this is where it
	// shows.
	reachable, unreachable := 0, 0
	for _, n := range corpus.Nodes {
		if len(n.Tags) == 0 {
			continue
		}
		if n.OutEdges+n.InEdges == 0 {
			unreachable++
			continue
		}
		reachable++
	}
	t.Logf("dead-end ceiling: %d of %d declarations with a reason have no edge in either direction", unreachable, reachable+unreachable)

	for _, o := range outcomes {
		switch {
		case o.Negative && o.Files > 0:
			t.Logf("  answered anyway: %-60q — nothing was recorded about this, got %d files [%s]", o.Question, o.Files, o.Evidence)
		case o.Negative:
		case !o.Answered:
			t.Logf("  no answer:       %-60q (%s) — the index matched no recorded reason [%s]", o.Question, o.Bucket, o.Evidence)
		case o.Rank == 0:
			t.Logf("  wrong start:     %-60q (%s) — %d files back, none acceptable [%s]", o.Question, o.Bucket, o.Files, o.Evidence)
		case o.Rank > 3:
			t.Logf("  buried:          %-60q (%s) — first acceptable file at %d of %d", o.Question, o.Bucket, o.Rank, o.Files)
		case o.DeadEnds > 0:
			t.Logf("  dead ends:       %-60q (%s) — %d of %d returned declarations have no edge to walk", o.Question, o.Bucket, o.DeadEnds, o.Entries)
		}
	}
}
