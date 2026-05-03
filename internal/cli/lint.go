package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"reflect"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/tae2089/trace"
	"go.yaml.in/yaml/v3"

	"github.com/tae2089/code-context-graph/internal/docs"
	"github.com/tae2089/code-context-graph/internal/pathutil"
)

// countNonIgnored counts lint issues not covered by an ignore rule in .ccg.yaml.
// @intent strict 모드에서 실제 실패로 간주할 lint 항목만 다시 집계한다.
// @domainRule action: ignore로 선언된 규칙은 strict 실패 수에서 제외한다.
func countNonIgnored(report *docs.LintReport) int {
	return countNonIgnoredWithRules(report, configuredLintRules())
}

func countNonIgnoredWithRules(report *docs.LintReport, rules []lintRule) int {
	ignoreSet := map[string]bool{}
	var ignoreRegexps []*regexp.Regexp

	for _, rule := range rules {
		if rule.Action != "ignore" || rule.Pattern == "" {
			continue
		}
		if pathutil.IsRegexPattern(rule.Pattern) {
			if re, err := regexp.Compile(rule.Pattern); err == nil {
				ignoreRegexps = append(ignoreRegexps, re)
			}
		} else {
			ignoreSet[rule.Pattern] = true
		}
	}

	isIgnored := func(value string) bool {
		if ignoreSet[value] {
			return true
		}
		for _, re := range ignoreRegexps {
			if re.MatchString(value) {
				return true
			}
		}
		return false
	}

	total := 0
	for _, v := range report.Orphans {
		if !isIgnored(v) {
			total++
		}
	}
	for _, v := range report.Missing {
		if !isIgnored(v) {
			total++
		}
	}
	for _, v := range report.Stale {
		if !isIgnored(v) {
			total++
		}
	}
	for _, v := range report.Unannotated {
		if !isIgnored(v) {
			total++
		}
	}
	for _, c := range report.Contradictions {
		if !isIgnored(c.QualifiedName) {
			total++
		}
	}
	for _, d := range report.DeadRefs {
		if !isIgnored(d.QualifiedName) {
			total++
		}
	}
	for _, v := range report.Incomplete {
		if !isIgnored(v) {
			total++
		}
	}
	for _, v := range report.Drifted {
		if !isIgnored(v) {
			total++
		}
	}
	return total
}

func configuredLintRules() []lintRule {
	var rules []lintRule
	if err := viper.UnmarshalKey("rules", &rules); err == nil && len(rules) > 0 {
		return rules
	}
	return flattenLintRules(viper.Get("rules"))
}

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
	if v, ok := lookup("auto"); ok {
		rule.Auto, _ = v.(bool)
	}
	if v, ok := lookup("created"); ok {
		rule.Created, _ = v.(string)
	}
	return rule, rule.Pattern != "" || rule.Category != "" || rule.Action != ""
}

type lintRuleDoc struct {
	Rules []lintRule `yaml:"rules"`
}

type lintRule struct {
	Pattern  string `yaml:"pattern"`
	Category string `yaml:"category"`
	Action   string `yaml:"action"`
	Auto     bool   `yaml:"auto,omitempty"`
	Created  string `yaml:"created,omitempty"`
}

func currentLintStateDir(historyDir string) string {
	if historyDir != "" {
		return historyDir
	}
	return ".ccg"
}

func mergeLintRulesWithAutoRules(stateDir string) ([]lintRule, error) {
	autoRules, err := docs.LoadAutoRules(filepath.Join(stateDir, "auto-rules.yaml"))
	if err != nil {
		return nil, err
	}
	baseRules := configuredLintRules()
	merged := make([]lintRule, 0, len(baseRules)+len(autoRules.Rules))
	for _, rule := range baseRules {
		merged = append(merged, rule)
	}
	for _, rule := range autoRules.Rules {
		merged = append(merged, lintRule{
			Pattern:  rule.Pattern,
			Category: rule.Category,
			Action:   rule.Action,
			Auto:     rule.Auto,
			Created:  rule.Created,
		})
	}
	return merged, nil
}

func migrateAutoRulesFromConfig(cfgPath, stateDir string) (int, error) {
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		return 0, err
	}

	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return 0, err
	}

	autoRules, err := docs.LoadAutoRules(filepath.Join(stateDir, "auto-rules.yaml"))
	if err != nil {
		return 0, err
	}

	triggered := make([]string, 0)
	if len(root.Content) == 0 {
		return 0, nil
	}
	doc := root.Content[0]
	if doc.Kind != yaml.MappingNode {
		return 0, nil
	}

	for i := 0; i+1 < len(doc.Content); i += 2 {
		key := doc.Content[i]
		value := doc.Content[i+1]
		if key.Value != "rules" || value.Kind != yaml.SequenceNode {
			continue
		}

		kept := make([]*yaml.Node, 0, len(value.Content))
		for _, item := range value.Content {
			rule, isAuto := decodeLintRuleNode(item)
			if isAuto {
				if rule.Category != "" && rule.Pattern != "" {
					triggered = append(triggered, rule.Category+":"+rule.Pattern)
				}
				continue
			}
			kept = append(kept, item)
		}
		value.Content = kept
		break
	}

	if len(triggered) == 0 {
		return 0, nil
	}

	added := autoRules.Upsert(triggered)
	if err := autoRules.Save(filepath.Join(stateDir, "auto-rules.yaml")); err != nil {
		return 0, err
	}
	out, err := yaml.Marshal(&root)
	if err != nil {
		return 0, err
	}
	if err := os.WriteFile(cfgPath, out, 0o644); err != nil {
		return 0, err
	}
	return len(added), nil
}

func decodeLintRuleNode(node *yaml.Node) (lintRule, bool) {
	var rule lintRule
	if node == nil || node.Kind != yaml.MappingNode {
		return rule, false
	}
	if err := node.Decode(&rule); err != nil {
		return lintRule{}, false
	}
	return rule, rule.Auto
}

// newLintCmd creates the docs lint command.
// @intent 문서 품질 점검과 Twice Rule 자동 기록을 하나의 CLI 흐름으로 제공한다.
// @domainRule 같은 이슈가 두 번 연속 발견되면 warn 규칙 후보로 승격한다.
// @requires deps.DB가 초기화되어 있어야 한다.
// @sideEffect lint 이력 파일과 .ccg.yaml 규칙을 갱신할 수 있다.
// @mutates lint history 파일, .ccg.yaml rules 섹션
func newLintCmd(deps *Deps) *cobra.Command {
	var outDir string
	var excludePatterns []string
	var strict bool
	var historyDir string
	var migrateAutoRules bool

	cmd := &cobra.Command{
		Use:   "lint",
		Short: "Check documentation for orphan, missing, and stale files",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if deps.DB == nil {
				return errDBNotInitialized
			}

			stateDir := currentLintStateDir(historyDir)
			lintRules := configuredLintRules()
			if migrateAutoRules {
				cfgPath := viper.ConfigFileUsed()
				if cfgPath == "" {
					cfgPath = ".ccg.yaml"
				}
				migrated, err := migrateAutoRulesFromConfig(cfgPath, stateDir)
				if err != nil {
					return trace.Wrap(err, "migrate auto rules")
				}
				if migrated == 0 {
					fmt.Fprintln(stdout(cmd), "Nothing to migrate.")
				} else {
					fmt.Fprintf(stdout(cmd), "Migrated %d auto rules to .ccg/auto-rules.yaml\n", migrated)
				}
				return nil
			}
			if merged, err := mergeLintRulesWithAutoRules(stateDir); err != nil {
				deps.Logger.Warn("load auto rules for lint failed", "error", err)
			} else {
				lintRules = merged
			}

			absOut, err := filepath.Abs(resolveOutDir(outDir))
			if err != nil {
				return trace.Wrap(err, "resolve out path")
			}

			gen := &docs.Generator{
				DB:        deps.DB,
				OutDir:    absOut,
				Exclude:   resolveExcludes(excludePatterns),
				Namespace: viper.GetString("namespace"),
			}

			report, err := gen.Lint()
			if err != nil {
				return trace.Wrap(err, "lint")
			}

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

			// Twice Rule: compare with previous run history
			{
				hDir := stateDir
				histPath := filepath.Join(hDir, "lint-history.json")

				history, err := docs.LoadHistory(histPath)
				if err != nil {
					deps.Logger.Warn("load lint history failed", "error", err)
				} else {
					// Build current keys as "category:value"
					var currentKeys []string
					for _, v := range report.Orphans {
						currentKeys = append(currentKeys, "orphan:"+v)
					}
					for _, v := range report.Missing {
						currentKeys = append(currentKeys, "missing:"+v)
					}
					for _, v := range report.Stale {
						currentKeys = append(currentKeys, "stale:"+v)
					}
					for _, v := range report.Unannotated {
						currentKeys = append(currentKeys, "unannotated:"+v)
					}
					for _, c := range report.Contradictions {
						currentKeys = append(currentKeys, "contradiction:"+c.QualifiedName)
					}
					for _, d := range report.DeadRefs {
						currentKeys = append(currentKeys, "dead-ref:"+d.QualifiedName)
					}
					for _, v := range report.Incomplete {
						currentKeys = append(currentKeys, "incomplete:"+v)
					}
					for _, v := range report.Drifted {
						currentKeys = append(currentKeys, "drift:"+v)
					}

					triggered := history.Update(currentKeys)
					if saveErr := history.Save(histPath); saveErr != nil {
						deps.Logger.Warn("save lint history failed", "error", saveErr)
					}

					if len(triggered) > 0 {
						autoRulesPath := filepath.Join(hDir, "auto-rules.yaml")
						autoRules, loadErr := docs.LoadAutoRules(autoRulesPath)
						if loadErr != nil {
							deps.Logger.Warn("load auto rules failed", "error", loadErr)
						} else {
							added := autoRules.Upsert(triggered)
							if saveErr := autoRules.Save(autoRulesPath); saveErr != nil {
								deps.Logger.Warn("save auto rules failed", "error", saveErr)
							}
							fmt.Fprintf(out, "Twice Rule triggered (%d):\n", len(triggered))
							for _, key := range triggered {
								fmt.Fprintf(out, "  %s → recorded in .ccg/auto-rules.yaml (warn)\n", key)
							}
							fmt.Fprintln(out)
							if len(added) == 0 {
								deps.Logger.Debug("no new auto rules added", "path", autoRulesPath)
							}
						}
					}
				}
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
	cmd.Flags().StringVar(&historyDir, "history-dir", "", "Directory for lint history (default: .ccg)")
	cmd.Flags().BoolVar(&migrateAutoRules, "migrate-auto-rules", false, "Move legacy auto: true lint rules from .ccg.yaml into .ccg/auto-rules.yaml")
	_ = cmd.Flags().MarkHidden("migrate-auto-rules")
	return cmd
}
