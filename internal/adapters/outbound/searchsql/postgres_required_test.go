//go:build postgres

package searchsql

import (
	"context"
	"os"
	"testing"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/tae2089/code-context-graph/internal/db/dbtest"
)

// TestPostgresIsReachable fails a run that asked for PostgreSQL and did not get
// it.
//
// Every other postgres-tagged test calls t.Skipf when the database is missing.
// That is right on a laptop and wrong in CI: with no server the whole tagged
// suite reports ok having exercised nothing, which is indistinguishable from a
// suite that ran and passed. The backend parity test the development guide
// cites is one of the tests that would vanish that way.
//
// REQUIRE_POSTGRES turns the silence into a failure. The CI job that runs these
// tags sets it; a laptop that does not set it keeps the skipping behaviour.
// @intent stop a postgres-tagged run from reporting success without a database.
// @domainRule with REQUIRE_POSTGRES unset this test skips, so local runs are unaffected.
func TestPostgresIsReachable(t *testing.T) {
	if os.Getenv("REQUIRE_POSTGRES") == "" {
		t.Skip("REQUIRE_POSTGRES unset: PostgreSQL is optional for this run")
	}

	db, err := gorm.Open(postgres.Open(dbtest.PostgresDSN()), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("REQUIRE_POSTGRES is set but PostgreSQL could not be opened, so every postgres-tagged test would have skipped: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("REQUIRE_POSTGRES is set but the connection pool is unusable: %v", err)
	}
	defer sqlDB.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := sqlDB.PingContext(ctx); err != nil {
		t.Fatalf("REQUIRE_POSTGRES is set but PostgreSQL did not answer a ping, so every postgres-tagged test would have skipped: %v", err)
	}
}
