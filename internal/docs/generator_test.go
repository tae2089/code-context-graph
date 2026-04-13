package docs_test

import (
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/imtaebin/code-context-graph/internal/docs"
	"github.com/imtaebin/code-context-graph/internal/store/gormstore"
)

func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	st := gormstore.New(db)
	if err := st.AutoMigrate(); err != nil {
		t.Fatal(err)
	}
	return db
}

func newGenerator(t *testing.T, db *gorm.DB) (*docs.Generator, string) {
	t.Helper()
	outDir := t.TempDir()
	return &docs.Generator{DB: db, OutDir: outDir}, outDir
}

func TestRun_EmptyDB(t *testing.T) {
	db := newTestDB(t)
	gen, _ := newGenerator(t, db)
	if err := gen.Run(); err != nil {
		t.Fatalf("unexpected error on empty DB: %v", err)
	}
}
