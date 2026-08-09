package intent_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/tae2089/code-context-graph/internal/app/search/intent"
	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

type stubSearcher struct {
	nodes   []graph.Node
	matched map[uint][]string
	terms   []intent.Term
	corpus  int
	err     error
	query   string
	limit   int
}

// QueryIntent truncates to the limit it was handed, the way a real backend's
// LIMIT clause does. Without that the stub would hide the bug the file budget
// exists to fix.
func (s *stubSearcher) QueryIntent(_ context.Context, query string, limit int) (intent.Result, error) {
	s.query = query
	s.limit = limit
	if s.err != nil {
		return intent.Result{}, s.err
	}
	nodes := s.nodes
	if limit < len(nodes) {
		nodes = nodes[:limit]
	}
	result := intent.Result{Terms: s.terms, Corpus: s.corpus}
	for _, node := range nodes {
		result.Hits = append(result.Hits, intent.Hit{Node: node, Terms: s.matched[node.ID]})
	}
	return result, nil
}

type stubCoverage struct {
	coverage intent.Coverage
	err      error
	calls    int
}

func (s *stubCoverage) IntentCoverage(_ context.Context) (intent.Coverage, error) {
	s.calls++
	return s.coverage, s.err
}

func annotated(id uint, name, file, reason string, line int) graph.Node {
	node := graph.Node{ID: id, Name: name, QualifiedName: "pkg." + name, Kind: graph.NodeKindFunction, FilePath: file, StartLine: line}
	if reason != "" {
		node.Annotation = &graph.Annotation{NodeID: id, Tags: []graph.DocTag{{Kind: graph.TagIntent, Value: reason}}}
	}
	return node
}

// The caller asked a question to find where to start reading, so the answer is
// shaped like a reading list: which files, and which declarations inside them.
// The order the index returned is the only ranking there is, so the first file
// must be the file holding the best-matching node, and a file must not be split
// into two entries because a lower-ranked node in it came back later.
func TestFind_GroupsAnswersByFileInRankOrder(t *testing.T) {
	searcher := &stubSearcher{nodes: []graph.Node{
		annotated(1, "handle", "webhook/handle.go", "verify the signature so a push from anywhere else is rejected", 12),
		annotated(2, "parse", "webhook/parse.go", "accept both forge payload shapes", 30),
		annotated(3, "secret", "webhook/handle.go", "read the shared secret once at startup", 90),
	}}
	service := intent.New(searcher, &stubCoverage{})

	answer, err := service.Find(context.Background(), "why do we verify the signature", 10)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if len(answer.Files) != 2 {
		t.Fatalf("got %d files, want 2", len(answer.Files))
	}
	if answer.Files[0].FilePath != "webhook/handle.go" {
		t.Errorf("first file = %q, want webhook/handle.go", answer.Files[0].FilePath)
	}
	if got := len(answer.Files[0].Entries); got != 2 {
		t.Fatalf("webhook/handle.go has %d entries, want 2", got)
	}
	first := answer.Files[0].Entries[0]
	if first.NodeID != 1 {
		t.Errorf("first entry node id = %d, want 1", first.NodeID)
	}
	if first.Reason != "verify the signature so a push from anywhere else is rejected" {
		t.Errorf("first entry reason = %q", first.Reason)
	}
	if first.Line != 12 {
		t.Errorf("first entry line = %d, want 12", first.Line)
	}
	if answer.Files[0].Entries[1].NodeID != 3 {
		t.Errorf("second entry in the first file = %d, want 3", answer.Files[0].Entries[1].NodeID)
	}
	if answer.Files[1].FilePath != "webhook/parse.go" {
		t.Errorf("second file = %q, want webhook/parse.go", answer.Files[1].FilePath)
	}
}

// Nothing found and nothing written down look identical from the outside, and
// the caller has to be able to tell them apart before deciding whether to give
// up on the question or go read the code by hand. Coverage is what tells them.
func TestFind_ReportsCoverageWhenNoReasonAnswers(t *testing.T) {
	coverage := &stubCoverage{coverage: intent.Coverage{NodesWithReason: 1646, NodesTotal: 3504}}
	service := intent.New(&stubSearcher{}, coverage)

	answer, err := service.Find(context.Background(), "how does the scheduler pick a leader", 10)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if len(answer.Files) != 0 {
		t.Fatalf("got %d files, want none", len(answer.Files))
	}
	if answer.Coverage.NodesWithReason != 1646 || answer.Coverage.NodesTotal != 3504 {
		t.Errorf("coverage = %+v, want 1646/3504", answer.Coverage)
	}
}

// Coverage is not only an excuse for an empty answer. Three files back out of a
// namespace where half the code has no recorded reason is a partial answer, and
// the caller cannot know that unless it is reported on hits too.
func TestFind_ReportsCoverageAlongsideHits(t *testing.T) {
	coverage := &stubCoverage{coverage: intent.Coverage{NodesWithReason: 10, NodesTotal: 100}}
	searcher := &stubSearcher{nodes: []graph.Node{annotated(1, "handle", "webhook/handle.go", "verify the signature", 12)}}
	service := intent.New(searcher, coverage)

	answer, err := service.Find(context.Background(), "why verify", 10)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if coverage.calls != 1 {
		t.Errorf("coverage read %d times, want 1", coverage.calls)
	}
	if answer.Coverage.NodesTotal != 100 {
		t.Errorf("coverage = %+v, want 10/100", answer.Coverage)
	}
}

// A node reaches the intent index only because a reason was recorded for it, so
// one coming back without a reason means the index and the graph disagree.
// Showing it with a blank line where the answer goes is worse than not showing
// it: the caller opens the file and finds nothing was ever written.
func TestFind_DropsNodesThatCarryNoReason(t *testing.T) {
	searcher := &stubSearcher{nodes: []graph.Node{
		annotated(1, "stale", "webhook/stale.go", "", 5),
		annotated(2, "handle", "webhook/handle.go", "verify the signature", 12),
	}}
	service := intent.New(searcher, &stubCoverage{})

	answer, err := service.Find(context.Background(), "why verify", 10)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if len(answer.Files) != 1 {
		t.Fatalf("got %d files, want 1", len(answer.Files))
	}
	if answer.Files[0].FilePath != "webhook/handle.go" {
		t.Errorf("file = %q, want webhook/handle.go", answer.Files[0].FilePath)
	}
}

// The index takes @intent and @domainRule, so a node carrying only a domain rule
// can win a question outright. Reading back only @intent would drop exactly that
// node as if it had no reason, and the rule it recorded is the answer.
func TestFind_AnswersFromADomainRuleWhenThatIsAllThatWasRecorded(t *testing.T) {
	ruled := graph.Node{ID: 7, Name: "charge", QualifiedName: "billing.charge", Kind: graph.NodeKindFunction, FilePath: "billing/charge.go", StartLine: 40}
	ruled.Annotation = &graph.Annotation{NodeID: 7, Tags: []graph.DocTag{
		{Kind: graph.TagDomainRule, Value: "a refund never exceeds the original charge"},
	}}
	service := intent.New(&stubSearcher{nodes: []graph.Node{ruled}}, &stubCoverage{})

	answer, err := service.Find(context.Background(), "why can a refund be rejected", 10)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if len(answer.Files) != 1 {
		t.Fatalf("got %d files, want 1", len(answer.Files))
	}
	if got := answer.Files[0].Entries[0].Reason; got != "a refund never exceeds the original charge" {
		t.Errorf("reason = %q, want the recorded domain rule", got)
	}
}

// A file list on its own cannot be judged. Twenty files that each matched one
// word written in half the codebase read exactly like twenty files that matched
// a rare one, so every declaration says which words of the question reached it.
func TestFind_ReportsWhichTermsReachedEachDeclaration(t *testing.T) {
	searcher := &stubSearcher{
		nodes: []graph.Node{
			annotated(1, "handle", "webhook/handle.go", "verify the signature so a push from anywhere else is rejected", 12),
			annotated(2, "parse", "webhook/parse.go", "accept both forge payload shapes", 30),
		},
		matched: map[uint][]string{1: {"verify", "signature"}, 2: {"payload"}},
	}
	service := intent.New(searcher, &stubCoverage{})

	answer, err := service.Find(context.Background(), "why do we verify the signature payload", 10)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if got := answer.Files[0].Entries[0].MatchedTerms; !slices.Equal(got, []string{"verify", "signature"}) {
		t.Errorf("first entry matched %v, want verify and signature", got)
	}
	if got := answer.Files[1].Entries[0].MatchedTerms; !slices.Equal(got, []string{"payload"}) {
		t.Errorf("second entry matched %v, want payload alone", got)
	}
}

// Knowing which word matched is only half of it: the reader also needs to know
// whether that word means anything here. A word written in most recorded reasons
// picked the file almost at random, and a word written in none is why the answer
// is thin. Both numbers ride along with the answer instead of being turned into
// a cutoff, because a cutoff would be a number fitted to one codebase.
func TestFind_ReportsHowCommonEachQuestionTermIs(t *testing.T) {
	searcher := &stubSearcher{
		nodes:  []graph.Node{annotated(1, "handle", "webhook/handle.go", "verify the signature", 12)},
		terms:  []intent.Term{{Text: "verify", InReasons: 40}, {Text: "quarantine", InReasons: 0}},
		corpus: 1751,
	}
	service := intent.New(searcher, &stubCoverage{})

	answer, err := service.Find(context.Background(), "why do we verify a quarantine", 10)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	want := []intent.Term{{Text: "verify", InReasons: 40}, {Text: "quarantine", InReasons: 0}}
	if !slices.Equal(answer.Terms, want) {
		t.Errorf("terms = %+v, want %+v", answer.Terms, want)
	}
	if answer.ReasonsSearched != 1751 {
		t.Errorf("reasons searched = %d, want the 1751 the counts are out of", answer.ReasonsSearched)
	}
}

func TestFind_PropagatesSearchFailure(t *testing.T) {
	boom := errors.New("index unavailable")
	service := intent.New(&stubSearcher{err: boom}, &stubCoverage{})

	if _, err := service.Find(context.Background(), "why verify", 10); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want %v", err, boom)
	}
}

// A caller that passes no limit still gets an answer rather than an empty one,
// which is what a limit of 0 would mean to the backend.
func TestFind_AppliesDefaultLimit(t *testing.T) {
	searcher := &stubSearcher{}
	service := intent.New(searcher, &stubCoverage{})

	if _, err := service.Find(context.Background(), "why verify", 0); err != nil {
		t.Fatalf("Find: %v", err)
	}
	if want := intent.DefaultLimit * intent.NodesPerFile; searcher.limit != want {
		t.Errorf("limit = %d, want %d", searcher.limit, want)
	}
}

// The limit counts files, because a file is what the caller picks from. Counting
// nodes let one talkative file eat the whole budget: on PostgreSQL the question
// "what decides which repositories and branches are allowed to sync" came back
// with nine files and admission.go was not among them, because the declarations
// above it used up all twenty node slots before its file was ever reached.
func TestFind_LimitCountsFilesNotNodes(t *testing.T) {
	searcher := &stubSearcher{nodes: []graph.Node{
		annotated(1, "A", "a.go", "first", 1),
		annotated(2, "B", "b.go", "second", 1),
		annotated(3, "C", "c.go", "third", 1),
		annotated(4, "D", "d.go", "fourth", 1),
	}}
	service := intent.New(searcher, &stubCoverage{})

	answer, err := service.Find(context.Background(), "why verify", 2)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if len(answer.Files) != 2 {
		t.Fatalf("got %d files, want 2", len(answer.Files))
	}
	if answer.Files[0].FilePath != "a.go" || answer.Files[1].FilePath != "b.go" {
		t.Errorf("kept %q and %q, want the two best-ranked files", answer.Files[0].FilePath, answer.Files[1].FilePath)
	}
}

// A file budget only helps if retrieval reaches past the files that fill it, so
// the service asks the index for several nodes per file it intends to keep.
func TestFind_ReachesAFileWhoseOnlyHitRanksDeep(t *testing.T) {
	nodes := make([]graph.Node, 0, intent.NodesPerFile+1)
	for i := range intent.NodesPerFile {
		nodes = append(nodes, annotated(uint(i+1), "Crowd", "crowded.go", "a reason", i+1))
	}
	nodes = append(nodes, annotated(999, "Buried", "buried.go", "the reason that answers", 1))

	searcher := &stubSearcher{nodes: nodes}
	service := intent.New(searcher, &stubCoverage{})

	answer, err := service.Find(context.Background(), "why verify", 2)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if len(answer.Files) != 2 || answer.Files[1].FilePath != "buried.go" {
		t.Fatalf("got %d files ending at %q, want buried.go as the second", len(answer.Files), answer.Files[len(answer.Files)-1].FilePath)
	}
}

// Deeper retrieval would otherwise turn one crowded file into a wall of prose.
// The reader picked the file; the first few declarations in rank order are
// enough to start reading it.
func TestFind_ShowsOnlyTheBestEntriesOfAFile(t *testing.T) {
	nodes := make([]graph.Node, 0, intent.MaxEntriesPerFile+2)
	for i := range intent.MaxEntriesPerFile + 2 {
		nodes = append(nodes, annotated(uint(i+1), "Decl", "crowded.go", "a reason", i+1))
	}
	searcher := &stubSearcher{nodes: nodes}
	service := intent.New(searcher, &stubCoverage{})

	answer, err := service.Find(context.Background(), "why verify", 5)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if len(answer.Files) != 1 {
		t.Fatalf("got %d files, want 1", len(answer.Files))
	}
	if got := len(answer.Files[0].Entries); got != intent.MaxEntriesPerFile {
		t.Fatalf("got %d entries, want %d", got, intent.MaxEntriesPerFile)
	}
	if answer.Files[0].Entries[0].Line != 1 {
		t.Errorf("kept entries starting at line %d, want the best-ranked one first", answer.Files[0].Entries[0].Line)
	}
}

// An empty question cannot be answered by any recorded reason, and asking the
// backend to match on nothing would return whatever ranks first overall.
func TestFind_RefusesAnEmptyQuestion(t *testing.T) {
	searcher := &stubSearcher{}
	service := intent.New(searcher, &stubCoverage{})

	if _, err := service.Find(context.Background(), "   ", 10); err == nil {
		t.Fatal("expected an error for an empty question")
	}
	if searcher.limit != 0 {
		t.Error("the backend was queried with an empty question")
	}
}
