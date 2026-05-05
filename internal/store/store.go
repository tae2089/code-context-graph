// @index Collection of abstract interfaces for graph storage (nodes/edges/annotations).
package store

import (
	"context"

	"github.com/tae2089/code-context-graph/internal/model"
)

// NodeReader defines node lookup functionality.
// @intent Provides a contract for reading graph nodes by identifier and file.
type NodeReader interface {
	GetNode(ctx context.Context, qualifiedName string) (*model.Node, error)
	GetNodeByID(ctx context.Context, id uint) (*model.Node, error)
	GetNodesByIDs(ctx context.Context, ids []uint) ([]model.Node, error)
	GetNodesByQualifiedNames(ctx context.Context, names []string) (map[string][]model.Node, error)
	GetNodesByFile(ctx context.Context, filePath string) ([]model.Node, error)
	GetNodesByFiles(ctx context.Context, filePaths []string) (map[string][]model.Node, error)
}

// NodeWriter defines node writing functionality.
// @intent abstract the storage of graph nodes and deletion on a per-file basis.
type NodeWriter interface {
	UpsertNodes(ctx context.Context, nodes []model.Node) error
	DeleteNodesByFile(ctx context.Context, filePath string) error
	DeleteGraph(ctx context.Context) error
}

// EdgeStore defines edge storage and lookup functionality.
// @intent Provides a consistent interface for reading and writing graph relationships.
type EdgeStore interface {
	UpsertEdges(ctx context.Context, edges []model.Edge) error
	GetEdgesFrom(ctx context.Context, nodeID uint) ([]model.Edge, error)
	GetEdgesFromNodes(ctx context.Context, nodeIDs []uint) ([]model.Edge, error)
	GetEdgesTo(ctx context.Context, nodeID uint) ([]model.Edge, error)
	GetEdgesToNodes(ctx context.Context, nodeIDs []uint) ([]model.Edge, error)
	DeleteEdgesByFile(ctx context.Context, filePath string) error
}

// AnnotationStore defines annotation storage functionality.
// @intent abstract the storage and lookup of structured comments per node.
type AnnotationStore interface {
	UpsertAnnotation(ctx context.Context, ann *model.Annotation) error
	GetAnnotation(ctx context.Context, nodeID uint) (*model.Annotation, error)
}

// GraphStore is the unified contract for the graph repository.
// @intent Provides node, edge, annotation, and transaction functionality in one place.
type GraphStore interface {
	NodeReader
	NodeWriter
	EdgeStore
	AnnotationStore

	WithTx(ctx context.Context, fn func(store GraphStore) error) error
	AutoMigrate() error
}
