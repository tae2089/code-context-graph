package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
)

func newSearchCmd(deps *Deps) *cobra.Command {
	var limit int

	cmd := &cobra.Command{
		Use:   "search <query>",
		Short: "Full-text search for code nodes",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			query := args[0]
			ctx := context.Background()

			nodes, err := deps.SearchBackend.Query(ctx, deps.DB, query, limit)
			if err != nil {
				return fmt.Errorf("search: %w", err)
			}

			out := stdout(cmd)

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

	return cmd
}
