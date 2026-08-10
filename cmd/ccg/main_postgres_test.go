//go:build postgres

package main

import (
	"testing"

	ccgdb "github.com/tae2089/code-context-graph/internal/db"
	"github.com/tae2089/code-context-graph/internal/db/dbtest"
	"github.com/tae2089/code-context-graph/internal/db/migration"
	"gorm.io/gorm"
)

// setupPostgresMigrationDB opens this test's own schema through the production
// db.Open, so the migration smoke tests exercise the same entry point the CLI uses.
func setupPostgresMigrationDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := ccgdb.Open("postgres", dbtest.IsolatedPostgresDSN(t))
	if err != nil {
		t.Fatalf("open isolated PostgreSQL: %v", err)
	}
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

func TestRunMigrations_PostgresSmoke(t *testing.T) {
	db := setupPostgresMigrationDB(t)

	if err := migration.RunMigrations(db, "postgres", ""); err != nil {
		t.Fatalf("run postgres migrations: %v", err)
	}
	if err := migration.CheckSchemaVersion(db, migration.RequiredSchemaVersion); err != nil {
		t.Fatalf("check schema version: %v", err)
	}

	// Asked through the production check, which scopes the lookup to this test's own
	// schema. A bare pg_indexes count would also see the same index in every other
	// schema on the server.
	hasIndex, err := migration.PostgresIndexExists(db, "idx_search_documents_tsv")
	if err != nil {
		t.Fatalf("query gin index: %v", err)
	}
	if !hasIndex {
		t.Fatal("expected search_documents tsv GIN index")
	}
	for _, tc := range migration.ModelNullabilityColumns() {
		notNull, err := migration.PostgresColumnNotNull(db, tc.Table, tc.Column)
		if err != nil {
			t.Fatalf("inspect %s.%s: %v", tc.Table, tc.Column, err)
		}
		if !notNull {
			t.Fatalf("expected %s.%s to be NOT NULL", tc.Table, tc.Column)
		}
	}
	for _, table := range []string{"ccg_postprocess_policy_state", "ccg_postprocess_run_logs"} {
		if db.Migrator().HasTable(table) {
			t.Fatalf("expected migration 007 to remove %s at schema version %d", table, migration.RequiredSchemaVersion)
		}
	}

	var varcharCount int64
	if err := db.Raw(`
		SELECT COUNT(*)
		FROM information_schema.columns
		WHERE table_schema = current_schema()
		AND data_type = 'character varying'
	`).Scan(&varcharCount).Error; err != nil {
		t.Fatalf("count varchar columns: %v", err)
	}
	if varcharCount != 0 {
		t.Fatalf("PostgreSQL schema retained %d varchar columns, want 0", varcharCount)
	}
}

func TestRunMigrations_PostgresVersionThreePolicyTablesUseJSONB(t *testing.T) {
	db := setupPostgresMigrationDB(t)

	migrator, _, err := migration.NewMigrator(db, "postgres", "")
	if err != nil {
		t.Fatalf("create migrator: %v", err)
	}
	if err := migrator.Steps(3); err != nil {
		t.Fatalf("run migrations through version 3: %v", err)
	}

	var version migration.MigrationSchemaVersion
	if err := db.Table("schema_migrations").First(&version).Error; err != nil {
		t.Fatalf("load schema version: %v", err)
	}
	if version.Version != 3 {
		t.Fatalf("schema version = %d, want 3", version.Version)
	}

	for _, tc := range []struct {
		Table  string
		Column string
		want   string
	}{
		{Table: "ccg_postprocess_run_logs", Column: "failed_steps", want: "jsonb"},
		{Table: "ccg_postprocess_run_logs", Column: "skipped_steps", want: "jsonb"},
	} {
		got, err := migration.PostgresColumnDataType(db, tc.Table, tc.Column)
		if err != nil {
			t.Fatalf("inspect %s.%s type: %v", tc.Table, tc.Column, err)
		}
		if got != tc.want {
			t.Fatalf("expected %s.%s type %q, got %q", tc.Table, tc.Column, tc.want, got)
		}
	}
}

func TestRunMigrations_PostgresVersionFourteenConvertsGraphStringsAndCanMigrateDown(t *testing.T) {
	db := setupPostgresMigrationDB(t)

	migrator, _, err := migration.NewMigrator(db, "postgres", "")
	if err != nil {
		t.Fatalf("create migrator: %v", err)
	}
	if err := migrator.Steps(13); err != nil {
		t.Fatalf("run migrations through version 13: %v", err)
	}
	assertPostgresColumnType(t, db, "nodes", "name", "character varying")

	if err := migrator.Steps(1); err != nil {
		t.Fatalf("migrate to version 14: %v", err)
	}
	assertPostgresColumnType(t, db, "nodes", "name", "text")

	if err := migrator.Steps(-1); err != nil {
		t.Fatalf("migrate down to version 13: %v", err)
	}
	assertPostgresColumnType(t, db, "nodes", "name", "character varying")
}

func assertPostgresColumnType(t *testing.T, db *gorm.DB, table, column, want string) {
	t.Helper()
	got, err := migration.PostgresColumnDataType(db, table, column)
	if err != nil {
		t.Fatalf("inspect %s.%s type: %v", table, column, err)
	}
	if got != want {
		t.Fatalf("%s.%s type = %q, want %q", table, column, got, want)
	}
}

func TestRunMigrations_PostgresBackfillsVersionOneNulls(t *testing.T) {
	db := setupPostgresMigrationDB(t)

	migrator, _, err := migration.NewMigrator(db, "postgres", "")
	if err != nil {
		t.Fatalf("create migrator: %v", err)
	}
	if err := migrator.Steps(1); err != nil {
		t.Fatalf("run version 1 migration: %v", err)
	}

	if err := db.Exec(`INSERT INTO nodes DEFAULT VALUES`).Error; err != nil {
		t.Fatalf("insert null node: %v", err)
	}
	if err := db.Exec(`INSERT INTO edges DEFAULT VALUES`).Error; err != nil {
		t.Fatalf("insert null edge: %v", err)
	}
	if err := db.Exec(`INSERT INTO communities(label, strategy) VALUES (?, ?)`, "community", "manual").Error; err != nil {
		t.Fatalf("insert null community key: %v", err)
	}
	if err := db.Exec(`INSERT INTO community_memberships DEFAULT VALUES`).Error; err != nil {
		t.Fatalf("insert null community membership: %v", err)
	}
	if err := db.Exec(`INSERT INTO flows DEFAULT VALUES`).Error; err != nil {
		t.Fatalf("insert null flow name: %v", err)
	}
	if err := db.Exec(`INSERT INTO flow_memberships(ordinal) VALUES (?)`, 0).Error; err != nil {
		t.Fatalf("insert null flow membership: %v", err)
	}

	if err := migration.RunMigrations(db, "postgres", ""); err != nil {
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

func TestRunMigrations_PostgresDownRestoresNullableColumns(t *testing.T) {
	db := setupPostgresMigrationDB(t)

	if err := migration.RunMigrations(db, "postgres", ""); err != nil {
		t.Fatalf("run postgres migrations: %v", err)
	}
	migrator, _, err := migration.NewMigrator(db, "postgres", "")
	if err != nil {
		t.Fatalf("create migrator: %v", err)
	}
	if err := migrator.Migrate(1); err != nil {
		t.Fatalf("migrate down to version 1: %v", err)
	}

	var version migration.MigrationSchemaVersion
	if err := db.Table("schema_migrations").First(&version).Error; err != nil {
		t.Fatalf("load schema version: %v", err)
	}
	if version.Version != 1 {
		t.Fatalf("schema version = %d, want 1", version.Version)
	}

	for _, tc := range migration.ModelNullabilityColumns() {
		notNull, err := migration.PostgresColumnNotNull(db, tc.Table, tc.Column)
		if err != nil {
			t.Fatalf("inspect %s.%s: %v", tc.Table, tc.Column, err)
		}
		if notNull {
			t.Fatalf("expected %s.%s to be nullable after down", tc.Table, tc.Column)
		}
	}
}

func TestRunMigrations_PostgresDownFromVersionThreeDropsPolicyTables(t *testing.T) {
	db := setupPostgresMigrationDB(t)

	if err := migration.RunMigrations(db, "postgres", ""); err != nil {
		t.Fatalf("run postgres migrations: %v", err)
	}
	migrator, _, err := migration.NewMigrator(db, "postgres", "")
	if err != nil {
		t.Fatalf("create migrator: %v", err)
	}
	if err := migrator.Migrate(2); err != nil {
		t.Fatalf("migrate down to version 2: %v", err)
	}

	var version migration.MigrationSchemaVersion
	if err := db.Table("schema_migrations").First(&version).Error; err != nil {
		t.Fatalf("load schema version: %v", err)
	}
	if version.Version != 2 {
		t.Fatalf("schema version = %d, want 2", version.Version)
	}
	if db.Migrator().HasTable("ccg_postprocess_policy_state") {
		t.Fatal("expected ccg_postprocess_policy_state to be dropped after stepping down from version 3")
	}
	if db.Migrator().HasTable("ccg_postprocess_run_logs") {
		t.Fatal("expected ccg_postprocess_run_logs to be dropped after stepping down from version 3")
	}
}
