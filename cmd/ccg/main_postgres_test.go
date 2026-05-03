//go:build postgres

package main

import (
	"os"
	"strings"
	"testing"

	"gorm.io/gorm"
)

func setupPostgresMigrationDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		dsn = "host=localhost user=postgres password=postgres dbname=ccg_test port=5432 sslmode=disable"
	}
	db, err := openDB("postgres", dsn)
	if err != nil {
		t.Skipf("PostgreSQL not available: %v", err)
	}
	var databaseName string
	if err := db.Raw("SELECT current_database()").Scan(&databaseName).Error; err != nil {
		t.Fatalf("query database name: %v", err)
	}
	if databaseName != "ccg_test" && !strings.HasSuffix(databaseName, "_test") {
		t.Fatalf("refusing to reset non-test database %q", databaseName)
	}
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			_ = sqlDB.Close()
		}
	})
	if err := db.Exec("DROP SCHEMA public CASCADE").Error; err != nil {
		t.Fatalf("drop schema: %v", err)
	}
	if err := db.Exec("CREATE SCHEMA public").Error; err != nil {
		t.Fatalf("create schema: %v", err)
	}
	return db
}

func TestRunMigrations_PostgresSmoke(t *testing.T) {
	db := setupPostgresMigrationDB(t)

	if err := runMigrations(db, "postgres", ""); err != nil {
		t.Fatalf("run postgres migrations: %v", err)
	}
	if err := checkSchemaVersion(db); err != nil {
		t.Fatalf("check schema version: %v", err)
	}

	var indexCount int64
	if err := db.Raw(`
		SELECT COUNT(*)
		FROM pg_indexes
		WHERE tablename = 'search_documents'
		AND indexname = 'idx_search_documents_tsv'
	`).Scan(&indexCount).Error; err != nil {
		t.Fatalf("query gin index: %v", err)
	}
	if indexCount != 1 {
		t.Fatalf("expected search_documents tsv GIN index, got %d", indexCount)
	}
	for _, tc := range modelNullabilityColumns() {
		notNull, err := postgresColumnNotNull(db, tc.table, tc.column)
		if err != nil {
			t.Fatalf("inspect %s.%s: %v", tc.table, tc.column, err)
		}
		if !notNull {
			t.Fatalf("expected %s.%s to be NOT NULL", tc.table, tc.column)
		}
	}
}

func TestRunMigrations_PostgresBackfillsVersionOneNulls(t *testing.T) {
	db := setupPostgresMigrationDB(t)

	migrator, _, err := newMigrator(db, "postgres", "")
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

	if err := runMigrations(db, "postgres", ""); err != nil {
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

	if err := runMigrations(db, "postgres", ""); err != nil {
		t.Fatalf("run postgres migrations: %v", err)
	}
	migrator, _, err := newMigrator(db, "postgres", "")
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
		notNull, err := postgresColumnNotNull(db, tc.table, tc.column)
		if err != nil {
			t.Fatalf("inspect %s.%s: %v", tc.table, tc.column, err)
		}
		if notNull {
			t.Fatalf("expected %s.%s to be nullable after down", tc.table, tc.column)
		}
	}
}
