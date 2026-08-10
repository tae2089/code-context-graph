//go:build postgres

package migration

import (
	"slices"
	"strings"
	"testing"

	gomigrate "github.com/golang-migrate/migrate/v4"
	"gorm.io/gorm"

	"github.com/tae2089/code-context-graph/internal/db/dbtest"
)

func openPostgresAtVersion(t *testing.T, version int) (*gorm.DB, *gomigrate.Migrate) {
	t.Helper()
	db := dbtest.OpenIsolatedPostgres(t)
	migrator, _, err := NewMigrator(db, "postgres", "")
	if err != nil {
		t.Fatalf("NewMigrator: %v", err)
	}
	if err := migrator.Steps(version); err != nil {
		t.Fatalf("migrate to version %d: %v", version, err)
	}
	return db, migrator
}

// The PostgreSQL half of TestSQLiteMigrationTwenty. The two backends do the
// split with different SQL — string_agg and a tsvector trigger here, group_concat
// and an FTS5 table there — so passing on SQLite says nothing about this one, and
// this is the migration that rewrites an existing user's intent index in place.
func TestPostgresMigrationTwenty_SplitsReasonsIntoRowsAndCanMigrateDown(t *testing.T) {
	db, migrator := openPostgresAtVersion(t, 19)
	seedPostgresVersionNineteenReasons(t, db)

	if err := migrator.Steps(1); err != nil {
		t.Fatalf("migrate to version 20: %v", err)
	}
	if postgresHasColumn(t, db, "search_documents", "intent_content") {
		t.Fatal("version 20 retained column search_documents.intent_content")
	}
	want := []string{
		"decide which push may trigger a build",
		"only allowlisted repositories are admitted",
		"a tag push never starts a build",
	}
	if got := postgresStrings(t, db, "SELECT content FROM search_reasons ORDER BY id"); !slices.Equal(got, want) {
		t.Errorf("search_reasons = %v, want %v", got, want)
	}
	// A second @intent is a writing mistake, not a list, and only the first one is
	// ever displayed. Indexing the rest would make text findable that can never be
	// shown as the reason it was found by.
	if got := postgresStrings(t, db, "SELECT content FROM search_reasons WHERE content LIKE 'a second purpose%'"); len(got) != 0 {
		t.Errorf("the second @intent reached the index as %v", got)
	}
	// The trigger has to have fired on the backfill, or the rows exist and nothing
	// can find them.
	if got := postgresStrings(t, db, "SELECT content FROM search_reasons WHERE reason_tsv @@ plainto_tsquery('simple', 'allowlisted')"); len(got) != 1 {
		t.Errorf("reason_tsv search for 'allowlisted' returned %v, want the one rule that says it", got)
	}

	if err := migrator.Steps(-1); err != nil {
		t.Fatalf("migrate down to version 19: %v", err)
	}
	if !postgresHasColumn(t, db, "search_documents", "intent_content") {
		t.Fatal("version 19 missing column search_documents.intent_content")
	}
	if db.Migrator().HasTable("search_reasons") {
		t.Fatal("version 19 retained table search_reasons")
	}
	rejoined := postgresStrings(t, db, "SELECT intent_content FROM search_documents ORDER BY id")
	if len(rejoined) != 1 || rejoined[0] != strings.Join(want, " ") {
		t.Errorf("rejoined intent_content = %v, want %q", rejoined, strings.Join(want, " "))
	}
	if got := postgresStrings(t, db, "SELECT intent_content FROM search_documents WHERE intent_tsv @@ plainto_tsquery('simple', 'allowlisted')"); len(got) != 1 {
		t.Errorf("intent_tsv search for 'allowlisted' returned %v, want the rejoined document", got)
	}
}

// seedPostgresVersionNineteenReasons writes one annotated declaration straight
// into the version-19 tables. The models cannot be used here: they describe the
// current schema, and the point is to start from the old one.
func seedPostgresVersionNineteenReasons(t *testing.T, db *gorm.DB) {
	t.Helper()
	statements := []string{
		`INSERT INTO nodes (id, namespace, qualified_name, kind, name, file_path, start_line, end_line, language)
		 VALUES (1, 'default', 'webhook.admitRepo', 'function', 'admitRepo', 'webhook/admit.go', 1, 10, 'go')`,
		`INSERT INTO annotations (id, node_id) VALUES (1, 1)`,
		`INSERT INTO doc_tags (annotation_id, kind, value, ordinal) VALUES
		 (1, 'intent', 'decide which push may trigger a build', 0),
		 (1, 'domainRule', 'only allowlisted repositories are admitted', 1),
		 (1, 'sideEffect', 'writes an admission record', 2),
		 (1, 'domainRule', 'a tag push never starts a build', 3),
		 (1, 'intent', 'a second purpose nobody will ever be shown', 4)`,
		`INSERT INTO search_documents (namespace, node_id, content, intent_content, language)
		 VALUES ('default', 1, 'admitRepo checks repository allowlist',
		         'decide which push may trigger a build only allowlisted repositories are admitted a tag push never starts a build',
		         'go')`,
	}
	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatalf("seed version 19 row: %v", err)
		}
	}
}

func postgresStrings(t *testing.T, db *gorm.DB, query string) []string {
	t.Helper()
	var out []string
	if err := db.Raw(query).Scan(&out).Error; err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return out
}

func postgresHasColumn(t *testing.T, db *gorm.DB, table, column string) bool {
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
