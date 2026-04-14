package cli

import (
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/tae2089/trace"

	"github.com/imtaebin/code-context-graph/internal/docs"
)

// newIndexCmd creates the index-only docs regeneration command.
// @intent 개별 문서는 건드리지 않고 index.md만 다시 만들어 빠른 재색인을 지원한다.
// @requires deps.DB가 초기화되어 있어야 한다.
// @sideEffect docs 디렉터리의 index.md를 다시 기록한다.
func newIndexCmd(deps *Deps) *cobra.Command {
	var outDir string
	var excludePatterns []string

	cmd := &cobra.Command{
		Use:   "index",
		Short: "Regenerate index.md from the code graph (without rewriting per-file docs)",
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

			if err := gen.RunIndex(); err != nil {
				return trace.Wrap(err, "generate index")
			}

			fmt.Fprintf(stdout(cmd), "Index written to %s\n", absOut)
			return nil
		},
	}

	cmd.Flags().StringVar(&outDir, "out", "docs", "Output directory for index.md")
	cmd.Flags().StringArrayVar(&excludePatterns, "exclude", nil, "Exclude files/paths matching pattern (repeatable)")
	return cmd
}
