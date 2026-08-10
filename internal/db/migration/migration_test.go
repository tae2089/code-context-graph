package migration

import (
	"path/filepath"
	"slices"
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
	const highest = 20 // 000020_reason_documents
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

// Version 17 put the recorded reason beside the name text, in a column of
// search_documents. Version 20 moved it into search_reasons because one column
// can only hold one document per node, and joining a node's reasons into it made
// each reason score as if it were as long as all of them together. This test
// still pins 17 on its own terms: what the column was, and that stepping back to
// 16 removes it.
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

// Version 20 splits the joined intent_content column into one search_reasons row
// per recorded reason, and reloads intent_fts from it. The backfill has to read
// the annotation tags rather than the joined column, because joined text cannot
// be cut back into the tags it came from — so this test writes tags that would
// be indistinguishable once joined and checks they come out as separate rows.
func TestSQLiteMigrationTwenty_SplitsReasonsIntoRowsAndCanMigrateDown(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "migration.db")
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open SQLite: %v", err)
	}
	migrator, _, err := NewMigrator(db, "sqlite", "")
	if err != nil {
		t.Fatalf("NewMigrator: %v", err)
	}
	if err := migrator.Steps(19); err != nil {
		t.Fatalf("migrate to version 19: %v", err)
	}
	seedVersionNineteenReasons(t, db)

	if err := migrator.Steps(1); err != nil {
		t.Fatalf("migrate to version 20: %v", err)
	}
	if sqliteHasColumn(t, db, "search_documents", "intent_content") {
		t.Fatal("version 20 retained column search_documents.intent_content")
	}
	want := []string{
		"decide which push may trigger a build",
		"only allowlisted repositories are admitted",
		"a tag push never starts a build",
	}
	if got := reasonContents(t, db, "SELECT content FROM search_reasons ORDER BY id"); !slices.Equal(got, want) {
		t.Errorf("search_reasons = %v, want %v", got, want)
	}
	// A second @intent is a writing mistake, not a list, and only the first one is
	// ever displayed. Indexing the rest would make text findable that can never be
	// shown as the reason it was found by.
	if got := reasonContents(t, db, "SELECT content FROM search_reasons WHERE content LIKE 'a second purpose%'"); len(got) != 0 {
		t.Errorf("the second @intent reached the index as %v", got)
	}
	if got := reasonContents(t, db, "SELECT content FROM intent_fts ORDER BY rowid"); !slices.Equal(got, want) {
		t.Errorf("intent_fts = %v, want %v", got, want)
	}

	if err := migrator.Steps(-1); err != nil {
		t.Fatalf("migrate down to version 19: %v", err)
	}
	if !sqliteHasColumn(t, db, "search_documents", "intent_content") {
		t.Fatal("version 19 missing column search_documents.intent_content")
	}
	if sqliteHasTable(t, db, "search_reasons") {
		t.Fatal("version 19 retained table search_reasons")
	}
	rejoined := reasonContents(t, db, "SELECT intent_content FROM search_documents ORDER BY id")
	if len(rejoined) != 1 || rejoined[0] != strings.Join(want, " ") {
		t.Errorf("rejoined intent_content = %v, want %q", rejoined, strings.Join(want, " "))
	}
}

// seedVersionNineteenReasons writes one annotated declaration straight into the
// version-19 tables. The models cannot be used here: they describe the current
// schema, and the point is to start from the old one.
func seedVersionNineteenReasons(t *testing.T, db *gorm.DB) {
	t.Helper()
	statements := []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO nodes (id, namespace, qualified_name, kind, name, file_path, start_line, end_line, language)
		  VALUES (1, 'default', 'webhook.admitRepo', 'function', 'admitRepo', 'webhook/admit.go', 1, 10, 'go')`, nil},
		{`INSERT INTO annotations (id, node_id) VALUES (1, 1)`, nil},
		{`INSERT INTO doc_tags (annotation_id, kind, value, ordinal) VALUES
		  (1, 'intent', 'decide which push may trigger a build', 0),
		  (1, 'domainRule', 'only allowlisted repositories are admitted', 1),
		  (1, 'sideEffect', 'writes an admission record', 2),
		  (1, 'domainRule', 'a tag push never starts a build', 3),
		  (1, 'intent', 'a second purpose nobody will ever be shown', 4)`, nil},
		{`INSERT INTO search_documents (namespace, node_id, content, intent_content, language)
		  VALUES ('default', 1, 'admitRepo checks repository allowlist', ?, 'go')`, []any{
			"decide which push may trigger a build only allowlisted repositories are admitted a tag push never starts a build",
		}},
		{`INSERT INTO intent_fts (node_id, content, namespace)
		  SELECT node_id, intent_content, namespace FROM search_documents`, nil},
	}
	for _, statement := range statements {
		if err := db.Exec(statement.sql, statement.args...).Error; err != nil {
			t.Fatalf("seed version 19 row: %v", err)
		}
	}
}

func reasonContents(t *testing.T, db *gorm.DB, query string) []string {
	t.Helper()
	var out []string
	if err := db.Raw(query).Scan(&out).Error; err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return out
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
