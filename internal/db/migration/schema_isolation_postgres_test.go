//go:build postgres

package migration

import (
	"testing"

	"github.com/tae2089/code-context-graph/internal/db/dbtest"
	"gorm.io/gorm"
)

// Nothing about the migrations should name the schema they run in. A deployment that
// puts ccg in its own PostgreSQL schema, and the test suite that gives every test a
// private schema, both depend on that: run the whole migration set somewhere other
// than public and the schema checks must still pass.
func TestRunMigrations_PostgresRunsUnchangedInANonPublicSchema(t *testing.T) {
	t.Parallel()
	db := dbtest.OpenIsolatedPostgres(t)
	schema := currentPostgresSchema(t, db)

	if err := RunMigrations(db, "postgres", ""); err != nil {
		t.Fatalf("run postgres migrations in schema %q: %v", schema, err)
	}
	if err := CheckSchemaVersion(db, RequiredSchemaVersion); err != nil {
		t.Fatalf("check schema version in schema %q: %v", schema, err)
	}
	if err := ValidateSchemaParity(db, "postgres"); err != nil {
		t.Fatalf("validate schema parity in schema %q: %v", schema, err)
	}

	// The tables have to land in this test's schema, not in public.
	var tablesHere int64
	err := db.Raw(
		"SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = ? AND table_name IN ?",
		schema, RequiredSchemaTables(),
	).Scan(&tablesHere).Error
	if err != nil {
		t.Fatalf("count tables in schema %q: %v", schema, err)
	}
	if want := int64(len(RequiredSchemaTables())); tablesHere != want {
		t.Fatalf("schema %q holds %d of the %d required tables", schema, tablesHere, want)
	}
}

// currentPostgresSchema reports the schema a connection reads and writes, failing the
// test if that is still the shared public schema.
func currentPostgresSchema(t *testing.T, db *gorm.DB) string {
	t.Helper()
	var schema string
	if err := db.Raw("SELECT current_schema()").Scan(&schema).Error; err != nil {
		t.Fatalf("query current schema: %v", err)
	}
	if schema == "public" {
		t.Fatalf("current_schema() = %q, want a schema other than public", schema)
	}
	return schema
}
