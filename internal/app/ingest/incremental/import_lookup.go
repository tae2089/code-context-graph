// @index Transaction-local import lookup decorator for staged incremental edge replay.
package incremental

import (
	"context"

	"github.com/tae2089/code-context-graph/internal/app/ingest/resolve"
	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

// importFileNodeLister is an optional store capability for one-shot import lookup indexing.
// @intent avoid expanding the legacy incremental Store contract for lightweight test doubles.
type importFileNodeLister interface {
	ListImportFileNodes(ctx context.Context) ([]graph.Node, error)
}

// fileSuffixLookup is the legacy import lookup capability implemented by production graph stores.
// @intent keep staged reconciliation compatible with custom stores that do not expose the bulk file-node query.
type fileSuffixLookup interface {
	GetFileNodesByPathSuffix(ctx context.Context, suffix string) ([]graph.Node, error)
}

// edgeReader preserves existing implements lookup when the decorated store supports it.
// @intent keep staged resolution behavior aligned with the underlying graph store capabilities.
type edgeReader interface {
	GetEdgesToNodes(ctx context.Context, nodeIDs []uint) ([]graph.Edge, error)
}

// importIndexedLookup shares one immutable import-file index across staged edge replay.
// @intent replace repeated suffix database scans with one transaction-local file-node snapshot.
type importIndexedLookup struct {
	Store
	fileNodesBySuffix map[string][]graph.Node
	index             *resolve.ImportFileIndex
}

// newImportIndexedLookup creates a lookup decorator for a single staged edge-resolution phase.
// @intent scope cached import paths to one update transaction and avoid stale store-wide state.
func newImportIndexedLookup(store Store) *importIndexedLookup {
	return &importIndexedLookup{
		Store:             store,
		fileNodesBySuffix: make(map[string][]graph.Node),
	}
}

// GetFileNodesByPathSuffix returns cached import candidates, initializing an immutable index once when supported.
// @intent preserve the legacy lookup fallback while avoiding repeated scans for staged bulk updates.
func (l *importIndexedLookup) GetFileNodesByPathSuffix(ctx context.Context, suffix string) ([]graph.Node, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if nodes, ok := l.fileNodesBySuffix[suffix]; ok {
		return nodes, nil
	}
	if l.index == nil {
		lister, ok := l.Store.(importFileNodeLister)
		if !ok {
			legacyLookup, ok := l.Store.(fileSuffixLookup)
			if !ok {
				return nil, nil
			}
			return legacyLookup.GetFileNodesByPathSuffix(ctx, suffix)
		}
		nodes, err := lister.ListImportFileNodes(ctx)
		if err != nil {
			return nil, err
		}
		l.index = resolve.NewImportFileIndex(nodes)
	}
	nodes := l.index.Find(suffix)
	l.fileNodesBySuffix[suffix] = nodes
	return nodes, nil
}

// GetEdgesToNodes forwards existing-edge reads when the underlying store supports them.
// @intent retain historical implements resolution while the lookup decorates import lookups.
func (l *importIndexedLookup) GetEdgesToNodes(ctx context.Context, nodeIDs []uint) ([]graph.Edge, error) {
	reader, ok := l.Store.(edgeReader)
	if !ok {
		return nil, nil
	}
	return reader.GetEdgesToNodes(ctx, nodeIDs)
}
