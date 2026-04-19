// Package benchmark provides types and utilities for benchmarking ccg MCP tool effectiveness.
package benchmark

import "time"

// Query represents a single benchmark query with expected results.
type Query struct {
	ID              string   `yaml:"id"               json:"id"`
	Description     string   `yaml:"description"      json:"description"`
	ExpectedFiles   []string `yaml:"expected_files"   json:"expected_files,omitempty"`
	ExpectedSymbols []string `yaml:"expected_symbols" json:"expected_symbols,omitempty"`
	Difficulty      string   `yaml:"difficulty"       json:"difficulty,omitempty"`
}

// Corpus holds the collection of benchmark queries.
type Corpus struct {
	Version string  `yaml:"version" json:"version,omitempty"`
	Queries []Query `yaml:"queries" json:"queries"`
}

// ToolCall records a single tool invocation during query execution.
type ToolCall struct {
	Tool  string `json:"tool"`
	Input string `json:"input,omitempty"`
}

// RunResult captures the outcome of executing a single query.
type RunResult struct {
	QueryID      string     `json:"query_id"`
	ToolCalls    []ToolCall `json:"tool_calls,omitempty"`
	FilesRead    []string   `json:"files_read,omitempty"`
	Answer       string     `json:"answer,omitempty"`
	InputTokens  int        `json:"input_tokens"`
	OutputTokens int        `json:"output_tokens"`
	ElapsedMs    int64      `json:"elapsed_ms"`
	Error        string     `json:"error,omitempty"`
}

// BenchmarkRun holds all results from a single benchmark execution.
type BenchmarkRun struct {
	Mode    string      `json:"mode"`
	RunAt   time.Time   `json:"run_at"`
	Results []RunResult `json:"results"`
}

// ResultByID returns the RunResult for the given query ID, or nil if not found.
func (r *BenchmarkRun) ResultByID(id string) *RunResult {
	for i := range r.Results {
		if r.Results[i].QueryID == id {
			return &r.Results[i]
		}
	}
	return nil
}

// MatchResult holds the computed match metrics for a single query.
type MatchResult struct {
	QueryID          string  `json:"query_id"`
	FileHitRatio     float64 `json:"file_hit_ratio"`
	SymbolHitRatio   float64 `json:"symbol_hit_ratio"`
	TotalToolCalls   int     `json:"total_tool_calls"`
	CcgToolCalls     int     `json:"ccg_tool_calls"`
	TotalInputTokens int     `json:"total_input_tokens"`
}

// ComparisonReport holds a comparison between two benchmark runs.
type ComparisonReport struct {
	WithCCG        *BenchmarkRun `json:"with_ccg"`
	WithoutCCG     *BenchmarkRun `json:"without_ccg,omitempty"`
	Matches        []MatchResult `json:"matches"`
	MatchesWithout []MatchResult `json:"matches_without,omitempty"`
}
