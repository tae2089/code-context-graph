package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/imtaebin/code-context-graph/internal/model"
)

var skipDirs = map[string]bool{
	".git":         true,
	"vendor":       true,
	"node_modules": true,
}

func newBuildCmd(deps *Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "build [directory]",
		Short: "Parse a directory and build the code graph",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := "."
			if len(args) > 0 {
				dir = args[0]
			}

			absDir, err := filepath.Abs(dir)
			if err != nil {
				return fmt.Errorf("resolve path: %w", err)
			}

			deps.Logger.Info("building graph", "dir", absDir)

			var totalFiles, totalNodes, totalEdges int
			ctx := context.Background()

			err = filepath.Walk(absDir, func(path string, info os.FileInfo, err error) error {
				if err != nil {
					return err
				}

				if info.IsDir() {
					if skipDirs[info.Name()] {
						return filepath.SkipDir
					}
					return nil
				}

				ext := strings.ToLower(filepath.Ext(path))
				walker, ok := deps.Walkers[ext]
				if !ok {
					return nil
				}

				content, err := os.ReadFile(path)
				if err != nil {
					deps.Logger.Warn("skip unreadable file", "path", path, "error", err)
					return nil
				}

				relPath, _ := filepath.Rel(absDir, path)
				nodes, edges, err := walker.Parse(relPath, content)
				if err != nil {
					deps.Logger.Warn("parse failed", "path", relPath, "error", err)
					return nil
				}

				if len(nodes) > 0 {
					if err := deps.Store.UpsertNodes(ctx, nodes); err != nil {
						return fmt.Errorf("upsert nodes for %s: %w", relPath, err)
					}
				}
				if len(edges) > 0 {
					if err := deps.Store.UpsertEdges(ctx, edges); err != nil {
						return fmt.Errorf("upsert edges for %s: %w", relPath, err)
					}
				}

				totalFiles++
				totalNodes += len(nodes)
				totalEdges += len(edges)

				return nil
			})
			if err != nil {
				return fmt.Errorf("walk directory: %w", err)
			}

			// Build search index
			if deps.SearchBackend != nil && deps.DB != nil {
				var nodes []model.Node
				deps.DB.Where("kind IN ?", []string{"function", "class", "type", "test"}).Find(&nodes)
				for _, n := range nodes {
					deps.DB.Where("node_id = ?", n.ID).Delete(&model.SearchDocument{})
					deps.DB.Create(&model.SearchDocument{
						NodeID:   n.ID,
						Content:  n.Name + " " + n.QualifiedName + " " + string(n.Kind),
						Language: n.Language,
					})
				}
				if err := deps.SearchBackend.Rebuild(ctx, deps.DB); err != nil {
					deps.Logger.Warn("search index rebuild failed", "error", err)
				} else {
					deps.Logger.Info("search index rebuilt", "documents", len(nodes))
				}
			}

			fmt.Fprintf(stdout(cmd), "Build complete: %d files, %d nodes, %d edges\n", totalFiles, totalNodes, totalEdges)
			deps.Logger.Info("build complete", "files", totalFiles, "nodes", totalNodes, "edges", totalEdges)

			return nil
		},
	}

	return cmd
}
