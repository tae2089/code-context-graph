// The ratchet in golden_test.go is the only automated defence the ranking has,
// and it had three holes: it walked the run instead of the baseline, it recorded
// the answer key's size without comparing it, and it left every entry that
// already scores nothing beyond the reach of any assertion. This file closes
// them, and its own tests are what prove each one shut.
package rank_test

import (
	"fmt"
	"sort"
	"strings"
	"testing"
)

// zeroScoreClass says why a baseline entry that scores nothing is allowed to
// stay in the set. There are two answers and no third: either the search
// decided not to answer the query, or it means to and cannot yet.
type zeroScoreClass string

const (
	// classOutOfScope is a recorded decision. queries.json must list `search`
	// in the query's out_of_scope, or the label is just a way to silence it.
	classOutOfScope zeroScoreClass = "out of scope"
	// classKnownGap is a debt. The search wants to answer this and does not.
	classKnownGap zeroScoreClass = "known gap"
)

// zeroScoreNote is one such entry's classification and the reason it carries.
type zeroScoreNote struct {
	class  zeroScoreClass
	reason string
}

// guardBaseline reports every way a corpus's baseline has stopped guarding.
//
// It takes this run's outcomes, the committed baseline, and the corpus's
// zero-score notes, and returns one line per problem — empty when the baseline
// guards every entry it holds. It is a plain function over data so the tests
// below can hand it a deliberately broken corpus and prove it says so; the
// corpus-wide test feeds it the real files.
func guardBaseline(outcomes, baseline []outcome, notes map[string]zeroScoreNote) []string {
	ran := make(map[string]outcome, len(outcomes))
	for _, o := range outcomes {
		ran[o.Query] = o
	}

	var problems []string
	explained := make(map[string]bool, len(notes))
	for _, prev := range baseline {
		got, visited := ran[prev.Query]
		if !visited {
			problems = append(problems, fmt.Sprintf(
				"%q is in the baseline but no golden query answers to it, so the ratchet never visits it; restore the query or drop the entry with -update-golden",
				prev.Query))
			// Its note is still spoken for. Reporting it stale on top would
			// read as "delete the classification", which is the opposite of
			// what a deleted query needs.
			explained[prev.Query] = true
			continue
		}
		// Recall is Found over Relevant, so a shorter answer key raises the
		// score with no code change. The baseline recorded the key's size all
		// along; this is the comparison it was missing.
		if got.Relevant < prev.Relevant {
			problems = append(problems, fmt.Sprintf(
				"%q: the answer key shrank from %d judged answers to %d, which raises Recall with no code change; restore the judgment or say in the commit why it was wrong",
				prev.Query, prev.Relevant, got.Relevant))
		}
		// The ratchet holds an entry with two assertions: Found may not drop,
		// and Rank may not rise. Both are dead at zero — nothing ranks below
		// nothing — so an entry needs a note exactly when both are dead. A
		// negative query is exempt: its right answer is nothing, so its zero is
		// the pass condition and the ratchet guards it through Returned.
		if prev.Negative || prev.Found > 0 || prev.Rank > 0 {
			continue
		}
		explained[prev.Query] = true
		problems = append(problems, classifyZeroScore(prev, notes[prev.Query])...)
	}

	stale := make([]string, 0, len(notes))
	for query := range notes {
		if !explained[query] {
			stale = append(stale, query)
		}
	}
	sort.Strings(stale)
	for _, query := range stale {
		problems = append(problems, fmt.Sprintf(
			"%q no longer scores zero in the baseline; drop its zeroScoreNotes entry so the list cannot rot into a silent excuse",
			query))
	}
	return problems
}

// classifyZeroScore checks the note standing in for the assertions a zero-score
// entry cannot carry. Nothing can rank below nothing, so the note is the only
// thing left holding the entry to account.
func classifyZeroScore(prev outcome, note zeroScoreNote) []string {
	if note == (zeroScoreNote{}) {
		return []string{fmt.Sprintf(
			"%q scores nothing, so no ranking change can fail it; add a zeroScoreNotes entry classifying it %q or %q, with the reason",
			prev.Query, classOutOfScope, classKnownGap)}
	}
	if note.class != classOutOfScope && note.class != classKnownGap {
		return []string{fmt.Sprintf(
			"%q is classified %q, which is neither %q nor %q",
			prev.Query, note.class, classOutOfScope, classKnownGap)}
	}

	var problems []string
	if strings.TrimSpace(note.reason) == "" {
		problems = append(problems, fmt.Sprintf(
			"%q is classified %q with no reason; the classification alone does not say what has to change to close it",
			prev.Query, note.class))
	}
	// The pool is what separates the two classes. If retrieval already handed
	// the ranker a relevant answer, nothing was declined — the ranker failed to
	// put it on the page, and that is a debt to work off, not a policy.
	if prev.Retrieved && note.class != classKnownGap {
		problems = append(problems, fmt.Sprintf(
			"%q is classified %q, but the candidate pool holds a relevant answer, so this is the ranker leaving it off the page; classify it %q",
			prev.Query, note.class, classKnownGap))
	}
	if note.class == classOutOfScope && !prev.OutOfScope {
		problems = append(problems, fmt.Sprintf(
			"%q is classified %q, but queries.json does not list search in its out_of_scope; record the decision there or classify it %q",
			prev.Query, classOutOfScope, classKnownGap))
	}
	return problems
}

// TestGolden_BaselineIsFullyGuarded runs the guard over every corpus's real
// files. Where TestGolden_RankingHasNotRegressed asks whether the ranking got
// worse, this asks whether that question is still being put to every entry the
// baseline holds — a green ratchet means nothing over an entry it skips.
func TestGolden_BaselineIsFullyGuarded(t *testing.T) {
	for _, corpus := range goldenCorpora(t) {
		t.Run(corpus.name, func(t *testing.T) {
			var baseline []outcome
			readJSON(t, corpus.dir+"/baseline.json", &baseline)
			for _, problem := range guardBaseline(runGolden(t, corpus.dir), baseline, zeroScoreNotes[corpus.name]) {
				t.Error(problem)
			}
		})
	}
}

// zeroScoreNotes lists, per corpus, every baseline entry that scores nothing,
// with the class and reason that let it stay. The list is a debt, not a
// permission: the guard above fails when an entry is missing one, and equally
// when a listed entry starts scoring, so it cannot rot into a silent excuse.
// Four of the nineteen are the ranker's own: the pool handed it the judged
// answer and the page of ten did not carry it. Those are `known gap` by
// definition — nothing was declined — and the guard enforces that, so no
// future zero can be filed under policy while retrieval is still finding it.
var zeroScoreNotes = map[string]map[string]zeroScoreNote{
	"ccg": {
		// The four the recorded decision covers. Each `why` in queries.json
		// carries the measurement anyone reversing the decision inherits.
		"cfg":       {classOutOfScope, "search does not expand abbreviations. No node is named cfg — it appears only as a local variable, and locals are not indexed — so answering means guessing that cfg is config. Measured over the whole graph that guess scores 0.28 against a 0.08 ceiling for unrelated junk, a margin too thin to gate on."},
		"sanitze":   {classOutOfScope, "search does not correct spelling. FTS returns no candidate for the misspelling at all, so the ranker is never handed one; only matching on the qualified name as a subsequence would reach it, and that was removed by decision."},
		"retreival": {classOutOfScope, "search does not correct spelling. 'ie' swapped for 'ei' breaks the ordered subsequence outright, so nothing short of edit-distance matching could recover it, and that was removed by decision."},
		"anotation": {classOutOfScope, "search does not correct spelling. One 'n' short is still an ordered subsequence of annotation, but FTS never returns the candidate, so the ranker is never given the chance."},

		// The pool held the judged answer and the page did not. These four are
		// the ranker's own debt, and the only ones here a reordering can pay.
		"how does the graph get built":                                                    {classKnownGap, "the intent pool holds workflow.Service.Build in the judged internal/app/ingest/workflow/build.go, and the page of ten files did not carry it. The name index answers nothing here — SanitizeFTS5 joins terms with a space and FTS5 reads a space as AND, so a six-word question needs all six words in one document — which leaves the ordering entirely to the intent scorer."},
		"why did one oversized file abort indexing before it was read":                    {classKnownGap, "the intent pool holds three declarations in the judged internal/app/ingest/workflow/fileio.go, CheckParseFileSize among them, and none reached the page of ten."},
		"what limits how much source code a single indexing pass may read":                {classKnownGap, "the intent pool holds CheckTotalParsedBytes, readRegularSourceFile and inspectRegularSourceFile, all three in the judged internal/app/ingest/workflow/fileio.go, and none reached the page of ten."},
		"why are old generated pages still present after their source files were removed": {classKnownGap, "the intent pool holds docs.Generator.pruneManaged in the judged internal/app/docs/generator.go — the prune path the question is about — and it did not reach the page of ten."},

		// Retrieval never handed the answer over. A reordering cannot pay
		// these; the index or the tokenizer has to change first.
		"mcp":                                 {classKnownGap, "the only defensible answer is the package node for internal/adapters/inbound/mcp, and the captured pool of 50 name-index candidates holds no package node at all, so no reordering can reach it. search means to answer identifier queries, so this is retrieval owing an answer, not a decision to decline — and closing it needs a recapture, not a constant."},
		"what happens when a webhook arrives": {classKnownGap, "neither pool holds the judged webhook.WebhookHandler.ServeHTTP. The name index returns nothing because it needs every word of the question in one document; the intent index returns 46 hits and the handler is not among them, so the reason text the question is asking for was never fetched."},
		"where do search results get ranked":  {classKnownGap, "neither pool holds the judged rank.Rerank. Its surface says Rerank, not ranked, and the question's other words — search, results, get — are spread across every file with a search API, so the 50 hits fetched are all of that spread. queries.json calls this the hardest question in the set."},
		"why does editing a function with many outgoing links rank as riskier":     {classKnownGap, "the judged internal/app/analyze/changes/service.go is in neither pool. Three of the seven scored terms — editing, riskier, and the phrasing around them — appear in no recorded reason, so the 49 intent hits fetched are ranked on function, many, outgoing, links and rank alone."},
		"what decides whether generated documentation may delete an existing page": {classKnownGap, "the judged internal/app/docs/generator.go is in neither pool for this phrasing, though the incident query about the same prune path does retrieve it. So the file is indexed and reachable; this wording does not reach it."},

		// Korean. All three are the same mechanism, and all three are the
		// reason this project exists — reasons written in Korean, asked for in
		// Korean. Each returns nothing at all, not a bad order.
		"읽지 못한 파일을 업데이트에서 삭제된 것으로 보지 않는 기준은 어디야":    {classKnownGap, "the answer is empty, not misordered: 2 of the question's 10 scored terms appear in any recorded reason, and CanAnswer drops every intent hit below half. The terms carry their particles — 파일을, 기준은, 업데이트에서 — so they cannot match 파일 or 기준 in a reason, which is why 8 of the 10 count zero. The judged internal/app/ingest/workflow/update.go is not among the 5 hits fetched."},
		"여러 묶음으로 읽은 파일 사이의 호출 관계는 왜 마지막에 한꺼번에 연결하지": {classKnownGap, "the same particle mismatch: 2 of 11 scored terms appear in any recorded reason, so CanAnswer drops all 30 intent hits and the answer is empty. Neither judged file — incremental.go, update.go — is among them."},
		"코드가 바뀐 뒤 어떤 주석을 다시 확인해야 하는지는 어디서 판단해":      {classKnownGap, "the same particle mismatch: 4 of 11 scored terms appear in any recorded reason, still under half, so CanAnswer drops all 9 intent hits. The judged internal/app/docs/lint.go is not among them."},
	},
	"cobra": {
		"levenshtein": {classKnownGap, "the name pool holds the judged cobra.ld, retrieved through its docstring, and the evidence cut drops it: the cut justifies a hit on name, path and @intent only, and cobra carries no annotations to speak with. Recorded in knownHiddenRelevant too, under the same reason — closing it means teaching the cut a docstring signal, which is a design change to make deliberately."},
		"Excute":      {classOutOfScope, "a missing letter, declined by the same recorded policy as the primary corpus's typos. queries.json lists search in its out_of_scope."},
	},
	"gorm": {
		"Preloda": {classOutOfScope, "a transposition, declined by the same recorded policy as the primary corpus's typos. queries.json lists search in its out_of_scope."},
	},
}

// guardedCorpus builds the smallest corpus the guard accepts: one query the
// search answers, one it declines with the decision recorded, and one negative
// whose right answer is nothing. Each test below breaks it in exactly one way,
// so a single reported problem is the proof that the hole is caught.
func guardedCorpus() (outcomes, baseline []outcome, notes map[string]zeroScoreNote) {
	outcomes = []outcome{
		{Query: "answered", Retrieved: true, Returned: 3, Relevant: 2, Found: 2, Rank: 1},
		{Query: "declined", OutOfScope: true, Relevant: 1},
		{Query: "nonsense", Negative: true},
	}
	baseline = append([]outcome(nil), outcomes...)
	notes = map[string]zeroScoreNote{
		"declined": {class: classOutOfScope, reason: "search does not correct spelling, and queries.json records the decision"},
	}
	return outcomes, baseline, notes
}

// onlyProblem asserts the guard reported exactly one problem and hands it back
// for a check on what it says.
func onlyProblem(t *testing.T, problems []string) string {
	t.Helper()
	if len(problems) != 1 {
		t.Fatalf("want exactly one problem, got %d: %v", len(problems), problems)
	}
	return problems[0]
}

func wantMentions(t *testing.T, problem string, phrases ...string) {
	t.Helper()
	for _, phrase := range phrases {
		if !strings.Contains(problem, phrase) {
			t.Errorf("problem does not mention %q: %s", phrase, problem)
		}
	}
}

func TestGuardBaseline_AcceptsAFullyGuardedCorpus(t *testing.T) {
	outcomes, baseline, notes := guardedCorpus()
	if problems := guardBaseline(outcomes, baseline, notes); len(problems) != 0 {
		t.Errorf("a fully guarded corpus reported problems: %v", problems)
	}
}

// TestGuardBaseline_CatchesADeletedQuery is hole one: the ratchet walks the run,
// not the baseline, so deleting a hard query from queries.json used to leave its
// baseline entry sitting there unvisited and unmissed.
func TestGuardBaseline_CatchesADeletedQuery(t *testing.T) {
	outcomes, baseline, notes := guardedCorpus()
	outcomes = outcomes[:1] // "declined" and "nonsense" deleted from queries.json

	problems := guardBaseline(outcomes, baseline, notes)
	if len(problems) != 2 {
		t.Fatalf("want a problem for each deleted query, got %d: %v", len(problems), problems)
	}
	wantMentions(t, problems[0], `"declined"`, "never visits it")
	wantMentions(t, problems[1], `"nonsense"`, "never visits it")
}

// TestGuardBaseline_CatchesAShrunkAnswerKey is hole two: Recall is Found over
// Relevant, so dropping an answer nobody found raises the score without a line
// of code changing. The baseline recorded Relevant but never compared it.
func TestGuardBaseline_CatchesAShrunkAnswerKey(t *testing.T) {
	outcomes, baseline, notes := guardedCorpus()
	outcomes[0].Relevant = 1 // one judged answer deleted from queries.json

	wantMentions(t, onlyProblem(t, guardBaseline(outcomes, baseline, notes)),
		`"answered"`, "answer key shrank from 2", "to 1")
}

// A key that grows is the honest way to raise the bar, and must pass.
func TestGuardBaseline_AllowsAGrownAnswerKey(t *testing.T) {
	outcomes, baseline, notes := guardedCorpus()
	outcomes[0].Relevant = 3

	if problems := guardBaseline(outcomes, baseline, notes); len(problems) != 0 {
		t.Errorf("growing the answer key reported problems: %v", problems)
	}
}

// TestGuardBaseline_CatchesAnUnclassifiedZeroScore is hole three: an entry that
// already scores nothing cannot score less, so no ranking change can fail it.
// It is only allowed to stay if somebody says why.
func TestGuardBaseline_CatchesAnUnclassifiedZeroScore(t *testing.T) {
	outcomes, baseline, _ := guardedCorpus()

	wantMentions(t, onlyProblem(t, guardBaseline(outcomes, baseline, nil)),
		`"declined"`, "no ranking change can fail it", "out of scope", "known gap")
}

func TestGuardBaseline_CatchesAClassificationWithNoReason(t *testing.T) {
	outcomes, baseline, notes := guardedCorpus()
	notes["declined"] = zeroScoreNote{class: classKnownGap, reason: "   "}

	wantMentions(t, onlyProblem(t, guardBaseline(outcomes, baseline, notes)),
		`"declined"`, "no reason")
}

func TestGuardBaseline_CatchesAnUnknownClassification(t *testing.T) {
	outcomes, baseline, notes := guardedCorpus()
	notes["declined"] = zeroScoreNote{class: "wontfix", reason: "because"}

	wantMentions(t, onlyProblem(t, guardBaseline(outcomes, baseline, notes)),
		`"declined"`, `"wontfix"`)
}

// TestGuardBaseline_RefusesOutOfScopeWhenTheAnswerWasRetrieved is the line the
// two classifications draw. If the candidate pool already held the answer, the
// search did not decline the query — the ranker failed to put it on the page.
func TestGuardBaseline_RefusesOutOfScopeWhenTheAnswerWasRetrieved(t *testing.T) {
	outcomes, baseline, notes := guardedCorpus()
	outcomes[1].Retrieved = true
	baseline[1].Retrieved = true

	wantMentions(t, onlyProblem(t, guardBaseline(outcomes, baseline, notes)),
		`"declined"`, "candidate pool", "known gap")
}

// A classification of "out of scope" has to be backed by the decision recorded
// in queries.json, or it is just a label that silences the entry.
func TestGuardBaseline_RefusesOutOfScopeWithNoRecordedDecision(t *testing.T) {
	outcomes, baseline, notes := guardedCorpus()
	outcomes[1].OutOfScope = false
	baseline[1].OutOfScope = false

	wantMentions(t, onlyProblem(t, guardBaseline(outcomes, baseline, notes)),
		`"declined"`, "out_of_scope", "queries.json")
}

// A note that outlives the zero it explains would rot into a silent excuse.
func TestGuardBaseline_CatchesAStaleNote(t *testing.T) {
	outcomes, baseline, notes := guardedCorpus()
	notes["answered"] = zeroScoreNote{class: classKnownGap, reason: "stale: this query scores 2"}

	wantMentions(t, onlyProblem(t, guardBaseline(outcomes, baseline, notes)),
		`"answered"`, "no longer scores zero")
}
