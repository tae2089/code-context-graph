package benchmark_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/tae2089/code-context-graph/internal/benchmark"
)

func TestRunnerConfig_Defaults(t *testing.T) {
	cfg := benchmark.DefaultRunnerConfig()
	if cfg.MaxToolCalls != 50 {
		t.Errorf("MaxToolCalls: got %d, want 50", cfg.MaxToolCalls)
	}
	if cfg.TimeoutSec != 120 {
		t.Errorf("TimeoutSec: got %d, want 120", cfg.TimeoutSec)
	}
}

func TestBuildClaudeArgs_WithCWD(t *testing.T) {
	// CWD is passed to Executor.Execute as dir, not as a --cwd CLI flag.
	cfg := benchmark.RunnerConfig{
		Mode:         "with-ccg",
		CWD:          "/tmp/benchmark-workspace",
		MaxToolCalls: 50,
		TimeoutSec:   120,
	}
	args := benchmark.BuildClaudeArgs(cfg)
	for _, a := range args {
		if a == "--cwd" {
			t.Errorf("--cwd flag must not appear in args (set via cmd.Dir): %v", args)
		}
	}
}

func TestBuildClaudeArgs_OutputFormat(t *testing.T) {
	cfg := benchmark.DefaultRunnerConfig()
	args := benchmark.BuildClaudeArgs(cfg)
	found := false
	for i, a := range args {
		if a == "--output-format" && i+1 < len(args) && args[i+1] == "json" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected --output-format json in args: %v", args)
	}
}

// mockExecutor implements benchmark.Executor for testing.
type mockExecutor struct {
	calls  int
	err    error
	output []byte
}

func (m *mockExecutor) Execute(_ context.Context, _ []string, _, _ string) ([]byte, error) {
	m.calls++
	if m.err != nil {
		return nil, m.err
	}
	return m.output, nil
}

func TestMockRunner_ExecutesEachQuery(t *testing.T) {
	exec := &mockExecutor{output: []byte(`{}`)}
	cfg := benchmark.DefaultRunnerConfig()
	runner := benchmark.NewRunner(cfg, exec)
	corpus := &benchmark.Corpus{
		Queries: []benchmark.Query{
			{ID: "q1", Description: "query one"},
			{ID: "q2", Description: "query two"},
			{ID: "q3", Description: "query three"},
		},
	}
	run, err := runner.Run(context.Background(), corpus)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(run.Results) != 3 {
		t.Errorf("expected 3 results, got %d", len(run.Results))
	}
	if exec.calls != 3 {
		t.Errorf("expected 3 executor calls, got %d", exec.calls)
	}
}

func TestMockRunner_HandlesError(t *testing.T) {
	exec := &mockExecutor{err: errors.New("claude not found")}
	cfg := benchmark.DefaultRunnerConfig()
	runner := benchmark.NewRunner(cfg, exec)
	corpus := &benchmark.Corpus{
		Queries: []benchmark.Query{
			{ID: "q1", Description: "fail query"},
		},
	}
	run, err := runner.Run(context.Background(), corpus)
	if err != nil {
		t.Fatalf("Run should not return error (errors go into RunResult): %v", err)
	}
	if len(run.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(run.Results))
	}
	if run.Results[0].Error == "" {
		t.Error("expected RunResult.Error to be set on executor error")
	}
}

func TestBuildPromptWithMarkers(t *testing.T) {
	q := benchmark.Query{ID: "q1", Description: "결제 실패 복구 로직"}
	prompt := benchmark.BuildPrompt(q)
	if !strings.Contains(prompt, "===BENCHMARK_QUERY_START id=q1===") {
		t.Errorf("prompt missing START marker: %q", prompt)
	}
	if !strings.Contains(prompt, "===BENCHMARK_QUERY_END id=q1===") {
		t.Errorf("prompt missing END marker: %q", prompt)
	}
	if !strings.Contains(prompt, q.Description) {
		t.Errorf("prompt missing description: %q", prompt)
	}
}
