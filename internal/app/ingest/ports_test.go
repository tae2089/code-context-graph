package ingest

import (
	"context"

	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

type graphStoreContractStub struct{}

func (graphStoreContractStub) GetNodesByIDs(context.Context, []uint) ([]graph.Node, error) {
	return nil, nil
}
func (graphStoreContractStub) GetNodesByFile(context.Context, string) ([]graph.Node, error) {
	return nil, nil
}
func (graphStoreContractStub) GetNodesByFiles(context.Context, []string) (map[string][]graph.Node, error) {
	return nil, nil
}
func (graphStoreContractStub) GetNodesByQualifiedNames(context.Context, []string) (map[string][]graph.Node, error) {
	return nil, nil
}
func (graphStoreContractStub) ListFileNodes(context.Context) ([]graph.Node, error) { return nil, nil }
func (graphStoreContractStub) ListImportFileNodes(context.Context) ([]graph.Node, error) {
	return nil, nil
}
func (graphStoreContractStub) GetFileNodesByPathSuffix(context.Context, string) ([]graph.Node, error) {
	return nil, nil
}
func (graphStoreContractStub) GetEdgesFromNodes(context.Context, []uint) ([]graph.Edge, error) {
	return nil, nil
}
func (graphStoreContractStub) GetEdgesToNodes(context.Context, []uint) ([]graph.Edge, error) {
	return nil, nil
}
func (graphStoreContractStub) UpsertNodes(context.Context, []graph.Node) error { return nil }
func (graphStoreContractStub) UpsertEdges(context.Context, []graph.Edge) error { return nil }
func (graphStoreContractStub) UpsertAnnotation(context.Context, *graph.Annotation) error {
	return nil
}
func (graphStoreContractStub) DeleteNodesByFile(context.Context, string) error { return nil }
func (graphStoreContractStub) DeleteEdgesByFile(context.Context, string) error { return nil }
func (graphStoreContractStub) DeletePackageSemanticEdges(context.Context, []string) error {
	return nil
}
func (graphStoreContractStub) DeleteGraph(context.Context) error { return nil }

type searchWriterContractStub struct{}

func (searchWriterContractStub) RebuildAll(context.Context) error           { return nil }
func (searchWriterContractStub) RebuildNodes(context.Context, []uint) error { return nil }

type transactionContractStub struct{}

func (transactionContractStub) Graph() GraphStore    { return graphStoreContractStub{} }
func (transactionContractStub) Search() SearchWriter { return searchWriterContractStub{} }

type unitOfWorkContractStub struct{}

func (unitOfWorkContractStub) WithinTransaction(_ context.Context, fn func(Transaction) error) error {
	return fn(transactionContractStub{})
}

var (
	_ GraphStore   = graphStoreContractStub{}
	_ SearchWriter = searchWriterContractStub{}
	_ Transaction  = transactionContractStub{}
	_ UnitOfWork   = unitOfWorkContractStub{}
)
