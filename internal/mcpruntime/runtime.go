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

	"github.com/tae2089/code-context-graph/internal/analysis/changes"
	"github.com/tae2089/code-context-graph/internal/analysis/flows"
	"github.com/tae2089/code-context-graph/internal/analysis/impact"
	"github.com/tae2089/code-context-graph/internal/analysis/query"
	"github.com/tae2089/code-context-graph/internal/core"
	"github.com/tae2089/code-context-graph/internal/mcp"
	ccgobs "github.com/tae2089/code-context-graph/internal/obs"
)

// Options controls shared MCP runtime setup independent of transport.
// @intent pass cache, telemetry, namespace, RAG, and parse-limit settings without importing HTTP server code.
type Options struct {
	CacheTTL            time.Duration
	NoCache             bool
	OTELEndpoint        string
	NamespaceRoot       string
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
	Server *mcpgo.MCPServer
	Cache  *mcp.Cache
	Deps   *mcp.Deps

	logger   *slog.Logger
	shutdown func(context.Context) error
	close    sync.Once
}

// New assembles MCP handlers, cache, and telemetry.
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
		Walkers:             mcpWalkers,
		SearchBackend:       rt.SearchBackend,
		ImpactAnalyzer:      impact.New(rt.Store),
		FlowTracer:          flows.New(rt.Store),
		ChangesGitClient:    changes.NewExecGitClient(),
		QueryService:        query.New(rt.DB),
		FlowBuilder:         flows.NewBuilder(rt.DB, rt.Store),
		Incremental:         rt.Syncer,
		Logger:              rt.Logger,
		Cache:               cache,
		RagIndexDir:         opts.RagIndexDir,
		RagProjectDesc:      opts.RagProjectDesc,
		NamespaceRoot:       opts.NamespaceRoot,
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
