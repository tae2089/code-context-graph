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
			total := len(report.Orphans) + len(report.Missing) + len(report.Stale) + len(report.Unannotated)

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

			if total == 0 {
				fmt.Fprintln(out, "All docs are clean — 0 issues found.")
			} else {
				fmt.Fprintf(out, "Summary: %d orphan, %d missing, %d stale, %d unannotated\n",
					len(report.Orphans), len(report.Missing), len(report.Stale), len(report.Unannotated))
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&outDir, "out", "docs", "Documentation directory to lint")
	cmd.Flags().StringArrayVar(&excludePatterns, "exclude", nil, "Exclude files/paths matching pattern (repeatable)")
	return cmd
}
