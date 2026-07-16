package migration

import (
	"path/filepath"
	"strings"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestRequiredSchemaVersion_IncludesUnboundedGraphStrings(t *testing.T) {
	if RequiredSchemaVersion != 14 {
		t.Fatalf("RequiredSchemaVersion = %d, want 14", RequiredSchemaVersion)
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
