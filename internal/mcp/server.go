// @index MCP 서버. 18개 도구와 5개 프롬프트 템플릿을 통해 코드 분석 기능을 AI에게 노출한다.
package mcp

import (
	"context"
	"log/slog"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"gorm.io/gorm"

	"github.com/imtaebin/code-context-graph/internal/analysis/changes"
	"github.com/imtaebin/code-context-graph/internal/analysis/community"
	"github.com/imtaebin/code-context-graph/internal/analysis/coupling"
	"github.com/imtaebin/code-context-graph/internal/analysis/coverage"
	"github.com/imtaebin/code-context-graph/internal/analysis/deadcode"
	"github.com/imtaebin/code-context-graph/internal/analysis/incremental"
	"github.com/imtaebin/code-context-graph/internal/analysis/query"
	"github.com/imtaebin/code-context-graph/internal/model"
	"github.com/imtaebin/code-context-graph/internal/store"
	storesearch "github.com/imtaebin/code-context-graph/internal/store/search"
)

type Parser interface {
	Parse(filePath string, content []byte) ([]model.Node, []model.Edge, error)
}

type ImpactAnalyzer interface {
	ImpactRadius(ctx context.Context, nodeID uint, depth int) ([]model.Node, error)
}

type FlowTracer interface {
	TraceFlow(ctx context.Context, startNodeID uint) (*model.Flow, error)
}

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

type LargefuncAnalyzer interface {
	Find(ctx context.Context, threshold int) ([]model.Node, error)
}

type DeadcodeAnalyzer interface {
	Find(ctx context.Context, opts deadcode.Options) ([]model.Node, error)
}

type CouplingAnalyzer interface {
	Analyze(ctx context.Context) ([]coupling.CouplingPair, error)
}

type CoverageAnalyzer interface {
	ByFile(ctx context.Context, filePath string) (*coverage.FileCoverage, error)
	ByCommunity(ctx context.Context, communityID uint) (*coverage.CommunityCoverage, error)
}

type CommunityBuilder interface {
	Rebuild(ctx context.Context, cfg community.Config) ([]community.Stats, error)
}

type IncrementalSyncer interface {
	Sync(ctx context.Context, files map[string]incremental.FileInfo) (*incremental.SyncStats, error)
}

type Deps struct {
	Store            store.GraphStore
	DB               *gorm.DB
	Parser           Parser
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
}

func NewServer(deps *Deps) *server.MCPServer {
	log := deps.Logger
	if log == nil {
		log = slog.Default()
	}

	srv := server.NewMCPServer(
		"code-context-graph",
		"1.0.0",
		server.WithToolCapabilities(true),
		server.WithPromptCapabilities(true),
	)

	h := &handlers{deps: deps, cache: deps.Cache}

	srv.AddTools(
		server.ServerTool{
			Tool: mcp.NewTool("parse_project",
				mcp.WithDescription("Parse source files and store nodes/edges in the graph database"),
				mcp.WithString("path", mcp.Description("Project directory path to parse"), mcp.Required()),
			),
			Handler: h.parseProject,
		},
		server.ServerTool{
			Tool: mcp.NewTool("get_node",
				mcp.WithDescription("Get a node by its qualified name"),
				mcp.WithString("qualified_name", mcp.Description("Fully qualified node name"), mcp.Required()),
			),
			Handler: h.getNode,
		},
		server.ServerTool{
			Tool: mcp.NewTool("get_impact_radius",
				mcp.WithDescription("Get blast-radius analysis for a node via BFS traversal"),
				mcp.WithString("qualified_name", mcp.Description("Fully qualified node name"), mcp.Required()),
				mcp.WithNumber("depth", mcp.Description("BFS traversal depth"), mcp.DefaultNumber(1)),
			),
			Handler: h.getImpactRadius,
		},
		server.ServerTool{
			Tool: mcp.NewTool("search",
				mcp.WithDescription("Full-text search across code nodes. Use 'path' to scope results to a module for token-efficient queries."),
				mcp.WithString("query", mcp.Description("Search query string"), mcp.Required()),
				mcp.WithNumber("limit", mcp.Description("Maximum number of results"), mcp.DefaultNumber(10)),
				mcp.WithString("path", mcp.Description("Filter results to file paths starting with this prefix (e.g. internal/auth)")),
			),
			Handler: h.search,
		},
		server.ServerTool{
			Tool: mcp.NewTool("get_annotation",
				mcp.WithDescription("Get annotation and doc tags for a node"),
				mcp.WithString("qualified_name", mcp.Description("Fully qualified node name"), mcp.Required()),
			),
			Handler: h.getAnnotation,
		},
		server.ServerTool{
			Tool: mcp.NewTool("trace_flow",
				mcp.WithDescription("Trace call-chain flow starting from a node"),
				mcp.WithString("qualified_name", mcp.Description("Fully qualified node name"), mcp.Required()),
			),
			Handler: h.traceFlow,
		},
		server.ServerTool{
			Tool: mcp.NewTool("build_or_update_graph",
				mcp.WithDescription("Build or incrementally update the code graph with recursive directory traversal and optional postprocessing"),
				mcp.WithString("path", mcp.Description("Project directory path to parse"), mcp.Required()),
				mcp.WithBoolean("full_rebuild", mcp.Description("If true, do a full rebuild; if false, use incremental sync")),
				mcp.WithString("postprocess", mcp.Description("Postprocessing mode: full, minimal, or none (default: full)")),
			),
			Handler: h.buildOrUpdateGraph,
		},
		server.ServerTool{
			Tool: mcp.NewTool("run_postprocess",
				mcp.WithDescription("Run postprocessing steps independently: flows, communities, and/or full-text search indexing"),
				mcp.WithBoolean("flows", mcp.Description("Rebuild flow traces (default: true)")),
				mcp.WithBoolean("communities", mcp.Description("Rebuild community detection (default: true)")),
				mcp.WithBoolean("fts", mcp.Description("Rebuild full-text search index (default: true)")),
				mcp.WithNumber("community_depth", mcp.Description("Directory depth for community detection (default: 2)")),
			),
			Handler: h.runPostprocess,
		},
		server.ServerTool{
			Tool: mcp.NewTool("query_graph",
				mcp.WithDescription("Run predefined graph queries: callers_of, callees_of, imports_of, importers_of, children_of, tests_for, inheritors_of, file_summary"),
				mcp.WithString("pattern", mcp.Description("Query pattern"), mcp.Required()),
				mcp.WithString("target", mcp.Description("Target qualified name or file path"), mcp.Required()),
			),
			Handler: h.queryGraph,
		},
		server.ServerTool{
			Tool: mcp.NewTool("list_graph_stats",
				mcp.WithDescription("Get graph statistics: total nodes, edges, and breakdowns by kind and language"),
			),
			Handler: h.listGraphStats,
		},
		server.ServerTool{
			Tool: mcp.NewTool("find_large_functions",
				mcp.WithDescription("Find functions exceeding a line count threshold"),
				mcp.WithNumber("min_lines", mcp.Description("Minimum line count threshold (default: 50)")),
				mcp.WithNumber("limit", mcp.Description("Maximum number of results (default: 50)")),
				mcp.WithString("path", mcp.Description("Filter results to file paths starting with this prefix")),
			),
			Handler: h.findLargeFunctions,
		},
		server.ServerTool{
			Tool: mcp.NewTool("detect_changes",
				mcp.WithDescription("Detect changed functions with risk scores based on git diff"),
				mcp.WithString("repo_root", mcp.Description("Git repository root path"), mcp.Required()),
				mcp.WithString("base", mcp.Description("Base commit reference (default: HEAD~1)")),
			),
			Handler: h.detectChanges,
		},
		server.ServerTool{
			Tool: mcp.NewTool("get_affected_flows",
				mcp.WithDescription("Get flows affected by recent code changes"),
				mcp.WithString("repo_root", mcp.Description("Git repository root path"), mcp.Required()),
				mcp.WithString("base", mcp.Description("Base commit reference (default: HEAD~1)")),
			),
			Handler: h.getAffectedFlows,
		},
		server.ServerTool{
			Tool: mcp.NewTool("list_flows",
				mcp.WithDescription("List all stored flows with member counts"),
				mcp.WithString("sort_by", mcp.Description("Sort order: name or node_count (default: name)")),
				mcp.WithNumber("limit", mcp.Description("Maximum number of results (default: 50)")),
			),
			Handler: h.listFlows,
		},
		server.ServerTool{
			Tool: mcp.NewTool("list_communities",
				mcp.WithDescription("List communities with node counts and optional filtering"),
				mcp.WithString("sort_by", mcp.Description("Sort order: size, name, or cohesion (default: size)")),
				mcp.WithNumber("min_size", mcp.Description("Minimum node count filter (default: 0)")),
			),
			Handler: h.listCommunities,
		},
		server.ServerTool{
			Tool: mcp.NewTool("get_community",
				mcp.WithDescription("Get community details with optional member listing and coverage"),
				mcp.WithNumber("community_id", mcp.Description("Community ID"), mcp.Required()),
				mcp.WithBoolean("include_members", mcp.Description("Include member nodes in response (default: false)")),
			),
			Handler: h.getCommunity,
		},
		server.ServerTool{
			Tool: mcp.NewTool("get_architecture_overview",
				mcp.WithDescription("Get architecture overview: communities, coupling analysis, and warnings"),
			),
			Handler: h.getArchitectureOverview,
		},
		server.ServerTool{
			Tool: mcp.NewTool("find_dead_code",
				mcp.WithDescription("Find unused code with no incoming edges"),
				mcp.WithString("path", mcp.Description("Filter results to file paths starting with this prefix")),
			),
			Handler: h.findDeadCode,
		},
	)

	log.Info("MCP server created", "name", "code-context-graph", "version", "1.0.0", "tools", 18, "prompts", 5)

	p := &promptHandlers{deps: deps}
	srv.AddPrompts(
		server.ServerPrompt{
			Prompt: mcp.NewPrompt("review_changes",
				mcp.WithPromptDescription("변경사항 리뷰: 리스크 분석 및 테스트 커버리지 갭 확인"),
				mcp.WithArgument("repo_root", mcp.ArgumentDescription("Git 저장소 루트 경로"), mcp.RequiredArgument()),
				mcp.WithArgument("base", mcp.ArgumentDescription("비교 기준 커밋 (기본: HEAD~1)")),
			),
			Handler: p.reviewChanges,
		},
		server.ServerPrompt{
			Prompt: mcp.NewPrompt("architecture_map",
				mcp.WithPromptDescription("아키텍처 맵: 커뮤니티 구조 및 모듈 간 결합도 분석"),
			),
			Handler: p.architectureMap,
		},
		server.ServerPrompt{
			Prompt: mcp.NewPrompt("debug_issue",
				mcp.WithPromptDescription("이슈 디버깅: 관련 코드 검색 및 호출 그래프 분석"),
				mcp.WithArgument("description", mcp.ArgumentDescription("이슈 설명"), mcp.RequiredArgument()),
			),
			Handler: p.debugIssue,
		},
		server.ServerPrompt{
			Prompt: mcp.NewPrompt("onboard_developer",
				mcp.WithPromptDescription("온보딩: 프로젝트 통계, 커뮤니티 구조, 대형 함수 요약"),
			),
			Handler: p.onboardDeveloper,
		},
		server.ServerPrompt{
			Prompt: mcp.NewPrompt("pre_merge_check",
				mcp.WithPromptDescription("머지 전 체크: 리스크, 커버리지, 미사용 코드, 대형 함수 확인"),
				mcp.WithArgument("repo_root", mcp.ArgumentDescription("Git 저장소 루트 경로"), mcp.RequiredArgument()),
				mcp.WithArgument("base", mcp.ArgumentDescription("비교 기준 커밋 (기본: HEAD~1)")),
			),
			Handler: p.preMergeCheck,
		},
	)

	return srv
}
