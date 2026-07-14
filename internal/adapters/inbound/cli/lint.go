package cli

import (
	"fmt"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/tae2089/trace"

	"github.com/tae2089/code-context-graph/internal/adapters/outbound/contentfiles"
	"github.com/tae2089/code-context-graph/internal/app/docs"
	"github.com/tae2089/code-context-graph/internal/pathspec"
)

// normalizeLintCategory maps legacy or variant category names to canonical forms.
// @intent ensure rule matching uses consistent category keys regardless of input spelling
// @domainRule "deadref" and "drifted" are accepted aliases for "dead-ref" and "drift"
func normalizeLintCategory(category string) string {
	switch strings.ToLower(strings.TrimSpace(category)) {
	case "deadref":
		return "dead-ref"
	case "drifted":
		return "drift"
	default:
		return strings.ToLower(strings.TrimSpace(category))
	}
}

// lintRuleMatches reports whether a lint rule suppresses the given category/value pair.
// @intent determine if a single ignore rule covers a specific lint finding
// @domainRule only rules with action "ignore" and a non-empty pattern are evaluated
// @domainRule category matching is case-insensitive and alias-normalized before comparison
func lintRuleMatches(rule lintRule, category, value string) bool {
	if rule.Action != "ignore" || rule.Pattern == "" {
		return false
	}
	if rule.Category != "" && normalizeLintCategory(rule.Category) != normalizeLintCategory(category) {
		return false
	}
	if pathspec.IsRegexPattern(rule.Pattern) {
		re, err := regexp.Compile(rule.Pattern)
		return err == nil && re.MatchString(value)
	}
	return rule.Pattern == value
}

// filterIgnoredLintReport removes lint findings covered by ignore rules from the report in place.
// @intent strip suppressed findings before display and strict-mode counting
// @param report the mutable lint report to filter
// @param rules the active set of lint rules loaded from .ccg.yaml config
// @return the same report pointer with ignored entries removed
// @sideEffect modifies report slice fields directly (orphans, missing, stale, etc.)
func filterIgnoredLintReport(report *docs.LintReport, rules []lintRule) *docs.LintReport {
	isIgnored := func(category, value string) bool {
		for _, rule := range rules {
			if lintRuleMatches(rule, category, value) {
				return true
			}
		}
		return false
	}

	filterStrings := func(category string, values []string) []string {
		out := values[:0]
		for _, value := range values {
			if !isIgnored(category, value) {
				out = append(out, value)
			}
		}
		return out
	}

	report.Orphans = filterStrings("orphan", report.Orphans)
	report.Missing = filterStrings("missing", report.Missing)
	report.Stale = filterStrings("stale", report.Stale)
	report.Unannotated = filterStrings("unannotated", report.Unannotated)
	report.Incomplete = filterStrings("incomplete", report.Incomplete)
	report.Drifted = filterStrings("drift", report.Drifted)

	contradictions := report.Contradictions[:0]
	for _, contradiction := range report.Contradictions {
		if !isIgnored("contradiction", contradiction.QualifiedName) {
			contradictions = append(contradictions, contradiction)
		}
	}
	report.Contradictions = contradictions

	deadRefs := report.DeadRefs[:0]
	for _, deadRef := range report.DeadRefs {
		if !isIgnored("dead-ref", deadRef.QualifiedName) {
			deadRefs = append(deadRefs, deadRef)
		}
	}
	report.DeadRefs = deadRefs

	return report
}

// countNonIgnoredWithRules tallies lint findings not suppressed by the given rules.
// @intent compute the strict-mode failure count against an explicit rule set
// @param report the filtered or unfiltered lint report to count
// @param rules the rule set to apply for suppression
// @return total number of non-ignored findings across all categories
func countNonIgnoredWithRules(report *docs.LintReport, rules []lintRule) int {
	isIgnored := func(category, value string) bool {
		for _, rule := range rules {
			if lintRuleMatches(rule, category, value) {
				return true
			}
		}
		return false
	}

	total := 0
	for _, v := range report.Orphans {
		if !isIgnored("orphan", v) {
			total++
		}
	}
	for _, v := range report.Missing {
		if !isIgnored("missing", v) {
			total++
		}
	}
	for _, v := range report.Stale {
		if !isIgnored("stale", v) {
			total++
		}
	}
	for _, v := range report.Unannotated {
		if !isIgnored("unannotated", v) {
			total++
		}
	}
	for _, c := range report.Contradictions {
		if !isIgnored("contradiction", c.QualifiedName) {
			total++
		}
	}
	for _, d := range report.DeadRefs {
		if !isIgnored("dead-ref", d.QualifiedName) {
			total++
		}
	}
	for _, v := range report.Incomplete {
		if !isIgnored("incomplete", v) {
			total++
		}
	}
	for _, v := range report.Drifted {
		if !isIgnored("drift", v) {
			total++
		}
	}
	return total
}

// configuredLintRules loads the active lint rules from viper config.
// @intent provide the canonical rule list for filtering and strict-mode counting
// @domainRule tries typed unmarshal first; falls back to dynamic flattening for YAML map variants
// @return slice of lint rules from .ccg.yaml rules section, empty if none configured
func configuredLintRules() []lintRule {
	var rules []lintRule
	if err := viper.UnmarshalKey("rules", &rules); err == nil && len(rules) > 0 {
		return rules
	}
	return flattenLintRules(viper.Get("rules"))
}

// flattenLintRules converts a raw viper value into a typed []lintRule slice.
// @intent handle the multiple concrete types viper may return for a YAML sequence
// @domainRule supports []any, []map[string]any, and arbitrary reflect.Slice to tolerate viper's type variance
func flattenLintRules(raw any) []lintRule {
	var out []lintRule
	switch rules := raw.(type) {
	case []any:
		for _, item := range rules {
			if rule, ok := parseLintRule(item); ok {
				out = append(out, rule)
			}
		}
	case []map[string]any:
		for _, item := range rules {
			if rule, ok := parseLintRule(item); ok {
				out = append(out, rule)
			}
		}
	default:
		rv := reflect.ValueOf(raw)
		if rv.IsValid() && rv.Kind() == reflect.Slice {
			for i := 0; i < rv.Len(); i++ {
				if rule, ok := parseLintRule(rv.Index(i).Interface()); ok {
					out = append(out, rule)
				}
			}
		}
	}
	return out
}

// parseLintRule converts an arbitrary runtime value into a typed lintRule.
// @intent normalize heterogeneous viper/YAML map representations into a single lintRule struct
// @domainRule accepts lintRule directly, map[string]any, or any reflect.Map via key lookup
func parseLintRule(raw any) (lintRule, bool) {
	switch item := raw.(type) {
	case lintRule:
		return item, true
	case map[string]any:
		return lintRuleFromMapLookup(func(key string) (any, bool) {
			v, ok := item[key]
			return v, ok
		})
	default:
		rv := reflect.ValueOf(raw)
		if rv.IsValid() && rv.Kind() == reflect.Map {
			return lintRuleFromMapLookup(func(key string) (any, bool) {
				for _, candidate := range rv.MapKeys() {
					if fmt.Sprint(candidate.Interface()) == key {
						return rv.MapIndex(candidate).Interface(), true
					}
				}
				return nil, false
			})
		}
		return lintRule{}, false
	}
}

// lintRuleFromMapLookup builds a lintRule from a generic key-value lookup function.
// @intent decouple rule field extraction from the concrete map type (map[string]any or reflect.Map)
// @return (rule, true) when at least one of pattern, category, or action is present
func lintRuleFromMapLookup(lookup func(string) (any, bool)) (lintRule, bool) {
	rule := lintRule{}
	if v, ok := lookup("pattern"); ok {
		rule.Pattern, _ = v.(string)
	}
	if v, ok := lookup("category"); ok {
		rule.Category, _ = v.(string)
	}
	if v, ok := lookup("action"); ok {
		rule.Action, _ = v.(string)
	}
	return rule, rule.Pattern != "" || rule.Category != "" || rule.Action != ""
}

// lintRule is a single suppression rule entry from the .ccg.yaml rules section.
// @intent carry the pattern, category, and action that determine how a lint finding is handled
type lintRule struct {
	Pattern  string `yaml:"pattern"`
	Category string `yaml:"category"`
	Action   string `yaml:"action"`
}

// newLintCmd creates the docs lint command.
// @intent 문서 품질 점검(orphan/missing/stale/annotation)을 하나의 CLI 흐름으로 제공한다.
// @requires lint 실행은 deps.Docs가 초기화되어 있어야 한다.
func newLintCmd(deps *Deps) *cobra.Command {
	var outDir string
	var excludePatterns []string
	var strict bool

	cmd := &cobra.Command{
		Use:   "lint",
		Short: "Check documentation for orphan, missing, and stale files",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			lintRules := configuredLintRules()
			if deps.Docs == nil {
				return errDBNotInitialized
			}

			absOut, err := filepath.Abs(resolveOutDir(outDir))
			if err != nil {
				return trace.Wrap(err, "resolve out path")
			}

			gen := &docs.Generator{
				Repository: deps.Docs,
				Files:      contentfiles.NewRoot(absOut),
				OutDir:     absOut,
				Exclude:    resolveExcludes(excludePatterns),
				Namespace:  resolveNamespace(cmd),
			}

			report, err := gen.Lint()
			if err != nil {
				return trace.Wrap(err, "lint")
			}
			report = filterIgnoredLintReport(report, lintRules)

			out := stdout(cmd)
			total := len(report.Orphans) + len(report.Missing) + len(report.Stale) + len(report.Unannotated) +
				len(report.Contradictions) + len(report.DeadRefs) + len(report.Incomplete) + len(report.Drifted)

			if len(report.Orphans) > 0 {
				fmt.Fprintf(out, "Orphan docs (%d) — no matching source in graph:\n", len(report.Orphans))
				for _, p := range report.Orphans {
					fmt.Fprintf(out, "  %s\n", p)
				}
				fmt.Fprintln(out)
			}

			if len(report.Missing) > 0 {
				fmt.Fprintf(out, "Missing docs (%d) — source exists but no doc file:\n", len(report.Missing))
				for _, p := range report.Missing {
					fmt.Fprintf(out, "  %s\n", p)
				}
				fmt.Fprintln(out)
			}

			if len(report.Stale) > 0 {
				fmt.Fprintf(out, "Stale docs (%d) — source updated since doc was generated:\n", len(report.Stale))
				for _, p := range report.Stale {
					fmt.Fprintf(out, "  %s\n", p)
				}
				fmt.Fprintln(out)
			}

			if len(report.Unannotated) > 0 {
				fmt.Fprintf(out, "Unannotated symbols (%d) — no @intent or other annotations:\n", len(report.Unannotated))
				for _, qn := range report.Unannotated {
					fmt.Fprintf(out, "  %s\n", qn)
				}
				fmt.Fprintln(out)
			}

			if len(report.Contradictions) > 0 {
				fmt.Fprintf(out, "Contradictions (%d) — code changed but @param annotation not updated:\n", len(report.Contradictions))
				for _, c := range report.Contradictions {
					fmt.Fprintf(out, "  %s — %s\n", c.QualifiedName, c.Detail)
				}
				fmt.Fprintln(out)
			}

			if len(report.DeadRefs) > 0 {
				fmt.Fprintf(out, "Dead refs (%d) — @see target not found in graph:\n", len(report.DeadRefs))
				for _, d := range report.DeadRefs {
					fmt.Fprintf(out, "  %s — @see %s (not found)\n", d.QualifiedName, d.SeeTarget)
				}
				fmt.Fprintln(out)
			}

			if len(report.Incomplete) > 0 {
				fmt.Fprintf(out, "Incomplete annotations (%d) — missing @intent:\n", len(report.Incomplete))
				for _, qn := range report.Incomplete {
					fmt.Fprintf(out, "  %s\n", qn)
				}
				fmt.Fprintln(out)
			}

			if len(report.Drifted) > 0 {
				fmt.Fprintf(out, "Drifted annotations (%d) — source updated since annotation:\n", len(report.Drifted))
				for _, qn := range report.Drifted {
					fmt.Fprintf(out, "  %s\n", qn)
				}
				fmt.Fprintln(out)
			}

			if total == 0 {
				fmt.Fprintln(out, "All docs are clean — 0 issues found.")
			} else {
				fmt.Fprintf(out, "Summary: %d orphan, %d missing, %d stale, %d unannotated, %d contradiction, %d dead-ref, %d incomplete, %d drifted\n",
					len(report.Orphans), len(report.Missing), len(report.Stale), len(report.Unannotated),
					len(report.Contradictions), len(report.DeadRefs), len(report.Incomplete), len(report.Drifted))
			}

			if strict {
				strictTotal := countNonIgnoredWithRules(report, lintRules)
				if strictTotal > 0 {
					return trace.New(fmt.Sprintf("lint found %d issues", strictTotal))
				}
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&outDir, "out", "docs", "Documentation directory to lint")
	cmd.Flags().StringArrayVar(&excludePatterns, "exclude", nil, "Exclude files/paths matching pattern (repeatable)")
	cmd.Flags().BoolVar(&strict, "strict", false, "Exit with error if any issues are found (useful for CI/pre-commit)")
	return cmd
}
