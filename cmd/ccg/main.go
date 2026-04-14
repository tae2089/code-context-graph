package main

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/mark3labs/mcp-go/server"
	"github.com/spf13/viper"

	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/imtaebin/code-context-graph/internal/analysis/changes"
	"github.com/imtaebin/code-context-graph/internal/analysis/community"
	"github.com/imtaebin/code-context-graph/internal/analysis/coupling"
	"github.com/imtaebin/code-context-graph/internal/analysis/coverage"
	"github.com/imtaebin/code-context-graph/internal/analysis/deadcode"
	"github.com/imtaebin/code-context-graph/internal/analysis/flows"
	"github.com/imtaebin/code-context-graph/internal/analysis/impact"
	"github.com/imtaebin/code-context-graph/internal/analysis/incremental"
	"github.com/imtaebin/code-context-graph/internal/analysis/largefunc"
	"github.com/imtaebin/code-context-graph/internal/analysis/query"
	"github.com/imtaebin/code-context-graph/internal/cli"
	mcpserver "github.com/imtaebin/code-context-graph/internal/mcp"
	"github.com/imtaebin/code-context-graph/internal/model"
	"github.com/imtaebin/code-context-graph/internal/parse/treesitter"
	"github.com/imtaebin/code-context-graph/internal/store/gormstore"
	"github.com/imtaebin/code-context-graph/internal/store/search"
)

var (
	_ mcpserver.ImpactAnalyzer    = (*impact.Analyzer)(nil)
	_ mcpserver.FlowTracer        = (*flows.Tracer)(nil)
	_ mcpserver.QueryService      = (*query.Service)(nil)
	_ mcpserver.LargefuncAnalyzer = (*largefunc.Service)(nil)
	_ mcpserver.DeadcodeAnalyzer  = (*deadcode.Service)(nil)
	_ mcpserver.CouplingAnalyzer  = (*coupling.Service)(nil)
	_ mcpserver.CoverageAnalyzer  = (*coverage.Service)(nil)
	_ mcpserver.CommunityBuilder  = (*community.Builder)(nil)
	_ mcpserver.IncrementalSyncer = (*incremental.Syncer)(nil)
)

func main() {
	logger := slog.Default()

	deps := &cli.Deps{
		Logger: logger,
	}

	deps.InitFunc = func(driver, dsn string) error {
		db, err := openDB(driver, dsn)
		if err != nil {
			return fmt.Errorf("open database: %w", err)
		}

		st := gormstore.New(db)
		if err := st.AutoMigrate(); err != nil {
			return fmt.Errorf("auto-migrate store: %w", err)
		}
		if err := db.AutoMigrate(&model.SearchDocument{}, &model.Flow{}, &model.FlowMembership{}); err != nil {
			return fmt.Errorf("migrate extra models: %w", err)
		}

		sb := newSearchBackend(driver)
		if err := sb.Migrate(db); err != nil {
			return fmt.Errorf("migrate search backend: %w", err)
		}

		walkers := buildWalkers(deps.Logger)
		syncerWalker := treesitter.NewWalker(treesitter.GoSpec, treesitter.WithLogger(deps.Logger))
		syncer := incremental.New(st, syncerWalker)

		deps.DB = db
		deps.Store = st
		deps.SearchBackend = sb
		deps.Walkers = walkers
		deps.Syncer = syncer
		deps.CleanupFunc = func() {
			for _, w := range walkers {
				w.Close()
			}
			syncerWalker.Close()
		}

		return nil
	}

	deps.ServeFunc = func(cfg cli.ServeConfig) error {
		return runServe(deps, cfg)
	}

	cmd := cli.NewRootCmd(deps)
	if err := cmd.Execute(); err != nil {
		slog.Error("command failed", "error", err)
		os.Exit(1)
	}
	if deps.CleanupFunc != nil {
		deps.CleanupFunc()
	}
}

// buildWalkers creates a Walker for each supported language extension.
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
		{treesitter.LuaSpec, []string{".lua"}},
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

func runServe(deps *cli.Deps, cfg cli.ServeConfig) error {
	deps.Logger.Info("starting code-context-graph MCP server")

	var cache *mcpserver.Cache
	if !cfg.NoCache && cfg.CacheTTL > 0 {
		cache = mcpserver.NewCache(cfg.CacheTTL)
		defer cache.Close()
		deps.Logger.Info("MCP cache enabled", "ttl", cfg.CacheTTL)
	}

	mcpWalkers := make(map[string]mcpserver.Parser, len(deps.Walkers))
	for ext, w := range deps.Walkers {
		mcpWalkers[ext] = w
	}

	mcpDeps := &mcpserver.Deps{
		Store:             deps.Store,
		DB:                deps.DB,
		Parser:            deps.Walkers[".go"],
		Walkers:           mcpWalkers,
		SearchBackend:     deps.SearchBackend,
		ImpactAnalyzer:    impact.New(deps.Store),
		FlowTracer:        flows.New(deps.Store),
		ChangesGitClient:  changes.NewExecGitClient(),
		QueryService:      query.New(deps.DB),
		LargefuncAnalyzer: largefunc.New(deps.DB),
		DeadcodeAnalyzer:  deadcode.New(deps.DB),
		CouplingAnalyzer:  coupling.New(deps.DB),
		CoverageAnalyzer:  coverage.New(deps.DB),
		CommunityBuilder:  community.New(deps.DB),
		Logger:            deps.Logger,
		Cache:             cache,
		RagIndexDir:       viper.GetString("rag.index_dir"),
		RagProjectDesc:    viper.GetString("rag.description"),
	}

	srv := mcpserver.NewServer(mcpDeps)

	deps.Logger.Info("serving MCP over stdio")
	if err := server.ServeStdio(srv); err != nil {
		return fmt.Errorf("MCP server: %w", err)
	}
	return nil
}

func openDB(driver, dsn string) (*gorm.DB, error) {
	cfg := &gorm.Config{
		Logger:                 gormlogger.Discard,
		SkipDefaultTransaction: true,
	}

	switch driver {
	case "sqlite":
		return gorm.Open(sqlite.Open(dsn), cfg)
	case "postgres":
		return gorm.Open(postgres.Open(dsn), cfg)
	default:
		return nil, fmt.Errorf("unsupported database driver: %s", driver)
	}
}

func newSearchBackend(driver string) search.Backend {
	switch driver {
	case "postgres":
		return search.NewPostgresBackend()
	default:
		return search.NewSQLiteBackend()
	}
}
