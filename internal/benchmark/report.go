package benchmark

import (
	"fmt"
	"os"
	"strings"
)

// WriteReport renders a ComparisonReport as markdown and writes it to outPath.
func WriteReport(report *ComparisonReport, outPath string) error {
	if report.WithCCG == nil {
		return fmt.Errorf("comparison report missing with-ccg run")
	}
	var sb strings.Builder

	sb.WriteString("# CCG Benchmark Report\n\n")

	// Summary section
	sb.WriteString("## Summary\n\n")
	fmt.Fprintf(&sb, "- Mode: `%s`\n", report.WithCCG.Mode)
	fmt.Fprintf(&sb, "- Queries: %d\n", len(report.WithCCG.Results))
	if report.WithoutCCG != nil {
		fmt.Fprintf(&sb, "- Comparison mode: `%s` vs `%s`\n", report.WithCCG.Mode, report.WithoutCCG.Mode)
	}
	sb.WriteString("\n")

	// Per-query results table
	sb.WriteString("## Query Results\n\n")
	sb.WriteString("| Query ID | File Hit | Symbol Hit | Tool Calls | CCG Calls | Input Tokens |\n")
	sb.WriteString("|----------|----------|------------|------------|-----------|-------------|\n")
	for _, m := range report.Matches {
		fmt.Fprintf(&sb, "| %s | %.2f | %.2f | %d | %d | %d |\n",
			m.QueryID, m.FileHitRatio, m.SymbolHitRatio,
			m.TotalToolCalls, m.CcgToolCalls, m.TotalInputTokens)
	}
	sb.WriteString("\n")

	if len(report.MatchesWithout) > 0 {
		sb.WriteString("## Without CCG Results\n\n")
		sb.WriteString("| Query ID | File Hit | Symbol Hit | Tool Calls | CCG Calls | Input Tokens |\n")
		sb.WriteString("|----------|----------|------------|------------|-----------|-------------|\n")
		for _, m := range report.MatchesWithout {
			fmt.Fprintf(&sb, "| %s | %.2f | %.2f | %d | %d | %d |\n",
				m.QueryID, m.FileHitRatio, m.SymbolHitRatio,
				m.TotalToolCalls, m.CcgToolCalls, m.TotalInputTokens)
		}
		sb.WriteString("\n")
	}

	return os.WriteFile(outPath, []byte(sb.String()), 0o644)
}
