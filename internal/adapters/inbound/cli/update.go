package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/tae2089/trace"

	"github.com/tae2089/code-context-graph/internal/app/ingest"
	"github.com/tae2089/code-context-graph/internal/app/ingest/workflow"
	requestctx "github.com/tae2089/code-context-graph/internal/ctx"
)

// newUpdateCmd creates the incremental graph sync command.
// @intent 변경 파일만 해시 기반으로 수집해 증분 그래프 동기화를 수행한다.
// @requires deps.Syncer와 deps.Walkers가 초기화되어 있어야 한다.
// @sideEffect 파일 시스템을 읽고 증분 동기화 결과를 저장소에 반영한다.
func newUpdateCmd(deps *Deps) *cobra.Command {
	var excludePatterns []string
	var noRecursive bool
	var includePaths []string
	var fallbackCalls bool
	var maxFileBytes int64
	var maxTotalParsedBytes int64

	cmd := &cobra.Command{
		Use:   "update [directory]",
		Short: "Incrementally sync changed files into the code graph",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := "."
			if len(args) > 0 {
				dir = args[0]
			}

			patterns := resolveExcludes(excludePatterns)
			paths := resolveIncludePaths(includePaths)
			fileLimit := resolveMaxFileBytes(maxFileBytes)
			totalLimit := resolveMaxTotalParsedBytes(maxTotalParsedBytes)
			ctx := cmd.Context()
			ns := resolveNamespace(cmd)
			ctx = requestctx.WithNamespace(ctx, ns)

			parseCache, _ := deps.Store.(ingest.ParseCache)
			svc := &workflow.Service{
				Store:      deps.Store,
				UnitOfWork: deps.UnitOfWork,
				Search:     deps.Search,
				ParseCache: parseCache,
				Walkers:    deps.Walkers,
				Logger:     deps.Logger,
			}
			stats, err := svc.Update(ctx, workflow.UpdateOptions{
				BuildOptions: workflow.BuildOptions{
					Dir:                 dir,
					NoRecursive:         noRecursive,
					ExcludePatterns:     patterns,
					IncludePaths:        paths,
					MaxFileBytes:        fileLimit,
					MaxTotalParsedBytes: totalLimit,
					FallbackCalls:       fallbackCalls,
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

	cmd.Flags().BoolVar(&noRecursive, "no-recursive", false, "Only parse files in the top-level directory, skip subdirectories")
	cmd.Flags().StringArrayVar(&excludePatterns, "exclude", nil, "Exclude files/directories matching pattern (repeatable, e.g. --exclude vendor --exclude *.pb.go)")
	cmd.Flags().StringArrayVar(&includePaths, "path", nil, "Only include specific paths (repeatable, e.g. --path src/api --path src/auth)")
	cmd.Flags().Int64Var(&maxFileBytes, "max-file-bytes", 0, "Maximum bytes allowed per parsed source file (0 disables limit; config: parse.max_file_bytes)")
	cmd.Flags().Int64Var(&maxTotalParsedBytes, "max-total-parsed-bytes", 0, "Maximum total bytes allowed across parsed source files (0 disables limit; config: parse.max_total_parsed_bytes)")
	cmd.Flags().BoolVar(&fallbackCalls, "fallback-calls", false, "Fallback to deterministic call resolution when strict matching is ambiguous")

	return cmd
}
