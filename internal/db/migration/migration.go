package migration

import (
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	gomigrate "github.com/golang-migrate/migrate/v4"
	migratedb "github.com/golang-migrate/migrate/v4/database"
	migratepostgres "github.com/golang-migrate/migrate/v4/database/postgres"
	migratesqlite3 "github.com/golang-migrate/migrate/v4/database/sqlite3"
	"github.com/golang-migrate/migrate/v4/source"
	"github.com/golang-migrate/migrate/v4/source/file"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/tae2089/code-context-graph/internal/ctxns"
	"github.com/tae2089/code-context-graph/internal/migrationfs"
	"github.com/tae2089/code-context-graph/internal/model"
	"github.com/tae2089/trace"
	"gorm.io/gorm"
)

const (
	RequiredSchemaVersion    = 5
	SchemaVersionKey         = "schema"
	LegacySchemaVersionTable = "ccg_schema_versions"
)

// LegacyBaselineFunc performs legacy-schema migration fallback during normal migration.
// @intent 일반 마이그레이션 전에 legacy 스키마를 현재 메타데이터 체계에 정렬하는 훅 계약을 정의한다.
type LegacyBaselineFunc func(db *gorm.DB, migrator *gomigrate.Migrate, driver string) error

// ValidateSchemaParityFunc validates required tables and constraints for a driver.
// @intent 드라이버별 스키마 정합성 검사를 주입 가능한 함수 계약으로 분리한다.
type ValidateSchemaParityFunc func(db *gorm.DB, driver string) error

// SourceInfo describes how migration files are sourced.
// @intent 마이그레이션 파일이 embedded인지 external인지와 사용 드라이버를 함께 기록한다.
type SourceInfo struct {
	Kind   string
	Driver string
	Path   string
}

// MigrationSchemaVersion mirrors a row from schema_migrations table.
// @intent golang-migrate 메타데이터 테이블을 런타임 검증에서 읽을 수 있게 한다.
type MigrationSchemaVersion struct {
	Version uint `gorm:"column:version"`
	Dirty   bool `gorm:"column:dirty"`
}

// SchemaColumn represents a required table/column pair.
// @intent 런타임 스키마 검증에서 필수 컬럼 목록을 표준 구조로 표현한다.
type SchemaColumn struct {
	Table  string
	Column string
}

// SchemaTypeCheck represents a required column type assertion.
// @intent 특정 컬럼의 데이터 타입까지 검증해야 하는 조건을 표현한다.
type SchemaTypeCheck struct {
	Table    string
	Column   string
	DataType string
}

// Run executes all pending migrations and validates schema parity.
// @intent 마이그레이션 실행과 사후 스키마 정합성 검사를 하나의 진입점으로 묶는다.
// @sideEffect 데이터베이스 스키마와 migration metadata를 변경할 수 있다.
func Run(db *gorm.DB, driver, migrationsDir string, baseline LegacyBaselineFunc, validateSchemaParity ValidateSchemaParityFunc) error {
	migrator, sourceInfo, err := NewMigrator(db, driver, migrationsDir)
	if err != nil {
		return err
	}
	logMigrationSource(sourceInfo)

	if baseline != nil {
		if err := baseline(db, migrator, driver); err != nil {
			return err
		}
	}
	err = migrator.Up()
	if err != nil && !errors.Is(err, gomigrate.ErrNoChange) {
		return trace.Wrap(err, "run database migrations")
	}
	if validateSchemaParity != nil {
		if err := validateSchemaParity(db, driver); err != nil {
			return actionableSchemaParityError(err)
		}
	}
	return nil
}

// RunMigrations executes application migrations with legacy schema baseline handling.
// @intent 애플리케이션 기본 마이그레이션 경로에 legacy baseline 로직을 포함시킨다.
func RunMigrations(db *gorm.DB, driver, migrationsDir string) error {
	return Run(
		db,
		driver,
		migrationsDir,
		func(db *gorm.DB, migrator *gomigrate.Migrate, legacyDriver string) error {
			_, err := BaselineLegacySchemaVersion(
				db,
				migrator,
				legacyDriver,
				SchemaVersionKey,
				LegacySchemaVersionTable,
				RequiredSchemaVersion,
			)
			return err
		},
		ValidateSchemaParity,
	)
}

// EnsureSchemaVersion prepares the database for runtime use, including optional auto-migration.
// @intent 런타임 명령이 시작되기 전에 스키마 버전과 자동 마이그레이션 조건을 검증한다.
// @domainRule 기본 로컬 sqlite + 비초기화 상태일 때만 자동 마이그레이션을 허용한다.
func EnsureSchemaVersion(db *gorm.DB, driver, dsn, migrationsDir string) error {
	if err := CheckSchemaVersion(db, RequiredSchemaVersion); err == nil {
		return ValidateSchemaForRuntime(db, driver, false)
	}

	if !ShouldAutoMigrateLocalSQLite(driver, dsn) || db.Migrator().HasTable("schema_migrations") {
		if err := CheckSchemaVersion(db, RequiredSchemaVersion); err != nil {
			return err
		}
		return ValidateSchemaForRuntime(db, driver, false)
	}

	if err := RunMigrations(db, driver, migrationsDir); err != nil {
		return trace.Wrap(err, "auto-migrate local sqlite database")
	}
	if err := CheckSchemaVersion(db, RequiredSchemaVersion); err != nil {
		return err
	}
	return ValidateSchemaForRuntime(db, driver, true)
}

// ValidateSchemaForRuntime validates schema parity and logs actionable results.
// @intent 런타임 시작 전에 스키마 이상을 운영 로그와 함께 명확히 보고한다.
// @sideEffect 성공/실패 결과를 slog에 기록한다.
func ValidateSchemaForRuntime(db *gorm.DB, driver string, autoMigrated bool) error {
	if err := ValidateSchemaParity(db, driver); err != nil {
		wrapped := ActionableSchemaParityError(err)
		slog.Error("database runtime schema check failed", "driver", driver, "required_version", RequiredSchemaVersion, "auto_migrated", autoMigrated, trace.SlogError(wrapped))
		return wrapped
	}
	slog.Info("database runtime schema check passed", "driver", driver, "required_version", RequiredSchemaVersion, "auto_migrated", autoMigrated)
	return nil
}

// ShouldAutoMigrateLocalSQLite determines whether SQLite should auto-migrate for this DSN.
// @intent 자동 마이그레이션을 기본 로컬 ccg.db 같은 안전한 sqlite 경로로만 제한한다.
// @domainRule 메모리 DB나 커스텀 파일명은 자동 마이그레이션 대상이 아니다.
func ShouldAutoMigrateLocalSQLite(driver, dsn string) bool {
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

// NewMigrator creates a new golang-migrate instance.
// @intent GORM DB와 migration source를 golang-migrate 실행 인스턴스로 결합한다.
func NewMigrator(db *gorm.DB, driver, migrationsDir string) (*gomigrate.Migrate, SourceInfo, error) {
	databaseDriver, databaseName, err := migrateDatabaseDriver(db, driver)
	if err != nil {
		return nil, SourceInfo{}, err
	}

	sourceDriver, sourceName, sourceInfo, err := migrateSourceDriver(driver, migrationsDir)
	if err != nil {
		return nil, SourceInfo{}, err
	}
	migrator, err := gomigrate.NewWithInstance(sourceName, sourceDriver, databaseName, databaseDriver)
	if err != nil {
		return nil, SourceInfo{}, trace.Wrap(err, "create migrator")
	}
	return migrator, sourceInfo, nil
}

// migrateSourceDriver resolves the migration source implementation from embedded or external files.
// @intent 드라이버별 마이그레이션 입력을 source.Driver로 변환해 migrator 생성에 넘긴다.
func migrateSourceDriver(driver, migrationsDir string) (source.Driver, string, SourceInfo, error) {
	sourceInfo, err := migrationSourceInfoFor(driver, migrationsDir)
	if err != nil {
		return nil, "", SourceInfo{}, err
	}
	if sourceInfo.Kind == "embedded" {
		d, err := iofs.New(migrationfs.FS, driver)
		if err != nil {
			return nil, "", SourceInfo{}, trace.Wrap(err, "create embedded migration source")
		}
		return d, "iofs", sourceInfo, nil
	}
	sourceURL := (&url.URL{Scheme: "file", Path: sourceInfo.Path}).String()
	d, err := (&file.File{}).Open(sourceURL)
	if err != nil {
		return nil, "", SourceInfo{}, trace.Wrap(err, "create file migration source")
	}
	return d, "file", sourceInfo, nil
}

// migrationSourceInfoFor decides whether to use embedded migrations or a custom directory.
// @intent 마이그레이션 디렉터리 설정값을 source kind와 경로 정보로 정규화한다.
func migrationSourceInfoFor(driver, migrationsDir string) (SourceInfo, error) {
	if strings.TrimSpace(migrationsDir) == "" {
		return SourceInfo{Kind: "embedded", Driver: driver}, nil
	}
	dir, err := migrationSourceDir(migrationsDir, driver)
	if err != nil {
		return SourceInfo{}, err
	}
	return SourceInfo{Kind: "external", Driver: driver, Path: dir}, nil
}

// SourceInfoFor returns the resolved migration source for a driver.
// @intent 운영 코드와 테스트가 실제 마이그레이션 소스 결정을 외부에서 확인하게 한다.
func SourceInfoFor(driver, migrationsDir string) (SourceInfo, error) {
	return migrationSourceInfoFor(driver, migrationsDir)
}

// logMigrationSource records which migration source path or mode is being used.
// @intent 운영 로그에서 embedded/external 마이그레이션 경로를 즉시 확인할 수 있게 한다.
// @sideEffect slog에 source kind와 path를 기록한다.
func logMigrationSource(sourceInfo SourceInfo) {
	args := []any{"source", sourceInfo.Kind, "driver", sourceInfo.Driver}
	if sourceInfo.Path != "" {
		args = append(args, "path", sourceInfo.Path)
	}
	slog.Info("running database migrations", args...)
}

// migrationSourceDir resolves and validates the on-disk migration directory for one driver.
// @intent 외부 migration source가 존재하는 실제 디렉터리인지 확인한다.
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

// migrateDatabaseDriver converts a GORM DB handle into the driver-specific golang-migrate adapter.
// @intent sqlite/postgres별 migration driver를 생성해 golang-migrate에 연결한다.
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

// actionableSchemaParityError adds operator guidance to raw schema parity failures.
// @intent 스키마 정합성 오류에 즉시 실행할 운영 조치를 함께 붙인다.
func actionableSchemaParityError(err error) error {
	return fmt.Errorf("database schema parity check failed: %w; run `ccg migrate`; if already migrated, verify migration source and schema drift", err)
}

// ActionableSchemaParityError wraps a schema-parity error with recommended remediation steps.
// @intent 외부 호출자도 동일한 운영 지침 메시지를 재사용하게 한다.
func ActionableSchemaParityError(err error) error {
	return actionableSchemaParityError(err)
}

// CheckSchemaVersion verifies the current schema migration metadata and dirty state.
// @intent schema_migrations 메타데이터가 현재 바이너리의 요구 버전과 호환되는지 확인한다.
// @domainRule dirty migration 상태는 런타임 시작 전에 반드시 실패시킨다.
func CheckSchemaVersion(db *gorm.DB, requiredSchemaVersion int) error {
	if !db.Migrator().HasTable("schema_migrations") {
		return fmt.Errorf("database schema is not initialized; run `ccg migrate` first")
	}

	var current MigrationSchemaVersion
	err := db.Table("schema_migrations").First(&current).Error
	if err != nil {
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

// BaselineLegacySchemaVersion aligns legacy schema versions to golang-migrate metadata.
// @intent legacy 버전 테이블만 있는 배포를 golang-migrate 메타데이터로 안전하게 기준선 맞춤한다.
// @domainRule legacy 버전과 실제 스키마 정합성이 모두 맞을 때만 baseline force를 수행한다.
func BaselineLegacySchemaVersion(db *gorm.DB, migrator *gomigrate.Migrate, driver, schemaVersionKey, legacySchemaTable string, requiredSchemaVersion int) (bool, error) {
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
		return false, trace.Wrap(err, "check legacy schema version")
	}
	if current.Version != requiredSchemaVersion {
		return false, nil
	}
	if err := ValidateSchemaParity(db, driver); err != nil {
		return false, actionableSchemaParityError(trace.Wrap(err, "validate legacy schema parity"))
	}
	if err := migrator.Force(requiredSchemaVersion); err != nil {
		return false, trace.Wrap(err, "baseline legacy schema version")
	}
	return true, nil
}

// ValidateSchemaParity ensures required tables and constraints exist in the database.
// @intent 런타임이 의존하는 테이블, 컬럼, 인덱스, 트리거가 모두 준비됐는지 검증한다.
func ValidateSchemaParity(db *gorm.DB, driver string) error {
	for _, table := range RequiredSchemaTables() {
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

// RequiredSchemaTables lists every core table that must exist before runtime commands proceed.
// @intent 스키마 정합성 검사가 공통으로 확인할 필수 테이블 집합을 제공한다.
func RequiredSchemaTables() []string {
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

// ModelNullabilityColumns enumerates columns that must remain NOT NULL for model invariants.
// @intent 주요 모델 필드의 nullable drift를 런타임 검증에서 감지한다.
func ModelNullabilityColumns() []SchemaColumn {
	return []SchemaColumn{
		{Table: "nodes", Column: "qualified_name"},
		{Table: "nodes", Column: "kind"},
		{Table: "nodes", Column: "name"},
		{Table: "nodes", Column: "file_path"},
		{Table: "nodes", Column: "start_line"},
		{Table: "nodes", Column: "end_line"},
		{Table: "edges", Column: "kind"},
		{Table: "edges", Column: "fingerprint"},
		{Table: "communities", Column: "key"},
		{Table: "community_memberships", Column: "community_id"},
		{Table: "community_memberships", Column: "node_id"},
		{Table: "flows", Column: "name"},
		{Table: "flow_memberships", Column: "flow_id"},
		{Table: "flow_memberships", Column: "node_id"},
		{Table: "ccg_postprocess_policy_state", Column: "namespace"},
		{Table: "ccg_postprocess_policy_state", Column: "tool"},
		{Table: "ccg_postprocess_policy_state", Column: "policy"},
		{Table: "ccg_postprocess_policy_state", Column: "updated_at"},
		{Table: "ccg_postprocess_run_logs", Column: "namespace"},
		{Table: "ccg_postprocess_run_logs", Column: "tool"},
		{Table: "ccg_postprocess_run_logs", Column: "policy"},
		{Table: "ccg_postprocess_run_logs", Column: "source"},
		{Table: "ccg_postprocess_run_logs", Column: "status"},
		{Table: "ccg_postprocess_run_logs", Column: "failed_steps"},
		{Table: "ccg_postprocess_run_logs", Column: "skipped_steps"},
		{Table: "ccg_postprocess_run_logs", Column: "created_at"},
	}
}

// MigrateLegacyDefaultNamespace backfills legacy empty namespace rows to the default namespace.
// @intent namespace 도입 이전 데이터셋을 기본 namespace로 올려 현재 모델과 호환시킨다.
// @sideEffect nodes, edges, search_documents, communities, flows의 namespace를 갱신한다.
func MigrateLegacyDefaultNamespace(db *gorm.DB) error {
	return db.Transaction(func(tx *gorm.DB) error {
		if err := failOnLegacyNamespaceCollisions(tx); err != nil {
			return err
		}

		updates := []struct {
			model any
		}{
			{model: &model.Node{}},
			{model: &model.Edge{}},
			{model: &model.SearchDocument{}},
			{model: &model.Community{}},
			{model: &model.Flow{}},
			{model: &model.FlowMembership{}},
		}

		for _, update := range updates {
			if err := tx.Model(update.model).Where("namespace = ?", "").Update("namespace", ctxns.DefaultNamespace).Error; err != nil {
				return trace.Wrap(err, "backfill namespace")
			}
		}

		return nil
	})
}

// failOnLegacyNamespaceCollisions aborts namespace backfill when legacy rows would collide with current default rows.
// @intent 빈 namespace 데이터를 default namespace로 올리기 전에 중복 키 충돌을 차단한다.
func failOnLegacyNamespaceCollisions(db *gorm.DB) error {
	// nodeCollision identifies a node row that would collide during namespace backfill.
	// @intent namespace 마이그레이션 충돌 리포트를 위한 최소 노드 식별자를 담는다.
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

	// edgeCollision identifies an edge fingerprint collision during namespace backfill.
	// @intent edge namespace 병합 시 fingerprint 충돌만 간단히 전달한다.
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

	// searchDocCollision identifies a search document collision by node ID.
	// @intent search_documents namespace 병합 시 중복되는 node_id를 보고한다.
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

	// communityCollision identifies a community key collision during namespace backfill.
	// @intent community namespace 병합 시 key 충돌을 보고한다.
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

// validateSQLiteSchemaParity checks SQLite-only runtime guarantees such as FTS table shape and NOT NULL columns.
// @intent SQLite 배포에서 FTS5 스키마와 모델 nullability 불변식을 확인한다.
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
	for _, column := range ModelNullabilityColumns() {
		notNull, err := sqliteColumnNotNull(db, column.Table, column.Column)
		if err != nil {
			return trace.Wrap(err, "inspect sqlite column nullability")
		}
		if !notNull {
			return fmt.Errorf("required column %q.%q is nullable", column.Table, column.Column)
		}
	}
	for _, indexName := range []string{"idx_edges_ns_from_kind_to", "idx_edges_ns_to_kind_from"} {
		exists, err := sqliteIndexExists(db, indexName)
		if err != nil {
			return trace.Wrap(err, "inspect sqlite edge index")
		}
		if !exists {
			return fmt.Errorf("required index %q is missing", indexName)
		}
	}
	return nil
}

// validatePostgresSchemaParity checks PostgreSQL-only indexes, triggers, and JSONB column types.
// @intent PostgreSQL 검색/후처리 스키마가 운영 계약과 일치하는지 확인한다.
func validatePostgresSchemaParity(db *gorm.DB) error {
	for _, column := range ModelNullabilityColumns() {
		notNull, err := postgresColumnNotNull(db, column.Table, column.Column)
		if err != nil {
			return trace.Wrap(err, "inspect postgres column nullability")
		}
		if !notNull {
			return fmt.Errorf("required column %q.%q is nullable", column.Table, column.Column)
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
	for _, tc := range []SchemaTypeCheck{
		{Table: "ccg_postprocess_run_logs", Column: "failed_steps", DataType: "jsonb"},
		{Table: "ccg_postprocess_run_logs", Column: "skipped_steps", DataType: "jsonb"},
	} {
		dataType, err := postgresColumnDataType(db, tc.Table, tc.Column)
		if err != nil {
			return trace.Wrap(err, "inspect postgres column type")
		}
		if dataType != tc.DataType {
			return fmt.Errorf("required column %q.%q type is %q, want %q", tc.Table, tc.Column, dataType, tc.DataType)
		}
	}
	for _, indexName := range []string{"idx_edges_ns_from_kind_to", "idx_edges_ns_to_kind_from"} {
		exists, err := postgresIndexExists(db, indexName)
		if err != nil {
			return trace.Wrap(err, "inspect postgres edge index")
		}
		if !exists {
			return fmt.Errorf("required index %q is missing", indexName)
		}
	}
	return nil
}

// sqliteColumnExists reports whether a SQLite table exposes the requested column.
// @intent SQLite PRAGMA 메타데이터를 공통 컬럼 존재 검증에 재사용한다.
func sqliteColumnExists(db *gorm.DB, tableName, columnName string) (bool, error) {
	column, err := sqliteColumnInfo(db, tableName, columnName)
	if err != nil {
		return false, err
	}
	return column.exists, nil
}

// sqliteColumnNotNull reports whether a SQLite column is marked NOT NULL.
// @intent SQLite 컬럼 nullability를 런타임 스키마 검증에 재사용한다.
func sqliteColumnNotNull(db *gorm.DB, tableName, columnName string) (bool, error) {
	column, err := sqliteColumnInfo(db, tableName, columnName)
	if err != nil {
		return false, err
	}
	return column.notNull, nil
}

// sqliteColumn caches whether a SQLite column exists and whether it is NOT NULL.
// @intent PRAGMA 결과를 helper 간에 재사용할 최소 내부 표현을 제공한다.
type sqliteColumn struct {
	exists  bool
	notNull bool
}

// sqliteIndexExists reports whether a SQLite index is present by name.
// @intent index presence can be verified during schema parity checks before query paths use them.
func sqliteIndexExists(db *gorm.DB, indexName string) (bool, error) {
	var count int64
	err := db.Raw("SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = ?", indexName).Scan(&count).Error
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// sqliteColumnInfo loads one SQLite column's metadata from PRAGMA table_info.
// @intent SQLite 컬럼 존재 여부와 NOT NULL 속성을 한 번에 조회한다.
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

// SQLiteColumnExists checks if a column exists in a SQLite table.
// @intent 외부 호출자가 SQLite 컬럼 존재 여부를 내부 helper 재사용으로 확인하게 한다.
func SQLiteColumnExists(db *gorm.DB, tableName, columnName string) (bool, error) {
	return sqliteColumnExists(db, tableName, columnName)
}

// SQLiteColumnNotNull checks if a SQLite column is defined as NOT NULL.
// @intent 외부 호출자가 SQLite 컬럼 nullability를 내부 helper 재사용으로 확인하게 한다.
func SQLiteColumnNotNull(db *gorm.DB, tableName, columnName string) (bool, error) {
	return sqliteColumnNotNull(db, tableName, columnName)
}

// SQLiteColumnInfo returns internal metadata for a SQLite column.
// @intent SQLite 컬럼 메타데이터를 공개형 struct로 노출해 테스트와 검증 코드에서 재사용하게 한다.
func SQLiteColumnInfo(db *gorm.DB, tableName, columnName string) (struct {
	Exists  bool
	NotNull bool
}, error) {
	column, err := sqliteColumnInfo(db, tableName, columnName)
	if err != nil {
		return struct {
			Exists  bool
			NotNull bool
		}{}, err
	}
	return struct {
		Exists  bool
		NotNull bool
	}{
		Exists:  column.exists,
		NotNull: column.notNull,
	}, nil
}

// postgresColumnNotNull checks whether an information_schema column is NOT NULL.
// @intent PostgreSQL 컬럼 nullability 검증을 위한 내부 helper를 제공한다.
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

// postgresColumnDataType loads the PostgreSQL information_schema data_type value for a column.
// @intent jsonb 같은 타입 불변식을 런타임 스키마 검증에서 확인한다.
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

// PostgresColumnNotNull checks if a Postgres column is NOT NULL.
// @intent 외부 검증 코드가 PostgreSQL 컬럼 nullability를 재사용 가능한 API로 확인하게 한다.
func PostgresColumnNotNull(db *gorm.DB, tableName, columnName string) (bool, error) {
	return postgresColumnNotNull(db, tableName, columnName)
}

// PostgresColumnDataType returns PostgreSQL data_type for a column.
// @intent 외부 검증 코드가 PostgreSQL 컬럼 타입을 재사용 가능한 API로 확인하게 한다.
func PostgresColumnDataType(db *gorm.DB, tableName, columnName string) (string, error) {
	return postgresColumnDataType(db, tableName, columnName)
}

// postgresIndexExists reports whether a named Postgres index exists in public schema.
// @intent 검색 인덱스와 운영 필수 인덱스 존재 여부를 내부 helper로 조회한다.
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

// PostgresIndexExists checks whether a Postgres index exists.
// @intent 외부 검증 코드가 Postgres 인덱스 존재 여부를 재사용 가능한 API로 확인하게 한다.
func PostgresIndexExists(db *gorm.DB, indexName string) (bool, error) {
	return postgresIndexExists(db, indexName)
}

// postgresTriggerExists reports whether a named non-internal Postgres trigger exists.
// @intent 검색 트리거 같은 운영 필수 트리거 존재 여부를 내부 helper로 조회한다.
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

// PostgresTriggerExists checks whether a non-internal Postgres trigger exists.
// @intent 외부 검증 코드가 Postgres 트리거 존재 여부를 재사용 가능한 API로 확인하게 한다.
func PostgresTriggerExists(db *gorm.DB, triggerName string) (bool, error) {
	return postgresTriggerExists(db, triggerName)
}
