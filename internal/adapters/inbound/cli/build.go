package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/tae2089/trace"

	"github.com/tae2089/code-context-graph/internal/app/ingest/workflow"
	requestctx "github.com/tae2089/code-context-graph/internal/ctx"
)

// newBuildCmd creates the full graph build command.
// @intent 소스 트리를 전체 재파싱해 그래프와 검색 인덱스를 다시 만드는 CLI 명령을 노출한다.
// @sideEffect 파일 시스템을 읽고 workflow.Service를 통해 그래프 저장소를 갱신한다.
// @see workflow.Service.Build
func newBuildCmd(deps *Deps) *cobra.Command {
	var excludePatterns []string
	var noRecursive bool
	var includePaths []string
	var fallbackCalls bool
	var maxFileBytes int64
	var maxTotalParsedBytes int64

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
			paths := resolveIncludePaths(includePaths)
			fileLimit := resolveMaxFileBytes(maxFileBytes)
			totalLimit := resolveMaxTotalParsedBytes(maxTotalParsedBytes)
			svc := &workflow.Service{
				Store:      deps.Store,
				UnitOfWork: deps.UnitOfWork,
				Search:     deps.Search,
				Walkers:    deps.Walkers,
				Logger:     deps.Logger,
			}

			opts := workflow.BuildOptions{
				Dir:                 dir,
				NoRecursive:         noRecursive,
				ExcludePatterns:     patterns,
				IncludePaths:        paths,
				MaxFileBytes:        fileLimit,
				MaxTotalParsedBytes: totalLimit,
				FallbackCalls:       fallbackCalls,
			}

			ctx := context.Background()
			ns, _ := cmd.Flags().GetString("namespace")
			ctx = requestctx.WithNamespace(ctx, ns)
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
	cmd.Flags().StringArrayVar(&includePaths, "path", nil, "Only include specific paths (repeatable, e.g. --path src/api --path src/auth)")
	cmd.Flags().Int64Var(&maxFileBytes, "max-file-bytes", 0, "Maximum bytes allowed per parsed source file (0 disables limit; config: parse.max_file_bytes)")
	cmd.Flags().Int64Var(&maxTotalParsedBytes, "max-total-parsed-bytes", 0, "Maximum total bytes allowed across parsed source files (0 disables limit; config: parse.max_total_parsed_bytes)")
	cmd.Flags().BoolVar(&fallbackCalls, "fallback-calls", false, "Fallback to deterministic call resolution when strict matching is ambiguous")

	return cmd
}
