package benchmark_test

import (
	"encoding/json"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/tae2089/code-context-graph/internal/benchmark"
)

func TestQuery_YAMLRoundtrip(t *testing.T) {
	q := benchmark.Query{
		ID:              "q1",
		Description:     "결제 실패 복구 로직",
		ExpectedFiles:   []string{"internal/webhook/handler.go"},
		ExpectedSymbols: []string{"WebhookHandler"},
		Difficulty:      "medium",
	}
	data, err := yaml.Marshal(q)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got benchmark.Query
	if err := yaml.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.ID != q.ID {
		t.Errorf("ID: got %q, want %q", got.ID, q.ID)
	}
	if got.Description != q.Description {
		t.Errorf("Description: got %q, want %q", got.Description, q.Description)
	}
	if len(got.ExpectedFiles) != 1 || got.ExpectedFiles[0] != q.ExpectedFiles[0] {
		t.Errorf("ExpectedFiles: got %v, want %v", got.ExpectedFiles, q.ExpectedFiles)
	}
	if got.Difficulty != q.Difficulty {
		t.Errorf("Difficulty: got %q, want %q", got.Difficulty, q.Difficulty)
	}
}

func TestRunResult_JSONRoundtrip(t *testing.T) {
	r := benchmark.RunResult{
		QueryID:      "q1",
		ToolCalls:    []benchmark.ToolCall{{Tool: "mcp__ccg__search", Input: "query"}},
		FilesRead:    []string{"internal/webhook/handler.go"},
		Answer:       "WebhookHandler는 ...",
		InputTokens:  100,
		OutputTokens: 50,
		ElapsedMs:    1234,
	}
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got benchmark.RunResult
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.QueryID != r.QueryID {
		t.Errorf("QueryID: got %q, want %q", got.QueryID, r.QueryID)
	}
	if len(got.ToolCalls) != 1 || got.ToolCalls[0].Tool != r.ToolCalls[0].Tool {
		t.Errorf("ToolCalls: got %v, want %v", got.ToolCalls, r.ToolCalls)
	}
	if got.InputTokens != r.InputTokens {
		t.Errorf("InputTokens: got %d, want %d", got.InputTokens, r.InputTokens)
	}
	if got.ElapsedMs != r.ElapsedMs {
		t.Errorf("ElapsedMs: got %d, want %d", got.ElapsedMs, r.ElapsedMs)
	}
}

func TestBenchmarkRun_ResultByID(t *testing.T) {
	run := benchmark.BenchmarkRun{
		Mode:  "with-ccg",
		RunAt: time.Now(),
		Results: []benchmark.RunResult{
			{QueryID: "q1", Answer: "답변1"},
			{QueryID: "q2", Answer: "답변2"},
		},
	}
	got := run.ResultByID("q1")
	if got == nil {
		t.Fatal("expected non-nil result for q1")
	}
	if got.Answer != "답변1" {
		t.Errorf("Answer: got %q, want %q", got.Answer, "답변1")
	}
}

func TestBenchmarkRun_ResultByID_Missing(t *testing.T) {
	run := benchmark.BenchmarkRun{
		Mode:    "with-ccg",
		Results: []benchmark.RunResult{{QueryID: "q1"}},
	}
	got := run.ResultByID("not-exist")
	if got != nil {
		t.Errorf("expected nil for missing ID, got %+v", got)
	}
}
