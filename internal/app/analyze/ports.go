// @index Consumer-owned outbound ports shared by graph analysis use cases.
package analyze

import (
	"context"

	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

// FlowRebuildStore is the transaction-scoped graph and flow persistence surface used by a rebuild.
// @intent let flow application policy trace and replace flows without importing a database adapter.
type FlowRebuildStore interface {
	GetEdgesFrom(ctx context.Context, nodeID uint) ([]graph.Edge, error)
	GetNodeByID(ctx context.Context, id uint) (*graph.Node, error)
	DeleteFlows(ctx context.Context) error
	FindFlowEntrypoints(ctx context.Context) ([]graph.Node, error)
	CreateFlow(ctx context.Context, flow *graph.Flow) error
}

// FlowUnitOfWork owns the atomic boundary for replacing persisted flows.
// @intent ensure stale-flow deletion and every replacement flow commit or roll back together.
type FlowUnitOfWork interface {
	WithinFlowRebuild(ctx context.Context, fn func(FlowRebuildStore) error) error
}

// EdgeDirection selects which endpoint of a relationship query becomes the result node.
// @intent express incoming and outgoing graph queries without leaking SQL join details.
type EdgeDirection string

const (
	EdgeDirectionIncoming EdgeDirection = "incoming"
	EdgeDirectionOutgoing EdgeDirection = "outgoing"
)

// RelatedNodesRequest describes one deterministic relationship page lookup.
// @intent carry graph-query scope and pagination from application policy to persistence.
type RelatedNodesRequest struct {
	NodeID    uint
	EdgeKinds []graph.EdgeKind
	Direction EdgeDirection
	Limit     int
	Offset    int
}

// RelatedNodesPage returns one relationship page and its unpaginated distinct count.
// @intent keep pagination totals coupled to the same namespace-scoped relationship query.
type RelatedNodesPage struct {
	Nodes      []graph.Node
	TotalCount int
}

// QueryRepository supplies persistence operations required by predefined graph-query policy.
// @intent keep query defaults and response mapping in app code while isolating database joins and filters.
type QueryRepository interface {
	RelatedNodes(ctx context.Context, request RelatedNodesRequest) (RelatedNodesPage, error)
	NodesByFile(ctx context.Context, filePath string) ([]graph.Node, error)
	NodesByExactName(ctx context.Context, name string, limit int) ([]graph.Node, error)
}

// ChangeRepository supplies graph facts used by git-diff overlap and risk policy.
// @intent isolate namespace-scoped node and outgoing-edge queries from change analysis algorithms.
type ChangeRepository interface {
	NodesByFiles(ctx context.Context, filePaths []string) ([]graph.Node, error)
	OutgoingEdgeCounts(ctx context.Context, nodeIDs []uint) (map[uint]int64, error)
}

// GraphStatistics is the namespace-scoped graph health snapshot consumed by
// operator and protocol surfaces.
// @intent keep graph totals and grouped distributions independent of database query types.
type GraphStatistics struct {
	NodeCount       int64
	EdgeCount       int64
	FileCount       int64
	NodesByKind     map[string]int64
	NodesByLanguage map[string]int64
	EdgesByKind     map[string]int64
	StrictCalls     int64
	FallbackCalls   int64
	NodeKinds       []KindCount
	EdgeKinds       []KindCount
}

// KindCount is one stable grouped aggregate row.
// @intent preserve database aggregate row ordering for CLI-compatible rendering.
type KindCount struct {
	Kind  string
	Count int64
}

// StatisticsReader supplies one consistent namespace-scoped graph statistics snapshot.
// @intent let CLI and MCP status surfaces share typed graph facts without receiving a database handle.
type StatisticsReader interface {
	GraphStatistics(ctx context.Context) (GraphStatistics, error)
}

// GraphLookup supplies the exact node and annotation reads used by graph-facing inbound tools.
// @intent keep MCP graph lookups on an application-owned port instead of a global storage contract.
type GraphLookup interface {
	GetNode(ctx context.Context, qualifiedName string) (*graph.Node, error)
	GetAnnotation(ctx context.Context, nodeID uint) (*graph.Annotation, error)
}

// NamespaceSummary is one globally discoverable graph namespace and its node count.
// @intent carry namespace discovery results independently of MCP response types.
type NamespaceSummary struct {
	Namespace string
	NodeCount int64
}

// FlowSummary is one stored flow with its member count.
// @intent carry bounded stored-flow facts independently of persistence rows.
type FlowSummary struct {
	ID          uint
	Name        string
	Description string
	NodeCount   int
}

// AffectedFlow is a stored flow and the changed node IDs that touch it.
// @intent carry change-to-flow overlap facts from analysis persistence to application consumers.
type AffectedFlow struct {
	ID            uint
	Name          string
	AffectedNodes []uint
}

// NamedCount is a ranked community or flow label and its member count.
// @intent represent ranked membership aggregates without exposing SQL scan structs.
type NamedCount struct {
	Name  string
	Count int64
}

// GraphReadRepository supplies bounded graph summaries used by analysis-oriented inbound surfaces.
// @intent centralize namespace-safe aggregate and evidence queries without exposing GORM to handlers.
type GraphReadRepository interface {
	NamespacesPage(ctx context.Context, limit, offset int) ([]NamespaceSummary, bool, error)
	FlowsPage(ctx context.Context, sortBy string, limit, offset int) ([]FlowSummary, bool, error)
	CallEdges(ctx context.Context, anchorID uint, peerIDs []uint, direction EdgeDirection) (map[uint]graph.Edge, error)
	AffectedFlowsPage(ctx context.Context, changedNodeIDs []uint, limit, offset int) ([]AffectedFlow, bool, error)
	UntestedCount(ctx context.Context, nodeIDs []uint) (int, error)
	TopCommunities(ctx context.Context, limit int) ([]NamedCount, error)
	TopFlows(ctx context.Context, limit int) ([]NamedCount, error)
}
