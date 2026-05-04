package main

import (
	"log/slog"
	"os"
	"sync"

	"github.com/tae2089/code-context-graph/internal/analysis/community"
	"github.com/tae2089/code-context-graph/internal/analysis/coupling"
	"github.com/tae2089/code-context-graph/internal/analysis/coverage"
	"github.com/tae2089/code-context-graph/internal/analysis/deadcode"
	"github.com/tae2089/code-context-graph/internal/analysis/flows"
	"github.com/tae2089/code-context-graph/internal/analysis/impact"
	"github.com/tae2089/code-context-graph/internal/analysis/incremental"
	"github.com/tae2089/code-context-graph/internal/analysis/largefunc"
	"github.com/tae2089/code-context-graph/internal/analysis/query"
	"github.com/tae2089/code-context-graph/internal/cli"
	ccgconfig "github.com/tae2089/code-context-graph/internal/config"
	ccgdb "github.com/tae2089/code-context-graph/internal/db"
	"github.com/tae2089/code-context-graph/internal/db/migration"
	"github.com/tae2089/code-context-graph/internal/mcp"
	"github.com/tae2089/code-context-graph/internal/parse/treesitter"
	ccgserver "github.com/tae2089/code-context-graph/internal/server"
	"github.com/tae2089/code-context-graph/internal/store/gormstore"
	"github.com/tae2089/trace"
)

var (
	_ mcp.ImpactAnalyzer    = (*impact.Analyzer)(nil)
	_ mcp.FlowTracer        = (*flows.Tracer)(nil)
	_ mcp.QueryService      = (*query.Service)(nil)
	_ mcp.LargefuncAnalyzer = (*largefunc.Service)(nil)
	_ mcp.DeadcodeAnalyzer  = (*deadcode.Service)(nil)
	_ mcp.CouplingAnalyzer  = (*coupling.Service)(nil)
	_ mcp.CoverageAnalyzer  = (*coverage.Service)(nil)
	_ mcp.CommunityBuilder  = (*community.Builder)(nil)
	_ mcp.IncrementalSyncer = (*incremental.Syncer)(nil)
)

var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

func main() {
	logger := slog.Default()

	deps := &cli.Deps{
		Logger:  logger,
		Walkers: buildWalkers(logger),
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
		db, err := ccgdb.Open(driver, dsn)
		if err != nil {
			return trace.Wrap(err, "open database")
		}
		if err := migration.EnsureSchemaVersion(db, driver, dsn, ccgconfig.MigrationsDir()); err != nil {
			if sqlDB, dbErr := db.DB(); dbErr == nil {
				sqlDB.Close()
			}
			return err
		}

		st := gormstore.New(db)
		sb := ccgdb.NewSearchBackend(driver)

		parsers := make(map[string]incremental.Parser, len(deps.Walkers))
		for ext, walker := range deps.Walkers {
			parsers[ext] = walker
		}
		syncer := incremental.NewWithRegistry(st, parsers)

		deps.DB = db
		deps.Store = st
		deps.SearchBackend = sb
		deps.Syncer = syncer
		deps.CleanupFunc = func() {
			for _, w := range deps.Walkers {
				w.Close()
			}
			if sqlDB, err := db.DB(); err == nil {
				sqlDB.Close()
			}
		}

		return nil
	}

	deps.MigrateFunc = func(cfg cli.MigrateConfig) error {
		db, err := ccgdb.Open(cfg.DBDriver, cfg.DBDSN)
		if err != nil {
			return trace.Wrap(err, "open database")
		}
		defer func() {
			if sqlDB, err := db.DB(); err == nil {
				sqlDB.Close()
			}
		}()
		return migration.RunMigrations(db, cfg.DBDriver, cfg.MigrationsDir)
	}

	deps.ServeFunc = func(cfg cli.ServeConfig) error {
		return ccgserver.Run(deps, cfg, version, ccgconfig.RagIndexDir(), ccgconfig.RagDescription())
	}

	cmd := cli.NewRootCmd(deps)

	if err := cmd.Execute(); err != nil {
		slog.Error("command failed", trace.SlogError(err))
		runCleanup()
		os.Exit(1)
	}
	runCleanup()
}

func buildWalkers(logger *slog.Logger) map[string]*treesitter.Walker {
	type langEntry struct {
		spec *treesitter.LangSpec
		exts []string
	}

	langs := []langEntry{
		{treesitter.GoSpec, []string{".go"}},
		{treesitter.PythonSpec, []string{".py"}},
		{treesitter.TypeScriptSpec, []string{".ts", ".tsx"}},
		{treesitter.JavaSpec, []string{".java"}},
		{treesitter.RubySpec, []string{".rb"}},
		{treesitter.JavaScriptSpec, []string{".js", ".jsx", ".mjs", ".cjs"}},
		{treesitter.CSpec, []string{".c", ".h"}},
		{treesitter.CppSpec, []string{".cpp", ".cc", ".cxx", ".hpp", ".hh", ".hxx"}},
		{treesitter.RustSpec, []string{".rs"}},
		{treesitter.KotlinSpec, []string{".kt", ".kts"}},
		{treesitter.PHPSpec, []string{".php"}},
		{treesitter.LuaSpec, []string{".lua", ".luau"}},
	}

	walkers := make(map[string]*treesitter.Walker)
	for _, l := range langs {
		w := treesitter.NewWalker(l.spec, treesitter.WithLogger(logger))
		for _, ext := range l.exts {
			walkers[ext] = w
		}
	}
	return walkers
}
