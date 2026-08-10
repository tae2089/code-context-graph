package migration

import (
	"path/filepath"
	"strings"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// Adding a migration file is not enough on its own: a running binary refuses a
// database older than RequiredSchemaVersion, so the constant has to move with
// the highest migration. Pinning it to a literal makes forgetting that a test
// failure rather than a runtime surprise.
func TestRequiredSchemaVersion_MatchesHighestMigration(t *testing.T) {
	const highest = 19 // 000019_tokenize_identifier_separators
	if RequiredSchemaVersion != highest {
		t.Fatalf("RequiredSchemaVersion = %d, want %d", RequiredSchemaVersion, highest)
	}
}

func TestRequiredSchemaTables_IncludesOptimizationState(t *testing.T) {
	want := []string{"parse_cache_entries", "unresolved_edge_candidates", "unresolved_index_states"}
	got := make(map[string]struct{})
	for _, table := range RequiredSchemaTables() {
		got[table] = struct{}{}
	}
	for _, table := range want {
		if _, ok := got[table]; !ok {
			t.Errorf("RequiredSchemaTables missing %q", table)
		}
	}
}

func TestModelNullabilityColumns_IncludesOptimizationState(t *testing.T) {
	want := []SchemaColumn{
		{Table: "parse_cache_entries", Column: "payload"},
		{Table: "unresolved_edge_candidates", Column: "lookup_key"},
		{Table: "unresolved_edge_candidates", Column: "lookup_key_hash"},
		{Table: "unresolved_edge_candidates", Column: "fingerprint_hash"},
		{Table: "unresolved_index_states", Column: "namespace"},
		{Table: "unresolved_index_states", Column: "version"},
	}
	got := make(map[SchemaColumn]struct{})
	for _, column := range ModelNullabilityColumns() {
		got[column] = struct{}{}
	}
	for _, column := range want {
		if _, ok := got[column]; !ok {
			t.Errorf("ModelNullabilityColumns missing %+v", column)
		}
	}
}

func TestSQLiteMigrationEleven_InvalidatesUnresolvedIndexAndCanMigrateDown(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "migration.db")
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open SQLite: %v", err)
	}
	migrator, _, err := NewMigrator(db, "sqlite", "")
	if err != nil {
		t.Fatalf("NewMigrator: %v", err)
	}
	if err := migrator.Steps(10); err != nil {
		t.Fatalf("migrate to version 10: %v", err)
	}
	if err := db.Table("unresolved_edge_candidates").Create(map[string]any{
		"namespace": "repo", "lookup_key": "Target", "fingerprint": "calls:a.go:Target:1",
		"file_path": "a.go", "kind": "calls", "line": 1,
	}).Error; err != nil {
		t.Fatalf("insert version-10 candidate: %v", err)
	}
	if err := db.Table("unresolved_index_states").Create(map[string]any{
		"namespace": "repo", "version": "old",
	}).Error; err != nil {
		t.Fatalf("insert version-10 state: %v", err)
	}

	if err := migrator.Steps(1); err != nil {
		t.Fatalf("migrate to version 11: %v", err)
	}
	for _, table := range []string{"unresolved_edge_candidates", "unresolved_index_states"} {
		var count int64
		if err := db.Table(table).Count(&count).Error; err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if count != 0 {
			t.Fatalf("%s rows after migration = %d, want 0", table, count)
		}
	}
	for _, column := range []string{"lookup_key_hash", "fingerprint_hash"} {
		if !db.Migrator().HasColumn("unresolved_edge_candidates", column) {
			t.Fatalf("version 11 missing column %q", column)
		}
	}
	for _, indexName := range []string{"idx_unresolved_ns_fp_hash", "idx_unresolved_lookup_hash"} {
		exists, err := sqliteIndexExists(db, indexName)
		if err != nil {
			t.Fatalf("inspect index %q: %v", indexName, err)
		}
		if !exists {
			t.Fatalf("version 11 missing index %q", indexName)
		}
	}
	if err := migrator.Steps(-1); err != nil {
		t.Fatalf("migrate down to version 10: %v", err)
	}
	for _, column := range []string{"lookup_key_hash", "fingerprint_hash"} {
		if db.Migrator().HasColumn("unresolved_edge_candidates", column) {
			t.Fatalf("version 10 retained column %q", column)
		}
	}
}

func TestSQLiteMigrationThirteen_AddsResolverFileLookupIndexAndCanMigrateDown(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "migration.db")
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open SQLite: %v", err)
	}
	migrator, _, err := NewMigrator(db, "sqlite", "")
	if err != nil {
		t.Fatalf("NewMigrator: %v", err)
	}
	if err := migrator.Steps(13); err != nil {
		t.Fatalf("migrate to version 13: %v", err)
	}

	const indexName = "idx_nodes_ns_file_path"
	exists, err := sqliteIndexExists(db, indexName)
	if err != nil {
		t.Fatalf("inspect index %q: %v", indexName, err)
	}
	if !exists {
		t.Fatalf("version 13 missing index %q", indexName)
	}
	if err := migrator.Steps(-1); err != nil {
		t.Fatalf("migrate down to version 12: %v", err)
	}
	exists, err = sqliteIndexExists(db, indexName)
	if err != nil {
		t.Fatalf("inspect index %q after down: %v", indexName, err)
	}
	if exists {
		t.Fatalf("version 12 retained index %q", indexName)
	}
}

func TestSQLiteMigrationFifteen_CreatesCrossRefsAndCanMigrateDown(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "migration.db")
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open SQLite: %v", err)
	}
	migrator, _, err := NewMigrator(db, "sqlite", "")
	if err != nil {
		t.Fatalf("NewMigrator: %v", err)
	}
	if err := migrator.Steps(15); err != nil {
		t.Fatalf("migrate to version 15: %v", err)
	}

	if !db.Migrator().HasTable("cross_refs") {
		t.Fatal("version 15 missing table cross_refs")
	}
	for _, indexName := range []string{"idx_crossref_from_ns", "idx_crossref_to_ns", "idx_crossref_resolved_node"} {
		exists, err := sqliteIndexExists(db, indexName)
		if err != nil {
			t.Fatalf("inspect index %q: %v", indexName, err)
		}
		if !exists {
			t.Fatalf("version 15 missing index %q", indexName)
		}
	}
	if err := migrator.Steps(-1); err != nil {
		t.Fatalf("migrate down to version 14: %v", err)
	}
	if db.Migrator().HasTable("cross_refs") {
		t.Fatal("version 14 retained table cross_refs")
	}
}

// The recorded reason for a node lives beside its name text rather than in a
// table of its own. Both are derived from the same node in the same refresh, so
// one row cannot drift from the other; two tables could. The separation callers
// asked for is between the two *indexes*, and that happens above this column —
// the name index reads `content`, the intent index reads `intent_content`, and
// neither sees the other's text.
func TestSQLiteMigrationSeventeen_AddsIntentContentAndCanMigrateDown(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "migration.db")
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open SQLite: %v", err)
	}
	migrator, _, err := NewMigrator(db, "sqlite", "")
	if err != nil {
		t.Fatalf("NewMigrator: %v", err)
	}
	if err := migrator.Steps(17); err != nil {
		t.Fatalf("migrate to version 17: %v", err)
	}

	if !sqliteHasColumn(t, db, "search_documents", "intent_content") {
		t.Fatal("version 17 missing column search_documents.intent_content")
	}
	if err := migrator.Steps(-1); err != nil {
		t.Fatalf("migrate down to version 16: %v", err)
	}
	if sqliteHasColumn(t, db, "search_documents", "intent_content") {
		t.Fatal("version 16 retained column search_documents.intent_content")
	}
}

// The intent index is only reachable through the numbered migrations. Nothing
// in the CLI calls SQLiteBackend.Migrate, so a table that only that method
// creates does not exist by the time `ccg build` rebuilds the search index, and
// the build dies with "no such table: intent_fts".
func TestSQLiteMigrationEighteen_AddsIntentFTSAndCanMigrateDown(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "migration.db")
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open SQLite: %v", err)
	}
	migrator, _, err := NewMigrator(db, "sqlite", "")
	if err != nil {
		t.Fatalf("NewMigrator: %v", err)
	}
	if err := migrator.Steps(18); err != nil {
		t.Fatalf("migrate to version 18: %v", err)
	}

	if !sqliteHasTable(t, db, "intent_fts") {
		t.Fatal("version 18 missing table intent_fts")
	}
	if err := migrator.Steps(-1); err != nil {
		t.Fatalf("migrate down to version 17: %v", err)
	}
	if sqliteHasTable(t, db, "intent_fts") {
		t.Fatal("version 17 retained table intent_fts")
	}
}

func sqliteHasTable(t *testing.T, db *gorm.DB, table string) bool {
	t.Helper()
	return db.Migrator().HasTable(table)
}

func sqliteHasColumn(t *testing.T, db *gorm.DB, table, column string) bool {
	t.Helper()
	columns, err := db.Migrator().ColumnTypes(table)
	if err != nil {
		t.Fatalf("inspect %s: %v", table, err)
	}
	for _, candidate := range columns {
		if strings.EqualFold(candidate.Name(), column) {
			return true
		}
	}
	return false
}

func TestSQLiteMigrationFourteen_PreservesTextColumnsAndCanMigrateDown(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "migration.db")
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open SQLite: %v", err)
	}
	migrator, _, err := NewMigrator(db, "sqlite", "")
	if err != nil {
		t.Fatalf("NewMigrator: %v", err)
	}
	if err := migrator.Steps(14); err != nil {
		t.Fatalf("migrate to version 14: %v", err)
	}

	columns, err := db.Migrator().ColumnTypes("nodes")
	if err != nil {
		t.Fatalf("inspect nodes.name: %v", err)
	}
	foundName := false
	for _, column := range columns {
		if column.Name() != "name" {
			continue
		}
		foundName = true
		if !strings.EqualFold(column.DatabaseTypeName(), "text") {
			t.Fatalf("nodes.name type = %q, want TEXT", column.DatabaseTypeName())
		}
	}
	if !foundName {
		t.Fatal("nodes.name column is missing")
	}
	if err := migrator.Steps(-1); err != nil {
		t.Fatalf("migrate down to version 13: %v", err)
	}
}
