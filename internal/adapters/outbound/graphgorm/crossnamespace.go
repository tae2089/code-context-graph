// @index Namespace-agnostic graph reader that merges cross_refs into traversal edges for cross-repository analysis.
package graphgorm

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"github.com/tae2089/trace"

	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

// CrossNamespaceReader reads nodes and edges across every namespace and augments
// traversal with synthetic cross_ref edges derived from resolved cross-namespace references.
// @intent let impact and flow analysis walk across repository boundaries declared by annotations.
// @domainRule node ids are globally unique, so id-based reads are safe without a namespace filter.
type CrossNamespaceReader struct {
	db *gorm.DB
}

// CrossNamespaceReader returns a namespace-agnostic reader over the same database.
// @intent derive the cross-repository read surface from an existing store without new wiring inputs.
func (s *Store) CrossNamespaceReader() *CrossNamespaceReader {
	return &CrossNamespaceReader{db: s.db}
}

// GetEdgesFrom returns outgoing edges of one node across namespaces, including cross refs.
// @intent satisfy the impact analyzer contract for cross-namespace traversal.
func (r *CrossNamespaceReader) GetEdgesFrom(ctx context.Context, nodeID uint) ([]graph.Edge, error) {
	return r.GetEdgesFromNodes(ctx, []uint{nodeID})
}

// GetEdgesFromNodes returns outgoing edges of the node set across namespaces, including cross refs.
// @intent expand traversal frontiers across repository boundaries in one query pair.
func (r *CrossNamespaceReader) GetEdgesFromNodes(ctx context.Context, nodeIDs []uint) ([]graph.Edge, error) {
	if len(nodeIDs) == 0 {
		return nil, nil
	}
	var edges []graph.Edge
	if err := r.db.WithContext(ctx).Where("from_node_id IN ?", nodeIDs).Find(&edges).Error; err != nil {
		return nil, trace.Wrap(err, "cross-namespace edges from nodes")
	}
	var refs []graph.CrossRef
	if err := r.db.WithContext(ctx).
		Where("from_node_id IN ? AND status = ? AND resolved_node_id IS NOT NULL", nodeIDs, graph.CrossRefStatusResolved).
		Find(&refs).Error; err != nil {
		return nil, trace.Wrap(err, "cross-namespace refs from nodes")
	}
	return append(edges, crossRefEdges(refs)...), nil
}

// GetEdgesTo returns incoming edges of one node across namespaces, including cross refs.
// @intent satisfy the impact analyzer contract for reverse cross-namespace traversal.
func (r *CrossNamespaceReader) GetEdgesTo(ctx context.Context, nodeID uint) ([]graph.Edge, error) {
	return r.GetEdgesToNodes(ctx, []uint{nodeID})
}

// GetEdgesToNodes returns incoming edges of the node set across namespaces, including cross refs.
// @intent let impact analysis find foreign namespaces that depend on the target nodes.
func (r *CrossNamespaceReader) GetEdgesToNodes(ctx context.Context, nodeIDs []uint) ([]graph.Edge, error) {
	if len(nodeIDs) == 0 {
		return nil, nil
	}
	var edges []graph.Edge
	if err := r.db.WithContext(ctx).Where("to_node_id IN ?", nodeIDs).Find(&edges).Error; err != nil {
		return nil, trace.Wrap(err, "cross-namespace edges to nodes")
	}
	var refs []graph.CrossRef
	if err := r.db.WithContext(ctx).
		Where("resolved_node_id IN ? AND status = ?", nodeIDs, graph.CrossRefStatusResolved).
		Find(&refs).Error; err != nil {
		return nil, trace.Wrap(err, "cross-namespace refs to nodes")
	}
	return append(edges, crossRefEdges(refs)...), nil
}

// GetNodeByID retrieves one node by primary key regardless of namespace.
// @intent resolve traversal frontiers that crossed into another namespace.
func (r *CrossNamespaceReader) GetNodeByID(ctx context.Context, id uint) (*graph.Node, error) {
	var node graph.Node
	result := r.db.WithContext(ctx).Where("id = ?", id).First(&node)
	if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if result.Error != nil {
		return nil, trace.Wrap(result.Error, "cross-namespace node by id")
	}
	return &node, nil
}

// GetNodesByIDs retrieves nodes by primary key regardless of namespace.
// @intent load result nodes for cross-namespace traversals in one query.
func (r *CrossNamespaceReader) GetNodesByIDs(ctx context.Context, ids []uint) ([]graph.Node, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var nodes []graph.Node
	if err := r.db.WithContext(ctx).Where("id IN ?", ids).Find(&nodes).Error; err != nil {
		return nil, trace.Wrap(err, "cross-namespace nodes by ids")
	}
	return nodes, nil
}

// crossRefEdges converts resolved cross refs into synthetic traversal edges.
// @intent reuse existing traversal algorithms unchanged by presenting refs as edges.
func crossRefEdges(refs []graph.CrossRef) []graph.Edge {
	if len(refs) == 0 {
		return nil
	}
	edges := make([]graph.Edge, 0, len(refs))
	for _, ref := range refs {
		if ref.ResolvedNodeID == nil {
			continue
		}
		edges = append(edges, graph.Edge{
			Namespace:   ref.FromNamespace,
			FromNodeID:  ref.FromNodeID,
			ToNodeID:    *ref.ResolvedNodeID,
			Kind:        graph.EdgeKindCrossRef,
			Fingerprint: ref.Raw,
		})
	}
	return edges
}
