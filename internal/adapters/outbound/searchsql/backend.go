// @index Shared search backend interface and errors for SQLite FTS5 and PostgreSQL tsvector implementations.
package searchsql

import (
	"context"

	"gorm.io/gorm"

	"github.com/tae2089/trace"

	"github.com/tae2089/code-context-graph/internal/app/search/intentrank"
	requestctx "github.com/tae2089/code-context-graph/internal/ctx"
	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

// ErrFTS5NotAvailable indicates the SQLite build lacks the fts5 extension.
var ErrFTS5NotAvailable = trace.New("fts5 module not available")

// Backend defines the full-text search backend contract.
// @intent provide one interface for backend-specific search index migration, rebuild, and query operations.
type Backend interface {
	// @intent prepare search tables and indexes for the active database driver.
	// @sideEffect may create or update search index schema objects.
	Migrate(db *gorm.DB) error
	// @intent refresh backend-specific full-text index state from the current persisted search documents for the active namespace.
	// @requires db must be a valid connection for processing the active namespace.
	// @sideEffect rewrites backend-specific search index records or derived vectors.
	Rebuild(ctx context.Context, db *gorm.DB) error
	// @intent reindex only changed nodes so incremental updates cost less than full rebuilds.
	// @param nodeIDs is the set of node IDs to reindex.
	// @sideEffect updates search index records for the specified nodes.
	RebuildNodes(ctx context.Context, db *gorm.DB, nodeIDs []uint) error
	// @intent remove or reconcile backend-specific search index state for the active namespace when physical cleanup is required.
	// @sideEffect may clear namespace-scoped search index records, though implementations may intentionally no-op.
	PurgeNamespace(ctx context.Context, db *gorm.DB) error
	// @intent execute a user query using the backend-specific full-text search syntax.
	// @param query is the raw query string to search for.
	// @param limit is the maximum number of results to return.
	// @return returns nodes ordered by relevance.
	Query(ctx context.Context, db *gorm.DB, query string, limit int) ([]graph.Node, error)
	// MatchIntent finds every recorded reason a question could be answered from,
	// in no particular order, and hands back the exact text that was indexed for
	// each one.
	//
	// Ordering is deliberately not the backend's job. SQLite would order by
	// bm25 and PostgreSQL by ts_rank, which never learns that a word is common,
	// so the same question ranks differently on the database that was measured
	// and the database that is deployed. Both backends retrieve here and
	// intentrank scores, so there is one answer to be judged by.
	// @intent find every candidate reason and leave the ranking to shared scoring.
	// @param query is a natural-language question, not an identifier.
	// @param maxCandidates caps how many candidates come back, guarding memory rather than shaping the answer.
	// @return returns unordered candidates, and nothing when no recorded reason matches.
	MatchIntent(ctx context.Context, db *gorm.DB, query string, maxCandidates int) ([]intentrank.Doc, error)
}

// loadNodesInOrder fetches nodes by id within the request namespace and
// restores the given ranked order, which the database does not preserve,
// dropping any id that no longer resolves. The annotation rides along because
// every consumer of a search result shows the author's @intent beside it, and
// asking for it afterwards would mean a second round trip per search.
// @intent keep the ranked order across the round trip that loads the nodes themselves.
func loadNodesInOrder(ctx context.Context, db *gorm.DB, nodeIDs []uint) ([]graph.Node, error) {
	var nodes []graph.Node
	if err := db.WithContext(ctx).
		Where("id IN ?", nodeIDs).
		Where("namespace = ?", requestctx.FromContext(ctx)).
		Preload("Annotation.Tags").
		Find(&nodes).Error; err != nil {
		return nil, trace.Wrap(err, "load nodes")
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

// maxIntentCandidates caps one question's candidate set.
//
// It is a runaway guard, not a page size. Scoring needs every document holding
// any query term, because that set is what makes a word common: cut it short and
// a term looks rarer than it is, and the ranking silently changes. A corpus
// where a question can match more than this many reasons would be ranked on a
// biased sample.
const maxIntentCandidates = 10000
