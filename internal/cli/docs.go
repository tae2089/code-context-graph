package cli

import (
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/tae2089/trace"

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

			if err := gen.Run(); err != nil {
				return trace.Wrap(err, "generate docs")
			}

			fmt.Fprintf(stdout(cmd), "Docs written to %s\n", absOut)
			return nil
		},
	}

	cmd.Flags().StringVar(&outDir, "out", "docs", "Output directory for generated documentation")
	cmd.Flags().StringArrayVar(&excludePatterns, "exclude", nil, "Exclude files/paths matching pattern (repeatable, e.g. --exclude vendor --exclude '*.pb.go')")
	return cmd
}
