package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/imtaebin/code-context-graph/internal/ragindex"
)

func newRagIndexCmd(deps *Deps) *cobra.Command {
	var outDir string
	var indexDir string

	cmd := &cobra.Command{
		Use:   "rag-index",
		Short: "Build Vectorless RAG index from docs and community structure",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if deps.DB == nil {
				return fmt.Errorf("database not initialized")
			}

			b := &ragindex.Builder{
				DB:       deps.DB,
				OutDir:   resolveOutDir(outDir),
				IndexDir: indexDir,
			}

			communities, files, err := b.Build()
			if err != nil {
				return fmt.Errorf("build rag index: %w", err)
			}

			fmt.Fprintf(stdout(cmd), "Built doc-index: %d communities, %d files\n", communities, files)
			return nil
		},
	}

	cmd.Flags().StringVar(&outDir, "out", "docs", "Documentation directory")
	cmd.Flags().StringVar(&indexDir, "index-dir", ".ccg", "Directory for doc-index.json output")

	return cmd
}
