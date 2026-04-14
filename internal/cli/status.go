package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/tae2089/trace"

	"github.com/imtaebin/code-context-graph/internal/model"
)

type kindCount struct {
	Kind  string
	Count int64
}

func newStatusCmd(deps *Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show graph statistics",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			var nodeCount, edgeCount int64
			if err := deps.DB.Model(&model.Node{}).Count(&nodeCount).Error; err != nil {
				return trace.Wrap(err, "count nodes")
			}
			if err := deps.DB.Model(&model.Edge{}).Count(&edgeCount).Error; err != nil {
				return trace.Wrap(err, "count edges")
			}

			var fileCount int64
			deps.DB.Model(&model.Node{}).Distinct("file_path").Count(&fileCount)

			out := stdout(cmd)

			if nodeCount == 0 && edgeCount == 0 {
				fmt.Fprintln(out, "No data")
				return nil
			}

			fmt.Fprintf(out, "Nodes: %d\n", nodeCount)
			fmt.Fprintf(out, "Edges: %d\n", edgeCount)
			fmt.Fprintf(out, "Files: %d\n", fileCount)

			var nodeKinds []kindCount
			deps.DB.Model(&model.Node{}).Select("kind, count(*) as count").Group("kind").Scan(&nodeKinds)
			if len(nodeKinds) > 0 {
				fmt.Fprintln(out, "\nNode kinds:")
				for _, k := range nodeKinds {
					fmt.Fprintf(out, "  %s: %d\n", k.Kind, k.Count)
				}
			}

			var edgeKinds []kindCount
			deps.DB.Model(&model.Edge{}).Select("kind, count(*) as count").Group("kind").Scan(&edgeKinds)
			if len(edgeKinds) > 0 {
				fmt.Fprintln(out, "\nEdge kinds:")
				for _, k := range edgeKinds {
					fmt.Fprintf(out, "  %s: %d\n", k.Kind, k.Count)
				}
			}

			return nil
		},
	}

	return cmd
}
