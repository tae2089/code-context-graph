package benchmark_test

import (
	"testing"
	"time"

	"github.com/tae2089/code-context-graph/internal/benchmark"
)

func makeRun(mode string, results []benchmark.RunResult) *benchmark.BenchmarkRun {
	return &benchmark.BenchmarkRun{Mode: mode, RunAt: time.Now(), Results: results}
}

func TestCompare_BothRuns(t *testing.T) {
	corpus := &benchmark.Corpus{
		Queries: []benchmark.Query{
			{ID: "q1", ExpectedFiles: []string{"a.go"}, ExpectedSymbols: []string{"Foo"}},
		},
	}
	withCCG := makeRun("with-ccg", []benchmark.RunResult{
		{QueryID: "q1", FilesRead: []string{"a.go"}, Answer: "Foo is here", InputTokens: 100},
	})
	withoutCCG := makeRun("without-ccg", []benchmark.RunResult{
		{QueryID: "q1", FilesRead: []string{}, Answer: "not sure", InputTokens: 200},
	})
	report := benchmark.Compare(withCCG, withoutCCG, corpus)
	if report.WithCCG == nil {
		t.Error("WithCCG should not be nil")
	}
	if report.WithoutCCG == nil {
		t.Error("WithoutCCG should not be nil")
	}
	if len(report.Matches) == 0 {
		t.Error("expected at least one match result")
	}
}

func TestCompare_OnlyWithCCG(t *testing.T) {
	corpus := &benchmark.Corpus{
		Queries: []benchmark.Query{
			{ID: "q1", Description: "test"},
		},
	}
	withCCG := makeRun("with-ccg", []benchmark.RunResult{
		{QueryID: "q1", Answer: "answer"},
	})
	report := benchmark.Compare(withCCG, nil, corpus)
	if report.WithCCG == nil {
		t.Error("WithCCG should not be nil")
	}
	if report.WithoutCCG != nil {
		t.Errorf("WithoutCCG should be nil, got %+v", report.WithoutCCG)
	}
}

func TestCompare_TokenDiff(t *testing.T) {
	corpus := &benchmark.Corpus{
		Queries: []benchmark.Query{{ID: "q1"}},
	}
	withCCG := makeRun("with-ccg", []benchmark.RunResult{
		{QueryID: "q1", InputTokens: 50},
	})
	withoutCCG := makeRun("without-ccg", []benchmark.RunResult{
		{QueryID: "q1", InputTokens: 300},
	})
	report := benchmark.Compare(withCCG, withoutCCG, corpus)
	if len(report.Matches) == 0 {
		t.Fatal("no matches")
	}
	if report.Matches[0].TotalInputTokens != 50 {
		t.Errorf("Matches should reflect with-ccg tokens (50), got %d", report.Matches[0].TotalInputTokens)
	}
}

func TestCompare_FileHitImprovement(t *testing.T) {
	corpus := &benchmark.Corpus{
		Queries: []benchmark.Query{
			{ID: "q1", ExpectedFiles: []string{"a.go", "b.go"}},
		},
	}
	withCCG := makeRun("with-ccg", []benchmark.RunResult{
		{QueryID: "q1", FilesRead: []string{"a.go", "b.go"}},
	})
	withoutCCG := makeRun("without-ccg", []benchmark.RunResult{
		{QueryID: "q1", FilesRead: []string{"a.go"}},
	})
	report := benchmark.Compare(withCCG, withoutCCG, corpus)
	if len(report.Matches) == 0 {
		t.Fatal("no matches")
	}
	if report.Matches[0].FileHitRatio != 1.0 {
		t.Errorf("with-ccg FileHitRatio should be 1.0, got %.3f", report.Matches[0].FileHitRatio)
	}
}
