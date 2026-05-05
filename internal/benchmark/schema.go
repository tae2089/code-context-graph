// Package benchmark provides types and utilities for benchmarking ccg MCP tool effectiveness.
package benchmark

import "time"

// Query represents a single benchmark query with expected results.
// @intent define one benchmark prompt and the files or symbols it is expected to surface.
type Query struct {
	ID                      string   `yaml:"id"                       json:"id"`
	Description             string   `yaml:"description"              json:"description"`
	ExpectedFiles           []string `yaml:"expected_files"           json:"expected_files,omitempty"`
	ExpectedSymbols         []string `yaml:"expected_symbols"         json:"expected_symbols,omitempty"`
	ExpectedStrictSymbols    []string `yaml:"expected_strict_symbols"  json:"expected_strict_symbols,omitempty"`
	ExpectedTentativeSymbols []string `yaml:"expected_tentative_symbols" json:"expected_tentative_symbols,omitempty"`
	Difficulty              string   `yaml:"difficulty"               json:"difficulty,omitempty"`
}

// Corpus holds the collection of benchmark queries.
// @intent group benchmark queries into a reusable corpus that can be run and validated together.
type Corpus struct {
	Version string  `yaml:"version" json:"version,omitempty"`
	Queries []Query `yaml:"queries" json:"queries"`
}

// ToolCall records a single tool invocation during query execution.
// @intent capture the tool usage footprint of one benchmarked query execution.
type ToolCall struct {
	Tool      string `json:"tool"`
	ToolUseID string `json:"tool_use_id,omitempty"`
	Input     string `json:"input,omitempty"`
	Output    string `json:"output,omitempty"`
}

// RunResult captures the outcome of executing a single query.
// @intent store answer text, tool usage, timing, and token counts for one benchmark query.
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
// @intent represent one complete benchmark session for a given execution mode.
type BenchmarkRun struct {
	Mode    string      `json:"mode"`
	RunAt   time.Time   `json:"run_at"`
	Results []RunResult `json:"results"`
}

// ResultByID returns the RunResult for the given query ID, or nil if not found.
// @intent support direct lookup of a query result inside a benchmark run.
func (r *BenchmarkRun) ResultByID(id string) *RunResult {
	for i := range r.Results {
		if r.Results[i].QueryID == id {
			return &r.Results[i]
		}
	}
	return nil
}

// MatchResult holds the computed match metrics for a single query.
// @intent summarize scored file, symbol, tool, and token metrics for one benchmark query.
type MatchResult struct {
	QueryID                string  `json:"query_id"`
	FileHitRatio           float64 `json:"file_hit_ratio"`
	SymbolHitRatio         float64 `json:"symbol_hit_ratio"`
	StrictSymbolHitRatio    float64 `json:"strict_symbol_hit_ratio"`
	TentativeSymbolHitRatio float64 `json:"tentative_symbol_hit_ratio"`
	LLMStrictBias          float64 `json:"llm_strict_bias"`
	StrictContaminationRate float64 `json:"strict_contamination_rate"`
	ToolAwareStrictRatio    float64 `json:"tool_aware_strict_ratio"`
	ToolAwareTentativeRatio float64 `json:"tool_aware_tentative_ratio"`
	TotalToolCalls         int     `json:"total_tool_calls"`
	CcgToolCalls           int     `json:"ccg_tool_calls"`
	TotalInputTokens       int     `json:"total_input_tokens"`
}

// ComparisonReport holds a comparison between two benchmark runs.
// @intent package benchmark runs and their scored matches into one comparison artifact.
type ComparisonReport struct {
	WithCCG        *BenchmarkRun `json:"with_ccg"`
	WithoutCCG     *BenchmarkRun `json:"without_ccg,omitempty"`
	Matches        []MatchResult `json:"matches"`
	MatchesWithout []MatchResult `json:"matches_without,omitempty"`
}
