// @index Bound SQL search and retrieval persistence adapters.
package searchsql

import (
	"context"
	"fmt"

	"gorm.io/gorm"

	"github.com/tae2089/trace"

	intentapp "github.com/tae2089/code-context-graph/internal/app/search/intent"
	"github.com/tae2089/code-context-graph/internal/app/search/intentrank"
	retrievalapp "github.com/tae2089/code-context-graph/internal/app/search/retrieval"
	requestctx "github.com/tae2089/code-context-graph/internal/ctx"
	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

// Reader binds candidate search and fallback retrieval persistence to one database.
// @intent adapt raw SQL backend and GORM operations to app/search retrieval ports.
type Reader struct {
	db      *gorm.DB
	backend Backend
}

var _ retrievalapp.CandidateSearcher = (*Reader)(nil)
var _ retrievalapp.Repository = (*Reader)(nil)
var _ intentapp.Searcher = (*Reader)(nil)
var _ intentapp.CoverageReader = (*Reader)(nil)

// NewReader constructs bound search and retrieval ports.
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
	candidates, err := r.backend.MatchIntent(ctx, r.db, query, maxIntentCandidates)
	if err != nil {
		return intentapp.Result{}, err
	}
	if len(candidates) == 0 {
		return intentapp.Result{}, nil
	}
	corpusSize, err := r.intentCorpusSize(ctx)
	if err != nil {
		return intentapp.Result{}, err
	}

	ranked := intentrank.Rank(query, candidates, corpusSize, limit)
	result := intentapp.Result{Terms: intentTerms(ranked.Terms), Corpus: ranked.Corpus}
	if len(ranked.Matches) == 0 {
		return result, nil
	}

	nodeIDs := make([]uint, 0, len(ranked.Matches))
	termsByNode := make(map[uint][]string, len(ranked.Matches))
	for _, match := range ranked.Matches {
		nodeIDs = append(nodeIDs, match.NodeID)
		termsByNode[match.NodeID] = match.Terms
	}
	nodes, err := r.loadNodesInOrder(ctx, nodeIDs)
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

// intentCorpusSize counts the declarations carrying a recorded reason, which is
// how many documents the scorer is measuring a word's commonness against.
//
// It is the same count IntentCoverage reports as NodesWithReason. A word written
// in half the recorded reasons tells the reader nothing, and without this number
// there is nothing to compare a word's document count against.
// @intent give the scorer the denominator that makes a common word common.
func (r *Reader) intentCorpusSize(ctx context.Context) (int, error) {
	var total int64
	if err := r.db.WithContext(ctx).Model(&graph.SearchDocument{}).
		Where("namespace = ?", requestctx.FromContext(ctx)).
		Where("intent_content <> ''").
		Count(&total).Error; err != nil {
		return 0, err
	}
	return int(total), nil
}

// loadNodesInOrder fetches nodes by id and restores the ranked order the database
// does not preserve, dropping any id that no longer resolves.
// @intent keep the ranked order across the round trip that loads the nodes themselves.
func (r *Reader) loadNodesInOrder(ctx context.Context, nodeIDs []uint) ([]graph.Node, error) {
	var nodes []graph.Node
	if err := r.db.WithContext(ctx).
		Where("id IN ?", nodeIDs).
		Where("namespace = ?", requestctx.FromContext(ctx)).
		Preload("Annotation.Tags").
		Find(&nodes).Error; err != nil {
		return nil, trace.Wrap(err, "load intent nodes")
	}
	position := make(map[uint]int, len(nodeIDs))
	for i, id := range nodeIDs {
		position[id] = i
	}
	ordered := make([]graph.Node, len(nodeIDs))
	for _, node := range nodes {
		if i, ok := position[node.ID]; ok {
			ordered[i] = node
		}
	}
	result := ordered[:0]
	for _, node := range ordered {
		if node.ID != 0 {
			result = append(result, node)
		}
	}
	return result, nil
}

// IntentCoverage counts how many searchable declarations carry a recorded reason.
//
// Both numbers come from search_documents rather than from nodes, because that
// table is already the set of declarations the index can reach: it drops the
// kinds search never returns. Counting nodes instead would put packages and
// other unindexed kinds into the denominator and understate coverage.
// @intent tell a caller how much of the searchable code could have answered at all.
func (r *Reader) IntentCoverage(ctx context.Context) (intentapp.Coverage, error) {
	if r == nil || r.db == nil {
		return intentapp.Coverage{}, gorm.ErrInvalidDB
	}
	ns := requestctx.FromContext(ctx)
	base := func() *gorm.DB {
		return r.db.WithContext(ctx).Model(&graph.SearchDocument{}).Where("namespace = ?", ns)
	}
	var total, withReason int64
	if err := base().Count(&total).Error; err != nil {
		return intentapp.Coverage{}, err
	}
	if err := base().Where("intent_content <> ''").Count(&withReason).Error; err != nil {
		return intentapp.Coverage{}, err
	}
	return intentapp.Coverage{NodesWithReason: int(withReason), NodesTotal: int(total)}, nil
}

// ScanCandidates loads a bounded, deterministic namespace snapshot with annotations.
// @intent provide sparse-FTS fallback inputs while keeping matching and scoring in app policy.
func (r *Reader) ScanCandidates(ctx context.Context, kinds []graph.NodeKind, limit int) ([]graph.Node, error) {
	if r == nil || r.db == nil {
		return nil, gorm.ErrInvalidDB
	}
	var nodes []graph.Node
	if err := r.db.WithContext(ctx).
		Where("namespace = ?", requestctx.FromContext(ctx)).
		Where("kind IN ?", kinds).
		Preload("Annotation.Tags").
		Order("file_path ASC, qualified_name ASC, id ASC").
		Limit(limit).
		Find(&nodes).Error; err != nil {
		return nil, err
	}
	return nodes, nil
}

// Annotations batch-loads structured annotations for namespace-owned candidate nodes.
// @intent provide bounded retrieval evidence without leaking joins into app policy.
func (r *Reader) Annotations(ctx context.Context, nodeIDs []uint) (map[uint]*graph.Annotation, error) {
	result := make(map[uint]*graph.Annotation, len(nodeIDs))
	if len(nodeIDs) == 0 {
		return result, nil
	}
	var rows []graph.Annotation
	if err := r.db.WithContext(ctx).
		Joins("JOIN nodes ON nodes.id = annotations.node_id").
		Where("annotations.node_id IN ? AND nodes.namespace = ?", nodeIDs, requestctx.FromContext(ctx)).
		Preload("Tags").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	for i := range rows {
		result[rows[i].NodeID] = &rows[i]
	}
	return result, nil
}
