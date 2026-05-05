// @index Local CLI bootstrap entry point for one-shot commands and stdio MCP.
package main

import (
	"log/slog"
	"os"
	"sync"

	"github.com/tae2089/code-context-graph/internal/cli"
	ccgconfig "github.com/tae2089/code-context-graph/internal/config"
	"github.com/tae2089/code-context-graph/internal/core"
	"github.com/tae2089/code-context-graph/internal/mcpruntime"
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
	rt := core.NewRuntime(logger)

	deps := &cli.Deps{
		Logger:      logger,
		Walkers:     rt.Walkers,
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
		deps.DB = rt.DB
		deps.Store = rt.Store
		deps.SearchBackend = rt.SearchBackend
		deps.Syncer = rt.Syncer
		deps.CleanupFunc = rt.Close
		return nil
	}

	deps.MigrateFunc = func(cfg cli.MigrateConfig) error {
		return rt.Migrate(cfg.DBDriver, cfg.DBDSN, cfg.MigrationsDir)
	}

	deps.ServeFunc = func(cfg cli.ServeConfig) error {
		return mcpruntime.RunStdio(rt, mcpruntime.Options{
			CacheTTL:            cfg.CacheTTL,
			NoCache:             cfg.NoCache,
			OTELEndpoint:        cfg.OTELEndpoint,
			NamespaceRoot:       cfg.NamespaceRoot,
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
