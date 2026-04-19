package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tae2089/trace"

	"github.com/tae2089/code-context-graph/internal/benchmark"
	"github.com/tae2089/code-context-graph/internal/store/gormstore"
	storesearch "github.com/tae2089/code-context-graph/internal/store/search"
)

func newBenchmarkCmd(deps *Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "benchmark",
		Short: "Benchmark ccg MCP tool effectiveness",
	}
	cmd.AddCommand(
		newBenchmarkInitCmd(),
		newBenchmarkValidateCmd(),
		newBenchmarkRunCmd(),
		newBenchmarkAnalyzeCmd(),
		newBenchmarkCompareCmd(),
		newBenchmarkReportCmd(),
		newBenchmarkTokenBenchCmd(deps),
	)
	return cmd
}

// newBenchmarkInitCmd creates a corpus directory with a template queries.yaml.
func newBenchmarkInitCmd() *cobra.Command {
	var outDir string
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize a benchmark corpus directory with a template queries.yaml",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := os.MkdirAll(outDir, 0o755); err != nil {
				return trace.Wrap(err, "create corpus directory")
			}
			corpus := &benchmark.Corpus{
				Version: "1",
				Queries: []benchmark.Query{
					{
						ID:              "example-01",
						Description:     "웹훅 push 수신 후 자동 그래프 업데이트 흐름",
						ExpectedFiles:   []string{"internal/webhook/handler.go"},
						ExpectedSymbols: []string{"WebhookHandler"},
						Difficulty:      "medium",
					},
				},
			}
			path := filepath.Join(outDir, "queries.yaml")
			if err := benchmark.SaveCorpus(path, corpus); err != nil {
				return trace.Wrap(err, "write template corpus")
			}
			fmt.Fprintf(stdout(cmd), "Initialized benchmark corpus at %s\n", path)
			return nil
		},
	}
	cmd.Flags().StringVar(&outDir, "out", "testdata/benchmark", "Output directory for corpus files")
	return cmd
}

// newBenchmarkValidateCmd validates a queries.yaml file.
func newBenchmarkValidateCmd() *cobra.Command {
	var corpusPath string
	cmd := &cobra.Command{
		Use:   "validate",
		Short: "Validate a benchmark corpus YAML file",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if _, err := benchmark.LoadCorpus(corpusPath); err != nil {
				return trace.Wrap(err, "validate corpus")
			}
			fmt.Fprintf(stdout(cmd), "Corpus %s is valid\n", corpusPath)
			return nil
		},
	}
	cmd.Flags().StringVar(&corpusPath, "corpus", "testdata/benchmark/queries.yaml", "Path to corpus YAML file")
	return cmd
}

// newBenchmarkRunCmd executes each query via `claude -p` subprocess.
func newBenchmarkRunCmd() *cobra.Command {
	var corpusPath, cwd, mode, outPath string
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Run benchmark queries via claude CLI subprocess",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if cwd == "" {
				return fmt.Errorf("--cwd is required: set the benchmark workspace directory")
			}
			corpus, err := benchmark.LoadCorpus(corpusPath)
			if err != nil {
				return trace.Wrap(err, "load corpus")
			}
			cfg := benchmark.DefaultRunnerConfig()
			cfg.Mode = mode
			cfg.CWD = cwd
			runner := benchmark.NewRunner(cfg, &osExecutor{})
			run, err := runner.Run(cmd.Context(), corpus)
			if err != nil {
				return trace.Wrap(err, "run benchmark")
			}
			data, err := json.MarshalIndent(run, "", "  ")
			if err != nil {
				return trace.Wrap(err, "marshal run")
			}
			if outPath != "" {
				if err := os.WriteFile(outPath, data, 0o644); err != nil {
					return trace.Wrap(err, "write output")
				}
				fmt.Fprintf(stdout(cmd), "Results saved to %s\n", outPath)
			} else {
				fmt.Fprintln(stdout(cmd), string(data))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&corpusPath, "corpus", "testdata/benchmark/queries.yaml", "Path to corpus YAML file")
	cmd.Flags().StringVar(&cwd, "cwd", "", "Benchmark workspace directory (required)")
	cmd.Flags().StringVar(&mode, "mode", "with-ccg", "Benchmark mode: with-ccg or without-ccg")
	cmd.Flags().StringVar(&outPath, "out", "", "Output file path (JSON); defaults to stdout")
	return cmd
}

// newBenchmarkAnalyzeCmd parses a Claude Code session JSONL and extracts RunResults.
func newBenchmarkAnalyzeCmd() *cobra.Command {
	var sessionPath string
	cmd := &cobra.Command{
		Use:   "analyze",
		Short: "Parse a Claude Code session JSONL and extract query run results",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if sessionPath == "" {
				return fmt.Errorf("--session is required: path to Claude Code session JSONL file")
			}
			msgs, err := benchmark.ParseJSONL(sessionPath)
			if err != nil {
				return trace.Wrap(err, "parse JSONL")
			}
			segs, err := benchmark.ExtractQuerySegments(msgs)
			if err != nil {
				return trace.Wrap(err, "extract segments")
			}
			results := make([]benchmark.RunResult, 0, len(segs))
			for _, seg := range segs {
				results = append(results, benchmark.ExtractRunResult(seg.QueryID, seg))
			}
			data, err := json.MarshalIndent(results, "", "  ")
			if err != nil {
				return trace.Wrap(err, "marshal results")
			}
			fmt.Fprintln(stdout(cmd), string(data))
			return nil
		},
	}
	cmd.Flags().StringVar(&sessionPath, "session", "", "Path to Claude Code session JSONL file (required)")
	return cmd
}

// newBenchmarkCompareCmd compares two BenchmarkRun JSON files.
func newBenchmarkCompareCmd() *cobra.Command {
	var withPath, withoutPath, corpusPath string
	cmd := &cobra.Command{
		Use:   "compare",
		Short: "Compare two benchmark runs (with-ccg vs without-ccg)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			withRun, err := loadBenchmarkRun(withPath)
			if err != nil {
				return trace.Wrap(err, "load with-ccg run")
			}
			var withoutRun *benchmark.BenchmarkRun
			if withoutPath != "" {
				withoutRun, err = loadBenchmarkRun(withoutPath)
				if err != nil {
					return trace.Wrap(err, "load without-ccg run")
				}
			}
			var corpus *benchmark.Corpus
			if corpusPath != "" {
				corpus, err = benchmark.LoadCorpus(corpusPath)
				if err != nil {
					return trace.Wrap(err, "load corpus")
				}
			} else {
				corpus = &benchmark.Corpus{}
			}
			report := benchmark.Compare(withRun, withoutRun, corpus)
			data, err := json.MarshalIndent(report, "", "  ")
			if err != nil {
				return trace.Wrap(err, "marshal report")
			}
			fmt.Fprintln(stdout(cmd), string(data))
			return nil
		},
	}
	cmd.Flags().StringVar(&withPath, "with", "", "Path to with-ccg run JSON (required)")
	cmd.Flags().StringVar(&withoutPath, "without", "", "Path to without-ccg run JSON (optional)")
	cmd.Flags().StringVar(&corpusPath, "corpus", "", "Path to corpus YAML for expected matching")
	_ = cmd.MarkFlagRequired("with")
	return cmd
}

// newBenchmarkReportCmd generates a markdown report from a ComparisonReport JSON.
func newBenchmarkReportCmd() *cobra.Command {
	var compPath, outPath string
	cmd := &cobra.Command{
		Use:   "report",
		Short: "Generate a markdown report from a comparison JSON file",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if compPath == "" {
				return fmt.Errorf("--comparison is required")
			}
			data, err := os.ReadFile(compPath)
			if err != nil {
				return trace.Wrap(err, "read comparison")
			}
			var report benchmark.ComparisonReport
			if err := json.Unmarshal(data, &report); err != nil {
				return trace.Wrap(err, "parse comparison")
			}
			if err := benchmark.WriteReport(&report, outPath); err != nil {
				return trace.Wrap(err, "write report")
			}
			fmt.Fprintf(stdout(cmd), "Report written to %s\n", outPath)
			return nil
		},
	}
	cmd.Flags().StringVar(&compPath, "comparison", "", "Path to comparison JSON file (required)")
	cmd.Flags().StringVar(&outPath, "out", "report.md", "Output markdown file path")
	return cmd
}

// loadBenchmarkRun reads and unmarshals a BenchmarkRun from a JSON file.
func loadBenchmarkRun(path string) (*benchmark.BenchmarkRun, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var run benchmark.BenchmarkRun
	if err := json.Unmarshal(data, &run); err != nil {
		return nil, err
	}
	return &run, nil
}

// newBenchmarkTokenBenchCmd measures token reduction: naive file reading vs CCG graph query.
func newBenchmarkTokenBenchCmd(deps *Deps) *cobra.Command {
	var corpusPath, repoRoot, outPath string
	var exts []string
	cmd := &cobra.Command{
		Use:   "token-bench",
		Short: "Measure token reduction: naive file reading vs CCG graph query (no LLM)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if deps.DB == nil {
				return errDBNotInitialized
			}
			corpus, err := benchmark.LoadCorpus(corpusPath)
			if err != nil {
				return trace.Wrap(err, "load corpus")
			}
			backend := storesearch.NewSQLiteBackend()
			results, err := benchmark.RunTokenBench(cmd.Context(), deps.DB, backend, gormstore.New(deps.DB), corpus, repoRoot, exts)
			if err != nil {
				return trace.Wrap(err, "run token bench")
			}
			data, err := json.MarshalIndent(results, "", "  ")
			if err != nil {
				return trace.Wrap(err, "marshal results")
			}
			if outPath != "" {
				if err := os.WriteFile(outPath, data, 0o644); err != nil {
					return trace.Wrap(err, "write output")
				}
				fmt.Fprintf(stdout(cmd), "Results saved to %s\n", outPath)
			} else {
				fmt.Fprintln(stdout(cmd), string(data))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&corpusPath, "corpus", "testdata/benchmark/queries.yaml", "Path to corpus YAML file")
	cmd.Flags().StringVar(&repoRoot, "repo", ".", "Repository root to measure naive tokens")
	cmd.Flags().StringVar(&outPath, "out", "", "Output JSON file; defaults to stdout")
	cmd.Flags().StringSliceVar(&exts, "exts", []string{".go"}, "Source file extensions to count")
	return cmd
}

// osExecutor implements benchmark.Executor using os/exec to call the `claude` CLI.
type osExecutor struct{}

func (e *osExecutor) Execute(ctx context.Context, args []string, prompt, dir string) ([]byte, error) {
	c := exec.CommandContext(ctx, "claude", args...)
	c.Stdin = strings.NewReader(prompt)
	if dir != "" {
		c.Dir = dir
	}
	out, err := c.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
			return out, fmt.Errorf("%w: %s", err, exitErr.Stderr)
		}
	}
	return out, err
}
