package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	gomigrate "github.com/golang-migrate/migrate/v4"
	"github.com/mark3labs/mcp-go/server"
	"github.com/tae2089/trace"

	"gorm.io/gorm"

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
	"github.com/tae2089/code-context-graph/internal/ctxns"
	ccgdb "github.com/tae2089/code-context-graph/internal/db"
	"github.com/tae2089/code-context-graph/internal/db/migration"
	mcpserver "github.com/tae2089/code-context-graph/internal/mcp"
	"github.com/tae2089/code-context-graph/internal/model"
	"github.com/tae2089/code-context-graph/internal/parse/treesitter"
	postprocesspolicy "github.com/tae2089/code-context-graph/internal/postprocess/policy"
	ccgserver "github.com/tae2089/code-context-graph/internal/server"
	"github.com/tae2089/code-context-graph/internal/store/gormstore"
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

const (
	schemaVersionKey      = "schema"
	legacySchemaTable     = "ccg_schema_versions"
	requiredSchemaVersion = 3
)

// main wires CLI dependencies and executes the root command.
// @intent 애플리케이션 시작 시 DB, 워커, MCP 실행 의존성을 구성해 CLI를 실행한다.
// @sideEffect 시그널 핸들러를 등록하고 명령 실행 중 필요한 리소스를 초기화한다.
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
		db, err := openDB(driver, dsn)
		if err != nil {
			return trace.Wrap(err, "open database")
		}
		if err := ensureSchemaVersion(db, driver, dsn, configuredMigrationsDir()); err != nil {
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
		db, err := openDB(cfg.DBDriver, cfg.DBDSN)
		if err != nil {
			return trace.Wrap(err, "open database")
		}
		defer func() {
			if sqlDB, err := db.DB(); err == nil {
				sqlDB.Close()
			}
		}()
		return runMigrations(db, cfg.DBDriver, cfg.MigrationsDir)
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

// runMigrations executes all pending database migrations.
// @intent 최신 스키마 버전을 적용하기 위해 미진행된 모든 마이그레이션 파일을 실행한다.
// @sideEffect 데이터베이스 스키마를 변경하고 성공 시 정합성 검증을 수행한다.
func runMigrations(db *gorm.DB, driver, migrationsDir string) error {
	return migration.Run(
		db,
		driver,
		migrationsDir,
		func(db *gorm.DB, migrator *gomigrate.Migrate, legacyDriver string) error {
			_, err := baselineLegacySchemaVersion(db, legacyDriver, migrator)
			return err
		},
		validateSchemaParity,
	)
}

// ensureSchemaVersion checks and automatically migrates the database schema if needed.
// @intent 실행 시점에 DB 스키마 버전을 확인하고, 필요 시 로컬 SQLite에 대해 자동 마이그레이션을 수행한다.
// @sideEffect 데이터베이스 연결 상태를 확인하고 마이그레이션을 실행할 수 있다.
func ensureSchemaVersion(db *gorm.DB, driver, dsn, migrationsDir string) error {
	if err := checkSchemaVersion(db); err == nil {
		return validateRuntimeSchema(db, driver, false)
	}

	if !shouldAutoMigrateLocalSQLite(driver, dsn) || db.Migrator().HasTable("schema_migrations") {
		if err := checkSchemaVersion(db); err != nil {
			return err
		}
		return validateRuntimeSchema(db, driver, false)
	}
	if err := runMigrations(db, driver, migrationsDir); err != nil {
		return trace.Wrap(err, "auto-migrate local sqlite database")
	}
	if err := checkSchemaVersion(db); err != nil {
		return err
	}
	return validateRuntimeSchema(db, driver, true)
}

// validateRuntimeSchema logs and returns the runtime schema validation result.
// @intent 실행 시점 스키마 정합성 검증 결과를 로깅하고 호출자에게 실패 원인을 전달한다.
func validateRuntimeSchema(db *gorm.DB, driver string, autoMigrated bool) error {
	if err := validateSchemaParity(db, driver); err != nil {
		wrapped := actionableSchemaParityError(err)
		slog.Error("database runtime schema check failed", "driver", driver, "required_version", requiredSchemaVersion, "auto_migrated", autoMigrated, trace.SlogError(wrapped))
		return wrapped
	}
	slog.Info("database runtime schema check passed", "driver", driver, "required_version", requiredSchemaVersion, "auto_migrated", autoMigrated)
	return nil
}

func actionableSchemaParityError(err error) error {
	return migration.ActionableSchemaParityError(err)
}

// shouldAutoMigrateLocalSQLite determines if the local SQLite database should be automatically migrated.
// @intent 로컬 SQLite(ccg.db) 환경에서 명시적 명령 없이도 자동 마이그레이션을 수행할지 여부를 판별한다.
func shouldAutoMigrateLocalSQLite(driver, dsn string) bool {
	if driver != "sqlite" {
		return false
	}
	path := strings.TrimSpace(dsn)
	if path == "" {
		return true
	}
	path = strings.TrimPrefix(path, "file:")
	if idx := strings.Index(path, "?"); idx >= 0 {
		path = path[:idx]
	}
	if path == ":memory:" || filepath.Base(path) != "ccg.db" {
		return false
	}
	if filepath.IsAbs(path) {
		return true
	}
	return filepath.Clean(path) == "ccg.db"
}

// configuredMigrationsDir returns the migration directory from configuration.
// @intent 설정 파일(viper)에 정의된 마이그레이션 파일 경로를 가져온다.
func configuredMigrationsDir() string {
	return ccgconfig.MigrationsDir()
}

// checkSchemaVersion verifies the current database schema version and dirty state.
// @intent DB에 기록된 현재 버전을 확인하여 요구 버전과 일치하는지, 혹은 비정상 종료된 마이그레이션이 있는지 검증한다.
// @requires schema_migrations 테이블이 존재해야 한다.
func checkSchemaVersion(db *gorm.DB) error {
	return migration.CheckSchemaVersion(db, requiredSchemaVersion)
}

// baselineLegacySchemaVersion handles the transition from older schema versioning to golang-migrate.
// @intent 구버전 스키마 관리 방식을 최신 마이그레이션 도구(golang-migrate)로 안전하게 동기화한다.
// @sideEffect 기존 데이터가 요구 버전과 일치하면 마이그레이션 메타데이터를 강제로 생성한다.
func baselineLegacySchemaVersion(db *gorm.DB, driver string, migrator *gomigrate.Migrate) (bool, error) {
	return migration.BaselineLegacySchemaVersion(
		db,
		migrator,
		driver,
		schemaVersionKey,
		legacySchemaTable,
		requiredSchemaVersion,
	)
}

type migrateSchemaVersion struct {
	Version uint `gorm:"column:version"`
	Dirty   bool `gorm:"column:dirty"`
}

type migrationSourceInfo = migration.SourceInfo

// schemaColumn identifies a required table/column pair for schema validation.
// @intent 스키마 정합성 검증에서 필수 컬럼을 테이블 단위로 지정하기 위한 식별자를 표현한다.
type schemaColumn struct {
	table  string
	column string
}

func migrationSourceInfoFor(driver, migrationsDir string) (migrationSourceInfo, error) {
	return migration.SourceInfoFor(driver, migrationsDir)
}

func newMigrator(db *gorm.DB, driver, migrationsDir string) (*gomigrate.Migrate, migrationSourceInfo, error) {
	return migration.NewMigrator(db, driver, migrationsDir)
}

// modelNullabilityColumns returns required table/column pairs for migration parity checks.
func modelNullabilityColumns() []schemaColumn {
	columns := migration.ModelNullabilityColumns()
	out := make([]schemaColumn, 0, len(columns))
	for _, c := range columns {
		out = append(out, schemaColumn{table: c.Table, column: c.Column})
	}
	return out
}

// validateSchemaParity delegates to migration.ValidateSchemaParity.
func validateSchemaParity(db *gorm.DB, driver string) error {
	return migration.ValidateSchemaParity(db, driver)
}

// sqliteColumn stores SQLite column metadata used by sqliteColumnInfo.
type sqliteColumn struct {
	exists  bool
	notNull bool
}

// sqliteColumnInfo retrieves metadata about a specific column in a SQLite table.
func sqliteColumnInfo(db *gorm.DB, tableName, columnName string) (sqliteColumn, error) {
	info, err := migration.SQLiteColumnInfo(db, tableName, columnName)
	if err != nil {
		return sqliteColumn{}, err
	}
	return sqliteColumn{
		exists:  info.Exists,
		notNull: info.NotNull,
	}, nil
}

// sqliteColumnExists checks if a column exists in a SQLite table.
func sqliteColumnExists(db *gorm.DB, tableName, columnName string) (bool, error) {
	return migration.SQLiteColumnExists(db, tableName, columnName)
}

// sqliteColumnNotNull checks if a column has a NOT NULL constraint in SQLite.
func sqliteColumnNotNull(db *gorm.DB, tableName, columnName string) (bool, error) {
	return migration.SQLiteColumnNotNull(db, tableName, columnName)
}

// validateSQLiteSchemaParity is delegated to migration validation.
func validateSQLiteSchemaParity(db *gorm.DB) error {
	return migration.ValidateSchemaParity(db, "sqlite")
}

// validatePostgresSchemaParity is delegated to migration validation.
func validatePostgresSchemaParity(db *gorm.DB) error {
	return migration.ValidateSchemaParity(db, "postgres")
}

// postgresColumnNotNull checks if a column has a NOT NULL constraint in Postgres.
func postgresColumnNotNull(db *gorm.DB, tableName, columnName string) (bool, error) {
	return migration.PostgresColumnNotNull(db, tableName, columnName)
}

// postgresColumnDataType retrieves the data type of a specific column in Postgres.
func postgresColumnDataType(db *gorm.DB, tableName, columnName string) (string, error) {
	return migration.PostgresColumnDataType(db, tableName, columnName)
}

// postgresIndexExists checks if an index exists in Postgres.
func postgresIndexExists(db *gorm.DB, indexName string) (bool, error) {
	return migration.PostgresIndexExists(db, indexName)
}

// postgresTriggerExists checks if a non-internal trigger exists in Postgres.
func postgresTriggerExists(db *gorm.DB, triggerName string) (bool, error) {
	return migration.PostgresTriggerExists(db, triggerName)
}

// schemaTypeCheck defines an expected Postgres column type assertion.
type schemaTypeCheck struct {
	table    string
	column   string
	dataType string
}

// migrateLegacyDefaultNamespace backfills empty namespaces to the default value.
// @intent 기존 데이터 중 네임스페이스가 비어있는 레코드를 기본 네임스페이스(DefaultNamespace)로 일괄 업데이트한다.
// @sideEffect 트랜잭션 내에서 여러 테이블을 업데이트하며 충돌 감지 시 중단된다.
func migrateLegacyDefaultNamespace(db *gorm.DB) error {
	return db.Transaction(func(tx *gorm.DB) error {
		if err := failOnLegacyNamespaceCollisions(tx); err != nil {
			return err
		}

		updates := []struct {
			name  string
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

// failOnLegacyNamespaceCollisions blocks namespace backfill when legacy rows would collide.
// @intent 기본 네임스페이스로 이관할 때 기존 레코드와 충돌하는 데이터가 있으면 마이그레이션을 중단시킨다.
func failOnLegacyNamespaceCollisions(db *gorm.DB) error {
	// nodeCollision defines a collision structure for nodes during namespace migration.
	// @intent 네임스페이스 마이그레이션 중 충돌이 발생한 노드의 식별 정보를 담는다.
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

	// edgeCollision defines a collision structure for edges during namespace migration.
	// @intent 네임스페이스 마이그레이션 중 충돌이 발생한 엣지의 핑거프린트 정보를 담는다.
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

	// searchDocCollision defines a collision structure for search documents during namespace migration.
	// @intent 네임스페이스 마이그레이션 중 충돌이 발생한 검색 문서의 노드 ID 정보를 담는다.
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

	// communityCollision defines a collision structure for communities during namespace migration.
	// @intent 네임스페이스 마이그레이션 중 충돌이 발생한 커뮤니티 키 정보를 담는다.
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
	// langEntry defines a mapping between Tree-sitter specs and file extensions.
	// @intent 특정 프로그래밍 언어 사양과 대응되는 파일 확장자 목록을 묶어 관리한다.
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
	return ccgserver.Run(deps, cfg, version, ccgconfig.RagIndexDir(), ccgconfig.RagDescription())
}

// flushMCPQueryCache clears the MCP query cache if it exists.
// @intent MCP 서버 캐시를 비워 새로운 분석 결과가 반영되도록 한다.
func flushMCPQueryCache(cache *mcpserver.Cache) {
	ccgserver.FlushMCPQueryCache(cache)
}

// mcpPostprocessPolicy manages post-processing policies for the MCP server.
// @intent MCP 서버에서 실행되는 후처리 작업(flows, communities 등)의 정책과 상태를 관리한다.
type mcpPostprocessPolicy = ccgserver.MCPPostprocessPolicy

// newMCPPostprocessPolicy creates a new mcpPostprocessPolicy.
// @intent 데이터베이스 연결을 사용하는 MCP용 후처리 정책 엔진을 초기화한다.
func newMCPPostprocessPolicy(db *gorm.DB) *mcpPostprocessPolicy {
	return (*mcpPostprocessPolicy)(ccgserver.NewPostprocessPolicy(db))
}

// serveStreamableHTTP serves the MCP server over streamable HTTP.
// @intent 원격 MCP 클라이언트를 위한 HTTP 엔드포인트와 헬스체크를 노출한다.
// @sideEffect HTTP 리스너를 열고 종료 시 graceful shutdown을 수행한다.
func serveStreamableHTTP(deps *cli.Deps, srv *server.MCPServer, cfg cli.ServeConfig, cache *mcpserver.Cache, postprocessSummary func(context.Context) (*postprocesspolicy.StatusSummary, error)) error {
	return ccgserver.RunStreamableHTTP(deps, srv, cfg, cache, postprocessSummary)
}

// validateHTTPExposure ensures non-loopback streamable-http requires authentication.
// @intent HTTP 노출 시 루프백 주소가 아닌 경우 반드시 인증 토큰 설정을 강제하여 보안을 강화한다.
func validateHTTPExposure(cfg cli.ServeConfig) error {
	return ccgserver.ValidateHTTPExposure(cfg)
}

// mcpAuthMiddleware provides bearer token authentication for MCP HTTP endpoints.
// @intent 설정된 Bearer 토큰을 검증하여 허가되지 않은 사용자의 MCP 접근을 차단한다.
func mcpAuthMiddleware(token string, next http.Handler) http.Handler {
	return ccgserver.MCPAuthMiddleware(token, next)
}

func withHTTPTraceContext(next http.Handler) http.Handler {
	return ccgserver.WithHTTPTraceContext(next)
}

// validBearerToken validates a bearer token against an expected value.
// @intent HTTP Authorization 헤더의 Bearer 토큰이 기대하는 값과 일치하는지 상수 시간 비교로 검증한다.
func validBearerToken(header, expected string) bool {
	return ccgserver.ValidateBearerToken(header, expected)
}

// isLoopbackHTTPAddr checks if an address is a loopback address.
// @intent 주어진 HTTP 주소가 로컬 루프백(localhost, 127.0.0.1 등) 주소인지 판별한다.
func isLoopbackHTTPAddr(addr string) bool {
	return ccgserver.IsLoopbackHTTPAddr(addr)
}

// handleHealth responds to HTTP health checks.
// @intent HTTP 전송 모드에서 프로세스 생존 여부를 단순 JSON으로 확인시킨다.
// @domainRule GET 이외 메서드는 405로 거부한다.
// @sideEffect HTTP 응답 헤더와 바디를 기록한다.
func handleHealth(w http.ResponseWriter, r *http.Request) {
	ccgserver.HandleHealth(w, r)
}

// readyHandler handles HTTP ready checks.
// @intent 시스템이 실제로 요청을 처리할 수 있는 상태(DB 연결, 웹훅 큐 가용성 등)인지 확인하는 엔드포인트를 제공한다.
func readyHandler(check func(*http.Request) error) http.Handler {
	return ccgserver.ReadyHandler(check)
}

// statusHandler provides detailed system status including DB, webhooks, and post-processing.
// @intent 시스템의 전반적인 상태(DB 연결, 웹훅 큐 통계, 후처리 엔진 요약)를 JSON 형태로 제공한다.
// @domainRule 상태가 정상이 아니거나 지연 발생 시 HTTP 503 또는 degraded 상태를 반환한다.
func statusHandler(dbCheck func(*http.Request) error, webhookTimeout time.Duration, queue func() *webhook.SyncQueue, postprocessSummary func(context.Context) (*postprocesspolicy.StatusSummary, error)) http.Handler {
	return ccgserver.StatusHandler(dbCheck, webhookTimeout, queue, postprocessSummary)
}

// webhookBlockingReadyCheck checks if the webhook queue is blocked.
// @intent 웹훅 큐의 상태를 확인하여 시스템이 요청을 받을 수 있는지 판별한다.
func webhookBlockingReadyCheck(q *webhook.SyncQueue, timeout time.Duration) error {
	return ccgserver.WebhookBlockingReadyCheck(q, timeout)
}

// webhookStatsBlockingReady checks if the webhook stats indicate a blocked state.
// @intent 웹훅 통계 수치를 바탕으로 큐 지연이나 초과 상태를 확인하여 서비스 불가 여부를 결정한다.
func webhookStatsBlockingReady(stats webhook.SyncQueueStats, timeout time.Duration) error {
	return ccgserver.WebhookStatsBlockingReady(stats, timeout)
}

// webhookStatsDegraded checks if the webhook stats indicate a degraded state.
// @intent 최근 웹훅 처리 실패 이력이 성공 이력보다 최신인 경우를 찾아 시스템 상태 저하를 판별한다.
func webhookStatsDegraded(stats webhook.SyncQueueStats) bool {
	return ccgserver.WebhookStatsDegraded(stats)
}

// webhookRepoStatsDegraded checks if a specific repo's stats indicate a degraded state.
// @intent 특정 저장소 단위에서 최근 처리 결과가 실패인지 확인하여 상태 저하 여부를 결정한다.
func webhookRepoStatsDegraded(stats webhook.RepoStats) bool {
	return ccgserver.WebhookRepoStatsDegraded(stats)
}

// openDB opens a GORM connection for the configured driver.
// @intent 실행 환경에 맞는 SQLite 또는 PostgreSQL 연결을 생성한다.
// @requires driver는 sqlite 또는 postgres여야 한다.
// @return 초기화된 GORM DB 핸들을 반환한다.
func openDB(driver, dsn string) (*gorm.DB, error) {
	return ccgdb.Open(driver, dsn)
}

// sqlDBPool abstracts the pool tuning methods used for sql.DB.
// @intent 드라이버별 연결 풀 설정을 테스트 가능한 최소 인터페이스로 추상화한다.
type sqlDBPool = ccgdb.SQLDBPool

// configureDBPool applies driver-specific SQL connection pool settings.
// @intent SQLite와 Postgres의 특성에 맞는 연결 풀 크기와 수명 정책을 일관되게 적용한다.
func configureDBPool(sqlDB sqlDBPool, driver string) {
	ccgdb.ConfigurePool(sqlDB, driver)
}
