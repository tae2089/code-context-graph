// @index Dependency contracts and injected services for MCP handlers.
package mcp

import (
	"context"
	"log/slog"

	"gorm.io/gorm"

	"github.com/tae2089/code-context-graph/internal/analysis/changes"
	"github.com/tae2089/code-context-graph/internal/analysis/community"
	"github.com/tae2089/code-context-graph/internal/analysis/coupling"
	"github.com/tae2089/code-context-graph/internal/analysis/coverage"
	"github.com/tae2089/code-context-graph/internal/analysis/deadcode"
	fallbackanalysis "github.com/tae2089/code-context-graph/internal/analysis/fallback"
	flowspkg "github.com/tae2089/code-context-graph/internal/analysis/flows"
	impactpkg "github.com/tae2089/code-context-graph/internal/analysis/impact"
	"github.com/tae2089/code-context-graph/internal/analysis/incremental"
	"github.com/tae2089/code-context-graph/internal/analysis/largefunc"
	"github.com/tae2089/code-context-graph/internal/analysis/query"
	"github.com/tae2089/code-context-graph/internal/model"
	"github.com/tae2089/code-context-graph/internal/paging"
	postprocesspolicy "github.com/tae2089/code-context-graph/internal/postprocess/policy"
	"github.com/tae2089/code-context-graph/internal/store"
	storesearch "github.com/tae2089/code-context-graph/internal/store/search"
)

// Parser defines the source parser contract used by MCP graph builds.
// @intent Injects an abstract parser to combine language-specific parsing implementations on the server.
// @see mcp.Deps
type Parser interface {
	Parse(filePath string, content []byte) ([]model.Node, []model.Edge, error)
	ParseWithContext(ctx context.Context, filePath string, content []byte) ([]model.Node, []model.Edge, error)
}

// ImpactAnalyzer defines the bounded blast-radius analysis contract for graph nodes.
// @intent inject a node/depth-capped blast-radius analyzer so a single MCP request cannot
// expand into an unbounded graph walk.
// @see mcp.handlers.getImpactRadius
type ImpactAnalyzer interface {
	ImpactRadiusBounded(ctx context.Context, nodeID uint, depth int, opts impactpkg.RadiusOptions) (*impactpkg.RadiusResult, error)
}

// FlowTracer defines the bounded call-flow tracing contract for graph nodes.
// @intent inject a node-capped call-flow tracer so a deep call chain cannot expand into an
// unbounded traversal.
// @see mcp.handlers.traceFlow
type FlowTracer interface {
	TraceFlowBounded(ctx context.Context, startNodeID uint, opts flowspkg.TraceOptions) (*flowspkg.TraceResult, error)
}

// FlowBuilder defines the persisted flow rebuild contract.
// @intent Injects a builder into the MCP handler that regenerates stored flow post-processing results.
// @see mcp.handlers.runPostprocess
type FlowBuilder interface {
	Rebuild(ctx context.Context, cfg flowspkg.Config) ([]flowspkg.Stats, error)
}

// QueryService defines predefined graph query operations exposed over MCP.
// @intent Simplifies handlers by abstracting standard graph queries into a single service interface.
// @see mcp.handlers.queryGraph
type QueryService interface {
	CallersOf(ctx context.Context, nodeID uint) ([]model.Node, error)
	CalleesOf(ctx context.Context, nodeID uint) ([]model.Node, error)
	CallersOfPage(ctx context.Context, nodeID uint, opts query.QueryOptions) (query.PagedNodes, error)
	CalleesOfPage(ctx context.Context, nodeID uint, opts query.QueryOptions) (query.PagedNodes, error)
	CallersOfWithOptions(ctx context.Context, nodeID uint, opts query.QueryOptions) ([]model.Node, error)
	CalleesOfWithOptions(ctx context.Context, nodeID uint, opts query.QueryOptions) ([]model.Node, error)
	ImportsOf(ctx context.Context, nodeID uint) ([]model.Node, error)
	ImportersOf(ctx context.Context, nodeID uint) ([]model.Node, error)
	ImportsOfPage(ctx context.Context, nodeID uint, opts query.QueryOptions) (query.PagedNodes, error)
	ImportersOfPage(ctx context.Context, nodeID uint, opts query.QueryOptions) (query.PagedNodes, error)
	ChildrenOf(ctx context.Context, nodeID uint) ([]model.Node, error)
	ChildrenOfPage(ctx context.Context, nodeID uint, opts query.QueryOptions) (query.PagedNodes, error)
	TestsFor(ctx context.Context, nodeID uint) ([]model.Node, error)
	TestsForPage(ctx context.Context, nodeID uint, opts query.QueryOptions) (query.PagedNodes, error)
	InheritorsOf(ctx context.Context, nodeID uint) ([]model.Node, error)
	InheritorsOfPage(ctx context.Context, nodeID uint, opts query.QueryOptions) (query.PagedNodes, error)
	FileSummaryOf(ctx context.Context, filePath string) (*query.FileSummary, error)
	FindExactNameMatches(ctx context.Context, target string, limit int) ([]query.CandidateMatch, error)
}

// LargefuncAnalyzer defines the oversized-function detection contract.
// @intent Injects an analyzer to detect large functions with high maintenance costs.
// @see mcp.handlers.findLargeFunctions
type LargefuncAnalyzer interface {
	Find(ctx context.Context, threshold int) ([]model.Node, error)
	FindPage(ctx context.Context, opts largefunc.Options) (largefunc.Result, error)
}

// DeadcodeAnalyzer defines the unused-code detection contract.
// @intent Injects an analyzer to find unreferenced nodes as candidates for cleanup.
// @see mcp.handlers.findDeadCode
type DeadcodeAnalyzer interface {
	Find(ctx context.Context, opts deadcode.Options) ([]model.Node, error)
	FindPage(ctx context.Context, opts deadcode.Options) (deadcode.Result, error)
}

// FallbackAnalyzer defines the suspect fallback-edge analysis contract.
// @intent Detects untrustworthy fallback call edges based on annotation overlap.
// @see mcp.handlers.findSuspectFallbackEdges
type FallbackAnalyzer interface {
	FindSuspects(ctx context.Context, opts fallbackanalysis.Options) ([]fallbackanalysis.SuspectEdge, error)
	FindSuspectsPage(ctx context.Context, opts fallbackanalysis.Options) (fallbackanalysis.Result, error)
}

// CouplingAnalyzer defines the inter-community coupling analysis contract.
// @intent Connects an analyzer to the server that calculates coupling between architectural boundaries.
// @see mcp.handlers.getArchitectureOverview
type CouplingAnalyzer interface {
	Analyze(ctx context.Context) ([]coupling.CouplingPair, error)
	AnalyzePage(ctx context.Context, req paging.Request) (coupling.Result, error)
}

// CoverageAnalyzer defines file and community coverage lookup operations.
// @intent Provides test coverage information for risk summaries and community detail responses.
// @see mcp.handlers.getCommunity
// @see mcp.promptHandlers.reviewChanges
type CoverageAnalyzer interface {
	ByFile(ctx context.Context, filePath string) (*coverage.FileCoverage, error)
	ByCommunity(ctx context.Context, communityID uint) (*coverage.CommunityCoverage, error)
}

// CommunityBuilder defines the community rebuild contract.
// @intent Injects an implementation to recalculate module communities during graph post-processing.
// @see mcp.handlers.runPostprocess
type CommunityBuilder interface {
	Rebuild(ctx context.Context, cfg community.Config) ([]community.Stats, error)
}

// IncrementalSyncer defines the incremental graph synchronization contract.
// @intent Injects a syncer that reflects only changed files into the graph without full re-parsing.
// @see mcp.handlers.buildOrUpdateGraph
type IncrementalSyncer interface {
	Sync(ctx context.Context, files map[string]incremental.FileInfo) (*incremental.SyncStats, error)
	SyncWithExisting(ctx context.Context, files map[string]incremental.FileInfo, existingFiles []string) (*incremental.SyncStats, error)
}

// PostprocessPolicy gates and records automatic postprocess runs so repeated failures can be detected and suppressed.
// @intent centralize automatic postprocess decisions so repeated failures can degrade execution before handlers retry expensive work.
type PostprocessPolicy interface {
	Resolve(ctx context.Context, input postprocesspolicy.DecisionInput) (string, string, error)
	RecordRun(ctx context.Context, record postprocesspolicy.RunRecord) error
	Status(ctx context.Context, opts postprocesspolicy.StatusOptions) (*postprocesspolicy.StatusSummary, error)
	Reset(ctx context.Context, tool string) error
}

// Deps collects the services and stores required by MCP handlers.
// @intent Assembles tool and prompt handlers by injecting all MCP server components at once.
type Deps struct {
	Store            store.GraphStore
	DB               *gorm.DB
	Parser           Parser
	Walkers          map[string]Parser
	SearchBackend    storesearch.Backend
	ImpactAnalyzer   ImpactAnalyzer
	FlowTracer       FlowTracer
	ChangesGitClient changes.GitClient
	Logger           *slog.Logger

	// Added in Phase 11
	QueryService      QueryService
	LargefuncAnalyzer LargefuncAnalyzer
	DeadcodeAnalyzer  DeadcodeAnalyzer
	FallbackAnalyzer  FallbackAnalyzer
	CouplingAnalyzer  CouplingAnalyzer
	CoverageAnalyzer  CoverageAnalyzer
	CommunityBuilder  CommunityBuilder
	FlowBuilder       FlowBuilder
	Incremental       IncrementalSyncer
	PostprocessPolicy PostprocessPolicy

	// Cache — nil disables caching
	Cache *Cache

	// RagIndexDir — Directory where doc-index.json is stored (default: ".ccg")
	RagIndexDir string
	// RagProjectDesc — Project description used in root node summary
	RagProjectDesc string

	NamespaceRoot string
	RepoRoot      string

	MaxFileBytes        int64
	MaxTotalParsedBytes int64

	// RefreshSearchDocuments overrides the search-document refresh used after a build; nil uses
	// service.RefreshSearchDocuments. Kept as an injectable field so tests need no package globals.
	RefreshSearchDocuments func(ctx context.Context, db *gorm.DB) (int, error)
}
