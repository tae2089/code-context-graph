// @index Dependency contracts and injected services for MCP handlers.
package mcp

import (
	"context"
	"log/slog"

	"github.com/tae2089/code-context-graph/internal/app/analyze"
	"github.com/tae2089/code-context-graph/internal/app/analyze/changes"
	flowspkg "github.com/tae2089/code-context-graph/internal/app/analyze/flow"
	impactpkg "github.com/tae2089/code-context-graph/internal/app/analyze/impact"
	"github.com/tae2089/code-context-graph/internal/app/analyze/query"
	"github.com/tae2089/code-context-graph/internal/app/ingest"
	"github.com/tae2089/code-context-graph/internal/app/ingest/incremental"
	"github.com/tae2089/code-context-graph/internal/app/search/document"
	"github.com/tae2089/code-context-graph/internal/app/search/retrieval"
	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

// Parser defines the source parser contract used by MCP graph builds.
// @intent Injects an abstract parser to combine language-specific parsing implementations on the server.
// @see mcp.Deps
type Parser interface {
	Parse(filePath string, content []byte) ([]graph.Node, []graph.Edge, error)
	ParseWithContext(ctx context.Context, filePath string, content []byte) ([]graph.Node, []graph.Edge, error)
}

// ChangeAnalyzer is the application change-risk surface consumed by tools and prompts.
// @intent inject a configured application change service without exposing Git or persistence implementations.
type ChangeAnalyzer interface {
	AnalyzePage(ctx context.Context, repoDir, baseRef string, limit, offset int) (changes.Result, error)
	ChangedNodeIDs(ctx context.Context, repoDir, baseRef string) ([]uint, error)
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
	CallersOf(ctx context.Context, nodeID uint) ([]graph.Node, error)
	CalleesOf(ctx context.Context, nodeID uint) ([]graph.Node, error)
	CallersOfPage(ctx context.Context, nodeID uint, opts query.QueryOptions) (query.PagedNodes, error)
	CalleesOfPage(ctx context.Context, nodeID uint, opts query.QueryOptions) (query.PagedNodes, error)
	CallersOfWithOptions(ctx context.Context, nodeID uint, opts query.QueryOptions) ([]graph.Node, error)
	CalleesOfWithOptions(ctx context.Context, nodeID uint, opts query.QueryOptions) ([]graph.Node, error)
	ImportsOf(ctx context.Context, nodeID uint) ([]graph.Node, error)
	ImportersOf(ctx context.Context, nodeID uint) ([]graph.Node, error)
	ImportsOfPage(ctx context.Context, nodeID uint, opts query.QueryOptions) (query.PagedNodes, error)
	ImportersOfPage(ctx context.Context, nodeID uint, opts query.QueryOptions) (query.PagedNodes, error)
	ChildrenOf(ctx context.Context, nodeID uint) ([]graph.Node, error)
	ChildrenOfPage(ctx context.Context, nodeID uint, opts query.QueryOptions) (query.PagedNodes, error)
	TestsFor(ctx context.Context, nodeID uint) ([]graph.Node, error)
	TestsForPage(ctx context.Context, nodeID uint, opts query.QueryOptions) (query.PagedNodes, error)
	InheritorsOf(ctx context.Context, nodeID uint) ([]graph.Node, error)
	InheritorsOfPage(ctx context.Context, nodeID uint, opts query.QueryOptions) (query.PagedNodes, error)
	FileSummaryOf(ctx context.Context, filePath string) (*query.FileSummary, error)
	FindExactNameMatches(ctx context.Context, target string, limit int) ([]query.CandidateMatch, error)
}

// IncrementalSyncer defines the incremental graph synchronization contract.
// @intent Injects a syncer that reflects only changed files into the graph without full re-parsing.
// @see mcp.handlers.buildOrUpdateGraph
type IncrementalSyncer interface {
	Sync(ctx context.Context, files map[string]incremental.FileInfo) (*incremental.SyncStats, error)
	SyncWithExisting(ctx context.Context, files map[string]incremental.FileInfo, existingFiles []string) (*incremental.SyncStats, error)
}

// BuildToolsDeps owns graph mutation and postprocess dependencies.
// @intent group only the dependencies required by parse, build, update, and postprocess tools.
type BuildToolsDeps struct {
	Store       ingest.GraphStore
	Walkers     map[string]Parser
	UnitOfWork  ingest.UnitOfWork
	Search      ingest.SearchWriter
	Maintenance document.Maintenance
	FlowBuilder FlowBuilder
	Incremental IncrementalSyncer
}

// GraphToolsDeps owns graph lookup, query, search, and aggregate dependencies.
// @intent group only the dependencies required by graph and search read tools.
type GraphToolsDeps struct {
	Store      analyze.GraphLookup
	Query      QueryService
	Search     retrieval.CandidateSearcher
	Statistics analyze.StatisticsReader
	Reader     analyze.GraphReadRepository
}

// CrossRefLister exposes materialized cross-namespace references for listing tools.
// @intent let handlers enumerate repository-level dependencies without a store implementation dependency.
type CrossRefLister interface {
	ListOutboundCrossRefs(ctx context.Context, fromNamespace string) ([]graph.CrossRef, error)
	ListInboundCrossRefs(ctx context.Context, toNamespace string) ([]graph.CrossRef, error)
}

// AnalysisToolsDeps owns bounded impact, flow, and git-change analysis dependencies.
// @intent group only configured application analyzers and their read-model port.
// @domainRule CrossImpact/CrossFlow/CrossRefs are optional; when nil the cross-namespace analysis surface reports itself unconfigured.
type AnalysisToolsDeps struct {
	Impact      ImpactAnalyzer
	Flow        FlowTracer
	Changes     ChangeAnalyzer
	Reader      analyze.GraphReadRepository
	CrossImpact ImpactAnalyzer
	CrossFlow   FlowTracer
	CrossRefs   CrossRefLister
}

// DocsToolsDeps owns DB-primary documentation retrieval.
// @intent group the configured application retrieval service used by documentation tools.
type DocsToolsDeps struct {
	Retrieval *retrieval.Service
}

// RuntimeToolsDeps owns cross-cutting cache, logging, paths, and request limits.
// @intent group transport runtime configuration separately from capability dependencies.
type RuntimeToolsDeps struct {
	Logger              *slog.Logger
	Cache               *Cache
	RagIndexDir         string
	RagProjectDesc      string
	NamespaceRoot       string
	RepoRoot            string
	MaxFileBytes        int64
	MaxTotalParsedBytes int64
}

// Deps groups MCP dependencies by registered tool surface.
// @intent make each MCP capability's required application contracts explicit at composition time.
type Deps struct {
	Build    BuildToolsDeps
	Graph    GraphToolsDeps
	Analysis AnalysisToolsDeps
	Docs     DocsToolsDeps
	Runtime  RuntimeToolsDeps
}
