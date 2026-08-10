//go:build postgres

package dbtest

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Both halves of this test build a table under the same name and fill it with
// different rows. Nothing in the two halves distinguishes them, so if they share
// one schema the second CREATE TABLE finds the first one's table and both halves
// read rows they never wrote. That is the failure that forced `-p 1` on CI, moved
// down to a single package where it can be observed directly.
func TestOpenIsolatedPostgres_ConcurrentTestsDoNotSeeEachOthersRows(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		word string
	}{
		{name: "first", word: "first-writer"},
		{name: "second", word: "second-writer"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			db := OpenIsolatedPostgres(t)

			if err := db.Exec("CREATE TABLE isolation_probe (word text NOT NULL)").Error; err != nil {
				t.Fatalf("create probe table: %v", err)
			}
			if err := db.Exec("INSERT INTO isolation_probe (word) VALUES (?)", tc.word).Error; err != nil {
				t.Fatalf("insert probe row: %v", err)
			}

			var words []string
			if err := db.Raw("SELECT word FROM isolation_probe ORDER BY word").Scan(&words).Error; err != nil {
				t.Fatalf("read probe rows: %v", err)
			}
			if len(words) != 1 || words[0] != tc.word {
				t.Fatalf("isolation_probe rows = %v, want exactly [%s]; another test's rows are visible", words, tc.word)
			}
		})
	}
}

// A schema is only private if every pooled connection agrees on it. `SET search_path`
// applies to one session, so on a pool of several connections it leaves the rest
// pointing at public and the test silently writes to the shared schema.
func TestOpenIsolatedPostgres_SearchPathHoldsOnEveryPooledConnection(t *testing.T) {
	t.Parallel()
	db := OpenIsolatedPostgres(t)

	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql DB: %v", err)
	}
	sqlDB.SetMaxOpenConns(8)

	want := currentSchema(t, db)
	if want == "public" {
		t.Fatalf("current_schema() = %q, want a private schema", want)
	}

	const queries = 40
	schemas := make([]string, queries)
	var wg sync.WaitGroup
	for i := range schemas {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			var schema string
			if err := db.Raw("SELECT current_schema()").Scan(&schema).Error; err != nil {
				schema = "error: " + err.Error()
			}
			schemas[i] = schema
		}(i)
	}
	wg.Wait()

	for i, schema := range schemas {
		if schema != want {
			t.Fatalf("query %d ran in schema %q, want %q; search_path does not reach every pooled connection", i, schema, want)
		}
	}
}

// Schemas must not pile up on the server between runs, so the schema a test creates
// has to be gone once that test returns.
func TestOpenIsolatedPostgres_DropsItsSchemaWhenTheTestFinishes(t *testing.T) {
	t.Parallel()

	var schema string
	t.Run("inner", func(t *testing.T) {
		db := OpenIsolatedPostgres(t)
		schema = currentSchema(t, db)
	})
	if schema == "" || schema == "public" {
		t.Fatalf("inner test ran in schema %q, want a private schema", schema)
	}

	admin := openAdminPostgres(t)
	if postgresSchemaExists(t, admin, schema) {
		t.Fatalf("schema %q still exists after its test finished", schema)
	}
}

// Cleanup drops one schema by name. A helper that reset the whole server, or that
// dropped every schema matching its own naming pattern, would take a concurrently
// running test's schema with it.
func TestOpenIsolatedPostgres_LeavesAnotherTestsSchemaAlone(t *testing.T) {
	t.Parallel()

	admin := openAdminPostgres(t)

	var bystander string
	t.Run("bystander-owner", func(t *testing.T) {
		bystanderDB := OpenIsolatedPostgres(t)
		bystander = currentSchema(t, bystanderDB)

		t.Run("finishes-first", func(t *testing.T) {
			db := OpenIsolatedPostgres(t)
			if own := currentSchema(t, db); own == bystander {
				t.Fatalf("two tests share schema %q", own)
			}
		})

		if !postgresSchemaExists(t, admin, bystander) {
			t.Fatalf("schema %q disappeared while its own test was still running", bystander)
		}
	})
	if postgresSchemaExists(t, admin, bystander) {
		t.Fatalf("schema %q outlived its test", bystander)
	}
}

// currentSchema reports the schema a connection writes to.
func currentSchema(t *testing.T, db *gorm.DB) string {
	t.Helper()
	var schema string
	if err := db.Raw("SELECT current_schema()").Scan(&schema).Error; err != nil {
		t.Fatalf("query current schema: %v", err)
	}
	return schema
}

// openAdminPostgres opens a connection outside any private schema, so a test can
// look at the server the way another process sees it.
func openAdminPostgres(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(postgres.Open(PostgresDSN()), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Skipf("PostgreSQL not available: %v", err)
	}
	t.Cleanup(func() {
		sqlDB, err := db.DB()
		if err != nil {
			t.Errorf("get admin sql DB: %v", err)
			return
		}
		if err := sqlDB.Close(); err != nil {
			t.Errorf("close admin PostgreSQL pool: %v", err)
		}
	})
	return db
}

// postgresSchemaExists reports whether a schema is still present on the server.
func postgresSchemaExists(t *testing.T, admin *gorm.DB, schema string) bool {
	t.Helper()
	var count int64
	err := admin.Raw("SELECT COUNT(*) FROM information_schema.schemata WHERE schema_name = ?", schema).Scan(&count).Error
	if err != nil {
		t.Fatalf("count schema %q: %v", schema, err)
	}
	return count > 0
}

// A test that dies without running its cleanup, killed or panicking, leaves its schema
// behind. The sweep is what stops those from accumulating, and the age cutoff is what
// keeps it from taking a schema a concurrent test is still using.
func TestSweepStalePostgresSchemas_DropsAbandonedSchemasAndSparesLiveOnes(t *testing.T) {
	t.Parallel()

	live := OpenIsolatedPostgres(t)
	liveSchema := currentSchema(t, live)
	admin := openAdminPostgres(t)

	// Named as though it had been created well before the cutoff, which is the only
	// thing that separates an abandoned schema from one still in use.
	abandoned := fmt.Sprintf("%s%d_beefcafe", postgresSchemaPrefix, time.Now().Add(-72*time.Hour).Unix())
	if err := admin.Exec("CREATE SCHEMA " + abandoned).Error; err != nil {
		t.Fatalf("create abandoned schema: %v", err)
	}
	t.Cleanup(func() {
		if err := admin.Exec("DROP SCHEMA IF EXISTS " + abandoned + " CASCADE").Error; err != nil {
			t.Errorf("drop abandoned schema: %v", err)
		}
	})

	if err := sweepStalePostgresSchemas(admin, stalePostgresSchemaAge, time.Now()); err != nil {
		t.Fatalf("sweep stale schemas: %v", err)
	}

	if postgresSchemaExists(t, admin, abandoned) {
		t.Fatalf("abandoned schema %q survived the sweep", abandoned)
	}
	if !postgresSchemaExists(t, admin, liveSchema) {
		t.Fatalf("sweep dropped schema %q, which a running test is still using", liveSchema)
	}
}

// The suite creates and drops whole schemas, so pointing it at a database holding real
// data has to fail before any of that happens.
func TestNewPostgresSchema_RefusesANonTestDatabase(t *testing.T) {
	t.Parallel()

	// "postgres" always exists on a server and is never a test database.
	_, err := newPostgresSchema(replacePostgresDBName(t, PostgresDSN(), "postgres"))
	if err == nil {
		t.Fatal("newPostgresSchema accepted the non-test database \"postgres\"")
	}
	want := `refusing to manage schemas in non-test database "postgres"`
	if err.Error() != want {
		t.Fatalf("error = %q, want %q", err, want)
	}
}

// replacePostgresDBName swaps the database in a key=value DSN.
func replacePostgresDBName(t *testing.T, dsn, name string) string {
	t.Helper()
	fields := strings.Fields(dsn)
	replaced := false
	for i, field := range fields {
		if strings.HasPrefix(field, "dbname=") {
			fields[i] = "dbname=" + name
			replaced = true
		}
	}
	if !replaced {
		t.Skipf("DSN %q has no dbname to replace", dsn)
	}
	return strings.Join(fields, " ")
}
