// @index Database connection, SQLite WAL/busy_timeout, connection pool, and search-backend selection helpers for SQLite and PostgreSQL.
package db

import (
	"time"

	"github.com/tae2089/trace"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	search "github.com/tae2089/code-context-graph/internal/adapters/outbound/searchsql"
)

// SQLDBPool abstracts configurable SQL connection pool knobs.
// @intent abstract the pool configuration API so both real sql.DB handles and test doubles can share the same seam.
type SQLDBPool interface {
	SetMaxOpenConns(int)
	SetMaxIdleConns(int)
	SetConnMaxLifetime(time.Duration)
	SetConnMaxIdleTime(time.Duration)
}

// Open creates a configured GORM connection for sqlite or postgres.
// @intent centralize driver-specific GORM initialization and pool setup behind one entry point.
// @requires driver must be sqlite or postgres and dsn must match the selected driver.
// @ensures returns a configured GORM connection with pool settings applied for the selected driver.
// @sideEffect enables SQLite WAL and busy_timeout pragmas when driver is sqlite.
func Open(driver, dsn string) (*gorm.DB, error) {
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
		return nil, trace.New("unsupported database driver: " + driver)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, trace.Wrap(err, "get underlying sql.DB")
	}
	ConfigurePool(sqlDB, driver)

	return db, nil
}

// ConfigurePool applies database-specific connection pool settings.
// @intent apply connection-pool limits that match each database driver's concurrency model.
// @domainRule sqlite is pinned to MaxOpenConns=1 because it supports only one writer at a time and FTS query workloads share the same DB file.
// @mutates sqlDB connection-pool settings.
func ConfigurePool(sqlDB SQLDBPool, driver string) {
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

// NewSearchBackend selects the search backend for the database driver.
// @intent select the full-text search backend implementation that matches the active database driver.
// @domainRule postgres uses the PostgreSQL backend and every other driver falls back to the SQLite backend.
func NewSearchBackend(driver string) search.Backend {
	switch driver {
	case "postgres":
		return search.NewPostgresBackend()
	default:
		return search.NewSQLiteBackend()
	}
}
