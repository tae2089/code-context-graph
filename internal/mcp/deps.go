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
	flowspkg "github.com/tae2089/code-context-graph/internal/analysis/flows"
	impactpkg "github.com/tae2089/code-context-graph/internal/analysis/impact"
	"github.com/tae2089/code-context-graph/internal/analysis/incremental"
	"github.com/tae2089/code-context-graph/internal/analysis/query"
	"github.com/tae2089/code-context-graph/internal/model"
	"github.com/tae2089/code-context-graph/internal/store"
	storesearch "github.com/tae2089/code-context-graph/internal/store/search"
)

// Parser defines the source parser contract used by MCP graph builds.
// @intent 추상 파서를 주입해 파일 확장자별 파싱 구현을 서버에서 조합한다.
// @see mcp.Deps
type Parser interface {
	Parse(filePath string, content []byte) ([]model.Node, []model.Edge, error)
	ParseWithContext(ctx context.Context, filePath string, content []byte) ([]model.Node, []model.Edge, error)
}

// ImpactAnalyzer defines the blast-radius analysis contract for graph nodes.
// @intent 노드 변경 영향 범위를 계산하는 분석기를 서버 핸들러에 주입한다.
// @see mcp.handlers.getImpactRadius
type ImpactAnalyzer interface {
	ImpactRadius(ctx context.Context, nodeID uint, depth int) ([]model.Node, error)
}

type BoundedImpactAnalyzer interface {
	ImpactRadiusBounded(ctx context.Context, nodeID uint, depth int, opts impactpkg.RadiusOptions) (*impactpkg.RadiusResult, error)
}

// FlowTracer defines the call-flow tracing contract for graph nodes.
// @intent 시작 노드 기준 호출 흐름을 복원하는 분석기를 서버에 연결한다.
// @see mcp.handlers.traceFlow
type FlowTracer interface {
	TraceFlow(ctx context.Context, startNodeID uint) (*model.Flow, error)
}

type BoundedFlowTracer interface {
	TraceFlowBounded(ctx context.Context, startNodeID uint, opts flowspkg.TraceOptions) (*flowspkg.TraceResult, error)
}

// QueryService defines predefined graph query operations exposed over MCP.
// @intent 표준 그래프 질의를 한 서비스 인터페이스로 추상화해 핸들러를 단순화한다.
// @see mcp.handlers.queryGraph
type QueryService interface {
	CallersOf(ctx context.Context, nodeID uint) ([]model.Node, error)
	CalleesOf(ctx context.Context, nodeID uint) ([]model.Node, error)
	ImportsOf(ctx context.Context, nodeID uint) ([]model.Node, error)
	ImportersOf(ctx context.Context, nodeID uint) ([]model.Node, error)
	ChildrenOf(ctx context.Context, nodeID uint) ([]model.Node, error)
	TestsFor(ctx context.Context, nodeID uint) ([]model.Node, error)
	InheritorsOf(ctx context.Context, nodeID uint) ([]model.Node, error)
	FileSummaryOf(ctx context.Context, filePath string) (*query.FileSummary, error)
}

// LargefuncAnalyzer defines the oversized-function detection contract.
// @intent 유지보수 비용이 높은 대형 함수를 탐지하는 분석기를 주입한다.
// @see mcp.handlers.findLargeFunctions
type LargefuncAnalyzer interface {
	Find(ctx context.Context, threshold int) ([]model.Node, error)
}

// DeadcodeAnalyzer defines the unused-code detection contract.
// @intent 참조되지 않는 노드를 탐지해 정리 후보를 찾는 분석기를 주입한다.
// @see mcp.handlers.findDeadCode
type DeadcodeAnalyzer interface {
	Find(ctx context.Context, opts deadcode.Options) ([]model.Node, error)
}

// CouplingAnalyzer defines the inter-community coupling analysis contract.
// @intent 아키텍처 경계 간 결합도를 계산하는 분석기를 서버에 연결한다.
// @see mcp.handlers.getArchitectureOverview
type CouplingAnalyzer interface {
	Analyze(ctx context.Context) ([]coupling.CouplingPair, error)
}

// CoverageAnalyzer defines file and community coverage lookup operations.
// @intent 리스크 요약과 커뮤니티 상세 응답에 테스트 커버리지 정보를 제공한다.
// @see mcp.handlers.getCommunity
// @see mcp.promptHandlers.reviewChanges
type CoverageAnalyzer interface {
	ByFile(ctx context.Context, filePath string) (*coverage.FileCoverage, error)
	ByCommunity(ctx context.Context, communityID uint) (*coverage.CommunityCoverage, error)
}

// CommunityBuilder defines the community rebuild contract.
// @intent 그래프 후처리에서 모듈 커뮤니티를 재계산하는 구현을 주입한다.
// @see mcp.handlers.runPostprocess
type CommunityBuilder interface {
	Rebuild(ctx context.Context, cfg community.Config) ([]community.Stats, error)
}

// IncrementalSyncer defines the incremental graph synchronization contract.
// @intent 전체 재파싱 없이 변경 파일만 그래프에 반영하는 동기화기를 주입한다.
// @see mcp.handlers.buildOrUpdateGraph
type IncrementalSyncer interface {
	Sync(ctx context.Context, files map[string]incremental.FileInfo) (*incremental.SyncStats, error)
	SyncWithExisting(ctx context.Context, files map[string]incremental.FileInfo, existingFiles []string) (*incremental.SyncStats, error)
}

// Deps collects the services and stores required by MCP handlers.
// @intent MCP 서버 구성요소를 한 번에 주입해 도구와 프롬프트 핸들러를 조립한다.
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

	// Phase 11 추가
	QueryService      QueryService
	LargefuncAnalyzer LargefuncAnalyzer
	DeadcodeAnalyzer  DeadcodeAnalyzer
	CouplingAnalyzer  CouplingAnalyzer
	CoverageAnalyzer  CoverageAnalyzer
	CommunityBuilder  CommunityBuilder
	Incremental       IncrementalSyncer

	// Cache — nil이면 캐시 비활성화
	Cache *Cache

	// RagIndexDir — doc-index.json이 저장되는 디렉토리 (기본: ".ccg")
	RagIndexDir string
	// RagProjectDesc — root 노드 summary에 사용되는 프로젝트 설명
	RagProjectDesc string

	NamespaceRoot string
	WorkspaceRoot string
	RepoRoot      string
}
