// @index Bound SQL search and retrieval persistence adapters.
package searchsql

import (
	"context"

	"gorm.io/gorm"

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

var _ retrievalapp.CandidateSearcher = (*Reader)(nil)
var _ retrievalapp.Repository = (*Reader)(nil)
