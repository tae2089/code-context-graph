// @index Consumer-owned graph and compatibility-index ports for the built-in CCG Wiki.
package wiki

import (
	"context"

	"github.com/tae2089/code-context-graph/internal/domain/graph"
	"github.com/tae2089/code-context-graph/internal/domain/reference"
)

// GraphView is one bounded, deterministic graph projection for the Wiki viewer.
// @intent carry viewer graph facts without exposing database queries to HTTP handlers.
type GraphView struct {
	TotalNodes int64
	Nodes      []graph.Node
	Edges      []graph.Edge
}

// GraphViewStage identifies the persistence stage that failed while building a viewer graph.
// @intent preserve stage-specific inbound error mapping without exposing database operations.
type GraphViewStage string

const (
	GraphViewStageCountNodes GraphViewStage = "count_nodes"
	GraphViewStageListNodes  GraphViewStage = "list_nodes"
	GraphViewStageListEdges  GraphViewStage = "list_edges"
)

// GraphViewError carries a graph-view stage while preserving the underlying error text.
// @intent let inbound adapters retain stable stage-specific responses across persistence implementations.
type GraphViewError struct {
	Stage GraphViewStage
	Err   error
}

// Error preserves the underlying persistence error detail.
// @intent satisfy error without leaking the application stage into the existing HTTP detail field.
func (e *GraphViewError) Error() string { return e.Err.Error() }

// Unwrap exposes the underlying cause for cancellation and driver error classification.
// @intent preserve errors.Is and errors.As behavior through graph-view stage classification.
func (e *GraphViewError) Unwrap() error { return e.Err }

// Repository supplies deterministic namespace-scoped graph facts for eager and lazy Wiki trees.
// @intent keep Wiki hierarchy and presentation policy independent of GORM query construction.
type Repository interface {
	Namespaces(ctx context.Context) ([]string, error)
	NavigationNodes(ctx context.Context, kinds []graph.NodeKind) ([]graph.Node, error)
	PathNodes(ctx context.Context, folderPath string, kinds []graph.NodeKind) ([]graph.Node, error)
	StoredNode(ctx context.Context, kind graph.NodeKind, filePath string) (*graph.Node, error)
	SymbolNode(ctx context.Context, qualifiedName string, kinds []graph.NodeKind) (*graph.Node, error)
	FileSymbols(ctx context.Context, filePath string, kinds []graph.NodeKind) ([]graph.Node, error)
	Annotations(ctx context.Context, nodeIDs []uint) (map[uint]*graph.Annotation, error)
	HasSymbol(ctx context.Context, filePath string, kinds []graph.NodeKind) (bool, error)
	GraphView(ctx context.Context, limit int, edgeKinds []graph.EdgeKind) (GraphView, error)
	ResolveReference(ctx context.Context, ref *reference.Ref) (*graph.Node, error)
}

// IndexWriter atomically persists the versioned Wiki compatibility snapshot.
// @intent let Wiki build policy choose namespace and payload without owning filesystem implementation.
type IndexWriter interface {
	WriteWikiIndex(ctx context.Context, namespace string, index *Index) error
}
