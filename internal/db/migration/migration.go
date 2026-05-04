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
	RequiredSchemaVersion    = 3
	SchemaVersionKey         = "schema"
	LegacySchemaVersionTable = "ccg_schema_versions"
)

// LegacyBaselineFunc performs legacy-schema migration fallback during normal migration.
type LegacyBaselineFunc func(db *gorm.DB, migrator *gomigrate.Migrate, driver string) error

// ValidateSchemaParityFunc validates required tables and constraints for a driver.
type ValidateSchemaParityFunc func(db *gorm.DB, driver string) error

// SourceInfo describes how migration files are sourced.
type SourceInfo struct {
	Kind   string
	Driver string
	Path   string
}

// MigrationSchemaVersion mirrors a row from schema_migrations table.
type MigrationSchemaVersion struct {
	Version uint `gorm:"column:version"`
	Dirty   bool `gorm:"column:dirty"`
}

// SchemaColumn represents a required table/column pair.
type SchemaColumn struct {
	Table  string
	Column string
}

// SchemaTypeCheck represents a required column type assertion.
type SchemaTypeCheck struct {
	Table    string
	Column   string
	DataType string
}

// Run executes all pending migrations and validates schema parity.
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
func SourceInfoFor(driver, migrationsDir string) (SourceInfo, error) {
	return migrationSourceInfoFor(driver, migrationsDir)
}

func logMigrationSource(sourceInfo SourceInfo) {
	args := []any{"source", sourceInfo.Kind, "driver", sourceInfo.Driver}
	if sourceInfo.Path != "" {
		args = append(args, "path", sourceInfo.Path)
	}
	slog.Info("running database migrations", args...)
}

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

func actionableSchemaParityError(err error) error {
	return fmt.Errorf("database schema parity check failed: %w; run `ccg migrate`; if already migrated, verify migration source and schema drift", err)
}

// ActionableSchemaParityError wraps a schema-parity error with recommended remediation steps.
func ActionableSchemaParityError(err error) error {
	return actionableSchemaParityError(err)
}

// CheckSchemaVersion verifies the current schema migration metadata and dirty state.
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
	return nil
}

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
	return nil
}

func sqliteColumnExists(db *gorm.DB, tableName, columnName string) (bool, error) {
	column, err := sqliteColumnInfo(db, tableName, columnName)
	if err != nil {
		return false, err
	}
	return column.exists, nil
}

func sqliteColumnNotNull(db *gorm.DB, tableName, columnName string) (bool, error) {
	column, err := sqliteColumnInfo(db, tableName, columnName)
	if err != nil {
		return false, err
	}
	return column.notNull, nil
}

type sqliteColumn struct {
	exists  bool
	notNull bool
}

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
func SQLiteColumnExists(db *gorm.DB, tableName, columnName string) (bool, error) {
	return sqliteColumnExists(db, tableName, columnName)
}

// SQLiteColumnNotNull checks if a SQLite column is defined as NOT NULL.
func SQLiteColumnNotNull(db *gorm.DB, tableName, columnName string) (bool, error) {
	return sqliteColumnNotNull(db, tableName, columnName)
}

// SQLiteColumnInfo returns internal metadata for a SQLite column.
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
func PostgresColumnNotNull(db *gorm.DB, tableName, columnName string) (bool, error) {
	return postgresColumnNotNull(db, tableName, columnName)
}

// PostgresColumnDataType returns PostgreSQL data_type for a column.
func PostgresColumnDataType(db *gorm.DB, tableName, columnName string) (string, error) {
	return postgresColumnDataType(db, tableName, columnName)
}

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
func PostgresIndexExists(db *gorm.DB, indexName string) (bool, error) {
	return postgresIndexExists(db, indexName)
}

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
func PostgresTriggerExists(db *gorm.DB, triggerName string) (bool, error) {
	return postgresTriggerExists(db, triggerName)
}
