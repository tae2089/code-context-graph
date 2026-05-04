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
// @intent sql.DB와 테스트 대역 모두에 적용할 풀 설정 API를 추상화한다.
type SQLDBPool interface {
	SetMaxOpenConns(int)
	SetMaxIdleConns(int)
	SetConnMaxLifetime(time.Duration)
	SetConnMaxIdleTime(time.Duration)
}

// Open creates a configured GORM connection for sqlite or postgres.
// @intent sqlite/postgres별 공통 초기화와 풀 설정을 한 진입점으로 묶는다.
// @sideEffect sqlite 경로에서는 WAL과 busy timeout을 설정한다.
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
// @intent 드라이버 특성에 맞는 connection pool 한도를 적용한다.
// @domainRule sqlite는 단일 writer 특성을 고려해 MaxOpenConns=1로 고정한다.
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
// @intent 현재 DB 드라이버에 맞는 전문 검색 backend 구현체를 선택한다.
func NewSearchBackend(driver string) search.Backend {
	switch driver {
	case "postgres":
		return search.NewPostgresBackend()
	default:
		return search.NewSQLiteBackend()
	}
}
