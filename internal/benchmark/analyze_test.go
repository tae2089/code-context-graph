package benchmark_test

import (
	"strings"
	"testing"

	"github.com/tae2089/code-context-graph/internal/benchmark"
)

func TestMatchFiles_AllMatch(t *testing.T) {
	result := benchmark.RunResult{
		FilesRead: []string{"internal/webhook/handler.go", "internal/webhook/syncqueue.go"},
	}
	query := benchmark.Query{
		ExpectedFiles: []string{"internal/webhook/handler.go", "internal/webhook/syncqueue.go"},
	}
	ratio := benchmark.MatchFiles(result, query)
	if ratio != 1.0 {
		t.Errorf("FileHitRatio: got %.3f, want 1.0", ratio)
	}
}

func TestMatchFiles_PartialMatch(t *testing.T) {
	result := benchmark.RunResult{
		FilesRead: []string{"internal/webhook/handler.go"},
	}
	query := benchmark.Query{
		ExpectedFiles: []string{"internal/webhook/handler.go", "internal/webhook/syncqueue.go", "internal/webhook/auth.go"},
	}
	ratio := benchmark.MatchFiles(result, query)
	want := 1.0 / 3.0
	if ratio < want-0.001 || ratio > want+0.001 {
		t.Errorf("FileHitRatio: got %.3f, want %.3f", ratio, want)
	}
}

func TestMatchFiles_NoExpected(t *testing.T) {
	result := benchmark.RunResult{FilesRead: []string{"any.go"}}
	query := benchmark.Query{ExpectedFiles: nil}
	ratio := benchmark.MatchFiles(result, query)
	if ratio != 1.0 {
		t.Errorf("FileHitRatio: got %.3f, want 1.0 when no expected files", ratio)
	}
}

func TestMatchFiles_AbsolutePathSuffix(t *testing.T) {
	result := benchmark.RunResult{
		FilesRead: []string{"/Users/user/repo/internal/webhook/handler.go"},
	}
	query := benchmark.Query{
		ExpectedFiles: []string{"internal/webhook/handler.go"},
	}
	ratio := benchmark.MatchFiles(result, query)
	if ratio != 1.0 {
		t.Errorf("FileHitRatio: got %.3f, want 1.0 for absolute path suffix match", ratio)
	}
}

func TestMatchSymbols_InAnswer(t *testing.T) {
	result := benchmark.RunResult{Answer: "WebhookHandler handles push events via SyncQueue"}
	query := benchmark.Query{ExpectedSymbols: []string{"WebhookHandler", "SyncQueue"}}
	ratio := benchmark.MatchSymbols(result, query)
	if ratio != 1.0 {
		t.Errorf("SymbolHitRatio: got %.3f, want 1.0", ratio)
	}
}

func TestMatchSymbols_PartialMatch(t *testing.T) {
	result := benchmark.RunResult{Answer: "WebhookHandler handles events"}
	query := benchmark.Query{ExpectedSymbols: []string{"WebhookHandler", "SyncQueue"}}
	ratio := benchmark.MatchSymbols(result, query)
	if ratio != 0.5 {
		t.Errorf("SymbolHitRatio: got %.3f, want 0.5", ratio)
	}
}

func TestCountCcgToolCalls(t *testing.T) {
	result := benchmark.RunResult{
		ToolCalls: []benchmark.ToolCall{
			{Tool: "mcp__ccg__search"},
			{Tool: "mcp__ccg__get_node"},
			{Tool: "Read"},
			{Tool: "Grep"},
		},
	}
	count := benchmark.CountCcgToolCalls(result)
	if count != 2 {
		t.Errorf("CcgToolCalls: got %d, want 2", count)
	}
}

func TestComputeMatch_FullResult(t *testing.T) {
	result := benchmark.RunResult{
		QueryID:     "q1",
		FilesRead:   []string{"internal/webhook/handler.go"},
		Answer:      "WebhookHandler 구현이 " + strings.Repeat("x", 10),
		InputTokens: 200,
		ToolCalls: []benchmark.ToolCall{
			{Tool: "mcp__ccg__search"},
			{Tool: "Read"},
		},
	}
	query := benchmark.Query{
		ID:              "q1",
		ExpectedFiles:   []string{"internal/webhook/handler.go"},
		ExpectedSymbols: []string{"WebhookHandler"},
	}
	m := benchmark.ComputeMatch(result, query)
	if m.QueryID != "q1" {
		t.Errorf("QueryID: got %q", m.QueryID)
	}
	if m.FileHitRatio != 1.0 {
		t.Errorf("FileHitRatio: got %.3f, want 1.0", m.FileHitRatio)
	}
	if m.SymbolHitRatio != 1.0 {
		t.Errorf("SymbolHitRatio: got %.3f, want 1.0", m.SymbolHitRatio)
	}
	if m.TotalToolCalls != 2 {
		t.Errorf("TotalToolCalls: got %d, want 2", m.TotalToolCalls)
	}
	if m.CcgToolCalls != 1 {
		t.Errorf("CcgToolCalls: got %d, want 1", m.CcgToolCalls)
	}
	if m.TotalInputTokens != 200 {
		t.Errorf("TotalInputTokens: got %d, want 200", m.TotalInputTokens)
	}
}

func TestAnalyzeRun_MultipleResults(t *testing.T) {
	run := &benchmark.BenchmarkRun{
		Mode: "with-ccg",
		Results: []benchmark.RunResult{
			{QueryID: "q1", FilesRead: []string{"a.go"}, Answer: "Foo"},
			{QueryID: "q2", FilesRead: []string{"b.go"}, Answer: "Bar"},
		},
	}
	corpus := &benchmark.Corpus{
		Queries: []benchmark.Query{
			{ID: "q1", ExpectedFiles: []string{"a.go"}, ExpectedSymbols: []string{"Foo"}},
			{ID: "q2", ExpectedFiles: []string{"b.go"}, ExpectedSymbols: []string{"Bar"}},
		},
	}
	matches := benchmark.AnalyzeRun(run, corpus)
	if len(matches) != 2 {
		t.Errorf("expected 2 matches, got %d", len(matches))
	}
}
