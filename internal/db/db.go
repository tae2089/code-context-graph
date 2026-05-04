package db

import (
	"time"

	"github.com/tae2089/trace"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/tae2089/code-context-graph/internal/store/search"
)

// SQLDBPool abstracts configurable SQL connection pool knobs.
type SQLDBPool interface {
	SetMaxOpenConns(int)
	SetMaxIdleConns(int)
	SetConnMaxLifetime(time.Duration)
	SetConnMaxIdleTime(time.Duration)
}

// Open creates a configured GORM connection for sqlite or postgres.
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
func NewSearchBackend(driver string) search.Backend {
	switch driver {
	case "postgres":
		return search.NewPostgresBackend()
	default:
		return search.NewSQLiteBackend()
	}
}
