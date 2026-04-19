package benchmark

import "strings"

// fileMatches reports whether actual matches expected, supporting both
// exact match and suffix match to handle absolute vs relative path differences.
// e.g. expected "internal/webhook/handler.go" matches actual "/home/user/repo/internal/webhook/handler.go"
func fileMatches(expected, actual string) bool {
	if expected == actual {
		return true
	}
	return strings.HasSuffix(actual, "/"+expected) || strings.HasSuffix(actual, "\\"+expected)
}

// MatchFiles computes the ratio of expected files found in FilesRead or mentioned
// in the Answer text (as a fallback when tool-call data is unavailable).
// Returns 1.0 if no expected files are specified.
func MatchFiles(result RunResult, query Query) float64 {
	if len(query.ExpectedFiles) == 0 {
		return 1.0
	}
	var matched int
	for _, expected := range query.ExpectedFiles {
		found := false
		for _, actual := range result.FilesRead {
			if fileMatches(expected, actual) {
				found = true
				break
			}
		}
		if !found && result.Answer != "" {
			base := expected[strings.LastIndex(expected, "/")+1:]
			found = strings.Contains(result.Answer, expected) || strings.Contains(result.Answer, base)
		}
		if found {
			matched++
		}
	}
	return float64(matched) / float64(len(query.ExpectedFiles))
}

// MatchSymbols computes the ratio of expected symbols found in the answer text.
// Returns 1.0 if no expected symbols are specified.
func MatchSymbols(result RunResult, query Query) float64 {
	if len(query.ExpectedSymbols) == 0 {
		return 1.0
	}
	var matched int
	for _, sym := range query.ExpectedSymbols {
		if strings.Contains(result.Answer, sym) {
			matched++
		}
	}
	return float64(matched) / float64(len(query.ExpectedSymbols))
}

// CountCcgToolCalls returns the number of tool calls using mcp__ccg__ prefix.
func CountCcgToolCalls(result RunResult) int {
	var count int
	for _, tc := range result.ToolCalls {
		if strings.HasPrefix(tc.Tool, "mcp__ccg__") {
			count++
		}
	}
	return count
}

// ComputeMatch derives a MatchResult from a single RunResult and its Query.
func ComputeMatch(result RunResult, query Query) MatchResult {
	return MatchResult{
		QueryID:          result.QueryID,
		FileHitRatio:     MatchFiles(result, query),
		SymbolHitRatio:   MatchSymbols(result, query),
		TotalToolCalls:   len(result.ToolCalls),
		CcgToolCalls:     CountCcgToolCalls(result),
		TotalInputTokens: result.InputTokens,
	}
}

// AnalyzeRun computes MatchResult for every result in the run against the corpus.
// Results with no matching query are skipped.
func AnalyzeRun(run *BenchmarkRun, corpus *Corpus) []MatchResult {
	queryMap := make(map[string]Query, len(corpus.Queries))
	for _, q := range corpus.Queries {
		queryMap[q.ID] = q
	}
	var matches []MatchResult
	for _, r := range run.Results {
		q, ok := queryMap[r.QueryID]
		if !ok {
			continue
		}
		matches = append(matches, ComputeMatch(r, q))
	}
	return matches
}
