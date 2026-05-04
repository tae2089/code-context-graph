package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	gomigrate "github.com/golang-migrate/migrate/v4"
	migratedb "github.com/golang-migrate/migrate/v4/database"
	migratepostgres "github.com/golang-migrate/migrate/v4/database/postgres"
	migratesqlite3 "github.com/golang-migrate/migrate/v4/database/sqlite3"
	"github.com/golang-migrate/migrate/v4/source"
	"github.com/golang-migrate/migrate/v4/source/file"
	"github.com/golang-migrate/migrate/v4/source/iofs"
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
	"github.com/tae2089/code-context-graph/internal/migrationfs"
	"github.com/tae2089/code-context-graph/internal/model"
	"github.com/tae2089/code-context-graph/internal/obs"
	"github.com/tae2089/code-context-graph/internal/parse/treesitter"
	"github.com/tae2089/code-context-graph/internal/pathutil"
	postprocesspolicy "github.com/tae2089/code-context-graph/internal/postprocess/policy"
	"github.com/tae2089/code-context-graph/internal/service"
	"github.com/tae2089/code-context-graph/internal/store/gormstore"
	"github.com/tae2089/code-context-graph/internal/store/search"
	"github.com/tae2089/code-context-graph/internal/webhook"
	"go.opentelemetry.io/otel/attribute"
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
		sb := newSearchBackend(driver)

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
	migrator, sourceInfo, err := newMigrator(db, driver, migrationsDir)
	if err != nil {
		return err
	}
	logMigrationSource(sourceInfo)

	if _, err := baselineLegacySchemaVersion(db, driver, migrator); err != nil {
		return err
	}
	err = migrator.Up()
	if err != nil && !errors.Is(err, gomigrate.ErrNoChange) {
		return trace.Wrap(err, "run database migrations")
	}
	if err := validateSchemaParity(db, driver); err != nil {
		return actionableSchemaParityError(err)
	}
	return nil
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

// actionableSchemaParityError wraps a schema parity error with actionable instructions.
// @intent 스키마 불일치 오류 발생 시 사용자가 취해야 할 조치(migrate 실행 등)를 포함한 메시지를 생성한다.
func actionableSchemaParityError(err error) error {
	return fmt.Errorf("database schema parity check failed: %w; run `ccg migrate`; if already migrated, verify migration source and schema drift", err)
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
	return strings.TrimSpace(viper.GetString("migrations.dir"))
}

// checkSchemaVersion verifies the current database schema version and dirty state.
// @intent DB에 기록된 현재 버전을 확인하여 요구 버전과 일치하는지, 혹은 비정상 종료된 마이그레이션이 있는지 검증한다.
// @requires schema_migrations 테이블이 존재해야 한다.
func checkSchemaVersion(db *gorm.DB) error {
	if !db.Migrator().HasTable("schema_migrations") {
		return fmt.Errorf("database schema is not initialized; run `ccg migrate` first")
	}

	var current migrateSchemaVersion
	err := db.Table("schema_migrations").First(&current).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("database schema version is missing; run `ccg migrate` first")
		}
		return trace.Wrap(err, "check schema version")
	}
	if current.Dirty {
		return fmt.Errorf("database schema migration is dirty at version %d; resolve the failed migration and run `ccg migrate`", current.Version)
	}
	if int(current.Version) != requiredSchemaVersion {
		return fmt.Errorf("database schema version %d is incompatible with required version %d; run `ccg migrate`", current.Version, requiredSchemaVersion)
	}
	return nil
}

// migrateSchemaVersion represents the current version row in schema_migrations.
// @intent schema_migrations 테이블에서 현재 적용된 버전과 dirty 상태를 읽기 위한 레코드 형태를 정의한다.
type migrateSchemaVersion struct {
	Version uint `gorm:"column:version"`
	Dirty   bool `gorm:"column:dirty"`
}

// migrationSourceInfo describes which migration source is being used.
// @intent 현재 마이그레이션이 내장 소스인지 외부 디렉토리인지와 해당 경로 정보를 함께 전달한다.
type migrationSourceInfo struct {
	Kind   string
	Driver string
	Path   string
}

// newMigrator creates a new golang-migrate instance.
// @intent DB 드라이버와 마이그레이션 소스를 결합하여 실제 마이그레이션을 수행할 객체를 생성한다.
func newMigrator(db *gorm.DB, driver, migrationsDir string) (*gomigrate.Migrate, migrationSourceInfo, error) {
	databaseDriver, databaseName, err := migrateDatabaseDriver(db, driver)
	if err != nil {
		return nil, migrationSourceInfo{}, err
	}

	sourceDriver, sourceName, sourceInfo, err := migrateSourceDriver(driver, migrationsDir)
	if err != nil {
		return nil, migrationSourceInfo{}, err
	}
	migrator, err := gomigrate.NewWithInstance(sourceName, sourceDriver, databaseName, databaseDriver)
	if err != nil {
		return nil, migrationSourceInfo{}, trace.Wrap(err, "create migrator")
	}
	return migrator, sourceInfo, nil
}

// migrateSourceDriver initializes the migration source driver for embedded or local files.
// @intent 실행 모드에 따라 바이너리 내장 파일(iofs) 또는 로컬 디렉토리(file)를 마이그레이션 소스로 설정한다.
// @return 초기화된 source.Driver와 드라이버 이름을 반환한다.
func migrateSourceDriver(driver, migrationsDir string) (source.Driver, string, migrationSourceInfo, error) {
	sourceInfo, err := migrationSourceInfoFor(driver, migrationsDir)
	if err != nil {
		return nil, "", migrationSourceInfo{}, err
	}
	if sourceInfo.Kind == "embedded" {
		d, err := iofs.New(migrationfs.FS, driver)
		if err != nil {
			return nil, "", migrationSourceInfo{}, trace.Wrap(err, "create embedded migration source")
		}
		return d, "iofs", sourceInfo, nil
	}
	sourceURL := (&url.URL{Scheme: "file", Path: sourceInfo.Path}).String()
	d, err := (&file.File{}).Open(sourceURL)
	if err != nil {
		return nil, "", migrationSourceInfo{}, trace.Wrap(err, "create file migration source")
	}
	return d, "file", sourceInfo, nil
}

// migrationSourceInfoFor returns metadata about the migration source.
// @intent 설정된 경로에 따라 마이그레이션 소스가 내장형(embedded)인지 외부 파일(external)인지 판단한다.
func migrationSourceInfoFor(driver, migrationsDir string) (migrationSourceInfo, error) {
	if strings.TrimSpace(migrationsDir) == "" {
		return migrationSourceInfo{Kind: "embedded", Driver: driver}, nil
	}
	dir, err := migrationSourceDir(migrationsDir, driver)
	if err != nil {
		return migrationSourceInfo{}, err
	}
	return migrationSourceInfo{Kind: "external", Driver: driver, Path: dir}, nil
}

// logMigrationSource logs information about the migration source being used.
// @intent 어떤 종류의 마이그레이션 소스(내장/외부)가 사용되는지 로그로 기록한다.
func logMigrationSource(sourceInfo migrationSourceInfo) {
	args := []any{"source", sourceInfo.Kind, "driver", sourceInfo.Driver}
	if sourceInfo.Path != "" {
		args = append(args, "path", sourceInfo.Path)
	}
	slog.Info("running database migrations", args...)
}

// migrationSourceDir resolves and validates the absolute path to the migration directory.
// @intent 주어진 마이그레이션 경로를 절대 경로로 변환하고 해당 디렉토리가 실제 존재하는지 확인한다.
func migrationSourceDir(migrationsDir, driver string) (string, error) {
	dir := filepath.Join(migrationsDir, driver)
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", trace.Wrap(err, "resolve migration directory")
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", trace.Wrap(err, "stat migration directory")
	}
	if !info.IsDir() {
		return "", fmt.Errorf("migration path %q is not a directory", abs)
	}
	return abs, nil
}

// migrateDatabaseDriver initializes the migration driver for the specified database.
// @intent GORM DB 연결을 기반으로 golang-migrate에서 인식할 수 있는 전용 드라이버 인스턴스를 생성한다.
func migrateDatabaseDriver(db *gorm.DB, driver string) (migratedb.Driver, string, error) {
	sqlDB, err := db.DB()
	if err != nil {
		return nil, "", trace.Wrap(err, "get sql database")
	}
	switch driver {
	case "sqlite":
		d, err := migratesqlite3.WithInstance(sqlDB, &migratesqlite3.Config{})
		if err != nil {
			return nil, "", trace.Wrap(err, "create sqlite migration driver")
		}
		return d, "sqlite3", nil
	case "postgres":
		d, err := migratepostgres.WithInstance(sqlDB, &migratepostgres.Config{})
		if err != nil {
			return nil, "", trace.Wrap(err, "create postgres migration driver")
		}
		return d, "postgres", nil
	default:
		return nil, "", fmt.Errorf("unsupported database driver %q", driver)
	}
}

// baselineLegacySchemaVersion handles the transition from older schema versioning to golang-migrate.
// @intent 구버전 스키마 관리 방식을 최신 마이그레이션 도구(golang-migrate)로 안전하게 동기화한다.
// @sideEffect 기존 데이터가 요구 버전과 일치하면 마이그레이션 메타데이터를 강제로 생성한다.
func baselineLegacySchemaVersion(db *gorm.DB, driver string, migrator *gomigrate.Migrate) (bool, error) {
	if db.Migrator().HasTable("schema_migrations") {
		var count int64
		if err := db.Table("schema_migrations").Count(&count).Error; err != nil {
			return false, trace.Wrap(err, "check migrate schema version")
		}
		if count > 0 {
			return false, nil
		}
	}
	if !db.Migrator().HasTable(legacySchemaTable) {
		return false, nil
	}

	var current model.SchemaVersion
	err := db.Table(legacySchemaTable).Where("key = ?", schemaVersionKey).First(&current).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, trace.Wrap(err, "check legacy schema version")
	}
	if current.Version != requiredSchemaVersion {
		return false, nil
	}
	if err := validateSchemaParity(db, driver); err != nil {
		return false, actionableSchemaParityError(trace.Wrap(err, "validate legacy schema parity"))
	}
	if err := migrator.Force(requiredSchemaVersion); err != nil {
		return false, trace.Wrap(err, "baseline legacy schema version")
	}
	return true, nil
}

// schemaColumn identifies a required table/column pair for schema validation.
// @intent 스키마 정합성 검증에서 필수 컬럼을 테이블 단위로 지정하기 위한 식별자를 표현한다.
type schemaColumn struct {
	table  string
	column string
}

// requiredSchemaTables returns the list of tables required for the schema.
// @intent 전체 시스템 동작에 필수적인 데이터베이스 테이블 목록을 정의한다.
func requiredSchemaTables() []string {
	return []string{
		"nodes",
		"edges",
		"annotations",
		"doc_tags",
		"communities",
		"community_memberships",
		"flows",
		"flow_memberships",
		"ccg_postprocess_policy_state",
		"ccg_postprocess_run_logs",
		"search_documents",
	}
}

// modelNullabilityColumns returns the list of columns that must be NOT NULL.
// @intent 데이터 정합성을 위해 NULL이 허용되지 않아야 하는 주요 테이블 및 컬럼 목록을 정의한다.
func modelNullabilityColumns() []schemaColumn {
	return []schemaColumn{
		{"nodes", "qualified_name"},
		{"nodes", "kind"},
		{"nodes", "name"},
		{"nodes", "file_path"},
		{"nodes", "start_line"},
		{"nodes", "end_line"},
		{"edges", "kind"},
		{"edges", "fingerprint"},
		{"communities", "key"},
		{"community_memberships", "community_id"},
		{"community_memberships", "node_id"},
		{"flows", "name"},
		{"flow_memberships", "flow_id"},
		{"flow_memberships", "node_id"},
		{"ccg_postprocess_policy_state", "namespace"},
		{"ccg_postprocess_policy_state", "tool"},
		{"ccg_postprocess_policy_state", "policy"},
		{"ccg_postprocess_policy_state", "updated_at"},
		{"ccg_postprocess_run_logs", "namespace"},
		{"ccg_postprocess_run_logs", "tool"},
		{"ccg_postprocess_run_logs", "policy"},
		{"ccg_postprocess_run_logs", "source"},
		{"ccg_postprocess_run_logs", "status"},
		{"ccg_postprocess_run_logs", "failed_steps"},
		{"ccg_postprocess_run_logs", "skipped_steps"},
		{"ccg_postprocess_run_logs", "created_at"},
	}
}

// validateSchemaParity ensures required tables and constraints exist in the database.
// @intent DB 드라이버별(SQLite, Postgres) 필수 테이블과 컬럼 제약 조건(Not Null 등)의 존재 여부를 검증한다.
func validateSchemaParity(db *gorm.DB, driver string) error {
	for _, table := range requiredSchemaTables() {
		if !db.Migrator().HasTable(table) {
			return fmt.Errorf("required table %q is missing", table)
		}
	}

	switch driver {
	case "sqlite":
		return validateSQLiteSchemaParity(db)
	case "postgres":
		return validatePostgresSchemaParity(db)
	default:
		return fmt.Errorf("unsupported database driver %q", driver)
	}
}

// validateSQLiteSchemaParity ensures required SQLite-specific tables and constraints exist.
// @intent SQLite 환경에서 FTS 테이블 존재 여부와 컬럼 제약 조건(Not Null)을 상세 검증한다.
func validateSQLiteSchemaParity(db *gorm.DB) error {
	if !db.Migrator().HasTable("search_fts") {
		return fmt.Errorf("required table %q is missing", "search_fts")
	}
	hasNamespace, err := sqliteColumnExists(db, "search_fts", "namespace")
	if err != nil {
		return trace.Wrap(err, "inspect sqlite search_fts namespace column")
	}
	if !hasNamespace {
		return fmt.Errorf("required column %q.%q is missing", "search_fts", "namespace")
	}
	for _, column := range modelNullabilityColumns() {
		notNull, err := sqliteColumnNotNull(db, column.table, column.column)
		if err != nil {
			return trace.Wrap(err, "inspect sqlite column nullability")
		}
		if !notNull {
			return fmt.Errorf("required column %q.%q is nullable", column.table, column.column)
		}
	}
	return nil
}

// sqliteColumnExists checks if a column exists in a SQLite table.
// @intent SQLite PRAGMA를 사용하여 특정 테이블에 특정 컬럼이 존재하는지 확인한다.
func sqliteColumnExists(db *gorm.DB, tableName, columnName string) (bool, error) {
	column, err := sqliteColumnInfo(db, tableName, columnName)
	if err != nil {
		return false, err
	}
	return column.exists, nil
}

// sqliteColumnNotNull checks if a column has a NOT NULL constraint in SQLite.
// @intent SQLite PRAGMA를 통해 특정 컬럼에 NOT NULL 제약 조건이 설정되어 있는지 확인한다.
func sqliteColumnNotNull(db *gorm.DB, tableName, columnName string) (bool, error) {
	column, err := sqliteColumnInfo(db, tableName, columnName)
	if err != nil {
		return false, err
	}
	return column.notNull, nil
}

// sqliteColumn stores SQLite column metadata used by PRAGMA inspection.
// @intent SQLite 컬럼 조회 결과에서 존재 여부와 Not Null 제약 조건을 함께 전달한다.
type sqliteColumn struct {
	exists  bool
	notNull bool
}

// sqliteColumnInfo retrieves metadata about a specific column in a SQLite table.
// @intent PRAGMA table_info를 실행하여 SQLite 컬럼의 존재 여부와 Not Null 제약 조건을 일괄 조회한다.
func sqliteColumnInfo(db *gorm.DB, tableName, columnName string) (sqliteColumn, error) {
	rows, err := db.Raw("PRAGMA table_info(" + tableName + ")").Rows()
	if err != nil {
		return sqliteColumn{}, err
	}
	defer rows.Close()

	for rows.Next() {
		var cid int
		var name, ctype string
		var notNull int
		var dflt any
		var pk int
		if err := rows.Scan(&cid, &name, &ctype, &notNull, &dflt, &pk); err != nil {
			return sqliteColumn{}, err
		}
		if name == columnName {
			return sqliteColumn{exists: true, notNull: notNull == 1}, nil
		}
	}
	return sqliteColumn{}, rows.Err()
}

// validatePostgresSchemaParity ensures required Postgres-specific tables and constraints exist.
// @intent Postgres 환경에서 컬럼 제약 조건, GIN 인덱스, 트리거 및 JSONB 타입을 상세 검증한다.
// validatePostgresSchemaParity ensures required Postgres-specific tables and constraints exist.
// @intent Postgres 환경에서 컬럼 제약 조건, GIN 인덱스, 트리거 및 JSONB 타입을 상세 검증한다.
func validatePostgresSchemaParity(db *gorm.DB) error {
	for _, column := range modelNullabilityColumns() {
		notNull, err := postgresColumnNotNull(db, column.table, column.column)
		if err != nil {
			return trace.Wrap(err, "inspect postgres column nullability")
		}
		if !notNull {
			return fmt.Errorf("required column %q.%q is nullable", column.table, column.column)
		}
	}
	if ok, err := postgresIndexExists(db, "idx_search_documents_tsv"); err != nil {
		return trace.Wrap(err, "inspect postgres search index")
	} else if !ok {
		return fmt.Errorf("required index %q is missing", "idx_search_documents_tsv")
	}
	if ok, err := postgresTriggerExists(db, "trg_search_documents_tsv"); err != nil {
		return trace.Wrap(err, "inspect postgres search trigger")
	} else if !ok {
		return fmt.Errorf("required trigger %q is missing", "trg_search_documents_tsv")
	}
	for _, tc := range []schemaTypeCheck{
		{table: "ccg_postprocess_run_logs", column: "failed_steps", dataType: "jsonb"},
		{table: "ccg_postprocess_run_logs", column: "skipped_steps", dataType: "jsonb"},
	} {
		dataType, err := postgresColumnDataType(db, tc.table, tc.column)
		if err != nil {
			return trace.Wrap(err, "inspect postgres column type")
		}
		if dataType != tc.dataType {
			return fmt.Errorf("required column %q.%q type is %q, want %q", tc.table, tc.column, dataType, tc.dataType)
		}
	}
	return nil
}

// schemaTypeCheck defines an expected Postgres column type assertion.
// @intent Postgres 스키마 검증 시 특정 컬럼이 기대하는 물리 타입인지 확인하기 위한 조건을 표현한다.
type schemaTypeCheck struct {
	table    string
	column   string
	dataType string
}

// postgresColumnNotNull checks if a column has a NOT NULL constraint in Postgres.
// @intent information_schema.columns를 조회하여 Postgres 컬럼의 Null 허용 여부를 확인한다.
func postgresColumnNotNull(db *gorm.DB, tableName, columnName string) (bool, error) {
	var nullable string
	err := db.Raw(`
		SELECT is_nullable
		FROM information_schema.columns
		WHERE table_schema = 'public'
		AND table_name = ?
		AND column_name = ?
	`, tableName, columnName).Scan(&nullable).Error
	if err != nil {
		return false, err
	}
	return nullable == "NO", nil
}

// postgresColumnDataType retrieves the data type of a specific column in Postgres.
// @intent information_schema.columns에서 특정 컬럼의 물리적 데이터 타입(예: jsonb)을 조회한다.
func postgresColumnDataType(db *gorm.DB, tableName, columnName string) (string, error) {
	var dataType string
	err := db.Raw(`
		SELECT data_type
		FROM information_schema.columns
		WHERE table_schema = 'public'
		AND table_name = ?
		AND column_name = ?
	`, tableName, columnName).Scan(&dataType).Error
	if err != nil {
		return "", err
	}
	return dataType, nil
}

// postgresIndexExists checks if an index exists in Postgres.
// @intent pg_indexes 시스템 뷰를 조회하여 명시된 인덱스 이름이 존재하는지 확인한다.
func postgresIndexExists(db *gorm.DB, indexName string) (bool, error) {
	var count int64
	err := db.Raw(`
		SELECT COUNT(*)
		FROM pg_indexes
		WHERE schemaname = 'public'
		AND indexname = ?
	`, indexName).Scan(&count).Error
	return count > 0, err
}

// postgresTriggerExists checks if a non-internal trigger exists in Postgres.
// @intent pg_trigger를 조회하여 시스템 내부용이 아닌 사용자 정의 트리거의 존재 여부를 확인한다.
func postgresTriggerExists(db *gorm.DB, triggerName string) (bool, error) {
	var count int64
	err := db.Raw(`
		SELECT COUNT(*)
		FROM pg_trigger
		WHERE tgname = ?
		AND NOT tgisinternal
	`, triggerName).Scan(&count).Error
	return count > 0, err
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
	deps.Logger.Info("starting code-context-graph MCP server")
	tel, err := obs.Setup(context.Background(), obs.Config{
		ServiceName:    "code-context-graph",
		ServiceVersion: version,
		Mode:           "serve",
		Endpoint:       cfg.OTELEndpoint,
		Logger:         deps.Logger,
	})
	if err != nil {
		return trace.Wrap(err, "setup telemetry")
	}
	obs.SetGlobal(tel)
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := tel.Shutdown(shutdownCtx); err != nil {
			deps.Logger.Error("telemetry shutdown failed", "error", err)
		}
		obs.SetGlobal(nil)
	}()

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
		Store:               deps.Store,
		DB:                  deps.DB,
		Parser:              deps.Walkers[".go"],
		Walkers:             mcpWalkers,
		SearchBackend:       deps.SearchBackend,
		ImpactAnalyzer:      impact.New(deps.Store),
		FlowTracer:          flows.New(deps.Store),
		ChangesGitClient:    changes.NewExecGitClient(),
		QueryService:        query.New(deps.DB),
		LargefuncAnalyzer:   largefunc.New(deps.DB),
		DeadcodeAnalyzer:    deadcode.New(deps.DB),
		CouplingAnalyzer:    coupling.New(deps.DB),
		CoverageAnalyzer:    coverage.New(deps.DB),
		CommunityBuilder:    community.New(deps.DB),
		FlowBuilder:         flows.NewBuilder(deps.DB, deps.Store),
		Incremental:         deps.Syncer,
		PostprocessPolicy:   newMCPPostprocessPolicy(deps.DB),
		Logger:              deps.Logger,
		Cache:               cache,
		RagIndexDir:         viper.GetString("rag.index_dir"),
		RagProjectDesc:      viper.GetString("rag.description"),
		NamespaceRoot:       cfg.NamespaceRoot,
		WorkspaceRoot:       cfg.WorkspaceRoot,
		RepoRoot:            cfg.RepoRoot,
		MaxFileBytes:        cfg.MaxFileBytes,
		MaxTotalParsedBytes: cfg.MaxTotalParsedBytes,
	}
	postprocessSummary := func(ctx context.Context) (*postprocesspolicy.StatusSummary, error) {
		if mcpDeps.PostprocessPolicy == nil {
			return nil, nil
		}
		return mcpDeps.PostprocessPolicy.Status(ctx, postprocesspolicy.StatusOptions{RecentLimit: postprocesspolicy.DefaultStatusLimit})
	}

	srv := mcpserver.NewServer(mcpDeps)

	switch cfg.Transport {
	case "streamable-http":
		return serveStreamableHTTP(deps, srv, cfg, cache, postprocessSummary)
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

// flushMCPQueryCache clears the MCP query cache if it exists.
// @intent MCP 서버 캐시를 비워 새로운 분석 결과가 반영되도록 한다.
func flushMCPQueryCache(cache *mcpserver.Cache) {
	if cache != nil {
		cache.Flush()
	}
}

// mcpPostprocessPolicy manages post-processing policies for the MCP server.
// @intent MCP 서버에서 실행되는 후처리 작업(flows, communities 등)의 정책과 상태를 관리한다.
type mcpPostprocessPolicy struct {
	engine *postprocesspolicy.Engine
	store  *postprocesspolicy.Store
}

// newMCPPostprocessPolicy creates a new mcpPostprocessPolicy.
// @intent 데이터베이스 연결을 사용하는 MCP용 후처리 정책 엔진을 초기화한다.
func newMCPPostprocessPolicy(db *gorm.DB) *mcpPostprocessPolicy {
	if db == nil {
		return nil
	}
	return &mcpPostprocessPolicy{
		engine: &postprocesspolicy.Engine{},
		store:  postprocesspolicy.NewStore(db),
	}
}

// Resolve decides the policy for a given tool and input.
// @intent 주어진 입력 정보를 바탕으로 특정 후처리 도구의 실행 정책을 결정한다.
func (p *mcpPostprocessPolicy) Resolve(ctx context.Context, input postprocesspolicy.DecisionInput) (string, string, error) {
	return p.engine.Resolve(ctx, p.store, input)
}

// RecordRun logs the results of a post-processing run.
// @intent 후처리 작업의 실행 결과를 기록하여 이력을 관리한다.
func (p *mcpPostprocessPolicy) RecordRun(ctx context.Context, record postprocesspolicy.RunRecord) error {
	return p.store.RecordRun(ctx, record)
}

// Status returns the current status summary of post-processing.
// @intent 전체 후처리 엔진의 상태 요약 정보(성공, 실패, 지연 등)를 가져온다.
func (p *mcpPostprocessPolicy) Status(ctx context.Context, opts postprocesspolicy.StatusOptions) (*postprocesspolicy.StatusSummary, error) {
	return p.store.Status(ctx, opts)
}

// Reset clears the state of a specific post-processing tool.
// @intent 특정 도구의 후처리 상태를 초기화하여 다시 실행될 수 있게 한다.
func (p *mcpPostprocessPolicy) Reset(ctx context.Context, tool string) error {
	return p.store.Reset(ctx, tool)
}

// serveStreamableHTTP serves the MCP server over streamable HTTP.
// @intent 원격 MCP 클라이언트를 위한 HTTP 엔드포인트와 헬스체크를 노출한다.
// @sideEffect HTTP 리스너를 열고 종료 시 graceful shutdown을 수행한다.
func serveStreamableHTTP(deps *cli.Deps, srv *server.MCPServer, cfg cli.ServeConfig, cache *mcpserver.Cache, postprocessSummary func(context.Context) (*postprocesspolicy.StatusSummary, error)) error {
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
	mux.Handle("/mcp", mcpAuthMiddleware(cfg.HTTPBearerToken, withHTTPTraceContext(mcpserver.LimitHTTPBody(httpSrv))))
	mux.HandleFunc("/health", handleHealth)
	dbReadyCheck := func(r *http.Request) error {
		if deps.DB == nil {
			return fmt.Errorf("database not configured")
		}
		sqlDB, err := deps.DB.DB()
		if err != nil {
			return trace.Wrap(err, "get sql db")
		}
		return sqlDB.PingContext(r.Context())
	}

	var syncQueue *webhook.SyncQueue
	syncCtx, syncCancel := context.WithCancel(context.Background())
	var syncCleanupOnce sync.Once
	cleanupSyncQueue := func() {
		syncCleanupOnce.Do(func() {
			syncCancel()
			if syncQueue != nil {
				deps.Logger.Info("cancelling sync context and draining workers")
				syncQueue.Shutdown()
			}
		})
	}
	defer cleanupSyncQueue()

	mux.Handle("/ready", readyHandler(func(r *http.Request) error {
		if err := dbReadyCheck(r); err != nil {
			return err
		}
		if err := webhookBlockingReadyCheck(syncQueue, cfg.WebhookAttemptTimeout); err != nil {
			return err
		}
		return nil
	}))
	mux.Handle("/status", statusHandler(dbReadyCheck, cfg.WebhookAttemptTimeout, func() *webhook.SyncQueue {
		return syncQueue
	}, postprocessSummary))

	if len(cfg.AllowRepo) > 0 {
		rules := make([]webhook.RepoRule, 0, len(cfg.AllowRepo))
		for _, s := range cfg.AllowRepo {
			rules = append(rules, webhook.ParseRepoRule(s))
		}
		filter := webhook.NewRepoFilterFromRules(rules)
		secret := []byte(cfg.WebhookSecret)
		repoLocker := webhook.NewRepoLocker(30 * time.Second)
		syncHandler := func(ctx context.Context, repoFullName, cloneURL, branch string) error {
			ctx, span := obs.StartSpan(ctx, "webhook.sync", attribute.String("repo.full_name", repoFullName), attribute.String("git.branch", branch))
			defer span.End()
			ns := webhook.ExtractNamespace(repoFullName)
			deps.Logger.InfoContext(ctx, "webhook sync started", append(obs.TraceLogArgs(ctx), "repo", repoFullName, "namespace", ns, "branch", branch)...)

			attemptCtx, attemptCancel := context.WithTimeout(ctx, cfg.WebhookAttemptTimeout)
			defer attemptCancel()

			if err := webhook.CloneOrPullBranchLocked(attemptCtx, repoLocker, cloneURL, cfg.RepoRoot, repoFullName, ns, branch, nil); err != nil {
				deps.Logger.ErrorContext(attemptCtx, "webhook clone/pull failed", append(obs.TraceLogArgs(attemptCtx), "repo", repoFullName, "namespace", ns, "branch", branch, "error", err)...)
				return err
			}

			repoDir := webhook.RepoDir(cfg.RepoRoot, ns)
			includePaths, err := pathutil.LoadIncludePathsFromConfig(repoDir)
			if err != nil {
				deps.Logger.ErrorContext(attemptCtx, "webhook include_paths config invalid", append(obs.TraceLogArgs(attemptCtx), "repo", repoFullName, "namespace", ns, "branch", branch, "error", err)...)
				return webhook.NonRetryable(err)
			}
			graphSvc := &service.GraphService{
				Store:         deps.Store,
				DB:            deps.DB,
				SearchBackend: deps.SearchBackend,
				Walkers:       deps.Walkers,
				Logger:        deps.Logger,
			}
			buildCtx := ctxns.WithNamespace(attemptCtx, ns)
			stats, err := graphSvc.Update(buildCtx, service.UpdateOptions{
				BuildOptions: service.BuildOptions{
					Dir:                 repoDir,
					IncludePaths:        includePaths,
					MaxFileBytes:        cfg.MaxFileBytes,
					MaxTotalParsedBytes: cfg.MaxTotalParsedBytes,
				},
				Syncer:           deps.Syncer,
				Replace:          true,
				FailOnUnreadable: cfg.WebhookFailOnUnreadable,
			})
			if err != nil {
				deps.Logger.ErrorContext(attemptCtx, "webhook update failed", append(obs.TraceLogArgs(attemptCtx), "repo", repoFullName, "namespace", ns, "branch", branch, "error", err)...)
				return err
			}
			flushMCPQueryCache(cache)
			deps.Logger.InfoContext(attemptCtx, "webhook sync completed", append(obs.TraceLogArgs(attemptCtx), "repo", repoFullName, "namespace", ns,
				"added", stats.Added, "modified", stats.Modified, "skipped", stats.Skipped, "deleted", stats.Deleted)...)
			return nil
		}
		syncQueue = webhook.NewSyncQueueWithConfig(syncCtx, cfg.WebhookWorkers, syncHandler, webhook.QueueConfig{
			RetryConfig: webhook.RetryConfig{
				MaxAttempts: cfg.WebhookRetryAttempts,
				BaseDelay:   cfg.WebhookRetryBaseDelay,
				MaxDelay:    cfg.WebhookRetryMaxDelay,
			},
			MaxTrackedRepos: cfg.WebhookMaxTrackedRepos,
		})
		mux.Handle("/webhook", webhook.NewWebhookHandlerWithConfig(webhook.WebhookHandlerConfig{
			Secret:        secret,
			Filter:        filter,
			OnSync:        syncQueue.Add,
			Insecure:      cfg.InsecureWebhook,
			CloneBaseURLs: cfg.RepoCloneBaseURLs,
		}))
		deps.Logger.Info("webhook endpoint registered", "path", "/webhook", "allowedRepos", cfg.AllowRepo)
	}

	httpServer := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      0,
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
		cleanupSyncQueue()
		return nil
	}
}

// validateHTTPExposure ensures non-loopback streamable-http requires authentication.
// @intent HTTP 노출 시 루프백 주소가 아닌 경우 반드시 인증 토큰 설정을 강제하여 보안을 강화한다.
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

// mcpAuthMiddleware provides bearer token authentication for MCP HTTP endpoints.
// @intent 설정된 Bearer 토큰을 검증하여 허가되지 않은 사용자의 MCP 접근을 차단한다.
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

func withHTTPTraceContext(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := obs.ContextWithHTTPTrace(r.Context(), r.Header)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// validBearerToken validates a bearer token against an expected value.
// @intent HTTP Authorization 헤더의 Bearer 토큰이 기대하는 값과 일치하는지 상수 시간 비교로 검증한다.
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

// isLoopbackHTTPAddr checks if an address is a loopback address.
// @intent 주어진 HTTP 주소가 로컬 루프백(localhost, 127.0.0.1 등) 주소인지 판별한다.
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

// readyHandler handles HTTP ready checks.
// @intent 시스템이 실제로 요청을 처리할 수 있는 상태(DB 연결, 웹훅 큐 가용성 등)인지 확인하는 엔드포인트를 제공한다.
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

// statusResponse defines the response structure for the status endpoint.
// @intent 시스템의 전반적인 상태 요약(DB, 웹훅, 후처리 정보)을 포함한 응답 구조를 정의한다.
type statusResponse struct {
	Status      string                           `json:"status"`
	DB          string                           `json:"db"`
	Webhook     *webhook.SyncQueueStats          `json:"webhook,omitempty"`
	Postprocess *postprocesspolicy.StatusSummary `json:"postprocess,omitempty"`
}

// statusHandler provides detailed system status including DB, webhooks, and post-processing.
// @intent 시스템의 전반적인 상태(DB 연결, 웹훅 큐 통계, 후처리 엔진 요약)를 JSON 형태로 제공한다.
// @domainRule 상태가 정상이 아니거나 지연 발생 시 HTTP 503 또는 degraded 상태를 반환한다.
func statusHandler(dbCheck func(*http.Request) error, webhookTimeout time.Duration, queue func() *webhook.SyncQueue, postprocessSummary func(context.Context) (*postprocesspolicy.StatusSummary, error)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		resp := statusResponse{Status: "ok", DB: "ready"}
		code := http.StatusOK
		if err := dbCheck(r); err != nil {
			resp.Status = "not_ready"
			resp.DB = "not_ready"
			code = http.StatusServiceUnavailable
		}
		if queue != nil {
			if q := queue(); q != nil {
				stats := q.Stats()
				resp.Webhook = &stats
				if err := webhookStatsBlockingReady(stats, webhookTimeout); err != nil {
					resp.Status = "not_ready"
					code = http.StatusServiceUnavailable
				} else if code == http.StatusOK && webhookStatsDegraded(stats) {
					resp.Status = "degraded"
				}
			}
		}
		if postprocessSummary != nil {
			summary, err := postprocessSummary(r.Context())
			if err == nil {
				resp.Postprocess = summary
				if code == http.StatusOK && summary != nil && summary.Status == postprocesspolicy.StatusDegraded {
					resp.Status = "degraded"
				}
			} else {
				slog.Error("postprocess status summary failed", "error", err)
			}
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			slog.Error("status check write failed", "error", err)
		}
	})
}

// webhookBlockingReadyCheck checks if the webhook queue is blocked.
// @intent 웹훅 큐의 상태를 확인하여 시스템이 요청을 받을 수 있는지 판별한다.
func webhookBlockingReadyCheck(q *webhook.SyncQueue, timeout time.Duration) error {
	if q == nil {
		return nil
	}
	return webhookStatsBlockingReady(q.Stats(), timeout)
}

// webhookStatsBlockingReady checks if the webhook stats indicate a blocked state.
// @intent 웹훅 통계 수치를 바탕으로 큐 지연이나 초과 상태를 확인하여 서비스 불가 여부를 결정한다.
func webhookStatsBlockingReady(stats webhook.SyncQueueStats, timeout time.Duration) error {
	if stats.MaxTrackedRepos > 0 && stats.TrackedRepos >= stats.MaxTrackedRepos {
		return fmt.Errorf("webhook sync queue full")
	}
	if timeout > 0 {
		if stats.OldestQueuedAge > timeout {
			return fmt.Errorf("webhook sync queue delayed for %s", stats.OldestQueuedAge)
		}
		if stats.OldestProcessingAge > timeout {
			return fmt.Errorf("webhook sync processing delayed for %s", stats.OldestProcessingAge)
		}
	}
	return nil
}

// webhookStatsDegraded checks if the webhook stats indicate a degraded state.
// @intent 최근 웹훅 처리 실패 이력이 성공 이력보다 최신인 경우를 찾아 시스템 상태 저하를 판별한다.
func webhookStatsDegraded(stats webhook.SyncQueueStats) bool {
	if !stats.LastErrorTime.IsZero() && (stats.LastSuccessTime.IsZero() || stats.LastSuccessTime.Before(stats.LastErrorTime)) {
		return true
	}
	for _, repo := range stats.RecentRepos {
		if webhookRepoStatsDegraded(repo) {
			return true
		}
	}
	return false
}

// webhookRepoStatsDegraded checks if a specific repo's stats indicate a degraded state.
// @intent 특정 저장소 단위에서 최근 처리 결과가 실패인지 확인하여 상태 저하 여부를 결정한다.
func webhookRepoStatsDegraded(stats webhook.RepoStats) bool {
	return !stats.LastErrorTime.IsZero() && (stats.LastSuccessTime.IsZero() || stats.LastSuccessTime.Before(stats.LastErrorTime))
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
	configureDBPool(sqlDB, driver)

	return db, nil
}

// sqlDBPool abstracts the pool tuning methods used for sql.DB.
// @intent 드라이버별 연결 풀 설정을 테스트 가능한 최소 인터페이스로 추상화한다.
type sqlDBPool interface {
	SetMaxOpenConns(int)
	SetMaxIdleConns(int)
	SetConnMaxLifetime(time.Duration)
	SetConnMaxIdleTime(time.Duration)
}

// configureDBPool applies driver-specific SQL connection pool settings.
// @intent SQLite와 Postgres의 특성에 맞는 연결 풀 크기와 수명 정책을 일관되게 적용한다.
func configureDBPool(sqlDB sqlDBPool, driver string) {
	if driver == "sqlite" {
		sqlDB.SetMaxOpenConns(1)
		sqlDB.SetMaxIdleConns(1)
		sqlDB.SetConnMaxLifetime(0)
		sqlDB.SetConnMaxIdleTime(0)
		return
	}

	sqlDB.SetMaxOpenConns(25)
	sqlDB.SetMaxIdleConns(5)
	sqlDB.SetConnMaxLifetime(5 * time.Minute)
	sqlDB.SetConnMaxIdleTime(5 * time.Minute)
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
