// @index Graph building service that orchestrates parsing, persistence, and search indexing.
package workflow

import (
	"context"
	"fmt"
	"log/slog"

	ingestapp "github.com/tae2089/code-context-graph/internal/app/ingest"
	"github.com/tae2089/code-context-graph/internal/app/ingest/resolve"
	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

type languagePackageInfo = ingestapp.PackageInfo

// @intent abstract build-time edge resolution so tests can inject resolver behavior per Service.
type resolveBuildEdgesFn func(ctx context.Context, lookup resolve.NodeLookup, edges []graph.Edge, options resolve.ResolveOptions) ([]graph.Edge, error)

const (
	buildFlushFileBatchSize   = 100
	buildFlushParsedBytes     = 16 << 20
	buildEdgeResolveChunkSize = 4000
	buildParseWorkerCount     = 4
	forceReparseEdgeChunkSize = 400
	scopedINQueryChunkSize    = 400
)

// Parser defines the minimum parser behavior required by graph builds.
// @intent let CLI, MCP, and tests share the same build policy with their parser implementations
type Parser = ingestapp.Parser

// @intent 메타데이터와 언어 정보까지 돌려주는 parser 확장을 build 경로에서 감지하게 한다.
type metadataParserWithLanguage = ingestapp.MetadataParser

// IncrementalSyncer defines the sync operation Service.Update delegates to.
// @intent keep filesystem/build policy in service while preserving the existing incremental engine
type IncrementalSyncer = ingestapp.IncrementalSyncer

// @intent allow incremental sync implementations to participate in the same transaction-scoped store used by Service updates.
type transactionalIncrementalSyncer = ingestapp.TransactionalIncrementalSyncer

// Service orchestrates graph building and search document refresh.
// @intent 파싱 결과 저장과 검색 인덱스 재구성을 하나의 서비스로 묶는다.
type Service struct {
	Store      ingestapp.GraphStore
	UnitOfWork ingestapp.UnitOfWork
	Search     ingestapp.SearchWriter
	Walkers    map[string]Parser
	Parsers    map[string]Parser
	Logger     *slog.Logger

	// resolveEdges overrides build-time edge resolution; nil uses resolve.ResolveWithOptions.
	// onBatchRelease, when set, is notified after each node batch is persisted and its buffers
	// are released. Both are test seams kept per-instance so the server has no mutable globals.
	resolveEdges   resolveBuildEdgesFn
	onBatchRelease func([]parsedBuildNodeBatch, int)
}

// @intent resolve build edges through the injected resolver, defaulting to the production resolver.
func (s *Service) edgeResolver() resolveBuildEdgesFn {
	if s.resolveEdges != nil {
		return s.resolveEdges
	}
	return resolve.ResolveWithOptions
}

// logger returns the configured slog.Logger or the process default when none was supplied.
// @intent keep service code logging-safe even when callers leave Logger nil.
func (s *Service) logger() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}

// parserForExt resolves a Parser for the given file extension, preferring an explicit Parsers map over Walkers.
// @intent let tests inject custom parsers while still using the production walker registry by default.
func (s *Service) parserForExt(ext string) (Parser, bool) {
	if s.Parsers != nil {
		parser, ok := s.Parsers[ext]
		return parser, ok
	}
	parser, ok := s.Walkers[ext]
	return parser, ok
}

// BuildOptions configures one graph build run.
// @intent 빌드 대상 경로와 탐색 범위를 호출자에서 제어하게 한다.
type BuildOptions struct {
	Dir                 string
	NoRecursive         bool
	ExcludePatterns     []string
	IncludePaths        []string
	MaxFileBytes        int64
	MaxTotalParsedBytes int64
	SkipSearchRebuild   bool
	FallbackCalls       bool
}

// BuildStats reports how much content a build processed.
// @intent CLI와 호출자가 빌드 결과 규모를 사용자에게 보여줄 수 있게 한다.
type BuildStats struct {
	TotalFiles int
	TotalNodes int
	TotalEdges int
	Unresolved resolve.FilterResolvedDiagnostics
	Timing     BuildTiming
}

// BuildTiming reports elapsed milliseconds for the major full-build stages.
// @intent expose actionable stage-level evidence so large-build regressions can be diagnosed without guessing.
type BuildTiming struct {
	ParseMS         int64
	PersistNodesMS  int64
	ResolveEdgesMS  int64
	SearchRebuildMS int64
	TotalMS         int64
	Resolve         BuildResolveTiming
}

// BuildResolveOperationTiming reports one resolver operation's call count and elapsed time.
// @intent show which edge-resolution store operation dominates a full build without changing resolution behavior.
type BuildResolveOperationTiming struct {
	Calls int
	MS    int64
}

// BuildResolveTiming breaks edge resolution into resolver, store-read, and edge-write operations.
// @intent expose actionable evidence for optimizing the remaining full-build bottleneck.
type BuildResolveTiming struct {
	Resolver              BuildResolveOperationTiming
	NodesByIDs            BuildResolveOperationTiming
	NodesByFiles          BuildResolveOperationTiming
	NodesByQualifiedNames BuildResolveOperationTiming
	ImportFileNodes       BuildResolveOperationTiming
	EdgesToNodes          BuildResolveOperationTiming
	UpsertEdges           BuildResolveOperationTiming
}

// UpdateOptions configures one incremental graph sync run.
// @intent reuse Service traversal and parse limit policy for CLI and MCP updates
type UpdateOptions struct {
	BuildOptions
	Syncer           IncrementalSyncer
	Replace          bool
	FailOnUnreadable bool
}

// UnreadableFilesError signals that the update path encountered files it could
// not stat or read while FailOnUnreadable was enabled.
// @intent give webhook/server callers a structured failure they can surface instead of silent partial sync
type UnreadableFilesError struct {
	Files []string
}

// Error formats the unreadable file failure with a count and a representative sample path.
// @intent give operators a stable, single-line summary they can grep instead of dumping every path.
func (e *UnreadableFilesError) Error() string {
	if e == nil || len(e.Files) == 0 {
		return "unreadable files encountered during update"
	}
	sample := e.Files[0]
	return fmt.Sprintf("unreadable files encountered during update: count=%d sample=%s", len(e.Files), sample)
}
