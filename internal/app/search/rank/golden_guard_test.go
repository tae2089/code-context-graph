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
