// @index Test-only PostgreSQL helpers that give each test its own schema so postgres-tagged packages can run concurrently.
package dbtest

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// defaultPostgresDSN is the local development server used when TEST_POSTGRES_DSN is unset.
const defaultPostgresDSN = "host=localhost user=postgres password=postgres dbname=ccg_test port=5432 sslmode=disable"

// PostgresDSN reports where postgres-tagged tests look for a database.
// @intent give every postgres-tagged package one shared answer for "which server" instead of a copy per package.
func PostgresDSN() string {
	if dsn := os.Getenv("TEST_POSTGRES_DSN"); dsn != "" {
		return dsn
	}
	return defaultPostgresDSN
}

// IsolatedPostgresDSN returns a DSN whose schema only this test may write to.
// @intent let a test build its own tables without a concurrently running test seeing or dropping them.
// @sideEffect creates a schema on the server and drops it when the test finishes.
func IsolatedPostgresDSN(t *testing.T) string {
	t.Helper()

	base := PostgresDSN()
	schema, err := newPostgresSchema(base)
	if err != nil {
		if isPostgresUnreachable(err) {
			t.Skipf("PostgreSQL not available: %v", err)
		}
		t.Fatalf("create isolated schema: %v", err)
	}
	// Registered before the caller opens its own pool, so cleanup runs in the other
	// order: the caller's connections close first and the drop never waits on them.
	t.Cleanup(func() {
		if err := schema.close(); err != nil {
			t.Errorf("drop isolated schema %q: %v", schema.name, err)
		}
	})
	return schema.dsn(base)
}

// OpenIsolatedPostgres opens a GORM connection scoped to this test's own schema.
// @intent replace the per-package "open postgres and wipe the shared schema" helper with one safe entry point.
func OpenIsolatedPostgres(t *testing.T) *gorm.DB {
	t.Helper()

	db, err := gorm.Open(postgres.Open(IsolatedPostgresDSN(t)), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open isolated PostgreSQL: %v", err)
	}
	t.Cleanup(func() {
		sqlDB, err := db.DB()
		if err != nil {
			t.Errorf("get sql DB: %v", err)
			return
		}
		if err := sqlDB.Close(); err != nil {
			t.Errorf("close PostgreSQL pool: %v", err)
		}
	})
	return db
}

// postgresSchema is one test's private schema plus the connection that owns its lifecycle.
// The lifecycle is kept off *testing.T so the drop path can be exercised on its own.
// @intent model "a schema that exists for as long as one test does" as a value with an explicit end.
type postgresSchema struct {
	name  string
	admin *gorm.DB
}

// newPostgresSchema creates an empty schema no other test will touch.
// DDL for schemas has no GORM model behind it, so the schema statements here are
// db.Exec, matching what the postgres test helpers in this repository already do.
// The name is generated from a fixed alphabet below, never from caller input.
// @intent hand back a private, empty schema together with the means to remove it.
// @sideEffect creates a schema and may drop abandoned schemas left by an earlier crashed run.
func newPostgresSchema(baseDSN string) (*postgresSchema, error) {
	admin, err := gorm.Open(postgres.Open(baseDSN), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		return nil, err
	}
	schema := &postgresSchema{name: newPostgresSchemaName(), admin: admin}

	if err := schema.requireTestDatabase(); err != nil {
		return nil, schema.abort(err)
	}
	sweepStalePostgresSchemasOnce(admin)
	if err := admin.Exec("CREATE SCHEMA " + schema.name).Error; err != nil {
		return nil, schema.abort(err)
	}
	return schema, nil
}

// requireTestDatabase refuses to create or drop schemas anywhere but a test database.
// @intent keep a misconfigured DSN from letting the suite create and drop schemas in real data.
func (s *postgresSchema) requireTestDatabase() error {
	var databaseName string
	if err := s.admin.Raw("SELECT current_database()").Scan(&databaseName).Error; err != nil {
		return err
	}
	if databaseName != "ccg_test" && !strings.HasSuffix(databaseName, "_test") {
		return fmt.Errorf("refusing to manage schemas in non-test database %q", databaseName)
	}
	return nil
}

// dsn points a connection string at this schema and nothing else.
// Every pooled connection needs the schema, and `SET search_path` only reaches the
// one session that ran it, so the schema travels in the DSN as a startup parameter.
// @intent make the private schema apply to every connection a pool opens, not just the first.
func (s *postgresSchema) dsn(baseDSN string) string {
	return withPostgresSearchPath(baseDSN, s.name)
}

// drop removes the schema and everything in it, naming only this schema.
// @intent leave a concurrently running test's schema untouched while removing this one.
func (s *postgresSchema) drop() error {
	return s.admin.Exec("DROP SCHEMA IF EXISTS " + s.name + " CASCADE").Error
}

// close drops the schema and releases the connection that managed it.
// @intent end the schema's life exactly once, reporting the drop failure ahead of the close failure.
func (s *postgresSchema) close() error {
	dropErr := s.drop()
	closeErr := s.closeAdmin()
	if dropErr != nil {
		return dropErr
	}
	return closeErr
}

// abort releases the admin connection after setup failed, keeping the original cause.
// @intent avoid leaking a connection when the schema never became usable.
func (s *postgresSchema) abort(cause error) error {
	_ = s.closeAdmin()
	return cause
}

// closeAdmin releases the schema's own connection.
func (s *postgresSchema) closeAdmin() error {
	sqlDB, err := s.admin.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

// postgresSchemaPrefix marks a schema as belonging to this test suite.
const postgresSchemaPrefix = "ccg_test_"

// newPostgresSchemaName builds a schema name unique across processes and machines.
// The creation time is part of the name because PostgreSQL does not record when a
// schema was created, and the stale sweep needs an age to decide on.
// @intent name a schema so that it cannot collide and so its age can be read back later.
func newPostgresSchemaName() string {
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		// crypto/rand does not fail in practice; the clock alone still separates runs.
		return fmt.Sprintf("%s%d_0", postgresSchemaPrefix, time.Now().Unix())
	}
	return fmt.Sprintf("%s%d_%s", postgresSchemaPrefix, time.Now().Unix(), hex.EncodeToString(suffix[:]))
}

// postgresSchemaNamePattern matches only the names newPostgresSchemaName produces, so
// the sweep can never act on a schema this suite did not create.
var postgresSchemaNamePattern = regexp.MustCompile(`^` + postgresSchemaPrefix + `(\d+)_[0-9a-f]+$`)

// postgresSchemaAge reports how old a suite schema is, from its name.
// @intent read a schema's age without a catalog column PostgreSQL does not have.
func postgresSchemaAge(name string, now time.Time) (time.Duration, bool) {
	match := postgresSchemaNamePattern.FindStringSubmatch(name)
	if match == nil {
		return 0, false
	}
	seconds, err := strconv.ParseInt(match[1], 10, 64)
	if err != nil {
		return 0, false
	}
	return now.Sub(time.Unix(seconds, 0)), true
}

// stalePostgresSchemaAge is the age past which a leftover schema is assumed abandoned.
// A test that dies without running its cleanup, from a panic that kills the process or
// a killed test binary, leaves its schema behind. The cutoff is far longer than any test
// run so a schema still in use by a concurrent test is never swept.
const stalePostgresSchemaAge = 2 * time.Hour

// sweepOnce keeps the sweep to one pass per test binary.
var sweepOnce sync.Once

// sweepStalePostgresSchemasOnce removes abandoned schemas the first time a test asks for one.
// @intent stop schemas from a crashed run piling up without touching a running test's schema.
// @sideEffect drops suite schemas older than stalePostgresSchemaAge.
func sweepStalePostgresSchemasOnce(admin *gorm.DB) {
	sweepOnce.Do(func() {
		// Best effort: a failed sweep must not fail the test that triggered it.
		_ = sweepStalePostgresSchemas(admin, stalePostgresSchemaAge, time.Now())
	})
}

// sweepStalePostgresSchemas drops suite schemas older than the given age.
// @intent bound how long a schema abandoned by a crashed run can survive.
// @sideEffect drops matching schemas.
func sweepStalePostgresSchemas(admin *gorm.DB, olderThan time.Duration, now time.Time) error {
	var names []string
	err := admin.Raw(
		"SELECT schema_name FROM information_schema.schemata WHERE schema_name LIKE ?",
		postgresSchemaPrefix+"%",
	).Scan(&names).Error
	if err != nil {
		return err
	}
	for _, name := range names {
		age, ok := postgresSchemaAge(name, now)
		if !ok || age <= olderThan {
			continue
		}
		if err := admin.Exec("DROP SCHEMA IF EXISTS " + name + " CASCADE").Error; err != nil {
			return err
		}
	}
	return nil
}

// withPostgresSearchPath adds a search_path to a DSN in either form libpq accepts.
// @intent carry the schema in the connection string whether the DSN is a URL or key=value pairs.
func withPostgresSearchPath(dsn, schema string) string {
	trimmed := strings.TrimSpace(dsn)
	if strings.HasPrefix(trimmed, "postgres://") || strings.HasPrefix(trimmed, "postgresql://") {
		parsed, err := url.Parse(trimmed)
		if err == nil {
			query := parsed.Query()
			query.Set("search_path", schema)
			parsed.RawQuery = query.Encode()
			return parsed.String()
		}
	}
	return trimmed + " search_path=" + schema
}

// isPostgresUnreachable reports whether an error means "no server here" rather than
// a real failure, so a developer without PostgreSQL still gets a skip instead of a
// failure while a misconfigured database still fails loudly.
// @intent keep the existing skip-when-absent behaviour without swallowing genuine errors.
func isPostgresUnreachable(err error) bool {
	if err == nil {
		return false
	}
	message := err.Error()
	for _, marker := range []string{
		"connection refused",
		"no such host",
		"connect: ",
		"i/o timeout",
		"failed to connect",
		"password authentication failed",
		"does not exist",
	} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}
