package cli

import (
	"fmt"
	"math"

	"github.com/spf13/cobra"
	"github.com/tae2089/trace"

	"github.com/tae2089/code-context-graph/internal/ctxns"
	"github.com/tae2089/code-context-graph/internal/model"
)

// kindCount holds grouped count rows for graph statistics queries.
// @intent 노드/엣지 kind별 집계 결과를 GORM 스캔 대상으로 담는다.
type kindCount struct {
	Kind  string
	Count int64
}

const (
	callEdgeGoodThreshold  = 0.02
	callEdgeWatchThreshold = 0.10
)

// newStatusCmd creates the graph status command.
// @intent 저장된 코드 그래프의 전체 규모와 kind 분포를 확인할 수 있게 한다.
// @requires deps.DB가 초기화되어 있어야 한다.
// @sideEffect 데이터베이스를 조회하고 통계를 출력한다.
func newStatusCmd(deps *Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show graph statistics",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if deps.DB == nil {
				return errDBNotInitialized
			}

			ns, _ := cmd.Flags().GetString("namespace")
			ctx := ctxns.WithNamespace(cmd.Context(), ns)
			nodeQuery := deps.DB.WithContext(ctx).Model(&model.Node{}).Where("namespace = ?", ctxns.FromContext(ctx))
			namespace := ctxns.FromContext(ctx)
			edgeQuery := deps.DB.WithContext(ctx).Model(&model.Edge{}).Where("namespace = ?", namespace)

			var nodeCount, edgeCount int64
			if err := nodeQuery.Count(&nodeCount).Error; err != nil {
				return trace.Wrap(err, "count nodes")
			}
			if err := edgeQuery.Count(&edgeCount).Error; err != nil {
				return trace.Wrap(err, "count edges")
			}

			var fileCount int64
			if err := nodeQuery.Distinct("file_path").Count(&fileCount).Error; err != nil {
				return trace.Wrap(err, "count files")
			}

			out := stdout(cmd)

			if nodeCount == 0 && edgeCount == 0 {
				fmt.Fprintln(out, "No data")
			} else {
				fmt.Fprintf(out, "Nodes: %d\n", nodeCount)
				fmt.Fprintf(out, "Edges: %d\n", edgeCount)
				fmt.Fprintf(out, "Files: %d\n", fileCount)

				var nodeKinds []kindCount
				if err := nodeQuery.Select("kind, count(*) as count").Group("kind").Scan(&nodeKinds).Error; err != nil {
					return trace.Wrap(err, "group nodes by kind")
				}
				if len(nodeKinds) > 0 {
					fmt.Fprintln(out, "\nNode kinds:")
					for _, k := range nodeKinds {
						fmt.Fprintf(out, "  %s: %d\n", k.Kind, k.Count)
					}
				}

				var edgeKinds []kindCount
				if err := edgeQuery.Select("kind, count(*) as count").Group("kind").Scan(&edgeKinds).Error; err != nil {
					return trace.Wrap(err, "group edges by kind")
				}
				if len(edgeKinds) > 0 {
					fmt.Fprintln(out, "\nEdge kinds:")
					for _, k := range edgeKinds {
						fmt.Fprintf(out, "  %s: %d\n", k.Kind, k.Count)
					}
				}

				var strictCalls int64
				var fallbackCalls int64
				if err := deps.DB.WithContext(ctx).Model(&model.Edge{}).
					Where("namespace = ?", namespace).
					Where("kind = ?", model.EdgeKindCalls).
					Count(&strictCalls).Error; err != nil {
					return trace.Wrap(err, "count strict call edges")
				}
				if err := deps.DB.WithContext(ctx).Model(&model.Edge{}).
					Where("namespace = ?", namespace).
					Where("kind = ?", model.EdgeKindFallbackCalls).
					Count(&fallbackCalls).Error; err != nil {
					return trace.Wrap(err, "count fallback call edges")
				}

				callEdgeTotal := strictCalls + fallbackCalls
				fallbackRatio := callFallbackRatio(strictCalls, fallbackCalls)
				fmt.Fprintln(out, "\nFallback call analysis:")
				fmt.Fprintf(out, "  calls: %d\n", strictCalls)
				fmt.Fprintf(out, "  fallback_calls: %d\n", fallbackCalls)
				fmt.Fprintf(out, "  fallback_ratio: %.2f%%\n", fallbackRatio*100)
				switch callFallbackWarning(fallbackRatio, callEdgeTotal == 0) {
				case "warn":
					fmt.Fprintln(out, "  Warning: fallback call ratio is elevated")
					fmt.Fprintln(out, "  Review fallback edge quality before trusting high-confidence analysis")
				case "watch":
					fmt.Fprintln(out, "  Warning: fallback call ratio is elevated")
				}
			}

			return nil
		},
	}

	return cmd
}

// @intent compute the share of fallback call edges within all call-like edges for operator-facing health reporting.
func callFallbackRatio(strictCalls, fallbackCalls int64) float64 {
	total := strictCalls + fallbackCalls
	if total == 0 {
		return 0
	}
	return float64(fallbackCalls) / float64(total)
}

// @intent convert fallback edge ratio thresholds into compact operator warning levels for ccg status output.
func callFallbackWarning(ratio float64, noCalls bool) string {
	if noCalls || math.Abs(ratio) < 1e-12 {
		return "ok"
	}
	if ratio <= callEdgeGoodThreshold {
		return "ok"
	}
	if ratio <= callEdgeWatchThreshold {
		return "watch"
	}
	return "warn"
}
