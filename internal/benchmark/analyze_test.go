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

func TestMatchSymbolsFromToolOutputs_StrictTentative(t *testing.T) {
	toolOut := `{"pattern":"callers_of","results":[{"qualified_name":"StrictA","confidence":"strict"},{"qualified_name":"TentativeA","confidence":"tentative"},{"qualified_name":"TentativeA","confidence":"tentative"}]}`
	result := benchmark.RunResult{
		QueryID: "q1",
		Answer:  "StrictA",
		ToolCalls: []benchmark.ToolCall{
			{Tool: "mcp__ccg__query_graph", Output: toolOut},
		},
	}
	query := benchmark.Query{
		ExpectedStrictSymbols:    []string{"StrictA"},
		ExpectedTentativeSymbols: []string{"TentativeA", "TentativeMissing"},
	}
	m := benchmark.ComputeMatch(result, query)
	if m.StrictSymbolHitRatio != 1.0 {
		t.Errorf("StrictSymbolHitRatio: got %.3f, want 1.0", m.StrictSymbolHitRatio)
	}
	if m.TentativeSymbolHitRatio != 0.0 {
		t.Errorf("TentativeSymbolHitRatio: got %.3f, want 0.0", m.TentativeSymbolHitRatio)
	}
	if m.ToolAwareStrictRatio != 1.0 {
		t.Errorf("ToolAwareStrictRatio: got %.3f, want 1.0", m.ToolAwareStrictRatio)
	}
	if m.ToolAwareTentativeRatio != 0.5 {
		t.Errorf("ToolAwareTentativeRatio: got %.3f, want 0.5", m.ToolAwareTentativeRatio)
	}
}

func TestLLMStrictBias_Contamination(t *testing.T) {
	result := benchmark.RunResult{
		Answer: "TentativeA 사용 지점만 확인",
		ToolCalls: []benchmark.ToolCall{
			{Tool: "mcp__ccg__query_graph", Output: `{"pattern":"callees_of","results":[]}`},
		},
	}
	query := benchmark.Query{
		ExpectedStrictSymbols:    []string{"StrictA"},
		ExpectedTentativeSymbols: []string{"TentativeA"},
	}
	m := benchmark.ComputeMatch(result, query)
	if m.LLMStrictBias != 0.0 {
		t.Errorf("LLMStrictBias: got %.3f, want 0.0", m.LLMStrictBias)
	}
	if m.StrictContaminationRate != 1.0 {
		t.Errorf("StrictContaminationRate: got %.3f, want 1.0", m.StrictContaminationRate)
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
	toolOut := `{"pattern":"callees_of","results":[{"qualified_name":"WebhookHandler","confidence":"strict"},{"qualified_name":"SyncQueue","confidence":"tentative"}]}`
	result := benchmark.RunResult{
		QueryID:     "q1",
		FilesRead:   []string{"internal/webhook/handler.go"},
		Answer:      "WebhookHandler 구현이 " + strings.Repeat("x", 10),
		InputTokens: 200,
		ToolCalls: []benchmark.ToolCall{
			{Tool: "mcp__ccg__search"},
			{Tool: "mcp__ccg__query_graph", Output: toolOut},
			{Tool: "Read"},
		},
	}
	query := benchmark.Query{
		ID:                      "q1",
		ExpectedFiles:           []string{"internal/webhook/handler.go"},
		ExpectedSymbols:         []string{"WebhookHandler"},
		ExpectedStrictSymbols:   []string{"WebhookHandler"},
		ExpectedTentativeSymbols: []string{"SyncQueue"},
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
	if m.StrictSymbolHitRatio != 1.0 {
		t.Errorf("StrictSymbolHitRatio: got %.3f, want 1.0", m.StrictSymbolHitRatio)
	}
	if m.TentativeSymbolHitRatio != 0.0 {
		t.Errorf("TentativeSymbolHitRatio: got %.3f, want 0.0", m.TentativeSymbolHitRatio)
	}
	if m.ToolAwareStrictRatio != 1.0 {
		t.Errorf("ToolAwareStrictRatio: got %.3f, want 1.0", m.ToolAwareStrictRatio)
	}
	if m.ToolAwareTentativeRatio != 1.0 {
		t.Errorf("ToolAwareTentativeRatio: got %.3f, want 1.0", m.ToolAwareTentativeRatio)
	}
	if m.LLMStrictBias != 1.0 {
		t.Errorf("LLMStrictBias: got %.3f, want 1.0", m.LLMStrictBias)
	}
	if m.StrictContaminationRate != 0.0 {
		t.Errorf("StrictContaminationRate: got %.3f, want 0.0", m.StrictContaminationRate)
	}
	if m.TotalToolCalls != 3 {
		t.Errorf("TotalToolCalls: got %d, want 3", m.TotalToolCalls)
	}
	if m.CcgToolCalls != 2 {
		t.Errorf("CcgToolCalls: got %d, want 2", m.CcgToolCalls)
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
