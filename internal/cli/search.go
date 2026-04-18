package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tae2089/trace"

	"github.com/imtaebin/code-context-graph/internal/ctxns"
)

// newSearchCmd creates the full-text search command.
// @intent 그래프 검색 결과를 빠르게 조회하고 필요 시 경로 접두사로 후처리 필터링한다.
// @requires deps.SearchBackend와 deps.DB가 초기화되어 있어야 한다.
// @sideEffect 검색 결과를 표준 출력으로 기록한다.
func newSearchCmd(deps *Deps) *cobra.Command {
	var limit int
	var pathPrefix string

	cmd := &cobra.Command{
		Use:   "search <query>",
		Short: "Full-text search for code nodes",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			query := args[0]
			ctx := context.Background()
			if ns, _ := cmd.Flags().GetString("namespace"); ns != "" {
				ctx = ctxns.WithNamespace(ctx, ns)
			}

			fetchLimit := limit
			if pathPrefix != "" {
				fetchLimit = max(limit*5, 50)
			}

			nodes, err := deps.SearchBackend.Query(ctx, deps.DB, query, fetchLimit)
			if err != nil {
				return trace.Wrap(err, "search")
			}

			out := stdout(cmd)

			if pathPrefix != "" {
				filtered := nodes[:0]
				for _, n := range nodes {
					if strings.HasPrefix(n.FilePath, pathPrefix) {
						filtered = append(filtered, n)
					}
				}
				nodes = filtered
				if len(nodes) > limit {
					nodes = nodes[:limit]
				}
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
	cmd.Flags().StringVar(&pathPrefix, "path", "", "Filter results to file paths starting with this prefix (e.g. internal/auth)")

	return cmd
}
