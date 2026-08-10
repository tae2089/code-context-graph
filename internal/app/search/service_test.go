package search

import (
	"context"
	"slices"
	"testing"

	"github.com/tae2089/trace"

	"github.com/tae2089/code-context-graph/internal/app/search/evidence"
	"github.com/tae2089/code-context-graph/internal/app/search/intent"
	searchrank "github.com/tae2089/code-context-graph/internal/app/search/rank"
	requestctx "github.com/tae2089/code-context-graph/internal/ctx"
	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

// fakeSearcher records the fetch it was asked for and answers from a canned
// pool, keyed by the namespace on the context so federated tests can vary
// answers per repository.
type fakeSearcher struct {
	byNamespace       map[string][]graph.Node
	intentByNamespace map[string][]intent.Hit
	// intentTerms is what the fake's whole intent index "knows": every scored
	// term of the question with its reason count, as the real backend reports.
	intentTerms    []intent.Term
	intentErr      error
	gotQuery       string
	gotLimit       int
	gotIntentQuery string
	gotIntentLimit int
}

func (f *fakeSearcher) Query(ctx context.Context, query string, limit int) ([]graph.Node, error) {
	f.gotQuery = query
	f.gotLimit = limit
	return f.byNamespace[requestctx.FromContext(ctx)], nil
}

func (f *fakeSearcher) QueryIntent(ctx context.Context, query string, limit int) (intent.Result, error) {
	f.gotIntentQuery = query
	f.gotIntentLimit = limit
	if f.intentErr != nil {
		return intent.Result{}, f.intentErr
	}
	return intent.Result{Hits: f.intentByNamespace[requestctx.FromContext(ctx)], Terms: f.intentTerms}, nil
}

func node(id uint, name, path string) graph.Node {
	return graph.Node{ID: id, Name: name, QualifiedName: name, FilePath: path, Kind: "function"}
}

func TestSearch_OverfetchesThenCutsToLimit(t *testing.T) {
	pool := []graph.Node{
		node(1, "alpha", "a/alpha.go"),
		node(2, "alphaHelper", "b/beta.go"),
		node(3, "alphaThing", "c/gamma.go"),
	}
	searcher := &fakeSearcher{byNamespace: map[string][]graph.Node{requestctx.DefaultNamespace: pool}}
	svc := New(searcher)

	ctx := requestctx.WithNamespace(context.Background(), requestctx.DefaultNamespace)
	list, err := svc.Search(ctx, Params{Query: "alpha", Limit: 2})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if searcher.gotLimit != searchrank.FetchLimit(2) {
		t.Errorf("fetch limit = %d, want the over-fetch %d", searcher.gotLimit, searchrank.FetchLimit(2))
	}
	if len(list.Files) != 2 {
		t.Fatalf("got %d files, want 2 (limit)", len(list.Files))
	}
	if list.OverflowFiles != 1 {
		t.Errorf("OverflowFiles = %d, want 1", list.OverflowFiles)
	}
}

func TestSearch_FiltersByPathPrefixBeforeRanking(t *testing.T) {
	pool := []graph.Node{
		node(1, "alpha", "keep/alpha.go"),
		node(2, "alphaHelper", "drop/beta.go"),
	}
	searcher := &fakeSearcher{byNamespace: map[string][]graph.Node{requestctx.DefaultNamespace: pool}}
	svc := New(searcher)

	ctx := requestctx.WithNamespace(context.Background(), requestctx.DefaultNamespace)
	list, err := svc.Search(ctx, Params{Query: "alpha", Limit: 10, PathPrefix: "keep"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(list.Files) != 1 || list.Files[0].FilePath != "keep/alpha.go" {
		t.Fatalf("got %+v, want only keep/alpha.go", list.Files)
	}
}

func TestSearchFederated_StampsNamespacesAndKeepsEveryRepositoryHeard(t *testing.T) {
	searcher := &fakeSearcher{byNamespace: map[string][]graph.Node{
		"repo-a": {
			node(1, "alpha", "a/one.go"),
			node(2, "alphaTwo", "a/two.go"),
			node(3, "alphaThree", "a/three.go"),
		},
		"repo-b": {
			node(4, "alpha", "b/one.go"),
		},
	}}
	svc := New(searcher)

	list, err := svc.SearchFederated(context.Background(), []string{"repo-a", "repo-b"}, Params{Query: "alpha", Limit: 2})
	if err != nil {
		t.Fatalf("SearchFederated: %v", err)
	}
	if len(list.Files) != 2 {
		t.Fatalf("got %d files, want limit 2", len(list.Files))
	}
	seen := map[string]bool{}
	for _, f := range list.Files {
		if f.Namespace == "" {
			t.Errorf("file %s lost its namespace", f.FilePath)
		}
		seen[f.Namespace] = true
	}
	if !seen["repo-a"] || !seen["repo-b"] {
		t.Errorf("quota did not keep both repositories on the page: %v", seen)
	}
	if list.OverflowFiles != 2 {
		t.Errorf("OverflowFiles = %d, want 2", list.OverflowFiles)
	}
}

// annotatedNode carries the @intent tag QueryIntent's hydration would have
// preloaded, so the merged hit can read its recorded reason back.
func annotatedNode(id uint, name, path, reason string) graph.Node {
	n := node(id, name, path)
	n.Annotation = &graph.Annotation{Tags: []graph.DocTag{{Kind: graph.TagIntent, Value: reason}}}
	return n
}

func TestSearch_MergesIntentHitsWithoutFusion(t *testing.T) {
	searcher := &fakeSearcher{
		byNamespace: map[string][]graph.Node{requestctx.DefaultNamespace: {
			node(1, "alpha", "a/alpha.go"),
		}},
		intentByNamespace: map[string][]intent.Hit{requestctx.DefaultNamespace: {
			{Node: annotatedNode(2, "admitRepo", "b/admission.go", "decide which push may build"), Terms: []string{"push", "build"}},
		}},
	}
	svc := New(searcher)

	ctx := requestctx.WithNamespace(context.Background(), requestctx.DefaultNamespace)
	list, err := svc.Search(ctx, Params{Query: "alpha", Limit: 10})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if searcher.gotIntentQuery != "alpha" || searcher.gotIntentLimit != searchrank.FetchLimit(10) {
		t.Errorf("intent query = (%q, %d), want (%q, %d)", searcher.gotIntentQuery, searcher.gotIntentLimit, "alpha", searchrank.FetchLimit(10))
	}
	if len(list.Files) != 2 {
		t.Fatalf("got %d files, want the name hit and the intent hit: %+v", len(list.Files), list.Files)
	}
	// No fusion: the name-ranked pool keeps its order, intent-only hits follow it.
	if list.Files[0].FilePath != "a/alpha.go" || list.Files[1].FilePath != "b/admission.go" {
		t.Errorf("file order = [%s, %s], want the name hit first", list.Files[0].FilePath, list.Files[1].FilePath)
	}
	hit := list.Files[1].Hits[0]
	if !slices.Contains(hit.Matched, evidence.MatchIntent) {
		t.Errorf("Matched = %v, want %q", hit.Matched, evidence.MatchIntent)
	}
	if hit.Reason != "decide which push may build" {
		t.Errorf("Reason = %q, want the recorded reason", hit.Reason)
	}
	if want := []string{"push", "build"}; !slices.Equal(hit.MatchedTerms, want) {
		t.Errorf("MatchedTerms = %v, want %v", hit.MatchedTerms, want)
	}
}

func TestSearch_AttachesIntentEvidenceToANameHitWithoutDuplicatingIt(t *testing.T) {
	shared := annotatedNode(1, "alpha", "a/alpha.go", "answer alpha requests")
	searcher := &fakeSearcher{
		byNamespace:       map[string][]graph.Node{requestctx.DefaultNamespace: {shared}},
		intentByNamespace: map[string][]intent.Hit{requestctx.DefaultNamespace: {{Node: shared, Terms: []string{"alpha"}}}},
	}
	svc := New(searcher)

	ctx := requestctx.WithNamespace(context.Background(), requestctx.DefaultNamespace)
	list, err := svc.Search(ctx, Params{Query: "alpha", Limit: 10})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(list.Files) != 1 || len(list.Files[0].Hits) != 1 {
		t.Fatalf("got %+v, want exactly one hit for the shared node", list.Files)
	}
	hit := list.Files[0].Hits[0]
	if hit.Reason == "" || len(hit.MatchedTerms) == 0 {
		t.Errorf("intent evidence lost on the name hit: Reason=%q MatchedTerms=%v", hit.Reason, hit.MatchedTerms)
	}
}

func TestSearch_DropsIntentHitsWhenTheReasonsCannotAnswerTheQuestion(t *testing.T) {
	// Three of the question's four words were never written in any reason: the
	// corpus cannot answer it, so the one common shared word ("symbol") does
	// not put anything on the page.
	searcher := &fakeSearcher{
		byNamespace: map[string][]graph.Node{requestctx.DefaultNamespace: nil},
		intentByNamespace: map[string][]intent.Hit{requestctx.DefaultNamespace: {
			{Node: annotatedNode(2, "addName", "b/resolve.go", "index every symbol by name"), Terms: []string{"symbol"}},
		}},
		intentTerms: []intent.Term{
			{Text: "zzz", InReasons: 0}, {Text: "nonexistent", InReasons: 0},
			{Text: "symbol", InReasons: 52}, {Text: "qqq", InReasons: 0},
		},
	}
	svc := New(searcher)

	ctx := requestctx.WithNamespace(context.Background(), requestctx.DefaultNamespace)
	list, err := svc.Search(ctx, Params{Query: "zzz nonexistent symbol qqq", Limit: 10})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(list.Files) != 0 {
		t.Fatalf("got %+v, want no results for a question the reasons cannot answer", list.Files)
	}
}

func TestSearch_KeepsIntentHitsWhenTheReasonsSpeakTheQuestionsTerms(t *testing.T) {
	// Every scored term is written in some reason: the question is answerable,
	// so a hit that matched the words that mattered stays even though it did
	// not match all of them.
	searcher := &fakeSearcher{
		byNamespace: map[string][]graph.Node{requestctx.DefaultNamespace: nil},
		intentByNamespace: map[string][]intent.Hit{requestctx.DefaultNamespace: {
			{Node: annotatedNode(2, "admitRepo", "b/admission.go", "decide which push may build"), Terms: []string{"push", "build"}},
		}},
		intentTerms: []intent.Term{
			{Text: "push", InReasons: 12}, {Text: "trigger", InReasons: 8}, {Text: "build", InReasons: 40},
		},
	}
	svc := New(searcher)

	ctx := requestctx.WithNamespace(context.Background(), requestctx.DefaultNamespace)
	list, err := svc.Search(ctx, Params{Query: "which push may trigger a build", Limit: 10})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(list.Files) != 1 || list.Files[0].FilePath != "b/admission.go" {
		t.Fatalf("got %+v, want the intent hit kept for an answerable question", list.Files)
	}
}

func TestSearch_PathPrefixFiltersIntentHitsToo(t *testing.T) {
	searcher := &fakeSearcher{
		byNamespace: map[string][]graph.Node{requestctx.DefaultNamespace: {node(1, "alpha", "keep/alpha.go")}},
		intentByNamespace: map[string][]intent.Hit{requestctx.DefaultNamespace: {
			{Node: annotatedNode(2, "other", "drop/other.go", "alpha related"), Terms: []string{"alpha"}},
		}},
	}
	svc := New(searcher)

	ctx := requestctx.WithNamespace(context.Background(), requestctx.DefaultNamespace)
	list, err := svc.Search(ctx, Params{Query: "alpha", Limit: 10, PathPrefix: "keep"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(list.Files) != 1 || list.Files[0].FilePath != "keep/alpha.go" {
		t.Fatalf("got %+v, want only keep/alpha.go", list.Files)
	}
}

func TestSearch_IntentQueryErrorFailsTheSearch(t *testing.T) {
	searcher := &fakeSearcher{
		byNamespace: map[string][]graph.Node{requestctx.DefaultNamespace: {node(1, "alpha", "a/alpha.go")}},
		intentErr:   trace.New("intent index gone"),
	}
	svc := New(searcher)

	ctx := requestctx.WithNamespace(context.Background(), requestctx.DefaultNamespace)
	if _, err := svc.Search(ctx, Params{Query: "alpha", Limit: 10}); err == nil {
		t.Error("a failing intent query was silently swallowed")
	}
}

func TestSearchFederated_StampsNamespaceOnIntentHits(t *testing.T) {
	searcher := &fakeSearcher{
		byNamespace: map[string][]graph.Node{"repo-a": {node(1, "alpha", "a/one.go")}},
		intentByNamespace: map[string][]intent.Hit{"repo-b": {
			{Node: annotatedNode(1, "admit", "b/admission.go", "decide which alpha push may build"), Terms: []string{"alpha"}},
		}},
	}
	svc := New(searcher)

	list, err := svc.SearchFederated(context.Background(), []string{"repo-a", "repo-b"}, Params{Query: "alpha", Limit: 10})
	if err != nil {
		t.Fatalf("SearchFederated: %v", err)
	}
	if len(list.Files) != 2 {
		t.Fatalf("got %d files, want one per repository: %+v", len(list.Files), list.Files)
	}
	var intentFile *evidence.File
	for i := range list.Files {
		if list.Files[i].FilePath == "b/admission.go" {
			intentFile = &list.Files[i]
		}
	}
	if intentFile == nil {
		t.Fatalf("intent hit missing from the federated answer: %+v", list.Files)
	}
	if intentFile.Namespace != "repo-b" {
		t.Errorf("intent hit namespace = %q, want repo-b", intentFile.Namespace)
	}
	// Same node id as repo-a's hit: the evidence must stay on repo-b's node.
	if hit := intentFile.Hits[0]; hit.Reason == "" {
		t.Errorf("intent evidence lost across namespaces: %+v", hit)
	}
}

func TestSearch_NilServiceOrSearcherFailsLoudly(t *testing.T) {
	var svc *Service
	if _, err := svc.Search(context.Background(), Params{Query: "q", Limit: 1}); err == nil {
		t.Error("nil service answered instead of failing")
	}
	if _, err := New(nil).Search(context.Background(), Params{Query: "q", Limit: 1}); err == nil {
		t.Error("nil searcher answered instead of failing")
	}
}
