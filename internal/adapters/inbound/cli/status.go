package cli

import (
	"fmt"
	"math"

	"github.com/spf13/cobra"
	requestctx "github.com/tae2089/code-context-graph/internal/ctx"
)

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
			if deps.Statistics == nil {
				return errDBNotInitialized
			}

			ns, _ := cmd.Flags().GetString("namespace")
			ctx := requestctx.WithNamespace(cmd.Context(), ns)
			stats, err := deps.Statistics.GraphStatistics(ctx)
			if err != nil {
				return err
			}

			out := stdout(cmd)

			if stats.NodeCount == 0 && stats.EdgeCount == 0 {
				fmt.Fprintln(out, "No data")
			} else {
				fmt.Fprintf(out, "Nodes: %d\n", stats.NodeCount)
				fmt.Fprintf(out, "Edges: %d\n", stats.EdgeCount)
				fmt.Fprintf(out, "Files: %d\n", stats.FileCount)

				if len(stats.NodeKinds) > 0 {
					fmt.Fprintln(out, "\nNode kinds:")
					for _, k := range stats.NodeKinds {
						fmt.Fprintf(out, "  %s: %d\n", k.Kind, k.Count)
					}
				}

				if len(stats.EdgeKinds) > 0 {
					fmt.Fprintln(out, "\nEdge kinds:")
					for _, k := range stats.EdgeKinds {
						fmt.Fprintf(out, "  %s: %d\n", k.Kind, k.Count)
					}
				}

				callEdgeTotal := stats.StrictCalls + stats.FallbackCalls
				fallbackRatio := callFallbackRatio(stats.StrictCalls, stats.FallbackCalls)
				fmt.Fprintln(out, "\nFallback call analysis:")
				fmt.Fprintf(out, "  calls: %d\n", stats.StrictCalls)
				fmt.Fprintf(out, "  fallback_calls: %d\n", stats.FallbackCalls)
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
