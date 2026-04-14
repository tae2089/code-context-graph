package store

import (
	"context"

	"github.com/imtaebin/code-context-graph/internal/model"
)

type GraphStore interface {
	UpsertNodes(ctx context.Context, nodes []model.Node) error
	GetNode(ctx context.Context, qualifiedName string) (*model.Node, error)
	GetNodeByID(ctx context.Context, id uint) (*model.Node, error)
	GetNodesByIDs(ctx context.Context, ids []uint) ([]model.Node, error)
	GetNodesByFile(ctx context.Context, filePath string) ([]model.Node, error)
	DeleteNodesByFile(ctx context.Context, filePath string) error

	UpsertEdges(ctx context.Context, edges []model.Edge) error
	GetEdgesFrom(ctx context.Context, nodeID uint) ([]model.Edge, error)
	GetEdgesFromNodes(ctx context.Context, nodeIDs []uint) ([]model.Edge, error)
	GetEdgesTo(ctx context.Context, nodeID uint) ([]model.Edge, error)
	GetEdgesToNodes(ctx context.Context, nodeIDs []uint) ([]model.Edge, error)
	DeleteEdgesByFile(ctx context.Context, filePath string) error

	UpsertAnnotation(ctx context.Context, ann *model.Annotation) error
	GetAnnotation(ctx context.Context, nodeID uint) (*model.Annotation, error)

	WithTx(ctx context.Context, fn func(store GraphStore) error) error

	AutoMigrate() error
}
