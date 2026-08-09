package search

import (
	"context"
	"testing"

	searchrank "github.com/tae2089/code-context-graph/internal/app/search/rank"
	requestctx "github.com/tae2089/code-context-graph/internal/ctx"
	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

// fakeSearcher records the fetch it was asked for and answers from a canned
// pool, keyed by the namespace on the context so federated tests can vary
// answers per repository.
type fakeSearcher struct {
	byNamespace map[string][]graph.Node
	gotQuery    string
	gotLimit    int
}

func (f *fakeSearcher) Query(ctx context.Context, query string, limit int) ([]graph.Node, error) {
	f.gotQuery = query
	f.gotLimit = limit
	return f.byNamespace[requestctx.FromContext(ctx)], nil
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

func TestSearch_NilServiceOrSearcherFailsLoudly(t *testing.T) {
	var svc *Service
	if _, err := svc.Search(context.Background(), Params{Query: "q", Limit: 1}); err == nil {
		t.Error("nil service answered instead of failing")
	}
	if _, err := New(nil).Search(context.Background(), Params{Query: "q", Limit: 1}); err == nil {
		t.Error("nil searcher answered instead of failing")
	}
}
