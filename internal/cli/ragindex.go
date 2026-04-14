package cli

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/imtaebin/code-context-graph/internal/ragindex"
)

func newRagIndexCmd(deps *Deps) *cobra.Command {
	var outDir string
	var indexDir string
	var projectDesc string

	cmd := &cobra.Command{
		Use:   "rag-index",
		Short: "Build Vectorless RAG index from docs and community structure",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if deps.DB == nil {
				return fmt.Errorf("database not initialized")
			}

			b := &ragindex.Builder{
				DB:          deps.DB,
				OutDir:      resolveOutDir(outDir),
				IndexDir:    indexDir,
				ProjectDesc: projectDesc,
			}

			communities, files, err := b.Build()
			if err != nil {
				return fmt.Errorf("build rag index: %w", err)
			}

			fmt.Fprintf(stdout(cmd), "Built doc-index: %d communities, %d files\n", communities, files)

			// Warn if docs directory has fewer .md files than indexed
			if files > 0 {
				effectiveOut := resolveOutDir(outDir)
				mdCount := countMDFiles(effectiveOut)
				if mdCount == 0 {
					fmt.Fprintf(stdout(cmd), "Warning: no .md files found in %q. Run 'ccg docs' to generate documentation.\n", effectiveOut)
				} else if mdCount < files {
					fmt.Fprintf(stdout(cmd), "Warning: %d files indexed but only %d .md files found in %q. Run 'ccg docs' to update documentation.\n", files, mdCount, effectiveOut)
				}
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&outDir, "out", "docs", "Documentation directory")
	cmd.Flags().StringVar(&indexDir, "index-dir", ".ccg", "Directory for doc-index.json output")
	cmd.Flags().StringVar(&projectDesc, "desc", "", "Project description for root node summary")

	return cmd
}

func countMDFiles(dir string) int {
	count := 0
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if strings.HasSuffix(path, ".md") && filepath.Base(path) != "index.md" {
			count++
		}
		return nil
	})
	return count
}
