package eval

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func TestLoadQueryCorpus(t *testing.T) {
	dir := t.TempDir()
	qc := QueryCorpus{
		CorpusDir: "testdata/eval/go",
		Queries: []QueryCase{
			{Query: "Hello", Relevant: []string{"function:Hello@sample.go"}, K: 5},
			{Query: "auth", Relevant: []string{"function:Authenticate@auth.go"}},
		},
	}
	data, _ := json.MarshalIndent(qc, "", "  ")
	os.WriteFile(dir+"/queries.json", data, 0o644)

	loaded, err := LoadQueryCorpus(dir + "/queries.json")
	if err != nil {
		t.Fatalf("LoadQueryCorpus: %v", err)
	}
	if len(loaded.Queries) != 2 {
		t.Fatalf("queries: got %d, want 2", len(loaded.Queries))
	}
	if loaded.Queries[0].K != 5 {
		t.Errorf("K: got %d, want 5", loaded.Queries[0].K)
	}
}

func TestLoadQueryCorpus_IncludesJavaCase(t *testing.T) {
	loaded, err := LoadQueryCorpus(filepath.Join(repoRoot(t), "testdata", "eval", "queries.json"))
	if err != nil {
		t.Fatalf("LoadQueryCorpus: %v", err)
	}
	if len(loaded.Queries) != 15 {
		t.Fatalf("queries: got %d, want 15", len(loaded.Queries))
	}
	if loaded.Queries[5].Relevant[0] != "class:UserService@Sample.java" {
		t.Fatalf("relevant: got %q, want %q", loaded.Queries[5].Relevant[0], "class:UserService@Sample.java")
	}
}

func TestLoadQueryCorpus_IncludesRustCase(t *testing.T) {
	loaded, err := LoadQueryCorpus(filepath.Join(repoRoot(t), "testdata", "eval", "queries.json"))
	if err != nil {
		t.Fatalf("LoadQueryCorpus: %v", err)
	}
	if len(loaded.Queries) != 15 {
		t.Fatalf("queries: got %d, want 15", len(loaded.Queries))
	}
	if loaded.Queries[6].Relevant[0] != "function:get_user@sample.rs" {
		t.Fatalf("relevant: got %q, want %q", loaded.Queries[6].Relevant[0], "function:get_user@sample.rs")
	}
}

func TestLoadQueryCorpus_IncludesJavaScriptCase(t *testing.T) {
	loaded, err := LoadQueryCorpus(filepath.Join(repoRoot(t), "testdata", "eval", "queries.json"))
	if err != nil {
		t.Fatalf("LoadQueryCorpus: %v", err)
	}
	if len(loaded.Queries) != 15 {
		t.Fatalf("queries: got %d, want 15", len(loaded.Queries))
	}
	if loaded.Queries[7].Relevant[0] != "function:getUser@sample.js" {
		t.Fatalf("relevant: got %q, want %q", loaded.Queries[7].Relevant[0], "function:getUser@sample.js")
	}
}

func TestLoadQueryCorpus_IncludesKotlinCase(t *testing.T) {
	loaded, err := LoadQueryCorpus(filepath.Join(repoRoot(t), "testdata", "eval", "queries.json"))
	if err != nil {
		t.Fatalf("LoadQueryCorpus: %v", err)
	}
	if len(loaded.Queries) != 15 {
		t.Fatalf("queries: got %d, want 15", len(loaded.Queries))
	}
	for _, q := range loaded.Queries {
		if hasRelevant(q.Relevant, "function:getUser@Sample.kt") {
			return
		}
	}
	t.Fatal("missing Kotlin relevant ID function:getUser@Sample.kt")
}

func TestLoadQueryCorpus_IncludesPHPCase(t *testing.T) {
	loaded, err := LoadQueryCorpus(filepath.Join(repoRoot(t), "testdata", "eval", "queries.json"))
	if err != nil {
		t.Fatalf("LoadQueryCorpus: %v", err)
	}
	if len(loaded.Queries) != 15 {
		t.Fatalf("queries: got %d, want 15", len(loaded.Queries))
	}
	for _, q := range loaded.Queries {
		if hasRelevant(q.Relevant, "function:getUser@sample.php") {
			return
		}
	}
	t.Fatal("missing PHP relevant ID function:getUser@sample.php")
}

func TestLoadQueryCorpus_CoversRemainingLanguages(t *testing.T) {
	loaded, err := LoadQueryCorpus(filepath.Join(repoRoot(t), "testdata", "eval", "queries.json"))
	if err != nil {
		t.Fatalf("LoadQueryCorpus: %v", err)
	}
	if len(loaded.Queries) != 15 {
		t.Fatalf("queries: got %d, want 15", len(loaded.Queries))
	}

	wants := []string{
		"function:get_user@sample.rb",
		"function:get_user@sample.c",
		"function:getUser@sample.cpp",
		"function:UserService:getUser@sample.lua",
	}
	for _, want := range wants {
		found := false
		for _, q := range loaded.Queries {
			if hasRelevant(q.Relevant, want) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("missing relevant ID %s", want)
		}
	}
}

func TestLoadQueryCorpus_UsesFixtureAlignedQueryTexts(t *testing.T) {
	loaded, err := LoadQueryCorpus(filepath.Join(repoRoot(t), "testdata", "eval", "queries.json"))
	if err != nil {
		t.Fatalf("LoadQueryCorpus: %v", err)
	}

	var python, typescript, rust, javascript bool
	for _, q := range loaded.Queries {
		switch q.Query {
		case "get_user":
			python = hasRelevant(q.Relevant, "function:get_user@sample.py")
		case "getUser":
			typescript = hasRelevant(q.Relevant, "function:getUser@sample.ts")
		case "get_user Rust":
			rust = hasRelevant(q.Relevant, "function:get_user@sample.rs")
		case "getUser JavaScript":
			javascript = hasRelevant(q.Relevant, "function:getUser@sample.js")
		}
	}

	if !python {
		t.Fatal("missing aligned Python query case")
	}
	if !typescript {
		t.Fatal("missing aligned TypeScript query case")
	}
	if !rust {
		t.Fatal("missing aligned Rust query case")
	}
	if !javascript {
		t.Fatal("missing aligned JavaScript query case")
	}
}

func TestLoadQueryCorpus_BareNameQueriesAreMultiRelevant(t *testing.T) {
	loaded, err := LoadQueryCorpus(filepath.Join(repoRoot(t), "testdata", "eval", "queries.json"))
	if err != nil {
		t.Fatalf("LoadQueryCorpus: %v", err)
	}

	expectations := map[string][]string{
		"UserService": {
			"class:UserService@sample.go",
			"class:UserService@Sample.java",
			"class:UserService@sample.php",
			"class:UserService@sample.cpp",
		},
		"get_user": {
			"function:get_user@sample.py",
			"function:get_user@sample.rs",
			"function:get_user@sample.rb",
			"function:get_user@sample.c",
		},
		"getUser": {
			"function:getUser@sample.ts",
			"function:getUser@sample.js",
			"function:getUser@Sample.kt",
			"function:getUser@sample.php",
			"function:getUser@sample.cpp",
		},
	}

	for query, wants := range expectations {
		var got []string
		for _, q := range loaded.Queries {
			if q.Query == query {
				got = q.Relevant
				break
			}
		}
		for _, want := range wants {
			if !hasRelevant(got, want) {
				t.Fatalf("query %q missing relevant ID %q (got %v)", query, want, got)
			}
		}
	}
}

func TestLoadQueryCorpus_IncludesNegativeControl(t *testing.T) {
	loaded, err := LoadQueryCorpus(filepath.Join(repoRoot(t), "testdata", "eval", "queries.json"))
	if err != nil {
		t.Fatalf("LoadQueryCorpus: %v", err)
	}
	for _, q := range loaded.Queries {
		if q.Query != "" && len(q.Relevant) == 0 {
			return
		}
	}
	t.Fatal("missing negative-control query with empty relevant set")
}

func hasRelevant(relevant []string, want string) bool {
	return slices.Contains(relevant, want)
}

func TestEvaluateQueries(t *testing.T) {
	cases := []QueryCase{
		{
			Query:    "Hello",
			Relevant: []string{"a", "b"},
			K:        3,
		},
	}

	mockSearch := func(query string, limit int) ([]string, error) {
		return []string{"a", "c", "b"}, nil
	}

	report, err := EvaluateQueries(cases, mockSearch)
	if err != nil {
		t.Fatalf("EvaluateQueries: %v", err)
	}
	if report.QueriesTotal != 1 {
		t.Errorf("total: got %d, want 1", report.QueriesTotal)
	}
	if !approxEqual(report.AvgPAt1, 1.0) {
		t.Errorf("P@1: got %f, want 1.0", report.AvgPAt1)
	}
	if !approxEqual(report.AvgMRR, 1.0) {
		t.Errorf("MRR: got %f, want 1.0", report.AvgMRR)
	}
	if !approxEqual(report.AvgRecallAt5, 1.0) {
		t.Errorf("R@5: got %f, want 1.0 (all relevant found within K=3)", report.AvgRecallAt5)
	}
}

func TestEvaluateQueries_Empty(t *testing.T) {
	report, err := EvaluateQueries(nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report.QueriesTotal != 0 {
		t.Errorf("total: got %d, want 0", report.QueriesTotal)
	}
}

func TestEvaluateQueries_NegativeControlExcludedFromRankingAverages(t *testing.T) {
	cases := []QueryCase{
		{Query: "positive", Relevant: []string{"a"}, K: 5},
		{Query: "negative", Relevant: []string{}, K: 5},
	}

	searchFn := func(query string, limit int) ([]string, error) {
		if query == "positive" {
			return []string{"a"}, nil
		}
		return nil, nil
	}

	report, err := EvaluateQueries(cases, searchFn)
	if err != nil {
		t.Fatalf("EvaluateQueries: %v", err)
	}
	if !approxEqual(report.AvgPAt1, 1.0) || !approxEqual(report.AvgMRR, 1.0) || !approxEqual(report.AvgRecallAt5, 1.0) {
		t.Fatalf("positive averages should remain perfect, got P@1=%f MRR=%f R@5=%f", report.AvgPAt1, report.AvgMRR, report.AvgRecallAt5)
	}
	if report.NegativeQueries != 1 || report.NegativeFalsePositives != 0 || !approxEqual(report.NegativePassRate, 1.0) {
		t.Fatalf("negative control aggregation mismatch: %+v", report)
	}
	if report.QueriesTotal != 2 {
		t.Fatalf("total queries: got %d, want 2", report.QueriesTotal)
	}
}

func TestEvaluateQueries_NegativeControlDetectsLeak(t *testing.T) {
	cases := []QueryCase{{Query: "negative", Relevant: []string{}, K: 5}}
	searchFn := func(query string, limit int) ([]string, error) {
		return []string{"unexpected"}, nil
	}

	report, err := EvaluateQueries(cases, searchFn)
	if err != nil {
		t.Fatalf("EvaluateQueries: %v", err)
	}
	if report.NegativeQueries != 1 || report.NegativeFalsePositives != 1 || !approxEqual(report.NegativePassRate, 0.0) {
		t.Fatalf("negative leak aggregation mismatch: %+v", report)
	}
}

func TestEvaluateQueries_PositiveBaselineUnchanged(t *testing.T) {
	cases := []QueryCase{{Query: "Hello", Relevant: []string{"a", "b"}, K: 3}}
	searchFn := func(query string, limit int) ([]string, error) {
		return []string{"a", "c", "b"}, nil
	}

	report, err := EvaluateQueries(cases, searchFn)
	if err != nil {
		t.Fatalf("EvaluateQueries: %v", err)
	}
	if !approxEqual(report.AvgPAt1, 1.0) || !approxEqual(report.AvgMRR, 1.0) || !approxEqual(report.AvgRecallAt5, 1.0) {
		t.Fatalf("positive baseline changed: %+v", report)
	}
}

func TestEvaluateQueries_EmitsPerQueryDiagnostics(t *testing.T) {
	cases := []QueryCase{
		{Query: "hit", Relevant: []string{"a", "b"}, K: 3},
		{Query: "miss", Relevant: []string{"x"}, K: 3},
		{Query: "neg", Relevant: []string{}, K: 3},
	}
	searchFn := func(query string, limit int) ([]string, error) {
		switch query {
		case "hit":
			return []string{"a", "c", "b"}, nil
		case "miss":
			return []string{"y", "z"}, nil
		case "neg":
			return []string{"unexpected"}, nil
		default:
			return nil, nil
		}
	}

	report, err := EvaluateQueries(cases, searchFn)
	if err != nil {
		t.Fatalf("EvaluateQueries: %v", err)
	}
	if len(report.PerQuery) != 3 {
		t.Fatalf("PerQuery len: got %d, want 3", len(report.PerQuery))
	}
	if report.PerQuery[0].Query != "hit" || !approxEqual(report.PerQuery[0].PAt1, 1.0) || !approxEqual(report.PerQuery[0].MRR, 1.0) || report.PerQuery[0].ResultsReturned != 3 || report.PerQuery[0].Kind != "positive" {
		t.Fatalf("hit diagnostic mismatch: %+v", report.PerQuery[0])
	}
	if report.PerQuery[1].Query != "miss" || !approxEqual(report.PerQuery[1].PAt1, 0.0) || !approxEqual(report.PerQuery[1].MRR, 0.0) || report.PerQuery[1].Kind != "positive" {
		t.Fatalf("miss diagnostic mismatch: %+v", report.PerQuery[1])
	}
	if report.PerQuery[2].Query != "neg" || report.PerQuery[2].Kind != "negative" || !report.PerQuery[2].FalsePositive {
		t.Fatalf("neg diagnostic mismatch: %+v", report.PerQuery[2])
	}
}

func TestEvaluateQueries_AggregatesUnchangedAfterDiagnostics(t *testing.T) {
	cases := []QueryCase{{Query: "Hello", Relevant: []string{"a", "b"}, K: 3}}
	searchFn := func(string, int) ([]string, error) { return []string{"a", "c", "b"}, nil }
	report, err := EvaluateQueries(cases, searchFn)
	if err != nil {
		t.Fatalf("EvaluateQueries: %v", err)
	}
	if !approxEqual(report.AvgPAt1, 1.0) || !approxEqual(report.AvgMRR, 1.0) || !approxEqual(report.AvgRecallAt5, 1.0) {
		t.Fatalf("aggregates regressed: %+v", report)
	}
}

func TestEvaluateQueries_NegativeDiagnosticIncludesReturnedKeys(t *testing.T) {
	cases := []QueryCase{{Query: "neg", Relevant: []string{}, K: 3}}
	searchFn := func(string, int) ([]string, error) { return []string{"unexpected1", "unexpected2"}, nil }

	report, err := EvaluateQueries(cases, searchFn)
	if err != nil {
		t.Fatalf("EvaluateQueries: %v", err)
	}
	if len(report.PerQuery) != 1 {
		t.Fatalf("PerQuery len: got %d, want 1", len(report.PerQuery))
	}
	got := report.PerQuery[0].TopResults
	want := []string{"unexpected1", "unexpected2"}
	if !slices.Equal(got, want) {
		t.Fatalf("TopResults: got %v, want %v", got, want)
	}
}

func TestEvaluateQueries_PositiveDiagnosticIncludesReturnedKeys(t *testing.T) {
	cases := []QueryCase{{Query: "hit", Relevant: []string{"a", "b"}, K: 3}}
	searchFn := func(string, int) ([]string, error) { return []string{"a", "c", "b"}, nil }

	report, err := EvaluateQueries(cases, searchFn)
	if err != nil {
		t.Fatalf("EvaluateQueries: %v", err)
	}
	if len(report.PerQuery) != 1 {
		t.Fatalf("PerQuery len: got %d, want 1", len(report.PerQuery))
	}
	got := report.PerQuery[0].TopResults
	want := []string{"a", "c", "b"}
	if !slices.Equal(got, want) {
		t.Fatalf("TopResults: got %v, want %v", got, want)
	}
}

func TestEvaluateQueries_DiagnosticTopResultsCappedAtFive(t *testing.T) {
	cases := []QueryCase{{Query: "hit", Relevant: []string{"a"}, K: 5}}
	searchFn := func(string, int) ([]string, error) {
		return []string{"a", "b", "c", "d", "e", "f", "g", "h"}, nil
	}

	report, err := EvaluateQueries(cases, searchFn)
	if err != nil {
		t.Fatalf("EvaluateQueries: %v", err)
	}
	got := report.PerQuery[0].TopResults
	want := []string{"a", "b", "c", "d", "e"}
	if !slices.Equal(got, want) {
		t.Fatalf("TopResults cap: got %v, want %v", got, want)
	}
}

func TestEvaluateQueries_AggregatesUnchangedAfterTopResults(t *testing.T) {
	cases := []QueryCase{{Query: "Hello", Relevant: []string{"a", "b"}, K: 3}}
	searchFn := func(string, int) ([]string, error) { return []string{"a", "c", "b"}, nil }
	report, err := EvaluateQueries(cases, searchFn)
	if err != nil {
		t.Fatalf("EvaluateQueries: %v", err)
	}
	if !approxEqual(report.AvgPAt1, 1.0) || !approxEqual(report.AvgMRR, 1.0) || !approxEqual(report.AvgRecallAt5, 1.0) || !approxEqual(report.NegativePassRate, 0.0) {
		t.Fatalf("aggregates regressed: %+v", report)
	}
}
