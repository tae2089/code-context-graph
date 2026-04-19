package benchmark_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tae2089/code-context-graph/internal/benchmark"
)

func makeReport(withoutCCG *benchmark.BenchmarkRun) *benchmark.ComparisonReport {
	corpus := &benchmark.Corpus{
		Queries: []benchmark.Query{
			{ID: "q1", ExpectedFiles: []string{"a.go"}, ExpectedSymbols: []string{"Foo"}},
			{ID: "q2", ExpectedFiles: []string{"b.go"}, ExpectedSymbols: []string{"Bar"}},
		},
	}
	withCCG := &benchmark.BenchmarkRun{
		Mode:  "with-ccg",
		RunAt: time.Now(),
		Results: []benchmark.RunResult{
			{QueryID: "q1", FilesRead: []string{"a.go"}, Answer: "Foo is here", InputTokens: 100,
				ToolCalls: []benchmark.ToolCall{{Tool: "mcp__ccg__search"}}},
			{QueryID: "q2", FilesRead: []string{}, Answer: "unknown", InputTokens: 150},
		},
	}
	return benchmark.Compare(withCCG, withoutCCG, corpus)
}

func TestReport_ContainsSummary(t *testing.T) {
	report := makeReport(nil)
	dir := t.TempDir()
	out := filepath.Join(dir, "report.md")
	if err := benchmark.WriteReport(report, out); err != nil {
		t.Fatalf("WriteReport: %v", err)
	}
	data, _ := os.ReadFile(out)
	if !strings.Contains(string(data), "## Summary") {
		t.Errorf("report missing '## Summary' section:\n%s", string(data))
	}
}

func TestReport_ContainsQueryTable(t *testing.T) {
	report := makeReport(nil)
	dir := t.TempDir()
	out := filepath.Join(dir, "report.md")
	if err := benchmark.WriteReport(report, out); err != nil {
		t.Fatalf("WriteReport: %v", err)
	}
	data, _ := os.ReadFile(out)
	content := string(data)
	if !strings.Contains(content, "q1") {
		t.Error("report missing query ID q1")
	}
	if !strings.Contains(content, "q2") {
		t.Error("report missing query ID q2")
	}
}

func TestReport_NoWithoutCCG(t *testing.T) {
	report := makeReport(nil)
	if report.WithoutCCG != nil {
		t.Fatal("test setup error: WithoutCCG should be nil")
	}
	dir := t.TempDir()
	out := filepath.Join(dir, "report.md")
	if err := benchmark.WriteReport(report, out); err != nil {
		t.Fatalf("WriteReport with single run: %v", err)
	}
	data, _ := os.ReadFile(out)
	if len(data) == 0 {
		t.Error("expected non-empty report")
	}
}

func TestReport_WritesToFile(t *testing.T) {
	report := makeReport(nil)
	dir := t.TempDir()
	out := filepath.Join(dir, "report.md")
	if err := benchmark.WriteReport(report, out); err != nil {
		t.Fatalf("WriteReport: %v", err)
	}
	if _, err := os.Stat(out); os.IsNotExist(err) {
		t.Error("report file was not created")
	}
}
