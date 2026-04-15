package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/mark3labs/mcp-go/server"
	"github.com/spf13/viper"
	"github.com/tae2089/trace"

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
	"github.com/imtaebin/code-context-graph/internal/webhook"
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

// main wires CLI dependencies and executes the root command.
// @intent 애플리케이션 시작 시 DB, 워커, MCP 실행 의존성을 구성해 CLI를 실행한다.
// @sideEffect 시그널 핸들러를 등록하고 명령 실행 중 필요한 리소스를 초기화한다.
func main() {
	logger := slog.Default()

	deps := &cli.Deps{
		Logger: logger,
	}

	deps.InitFunc = func(driver, dsn string) error {
		db, err := openDB(driver, dsn)
		if err != nil {
			return trace.Wrap(err, "open database")
		}

		st := gormstore.New(db)
		if err := st.AutoMigrate(); err != nil {
			return trace.Wrap(err, "auto-migrate store")
		}
		if err := db.AutoMigrate(&model.SearchDocument{}, &model.Flow{}, &model.FlowMembership{}); err != nil {
			return trace.Wrap(err, "migrate extra models")
		}

		sb := newSearchBackend(driver)
		if err := sb.Migrate(db); err != nil {
			return trace.Wrap(err, "migrate search backend")
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
			if sqlDB, err := db.DB(); err == nil {
				sqlDB.Close()
			}
		}

		return nil
	}

	deps.ServeFunc = func(cfg cli.ServeConfig) error {
		return runServe(deps, cfg)
	}

	cmd := cli.NewRootCmd(deps)

	// Signal handler: SIGINT/SIGTERM 수신 시 cleanup 실행 후 종료
	var cleanupOnce sync.Once
	cleanup := func() {
		cleanupOnce.Do(func() {
			if deps.CleanupFunc != nil {
				deps.CleanupFunc()
			}
		})
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		slog.Info("received signal, shutting down", "signal", sig)
		cleanup()
		os.Exit(1)
	}()

	if err := cmd.Execute(); err != nil {
		slog.Error("command failed", trace.SlogError(err))
		cleanup()
		os.Exit(1)
	}
	cleanup()
}

// buildWalkers creates a Walker for each supported language extension.
// @intent 지원 언어별 Tree-sitter 워커를 확장자 맵으로 등록한다.
// @return 파일 확장자에서 재사용 가능한 워커로 매핑된 테이블을 반환한다.
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

// runServe starts the MCP server with the configured transport.
// @intent CLI 의존성을 MCP 서버 의존성으로 변환해 실제 서버 실행을 위임한다.
// @sideEffect 캐시를 생성하고 stdio 또는 HTTP 서버를 시작한다.
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
		WorkspaceRoot:     cfg.WorkspaceRoot,
	}

	srv := mcpserver.NewServer(mcpDeps)

	switch cfg.Transport {
	case "streamable-http":
		return serveStreamableHTTP(deps, srv, cfg)
	default:
		deps.Logger.Info("serving MCP over stdio")
		if err := server.ServeStdio(srv); err != nil {
			return trace.Wrap(err, "MCP server")
		}
		return nil
	}
}

// serveStreamableHTTP serves the MCP server over streamable HTTP.
// @intent 원격 MCP 클라이언트를 위한 HTTP 엔드포인트와 헬스체크를 노출한다.
// @sideEffect HTTP 리스너를 열고 종료 시 graceful shutdown을 수행한다.
func serveStreamableHTTP(deps *cli.Deps, srv *server.MCPServer, cfg cli.ServeConfig) error {
	deps.Logger.Info("serving MCP over streamable-http", "addr", cfg.HTTPAddr, "stateless", cfg.Stateless)

	opts := []server.StreamableHTTPOption{
		server.WithEndpointPath("/mcp"),
	}
	if cfg.Stateless {
		opts = append(opts, server.WithStateLess(true))
	}

	httpSrv := server.NewStreamableHTTPServer(srv, opts...)

	mux := http.NewServeMux()
	mux.Handle("/mcp", httpSrv)
	mux.HandleFunc("/health", handleHealth)

	var syncQueue *webhook.SyncQueue
	if len(cfg.AllowRepo) > 0 {
		allowlist := webhook.NewRepoAllowlist(cfg.AllowRepo)
		secret := []byte(cfg.WebhookSecret)
		syncHandler := func(repoFullName, cloneURL string) {
			ns := webhook.ExtractNamespace(repoFullName)
			deps.Logger.Info("webhook sync started", "repo", repoFullName, "namespace", ns)
			cloneCtx, cloneCancel := context.WithTimeout(context.Background(), 10*time.Minute)
			defer cloneCancel()
			if err := webhook.CloneOrPull(cloneCtx, cloneURL, cfg.RepoRoot, ns, nil); err != nil {
				deps.Logger.Error("webhook clone/pull failed", "repo", repoFullName, "error", err)
				return
			}
			deps.Logger.Info("webhook sync completed", "repo", repoFullName, "namespace", ns)
		}
		syncQueue = webhook.NewSyncQueue(4, syncHandler)
		mux.Handle("/webhook", webhook.NewWebhookHandler(secret, allowlist, syncQueue.Add))
		deps.Logger.Info("webhook endpoint registered", "path", "/webhook", "allowedRepos", cfg.AllowRepo)
	}

	httpServer := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	signal.Reset(syscall.SIGINT, syscall.SIGTERM)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		errCh <- httpServer.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		return trace.Wrap(err, "HTTP server")
	case <-ctx.Done():
		deps.Logger.Info("shutting down HTTP server")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			return trace.Wrap(err, "HTTP server shutdown")
		}
		if syncQueue != nil {
			deps.Logger.Info("draining sync queue workers")
			syncQueue.Shutdown()
		}
		return nil
	}
}

// handleHealth responds to HTTP health checks.
// @intent HTTP 전송 모드에서 프로세스 생존 여부를 단순 JSON으로 확인시킨다.
// @domainRule GET 이외 메서드는 405로 거부한다.
// @sideEffect HTTP 응답 헤더와 바디를 기록한다.
func handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

// openDB opens a GORM connection for the configured driver.
// @intent 실행 환경에 맞는 SQLite 또는 PostgreSQL 연결을 생성한다.
// @requires driver는 sqlite 또는 postgres여야 한다.
// @return 초기화된 GORM DB 핸들을 반환한다.
func openDB(driver, dsn string) (*gorm.DB, error) {
	cfg := &gorm.Config{
		Logger:                 gormlogger.Discard,
		SkipDefaultTransaction: true,
	}

	var db *gorm.DB
	var err error

	switch driver {
	case "sqlite":
		db, err = gorm.Open(sqlite.Open(dsn), cfg)
		if err != nil {
			return nil, err
		}
		// Enable WAL mode for concurrent read/write support.
		if err := db.Exec("PRAGMA journal_mode=WAL").Error; err != nil {
			return nil, trace.Wrap(err, "enable WAL mode")
		}
		if err := db.Exec("PRAGMA busy_timeout=5000").Error; err != nil {
			return nil, trace.Wrap(err, "set busy timeout")
		}
	case "postgres":
		db, err = gorm.Open(postgres.Open(dsn), cfg)
		if err != nil {
			return nil, err
		}
	default:
		return nil, trace.New(fmt.Sprintf("unsupported database driver: %s", driver))
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, trace.Wrap(err, "get underlying sql.DB")
	}
	sqlDB.SetMaxOpenConns(25)
	sqlDB.SetMaxIdleConns(5)
	sqlDB.SetConnMaxLifetime(5 * time.Minute)
	sqlDB.SetConnMaxIdleTime(5 * time.Minute)

	return db, nil
}

// newSearchBackend selects the search backend for a database driver.
// @intent DB 종류에 맞는 전문 검색 구현을 선택해 일관된 인터페이스로 노출한다.
// @return postgres면 PostgresBackend, 그 외에는 SQLiteBackend를 반환한다.
func newSearchBackend(driver string) search.Backend {
	switch driver {
	case "postgres":
		return search.NewPostgresBackend()
	default:
		return search.NewSQLiteBackend()
	}
}
