// @index Shared MCP runtime assembly for stdio and HTTP server entry points.
package mcpruntime

import (
	"context"
	"log/slog"
	"os/signal"
	"sync"
	"syscall"
	"time"

	mcpgo "github.com/mark3labs/mcp-go/server"
	"github.com/tae2089/trace"
	"gorm.io/gorm"

	"github.com/tae2089/code-context-graph/internal/analysis/changes"
	"github.com/tae2089/code-context-graph/internal/analysis/community"
	"github.com/tae2089/code-context-graph/internal/analysis/coupling"
	"github.com/tae2089/code-context-graph/internal/analysis/coverage"
	"github.com/tae2089/code-context-graph/internal/analysis/deadcode"
	"github.com/tae2089/code-context-graph/internal/analysis/flows"
	"github.com/tae2089/code-context-graph/internal/analysis/impact"
	"github.com/tae2089/code-context-graph/internal/analysis/largefunc"
	"github.com/tae2089/code-context-graph/internal/analysis/query"
	"github.com/tae2089/code-context-graph/internal/core"
	"github.com/tae2089/code-context-graph/internal/mcp"
	ccgobs "github.com/tae2089/code-context-graph/internal/obs"
	postprocesspolicy "github.com/tae2089/code-context-graph/internal/postprocess/policy"
)

// Options controls shared MCP runtime setup independent of transport.
// @intent pass cache, telemetry, namespace, RAG, and parse-limit settings without importing HTTP server code.
type Options struct {
	CacheTTL            time.Duration
	NoCache             bool
	OTELEndpoint        string
	NamespaceRoot       string
	WorkspaceRoot       string
	RepoRoot            string
	MaxFileBytes        int64
	MaxTotalParsedBytes int64
	ServiceVersion      string
	RagIndexDir         string
	RagProjectDesc      string
}

// Instance is a fully assembled MCP server plus runtime resources.
// @intent share MCP server construction while keeping stdio and HTTP transports in separate packages.
type Instance struct {
	Server             *mcpgo.MCPServer
	Cache              *mcp.Cache
	Deps               *mcp.Deps
	PostprocessSummary func(context.Context) (*postprocesspolicy.StatusSummary, error)

	logger   *slog.Logger
	shutdown func(context.Context) error
	close    sync.Once
}

// New assembles MCP handlers, cache, telemetry, and postprocess policy.
// @intent centralize common MCP dependency wiring without linking webhook/HTTP code into the local CLI binary.
// @sideEffect initializes telemetry and optional in-memory cache.
func New(rt *core.Runtime, opts Options) (*Instance, error) {
	rt.Logger.Info("starting code-context-graph MCP runtime")
	tel, err := ccgobs.Setup(context.Background(), ccgobs.Config{
		ServiceName:    "code-context-graph",
		ServiceVersion: opts.ServiceVersion,
		Mode:           "serve",
		Endpoint:       opts.OTELEndpoint,
		Logger:         rt.Logger,
	})
	if err != nil {
		return nil, trace.Wrap(err, "setup telemetry")
	}
	ccgobs.SetGlobal(tel)

	var cache *mcp.Cache
	if !opts.NoCache && opts.CacheTTL > 0 {
		cache = mcp.NewCache(opts.CacheTTL)
		rt.Logger.Info("MCP cache enabled", "ttl", opts.CacheTTL)
	}

	mcpWalkers := make(map[string]mcp.Parser, len(rt.Walkers))
	for ext, w := range rt.Walkers {
		mcpWalkers[ext] = w
	}

	mcpDeps := &mcp.Deps{
		Store:               rt.Store,
		DB:                  rt.DB,
		Parser:              rt.Walkers[".go"],
		Walkers:             mcpWalkers,
		SearchBackend:       rt.SearchBackend,
		ImpactAnalyzer:      impact.New(rt.Store),
		FlowTracer:          flows.New(rt.Store),
		ChangesGitClient:    changes.NewExecGitClient(),
		QueryService:        query.New(rt.DB),
		LargefuncAnalyzer:   largefunc.New(rt.DB),
		DeadcodeAnalyzer:    deadcode.New(rt.DB),
		CouplingAnalyzer:    coupling.New(rt.DB),
		CoverageAnalyzer:    coverage.New(rt.DB),
		CommunityBuilder:    community.New(rt.DB),
		FlowBuilder:         flows.NewBuilder(rt.DB, rt.Store),
		Incremental:         rt.Syncer,
		PostprocessPolicy:   NewPostprocessPolicy(rt.DB),
		Logger:              rt.Logger,
		Cache:               cache,
		RagIndexDir:         opts.RagIndexDir,
		RagProjectDesc:      opts.RagProjectDesc,
		NamespaceRoot:       opts.NamespaceRoot,
		WorkspaceRoot:       opts.WorkspaceRoot,
		RepoRoot:            opts.RepoRoot,
		MaxFileBytes:        opts.MaxFileBytes,
		MaxTotalParsedBytes: opts.MaxTotalParsedBytes,
	}

	inst := &Instance{
		Server:   mcp.NewServer(mcpDeps),
		Cache:    cache,
		Deps:     mcpDeps,
		logger:   rt.Logger,
		shutdown: tel.Shutdown,
	}
	inst.PostprocessSummary = func(ctx context.Context) (*postprocesspolicy.StatusSummary, error) {
		if mcpDeps.PostprocessPolicy == nil {
			return nil, nil
		}
		return mcpDeps.PostprocessPolicy.Status(ctx, postprocesspolicy.StatusOptions{RecentLimit: postprocesspolicy.DefaultStatusLimit})
	}
	return inst, nil
}

// Close releases MCP cache and telemetry resources.
// @intent provide one idempotent cleanup path for transport-specific runners.
// @sideEffect closes the cache and shuts down telemetry with a bounded context.
func (i *Instance) Close() {
	i.close.Do(func() {
		if i.Cache != nil {
			i.Cache.Close()
		}
		if i.shutdown != nil {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := i.shutdown(shutdownCtx); err != nil {
				i.logger.Error("telemetry shutdown failed", "error", err)
			}
		}
		ccgobs.SetGlobal(nil)
	})
}

// RunStdio assembles and serves MCP over stdio.
// @intent keep the local ccg binary on a stdio-only runtime without importing HTTP/webhook server code.
// @sideEffect starts a long-running stdio MCP process and handles SIGINT/SIGTERM shutdown.
func RunStdio(rt *core.Runtime, opts Options) error {
	inst, err := New(rt, opts)
	if err != nil {
		return err
	}
	defer inst.Close()

	rt.Logger.Info("serving MCP over stdio")
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	errCh := make(chan error, 1)
	go func() {
		errCh <- mcpgo.ServeStdio(inst.Server)
	}()
	select {
	case err := <-errCh:
		if err != nil {
			return trace.Wrap(err, "MCP server")
		}
	case <-ctx.Done():
		rt.Logger.Info("received signal, shutting down stdio MCP server")
	}
	return nil
}

// FlushQueryCache clears the MCP query cache if it exists.
// @intent let graph updates invalidate shared MCP cache without coupling to transport packages.
// @sideEffect cache가 있으면 저장된 질의 결과를 비운다.
func FlushQueryCache(cache *mcp.Cache) {
	if cache != nil {
		cache.Flush()
	}
}

// MCPPostprocessPolicy manages post-processing policies for the MCP runtime.
// @intent MCP 실행 경로가 후처리 정책 결정을 공통 래퍼로 호출하게 한다.
type MCPPostprocessPolicy struct {
	engine *postprocesspolicy.Engine
	store  *postprocesspolicy.Store
}

// NewPostprocessPolicy creates a new MCP post-processing policy wrapper.
// @intent MCP 실행 경로에서 후처리 정책 엔진과 저장소를 함께 묶어 제공한다.
func NewPostprocessPolicy(db *gorm.DB) *MCPPostprocessPolicy {
	if db == nil {
		return nil
	}
	return &MCPPostprocessPolicy{
		engine: &postprocesspolicy.Engine{},
		store:  postprocesspolicy.NewStore(db),
	}
}

// Resolve decides the policy for a given tool and input.
// @intent 요청된 후처리 도구에 적용할 정책과 출처를 계산한다.
func (p *MCPPostprocessPolicy) Resolve(ctx context.Context, input postprocesspolicy.DecisionInput) (string, string, error) {
	return p.engine.Resolve(ctx, p.store, input)
}

// RecordRun logs the results of a post-processing run.
// @intent 후처리 실행 결과를 정책 저장소에 기록해 후속 판단에 반영한다.
// @sideEffect ccg_postprocess_run_logs 상태를 갱신한다.
func (p *MCPPostprocessPolicy) RecordRun(ctx context.Context, record postprocesspolicy.RunRecord) error {
	return p.store.RecordRun(ctx, record)
}

// Status returns the current status summary of post-processing.
// @intent 운영 상태 엔드포인트가 후처리 건강 상태를 요약해서 볼 수 있게 한다.
func (p *MCPPostprocessPolicy) Status(ctx context.Context, opts postprocesspolicy.StatusOptions) (*postprocesspolicy.StatusSummary, error) {
	return p.store.Status(ctx, opts)
}

// Reset clears the state of a specific post-processing tool.
// @intent 실패 누적 상태를 초기화해 특정 후처리 도구를 다시 정상 정책으로 돌린다.
// @sideEffect 해당 도구의 정책 상태를 재설정한다.
func (p *MCPPostprocessPolicy) Reset(ctx context.Context, tool string) error {
	return p.store.Reset(ctx, tool)
}
