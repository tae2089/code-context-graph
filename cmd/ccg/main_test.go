package main

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/tae2089/code-context-graph/internal/ctxns"
	"github.com/tae2089/code-context-graph/internal/model"
	"github.com/tae2089/code-context-graph/internal/store/gormstore"
)

func setupNamespaceMigrationDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql db: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	st := gormstore.New(db)
	if err := st.AutoMigrate(); err != nil {
		t.Fatalf("migrate store: %v", err)
	}
	if err := db.AutoMigrate(&model.SearchDocument{}, &model.Flow{}, &model.FlowMembership{}); err != nil {
		t.Fatalf("migrate extra models: %v", err)
	}
	return db
}

func requireSQLiteFTS5(t *testing.T) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatalf("open fts probe db: %v", err)
	}
	if err := db.Exec(`CREATE VIRTUAL TABLE fts_probe USING fts5(content)`).Error; err != nil {
		t.Skipf("sqlite fts5 is unavailable in this build: %v", err)
	}
}

func TestRunMigrations_AllowsRuntimeSchemaCheck(t *testing.T) {
	requireSQLiteFTS5(t)

	dbPath := filepath.Join(t.TempDir(), "ccg.db")
	db, err := openDB("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			_ = sqlDB.Close()
		}
	}()

	if err := checkSchemaVersion(db); err == nil {
		t.Fatal("expected schema check to fail before migrate")
	}
	if err := runMigrations(db, "sqlite", ""); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	if err := checkSchemaVersion(db); err != nil {
		t.Fatalf("schema check after migrate: %v", err)
	}

	var version migrateSchemaVersion
	if err := db.Table("schema_migrations").First(&version).Error; err != nil {
		t.Fatalf("load schema version: %v", err)
	}
	if int(version.Version) != requiredSchemaVersion {
		t.Fatalf("schema version = %d, want %d", version.Version, requiredSchemaVersion)
	}
	if version.Dirty {
		t.Fatal("schema version is dirty")
	}
}

func TestRunMigrations_SQLiteSchemaMatchesModelNullability(t *testing.T) {
	requireSQLiteFTS5(t)

	dbPath := filepath.Join(t.TempDir(), "ccg.db")
	db, err := openDB("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			_ = sqlDB.Close()
		}
	}()

	if err := runMigrations(db, "sqlite", ""); err != nil {
		t.Fatalf("run migrations: %v", err)
	}

	for _, tc := range modelNullabilityColumns() {
		notNull, err := sqliteColumnNotNull(db, tc.table, tc.column)
		if err != nil {
			t.Fatalf("inspect %s.%s: %v", tc.table, tc.column, err)
		}
		if !notNull {
			t.Fatalf("expected %s.%s to be NOT NULL", tc.table, tc.column)
		}
	}
}

func TestRunMigrations_SQLiteBackfillsVersionOneNulls(t *testing.T) {
	requireSQLiteFTS5(t)

	dbPath := filepath.Join(t.TempDir(), "ccg.db")
	db, err := openDB("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			_ = sqlDB.Close()
		}
	}()

	migrator, _, err := newMigrator(db, "sqlite", "")
	if err != nil {
		t.Fatalf("create migrator: %v", err)
	}
	if err := migrator.Steps(1); err != nil {
		t.Fatalf("run version 1 migration: %v", err)
	}

	if err := db.Exec(`INSERT INTO nodes(namespace) VALUES (?)`, ctxns.DefaultNamespace).Error; err != nil {
		t.Fatalf("insert null node: %v", err)
	}
	if err := db.Exec(`INSERT INTO edges(namespace) VALUES (?)`, ctxns.DefaultNamespace).Error; err != nil {
		t.Fatalf("insert null edge: %v", err)
	}
	if err := db.Exec(`INSERT INTO communities(namespace, label, strategy) VALUES (?, ?, ?)`, ctxns.DefaultNamespace, "community", "manual").Error; err != nil {
		t.Fatalf("insert null community key: %v", err)
	}
	if err := db.Exec(`INSERT INTO community_memberships DEFAULT VALUES`).Error; err != nil {
		t.Fatalf("insert null community membership: %v", err)
	}
	if err := db.Exec(`INSERT INTO flows(namespace) VALUES (?)`, ctxns.DefaultNamespace).Error; err != nil {
		t.Fatalf("insert null flow name: %v", err)
	}
	if err := db.Exec(`INSERT INTO flow_memberships(namespace, ordinal) VALUES (?, ?)`, ctxns.DefaultNamespace, 0).Error; err != nil {
		t.Fatalf("insert null flow membership: %v", err)
	}

	if err := runMigrations(db, "sqlite", ""); err != nil {
		t.Fatalf("run migrations: %v", err)
	}

	assertScalar(t, db, `SELECT qualified_name FROM nodes LIMIT 1`, "")
	assertScalar(t, db, `SELECT kind FROM nodes LIMIT 1`, "")
	assertScalar(t, db, `SELECT name FROM nodes LIMIT 1`, "")
	assertScalar(t, db, `SELECT file_path FROM nodes LIMIT 1`, "")
	assertScalar(t, db, `SELECT start_line FROM nodes LIMIT 1`, int64(0))
	assertScalar(t, db, `SELECT end_line FROM nodes LIMIT 1`, int64(0))
	assertScalar(t, db, `SELECT kind FROM edges LIMIT 1`, "")
	assertScalar(t, db, `SELECT fingerprint FROM edges LIMIT 1`, "")
	assertScalar(t, db, `SELECT key FROM communities LIMIT 1`, "")
	assertScalar(t, db, `SELECT community_id FROM community_memberships LIMIT 1`, int64(0))
	assertScalar(t, db, `SELECT node_id FROM community_memberships LIMIT 1`, int64(0))
	assertScalar(t, db, `SELECT name FROM flows LIMIT 1`, "")
	assertScalar(t, db, `SELECT flow_id FROM flow_memberships LIMIT 1`, int64(0))
	assertScalar(t, db, `SELECT node_id FROM flow_memberships LIMIT 1`, int64(0))
}

func TestRunMigrations_SQLiteDownRestoresNullableColumns(t *testing.T) {
	requireSQLiteFTS5(t)

	dbPath := filepath.Join(t.TempDir(), "ccg.db")
	db, err := openDB("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			_ = sqlDB.Close()
		}
	}()

	if err := runMigrations(db, "sqlite", ""); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	migrator, _, err := newMigrator(db, "sqlite", "")
	if err != nil {
		t.Fatalf("create migrator: %v", err)
	}
	if err := migrator.Steps(-1); err != nil {
		t.Fatalf("run down migration: %v", err)
	}

	var version migrateSchemaVersion
	if err := db.Table("schema_migrations").First(&version).Error; err != nil {
		t.Fatalf("load schema version: %v", err)
	}
	if version.Version != 1 {
		t.Fatalf("schema version = %d, want 1", version.Version)
	}

	for _, tc := range modelNullabilityColumns() {
		notNull, err := sqliteColumnNotNull(db, tc.table, tc.column)
		if err != nil {
			t.Fatalf("inspect %s.%s: %v", tc.table, tc.column, err)
		}
		if notNull {
			t.Fatalf("expected %s.%s to be nullable after down", tc.table, tc.column)
		}
	}
}

func assertScalar[T comparable](t *testing.T, db *gorm.DB, query string, want T) {
	t.Helper()
	var got T
	if err := db.Raw(query).Scan(&got).Error; err != nil {
		t.Fatalf("query scalar: %v", err)
	}
	if got != want {
		t.Fatalf("scalar %q = %v, want %v", query, got, want)
	}
}

func TestEnsureSchemaVersion_AutoMigratesLocalSQLiteDefault(t *testing.T) {
	requireSQLiteFTS5(t)

	dbPath := filepath.Join(t.TempDir(), "ccg.db")
	db, err := openDB("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			_ = sqlDB.Close()
		}
	}()

	if err := ensureSchemaVersion(db, "sqlite", dbPath, ""); err != nil {
		t.Fatalf("ensure schema version: %v", err)
	}
	if err := checkSchemaVersion(db); err != nil {
		t.Fatalf("schema check after auto migrate: %v", err)
	}
	if !db.Migrator().HasTable(&model.Node{}) {
		t.Fatal("expected nodes table after auto migrate")
	}
}

func TestEnsureSchemaVersion_ValidatesParityAfterVersionCheck(t *testing.T) {
	requireSQLiteFTS5(t)

	dbPath := filepath.Join(t.TempDir(), "ccg.db")
	db, err := openDB("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			_ = sqlDB.Close()
		}
	}()

	if err := runMigrations(db, "sqlite", ""); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	if err := db.Exec(`DROP TABLE search_fts`).Error; err != nil {
		t.Fatalf("drop search_fts: %v", err)
	}

	err = ensureSchemaVersion(db, "sqlite", dbPath, "")
	if err == nil {
		t.Fatal("expected parity failure after schema version check succeeds")
	}
	msg := err.Error()
	for _, want := range []string{"run `ccg migrate`", "verify migration source", "schema drift"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("error %q does not contain remediation %q", msg, want)
		}
	}
}

func TestEnsureSchemaVersion_LogsRuntimeSchemaPassAndFail(t *testing.T) {
	requireSQLiteFTS5(t)

	var logs bytes.Buffer
	previousLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() { slog.SetDefault(previousLogger) })

	dbPath := filepath.Join(t.TempDir(), "ccg.db")
	db, err := openDB("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			_ = sqlDB.Close()
		}
	}()

	if err := ensureSchemaVersion(db, "sqlite", dbPath, ""); err != nil {
		t.Fatalf("ensure schema version: %v", err)
	}
	passLog := logs.String()
	for _, want := range []string{"database runtime schema check passed", "driver=sqlite", "required_version=2", "auto_migrated=true"} {
		if !strings.Contains(passLog, want) {
			t.Fatalf("expected runtime schema pass log to contain %q, got %q", want, passLog)
		}
	}

	logs.Reset()
	if err := db.Exec(`DROP TABLE search_fts`).Error; err != nil {
		t.Fatalf("drop search_fts: %v", err)
	}
	if err := ensureSchemaVersion(db, "sqlite", dbPath, ""); err == nil {
		t.Fatal("expected parity failure")
	}
	failLog := logs.String()
	for _, want := range []string{"database runtime schema check failed", "driver=sqlite", "required_version=2", "auto_migrated=false", "error="} {
		if !strings.Contains(failLog, want) {
			t.Fatalf("expected runtime schema failure log to contain %q, got %q", want, failLog)
		}
	}
}

func TestRunMigrations_WrapsParityFailureWithRemediation(t *testing.T) {
	db, err := openDB("sqlite", filepath.Join(t.TempDir(), "legacy-drift.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			_ = sqlDB.Close()
		}
	}()
	st := gormstore.New(db)
	if err := st.AutoMigrate(); err != nil {
		t.Fatalf("migrate store: %v", err)
	}
	if err := db.AutoMigrate(&model.SearchDocument{}, &model.Flow{}, &model.FlowMembership{}, &model.SchemaVersion{}); err != nil {
		t.Fatalf("migrate extra models: %v", err)
	}
	if err := db.Create(&model.SchemaVersion{Key: schemaVersionKey, Version: requiredSchemaVersion}).Error; err != nil {
		t.Fatalf("create legacy schema version: %v", err)
	}

	err = runMigrations(db, "sqlite", "")
	if err == nil {
		t.Fatal("expected legacy baseline to fail with schema drift")
	}
	msg := err.Error()
	for _, want := range []string{"run `ccg migrate`", "verify migration source", "schema drift"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("error %q does not contain remediation %q", msg, want)
		}
	}
}

func TestEnsureSchemaVersion_DoesNotAutoMigrateCustomSQLiteDSN(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "custom.db")
	db, err := openDB("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			_ = sqlDB.Close()
		}
	}()

	if err := ensureSchemaVersion(db, "sqlite", dbPath, ""); err == nil {
		t.Fatal("expected custom sqlite dsn to require explicit migration")
	}
	if db.Migrator().HasTable("schema_migrations") {
		t.Fatal("did not expect custom sqlite dsn to be auto migrated")
	}
}

func TestShouldAutoMigrateLocalSQLite(t *testing.T) {
	tests := []struct {
		name   string
		driver string
		dsn    string
		want   bool
	}{
		{name: "empty default", driver: "sqlite", dsn: "", want: true},
		{name: "bare default", driver: "sqlite", dsn: "ccg.db", want: true},
		{name: "relative current dir default", driver: "sqlite", dsn: "./ccg.db", want: true},
		{name: "absolute default", driver: "sqlite", dsn: filepath.Join(t.TempDir(), "ccg.db"), want: true},
		{name: "file absolute default", driver: "sqlite", dsn: "file:" + filepath.Join(t.TempDir(), "ccg.db") + "?cache=shared", want: true},
		{name: "nested relative", driver: "sqlite", dsn: "nested/ccg.db", want: false},
		{name: "parent relative", driver: "sqlite", dsn: "../ccg.db", want: false},
		{name: "memory", driver: "sqlite", dsn: ":memory:", want: false},
		{name: "custom name", driver: "sqlite", dsn: "custom.db", want: false},
		{name: "postgres", driver: "postgres", dsn: "ccg.db", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldAutoMigrateLocalSQLite(tt.driver, tt.dsn); got != tt.want {
				t.Fatalf("shouldAutoMigrateLocalSQLite(%q, %q) = %v, want %v", tt.driver, tt.dsn, got, tt.want)
			}
		})
	}
}

func TestMigrationSourceInfoFor(t *testing.T) {
	embedded, err := migrationSourceInfoFor("sqlite", "")
	if err != nil {
		t.Fatalf("embedded source info: %v", err)
	}
	if embedded.Kind != "embedded" || embedded.Driver != "sqlite" || embedded.Path != "" {
		t.Fatalf("embedded source info = %+v", embedded)
	}

	root := t.TempDir()
	sqliteDir := filepath.Join(root, "sqlite")
	if err := os.MkdirAll(sqliteDir, 0o755); err != nil {
		t.Fatalf("create sqlite migration dir: %v", err)
	}
	external, err := migrationSourceInfoFor("sqlite", root)
	if err != nil {
		t.Fatalf("external source info: %v", err)
	}
	if external.Kind != "external" || external.Driver != "sqlite" || external.Path != sqliteDir {
		t.Fatalf("external source info = %+v, want path %q", external, sqliteDir)
	}
}

func TestCheckSchemaVersion_FailsOnIncompatibleVersion(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.Exec(`CREATE TABLE schema_migrations (version uint64, dirty bool)`).Error; err != nil {
		t.Fatalf("create schema migrations table: %v", err)
	}
	if err := db.Exec(`INSERT INTO schema_migrations(version, dirty) VALUES (?, ?)`, requiredSchemaVersion-1, false).Error; err != nil {
		t.Fatalf("insert old schema version: %v", err)
	}

	if err := checkSchemaVersion(db); err == nil {
		t.Fatal("expected incompatible schema version error")
	}
}

func TestCheckSchemaVersion_FailsOnDirtyMigration(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.Exec(`CREATE TABLE schema_migrations (version uint64, dirty bool)`).Error; err != nil {
		t.Fatalf("create schema migrations table: %v", err)
	}
	if err := db.Exec(`INSERT INTO schema_migrations(version, dirty) VALUES (?, ?)`, requiredSchemaVersion, true).Error; err != nil {
		t.Fatalf("insert dirty schema version: %v", err)
	}

	if err := checkSchemaVersion(db); err == nil {
		t.Fatal("expected dirty migration error")
	}
}

func TestRunMigrations_BaselinesLegacySchemaVersion(t *testing.T) {
	requireSQLiteFTS5(t)

	db, err := openDB("sqlite", filepath.Join(t.TempDir(), "legacy.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			_ = sqlDB.Close()
		}
	}()

	if err := runMigrations(db, "sqlite", ""); err != nil {
		t.Fatalf("prepare migrated schema: %v", err)
	}
	if err := db.Exec(`DELETE FROM schema_migrations`).Error; err != nil {
		t.Fatalf("clear schema migrations: %v", err)
	}
	if err := db.AutoMigrate(&model.SchemaVersion{}); err != nil {
		t.Fatalf("migrate legacy schema version table: %v", err)
	}
	if err := db.Create(&model.SchemaVersion{Key: schemaVersionKey, Version: requiredSchemaVersion}).Error; err != nil {
		t.Fatalf("create legacy schema version: %v", err)
	}

	if err := runMigrations(db, "sqlite", ""); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	if err := checkSchemaVersion(db); err != nil {
		t.Fatalf("schema check after baseline: %v", err)
	}
}

func TestRunMigrations_FailsLegacyBaselineWithSchemaDrift(t *testing.T) {
	db, err := openDB("sqlite", filepath.Join(t.TempDir(), "legacy-drift.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			_ = sqlDB.Close()
		}
	}()
	st := gormstore.New(db)
	if err := st.AutoMigrate(); err != nil {
		t.Fatalf("migrate store: %v", err)
	}
	if err := db.AutoMigrate(&model.SearchDocument{}, &model.Flow{}, &model.FlowMembership{}, &model.SchemaVersion{}); err != nil {
		t.Fatalf("migrate extra models: %v", err)
	}
	if err := db.Create(&model.SchemaVersion{Key: schemaVersionKey, Version: requiredSchemaVersion}).Error; err != nil {
		t.Fatalf("create legacy schema version: %v", err)
	}

	err = runMigrations(db, "sqlite", "")
	if err == nil {
		t.Fatal("expected legacy baseline to fail with schema drift")
	}
	if !strings.Contains(err.Error(), "run `ccg migrate`") {
		t.Fatalf("expected remediation guidance, got %q", err.Error())
	}
}

func TestMigrateLegacyDefaultNamespace_BackfillsEmptyNamespaceRows(t *testing.T) {
	db := setupNamespaceMigrationDB(t)

	if err := db.Exec(`INSERT INTO nodes(namespace, qualified_name, kind, name, file_path, start_line, end_line, language, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`, "", "pkg.Legacy", model.NodeKindFunction, "Legacy", "legacy.go", 1, 2, "go").Error; err != nil {
		t.Fatalf("insert legacy node: %v", err)
	}
	var node model.Node
	if err := db.Where("namespace = ? AND qualified_name = ?", "", "pkg.Legacy").First(&node).Error; err != nil {
		t.Fatalf("load legacy node: %v", err)
	}
	if err := db.Exec(`INSERT INTO edges(namespace, from_node_id, to_node_id, kind, fingerprint, created_at) VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP)`, "", node.ID, node.ID, model.EdgeKindCalls, "legacy-edge").Error; err != nil {
		t.Fatalf("insert legacy edge: %v", err)
	}
	if err := db.Create(&model.SearchDocument{Namespace: "", NodeID: node.ID, Content: "legacy doc", Language: "go"}).Error; err != nil {
		t.Fatalf("create search doc: %v", err)
	}
	if err := db.Exec(`INSERT INTO communities(namespace, key, label, strategy, created_at, updated_at) VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`, "", "legacy", "legacy", "directory").Error; err != nil {
		t.Fatalf("insert legacy community: %v", err)
	}
	var community model.Community
	if err := db.Where("namespace = ? AND key = ?", "", "legacy").First(&community).Error; err != nil {
		t.Fatalf("load legacy community: %v", err)
	}
	if err := db.Exec(`INSERT INTO flows(namespace, name, created_at) VALUES (?, ?, CURRENT_TIMESTAMP)`, "", "legacy-flow").Error; err != nil {
		t.Fatalf("insert legacy flow: %v", err)
	}
	var flow model.Flow
	if err := db.Where("namespace = ? AND name = ?", "", "legacy-flow").First(&flow).Error; err != nil {
		t.Fatalf("load legacy flow: %v", err)
	}
	if err := db.Exec(`INSERT INTO flow_memberships(namespace, flow_id, node_id, ordinal) VALUES (?, ?, ?, ?)`, "", flow.ID, node.ID, 0).Error; err != nil {
		t.Fatalf("insert flow membership: %v", err)
	}

	if err := migrateLegacyDefaultNamespace(db); err != nil {
		t.Fatalf("migrate legacy default namespace: %v", err)
	}

	for _, tc := range []struct {
		name  string
		model any
	}{
		{name: "nodes", model: &model.Node{}},
		{name: "edges", model: &model.Edge{}},
		{name: "search_documents", model: &model.SearchDocument{}},
		{name: "communities", model: &model.Community{}},
		{name: "flows", model: &model.Flow{}},
		{name: "flow_memberships", model: &model.FlowMembership{}},
	} {
		var legacyCount int64
		if err := db.Model(tc.model).Where("namespace = ?", "").Count(&legacyCount).Error; err != nil {
			t.Fatalf("count legacy %s: %v", tc.name, err)
		}
		if legacyCount != 0 {
			t.Fatalf("expected no legacy rows in %s, got %d", tc.name, legacyCount)
		}
	}

	var defaultNode model.Node
	if err := db.Where("qualified_name = ?", "pkg.Legacy").First(&defaultNode).Error; err != nil {
		t.Fatalf("load migrated node: %v", err)
	}
	if defaultNode.Namespace != ctxns.DefaultNamespace {
		t.Fatalf("migrated node namespace = %q, want %q", defaultNode.Namespace, ctxns.DefaultNamespace)
	}
}

func TestMigrateLegacyDefaultNamespace_FailsOnNodeCollision(t *testing.T) {
	db := setupNamespaceMigrationDB(t)

	current := model.Node{Namespace: ctxns.DefaultNamespace, QualifiedName: "pkg.Collision", Kind: model.NodeKindFunction, Name: "Collision", FilePath: "same.go", StartLine: 1, EndLine: 2, Language: "go"}
	if err := db.Exec(`INSERT INTO nodes(namespace, qualified_name, kind, name, file_path, start_line, end_line, language, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`, "", "pkg.Collision", model.NodeKindFunction, "Collision", "same.go", 1, 2, "go").Error; err != nil {
		t.Fatalf("insert legacy node: %v", err)
	}
	if err := db.Create(&current).Error; err != nil {
		t.Fatalf("create default node: %v", err)
	}

	err := migrateLegacyDefaultNamespace(db)
	if err == nil {
		t.Fatal("expected collision error")
	}

	var legacyCount int64
	if err := db.Model(&model.Node{}).Where("namespace = ?", "").Count(&legacyCount).Error; err != nil {
		t.Fatalf("count legacy nodes: %v", err)
	}
	if legacyCount != 1 {
		t.Fatalf("expected legacy row to remain after failed migration, got %d", legacyCount)
	}
}
