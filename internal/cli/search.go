package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
)

func newSearchCmd(deps *Deps) *cobra.Command {
	var limit int
	var semantic bool

	cmd := &cobra.Command{
		Use:   "search <query>",
		Short: "Full-text or semantic search for code nodes",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			query := args[0]
			ctx := context.Background()
			out := stdout(cmd)

			// Semantic search via vector embeddings
			if semantic {
				if deps.VectorDB == nil || deps.VectorDB.Collection == nil {
					return fmt.Errorf("vector embeddings not available — run 'ccg build --embed' first")
				}
				results, err := deps.VectorDB.Collection.Query(ctx, query, limit, nil, nil)
				if err != nil {
					return fmt.Errorf("semantic search: %w", err)
				}
				if len(results) == 0 {
					fmt.Fprintln(out, "No results")
					return nil
				}
				for _, r := range results {
					qn := r.Metadata["qualified_name"]
					kind := r.Metadata["kind"]
					fp := r.Metadata["file_path"]
					fmt.Fprintf(out, "%s\t%s\t%s\t(score: %.3f)\n", qn, kind, fp, r.Similarity)
				}
				return nil
			}

			// FTS keyword search
			nodes, err := deps.SearchBackend.Query(ctx, deps.DB, query, limit)
			if err != nil {
				return fmt.Errorf("search: %w", err)
			}

			if len(nodes) == 0 {
				fmt.Fprintln(out, "No results")
				return nil
			}

			for _, n := range nodes {
				fmt.Fprintf(out, "%s\t%s\t%s:%d\n", n.QualifiedName, n.Kind, n.FilePath, n.StartLine)
			}

			return nil
		},
	}

	cmd.Flags().IntVar(&limit, "limit", 10, "Maximum number of results")
	cmd.Flags().BoolVar(&semantic, "semantic", false, "Use semantic search via vector embeddings")

	return cmd
}
