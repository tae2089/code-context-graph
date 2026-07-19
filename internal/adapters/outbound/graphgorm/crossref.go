// @index GORM adapter for materialized cross-namespace reference rows and ccg ref resolution.
package graphgorm

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"github.com/tae2089/trace"

	crossrefapp "github.com/tae2089/code-context-graph/internal/app/crossref"
	"github.com/tae2089/code-context-graph/internal/domain/graph"
	"github.com/tae2089/code-context-graph/internal/domain/reference"
)

var _ crossrefapp.Store = (*Store)(nil)

// ListAnnotationCCGRefs returns every @see ccg:// tag value declared by nodes of one namespace.
// @intent collect the source facts for rebuilding a namespace's outbound cross refs.
func (s *Store) ListAnnotationCCGRefs(ctx context.Context, namespace string) ([]crossrefapp.AnnotationRef, error) {
	var rows []crossrefapp.AnnotationRef
	err := s.db.WithContext(ctx).Model(&graph.DocTag{}).
		Select("annotations.node_id AS node_id, doc_tags.value AS value").
		Joins("JOIN annotations ON annotations.id = doc_tags.annotation_id").
		Joins("JOIN nodes ON nodes.id = annotations.node_id").
		Where("doc_tags.kind = ?", graph.TagSee).
		Where("doc_tags.value LIKE ?", reference.Scheme+"://%").
		Where("nodes.namespace = ?", namespace).
		Scan(&rows).Error
	if err != nil {
		return nil, trace.Wrap(err, "list annotation ccg refs")
	}
	return rows, nil
}

// ResolveCCGRef resolves a parsed ccg:// reference to a target node id.
// @intent give cross-ref materialization the concrete node identity behind a symbolic reference.
// @domainRule namespace-scope refs resolve with a zero node id when the namespace has any nodes.
// @domainRule path-scope refs prefer the file node of the path; remaining ties resolve to the lowest node id.
func (s *Store) ResolveCCGRef(ctx context.Context, ref reference.Ref) (uint, bool, error) {
	if ref.Path == "" && ref.Symbol == "" {
		var count int64
		if err := s.db.WithContext(ctx).Model(&graph.Node{}).Where("namespace = ?", ref.Namespace).Count(&count).Error; err != nil {
			return 0, false, trace.Wrap(err, "resolve namespace-scope ccg ref")
		}
		return 0, count > 0, nil
	}
	var node graph.Node
	err := s.ccgRefNodeQuery(ctx, ref).
		Order("CASE WHEN kind = 'file' THEN 0 ELSE 1 END, id").
		First(&node).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, trace.Wrap(err, "resolve ccg ref")
	}
	return node.ID, true, nil
}

// ReplaceCrossRefsFrom atomically replaces every cross ref originating from one namespace.
// @intent make outbound cross-ref state a pure function of the namespace's current annotations.
// @sideEffect deletes and inserts cross_refs rows in one transaction.
func (s *Store) ReplaceCrossRefsFrom(ctx context.Context, fromNamespace string, refs []graph.CrossRef) error {
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("from_namespace = ?", fromNamespace).Delete(&graph.CrossRef{}).Error; err != nil {
			return err
		}
		if len(refs) == 0 {
			return nil
		}
		return tx.CreateInBatches(refs, 100).Error
	})
	if err != nil {
		return trace.Wrap(err, "replace cross refs")
	}
	return nil
}

// ListInboundCrossRefs returns refs from other namespaces that target the given namespace.
// @intent select the rows whose resolution may change after this namespace rebuilds.
// @domainRule self-namespace refs are excluded because the outbound rebuild already re-resolved them.
func (s *Store) ListInboundCrossRefs(ctx context.Context, toNamespace string) ([]graph.CrossRef, error) {
	var rows []graph.CrossRef
	err := s.db.WithContext(ctx).
		Where("to_namespace = ? AND from_namespace <> ?", toNamespace, toNamespace).
		Order("id").
		Find(&rows).Error
	if err != nil {
		return nil, trace.Wrap(err, "list inbound cross refs")
	}
	return rows, nil
}

// ListOutboundCrossRefs returns every cross ref originating from one namespace.
// @intent expose a namespace's declared external dependencies for listing and analysis.
func (s *Store) ListOutboundCrossRefs(ctx context.Context, fromNamespace string) ([]graph.CrossRef, error) {
	var rows []graph.CrossRef
	err := s.db.WithContext(ctx).
		Where("from_namespace = ?", fromNamespace).
		Order("id").
		Find(&rows).Error
	if err != nil {
		return nil, trace.Wrap(err, "list outbound cross refs")
	}
	return rows, nil
}

// UpdateCrossRefResolution updates one row's derived resolution state.
// @intent remap or invalidate a reference after its target namespace rebuilt.
// @sideEffect updates resolved_node_id and status of one cross_refs row.
func (s *Store) UpdateCrossRefResolution(ctx context.Context, id uint, resolvedNodeID *uint, status graph.CrossRefStatus) error {
	err := s.db.WithContext(ctx).Model(&graph.CrossRef{}).Where("id = ?", id).
		Updates(map[string]any{"resolved_node_id": resolvedNodeID, "status": status}).Error
	if err != nil {
		return trace.Wrap(err, "update cross ref resolution")
	}
	return nil
}
