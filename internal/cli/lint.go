package cli

import (
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/tae2089/trace"

	"github.com/imtaebin/code-context-graph/internal/docs"
)

// countNonIgnored counts lint issues not covered by an ignore rule in .ccg.yaml.
// @intent strict 모드에서 실제 실패로 간주할 lint 항목만 다시 집계한다.
// @domainRule action: ignore로 선언된 규칙은 strict 실패 수에서 제외한다.
func countNonIgnored(report *docs.LintReport) int {
	rules := viper.Get("rules")
	ignoreSet := map[string]bool{}

	if ruleSlice, ok := rules.([]any); ok {
		for _, r := range ruleSlice {
			if rm, ok := r.(map[string]any); ok {
				action, _ := rm["action"].(string)
				pattern, _ := rm["pattern"].(string)
				if action == "ignore" && pattern != "" {
					ignoreSet[pattern] = true
				}
			}
		}
	}

	total := 0
	for _, v := range report.Orphans {
		if !ignoreSet[v] {
			total++
		}
	}
	for _, v := range report.Missing {
		if !ignoreSet[v] {
			total++
		}
	}
	for _, v := range report.Stale {
		if !ignoreSet[v] {
			total++
		}
	}
	for _, v := range report.Unannotated {
		if !ignoreSet[v] {
			total++
		}
	}
	for _, c := range report.Contradictions {
		if !ignoreSet[c.QualifiedName] {
			total++
		}
	}
	for _, d := range report.DeadRefs {
		if !ignoreSet[d.QualifiedName] {
			total++
		}
	}
	for _, v := range report.Incomplete {
		if !ignoreSet[v] {
			total++
		}
	}
	for _, v := range report.Drifted {
		if !ignoreSet[v] {
			total++
		}
	}
	return total
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

	cmd := &cobra.Command{
		Use:   "lint",
		Short: "Check documentation for orphan, missing, and stale files",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if deps.DB == nil {
				return errDBNotInitialized
			}

			absOut, err := filepath.Abs(resolveOutDir(outDir))
			if err != nil {
				return trace.Wrap(err, "resolve out path")
			}

			gen := &docs.Generator{
				DB:      deps.DB,
				OutDir:  absOut,
				Exclude: resolveExcludes(excludePatterns),
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
				hDir := historyDir
				if hDir == "" {
					hDir = ".ccg"
				}
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
						fmt.Fprintf(out, "Twice Rule triggered (%d):\n", len(triggered))
						for _, key := range triggered {
							fmt.Fprintf(out, "  %s → added to .ccg.yaml rules (warn)\n", key)
						}
						fmt.Fprintln(out)

						cfgPath := ".ccg.yaml"
						if writeErr := docs.WriteYamlRules(cfgPath, triggered); writeErr != nil {
							deps.Logger.Warn("write yaml rules failed", "error", writeErr)
						}
					}
				}
			}

			if strict {
				strictTotal := countNonIgnored(report)
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
	return cmd
}
