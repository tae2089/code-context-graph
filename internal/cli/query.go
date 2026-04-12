package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
)

func newQueryCmd(deps *Deps) *cobra.Command {
	var columns int

	cmd := &cobra.Command{
		Use:   "query <cypher>",
		Short: "Execute a Cypher query on the Apache AGE graph",
		Long: `Execute a Cypher query directly against the Apache AGE graph database.

Examples:
  ccg query "MATCH (n:Function) RETURN n LIMIT 5"
  ccg query "MATCH (a)-[:CALLS]->(b) WHERE a.name = 'Login' RETURN a, b" --columns 2
  ccg query "MATCH path = (a {name: 'Login'})-[:CALLS*1..3]->(b) RETURN path"
  ccg query "MATCH (n:Function) WHERE NOT ()-[:CALLS]->(n) RETURN n"`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if deps.AgeStore == nil {
				return fmt.Errorf("AGE not configured — set AGE_DSN env or use --age-dsn flag")
			}
			ctx := context.Background()
			results, err := deps.AgeStore.ExecuteCypher(ctx, args[0], columns)
			if err != nil {
				return err
			}
			out := stdout(cmd)
			if len(results) == 0 {
				fmt.Fprintln(out, "No results")
				return nil
			}
			for _, row := range results {
				fmt.Fprintln(out, joinRow(row))
			}
			fmt.Fprintf(out, "\n(%d rows)\n", len(results))
			return nil
		},
	}

	cmd.Flags().IntVar(&columns, "columns", 1, "Number of RETURN columns")

	return cmd
}

func joinRow(row []string) string {
	if len(row) == 1 {
		return row[0]
	}
	result := ""
	for i, v := range row {
		if i > 0 {
			result += "\t"
		}
		result += v
	}
	return result
}
