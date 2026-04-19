package eval

import (
	"encoding/json"
	"os"
	"testing"
)

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
