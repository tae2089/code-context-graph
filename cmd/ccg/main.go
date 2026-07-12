// @index Local CLI bootstrap entry point for one-shot commands and stdio MCP.
package main

import (
	"log/slog"
	"os"
	"sync"

	"github.com/tae2089/code-context-graph/internal/adapters/inbound/cli"
	"github.com/tae2089/code-context-graph/internal/app/ingest"
	ccgconfig "github.com/tae2089/code-context-graph/internal/config"
	ccgruntime "github.com/tae2089/code-context-graph/internal/runtime"
	mcpruntime "github.com/tae2089/code-context-graph/internal/runtime/mcp"
	"github.com/tae2089/trace"
)

var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

// @intent assemble local CLI dependencies and guarantee cleanup on command failure.
func main() {
	logger := slog.Default()
	rt := ccgruntime.NewRuntime(logger)
	walkers := make(map[string]ingest.Parser, len(rt.Walkers))
	for ext, walker := range rt.Walkers {
		walkers[ext] = walker
	}

	deps := &cli.Deps{
		Logger:      logger,
		Walkers:     walkers,
		CleanupFunc: rt.Close,
		Version: cli.VersionInfo{
			Version: version,
			Commit:  commit,
			Date:    date,
		},
	}

	var cleanupOnce sync.Once
	runCleanup := func() {
		cleanupOnce.Do(func() {
			if deps.CleanupFunc != nil {
				deps.CleanupFunc()
			}
		})
	}

	deps.InitFunc = func(driver, dsn string) error {
		if err := rt.Init(driver, dsn); err != nil {
			return err
		}
		deps.Store = rt.Store
		deps.UnitOfWork = rt.UnitOfWork
		deps.Search = rt.Search
		deps.SearchReader = rt.SearchReader
		deps.Statistics = rt.Store
		deps.Docs = rt.Store
		deps.Wiki = rt.Store
		deps.Syncer = rt.Syncer
		deps.CleanupFunc = rt.Close
		return nil
	}

	deps.MigrateFunc = func(cfg cli.MigrateConfig) error {
		return rt.Migrate(cfg.DBDriver, cfg.DBDSN, cfg.MigrationsDir)
	}

	deps.ServeFunc = func(cfg cli.ServeConfig) error {
		repoRoot := os.Getenv("CCG_REPO_ROOT")
		if repoRoot == "" {
			// @domainRule local stdio MCP uses the launch working directory as the analysis boundary when no repo root is configured.
			if wd, err := os.Getwd(); err == nil {
				repoRoot = wd
			}
		}
		return mcpruntime.RunStdio(rt.MCPComponents(), mcpruntime.Options{
			CacheTTL:            cfg.CacheTTL,
			NoCache:             cfg.NoCache,
			OTELEndpoint:        cfg.OTELEndpoint,
			NamespaceRoot:       cfg.NamespaceRoot,
			RepoRoot:            repoRoot,
			MaxFileBytes:        cfg.MaxFileBytes,
			MaxTotalParsedBytes: cfg.MaxTotalParsedBytes,
			ServiceVersion:      version,
			RagIndexDir:         ccgconfig.RagIndexDir(),
			RagProjectDesc:      ccgconfig.RagDescription(),
		})
	}

	cmd := cli.NewRootCmd(deps)

	if err := cmd.Execute(); err != nil {
		slog.Error("command failed", trace.SlogError(err))
		runCleanup()
		os.Exit(1)
	}
	runCleanup()
}
