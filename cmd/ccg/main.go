package main

import (
	"crypto/subtle"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net"
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

	"github.com/tae2089/code-context-graph/internal/analysis/changes"
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
	"github.com/tae2089/code-context-graph/internal/ctxns"
	mcpserver "github.com/tae2089/code-context-graph/internal/mcp"
	"github.com/tae2089/code-context-graph/internal/model"
	"github.com/tae2089/code-context-graph/internal/parse/treesitter"
	"github.com/tae2089/code-context-graph/internal/pathutil"
	"github.com/tae2089/code-context-graph/internal/service"
	"github.com/tae2089/code-context-graph/internal/store/gormstore"
	"github.com/tae2089/code-context-graph/internal/store/search"
	"github.com/tae2089/code-context-graph/internal/webhook"
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

var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

// main wires CLI dependencies and executes the root command.
// @intent 애플리케이션 시작 시 DB, 워커, MCP 실행 의존성을 구성해 CLI를 실행한다.
// @sideEffect 시그널 핸들러를 등록하고 명령 실행 중 필요한 리소스를 초기화한다.
func main() {
	logger := slog.Default()

	deps := &cli.Deps{
		Logger: logger,
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
		if err := migrateLegacyDefaultNamespace(db); err != nil {
			return trace.Wrap(err, "migrate legacy namespace")
		}

		sb := newSearchBackend(driver)
		if err := sb.Migrate(db); err != nil {
			return trace.Wrap(err, "migrate search backend")
		}

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

	deps.ServeFunc = func(cfg cli.ServeConfig) error {
		return runServe(deps, cfg)
	}

	cmd := cli.NewRootCmd(deps)

	if err := cmd.Execute(); err != nil {
		slog.Error("command failed", trace.SlogError(err))
		runCleanup()
		os.Exit(1)
	}
	runCleanup()
}

func migrateLegacyDefaultNamespace(db *gorm.DB) error {
	return db.Transaction(func(tx *gorm.DB) error {
		if err := failOnLegacyNamespaceCollisions(tx); err != nil {
			return err
		}

		updates := []struct {
			name string
			model any
		}{
			{name: "nodes", model: &model.Node{}},
			{name: "edges", model: &model.Edge{}},
			{name: "search_documents", model: &model.SearchDocument{}},
			{name: "communities", model: &model.Community{}},
			{name: "flows", model: &model.Flow{}},
			{name: "flow_memberships", model: &model.FlowMembership{}},
		}

		for _, update := range updates {
			if err := tx.Model(update.model).Where("namespace = ?", "").Update("namespace", ctxns.DefaultNamespace).Error; err != nil {
				return trace.Wrap(err, "backfill "+update.name)
			}
		}

		return nil
	})
}

func failOnLegacyNamespaceCollisions(db *gorm.DB) error {
	type nodeCollision struct {
		QualifiedName string
		FilePath      string
		StartLine     int
	}

	var nodeCollisions []nodeCollision
	if err := db.Raw(`
		SELECT legacy.qualified_name, legacy.file_path, legacy.start_line
		FROM nodes AS legacy
		INNER JOIN nodes AS current
			ON current.namespace = ?
			AND legacy.namespace = ''
			AND current.qualified_name = legacy.qualified_name
			AND current.file_path = legacy.file_path
			AND current.start_line = legacy.start_line
	`, ctxns.DefaultNamespace).Scan(&nodeCollisions).Error; err != nil {
		return trace.Wrap(err, "check node namespace collisions")
	}
	if len(nodeCollisions) > 0 {
		collision := nodeCollisions[0]
		return fmt.Errorf("legacy namespace collision for node %s (%s:%d)", collision.QualifiedName, collision.FilePath, collision.StartLine)
	}

	type edgeCollision struct {
		Fingerprint string
	}
	var edgeCollisions []edgeCollision
	if err := db.Raw(`
		SELECT legacy.fingerprint
		FROM edges AS legacy
		INNER JOIN edges AS current
			ON current.namespace = ?
			AND legacy.namespace = ''
			AND current.fingerprint = legacy.fingerprint
	`, ctxns.DefaultNamespace).Scan(&edgeCollisions).Error; err != nil {
		return trace.Wrap(err, "check edge namespace collisions")
	}
	if len(edgeCollisions) > 0 {
		return fmt.Errorf("legacy namespace collision for edge %s", edgeCollisions[0].Fingerprint)
	}

	type searchDocCollision struct {
		NodeID uint
	}
	var searchDocCollisions []searchDocCollision
	if err := db.Raw(`
		SELECT legacy.node_id
		FROM search_documents AS legacy
		INNER JOIN search_documents AS current
			ON current.namespace = ?
			AND legacy.namespace = ''
			AND current.node_id = legacy.node_id
	`, ctxns.DefaultNamespace).Scan(&searchDocCollisions).Error; err != nil {
		return trace.Wrap(err, "check search document namespace collisions")
	}
	if len(searchDocCollisions) > 0 {
		return fmt.Errorf("legacy namespace collision for search document node_id=%d", searchDocCollisions[0].NodeID)
	}

	type communityCollision struct {
		Key string
	}
	var communityCollisions []communityCollision
	if err := db.Raw(`
		SELECT legacy.key
		FROM communities AS legacy
		INNER JOIN communities AS current
			ON current.namespace = ?
			AND legacy.namespace = ''
			AND current.key = legacy.key
	`, ctxns.DefaultNamespace).Scan(&communityCollisions).Error; err != nil {
		return trace.Wrap(err, "check community namespace collisions")
	}
	if len(communityCollisions) > 0 {
		return fmt.Errorf("legacy namespace collision for community %s", communityCollisions[0].Key)
	}

	return nil
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
		Incremental:       deps.Syncer,
		Logger:            deps.Logger,
		Cache:             cache,
		RagIndexDir:       viper.GetString("rag.index_dir"),
		RagProjectDesc:    viper.GetString("rag.description"),
		NamespaceRoot:     cfg.NamespaceRoot,
		WorkspaceRoot:     cfg.WorkspaceRoot,
		RepoRoot:          cfg.RepoRoot,
	}

		srv := mcpserver.NewServer(mcpDeps)

	switch cfg.Transport {
	case "streamable-http":
		return serveStreamableHTTP(deps, srv, cfg)
	default:
		deps.Logger.Info("serving MCP over stdio")
		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer stop()
		errCh := make(chan error, 1)
		go func() {
			errCh <- server.ServeStdio(srv)
		}()
		select {
		case err := <-errCh:
			if err != nil {
				return trace.Wrap(err, "MCP server")
			}
		case <-ctx.Done():
			deps.Logger.Info("received signal, shutting down stdio MCP server")
		}
		return nil
	}
}

// serveStreamableHTTP serves the MCP server over streamable HTTP.
// @intent 원격 MCP 클라이언트를 위한 HTTP 엔드포인트와 헬스체크를 노출한다.
// @sideEffect HTTP 리스너를 열고 종료 시 graceful shutdown을 수행한다.
func serveStreamableHTTP(deps *cli.Deps, srv *server.MCPServer, cfg cli.ServeConfig) error {
	deps.Logger.Info("serving MCP over streamable-http", "addr", cfg.HTTPAddr, "stateless", cfg.Stateless)

	if err := validateHTTPExposure(cfg); err != nil {
		return err
	}

	opts := []server.StreamableHTTPOption{
		server.WithEndpointPath("/mcp"),
	}
	if cfg.Stateless {
		opts = append(opts, server.WithStateLess(true))
	}

	httpSrv := server.NewStreamableHTTPServer(srv, opts...)

	mux := http.NewServeMux()
	mux.Handle("/mcp", mcpAuthMiddleware(cfg.HTTPBearerToken, mcpserver.LimitHTTPBody(httpSrv)))
	mux.HandleFunc("/health", handleHealth)
	mux.Handle("/ready", readyHandler(func(r *http.Request) error {
		if deps.DB == nil {
			return fmt.Errorf("database not configured")
		}
		sqlDB, err := deps.DB.DB()
		if err != nil {
			return trace.Wrap(err, "get sql db")
		}
		return sqlDB.PingContext(r.Context())
	}))

	var syncQueue *webhook.SyncQueue
	syncCtx, syncCancel := context.WithCancel(context.Background())
	defer syncCancel()

	if len(cfg.AllowRepo) > 0 {
		rules := make([]webhook.RepoRule, 0, len(cfg.AllowRepo))
		for _, s := range cfg.AllowRepo {
			rules = append(rules, webhook.ParseRepoRule(s))
		}
		filter := webhook.NewRepoFilterFromRules(rules)
		secret := []byte(cfg.WebhookSecret)
		syncHandler := func(ctx context.Context, repoFullName, cloneURL, branch string) error {
			ns := webhook.ExtractNamespace(repoFullName)
			deps.Logger.Info("webhook sync started", "repo", repoFullName, "namespace", ns, "branch", branch)

			attemptCtx, attemptCancel := context.WithTimeout(ctx, 15*time.Minute)
			defer attemptCancel()

			if err := webhook.CloneOrPullBranch(attemptCtx, cloneURL, cfg.RepoRoot, ns, branch, nil); err != nil {
				deps.Logger.Error("webhook clone/pull failed", "repo", repoFullName, "error", err)
				return err
			}

			repoDir := webhook.RepoDir(cfg.RepoRoot, ns)
			includePaths, err := pathutil.LoadIncludePathsFromConfig(repoDir)
			if err != nil {
				deps.Logger.Error("webhook include_paths config invalid", "repo", repoFullName, "namespace", ns, "error", err)
				return err
			}
			graphSvc := &service.GraphService{
				Store:         deps.Store,
				DB:            deps.DB,
				SearchBackend: deps.SearchBackend,
				Walkers:       deps.Walkers,
				Logger:        deps.Logger,
			}
			buildCtx := ctxns.WithNamespace(attemptCtx, ns)
			stats, err := graphSvc.Build(buildCtx, service.BuildOptions{
				Dir:          repoDir,
				IncludePaths: includePaths,
			})
			if err != nil {
				deps.Logger.Error("webhook build failed", "repo", repoFullName, "error", err)
				return err
			}
			deps.Logger.Info("webhook sync completed", "repo", repoFullName, "namespace", ns,
				"files", stats.TotalFiles, "nodes", stats.TotalNodes, "edges", stats.TotalEdges)
			return nil
		}
		syncQueue = webhook.NewSyncQueueWithContext(syncCtx, cfg.WebhookWorkers, syncHandler)
		mux.Handle("/webhook", webhook.NewWebhookHandlerWithConfig(webhook.WebhookHandlerConfig{
			Secret:       secret,
			Filter:       filter,
			OnSync:       syncQueue.Add,
			Insecure:     cfg.InsecureWebhook,
			CloneBaseURL: cfg.RepoCloneBaseURL,
		}))
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

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("HTTP server goroutine panicked", "panic", r)
				errCh <- fmt.Errorf("HTTP server panicked: %v", r)
			}
		}()
		err := httpServer.ListenAndServe()
		if err != nil && err != http.ErrServerClosed {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case err := <-errCh:
		if err != nil {
			return trace.Wrap(err, "HTTP server")
		}
		return nil
	case <-ctx.Done():
		deps.Logger.Info("shutting down HTTP server")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			return trace.Wrap(err, "HTTP server shutdown")
		}
		if syncQueue != nil {
			deps.Logger.Info("cancelling sync context and draining workers")
			syncCancel()
			syncQueue.Shutdown()
		}
		return nil
	}
}

func validateHTTPExposure(cfg cli.ServeConfig) error {
	if cfg.Transport != "streamable-http" {
		return nil
	}
	if cfg.InsecureHTTP {
		return nil
	}
	if isLoopbackHTTPAddr(cfg.HTTPAddr) {
		return nil
	}
	if cfg.HTTPBearerToken == "" {
		return fmt.Errorf("non-loopback streamable-http requires --http-bearer-token or --insecure-http")
	}
	return nil
}

func mcpAuthMiddleware(token string, next http.Handler) http.Handler {
	if token == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !validBearerToken(r.Header.Get("Authorization"), token) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func validBearerToken(header, expected string) bool {
	const prefix = "Bearer "
	if len(header) <= len(prefix) || header[:len(prefix)] != prefix {
		return false
	}
	token := header[len(prefix):]
	if len(token) != len(expected) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(token), []byte(expected)) == 1
}

func isLoopbackHTTPAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "" || host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
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
	_, err := w.Write([]byte(`{"status":"ok"}`))
	if err != nil {
		slog.Error("health check write failed", "error", err)
	}
}

func readyHandler(check func(*http.Request) error) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := check(r); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			if _, writeErr := w.Write([]byte(`{"status":"not_ready"}`)); writeErr != nil {
				slog.Error("ready check write failed", "error", writeErr)
			}
			return
		}
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte(`{"status":"ready"}`)); err != nil {
			slog.Error("ready check write failed", "error", err)
		}
	})
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
