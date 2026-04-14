package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/tae2089/trace"

	"github.com/imtaebin/code-context-graph/internal/service"
)

// newBuildCmd creates the full graph build command.
// @intent 소스 트리를 전체 재파싱해 그래프와 검색 인덱스를 다시 만드는 CLI 명령을 노출한다.
// @sideEffect 파일 시스템을 읽고 service.GraphService를 통해 그래프 저장소를 갱신한다.
// @see service.GraphService.Build
func newBuildCmd(deps *Deps) *cobra.Command {
	var excludePatterns []string
	var noRecursive bool

	cmd := &cobra.Command{
		Use:   "build [directory]",
		Short: "Parse a directory and build the code graph",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := "."
			if len(args) > 0 {
				dir = args[0]
			}

			patterns := resolveExcludes(excludePatterns)

			svc := &service.GraphService{
				Store:         deps.Store,
				DB:            deps.DB,
				SearchBackend: deps.SearchBackend,
				Walkers:       deps.Walkers,
				Logger:        deps.Logger,
			}

			opts := service.BuildOptions{
				Dir:             dir,
				NoRecursive:     noRecursive,
				ExcludePatterns: patterns,
			}

			ctx := context.Background()
			stats, err := svc.Build(ctx, opts)
			if err != nil {
				return trace.Wrap(err, "build project")
			}

			fmt.Fprintf(stdout(cmd), "Build complete: %d files, %d nodes, %d edges\n", stats.TotalFiles, stats.TotalNodes, stats.TotalEdges)

			return nil
		},
	}

	cmd.Flags().BoolVar(&noRecursive, "no-recursive", false, "Only parse files in the top-level directory, skip subdirectories")
	cmd.Flags().StringArrayVar(&excludePatterns, "exclude", nil, "Exclude files/directories matching pattern (repeatable, e.g. --exclude vendor --exclude *.pb.go)")

	return cmd
}
