// Package search assembles the one pipeline every search surface shares:
// fetch full-text and recorded-reason candidates in parallel, filter them by
// path, rerank the full-text pool structurally, absorb the intent hits as
// evidence, and cut the answer down to the hits that can justify themselves.
// MCP and the CLI both call this service, so the two cannot drift into
// answering the same query differently.
package search

import (
	"context"

	"github.com/tae2089/trace"

	"github.com/tae2089/code-context-graph/internal/app/search/evidence"
	intentapp "github.com/tae2089/code-context-graph/internal/app/search/intent"
	searchrank "github.com/tae2089/code-context-graph/internal/app/search/rank"
	requestctx "github.com/tae2089/code-context-graph/internal/ctx"
	"github.com/tae2089/code-context-graph/internal/domain/graph"
	"github.com/tae2089/code-context-graph/internal/pathspec"
)

// Searcher answers one query from both indexes a search runs over: the
// full-text index of names and content, and the recorded-reason index of
// @intent/@domainRule text. One port because the two answers are two halves of
// the same question — a caller wired for only one would answer it worse
// without any way to see that from the types.
// @intent keep the service on fetch-only ports so no backend or scoring package leaks in.
type Searcher interface {
	Query(ctx context.Context, query string, limit int) ([]graph.Node, error)
	QueryIntent(ctx context.Context, query string, limit int) (intentapp.Result, error)
}

// Params is one search request as every surface phrases it.
// @intent give MCP and the CLI the same request shape so their answers stay comparable.
type Params struct {
	Query string
	// Limit bounds how many files the answer shows, not how many candidates
	// are fetched: the service over-fetches so reranking can promote matches
	// the backend ranked below it.
	Limit       int
	Offset      int
	PathPrefix  string
	IncludeWeak bool
}

// Service runs the shared search pipeline over one candidate searcher.
// @intent hold the fetch→filter→rerank→evidence chain in one place instead of one copy per inbound adapter.
type Service struct {
	searcher Searcher
}

// New constructs the shared search service.
// @intent keep construction trivial so composition roots stay declarative.
func New(searcher Searcher) *Service {
	return &Service{searcher: searcher}
}

// Search answers one query within the namespace already on the context.
//
// The whole pool is reranked rather than just Limit of it: the evidence cut
// decides membership, so bounding the rerank would spend slots on candidates
// that are about to be dropped anyway.
// @intent answer a search with the files that can justify their place, not the backend's raw order.
// @ensures returns at most Limit files, each carrying every hit it answered with.
func (s *Service) Search(ctx context.Context, p Params) (evidence.List, error) {
	if s == nil || s.searcher == nil {
		return evidence.List{}, trace.New("search service not configured")
	}
	pool, err := s.fetch(ctx, p)
	if err != nil {
		return evidence.List{}, err
	}
	ranked := searchrank.Rerank(p.Query, pool.named, 0)
	merged, intentEvidence := absorbIntent(ranked, pool.intent)
	return evidence.Build(p.Query, merged, evidence.Options{Limit: p.Limit, Offset: p.Offset, IncludeWeak: p.IncludeWeak, Intent: intentEvidence}), nil
}

// SearchFederated answers one query across an explicit namespace set.
//
// Each namespace stays its own ranked list so fusion charges a hit the rank it
// held in its own namespace, not its offset in a concatenated slice. The
// evidence cut runs before the quota, so a namespace's slots go to hits it can
// justify rather than to whatever it retrieved first.
// @intent answer one search across several repositories with per-item namespace labels.
// @domainRule each namespace is queried in isolation, and every namespace with hits keeps at least one file on the page.
func (s *Service) SearchFederated(ctx context.Context, namespaces []string, p Params) (evidence.List, error) {
	if s == nil || s.searcher == nil {
		return evidence.List{}, trace.New("search service not configured")
	}
	groups := make([][]graph.Node, 0, len(namespaces))
	intentHits := make([]intentapp.Hit, 0)
	for _, ns := range namespaces {
		pool, err := s.fetch(requestctx.WithNamespace(ctx, ns), p)
		if err != nil {
			return evidence.List{}, err
		}
		for i := range pool.named {
			pool.named[i].Namespace = ns
		}
		for i := range pool.intent {
			pool.intent[i].Node.Namespace = ns
		}
		groups = append(groups, pool.named)
		intentHits = append(intentHits, pool.intent...)
	}
	merged := searchrank.RerankGroups(p.Query, groups, 0)
	merged, intentEvidence := absorbIntent(merged, intentHits)
	// Page over the whole grouped answer, then let the quota decide which of
	// those files each repository gets, so no repository's files are spent
	// before another repository is heard from.
	list := evidence.Build(p.Query, merged, evidence.Options{Offset: p.Offset, IncludeWeak: p.IncludeWeak, Intent: intentEvidence})
	reachable := len(list.Files)
	list.Files = selectWithNamespaceQuota(list.Files, func(f evidence.File) string { return f.Namespace }, p.Limit, len(namespaces))
	list.OverflowFiles = reachable - len(list.Files)
	return list, nil
}

// pool is one namespace's candidates from both indexes, already path-filtered.
type pool struct {
	named  []graph.Node
	intent []intentapp.Hit
}

// fetch over-fetches one namespace's candidate pool from both indexes in
// parallel and applies the path filter to each. Over-fetching lets structural
// reranking promote good matches the backend ranked below the caller's limit,
// and keeps path filtering from emptying the page. The two queries run
// concurrently because neither needs the other's answer and both are the same
// round-trip to the same database.
// @intent give both search shapes the same candidate pool for the same request.
func (s *Service) fetch(ctx context.Context, p Params) (pool, error) {
	type intentAnswer struct {
		result intentapp.Result
		err    error
	}
	intentCh := make(chan intentAnswer, 1)
	go func() {
		result, err := s.searcher.QueryIntent(ctx, p.Query, searchrank.FetchLimit(p.Limit))
		intentCh <- intentAnswer{result: result, err: err}
	}()

	named, err := s.searcher.Query(ctx, p.Query, searchrank.FetchLimit(p.Limit))
	fromIntent := <-intentCh
	if err != nil {
		return pool{}, err
	}
	if fromIntent.err != nil {
		return pool{}, fromIntent.err
	}

	out := pool{named: named, intent: fromIntent.result.Hits}
	if p.PathPrefix == "" {
		return out, nil
	}
	out.named = out.named[:0]
	for _, n := range named {
		if pathspec.HasPathPrefix(n.FilePath, p.PathPrefix) {
			out.named = append(out.named, n)
		}
	}
	filteredIntent := out.intent[:0]
	for _, h := range fromIntent.result.Hits {
		if pathspec.HasPathPrefix(h.Node.FilePath, p.PathPrefix) {
			filteredIntent = append(filteredIntent, h)
		}
	}
	out.intent = filteredIntent
	return out, nil
}

// absorbIntent merges intent hits into the name-ranked pool as evidence, not
// as a score. The name order is untouched; a hit only the intent index found
// is appended after it, in intent rank order. Fusing the two orders was ruled
// out the same way backend-rank fusion was (see Rerank): the two rankings
// measure different things and a weighted mix answers neither question well.
// @ensures the returned evidence map holds every intent hit, keyed by namespace and node id.
// @intent let a recorded reason put a node on the page without letting it reshuffle the name matches.
func absorbIntent(ranked []graph.Node, hits []intentapp.Hit) ([]graph.Node, map[evidence.NodeRef]evidence.IntentHit) {
	if len(hits) == 0 {
		return ranked, nil
	}
	marks := make(map[evidence.NodeRef]evidence.IntentHit, len(hits))
	present := make(map[evidence.NodeRef]bool, len(ranked))
	for _, n := range ranked {
		present[evidence.NodeRef{Namespace: n.Namespace, ID: n.ID}] = true
	}
	merged := ranked
	for _, h := range hits {
		ref := evidence.NodeRef{Namespace: h.Node.Namespace, ID: h.Node.ID}
		if _, seen := marks[ref]; seen {
			continue
		}
		marks[ref] = evidence.IntentHit{Reason: intentapp.RecordedReason(h.Node), Terms: h.Terms}
		if !present[ref] {
			merged = append(merged, h.Node)
			present[ref] = true
		}
	}
	return merged, marks
}

// selectWithNamespaceQuota bounds globally-ranked federated results while guaranteeing
// every namespace with hits at least limit/namespaceCount slots (minimum one).
// @intent keep one high-scoring repository from starving the other namespaces out of a federated result.
// @domainRule remaining slots after the per-namespace quota pass are filled in global rank order.
// @requires namespaceOf returns the namespace an item belongs to.
func selectWithNamespaceQuota[T any](ranked []T, namespaceOf func(T) string, limit, namespaceCount int) []T {
	if limit <= 0 || len(ranked) <= limit {
		return ranked
	}
	quota := max(limit/max(namespaceCount, 1), 1)
	chosen := make([]bool, len(ranked))
	count := 0
	perNamespace := map[string]int{}
	for i, item := range ranked {
		if count == limit {
			break
		}
		if ns := namespaceOf(item); perNamespace[ns] < quota {
			chosen[i] = true
			perNamespace[ns]++
			count++
		}
	}
	for i := range ranked {
		if count == limit {
			break
		}
		if !chosen[i] {
			chosen[i] = true
			count++
		}
	}
	kept := make([]T, 0, count)
	for i, item := range ranked {
		if chosen[i] {
			kept = append(kept, item)
		}
	}
	return kept
}
