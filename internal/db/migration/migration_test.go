package migration

import "testing"

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
