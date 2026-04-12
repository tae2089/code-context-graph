package main

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/mark3labs/mcp-go/server"
	chromem "github.com/philippgille/chromem-go"

	"gorm.io/driver/mysql"
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

func main() {
	logger := slog.Default()

	// CLI용 DB + Store + Walkers 초기화 (SQLite 기본)
	db, err := openDB("sqlite", "ccg.db")
	if err != nil {
		slog.Error("open database", "error", err)
		os.Exit(1)
	}

	st := gormstore.New(db)
	if err := st.AutoMigrate(); err != nil {
		slog.Error("auto-migrate", "error", err)
		os.Exit(1)
	}
	if err := db.AutoMigrate(&model.SearchDocument{}, &model.Flow{}, &model.FlowMembership{}); err != nil {
		slog.Error("migrate extra models", "error", err)
		os.Exit(1)
	}

	sb := newSearchBackend("sqlite")
	if err := sb.Migrate(db); err != nil {
		slog.Error("migrate search backend", "error", err)
		os.Exit(1)
	}

	walkers := buildWalkers(logger)

	syncer := incremental.New(st, walkers[".go"])

	// Initialize vector DB for semantic search (persisted to disk)
	var vectorStore *cli.VectorStore
	if os.Getenv("OPENAI_API_KEY") != "" {
		vdb := chromem.NewDB()
		collection, err := vdb.GetOrCreateCollection("nodes", nil, nil)
		if err != nil {
			slog.Warn("vector DB init failed", "error", err)
		} else {
			vectorStore = &cli.VectorStore{DB: vdb, Collection: collection}
		}
	}

	deps := &cli.Deps{
		Logger:        logger,
		DB:            db,
		Store:         st,
		SearchBackend: sb,
		Walkers:       walkers,
		Syncer:        syncer,
		VectorDB:      vectorStore,
		ServeFunc: func(cfg cli.ServeConfig) error {
			return runServe(cfg.DBDriver, cfg.DSN)
		},
	}

	cmd := cli.NewRootCmd(deps)
	if err := cmd.Execute(); err != nil {
		slog.Error("command failed", "error", err)
		os.Exit(1)
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
		{treesitter.CSharpSpec, []string{".cs"}},
		{treesitter.KotlinSpec, []string{".kt", ".kts"}},
		{treesitter.PHPSpec, []string{".php"}},
		{treesitter.SwiftSpec, []string{".swift"}},
		{treesitter.ScalaSpec, []string{".scala", ".sc"}},
		{treesitter.LuaSpec, []string{".lua"}},
		{treesitter.BashSpec, []string{".sh", ".bash", ".zsh"}},
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

func runServe(dbDriver, dsn string) error {
	logger := slog.Default()
	logger.Info("starting code-context-graph", "db", dbDriver)

	db, err := openDB(dbDriver, dsn)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}

	st := gormstore.New(db)
	if err := st.AutoMigrate(); err != nil {
		return fmt.Errorf("auto-migrate: %w", err)
	}
	if err := db.AutoMigrate(&model.SearchDocument{}, &model.Flow{}, &model.FlowMembership{}); err != nil {
		return fmt.Errorf("migrate extra models: %w", err)
	}

	sb := newSearchBackend(dbDriver)
	if err := sb.Migrate(db); err != nil {
		return fmt.Errorf("migrate search backend: %w", err)
	}

	walker := treesitter.NewWalker(treesitter.GoSpec, treesitter.WithLogger(logger))

	mcpDeps := &mcpserver.Deps{
		Store:            st,
		DB:               db,
		Parser:           walker,
		SearchBackend:    sb,
		ImpactAnalyzer:   impact.New(st),
		FlowTracer:       flows.New(st),
		ChangesGitClient: changes.NewExecGitClient(),
		QueryService:     query.New(db),
		LargefuncAnalyzer: largefunc.New(db),
		DeadcodeAnalyzer:  deadcode.New(db),
		CouplingAnalyzer:  coupling.New(db),
		CoverageAnalyzer:  coverage.New(db),
		CommunityBuilder:  community.New(db),
		Logger:           logger,
	}

	srv := mcpserver.NewServer(mcpDeps)

	logger.Info("serving MCP over stdio")
	if err := server.ServeStdio(srv); err != nil {
		return fmt.Errorf("MCP server: %w", err)
	}
	return nil
}

func openDB(driver, dsn string) (*gorm.DB, error) {
	cfg := &gorm.Config{Logger: gormlogger.Discard}

	switch driver {
	case "sqlite":
		return gorm.Open(sqlite.Open(dsn), cfg)
	case "postgres":
		return gorm.Open(postgres.Open(dsn), cfg)
	case "mysql":
		return gorm.Open(mysql.Open(dsn), cfg)
	default:
		return nil, fmt.Errorf("unsupported database driver: %s", driver)
	}
}

func newSearchBackend(driver string) search.Backend {
	switch driver {
	case "postgres":
		return search.NewPostgresBackend()
	case "mysql":
		return search.NewMySQLBackend()
	default:
		return search.NewSQLiteBackend()
	}
}
