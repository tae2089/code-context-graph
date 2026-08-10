// @index Bound SQL search persistence adapters.
package searchsql

import (
	"context"
	"fmt"

	"gorm.io/gorm"

	"github.com/tae2089/trace"

	intentapp "github.com/tae2089/code-context-graph/internal/app/search/intent"
	"github.com/tae2089/code-context-graph/internal/app/search/intentrank"
	requestctx "github.com/tae2089/code-context-graph/internal/ctx"
	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

// Reader binds candidate and intent search persistence to one database.
// @intent adapt raw SQL backend and GORM operations to app/search read ports.
type Reader struct {
	db      *gorm.DB
	backend Backend
}

var _ intentapp.Searcher = (*Reader)(nil)

// NewReader constructs bound search read ports.
// @intent keep database handles out of application service construction.
func NewReader(db *gorm.DB, backend Backend) *Reader {
	return &Reader{db: db, backend: backend}
}

// Query returns relevance-ordered candidates from the configured SQL search backend.
// @intent implement the bound candidate-search port without exposing a DB argument.
func (r *Reader) Query(ctx context.Context, query string, limit int) ([]graph.Node, error) {
	if r == nil || r.backend == nil || r.db == nil {
		return nil, nil
	}
	return r.backend.Query(ctx, r.db, query, limit)
}

// QueryIntent answers a question from the recorded-reason index only.
//
// The database finds the candidates and Go ranks them. That split is what makes
// SQLite and PostgreSQL answer alike: each database has its own scoring function
// and they disagree, so leaving the order to whichever one is deployed meant the
// golden score measured on a laptop said nothing about the running server.
// The terms come back with the nodes because the scorer is the only place that
// knows them, and the caller cannot judge an answer without them: a file that
// matched one word written in half the recorded reasons and a file that matched
// a word written in three are the same row otherwise.
//
// @intent implement the bound intent-search port without exposing a DB argument, and rank the same way on every backend.
// @return returns at most limit nodes, best first, each with the question terms written in its reason, and nothing when no recorded reason matches.
func (r *Reader) QueryIntent(ctx context.Context, query string, limit int) (intentapp.Result, error) {
	if r == nil || r.backend == nil || r.db == nil {
		return intentapp.Result{}, nil
	}
	if limit <= 0 {
		return intentapp.Result{}, fmt.Errorf("limit must be > 0, got %d", limit)
	}
	// Coverage is measured before the candidates are looked at, because the answer
	// it explains is the one with no candidates: a question nothing matched in a
	// repository nobody annotated has to come back saying so, and returning early
	// here is exactly what used to leave that answer talking about the full-text
	// index instead.
	coverage, err := r.annotationCoverage(ctx)
	if err != nil {
		return intentapp.Result{}, err
	}
	candidates, err := r.backend.MatchIntent(ctx, r.db, query, maxIntentCandidates)
	if err != nil {
		return intentapp.Result{}, err
	}
	if len(candidates) == 0 {
		return intentapp.Result{Coverage: coverage}, nil
	}
	corpusSize, err := r.intentCorpusSize(ctx)
	if err != nil {
		return intentapp.Result{}, err
	}

	ranked := intentrank.Rank(query, candidates, corpusSize, limit)
	result := intentapp.Result{Terms: intentTerms(ranked.Terms), Corpus: ranked.Corpus, Coverage: coverage}
	if len(ranked.Matches) == 0 {
		return result, nil
	}

	nodeIDs := make([]uint, 0, len(ranked.Matches))
	termsByNode := make(map[uint][]string, len(ranked.Matches))
	for _, match := range ranked.Matches {
		nodeIDs = append(nodeIDs, match.NodeID)
		termsByNode[match.NodeID] = match.Terms
	}
	nodes, err := loadNodesInOrder(ctx, r.db, nodeIDs)
	if err != nil {
		return intentapp.Result{}, err
	}
	result.Hits = make([]intentapp.Hit, 0, len(nodes))
	for _, node := range nodes {
		result.Hits = append(result.Hits, intentapp.Hit{Node: node, Terms: termsByNode[node.ID]})
	}
	return result, nil
}

// intentTerms carries the scorer's term counts across the port boundary.
// @intent keep the application layer free of the scoring package's types.
func intentTerms(terms []intentrank.Term) []intentapp.Term {
	if len(terms) == 0 {
		return nil
	}
	out := make([]intentapp.Term, len(terms))
	for i, term := range terms {
		out[i] = intentapp.Term{Text: term.Text, InReasons: term.InReasons}
	}
	return out
}

// annotationCoverage counts how many declarations recorded a reason, out of how
// many were indexed at all.
//
// The numerator counts distinct node ids rather than rows: one reason is one row,
// so a declaration whose author wrote three would otherwise be counted three
// times and the fraction would report better coverage than the repository has.
// The denominator is the derived document table rather than the node table
// because both numbers then come from the same refresh pass, which is what makes
// the numerator a subset of the denominator — one row per indexed declaration,
// and a declaration too coarse to index records no reason either.
//
// @ensures the returned count of annotated declarations never exceeds the count of declarations.
// @intent give an answer the two numbers that separate "nobody wrote a reason" from "no reason matched".
func (r *Reader) annotationCoverage(ctx context.Context) (intentapp.Coverage, error) {
	ns := requestctx.FromContext(ctx)
	var withReason int64
	if err := r.db.WithContext(ctx).Model(&graph.SearchReason{}).
		Where("namespace = ?", ns).
		Distinct("node_id").
		Count(&withReason).Error; err != nil {
		return intentapp.Coverage{}, trace.Wrap(err, "count declarations carrying a reason")
	}
	var declarations int64
	if err := r.db.WithContext(ctx).Model(&graph.SearchDocument{}).
		Where("namespace = ?", ns).
		Count(&declarations).Error; err != nil {
		return intentapp.Coverage{}, trace.Wrap(err, "count indexed declarations")
	}
	return intentapp.Coverage{WithReason: int(withReason), Declarations: int(declarations)}, nil
}

// intentCorpusSize counts the recorded reasons, which is how many documents the
// scorer is measuring a word's commonness against.
//
// A word written in half the recorded reasons tells the reader nothing, and
// without this number there is nothing to compare a word's document count
// against. The count is of reasons, not of declarations, because one reason is
// one document now: a declaration that wrote three of them contributes three,
// and counting it once would leave the denominator smaller than the number of
// documents a word can appear in.
// @intent give the scorer the denominator that makes a common word common.
func (r *Reader) intentCorpusSize(ctx context.Context) (int, error) {
	var total int64
	if err := r.db.WithContext(ctx).Model(&graph.SearchReason{}).
		Where("namespace = ?", requestctx.FromContext(ctx)).
		Count(&total).Error; err != nil {
		return 0, err
	}
	return int(total), nil
}
