package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tae2089/trace"
	"gorm.io/gorm"

	"github.com/tae2089/code-context-graph/internal/ctxns"
	"github.com/tae2089/code-context-graph/internal/model"
	postprocesspolicy "github.com/tae2089/code-context-graph/internal/postprocess/policy"
)

// kindCount holds grouped count rows for graph statistics queries.
// @intent 노드/엣지 kind별 집계 결과를 GORM 스캔 대상으로 담는다.
type kindCount struct {
	Kind  string
	Count int64
}

// newStatusCmd creates the graph status command.
// @intent 저장된 코드 그래프의 전체 규모와 kind 분포를 확인할 수 있게 한다.
// @requires deps.DB가 초기화되어 있어야 한다.
// @sideEffect 데이터베이스를 조회하고 통계를 출력한다.
func newStatusCmd(deps *Deps) *cobra.Command {
	var showErrors bool
	var recentLimit int

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show graph statistics",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if deps.DB == nil {
				return errDBNotInitialized
			}
			if recentLimit <= 0 {
				return fmt.Errorf("recent must be > 0, got %d", recentLimit)
			}

			ns, _ := cmd.Flags().GetString("namespace")
			ctx := ctxns.WithNamespace(cmd.Context(), ns)
			nodeQuery := deps.DB.WithContext(ctx).Model(&model.Node{}).Where("namespace = ?", ctxns.FromContext(ctx))
			edgeQuery := deps.DB.WithContext(ctx).Model(&model.Edge{}).Where("namespace = ?", ctxns.FromContext(ctx))

			var nodeCount, edgeCount int64
			if err := nodeQuery.Count(&nodeCount).Error; err != nil {
				return trace.Wrap(err, "count nodes")
			}
			if err := edgeQuery.Count(&edgeCount).Error; err != nil {
				return trace.Wrap(err, "count edges")
			}

			var fileCount int64
			nodeQuery.Distinct("file_path").Count(&fileCount)

			out := stdout(cmd)

			if nodeCount == 0 && edgeCount == 0 {
				fmt.Fprintln(out, "No data")
			} else {
				fmt.Fprintf(out, "Nodes: %d\n", nodeCount)
				fmt.Fprintf(out, "Edges: %d\n", edgeCount)
				fmt.Fprintf(out, "Files: %d\n", fileCount)

				var nodeKinds []kindCount
				nodeQuery.Select("kind, count(*) as count").Group("kind").Scan(&nodeKinds)
				if len(nodeKinds) > 0 {
					fmt.Fprintln(out, "\nNode kinds:")
					for _, k := range nodeKinds {
						fmt.Fprintf(out, "  %s: %d\n", k.Kind, k.Count)
					}
				}

				var edgeKinds []kindCount
				edgeQuery.Select("kind, count(*) as count").Group("kind").Scan(&edgeKinds)
				if len(edgeKinds) > 0 {
					fmt.Fprintln(out, "\nEdge kinds:")
					for _, k := range edgeKinds {
						fmt.Fprintf(out, "  %s: %d\n", k.Kind, k.Count)
					}
				}
			}

			if err := printPostprocessStatus(ctx, out, deps.DB, ctxns.FromContext(ctx), recentLimit, showErrors); err != nil {
				return trace.Wrap(err, "postprocess status")
			}

			return nil
		},
	}

	cmd.Flags().BoolVar(&showErrors, "errors", false, "Show recent postprocess failure details")
	cmd.Flags().IntVar(&recentLimit, "recent", postprocesspolicy.DefaultStatusLimit, "Number of recent postprocess failures to inspect")

	return cmd
}

// printPostprocessStatus adds persisted postprocess policy health to graph stats.
// @intent surface degraded derived-state failures from the same CLI status command operators already use.
func printPostprocessStatus(ctx context.Context, out io.Writer, db *gorm.DB, namespace string, recentLimit int, showErrors bool) error {
	summary, err := postprocesspolicy.NewStore(db).Status(ctx, postprocesspolicy.StatusOptions{
		Namespace:   namespace,
		RecentLimit: recentLimit,
	})
	if err != nil {
		return err
	}

	fmt.Fprintln(out, "\nPostprocess:")
	fmt.Fprintf(out, "  Status: %s\n", summary.Status)
	if !showErrors {
		fmt.Fprintf(out, "  Fail-closed: %d\n", len(summary.FailClosed))
		fmt.Fprintf(out, "  Recent failures: %d\n", len(summary.RecentFailures))
		return nil
	}

	if len(summary.FailClosed) > 0 {
		fmt.Fprintln(out, "\nFail-closed:")
		for _, state := range summary.FailClosed {
			fmt.Fprintf(out, "  %s  consecutive_failures=%d  updated_at=%s\n",
				state.Tool, state.ConsecutiveFailures, state.UpdatedAt.UTC().Format(timeFormatRFC3339))
		}
	}

	if len(summary.RecentFailures) > 0 {
		fmt.Fprintln(out, "\nRecent failures:")
		for _, run := range summary.RecentFailures {
			fmt.Fprintf(out, "  %s  policy=%s  failed_steps=%s  skipped_steps=%s  created_at=%s\n",
				run.Tool, run.Policy, formatStatusList(run.FailedSteps), formatStatusList(run.SkippedSteps), run.CreatedAt.UTC().Format(timeFormatRFC3339))
		}
	}

	return nil
}

const timeFormatRFC3339 = "2006-01-02T15:04:05Z07:00"

// @intent 최근 실패 step 목록을 status 출력에 compact한 한 줄 문자열로 직렬화한다.
func formatStatusList(values []string) string {
	if len(values) == 0 {
		return "[]"
	}
	return strings.Join(values, ",")
}
