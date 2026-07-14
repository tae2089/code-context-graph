// @index Shared runtime wiring for ccg CLI and ccg-server binaries.
package runtime

import (
	"log/slog"
	"sync"

	"gorm.io/gorm"

	"github.com/tae2089/code-context-graph/internal/adapters/outbound/graphgorm"
	"github.com/tae2089/code-context-graph/internal/adapters/outbound/searchsql"
	"github.com/tae2089/code-context-graph/internal/adapters/outbound/treesitter"
	"github.com/tae2089/code-context-graph/internal/app/ingest"
	"github.com/tae2089/code-context-graph/internal/app/ingest/incremental"
	"github.com/tae2089/code-context-graph/internal/app/search/document"
	ccgconfig "github.com/tae2089/code-context-graph/internal/config"
	ccgdb "github.com/tae2089/code-context-graph/internal/db"
	"github.com/tae2089/code-context-graph/internal/db/migration"
	mcpruntime "github.com/tae2089/code-context-graph/internal/runtime/mcp"
	"github.com/tae2089/trace"
)

// Runtime holds shared graph, DB, parser, and search dependencies.
// @intent provide one dependency assembly path for local CLI and self-hosted server binaries.
// @sideEffect Init opens a database connection and Close closes parser/database resources.
type Runtime struct {
	Logger            *slog.Logger
	DB                *gorm.DB
	Store             *graphgorm.Store
	UnitOfWork        ingest.UnitOfWork
	Search            ingest.SearchWriter
	SearchMaintenance document.Maintenance
	SearchBackend     searchsql.Backend
	SearchReader      *searchsql.Reader
	Walkers           map[string]*treesitter.Walker
	Syncer            *incremental.Syncer
	closeOnce         sync.Once
	closeHook         func()
}

// NewRuntime creates shared runtime dependencies that do not require the database yet.
// @intent initialize parser walkers once before command-specific database setup runs.
func NewRuntime(logger *slog.Logger) *Runtime {
	if logger == nil {
		logger = slog.Default()
	}
	return &Runtime{
		Logger:  logger,
		Walkers: BuildWalkers(logger),
	}
}

// MCPComponents returns the shared application/adapter set used by stdio and HTTP MCP runtimes.
// @intent keep both transports on one grouped MCP assembly input without exposing composition to inbound adapters.
func (r *Runtime) MCPComponents() mcpruntime.Components {
	return mcpruntime.Components{
		Logger: r.Logger, Store: r.Store, UnitOfWork: r.UnitOfWork,
		Search: r.Search, SearchMaintenance: r.SearchMaintenance,
		SearchReader: r.SearchReader, Walkers: r.Walkers, Syncer: r.Syncer,
	}
}

// Init opens the configured DB and attaches store/search/incremental services to the runtime.
// @intent keep schema validation and graph storage wiring identical across ccg and ccg-server.
// @sideEffect opens a database connection and may run safe local schema initialization.
func (r *Runtime) Init(driver, dsn string) error {
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

	st := graphgorm.New(db)
	sb := ccgdb.NewSearchBackend(driver)

	parsers := make(map[string]incremental.Parser, len(r.Walkers))
	for ext, walker := range r.Walkers {
		parsers[ext] = walker
	}

	r.DB = db
	r.Store = st
	r.SearchBackend = sb
	r.SearchReader = searchsql.NewReader(db, sb)
	searchWriter := searchsql.NewSearchWriter(db, sb, r.Logger)
	r.Search = searchWriter
	r.SearchMaintenance = searchWriter
	r.UnitOfWork = graphgorm.NewUnitOfWork(db, func(tx *gorm.DB) ingest.SearchWriter {
		return searchsql.NewSearchWriter(tx, sb, r.Logger)
	})
	r.Syncer = incremental.NewWithRegistry(st, parsers)
	return nil
}

// Migrate runs application migrations for the provided database settings.
// @intent expose migration execution without coupling binaries to migration internals.
// @sideEffect modifies the target database schema.
func (r *Runtime) Migrate(driver, dsn, migrationsDir string) error {
	db, err := ccgdb.Open(driver, dsn)
	if err != nil {
		return trace.Wrap(err, "open database")
	}
	defer func() {
		if sqlDB, err := db.DB(); err == nil {
			sqlDB.Close()
		}
	}()
	return migration.RunMigrations(db, driver, migrationsDir)
}

// Close releases parser and database resources owned by the runtime.
// @intent give both binaries one cleanup path for shared dependencies.
// @sideEffect closes Tree-sitter parsers and the active DB connection.
func (r *Runtime) Close() {
	r.closeOnce.Do(func() {
		if r.closeHook != nil {
			r.closeHook()
		}
		closedWalkers := make(map[*treesitter.Walker]struct{}, len(r.Walkers))
		for _, w := range r.Walkers {
			if _, ok := closedWalkers[w]; ok {
				continue
			}
			w.Close()
			closedWalkers[w] = struct{}{}
		}
		if r.DB != nil {
			if sqlDB, err := r.DB.DB(); err == nil {
				sqlDB.Close()
			}
		}
	})
}

// BuildWalkers constructs the language parser registry keyed by file extension.
// @intent register supported language walkers for build, update, and MCP execution paths.
func BuildWalkers(logger *slog.Logger) map[string]*treesitter.Walker {
	// langEntry pairs one language spec with the extensions it should parse.
	// @intent keep language specs and extension aliases together during registry initialization.
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
