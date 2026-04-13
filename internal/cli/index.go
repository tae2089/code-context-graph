package cli

import (
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/imtaebin/code-context-graph/internal/docs"
)

func newIndexCmd(deps *Deps) *cobra.Command {
	var outDir string
	var excludePatterns []string

	cmd := &cobra.Command{
		Use:   "index",
		Short: "Regenerate index.md from the code graph (without rewriting per-file docs)",
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

			if err := gen.RunIndex(); err != nil {
				return fmt.Errorf("generate index: %w", err)
			}

			fmt.Fprintf(stdout(cmd), "Index written to %s\n", absOut)
			return nil
		},
	}

	cmd.Flags().StringVar(&outDir, "out", "docs", "Output directory for index.md")
	cmd.Flags().StringArrayVar(&excludePatterns, "exclude", nil, "Exclude files/paths matching pattern (repeatable)")
	return cmd
}
