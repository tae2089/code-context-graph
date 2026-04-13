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
	"github.com/imtaebin/code-context-graph/internal/pathutil"
	"github.com/imtaebin/code-context-graph/internal/store/pgstore"
)

var skipDirs = map[string]bool{
	".git":         true,
	"vendor":       true,
	"node_modules": true,
}

func newBuildCmd(deps *Deps) *cobra.Command {
	var syncGraph bool
	var excludePatterns []string
	var noRecursive bool

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

			patterns := resolveExcludes(excludePatterns)

			err = filepath.Walk(absDir, func(path string, info os.FileInfo, err error) error {
				if err != nil {
					return err
				}

				relPath, _ := filepath.Rel(absDir, path)

				if info.IsDir() {
					if path != absDir && noRecursive {
						return filepath.SkipDir
					}
					if skipDirs[info.Name()] || pathutil.MatchExcludes(patterns, relPath) {
						return filepath.SkipDir
					}
					return nil
				}

				if pathutil.MatchExcludes(patterns, relPath) {
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

			// Sync to Apache AGE graph + pgvector if --graph flag is set
			if syncGraph && deps.PGStore != nil {
				deps.Logger.Info("syncing to AGE graph")
				if err := deps.PGStore.ClearGraph(ctx); err != nil {
					deps.Logger.Warn("clear graph failed", "error", err)
				}
				if err := deps.PGStore.SyncNodes(ctx, indexNodes); err != nil {
					deps.Logger.Warn("sync nodes to AGE failed", "error", err)
				} else {
					var edges []model.Edge
					deps.DB.Find(&edges)
					if err := deps.PGStore.SyncEdges(ctx, edges); err != nil {
						deps.Logger.Warn("sync edges to AGE failed", "error", err)
					} else {
						deps.Logger.Info("AGE graph synced", "nodes", len(indexNodes), "edges", len(edges))
					}
				}

				// Sync pgvector documents for semantic search
				if err := deps.PGStore.InitPGVector(ctx); err != nil {
					deps.Logger.Warn("pgvector init failed", "error", err)
				} else {
					var pvDocs []pgstore.PGVectorDocument
					for _, n := range indexNodes {
						pvDocs = append(pvDocs, pgstore.PGVectorDocument{
							NodeID:  n.ID,
							Content: buildContent(n),
							Metadata: map[string]string{
								"qualified_name": n.QualifiedName,
								"kind":           string(n.Kind),
								"file_path":      n.FilePath,
								"language":       n.Language,
							},
						})
					}
					if err := deps.PGStore.SyncPGVectorDocuments(ctx, pvDocs); err != nil {
						deps.Logger.Warn("pgvector sync failed", "error", err)
					} else {
						deps.Logger.Info("pgvector documents synced", "documents", len(pvDocs))
					}
				}
			}

			fmt.Fprintf(stdout(cmd), "Build complete: %d files, %d nodes, %d edges\n", totalFiles, totalNodes, totalEdges)
			deps.Logger.Info("build complete", "files", totalFiles, "nodes", totalNodes, "edges", totalEdges)

			return nil
		},
	}

	cmd.Flags().BoolVar(&syncGraph, "graph", false, "Sync to PostgreSQL + pgvector (requires PG_DSN)")
	cmd.Flags().BoolVar(&noRecursive, "no-recursive", false, "Only parse files in the top-level directory, skip subdirectories")
	cmd.Flags().StringArrayVar(&excludePatterns, "exclude", nil, "Exclude files/directories matching pattern (repeatable, e.g. --exclude vendor --exclude *.pb.go)")

	return cmd
}
