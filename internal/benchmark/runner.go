// @index Benchmark runner orchestration for Claude CLI subprocess execution.
package benchmark

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"time"
)

// Executor abstracts the subprocess execution so tests can inject a mock.
// dir sets the working directory for the subprocess; empty means inherit from parent.
// @intent decouple benchmark orchestration from the concrete Claude CLI process implementation.
type Executor interface {
	Execute(ctx context.Context, args []string, prompt, dir string) ([]byte, error)
}

// RunnerConfig holds configuration for a benchmark run.
// @intent collect execution mode, working directory, and timeout settings for one benchmark run.
type RunnerConfig struct {
	Mode         string // "with-ccg" | "without-ccg"
	CWD          string // benchmark working directory
	MaxToolCalls int
	TimeoutSec   int
}

// DefaultRunnerConfig returns a RunnerConfig with sensible defaults.
// @intent provide conservative benchmark defaults that work for most local runs.
func DefaultRunnerConfig() RunnerConfig {
	return RunnerConfig{
		MaxToolCalls: 50,
		TimeoutSec:   120,
	}
}

// claudeOutput is the structure of `claude -p --output-format json` output.
// @intent decode the subset of Claude CLI JSON output needed for benchmark scoring.
type claudeOutput struct {
	IsError bool   `json:"is_error"`
	Result  string `json:"result"`
	Usage   struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

// buildArgs constructs CLI args for a specific run.
// mcpConfigFile is the path to an empty MCP config file for without-ccg mode.
// @intent centralize Claude CLI argument construction for both benchmark modes.
func buildArgs(cfg RunnerConfig, mcpConfigFile string) []string {
	args := []string{"-p", "--output-format", "json"}
	if cfg.MaxToolCalls > 0 {
		args = append(args, "--max-turns", strconv.Itoa(cfg.MaxToolCalls))
	}
	if cfg.Mode == "without-ccg" {
		args = append(args, "--strict-mcp-config", "--mcp-config", mcpConfigFile)
	}
	return args
}

// BuildClaudeArgs constructs the CLI argument slice for `claude -p`.
// Working directory is set on the subprocess directly (not via --cwd flag).
// In "without-ccg" mode, --strict-mcp-config disables all MCP servers.
// NOTE: For actual runs, use Runner.Run which creates a real temp file for --mcp-config.
// @intent expose deterministic argument construction for tests and tooling that inspect benchmark invocation behavior.
func BuildClaudeArgs(cfg RunnerConfig) []string {
	if cfg.Mode == "without-ccg" {
		return buildArgs(cfg, `{"mcpServers":{}}`)
	}
	return buildArgs(cfg, "")
}

// BuildPrompt creates the prompt string that wraps a query with benchmark markers.
// The markers allow the JSONL analyzer to locate query boundaries.
// @intent surround benchmark queries with markers so later session analysis can recover exact query segments.
func BuildPrompt(q Query) string {
	return fmt.Sprintf(
		"===BENCHMARK_QUERY_START id=%s===\n%s\n===BENCHMARK_QUERY_END id=%s===",
		q.ID, q.Description, q.ID,
	)
}

// Runner executes benchmark queries sequentially using the provided Executor.
// @intent own the end-to-end lifecycle of benchmark query execution for one mode.
type Runner struct {
	cfg  RunnerConfig
	exec Executor
}

// NewRunner creates a Runner with the given config and executor.
// @intent assemble benchmark orchestration from execution config and a subprocess adapter.
func NewRunner(cfg RunnerConfig, exec Executor) *Runner {
	return &Runner{cfg: cfg, exec: exec}
}

// Run executes each query in the corpus and returns a BenchmarkRun.
// Executor errors are captured in RunResult.Error rather than aborting the run.
// @intent execute the full benchmark corpus while preserving per-query failures in the final report.
func (r *Runner) Run(ctx context.Context, corpus *Corpus) (*BenchmarkRun, error) {
	run := &BenchmarkRun{
		Mode:    r.cfg.Mode,
		RunAt:   time.Now(),
		Results: make([]RunResult, 0, len(corpus.Queries)),
	}

	mcpConfigFile, cleanup, err := r.prepareMCPConfig()
	if err != nil {
		return nil, err
	}
	defer cleanup()

	args := buildArgs(r.cfg, mcpConfigFile)
	for _, q := range corpus.Queries {
		start := time.Now()
		prompt := BuildPrompt(q)
		var (
			out     []byte
			execErr error
		)
		func() {
			qctx := ctx
			if r.cfg.TimeoutSec > 0 {
				var cancel context.CancelFunc
				qctx, cancel = context.WithTimeout(ctx, time.Duration(r.cfg.TimeoutSec)*time.Second)
				defer cancel()
			}
			out, execErr = r.exec.Execute(qctx, args, prompt, r.cfg.CWD)
		}()
		elapsed := time.Since(start).Milliseconds()

		res := RunResult{
			QueryID:   q.ID,
			ElapsedMs: elapsed,
		}
		if execErr != nil {
			res.Error = execErr.Error()
		} else {
			r.parseClaudeOutput(out, &res)
		}
		run.Results = append(run.Results, res)
	}
	return run, nil
}

// prepareMCPConfig creates a temp JSON file for without-ccg mode so that
// --mcp-config receives a file path rather than inline JSON (avoiding the
// variadic flag consuming the prompt from stdin).
// @intent disable MCP cleanly in without-ccg mode without confusing the Claude CLI argument parser.
func (r *Runner) prepareMCPConfig() (path string, cleanup func(), err error) {
	if r.cfg.Mode != "without-ccg" {
		return "", func() {}, nil
	}
	f, ferr := os.CreateTemp("", "ccg-bench-*.json")
	if ferr != nil {
		return "", nil, fmt.Errorf("create mcp config: %w", ferr)
	}
	if _, werr := f.WriteString(`{"mcpServers":{}}`); werr != nil {
		f.Close()
		os.Remove(f.Name())
		return "", nil, fmt.Errorf("write mcp config: %w", werr)
	}
	f.Close()
	return f.Name(), func() { os.Remove(f.Name()) }, nil
}

// parseClaudeOutput extracts token counts and answer text from claude's JSON output.
// @intent translate Claude CLI JSON output into benchmark result fields without failing the whole run on parse misses.
func (r *Runner) parseClaudeOutput(out []byte, res *RunResult) {
	if len(out) == 0 {
		return
	}
	var co claudeOutput
	if err := json.Unmarshal(out, &co); err != nil {
		return
	}
	res.InputTokens = co.Usage.InputTokens
	res.OutputTokens = co.Usage.OutputTokens
	res.Answer = co.Result
}
