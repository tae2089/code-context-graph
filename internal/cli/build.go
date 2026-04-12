package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	chromem "github.com/philippgille/chromem-go"
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
	var embed bool
	var syncGraph bool

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

			// Batch load all nodes and annotations for indexing
			var indexNodes []model.Node
			annByNode := map[uint]*model.Annotation{}
			if deps.DB != nil {
				deps.DB.Where("kind IN ?", []string{"function", "class", "type", "test", "file"}).Find(&indexNodes)
				nodeIDs := make([]uint, len(indexNodes))
				for i, n := range indexNodes {
					nodeIDs[i] = n.ID
				}
				if len(nodeIDs) > 0 {
					var annotations []model.Annotation
					deps.DB.Where("node_id IN ?", nodeIDs).Preload("Tags").Find(&annotations)
					for i := range annotations {
						annByNode[annotations[i].NodeID] = &annotations[i]
					}
				}
			}

			// Helper: build content string for a node + annotation
			buildContent := func(n model.Node) string {
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
				return content
			}

			// Build FTS search index
			if deps.SearchBackend != nil && deps.DB != nil {
				deps.DB.Where("1 = 1").Delete(&model.SearchDocument{})
				docs := make([]model.SearchDocument, 0, len(indexNodes))
				for _, n := range indexNodes {
					docs = append(docs, model.SearchDocument{
						NodeID:   n.ID,
						Content:  buildContent(n),
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

			// Build vector embeddings if --embed flag is set
			if embed && deps.VectorDB != nil {
				deps.Logger.Info("building vector embeddings")
				for _, n := range indexNodes {
					err := deps.VectorDB.Collection.AddDocument(ctx, chromem.Document{
						ID:      fmt.Sprintf("%d", n.ID),
						Content: buildContent(n),
						Metadata: map[string]string{
							"qualified_name": n.QualifiedName,
							"kind":           string(n.Kind),
							"file_path":      n.FilePath,
							"language":       n.Language,
						},
					})
					if err != nil {
						deps.Logger.Warn("embed failed", "node", n.QualifiedName, "error", err)
					}
				}
				deps.Logger.Info("vector embeddings built", "documents", len(indexNodes))
			}

			// Sync to Apache AGE graph if --graph flag is set
			if syncGraph && deps.AgeStore != nil {
				deps.Logger.Info("syncing to AGE graph")
				if err := deps.AgeStore.ClearGraph(ctx); err != nil {
					deps.Logger.Warn("clear graph failed", "error", err)
				}
				if err := deps.AgeStore.SyncNodes(ctx, indexNodes); err != nil {
					deps.Logger.Warn("sync nodes to AGE failed", "error", err)
				} else {
					var edges []model.Edge
					deps.DB.Find(&edges)
					if err := deps.AgeStore.SyncEdges(ctx, edges); err != nil {
						deps.Logger.Warn("sync edges to AGE failed", "error", err)
					} else {
						deps.Logger.Info("AGE graph synced", "nodes", len(indexNodes), "edges", len(edges))
					}
				}
			}

			fmt.Fprintf(stdout(cmd), "Build complete: %d files, %d nodes, %d edges\n", totalFiles, totalNodes, totalEdges)
			deps.Logger.Info("build complete", "files", totalFiles, "nodes", totalNodes, "edges", totalEdges)

			return nil
		},
	}

	cmd.Flags().BoolVar(&embed, "embed", false, "Build vector embeddings for semantic search (requires OPENAI_API_KEY)")
	cmd.Flags().BoolVar(&syncGraph, "graph", false, "Sync graph to Apache AGE (requires AGE_DSN)")

	return cmd
}
