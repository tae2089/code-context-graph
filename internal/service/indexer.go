// @index Graph building service that orchestrates parsing, persistence, and search indexing.
package service

import (
	"context"
	"fmt"
	"log/slog"

	"gorm.io/gorm"

	"github.com/tae2089/code-context-graph/internal/analysis/incremental"
	"github.com/tae2089/code-context-graph/internal/edgeresolve"
	"github.com/tae2089/code-context-graph/internal/model"
	"github.com/tae2089/code-context-graph/internal/parse/treesitter"
	"github.com/tae2089/code-context-graph/internal/store"
	"github.com/tae2089/code-context-graph/internal/store/search"
)

type languagePackageInfo = treesitter.PackageInfo

var (
	testBuildBatchReleaseHook func([]parsedBuildNodeBatch, int)
	resolveBuildEdges         resolveBuildEdgesFn = edgeresolve.ResolveWithOptions
)

// @intent abstract build-time edge resolution so tests and build paths can swap resolver behavior without rewiring callers.
type resolveBuildEdgesFn func(ctx context.Context, lookup edgeresolve.NodeLookup, edges []model.Edge, options edgeresolve.ResolveOptions) ([]model.Edge, error)

const (
	buildFlushFileBatchSize   = 100
	buildFlushParsedBytes     = 16 << 20
	buildEdgeResolveChunkSize = 400
	forceReparseEdgeChunkSize = 400
	scopedINQueryChunkSize    = 400
)

// graphFileNodeState is a minimal projection of model.Node used to discover existing graph files cheaply.
// @intent avoid loading full node rows when only id, file path, and content hash are needed.
type graphFileNodeState struct {
	ID       uint
	FilePath string
	Hash     string
}

// Parser defines the minimum parser behavior required by graph builds.
// @intent let CLI, MCP, and tests share the same build policy with their parser implementations
type Parser interface {
	Parse(filePath string, content []byte) ([]model.Node, []model.Edge, error)
	ParseWithContext(ctx context.Context, filePath string, content []byte) ([]model.Node, []model.Edge, error)
}

// commentParserWithLanguage is the optional contract a Parser may satisfy to expose comment blocks plus its source language.
// @intent let the build pipeline collect docstring/comment blocks alongside nodes and edges when the parser supports it.
type commentParserWithLanguage interface {
	Parser
	ParseWithComments(ctx context.Context, filePath string, content []byte) ([]model.Node, []model.Edge, []treesitter.CommentBlock, error)
	Language() string
}

// @intent 메타데이터와 언어 정보까지 돌려주는 parser 확장을 build 경로에서 감지하게 한다.
type metadataParserWithLanguage interface {
	Parser
	ParseWithCommentsAndMetadata(ctx context.Context, filePath string, content []byte) ([]model.Node, []model.Edge, []treesitter.CommentBlock, treesitter.ParseMetadata, error)
	Language() string
}

// IncrementalSyncer defines the sync operation GraphService.Update delegates to.
// @intent keep filesystem/build policy in service while preserving the existing incremental engine
type IncrementalSyncer interface {
	SyncWithExisting(ctx context.Context, files map[string]incremental.FileInfo, existingFiles []string) (*incremental.SyncStats, error)
}

// @intent allow incremental sync implementations to participate in the same transaction-scoped store used by GraphService updates.
type transactionalIncrementalSyncer interface {
	SyncWithExistingStore(ctx context.Context, syncStore incremental.Store, files map[string]incremental.FileInfo, existingFiles []string) (*incremental.SyncStats, error)
}

// @intent expose a graph-store transaction that also hands back the matching gorm DB handle for coupled search rebuilds.
type transactionalDBStore interface {
	WithTxDB(ctx context.Context, fn func(store.GraphStore, *gorm.DB) error) error
}

// GraphService orchestrates graph building and search document refresh.
// @intent 파싱 결과 저장과 검색 인덱스 재구성을 하나의 서비스로 묶는다.
type GraphService struct {
	Store         store.GraphStore
	DB            *gorm.DB
	SearchBackend search.Backend
	Walkers       map[string]*treesitter.Walker
	Parsers       map[string]Parser
	Logger        *slog.Logger
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
	Unresolved edgeresolve.FilterResolvedDiagnostics
}

// UpdateOptions configures one incremental graph sync run.
// @intent reuse GraphService traversal and parse limit policy for CLI and MCP updates
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

// logger returns the configured slog.Logger or the process default when none was supplied.
// @intent keep service code logging-safe even when callers leave Logger nil.
func (s *GraphService) logger() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}

// parserForExt resolves a Parser for the given file extension, preferring an explicit Parsers map over Walkers.
// @intent let tests inject custom parsers while still using the production walker registry by default.
func (s *GraphService) parserForExt(ext string) (Parser, bool) {
	if s.Parsers != nil {
		parser, ok := s.Parsers[ext]
		return parser, ok
	}
	parser, ok := s.Walkers[ext]
	return parser, ok
}
