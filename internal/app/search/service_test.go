package search

import (
	"context"
	"fmt"
	"slices"
	"strings"
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
	intentTerms []intent.Term
	// coverageByNamespace is what the fake's recorded-reason index reports about
	// each repository as a whole, whatever the query was.
	coverageByNamespace map[string]intent.Coverage
	intentErr           error
	gotQuery            string
	gotLimit            int
	gotIntentQuery      string
	gotIntentLimit      int
}

func (f *fakeSearcher) Query(ctx context.Context, query string, limit int) ([]graph.Node, error) {
	f.gotQuery = query
	f.gotLimit = limit
	return capped(f.byNamespace[requestctx.FromContext(ctx)], limit), nil
}

func (f *fakeSearcher) QueryIntent(ctx context.Context, query string, limit int) (intent.Result, error) {
	f.gotIntentQuery = query
	f.gotIntentLimit = limit
	if f.intentErr != nil {
		return intent.Result{}, f.intentErr
	}
	ns := requestctx.FromContext(ctx)
	hits := capped(f.intentByNamespace[ns], limit)
	return intent.Result{Hits: hits, Terms: f.intentTerms, Coverage: f.coverageByNamespace[ns]}, nil
}

// capped answers with at most limit rows, the way a real backend's LIMIT does.
// Without it the fake hands back its whole canned pool however narrow the fetch
// was, and a test can never see the service ask for too few candidates.
func capped[T any](rows []T, limit int) []T {
	if limit > 0 && len(rows) > limit {
		return rows[:limit]
	}
	return rows
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

// answerableCorpus is n files that each hold one declaration the query names.
// Every declaration carries the same name and no path segment the query
// touches, so structural reranking scores them all alike and leaves the pool's
// own order alone. That keeps the growing pool's prefix stable, which is what
// lets a test walk the whole answer one page at a time.
func answerableCorpus(n int) []graph.Node {
	out := make([]graph.Node, 0, n)
	for i := range n {
		out = append(out, node(uint(i+1), "alpha", fmt.Sprintf("pkg/f%03d.go", i)))
	}
	return out
}

// reversedCorpus is files files of hitsPerFile declarations the query names,
// handed over in the order a backend whose own relevance order runs exactly
// against the structural one would hand them: the row it puts first is the row
// structural scoring puts last.
//
// The padding is what turns the order around. Every name starts with the query,
// so every file is a real answer to it, but a longer surrounding identifier
// scores lower — so the most padded name is the weakest structurally, and that
// is the row the backend offers first. No two names are padded alike, so no two
// files score alike and nothing falls through to a tie-break.
//
// This is the shape answerableCorpus cannot show. There, every file scores the
// same and the pool's own order survives reranking, so a pool that grows only
// ever grows at the end. Here reranking turns the pool around, and every row a
// wider pool adds belongs in front of the ones already delivered.
//
// hitsPerFile is how many rows one file spends of the pool. It is 1 for the
// plainest reading of the shape, and more where a test needs the page to sit at
// the far end of its own pool: the pool is five rows wide for every file asked
// for, so files of five hits put the last file of a page on the pool's last row.
func reversedCorpus(files, hitsPerFile int) []graph.Node {
	out := make([]graph.Node, 0, files*hitsPerFile)
	id := uint(1)
	for i := range files {
		padded := "alpha" + strings.Repeat("x", files-1-i)
		path := fmt.Sprintf("pkg/f%03d.go", i)
		for range hitsPerFile {
			out = append(out, node(id, padded, path))
			id++
		}
	}
	return out
}

// crowdedCorpus is one file holding hits hits, followed by quiet files of one
// hit each. The crowded file alone fills a first page's candidate pool.
func crowdedCorpus(hits, quiet int) []graph.Node {
	out := make([]graph.Node, 0, hits+quiet)
	for i := range hits {
		out = append(out, node(uint(i+1), "alpha", "pkg/crowded.go"))
	}
	for i := range quiet {
		out = append(out, node(uint(1000+i), "alpha", fmt.Sprintf("pkg/quiet%02d.go", i)))
	}
	return out
}

func TestSearch_FetchesThePoolForTheOffsetAsWellAsTheLimit(t *testing.T) {
	searcher := &fakeSearcher{byNamespace: map[string][]graph.Node{
		requestctx.DefaultNamespace: {node(1, "alpha", "a/alpha.go")},
	}}
	svc := New(searcher)

	ctx := requestctx.WithNamespace(context.Background(), requestctx.DefaultNamespace)
	if _, err := svc.Search(ctx, Params{Query: "alpha", Limit: 10, Offset: 200}); err != nil {
		t.Fatalf("Search: %v", err)
	}
	want := searchrank.FetchLimit(210)
	if searcher.gotLimit != want {
		t.Errorf("name fetch limit = %d, want %d — the pool has to cover the offset too", searcher.gotLimit, want)
	}
	if searcher.gotIntentLimit != want {
		t.Errorf("intent fetch limit = %d, want %d — the pool has to cover the offset too", searcher.gotIntentLimit, want)
	}
}

func TestSearch_DoesNotClaimCompletionWhileAnswerableFilesRemain(t *testing.T) {
	searcher := &fakeSearcher{byNamespace: map[string][]graph.Node{
		requestctx.DefaultNamespace: answerableCorpus(300),
	}}
	svc := New(searcher)

	ctx := requestctx.WithNamespace(context.Background(), requestctx.DefaultNamespace)
	list, err := svc.Search(ctx, Params{Query: "alpha", Limit: 10, Offset: 60})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(list.Files) == 0 {
		t.Fatalf("page at offset 60 came back empty (%q), but 300 files answer this query", list.Note)
	}
	if list.OverflowFiles == 0 {
		t.Errorf("OverflowFiles = 0, want the 230 files still unreached: the answer called itself complete")
	}
}

func TestSearch_PagesAThreeHundredFileAnswerToTheEnd(t *testing.T) {
	const total = 300
	searcher := &fakeSearcher{byNamespace: map[string][]graph.Node{
		requestctx.DefaultNamespace: answerableCorpus(total),
	}}
	svc := New(searcher)

	ctx := requestctx.WithNamespace(context.Background(), requestctx.DefaultNamespace)
	seen := map[string]bool{}
	offset := 0
	for page := 0; page < total; page++ {
		list, err := svc.Search(ctx, Params{Query: "alpha", Limit: 10, Offset: offset})
		if err != nil {
			t.Fatalf("Search at offset %d: %v", offset, err)
		}
		if len(list.Files) == 0 {
			t.Fatalf("page at offset %d came back empty after %d of %d files", offset, len(seen), total)
		}
		for _, f := range list.Files {
			if seen[f.FilePath] {
				t.Errorf("file %s came back on two pages", f.FilePath)
			}
			seen[f.FilePath] = true
		}
		// Exactly the call the answer's own `next` action names.
		offset += len(list.Files)
		if list.OverflowFiles == 0 {
			break
		}
	}
	if len(seen) != total {
		t.Fatalf("paging to the completion signal yielded %d files, want all %d", len(seen), total)
	}
}

// walkAnswer pages through one repository's answer with exactly the offsets the
// answer suggests, and reports how many pages each file arrived on.
func walkAnswer(t *testing.T, svc *Service, p Params, maxPages int) (seen map[string]int, repeats []string) {
	t.Helper()
	ctx := requestctx.WithNamespace(context.Background(), requestctx.DefaultNamespace)
	seen = map[string]int{}
	for range maxPages {
		list, err := svc.Search(ctx, p)
		if err != nil {
			t.Fatalf("Search at offset %d: %v", p.Offset, err)
		}
		if len(list.Files) == 0 {
			t.Fatalf("page at offset %d came back empty (%q) after %d files", p.Offset, list.Note, len(seen))
		}
		for _, f := range list.Files {
			if seen[f.FilePath] > 0 {
				repeats = append(repeats, f.FilePath)
			}
			seen[f.FilePath]++
		}
		if list.OverflowFiles == 0 && !list.PoolTruncated {
			return seen, repeats
		}
		if list.NextOffset <= p.Offset {
			t.Fatalf("the answer at offset %d suggested offset %d, which does not move", p.Offset, list.NextOffset)
		}
		// Exactly the call the answer's own `next` action names.
		p.Offset = list.NextOffset
	}
	t.Fatalf("paging did not finish in %d pages", maxPages)
	return nil, nil
}

// A backend order that runs against the structural one is where paging used to
// come apart: every row a wider pool added belonged in front of the rows already
// delivered, so the page boundary slid backwards over files that had already
// been handed out and past files that never were. Measured before the fix, this
// answer reached 250 of its 300 files and delivered 50 of them twice.
func TestSearch_PagesToTheEndWhenTheBackendOrderRunsAgainstTheStructuralOne(t *testing.T) {
	const total = 300
	svc := New(&fakeSearcher{byNamespace: map[string][]graph.Node{
		requestctx.DefaultNamespace: reversedCorpus(total, 1),
	}})

	seen, repeats := walkAnswer(t, svc, Params{Query: "alpha", Limit: 10}, total)
	if len(repeats) > 0 {
		t.Errorf("%d files came back on more than one page: %v", len(repeats), repeats)
	}
	if len(seen) != total {
		t.Errorf("paging reached %d files, want all %d", len(seen), total)
	}
}

// A page of files that each answer with several hits sits at the far end of its
// own candidate pool: the pool is five rows wide for every file asked for, so
// files of five hits put the last file of the page on the pool's last row. That
// is where the block the pool ends in starts to matter. A pool that stops in the
// middle of a block holds a block the backend has more rows for, so the next,
// wider page fills that block in and reorders it — and the files the earlier page
// cut out of the half-filled block come back on the later one.
//
// Limit 7 is a limit whose pool is not a whole number of blocks: the pool for its
// second page is 5×(7+7) = 70 rows, and the block is 50.
func TestSearch_PagesToTheEndWhenThePageReachesThePoolsLastBlock(t *testing.T) {
	const (
		files = 60
		hits  = 5
	)
	svc := New(&fakeSearcher{byNamespace: map[string][]graph.Node{
		requestctx.DefaultNamespace: reversedCorpus(files, hits),
	}})

	seen, repeats := walkAnswer(t, svc, Params{Query: "alpha", Limit: 7}, files)
	if len(repeats) > 0 {
		t.Errorf("%d files came back on more than one page: %v", len(repeats), repeats)
	}
	if len(seen) != files {
		t.Errorf("paging reached %d files, want all %d", len(seen), files)
	}
}

func TestSearch_AFileFullOfHitsDoesNotHideTheFilesBehindIt(t *testing.T) {
	searcher := &fakeSearcher{byNamespace: map[string][]graph.Node{
		requestctx.DefaultNamespace: crowdedCorpus(evidence.PageHitBudget, 10),
	}}
	svc := New(searcher)

	ctx := requestctx.WithNamespace(context.Background(), requestctx.DefaultNamespace)
	first, err := svc.Search(ctx, Params{Query: "alpha", Limit: 10})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(first.Files) != 1 || first.Files[0].FilePath != "pkg/crowded.go" {
		t.Fatalf("first page = %+v, want the crowded file alone", first.Files)
	}

	second, err := svc.Search(ctx, Params{Query: "alpha", Limit: 10, Offset: len(first.Files)})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(second.Files) == 0 {
		t.Fatalf("the page after the crowded file came back empty (%q); the quiet files are unreachable", second.Note)
	}
	if second.Files[0].FilePath != "pkg/quiet00.go" {
		t.Errorf("second page starts at %s, want pkg/quiet00.go", second.Files[0].FilePath)
	}
}

func TestSearch_ReportsAPoolCutApartFromTheFileOverflow(t *testing.T) {
	// The crowded file's hits alone fill the whole first-page pool, so the
	// answer ends at the pool's edge and not at the end of what it can answer.
	searcher := &fakeSearcher{byNamespace: map[string][]graph.Node{
		requestctx.DefaultNamespace: crowdedCorpus(evidence.PageHitBudget, 10),
	}}
	svc := New(searcher)

	ctx := requestctx.WithNamespace(context.Background(), requestctx.DefaultNamespace)
	list, err := svc.Search(ctx, Params{Query: "alpha", Limit: 10})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	// The completion signal keeps counting files, and it reached every file the
	// pool held — so the pool cut has to be said some other way.
	if list.OverflowFiles != 0 {
		t.Fatalf("OverflowFiles = %d, want 0: the page reached every file in the pool", list.OverflowFiles)
	}
	if !list.PoolTruncated {
		t.Errorf("PoolTruncated = false, but the pool came back full at %d rows", searcher.gotLimit)
	}
}

func TestSearch_ReportsNoPoolCutWhenTheBackendHadRoomToSpare(t *testing.T) {
	searcher := &fakeSearcher{byNamespace: map[string][]graph.Node{
		requestctx.DefaultNamespace: answerableCorpus(3),
	}}
	svc := New(searcher)

	ctx := requestctx.WithNamespace(context.Background(), requestctx.DefaultNamespace)
	list, err := svc.Search(ctx, Params{Query: "alpha", Limit: 10})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if list.PoolTruncated {
		t.Errorf("PoolTruncated = true, but the backend answered with 3 of the %d rows it was offered", searcher.gotLimit)
	}
}

func TestSearchFederated_FetchesEachNamespacePoolForTheOffsetToo(t *testing.T) {
	searcher := &fakeSearcher{byNamespace: map[string][]graph.Node{
		"repo-a": answerableCorpus(400),
	}}
	svc := New(searcher)

	list, err := svc.SearchFederated(context.Background(), []string{"repo-a"}, Params{Query: "alpha", Limit: 10, Offset: 60})
	if err != nil {
		t.Fatalf("SearchFederated: %v", err)
	}
	if want := searchrank.FetchLimit(70); searcher.gotLimit != want {
		t.Errorf("fetch limit = %d, want %d", searcher.gotLimit, want)
	}
	if len(list.Files) == 0 {
		t.Fatalf("page at offset 60 came back empty (%q), but 400 files answer this query", list.Note)
	}
	if !list.PoolTruncated {
		t.Errorf("PoolTruncated = false, but repo-a's pool came back full at %d rows", searcher.gotLimit)
	}
}

func TestSearch_FiltersByPathPrefix(t *testing.T) {
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

	// Limit 2 is two files per repository: repo-a shows two of its three, repo-b
	// shows its only one, and repo-a's third file is the one left over.
	list, err := svc.SearchFederated(context.Background(), []string{"repo-a", "repo-b"}, Params{Query: "alpha", Limit: 2})
	if err != nil {
		t.Fatalf("SearchFederated: %v", err)
	}
	if len(list.Files) != 3 {
		t.Fatalf("got %d files, want 3: two from repo-a and repo-b's one", len(list.Files))
	}
	seen := map[string]bool{}
	for _, f := range list.Files {
		if f.Namespace == "" {
			t.Errorf("file %s lost its namespace", f.FilePath)
		}
		seen[f.Namespace] = true
	}
	if !seen["repo-a"] || !seen["repo-b"] {
		t.Errorf("a repository with hits was left off the page: %v", seen)
	}
	if list.OverflowFiles != 1 {
		t.Errorf("OverflowFiles = %d, want 1: repo-a's third file", list.OverflowFiles)
	}
}

// federatedCorpus gives every namespace filesEach files, each holding
// hitsPerFile declarations the query names.
//
// Every declaration carries the same name, so structural reranking cannot
// separate them and ties break on the file path — which puts each namespace's
// files together, in namespace order. That is the shape that used to let the
// first repository spend the whole page before the next was heard from.
func federatedCorpus(namespaces []string, filesEach, hitsPerFile int) map[string][]graph.Node {
	byNamespace := make(map[string][]graph.Node, len(namespaces))
	id := uint(1)
	for _, ns := range namespaces {
		nodes := make([]graph.Node, 0, filesEach*hitsPerFile)
		for i := range filesEach {
			path := fmt.Sprintf("%s/f%03d.go", ns, i)
			for range hitsPerFile {
				nodes = append(nodes, node(id, "alpha", path))
				id++
			}
		}
		byNamespace[ns] = nodes
	}
	return byNamespace
}

// filesPerNamespace counts how many of a page's files each repository put there.
func filesPerNamespace(list evidence.List) map[string]int {
	counts := map[string]int{}
	for _, f := range list.Files {
		counts[f.Namespace]++
	}
	return counts
}

func TestSearchFederated_LimitIsABudgetEachRepositoryGetsOnItsOwn(t *testing.T) {
	namespaces := []string{"repo-a", "repo-b"}
	svc := New(&fakeSearcher{byNamespace: federatedCorpus(namespaces, 10, 1)})

	list, err := svc.SearchFederated(context.Background(), namespaces, Params{Query: "alpha", Limit: 5})
	if err != nil {
		t.Fatalf("SearchFederated: %v", err)
	}
	got := filesPerNamespace(list)
	for _, ns := range namespaces {
		if got[ns] != 5 {
			t.Errorf("%s put %d files on the page, want the whole per-repository budget of 5 (all of them: %v)", ns, got[ns], got)
		}
	}
	if len(list.Files) != 10 {
		t.Errorf("page holds %d files, want 5 per repository across 2 repositories", len(list.Files))
	}
}

func TestSearchFederated_EveryRepositoryWithHitsIsHeardBelowTheLimit(t *testing.T) {
	namespaces := []string{"repo-a", "repo-b", "repo-c", "repo-d", "repo-e"}
	svc := New(&fakeSearcher{byNamespace: federatedCorpus(namespaces, 1, 1)})

	// Fewer slots than repositories under the old shared budget: the last
	// repositories got nothing, and nothing in the answer said so.
	list, err := svc.SearchFederated(context.Background(), namespaces, Params{Query: "alpha", Limit: 3})
	if err != nil {
		t.Fatalf("SearchFederated: %v", err)
	}
	got := filesPerNamespace(list)
	silent := make([]string, 0, len(namespaces))
	for _, ns := range namespaces {
		if got[ns] == 0 {
			silent = append(silent, ns)
		}
	}
	if len(silent) > 0 {
		t.Errorf("repositories with a hit that got no file on the page: %v (page: %v)", silent, got)
	}
}

func TestSearchFederated_ARepositoryUnderTheHitBudgetCutStillReachesThePage(t *testing.T) {
	// repo-a's one file fills the whole page budget on its own, and it sorts
	// ahead of repo-b. Under a shared budget the page ended there, so repo-b was
	// silenced on this page and on every later one too.
	namespaces := []string{"repo-a", "repo-b"}
	byNamespace := federatedCorpus([]string{"repo-a"}, 1, evidence.PageHitBudget)
	byNamespace["repo-b"] = federatedCorpus([]string{"repo-b"}, 3, 1)["repo-b"]
	svc := New(&fakeSearcher{byNamespace: byNamespace})

	list, err := svc.SearchFederated(context.Background(), namespaces, Params{Query: "alpha", Limit: 5})
	if err != nil {
		t.Fatalf("SearchFederated: %v", err)
	}
	if got := filesPerNamespace(list); got["repo-b"] == 0 {
		t.Errorf("repo-b put no file on the page: %v — its files all sit below repo-a's budget cut", got)
	}
}

func TestSearchFederated_CountsEveryFileItLeftOff(t *testing.T) {
	// Small enough that neither pool is cut, so every file this query answers
	// with is reachable and the hidden count has to add up exactly.
	namespaces := []string{"repo-a", "repo-b"}
	const filesEach = 30
	svc := New(&fakeSearcher{byNamespace: federatedCorpus(namespaces, filesEach, 1)})

	list, err := svc.SearchFederated(context.Background(), namespaces, Params{Query: "alpha", Limit: 5})
	if err != nil {
		t.Fatalf("SearchFederated: %v", err)
	}
	if list.PoolTruncated {
		t.Fatalf("a pool was cut, so this test cannot tell a hidden file from an unfetched one")
	}
	total := filesEach * len(namespaces)
	if want := total - len(list.Files); list.OverflowFiles != want {
		t.Errorf("OverflowFiles = %d, want %d: %d files answered this query and %d are on the page",
			list.OverflowFiles, want, total, len(list.Files))
	}
}

// walkFederated pages through a federated answer with exactly the offsets the
// answer suggests, and reports the files it saw and the ones it saw twice.
func walkFederated(t *testing.T, svc *Service, namespaces []string, p Params, maxPages int) (seen map[string]int, repeats []string) {
	t.Helper()
	seen = map[string]int{}
	for range maxPages {
		list, err := svc.SearchFederated(context.Background(), namespaces, p)
		if err != nil {
			t.Fatalf("SearchFederated at offset %d: %v", p.Offset, err)
		}
		if len(list.Files) == 0 {
			t.Fatalf("page at offset %d came back empty (%q) with files still unseen", p.Offset, list.Note)
		}
		for _, f := range list.Files {
			key := f.Namespace + "/" + f.FilePath
			if seen[key] > 0 {
				repeats = append(repeats, key)
			}
			seen[key]++
		}
		if list.OverflowFiles == 0 && !list.PoolTruncated {
			return seen, repeats
		}
		if list.NextOffset <= p.Offset {
			t.Fatalf("the answer at offset %d suggested offset %d, which does not move", p.Offset, list.NextOffset)
		}
		// Exactly the call the answer's own `next` action names.
		p.Offset = list.NextOffset
	}
	t.Fatalf("paging did not finish in %d pages", maxPages)
	return nil, nil
}

// Federated paging is cut from one pool per repository, so it comes apart the
// same way a single repository's does when the pool it is cut from is reordered
// as a whole. Each repository here hands over the order that runs against
// structural scoring, and there are enough files in each that the pool has to
// grow past its first block to reach the end.
func TestSearchFederated_PagesToTheEndWhenEveryBackendOrderRunsAgainstTheStructuralOne(t *testing.T) {
	namespaces := []string{"repo-a", "repo-b"}
	const filesEach = 120
	svc := New(&fakeSearcher{byNamespace: map[string][]graph.Node{
		"repo-a": reversedCorpus(filesEach, 1),
		"repo-b": reversedCorpus(filesEach, 1),
	}})

	seen, repeats := walkFederated(t, svc, namespaces, Params{Query: "alpha", Limit: 10}, filesEach)
	if len(repeats) > 0 {
		t.Errorf("%d files came back on more than one page: %v", len(repeats), repeats)
	}
	if want := filesEach * len(namespaces); len(seen) != want {
		t.Errorf("paging reached %d files, want all %d", len(seen), want)
	}
}

func TestSearchFederated_PagingWithTheSuggestedOffsetSkipsAndRepeatsNothing(t *testing.T) {
	namespaces := []string{"repo-a", "repo-b"}
	const filesEach = 10
	svc := New(&fakeSearcher{byNamespace: federatedCorpus(namespaces, filesEach, 1)})

	seen, repeats := walkFederated(t, svc, namespaces, Params{Query: "alpha", Limit: 5}, 20)
	if len(repeats) > 0 {
		t.Errorf("these files came back on two pages: %v", repeats)
	}
	if want := filesEach * len(namespaces); len(seen) != want {
		t.Errorf("paging reached %d files, want all %d", len(seen), want)
	}
}

func TestSearchFederated_PagingHoldsUpWhenTheHitBudgetCutsEveryPageShort(t *testing.T) {
	// Three repositories, each holding six files of fifteen hits. One
	// repository's files alone hold 90 hits against a 50-hit page budget, so
	// every page stops mid-repository — the case where "offset plus the files
	// on this page" stops being a place any repository's list can be resumed
	// from. Limit is wide enough that the budget, not the limit, is what cuts.
	namespaces := []string{"repo-a", "repo-b", "repo-c"}
	const filesEach = 6
	svc := New(&fakeSearcher{byNamespace: federatedCorpus(namespaces, filesEach, 15)})

	seen, repeats := walkFederated(t, svc, namespaces, Params{Query: "alpha", Limit: 20}, 20)
	if len(repeats) > 0 {
		t.Errorf("these files came back on two pages: %v", repeats)
	}
	if want := filesEach * len(namespaces); len(seen) != want {
		t.Errorf("paging reached %d files, want all %d", len(seen), want)
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

// How much of a repository ever recorded a reason is a fact about the repository,
// not about the query, and the answer that needs it most is the one with no files.
func TestSearch_CarriesTheRepositoryAnnotationCoverage(t *testing.T) {
	searcher := &fakeSearcher{
		coverageByNamespace: map[string]intent.Coverage{
			requestctx.DefaultNamespace: {WithReason: 0, Declarations: 1900},
		},
	}
	ctx := requestctx.WithNamespace(context.Background(), requestctx.DefaultNamespace)

	list, err := New(searcher).Search(ctx, Params{Query: "why is this repository isolated", Limit: 10})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if list.Coverage.Declarations != 1900 || list.Coverage.WithReason != 0 {
		t.Errorf("Coverage = %+v, want 0 of 1900", list.Coverage)
	}
}

// Several repositories answered at once have one coverage between them, so both
// numbers add. Keeping only the last namespace's would describe one repository
// and label it the answer.
func TestSearchFederated_AddsUpTheCoverageOfEveryRepository(t *testing.T) {
	searcher := &fakeSearcher{
		coverageByNamespace: map[string]intent.Coverage{
			"repo-a": {WithReason: 0, Declarations: 40},
			"repo-b": {WithReason: 30, Declarations: 60},
		},
	}

	list, err := New(searcher).SearchFederated(context.Background(), []string{"repo-a", "repo-b"}, Params{Query: "alpha", Limit: 10})
	if err != nil {
		t.Fatalf("SearchFederated: %v", err)
	}
	if list.Coverage.WithReason != 30 || list.Coverage.Declarations != 100 {
		t.Errorf("Coverage = %+v, want 30 of 100", list.Coverage)
	}
}

// The whole point of the tool is reasons written in Korean, asked for in
// Korean. This is the path that used to lose them: the question carries
// particles, so under half its terms are counted in any reason and CanAnswer
// drops every intent hit — leaving the full-text pool as the only thing on the
// page, and the evidence cut as the only thing that can justify it. The cut
// compared the question's words to the reason's for equality, so 네임스페이스
// never reached 네임스페이스를 and the node the index had already found was
// dropped again.
func TestSearch_AKoreanQuestionSurvivesWhenTheIntentIndexCannotAnswerIt(t *testing.T) {
	hit := annotatedNode(1, "isolate", "internal/adapters/outbound/searchsql/postgres.go",
		"테스트마다 네임스페이스를 격리해서 서로 간섭하지 않게 한다")
	searcher := &fakeSearcher{
		byNamespace:       map[string][]graph.Node{requestctx.DefaultNamespace: {hit}},
		intentByNamespace: map[string][]intent.Hit{requestctx.DefaultNamespace: {{Node: hit, Terms: []string{"네임스페이스"}}}},
		// Only one of the three scored terms is written in any reason, so
		// CanAnswer is false and the intent hits are dropped before the cut.
		intentTerms: []intent.Term{
			{Text: "네임스페이스", InReasons: 4}, {Text: "격리를", InReasons: 0}, {Text: "어디서", InReasons: 0},
		},
	}
	svc := New(searcher)

	ctx := requestctx.WithNamespace(context.Background(), requestctx.DefaultNamespace)
	list, err := svc.Search(ctx, Params{Query: "네임스페이스 격리를 어디서", Limit: 10})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(list.Files) != 1 || list.Files[0].FilePath != "internal/adapters/outbound/searchsql/postgres.go" {
		t.Fatalf("got %+v (WeakFiltered=%d, Note=%q), want the node whose Korean reason answers the question",
			list.Files, list.WeakFiltered, list.Note)
	}
	if got := list.Hits()[0].Matched; !slices.Contains(got, evidence.MatchIntent) {
		t.Errorf("Matched = %v, want it to name the recorded reason", got)
	}
}

// ruleOnlyNode is a node whose author wrote a @domainRule and no @intent. One
// such node exists in this repository's own graph — intent.Result.CanAnswer —
// and it is the top hit of a real question about it, so the readback that
// reaches past @intent is load-bearing, not a defensive branch.
func ruleOnlyNode(id uint, name, path, rule string) graph.Node {
	n := node(id, name, path)
	n.Annotation = &graph.Annotation{Tags: []graph.DocTag{{Kind: graph.TagDomainRule, Value: rule}}}
	return n
}

// The reason shown beside a hit is read past @intent to the domain rule when
// that is the only reason recorded. Reading @intent alone would put this node
// on the page with a blank line where its reason goes — and in the CLI the
// evidence line carries the match labels, so the node would lose those too.
func TestSearch_ShowsADomainRuleWhenItIsTheOnlyReasonRecorded(t *testing.T) {
	const rule = "intent hits justify membership only when at least half of the question's scored terms appear in some recorded reason"
	hit := ruleOnlyNode(1, "CanAnswer", "internal/app/search/intent/intent.go", rule)
	searcher := &fakeSearcher{
		byNamespace:       map[string][]graph.Node{requestctx.DefaultNamespace: nil},
		intentByNamespace: map[string][]intent.Hit{requestctx.DefaultNamespace: {{Node: hit, Terms: []string{"membership"}}}},
		intentTerms:       []intent.Term{{Text: "membership", InReasons: 3}, {Text: "justify", InReasons: 2}},
	}
	svc := New(searcher)

	ctx := requestctx.WithNamespace(context.Background(), requestctx.DefaultNamespace)
	list, err := svc.Search(ctx, Params{Query: "justify membership", Limit: 10})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(list.Files) != 1 {
		t.Fatalf("got %+v, want the domain-rule hit on the page", list.Files)
	}
	if got := list.Hits()[0].Reason; got != rule {
		t.Errorf("Reason = %q, want the recorded domain rule", got)
	}
}
