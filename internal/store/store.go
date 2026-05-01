package store

import (
	"context"

	"github.com/tae2089/code-context-graph/internal/model"
)

// NodeReader는 노드 조회 기능을 정의한다.
// @intent 그래프 노드를 식별자와 파일 기준으로 읽는 계약을 제공한다.
type NodeReader interface {
	GetNode(ctx context.Context, qualifiedName string) (*model.Node, error)
	GetNodeByID(ctx context.Context, id uint) (*model.Node, error)
	GetNodesByIDs(ctx context.Context, ids []uint) ([]model.Node, error)
	GetNodesByQualifiedNames(ctx context.Context, names []string) (map[string]*model.Node, error)
	GetNodesByFile(ctx context.Context, filePath string) ([]model.Node, error)
	GetNodesByFiles(ctx context.Context, filePaths []string) (map[string][]model.Node, error)
}

// NodeWriter는 노드 쓰기 기능을 정의한다.
// @intent 그래프 노드의 저장과 파일 단위 삭제를 추상화한다.
type NodeWriter interface {
	UpsertNodes(ctx context.Context, nodes []model.Node) error
	DeleteNodesByFile(ctx context.Context, filePath string) error
	DeleteGraph(ctx context.Context) error
}

// EdgeStore는 엣지 저장과 조회 기능을 정의한다.
// @intent 그래프 관계의 읽기·쓰기를 일관된 인터페이스로 제공한다.
type EdgeStore interface {
	UpsertEdges(ctx context.Context, edges []model.Edge) error
	GetEdgesFrom(ctx context.Context, nodeID uint) ([]model.Edge, error)
	GetEdgesFromNodes(ctx context.Context, nodeIDs []uint) ([]model.Edge, error)
	GetEdgesTo(ctx context.Context, nodeID uint) ([]model.Edge, error)
	GetEdgesToNodes(ctx context.Context, nodeIDs []uint) ([]model.Edge, error)
	DeleteEdgesByFile(ctx context.Context, filePath string) error
}

// AnnotationStore는 어노테이션 저장 기능을 정의한다.
// @intent 노드별 구조화 주석의 저장과 조회를 추상화한다.
type AnnotationStore interface {
	UpsertAnnotation(ctx context.Context, ann *model.Annotation) error
	GetAnnotation(ctx context.Context, nodeID uint) (*model.Annotation, error)
}

// GraphStore는 그래프 저장소의 통합 계약이다.
// @intent 노드, 엣지, 어노테이션과 트랜잭션 기능을 한 번에 제공한다.
type GraphStore interface {
	NodeReader
	NodeWriter
	EdgeStore
	AnnotationStore

	WithTx(ctx context.Context, fn func(store GraphStore) error) error
	AutoMigrate() error
}
