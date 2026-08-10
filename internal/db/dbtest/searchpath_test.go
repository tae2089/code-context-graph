package dbtest

import (
	"testing"
	"time"
)

func TestWithPostgresSearchPath(t *testing.T) {
	cases := []struct {
		name   string
		dsn    string
		schema string
		want   string
	}{
		{
			name:   "key value dsn gains a search_path pair",
			dsn:    "host=localhost user=postgres dbname=ccg_test port=5432 sslmode=disable",
			schema: "ccg_test_1_ab",
			want:   "host=localhost user=postgres dbname=ccg_test port=5432 sslmode=disable search_path=ccg_test_1_ab",
		},
		{
			name:   "surrounding whitespace does not produce a broken pair",
			dsn:    "  host=localhost dbname=ccg_test  ",
			schema: "ccg_test_1_ab",
			want:   "host=localhost dbname=ccg_test search_path=ccg_test_1_ab",
		},
		{
			name:   "url dsn gains a search_path query parameter",
			dsn:    "postgres://postgres@localhost:5432/ccg_test",
			schema: "ccg_test_1_ab",
			want:   "postgres://postgres@localhost:5432/ccg_test?search_path=ccg_test_1_ab",
		},
		{
			name:   "url dsn keeps the parameters it already had",
			dsn:    "postgresql://postgres@localhost:5432/ccg_test?sslmode=disable",
			schema: "ccg_test_1_ab",
			want:   "postgresql://postgres@localhost:5432/ccg_test?search_path=ccg_test_1_ab&sslmode=disable",
		},
		{
			name:   "url dsn search_path is replaced, not appended twice",
			dsn:    "postgres://postgres@localhost:5432/ccg_test?search_path=public",
			schema: "ccg_test_1_ab",
			want:   "postgres://postgres@localhost:5432/ccg_test?search_path=ccg_test_1_ab",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := withPostgresSearchPath(tc.dsn, tc.schema); got != tc.want {
				t.Fatalf("withPostgresSearchPath(%q, %q) = %q, want %q", tc.dsn, tc.schema, got, tc.want)
			}
		})
	}
}

func TestNewPostgresSchemaName_IsUniqueAndReadableAsAnAge(t *testing.T) {
	first := newPostgresSchemaName()
	second := newPostgresSchemaName()
	if first == second {
		t.Fatalf("two generated names are equal: %q", first)
	}
	if len(first) > 63 {
		t.Fatalf("schema name %q is %d bytes, PostgreSQL truncates past 63", first, len(first))
	}
	age, ok := postgresSchemaAge(first, time.Now())
	if !ok {
		t.Fatalf("generated name %q is not recognised as a suite schema", first)
	}
	if age < 0 || age > time.Minute {
		t.Fatalf("age of a just-created schema = %v, want a value near zero", age)
	}
}

func TestPostgresSchemaAge_OnlyRecognisesSuiteSchemas(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	cases := []struct {
		name    string
		schema  string
		wantAge time.Duration
		wantOK  bool
	}{
		{name: "suite schema", schema: "ccg_test_999000_ab12cd34", wantAge: 1000 * time.Second, wantOK: true},
		{name: "public is never a suite schema", schema: "public", wantOK: false},
		{name: "another product's schema", schema: "tenant_ccg_test_999000_ab", wantOK: false},
		{name: "suite prefix without a timestamp", schema: "ccg_test_abc_ab12", wantOK: false},
		{name: "suite prefix without a random suffix", schema: "ccg_test_999000_", wantOK: false},
		{name: "non-hex suffix", schema: "ccg_test_999000_zz", wantOK: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			age, ok := postgresSchemaAge(tc.schema, now)
			if ok != tc.wantOK {
				t.Fatalf("postgresSchemaAge(%q) ok = %v, want %v", tc.schema, ok, tc.wantOK)
			}
			if ok && age != tc.wantAge {
				t.Fatalf("postgresSchemaAge(%q) age = %v, want %v", tc.schema, age, tc.wantAge)
			}
		})
	}
}
