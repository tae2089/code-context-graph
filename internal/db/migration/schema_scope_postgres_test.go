//go:build postgres

package migration

import (
	"testing"

	"github.com/tae2089/code-context-graph/internal/db/dbtest"
	"gorm.io/gorm"
)

// The schema-parity checks have to answer about the schema the connection is using.
// A check pinned to public reports on somebody else's tables: it passes a deployment
// whose own schema is broken, and fails one that is fine. It also makes two tests in
// two schemas answer for each other.
//
// Every case asserts both directions, present then absent. A check pinned to public
// gives the same answer to both, whatever public happens to hold, so it cannot pass
// a pair. The objects are built by hand rather than migrated, to keep this about the
// checks themselves.
func TestPostgresSchemaChecks_AnswerAboutTheConnectionsOwnSchema(t *testing.T) {
	t.Parallel()
	db := dbtest.OpenIsolatedPostgres(t)

	var schema string
	if err := db.Raw("SELECT current_schema()").Scan(&schema).Error; err != nil {
		t.Fatalf("query current schema: %v", err)
	}
	if schema == "public" {
		t.Fatalf("current_schema() = %q, want a schema other than public", schema)
	}

	setup := []string{
		"CREATE TABLE nodes (name text NOT NULL, language text)",
		"CREATE TABLE search_documents (content text)",
		"CREATE INDEX idx_search_documents_tsv ON search_documents (content)",
		`CREATE FUNCTION search_documents_tsv() RETURNS trigger AS $$ BEGIN RETURN NEW; END $$ LANGUAGE plpgsql`,
		"CREATE TRIGGER trg_search_documents_tsv BEFORE INSERT ON search_documents FOR EACH ROW EXECUTE FUNCTION search_documents_tsv()",
	}
	for _, statement := range setup {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatalf("set up schema %q with %q: %v", schema, statement, err)
		}
	}

	t.Run("index", func(t *testing.T) {
		assertPostgresIndex(t, db, schema, true)
		if err := db.Exec("DROP INDEX idx_search_documents_tsv").Error; err != nil {
			t.Fatalf("drop index: %v", err)
		}
		assertPostgresIndex(t, db, schema, false)
	})

	t.Run("trigger", func(t *testing.T) {
		assertPostgresTrigger(t, db, schema, true)
		if err := db.Exec("DROP TRIGGER trg_search_documents_tsv ON search_documents").Error; err != nil {
			t.Fatalf("drop trigger: %v", err)
		}
		assertPostgresTrigger(t, db, schema, false)
	})

	t.Run("column nullability", func(t *testing.T) {
		notNull, err := PostgresColumnNotNull(db, "nodes", "name")
		if err != nil {
			t.Fatalf("check nullability: %v", err)
		}
		if !notNull {
			t.Fatalf("nodes.name reported nullable, but schema %q declares it NOT NULL", schema)
		}

		if err := db.Exec("ALTER TABLE nodes ALTER COLUMN name DROP NOT NULL").Error; err != nil {
			t.Fatalf("drop not null: %v", err)
		}
		notNull, err = PostgresColumnNotNull(db, "nodes", "name")
		if err != nil {
			t.Fatalf("re-check nullability: %v", err)
		}
		if notNull {
			t.Fatalf("nodes.name reported NOT NULL after the constraint was dropped in schema %q", schema)
		}
	})

	t.Run("column type", func(t *testing.T) {
		dataType, err := PostgresColumnDataType(db, "nodes", "language")
		if err != nil {
			t.Fatalf("check data type: %v", err)
		}
		if dataType != "text" {
			t.Fatalf("nodes.language type = %q in schema %q, want text", dataType, schema)
		}

		if err := db.Exec("ALTER TABLE nodes ALTER COLUMN language TYPE varchar(64)").Error; err != nil {
			t.Fatalf("narrow column: %v", err)
		}
		dataType, err = PostgresColumnDataType(db, "nodes", "language")
		if err != nil {
			t.Fatalf("re-check data type: %v", err)
		}
		if dataType != "character varying" {
			t.Fatalf("nodes.language type = %q after being narrowed in schema %q, want character varying", dataType, schema)
		}
	})
}

// assertPostgresIndex checks the index presence the parity check would see.
func assertPostgresIndex(t *testing.T, db *gorm.DB, schema string, want bool) {
	t.Helper()
	got, err := PostgresIndexExists(db, "idx_search_documents_tsv")
	if err != nil {
		t.Fatalf("check index: %v", err)
	}
	if got != want {
		t.Fatalf("PostgresIndexExists(idx_search_documents_tsv) = %v in schema %q, want %v", got, schema, want)
	}
}

// assertPostgresTrigger checks the trigger presence the parity check would see.
func assertPostgresTrigger(t *testing.T, db *gorm.DB, schema string, want bool) {
	t.Helper()
	got, err := PostgresTriggerExists(db, "trg_search_documents_tsv")
	if err != nil {
		t.Fatalf("check trigger: %v", err)
	}
	if got != want {
		t.Fatalf("PostgresTriggerExists(trg_search_documents_tsv) = %v in schema %q, want %v", got, schema, want)
	}
}
