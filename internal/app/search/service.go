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
	// the backend ranked below it. Across several namespaces it is what each
	// one may show, not what they share — see SearchFederated.
	Limit       int
	Offset      int
	PathPrefix  string
	IncludeWeak bool
}

// ValidateOffset turns away the one page position no answer exists for, in the
// words every search surface says it in.
//
// MCP and the CLI both call it before the pipeline runs. The sentence lives
// here rather than at either entry point so the two cannot end up describing
// the same rejected request differently — a reader who learns what the tool
// says has also learned what the flag says.
//
// @intent keep the search entry points agreeing about which requests are askable.
func ValidateOffset(offset int) error {
	if offset < 0 {
		return trace.New("offset must not be negative")
	}
	return nil
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
	list := evidence.Build(p.Query, merged, evidence.Options{
		Limit: p.Limit, Offset: p.Offset, IncludeWeak: p.IncludeWeak,
		Intent: intentEvidence, Coverage: pool.coverage,
	})
	list.PoolTruncated = pool.truncated
	return list, nil
}

// SearchFederated answers one query across an explicit namespace set.
//
// Each namespace stays its own ranked list so fusion charges a hit the rank it
// held in its own namespace, not its offset in a concatenated slice.
//
// Limit is what each repository may show, not what they share. Sharing it meant
// the repositories competed: a limit smaller than the namespace count silenced
// the ones at the back outright, and the quota that was supposed to prevent
// that took its slots out of the middle of the ranked list, which left no
// offset that could resume the page. A budget each repository holds on its own
// removes the competition, so none of the three has anywhere to come from.
//
// @intent answer one search across several repositories with per-item namespace labels.
// @domainRule each namespace is queried in isolation and spends its own file budget, so no namespace with hits can be crowded off the page by another.
func (s *Service) SearchFederated(ctx context.Context, namespaces []string, p Params) (evidence.List, error) {
	if s == nil || s.searcher == nil {
		return evidence.List{}, trace.New("search service not configured")
	}
	groups := make([][]graph.Node, 0, len(namespaces))
	intentHits := make([]intentapp.Hit, 0)
	poolTruncated := false
	coverage := evidence.Coverage{}
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
		// One repository's pool running out is enough to make the whole answer
		// short, and the caller cannot tell which one it was from the files.
		poolTruncated = poolTruncated || pool.truncated
		coverage = addCoverage(coverage, pool.coverage)
	}
	merged := searchrank.RerankGroups(p.Query, groups, 0)
	merged, intentEvidence := absorbIntent(merged, intentHits)
	list := evidence.Build(p.Query, merged, evidence.Options{
		Limit: p.Limit, Offset: p.Offset, PerNamespace: true,
		IncludeWeak: p.IncludeWeak, Intent: intentEvidence, Coverage: coverage,
	})
	list.PoolTruncated = poolTruncated
	return list, nil
}

// pool is one namespace's candidates from both indexes, already path-filtered.
type pool struct {
	named  []graph.Node
	intent []intentapp.Hit
	// truncated says the backend answered with as many rows as the fetch had
	// room for, so there were candidates it never got to send.
	truncated bool
	// coverage is how much of this namespace ever recorded a reason. It survives
	// the path filter untouched: the filter narrows what this query may answer
	// with, while the coverage is about the repository the question was put to.
	coverage evidence.Coverage
}

// fetch over-fetches one namespace's candidate pool from both indexes in
// parallel and applies the path filter to each. Over-fetching lets structural
// reranking promote good matches the backend ranked below the caller's limit,
// and keeps path filtering from emptying the page. The two queries run
// concurrently because neither needs the other's answer and both are the same
// round-trip to the same database.
//
// The pool is sized for Offset+Limit, not Limit. Neither query carries a skip,
// so page five is cut out of the same pool page one was: a pool wide enough for
// Limit alone runs out under any offset worth asking for, and the page then
// comes back empty while the query still has hundreds of files to answer with.
// Pushing the skip into the backend instead was considered and dropped — it
// keeps the pool small but pays for the skipped rows on every deep page.
//
// @ensures the returned pool is marked truncated when either index answered with as many rows as the fetch had room for.
// @intent give both search shapes the same candidate pool for the same request, wide enough to reach the page that was asked for.
func (s *Service) fetch(ctx context.Context, p Params) (pool, error) {
	type intentAnswer struct {
		result intentapp.Result
		err    error
	}
	fetchLimit := searchrank.FetchLimit(p.Offset + p.Limit)
	intentCh := make(chan intentAnswer, 1)
	go func() {
		result, err := s.searcher.QueryIntent(ctx, p.Query, fetchLimit)
		intentCh <- intentAnswer{result: result, err: err}
	}()

	named, err := s.searcher.Query(ctx, p.Query, fetchLimit)
	fromIntent := <-intentCh
	if err != nil {
		return pool{}, err
	}
	if fromIntent.err != nil {
		return pool{}, fromIntent.err
	}

	// Judged on what the backends sent, before the path filter and the
	// answerability cut thin it out: those drop candidates that were fetched,
	// which says nothing about candidates that never were.
	truncated := len(named) >= fetchLimit || len(fromIntent.result.Hits) >= fetchLimit

	intentHits := fromIntent.result.Hits
	// A question the recorded reasons cannot answer contributes no intent hits:
	// whatever the index matched was a shared word, not an answer.
	if !fromIntent.result.CanAnswer() {
		intentHits = nil
	}

	out := pool{
		named:     named,
		intent:    intentHits,
		truncated: truncated,
		coverage:  coverageFromIntent(fromIntent.result.Coverage),
	}
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
	for _, h := range intentHits {
		if pathspec.HasPathPrefix(h.Node.FilePath, p.PathPrefix) {
			filteredIntent = append(filteredIntent, h)
		}
	}
	out.intent = filteredIntent
	return out, nil
}

// coverageFromIntent carries the recorded-reason index's coverage across the port
// boundary. The two types hold the same two numbers on purpose: an evidence list
// has to be describable without the retrieval port that filled it.
// @intent keep the intent port's types out of the answer the surfaces serialize.
func coverageFromIntent(c intentapp.Coverage) evidence.Coverage {
	return evidence.Coverage{WithReason: c.WithReason, Declarations: c.Declarations}
}

// addCoverage sums one namespace's coverage into the answer's running total.
// Both numbers add, so several repositories searched at once report one fraction
// over all of them: keeping only the last namespace's would describe one
// repository and label it the answer.
// @intent make a federated answer's coverage cover every repository it searched.
func addCoverage(total, next evidence.Coverage) evidence.Coverage {
	return evidence.Coverage{
		WithReason:   total.WithReason + next.WithReason,
		Declarations: total.Declarations + next.Declarations,
	}
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
