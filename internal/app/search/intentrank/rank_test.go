package intentrank_test

import (
	"slices"
	"testing"

	"github.com/tae2089/code-context-graph/internal/app/search/intentrank"
	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

// rank is the shorthand every test here uses: score these documents against this
// question and report the node ids in answer order.
func rank(t *testing.T, question string, corpusSize int, docs ...intentrank.Doc) []uint {
	t.Helper()
	return ids(intentrank.Rank(question, docs, corpusSize, len(docs)))
}

// ids pulls the answer order out of a result, for the tests that only care about
// which documents came back and in what order.
func ids(result intentrank.Result) []uint {
	out := make([]uint, 0, len(result.Matches))
	for _, match := range result.Matches {
		out = append(out, match.NodeID)
	}
	return out
}

// The whole reason for scoring in Go is that PostgreSQL's ts_rank never sees how
// common a word is. "sync" is written in nearly every recorded reason here and
// "quarantine" in one, so a document earning both should outrank one that only
// repeats the common word, however often it repeats it.
func TestRank_PrefersTheRarerWord(t *testing.T) {
	docs := []intentrank.Doc{
		{NodeID: 1, Content: "sync the repository and sync it again and sync once more"},
		{NodeID: 2, Content: "quarantine a repository whose sync keeps failing"},
	}
	for i := 3; i < 40; i++ {
		docs = append(docs, intentrank.Doc{NodeID: uint(i), Content: "sync something else"})
	}
	got := rank(t, "why does a sync quarantine a repository", len(docs), docs...)
	if len(got) == 0 || got[0] != 2 {
		t.Fatalf("first answer is %v, want node 2 to win on the rarer word", got)
	}
}

// Two documents that earn exactly the same score must always come back in the
// same order, or asking for one more row reshuffles the answer.
//
// The order is the identity order, and the ids here run against it on purpose:
// ascending file path is descending node id, so a tie broken by id would answer
// 3, 5, 7 while a tie broken by identity answers 7, 5, 3.
func TestRank_BreaksTiesByIdentityNotByNodeID(t *testing.T) {
	docs := tiedDocs()
	got := rank(t, "what keeps the queue draining", len(docs), docs...)
	if !slices.Equal(got, []uint{7, 5, 3}) {
		t.Fatalf("got %v, want the file-path order 7, 5, 3 on a three-way tie", got)
	}
}

// The same corpus with its ids handed out differently is the same answer. This
// is the unit-level form of what re-indexing a repository does: same
// declarations, same reasons, different ids.
func TestRank_TiedAnswerDoesNotDependOnWhichIDsWereHandedOut(t *testing.T) {
	docs := tiedDocs()
	reassigned := make([]intentrank.Doc, 0, len(docs))
	for i, doc := range docs {
		doc.NodeID = uint(100 - i)
		reassigned = append(reassigned, doc)
	}

	first := namesInAnswerOrder(rank(t, "what keeps the queue draining", len(docs), docs...), docs)
	second := namesInAnswerOrder(rank(t, "what keeps the queue draining", len(reassigned), reassigned...), reassigned)
	if !slices.Equal(first, second) {
		t.Fatalf("answer is %v with one set of ids and %v with another", first, second)
	}
}

// tiedDocs is three declarations whose recorded reason is byte-identical, so all
// three score the same and only the tie-break decides the order.
func tiedDocs() []intentrank.Doc {
	same := "keep the queue draining under backpressure"
	return []intentrank.Doc{
		{NodeID: 7, Content: same, FilePath: "queue/drain.go", QualifiedName: "queue.Drain", Kind: graph.NodeKindFunction},
		{NodeID: 3, Content: same, FilePath: "queue/worker.go", QualifiedName: "queue.Worker", Kind: graph.NodeKindFunction},
		{NodeID: 5, Content: same, FilePath: "queue/pump.go", QualifiedName: "queue.Pump", Kind: graph.NodeKindFunction},
	}
}

// namesInAnswerOrder reads an answer back as qualified names, which stay the
// same across two seedings while the ids do not.
func namesInAnswerOrder(answer []uint, docs []intentrank.Doc) []string {
	names := make([]string, 0, len(answer))
	for _, id := range answer {
		for _, doc := range docs {
			if doc.NodeID == id {
				names = append(names, doc.QualifiedName)
				break
			}
		}
	}
	return names
}

// A short Latin term matches whole words only. `get` reaching `getAnnotation`
// spelled inside prose is the precision leak the sanitizer closed, and the Go
// scorer has to close it the same way or it reopens one layer down.
func TestRank_ShortLatinTermMatchesWholeWordsOnly(t *testing.T) {
	docs := []intentrank.Doc{
		{NodeID: 1, Content: "getAnnotation returns the recorded reason for a node"},
	}
	if got := rank(t, "why does an invoice get a discount", 1, docs...); len(got) != 0 {
		t.Fatalf("got %v, want nothing: no term of the question is written in that reason", got)
	}
}

// A long term still matches by prefix, which is what lets one question reach the
// plural, the gerund, and every other inflection of the same word.
func TestRank_LongTermStillMatchesByPrefix(t *testing.T) {
	docs := []intentrank.Doc{
		{NodeID: 1, Content: "isolate namespaces so one repository cannot read another"},
	}
	if got := rank(t, "how are namespace boundaries enforced", 1, docs...); len(got) != 1 {
		t.Fatalf("got %v, want the namespaces reason to answer a question about namespace", got)
	}
}

// Korean glues the particle onto the noun, so "네임스페이스가" is one token and a
// prefix is the only way a question about "네임스페이스" can reach it.
func TestRank_MatchesAKoreanNounCarryingAParticle(t *testing.T) {
	docs := []intentrank.Doc{
		{NodeID: 1, Content: "네임스페이스가 서로 섞이지 않도록 격리한다"},
	}
	if got := rank(t, "네임스페이스 격리는 어떻게 하지", 1, docs...); len(got) != 1 {
		t.Fatalf("got %v, want the Korean reason to answer a Korean question", got)
	}
}

// A document no term of the question is written in is not a weak answer, it is
// not an answer. Returning it would make "nobody recorded this" look the same as
// "here is something", which is the one thing this tool cannot blur.
func TestRank_DropsDocumentsNoTermReaches(t *testing.T) {
	docs := []intentrank.Doc{
		{NodeID: 1, Content: "verify the signature so a push from anywhere else is rejected"},
		{NodeID: 2, Content: "pick a leader before the scheduler starts assigning work"},
	}
	got := rank(t, "why do we verify the signature on a push", 2, docs...)
	if !slices.Equal(got, []uint{1}) {
		t.Fatalf("got %v, want only node 1", got)
	}
}

// The caller asked for a bounded answer and gets one.
func TestRank_TruncatesToLimit(t *testing.T) {
	var docs []intentrank.Doc
	for i := 1; i <= 10; i++ {
		docs = append(docs, intentrank.Doc{NodeID: uint(i), Content: "drain the queue under backpressure"})
	}
	got := intentrank.Rank("what drains the queue", docs, 10, 3)
	if len(got.Matches) != 3 {
		t.Fatalf("got %d ids, want 3", len(got.Matches))
	}
}

// A ranked list on its own cannot be judged. Twenty files that all matched one
// very common word look exactly like twenty files that each matched a rare one,
// so the answer has to say which words did the matching.
func TestRank_ReportsWhichTermsReachedEachDocument(t *testing.T) {
	docs := []intentrank.Doc{
		{NodeID: 1, Content: "quarantine a repository whose sync keeps failing"},
		{NodeID: 2, Content: "sync something else entirely"},
	}
	result := intentrank.Rank("why does a sync quarantine a repository", docs, 2, 2)
	if !slices.Equal(ids(result), []uint{1, 2}) {
		t.Fatalf("answer order is %v, want node 1 then node 2", ids(result))
	}
	if !slices.Equal(result.Matches[0].Terms, []string{"sync", "quarantine", "repository"}) {
		t.Errorf("node 1 matched %v, want every term of the question", result.Matches[0].Terms)
	}
	if !slices.Equal(result.Matches[1].Terms, []string{"sync"}) {
		t.Errorf("node 2 matched %v, want the one common word alone", result.Matches[1].Terms)
	}
}

// How common a word is has to come back with the answer, because that is the
// number that says whether a match means anything. A word nobody wrote down is
// reported too: "quarantine appears in no recorded reason" is the answer to why
// the question was not really answered.
func TestRank_ReportsHowManyReasonsHoldEachTerm(t *testing.T) {
	docs := []intentrank.Doc{
		{NodeID: 1, Content: "sync the repository on every push"},
		{NodeID: 2, Content: "sync something else entirely"},
	}
	result := intentrank.Rank("why does a sync quarantine a repository", docs, 40, 2)
	if result.Corpus != 40 {
		t.Errorf("corpus is %d, want the 40 recorded reasons the counts are out of", result.Corpus)
	}
	want := []intentrank.Term{
		{Text: "sync", InReasons: 2},
		{Text: "quarantine", InReasons: 0},
		{Text: "repository", InReasons: 1},
	}
	if !slices.Equal(result.Terms, want) {
		t.Errorf("terms are %+v, want %+v", result.Terms, want)
	}
}

// A question made entirely of function words has nothing to score with, and a
// question with no documents behind it has nothing to score.
func TestRank_AnswersNothingWithoutTermsOrDocuments(t *testing.T) {
	docs := []intentrank.Doc{{NodeID: 1, Content: "drain the queue"}}
	if got := rank(t, "why does it", 1, docs...); len(got) != 0 {
		t.Errorf("got %v, want nothing from a question of function words alone", got)
	}
	if got := intentrank.Rank("what drains the queue", nil, 0, 5); len(got.Matches) != 0 {
		t.Errorf("got %v, want nothing from an empty candidate set", got)
	}
}

// MatchesByPrefix is the rule the query sanitizers and this scorer have to agree
// on: if the index matched a term one way and the scorer scores it the other,
// the ranking is measuring a different query than the one that ran.
func TestMatchesByPrefix_SplitsOnLengthExceptOutsideASCII(t *testing.T) {
	for _, tc := range []struct {
		term string
		want bool
	}{
		{"get", false},
		{"why", false},
		{"sync", true},
		{"namespace", true},
		{"락", true},
		{"네임스페이스", true},
	} {
		if got := intentrank.MatchesByPrefix(tc.term); got != tc.want {
			t.Errorf("MatchesByPrefix(%q) = %v, want %v", tc.term, got, tc.want)
		}
	}
}

// A node whose author wrote two reasons is still one declaration. The index
// holds one document per reason, so a question touching both reaches the same
// node twice, and a list that names it twice tells the reader there are two
// answers when there is one.
func TestRank_AnswersOnceForANodeIndexedUnderSeveralReasons(t *testing.T) {
	docs := []intentrank.Doc{
		{NodeID: 1, Content: "quarantine a repository whose sync keeps failing"},
		{NodeID: 1, Content: "a quarantined repository stays out of every later sync"},
		{NodeID: 2, Content: "sync something else entirely"},
	}
	got := rank(t, "why does a sync quarantine a repository", 3, docs...)
	if !slices.Equal(got, []uint{1, 2}) {
		t.Fatalf("got %v, want node 1 once and then node 2", got)
	}
}

// The terms are what a reader judges an answer by, and they are per node, not
// per reason. A node whose @intent holds one word of the question and whose
// @domainRule holds another matched on both, and reporting either one alone
// understates the evidence that put it on the page.
func TestRank_CombinesTheTermsMatchedAcrossSeveralReasons(t *testing.T) {
	docs := []intentrank.Doc{
		{NodeID: 1, Content: "keep every sync bounded"},
		{NodeID: 1, Content: "a repository is quarantined after three failures"},
	}
	result := intentrank.Rank("why does a sync quarantine a repository", docs, 2, 5)
	if len(result.Matches) != 1 {
		t.Fatalf("got %d matches, want the one node", len(result.Matches))
	}
	want := []string{"sync", "quarantine", "repository"}
	if !slices.Equal(result.Matches[0].Terms, want) {
		t.Errorf("matched terms are %v, want %v: every term reached the node, across two reasons", result.Matches[0].Terms, want)
	}
}

// The caller asks for a number of answers, and an answer is a declaration. If
// the limit counted reasons, a node carrying three of them would eat three of
// the caller's slots and the page would be shorter than the page it asked for.
func TestRank_LimitCountsNodesNotReasons(t *testing.T) {
	docs := []intentrank.Doc{
		{NodeID: 1, Content: "drain the queue under backpressure"},
		{NodeID: 1, Content: "the queue drains oldest first"},
		{NodeID: 1, Content: "a drained queue is never re-entered"},
		{NodeID: 2, Content: "drain the queue on shutdown"},
	}
	result := intentrank.Rank("what drains the queue", docs, 4, 2)
	if !slices.Equal(ids(result), []uint{1, 2}) {
		t.Fatalf("got %v, want two nodes: the limit counts declarations, not reasons", ids(result))
	}
}

// Writing more reasons down must not cost a node the question it does answer.
// All three declarations record the same @intent; the middle one also records
// three domain rules the question says nothing about. Scored per reason, the
// rules it did not match are simply other documents, and the three tie.
func TestRank_ExtraReasonsDoNotOutweighTheOneThatMatched(t *testing.T) {
	intent := "verify the signature so a push from anywhere else is rejected"
	docs := []intentrank.Doc{
		{NodeID: 1, Content: intent},
		{NodeID: 2, Content: intent},
		{NodeID: 2, Content: "a rejected delivery is retried at most five times before it is dropped"},
		{NodeID: 2, Content: "the retry delay doubles each attempt and never exceeds one hour"},
		{NodeID: 2, Content: "a dropped delivery is recorded in the audit log with its last error"},
		{NodeID: 3, Content: intent},
	}
	got := rank(t, "why do we verify the signature on a push", 6, docs...)
	if !slices.Equal(got, []uint{1, 2, 3}) {
		t.Fatalf("got %v, want 1 2 3: node 2 recorded three more rules and must still tie on the intent it shares", got)
	}
}

// A question is answered better by a declaration whose reasons cover all of it
// than by one that covers half, and it must not matter that the covering words
// were written in two reasons rather than one. Scoring a node on its single best
// reason threw the other half away: the node that answered the whole question
// scored the same as the node that answered part of it.
func TestRank_CoveringMoreOfTheQuestionOutranksCoveringLess(t *testing.T) {
	docs := []intentrank.Doc{
		// Both words of the question, split across two long reasons.
		{NodeID: 1, Content: "rotate the webhook secret without dropping any delivery that is already in flight"},
		{NodeID: 1, Content: "the previous secret stays valid for one overlap window before it stops being accepted"},
		// One word of the question, in a reason short enough to score higher on
		// that word alone than either of node 1's reasons does.
		{NodeID: 2, Content: "rotate the signing key"},
		// Filler, so neither word of the question looks unique to one reason.
		{NodeID: 3, Content: "rotate the log files once a day"},
		{NodeID: 4, Content: "the overlap between two scans is discarded"},
		{NodeID: 5, Content: "rotate the cached credentials before they expire"},
		{NodeID: 6, Content: "an overlap in file ranges means the same line was read twice"},
	}
	got := rank(t, "rotate overlap", 6, docs...)
	if len(got) == 0 || got[0] != 1 {
		t.Fatalf("got %v, want node 1 first: its two reasons together hold both words of the question", got)
	}
}
