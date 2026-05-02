package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/tae2089/trace"

	"github.com/tae2089/code-context-graph/internal/ctxns"
	"github.com/tae2089/code-context-graph/internal/service"
)

// newUpdateCmd creates the incremental graph sync command.
// @intent 변경 파일만 해시 기반으로 수집해 증분 그래프 동기화를 수행한다.
// @requires deps.Syncer와 deps.Walkers가 초기화되어 있어야 한다.
// @sideEffect 파일 시스템을 읽고 증분 동기화 결과를 저장소에 반영한다.
func newUpdateCmd(deps *Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "update [directory]",
		Short: "Incrementally sync changed files into the code graph",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := "."
			if len(args) > 0 {
				dir = args[0]
			}

			ctx := cmd.Context()
			ns, _ := cmd.Flags().GetString("namespace")
			ctx = ctxns.WithNamespace(ctx, ns)

			svc := &service.GraphService{
				Store:         deps.Store,
				DB:            deps.DB,
				SearchBackend: deps.SearchBackend,
				Walkers:       deps.Walkers,
				Logger:        deps.Logger,
			}
			stats, err := svc.Update(ctx, service.UpdateOptions{
				BuildOptions: service.BuildOptions{
					Dir: dir,
				},
				Syncer:  deps.Syncer,
				Replace: true,
			})
			if err != nil {
				return trace.Wrap(err, "incremental sync")
			}

			fmt.Fprintf(stdout(cmd), "Update complete: added=%d modified=%d skipped=%d deleted=%d\n",
				stats.Added, stats.Modified, stats.Skipped, stats.Deleted)

			return nil
		},
	}

	return cmd
}
