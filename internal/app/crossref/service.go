// @index Cross-namespace reference sync: materializes @see ccg:// annotation tags into cross_refs rows.
package crossref

import (
	"context"
	"log/slog"

	"github.com/tae2089/trace"

	requestctx "github.com/tae2089/code-context-graph/internal/ctx"
	"github.com/tae2089/code-context-graph/internal/domain/graph"
	"github.com/tae2089/code-context-graph/internal/domain/reference"
)

// AnnotationRef pairs one ccg:// @see tag value with the node that declared it.
// @intent carry the minimal source facts needed to materialize a cross-namespace reference.
type AnnotationRef struct {
	NodeID uint
	Value  string
}

// Store exposes the persistence operations cross-ref sync needs.
// @intent keep the sync policy independent from GORM by owning a minimal consumer-side port.
type Store interface {
	ListAnnotationCCGRefs(ctx context.Context, namespace string) ([]AnnotationRef, error)
	ResolveCCGRef(ctx context.Context, ref reference.Ref) (uint, bool, error)
	ReplaceCrossRefsFrom(ctx context.Context, fromNamespace string, refs []graph.CrossRef) error
	ListInboundCrossRefs(ctx context.Context, toNamespace string) ([]graph.CrossRef, error)
	UpdateCrossRefResolution(ctx context.Context, id uint, resolvedNodeID *uint, status graph.CrossRefStatus) error
}

// Service rebuilds cross-namespace reference state after a namespace ingest.
// @intent keep cross_refs derived state consistent with annotations and both namespaces' current nodes.
type Service struct {
	Store  Store
	Logger *slog.Logger
}

// New constructs a cross-ref sync service.
// @intent bind the sync policy to one persistence port instance.
func New(store Store) *Service {
	return &Service{Store: store}
}

// SyncNamespace rebuilds outbound cross refs of the context namespace and re-resolves inbound ones.
// @intent run after a build/update commit so cross-namespace links reflect the namespace's new node identity.
// @domainRule outbound rows are fully replaced from current @see tags; malformed refs are skipped (lint owns reporting).
// @domainRule inbound rows are re-resolved because a replace-style build regenerates the target node ids.
// @sideEffect deletes, inserts, and updates cross_refs rows.
func (s *Service) SyncNamespace(ctx context.Context) error {
	if s == nil || s.Store == nil {
		return trace.New("cross-ref store is not configured")
	}
	ns := requestctx.FromContext(ctx)
	if err := s.rebuildOutbound(ctx, ns); err != nil {
		return trace.Wrap(err, "rebuild outbound cross refs")
	}
	if err := s.reresolveInbound(ctx, ns); err != nil {
		return trace.Wrap(err, "re-resolve inbound cross refs")
	}
	return nil
}

// @intent replace the namespace's outbound rows with rows derived from its current annotations.
func (s *Service) rebuildOutbound(ctx context.Context, ns string) error {
	tags, err := s.Store.ListAnnotationCCGRefs(ctx, ns)
	if err != nil {
		return err
	}
	rows := make([]graph.CrossRef, 0, len(tags))
	seen := map[AnnotationRef]bool{}
	for _, tag := range tags {
		if seen[tag] {
			continue
		}
		seen[tag] = true
		ref, err := reference.Parse(tag.Value)
		if err != nil {
			s.logger().Debug("skipping malformed ccg ref", "node_id", tag.NodeID, "value", tag.Value, "error", err)
			continue
		}
		resolvedID, status, err := s.resolve(ctx, *ref)
		if err != nil {
			return err
		}
		rows = append(rows, graph.CrossRef{
			FromNamespace:  ns,
			FromNodeID:     tag.NodeID,
			Raw:            ref.Raw,
			ToNamespace:    ref.Namespace,
			ToPath:         ref.Path,
			ToSymbol:       ref.Symbol,
			ResolvedNodeID: resolvedID,
			Status:         status,
			Source:         graph.CrossRefSourceAnnotation,
		})
	}
	return s.Store.ReplaceCrossRefsFrom(ctx, ns, rows)
}

// @intent update inbound rows whose resolution changed after this namespace's nodes were rebuilt.
func (s *Service) reresolveInbound(ctx context.Context, ns string) error {
	inbound, err := s.Store.ListInboundCrossRefs(ctx, ns)
	if err != nil {
		return err
	}
	for _, row := range inbound {
		ref := reference.Ref{Raw: row.Raw, Namespace: row.ToNamespace, Path: row.ToPath, Symbol: row.ToSymbol}
		resolvedID, status, err := s.resolve(ctx, ref)
		if err != nil {
			return err
		}
		if status == row.Status && equalNodeID(resolvedID, row.ResolvedNodeID) {
			continue
		}
		if err := s.Store.UpdateCrossRefResolution(ctx, row.ID, resolvedID, status); err != nil {
			return err
		}
	}
	return nil
}

// @intent translate matcher output into row state: namespace-scope hits stay resolved without a node target.
func (s *Service) resolve(ctx context.Context, ref reference.Ref) (*uint, graph.CrossRefStatus, error) {
	id, ok, err := s.Store.ResolveCCGRef(ctx, ref)
	if err != nil {
		return nil, graph.CrossRefStatusDead, err
	}
	if !ok {
		return nil, graph.CrossRefStatusDead, nil
	}
	if id == 0 {
		return nil, graph.CrossRefStatusResolved, nil
	}
	return &id, graph.CrossRefStatusResolved, nil
}

func equalNodeID(a, b *uint) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func (s *Service) logger() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}
