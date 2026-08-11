//go:build fts5

package searchsql

import (
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// This file is the evidence behind one line of guide/development.md §Raw SQL:
// that the schema introspection in this package cannot go through GORM's
// migrator. GORM does ship HasTable, HasColumn and ColumnTypes, so the exemption
// would be a bare assertion without a test that runs all three against a real
// FTS5 virtual table and records what they do.
//
// If a GORM upgrade makes one of these fail, the right response is to
// reconsider the exemption for that call, not to relax the test.

// newVirtualTableProbeDB opens an in-memory database holding the FTS5 table this
// package creates in production, plus a second one shaped like the pre-namespace
// legacy table.
func newVirtualTableProbeDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := createSQLiteFTSTable(db, sqliteFTSTable, true); err != nil {
		t.Fatalf("create %s: %v", sqliteFTSTable, err)
	}
	if err := db.Exec(
		"CREATE VIRTUAL TABLE legacy_probe USING fts5(node_id UNINDEXED, content, language)",
	).Error; err != nil {
		t.Fatalf("create legacy_probe: %v", err)
	}
	return db
}

// TestMigratorHasTableSeesFTS5VirtualTables records that table presence is the
// one introspection GORM answers correctly here — and why sqliteTableExists
// keeps its raw statement anyway: HasTable returns a bool with no error, while
// every caller of sqliteTableExists propagates one. Swallowing that error would
// read a transient failure as "the table is absent", which in the legacy-upgrade
// path is the difference between stopping and rebuilding.
func TestMigratorHasTableSeesFTS5VirtualTables(t *testing.T) {
	db := newVirtualTableProbeDB(t)

	if !db.Migrator().HasTable(sqliteFTSTable) {
		t.Errorf("HasTable(%q) = false, want true: the virtual table was just created", sqliteFTSTable)
	}
	if db.Migrator().HasTable("no_such_table") {
		t.Error("HasTable(\"no_such_table\") = true, want false")
	}

	exists, err := sqliteTableExists(db, sqliteFTSTable)
	if err != nil || !exists {
		t.Errorf("sqliteTableExists(%q) = (%v, %v), want (true, nil)", sqliteFTSTable, exists, err)
	}
}

// TestMigratorColumnTypesRejectsFTS5VirtualTables records that ColumnTypes
// cannot describe a virtual table at all: it parses the stored DDL, and
// CREATE VIRTUAL TABLE is not a shape it can parse.
func TestMigratorColumnTypesRejectsFTS5VirtualTables(t *testing.T) {
	db := newVirtualTableProbeDB(t)

	types, err := db.Migrator().ColumnTypes(sqliteFTSTable)
	if err == nil {
		t.Fatalf("ColumnTypes(%q) succeeded with %d columns, want an error", sqliteFTSTable, len(types))
	}
	if got := err.Error(); got != "invalid DDL" {
		t.Errorf("ColumnTypes(%q) error = %q, want %q", sqliteFTSTable, got, "invalid DDL")
	}
}

// TestMigratorHasColumnMisreadsFTS5VirtualTables is the reason PRAGMA table_info
// stays. HasColumn matches patterns against the stored DDL text rather than
// asking the schema, so whether it finds a column depends on how that column
// happens to be spelled in the CREATE statement. `content` is a real column on
// legacy_probe and HasColumn says it is not there.
func TestMigratorHasColumnMisreadsFTS5VirtualTables(t *testing.T) {
	db := newVirtualTableProbeDB(t)

	present, err := sqliteColumnExists(db, "legacy_probe", "content")
	if err != nil {
		t.Fatalf("sqliteColumnExists: %v", err)
	}
	if !present {
		t.Fatal("PRAGMA table_info does not see legacy_probe.content; the fixture is wrong, not GORM")
	}

	if db.Migrator().HasColumn("legacy_probe", "content") {
		t.Error("HasColumn now agrees with the schema on this table. GORM may have gained real " +
			"virtual-table introspection: reconsider the exemption in guide/development.md §Raw SQL " +
			"rather than relaxing this test")
	}
}
