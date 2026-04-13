package cli

import (
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/imtaebin/code-context-graph/internal/docs"
)

func newDocsCmd(deps *Deps) *cobra.Command {
	var outDir string
	var excludePatterns []string

	cmd := &cobra.Command{
		Use:   "docs",
		Short: "Generate markdown documentation from the code graph",
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

			if err := gen.Run(); err != nil {
				return fmt.Errorf("generate docs: %w", err)
			}

			fmt.Fprintf(stdout(cmd), "Docs written to %s\n", absOut)
			return nil
		},
	}

	cmd.Flags().StringVar(&outDir, "out", "docs", "Output directory for generated documentation")
	cmd.Flags().StringArrayVar(&excludePatterns, "exclude", nil, "Exclude files/paths matching pattern (repeatable, e.g. --exclude vendor --exclude '*.pb.go')")
	return cmd
}
