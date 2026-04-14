package cli

import (
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/imtaebin/code-context-graph/internal/docs"
)

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
				return fmt.Errorf("database not initialized")
			}

			absOut, err := filepath.Abs(resolveOutDir(outDir))
			if err != nil {
				return fmt.Errorf("resolve out path: %w", err)
			}

			gen := &docs.Generator{
				DB:      deps.DB,
				OutDir:  absOut,
				Exclude: resolveExcludes(excludePatterns),
			}

			report, err := gen.Lint()
			if err != nil {
				return fmt.Errorf("lint: %w", err)
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

			if strict && total > 0 {
				return fmt.Errorf("lint found %d issues", total)
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
