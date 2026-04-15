// @index MCP 서버. 다수의 도구와 5개 프롬프트 템플릿을 통해 코드 분석 기능을 AI에게 노출한다.
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

// FlowTracer defines the call-flow tracing contract for graph nodes.
// @intent 시작 노드 기준 호출 흐름을 복원하는 분석기를 서버에 연결한다.
// @see mcp.handlers.traceFlow
type FlowTracer interface {
	TraceFlow(ctx context.Context, startNodeID uint) (*model.Flow, error)
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

	WorkspaceRoot string
}

// NewServer creates and configures the MCP server with all tools and prompts.
// @intent 코드 그래프 기능을 MCP 도구와 프롬프트로 노출하는 서버 인스턴스를 구성한다.
// @requires deps != nil
// @ensures 반환 서버에는 MCP 도구와 프롬프트가 등록된다.
// @sideEffect 서버 메타데이터를 로거에 기록한다.
// @see mcp.Deps
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
				mcp.WithString("workspace", mcp.Description("Workspace name for namespace isolation")),
			),
			Handler: h.parseProject,
		},
		server.ServerTool{
			Tool: mcp.NewTool("get_node",
				mcp.WithDescription("Get a node by its qualified name"),
				mcp.WithString("qualified_name", mcp.Description("Fully qualified node name"), mcp.Required()),
				mcp.WithString("workspace", mcp.Description("Workspace name for namespace isolation")),
			),
			Handler: h.getNode,
		},
		server.ServerTool{
			Tool: mcp.NewTool("get_impact_radius",
				mcp.WithDescription("Get blast-radius analysis for a node via BFS traversal"),
				mcp.WithString("qualified_name", mcp.Description("Fully qualified node name"), mcp.Required()),
				mcp.WithNumber("depth", mcp.Description("BFS traversal depth"), mcp.DefaultNumber(1)),
				mcp.WithString("workspace", mcp.Description("Workspace name for namespace isolation")),
			),
			Handler: h.getImpactRadius,
		},
		server.ServerTool{
			Tool: mcp.NewTool("search",
				mcp.WithDescription("Full-text search across code nodes. Use 'path' to scope results to a module for token-efficient queries."),
				mcp.WithString("query", mcp.Description("Search query string"), mcp.Required()),
				mcp.WithNumber("limit", mcp.Description("Maximum number of results"), mcp.DefaultNumber(10)),
				mcp.WithString("path", mcp.Description("Filter results to file paths starting with this prefix (e.g. internal/auth)")),
				mcp.WithString("workspace", mcp.Description("Workspace name for namespace isolation")),
			),
			Handler: h.search,
		},
		server.ServerTool{
			Tool: mcp.NewTool("get_annotation",
				mcp.WithDescription("Get annotation and doc tags for a node"),
				mcp.WithString("qualified_name", mcp.Description("Fully qualified node name"), mcp.Required()),
				mcp.WithString("workspace", mcp.Description("Workspace name for namespace isolation")),
			),
			Handler: h.getAnnotation,
		},
		server.ServerTool{
			Tool: mcp.NewTool("trace_flow",
				mcp.WithDescription("Trace call-chain flow starting from a node"),
				mcp.WithString("qualified_name", mcp.Description("Fully qualified node name"), mcp.Required()),
				mcp.WithString("workspace", mcp.Description("Workspace name for namespace isolation")),
			),
			Handler: h.traceFlow,
		},
		server.ServerTool{
			Tool: mcp.NewTool("build_or_update_graph",
				mcp.WithDescription("Build or incrementally update the code graph with recursive directory traversal and optional postprocessing"),
				mcp.WithString("path", mcp.Description("Project directory path to parse"), mcp.Required()),
				mcp.WithBoolean("full_rebuild", mcp.Description("If true, do a full rebuild; if false, use incremental sync")),
				mcp.WithString("postprocess", mcp.Description("Postprocessing mode: full, minimal, or none (default: full)")),
				mcp.WithString("workspace", mcp.Description("Workspace name for namespace isolation")),
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
				mcp.WithString("workspace", mcp.Description("Workspace name for namespace isolation")),
			),
			Handler: h.runPostprocess,
		},
		server.ServerTool{
			Tool: mcp.NewTool("query_graph",
				mcp.WithDescription("Run predefined graph queries: callers_of, callees_of, imports_of, importers_of, children_of, tests_for, inheritors_of, file_summary"),
				mcp.WithString("pattern", mcp.Description("Query pattern"), mcp.Required()),
				mcp.WithString("target", mcp.Description("Target qualified name or file path"), mcp.Required()),
				mcp.WithString("workspace", mcp.Description("Workspace name for namespace isolation")),
			),
			Handler: h.queryGraph,
		},
		server.ServerTool{
			Tool: mcp.NewTool("list_graph_stats",
				mcp.WithDescription("Get graph statistics: total nodes, edges, and breakdowns by kind and language"),
				mcp.WithString("workspace", mcp.Description("Workspace name for namespace isolation")),
			),
			Handler: h.listGraphStats,
		},
		server.ServerTool{
			Tool: mcp.NewTool("find_large_functions",
				mcp.WithDescription("Find functions exceeding a line count threshold"),
				mcp.WithNumber("min_lines", mcp.Description("Minimum line count threshold (default: 50)")),
				mcp.WithNumber("limit", mcp.Description("Maximum number of results (default: 50)")),
				mcp.WithString("path", mcp.Description("Filter results to file paths starting with this prefix")),
				mcp.WithString("workspace", mcp.Description("Workspace name for namespace isolation")),
			),
			Handler: h.findLargeFunctions,
		},
		server.ServerTool{
			Tool: mcp.NewTool("detect_changes",
				mcp.WithDescription("Detect changed functions with risk scores based on git diff"),
				mcp.WithString("repo_root", mcp.Description("Git repository root path"), mcp.Required()),
				mcp.WithString("base", mcp.Description("Base commit reference (default: HEAD~1)")),
				mcp.WithString("workspace", mcp.Description("Workspace name for namespace isolation")),
			),
			Handler: h.detectChanges,
		},
		server.ServerTool{
			Tool: mcp.NewTool("get_affected_flows",
				mcp.WithDescription("Get flows affected by recent code changes"),
				mcp.WithString("repo_root", mcp.Description("Git repository root path"), mcp.Required()),
				mcp.WithString("base", mcp.Description("Base commit reference (default: HEAD~1)")),
				mcp.WithString("workspace", mcp.Description("Workspace name for namespace isolation")),
			),
			Handler: h.getAffectedFlows,
		},
		server.ServerTool{
			Tool: mcp.NewTool("list_flows",
				mcp.WithDescription("List all stored flows with member counts"),
				mcp.WithString("sort_by", mcp.Description("Sort order: name or node_count (default: name)")),
				mcp.WithNumber("limit", mcp.Description("Maximum number of results (default: 50)")),
				mcp.WithString("workspace", mcp.Description("Workspace name for namespace isolation")),
			),
			Handler: h.listFlows,
		},
		server.ServerTool{
			Tool: mcp.NewTool("list_communities",
				mcp.WithDescription("List communities with node counts and optional filtering"),
				mcp.WithString("sort_by", mcp.Description("Sort order: size, name, or cohesion (default: size)")),
				mcp.WithNumber("min_size", mcp.Description("Minimum node count filter (default: 0)")),
				mcp.WithString("workspace", mcp.Description("Workspace name for namespace isolation")),
			),
			Handler: h.listCommunities,
		},
		server.ServerTool{
			Tool: mcp.NewTool("get_community",
				mcp.WithDescription("Get community details with optional member listing and coverage"),
				mcp.WithNumber("community_id", mcp.Description("Community ID"), mcp.Required()),
				mcp.WithBoolean("include_members", mcp.Description("Include member nodes in response (default: false)")),
				mcp.WithString("workspace", mcp.Description("Workspace name for namespace isolation")),
			),
			Handler: h.getCommunity,
		},
		server.ServerTool{
			Tool: mcp.NewTool("get_architecture_overview",
				mcp.WithDescription("Get architecture overview: communities, coupling analysis, and warnings"),
				mcp.WithString("workspace", mcp.Description("Workspace name for namespace isolation")),
			),
			Handler: h.getArchitectureOverview,
		},
		server.ServerTool{
			Tool: mcp.NewTool("find_dead_code",
				mcp.WithDescription("Find unused code with no incoming edges"),
				mcp.WithString("path", mcp.Description("Filter results to file paths starting with this prefix")),
				mcp.WithString("workspace", mcp.Description("Workspace name for namespace isolation")),
			),
			Handler: h.findDeadCode,
		},
		server.ServerTool{
			Tool: mcp.NewTool("build_rag_index",
				mcp.WithDescription("Build Vectorless RAG index from docs/ and community structure. Stores result in .ccg/doc-index.json. When workspace is specified, reads docs from {workspace_root}/{workspace}/ instead of local docs/."),
				mcp.WithString("out_dir", mcp.Description("Documentation directory root (default: from config or 'docs')")),
				mcp.WithString("index_dir", mcp.Description("Directory to write doc-index.json (default: '.ccg')")),
				mcp.WithString("workspace", mcp.Description("Workspace name. When set, reads docs from the workspace directory instead of local docs/.")),
			),
			Handler: h.buildRagIndex,
		},
		server.ServerTool{
			Tool: mcp.NewTool("get_rag_tree",
				mcp.WithDescription("Get the RAG document tree for navigation. Call without arguments first to see all communities, then pass community_id to drill into a specific one."),
				mcp.WithString("community_id", mcp.Description("Community node ID as shown in the tree (e.g. 'community:auth'). Omit to get the full tree.")),
				mcp.WithNumber("depth", mcp.Description("Maximum tree depth to return (1=communities only, 2=communities+files). Default: 0 (unlimited).")),
				mcp.WithString("workspace", mcp.Description("Workspace name. When set, reads doc-index.json from the workspace-specific index directory.")),
			),
			Handler: h.getRagTree,
		},
		server.ServerTool{
			Tool: mcp.NewTool("get_doc_content",
				mcp.WithDescription("Get the content of a documentation file by its path. When workspace is specified, reads from {workspace_root}/{workspace}/{file_path}."),
				mcp.WithString("file_path", mcp.Description("Path to the doc file (e.g. 'docs/internal/mcp/handlers.go.md')"), mcp.Required()),
				mcp.WithString("workspace", mcp.Description("Workspace name. When set, reads from the workspace directory.")),
			),
			Handler: h.getDocContent,
		},
		server.ServerTool{
			Tool: mcp.NewTool("search_docs",
				mcp.WithDescription("Search the RAG document tree by keyword. Matches against node labels and summaries. Returns breadcrumb paths to matching nodes."),
				mcp.WithString("query", mcp.Description("Search keyword (case-insensitive)"), mcp.Required()),
				mcp.WithNumber("limit", mcp.Description("Maximum number of results (default: 10)")),
				mcp.WithString("workspace", mcp.Description("Workspace name. When set, searches the workspace-specific doc-index.json.")),
			),
			Handler: h.searchDocs,
		},
		server.ServerTool{
			Tool: mcp.NewTool("upload_file",
				mcp.WithDescription("Upload a file to a workspace. Content must be base64-encoded. Creates {workspace}/{file_path} on the server."),
				mcp.WithString("workspace", mcp.Description("Workspace name (e.g. service name)"), mcp.Required()),
				mcp.WithString("file_path", mcp.Description("Relative file path within workspace (e.g. docs/readme.md)"), mcp.Required()),
				mcp.WithString("content", mcp.Description("Base64-encoded file content"), mcp.Required()),
			),
			Handler: h.uploadFile,
		},
		server.ServerTool{
			Tool: mcp.NewTool("list_workspaces",
				mcp.WithDescription("List all available workspaces"),
			),
			Handler: h.listWorkspaces,
		},
		server.ServerTool{
			Tool: mcp.NewTool("list_files",
				mcp.WithDescription("List all files in a workspace"),
				mcp.WithString("workspace", mcp.Description("Workspace name"), mcp.Required()),
			),
			Handler: h.listFiles,
		},
		server.ServerTool{
			Tool: mcp.NewTool("delete_file",
				mcp.WithDescription("Delete a file from a workspace"),
				mcp.WithString("workspace", mcp.Description("Workspace name"), mcp.Required()),
				mcp.WithString("file_path", mcp.Description("Relative file path within workspace"), mcp.Required()),
			),
			Handler: h.deleteFile,
		},
		server.ServerTool{
			Tool: mcp.NewTool("upload_files",
				mcp.WithDescription("Upload multiple files to workspaces in a single call. The 'files' parameter is a JSON array of objects with workspace, file_path, and content (base64-encoded) fields."),
				mcp.WithString("files", mcp.Description("JSON array of file entries: [{\"workspace\":\"...\",\"file_path\":\"...\",\"content\":\"base64...\"}]"), mcp.Required()),
			),
			Handler: h.uploadFiles,
		},
		server.ServerTool{
			Tool: mcp.NewTool("delete_workspace",
				mcp.WithDescription("Delete an entire workspace and all its files"),
				mcp.WithString("workspace", mcp.Description("Workspace name to delete"), mcp.Required()),
			),
			Handler: h.deleteWorkspace,
		},
	)

	log.Info("MCP server created", "name", "code-context-graph", "version", "1.0.0", "prompts", 5)

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
