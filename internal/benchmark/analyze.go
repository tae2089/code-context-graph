// @index Match analysis helpers for benchmark run results.
package benchmark

import (
	"encoding/json"
	"strings"
)

type queryGraphResultItem struct {
	QualifiedName string `json:"qualified_name"`
	Confidence    string `json:"confidence"`
}

type queryGraphToolResponse struct {
	Pattern  string               `json:"pattern"`
	Results  []queryGraphResultItem `json:"results"`
}

type queryConfidenceCoverage struct {
	StrictCount     int
	TentativeCount  int
	StrictMatched   int
	TentativeMatched int
}

// fileMatches reports whether actual matches expected, supporting both
// exact match and suffix match to handle absolute vs relative path differences.
// e.g. expected "internal/webhook/handler.go" matches actual "/home/user/repo/internal/webhook/handler.go"
// @intent compare expected files against recorded reads without depending on absolute path stability.
func fileMatches(expected, actual string) bool {
	if expected == actual {
		return true
	}
	return strings.HasSuffix(actual, "/"+expected) || strings.HasSuffix(actual, "\\"+expected)
}

// MatchFiles computes the ratio of expected files found in FilesRead or mentioned
// in the Answer text (as a fallback when tool-call data is unavailable).
// Returns 1.0 if no expected files are specified.
// @intent score whether a benchmark run inspected the files the query was expected to touch.
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
// @intent score whether the final answer mentioned the symbols the query was expected to surface.
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
// @intent quantify how much a benchmark run relied on CCG-specific MCP tools.
func CountCcgToolCalls(result RunResult) int {
	var count int
	for _, tc := range result.ToolCalls {
		if strings.HasPrefix(tc.Tool, "mcp__ccg__") {
			count++
		}
	}
	return count
}

// matchSymbolsFromToolOutputs computes strict/tentative symbol hit ratios from query_graph
// call outputs. Returns 1.0 when no expectation is provided.
func matchSymbolsFromToolOutputs(result RunResult, expectedStrict, expectedTentative []string) queryConfidenceCoverage {
	expectedStrictSet := make(map[string]struct{}, len(expectedStrict))
	for _, sym := range expectedStrict {
		expectedStrictSet[sym] = struct{}{}
	}
	expectedTentativeSet := make(map[string]struct{}, len(expectedTentative))
	for _, sym := range expectedTentative {
		expectedTentativeSet[sym] = struct{}{}
	}

	coverage := queryConfidenceCoverage{
		StrictCount:     len(expectedStrict),
		TentativeCount:  len(expectedTentative),
	}
	strictMatched := make(map[string]struct{}, len(expectedStrict))
	tentativeMatched := make(map[string]struct{}, len(expectedTentative))

	for _, tc := range result.ToolCalls {
		if tc.Tool != "mcp__ccg__query_graph" || tc.Output == "" {
			continue
		}
		var parsed queryGraphToolResponse
		if err := json.Unmarshal([]byte(tc.Output), &parsed); err != nil {
			continue
		}
		if parsed.Pattern != "callers_of" && parsed.Pattern != "callees_of" {
			continue
		}
		for _, item := range parsed.Results {
			switch item.Confidence {
			case "strict":
				if _, ok := expectedStrictSet[item.QualifiedName]; ok {
					if _, exists := strictMatched[item.QualifiedName]; exists {
						continue
					}
					coverage.StrictMatched++
					strictMatched[item.QualifiedName] = struct{}{}
				}
			case "tentative":
				if _, ok := expectedTentativeSet[item.QualifiedName]; ok {
					if _, exists := tentativeMatched[item.QualifiedName]; exists {
						continue
					}
					coverage.TentativeMatched++
					tentativeMatched[item.QualifiedName] = struct{}{}
				}
			}
		}
	}
	return coverage
}

func safeRatio(n, d int) float64 {
	if d == 0 {
		return 1.0
	}
	return float64(n) / float64(d)
}

// computeLLMStrictBias returns:
// - 1.0 when strict expectations are all met or no strict expectation exists.
// - 0.5 when strict is mentioned but tentative evidence appears equally or less.
// - 0.0 when strict was ignored while tentative was preferred or no strict evidence exists.
func computeLLMStrictBias(result RunResult, query Query) float64 {
	strictHit := MatchSymbols(result, Query{ExpectedSymbols: query.ExpectedStrictSymbols})
	tentativeHit := MatchSymbols(result, Query{ExpectedSymbols: query.ExpectedTentativeSymbols})
	if len(query.ExpectedStrictSymbols) == 0 {
		return 1.0
	}
	if strictHit == 0.0 {
		return 0.0
	}
	if len(query.ExpectedTentativeSymbols) == 0 || strictHit >= tentativeHit {
		return 1.0
	}
	if strictHit > 0.0 {
		return 0.5
	}
	return 0.0
}

func computeStrictContamination(result RunResult, query Query) float64 {
	strictHit := MatchSymbols(result, Query{ExpectedSymbols: query.ExpectedStrictSymbols})
	tentativeHit := MatchSymbols(result, Query{ExpectedSymbols: query.ExpectedTentativeSymbols})
	if len(query.ExpectedStrictSymbols) == 0 || len(query.ExpectedTentativeSymbols) == 0 {
		return 0.0
	}
	if strictHit > 0.0 {
		return 0.0
	}
	return tentativeHit
}

// ComputeMatch derives a MatchResult from a single RunResult and its Query.
// @intent consolidate per-query benchmark scoring into one reusable result structure.
func ComputeMatch(result RunResult, query Query) MatchResult {
	coverage := matchSymbolsFromToolOutputs(result, query.ExpectedStrictSymbols, query.ExpectedTentativeSymbols)
	return MatchResult{
		QueryID:                result.QueryID,
		FileHitRatio:           MatchFiles(result, query),
		SymbolHitRatio:         MatchSymbols(result, query),
		StrictSymbolHitRatio:    MatchSymbols(result, Query{ExpectedSymbols: query.ExpectedStrictSymbols}),
		TentativeSymbolHitRatio: MatchSymbols(result, Query{ExpectedSymbols: query.ExpectedTentativeSymbols}),
		LLMStrictBias:           computeLLMStrictBias(result, query),
		StrictContaminationRate:  computeStrictContamination(result, query),
		ToolAwareStrictRatio:     safeRatio(coverage.StrictMatched, coverage.StrictCount),
		ToolAwareTentativeRatio:  safeRatio(coverage.TentativeMatched, coverage.TentativeCount),
		TotalToolCalls:          len(result.ToolCalls),
		CcgToolCalls:            CountCcgToolCalls(result),
		TotalInputTokens:        result.InputTokens,
	}
}

// AnalyzeRun computes MatchResult for every result in the run against the corpus.
// Results with no matching query are skipped.
// @intent turn a full benchmark run into per-query scored matches against the corpus definition.
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
