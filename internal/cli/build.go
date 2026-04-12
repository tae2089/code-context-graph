package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/imtaebin/code-context-graph/internal/model"
	"github.com/imtaebin/code-context-graph/internal/parse"
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
				// Single-pass: parse nodes, edges, and comments together
				nodes, edges, tsComments, err := walker.ParseWithComments(relPath, content)
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

				// Bind annotations from comments extracted in the same parse
				if len(tsComments) > 0 {
					binderComments := make([]parse.CommentBlock, len(tsComments))
					for i, c := range tsComments {
						binderComments[i] = parse.CommentBlock{
							StartLine: c.StartLine,
							EndLine:   c.EndLine,
							Text:      c.Text,
						}
					}
					binder := parse.NewBinder()
					bindings := binder.Bind(binderComments, nodes, walker.Language())
					for _, b := range bindings {
						stored, err := deps.Store.GetNode(ctx, b.Node.QualifiedName)
						if err != nil || stored == nil {
							continue
						}
						b.Annotation.NodeID = stored.ID
						deps.Store.UpsertAnnotation(ctx, b.Annotation)
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

			// Build search index with annotation content (batch query)
			if deps.SearchBackend != nil && deps.DB != nil {
				var nodes []model.Node
				deps.DB.Where("kind IN ?", []string{"function", "class", "type", "test"}).Find(&nodes)

				// Batch load all annotations with tags in 2 queries instead of N+1
				nodeIDs := make([]uint, len(nodes))
				for i, n := range nodes {
					nodeIDs[i] = n.ID
				}
				var annotations []model.Annotation
				deps.DB.Where("node_id IN ?", nodeIDs).Preload("Tags").Find(&annotations)
				annByNode := make(map[uint]*model.Annotation, len(annotations))
				for i := range annotations {
					annByNode[annotations[i].NodeID] = &annotations[i]
				}

				// Batch delete + create search documents
				deps.DB.Where("1 = 1").Delete(&model.SearchDocument{})
				docs := make([]model.SearchDocument, 0, len(nodes))
				for _, n := range nodes {
					content := n.Name + " " + n.QualifiedName + " " + string(n.Kind)
					if ann := annByNode[n.ID]; ann != nil {
						if ann.Summary != "" {
							content += " " + ann.Summary
						}
						if ann.Context != "" {
							content += " " + ann.Context
						}
						for _, tag := range ann.Tags {
							content += " " + tag.Value
						}
					}
					docs = append(docs, model.SearchDocument{
						NodeID:   n.ID,
						Content:  content,
						Language: n.Language,
					})
				}
				if len(docs) > 0 {
					deps.DB.CreateInBatches(docs, 100)
				}
				if err := deps.SearchBackend.Rebuild(ctx, deps.DB); err != nil {
					deps.Logger.Warn("search index rebuild failed", "error", err)
				} else {
					deps.Logger.Info("search index rebuilt", "documents", len(docs))
				}
			}

			fmt.Fprintf(stdout(cmd), "Build complete: %d files, %d nodes, %d edges\n", totalFiles, totalNodes, totalEdges)
			deps.Logger.Info("build complete", "files", totalFiles, "nodes", totalNodes, "edges", totalEdges)

			return nil
		},
	}

	return cmd
}
