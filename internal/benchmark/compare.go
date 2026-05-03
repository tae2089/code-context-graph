// @index Comparison helpers for benchmark runs with and without CCG.
package benchmark

// Compare builds a ComparisonReport from a with-ccg run and an optional without-ccg run.
// Matches contains with-ccg metrics; MatchesWithout contains without-ccg metrics when provided.
// @intent produce one report that captures both benchmark modes and their scored query matches.
func Compare(withCCG *BenchmarkRun, withoutCCG *BenchmarkRun, corpus *Corpus) *ComparisonReport {
	report := &ComparisonReport{
		WithCCG:    withCCG,
		WithoutCCG: withoutCCG,
		Matches:    AnalyzeRun(withCCG, corpus),
	}
	if withoutCCG != nil {
		report.MatchesWithout = AnalyzeRun(withoutCCG, corpus)
	}
	return report
}
