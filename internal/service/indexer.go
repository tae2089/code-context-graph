package service

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"gorm.io/gorm"

	"github.com/imtaebin/code-context-graph/internal/model"
	"github.com/imtaebin/code-context-graph/internal/parse"
	"github.com/imtaebin/code-context-graph/internal/parse/treesitter"
	"github.com/imtaebin/code-context-graph/internal/pathutil"
	"github.com/imtaebin/code-context-graph/internal/store"
	"github.com/imtaebin/code-context-graph/internal/store/search"
)

var skipDirs = map[string]bool{
	".git":         true,
	"vendor":       true,
	"node_modules": true,
}

type GraphService struct {
	Store         store.GraphStore
	DB            *gorm.DB
	SearchBackend search.Backend
	Walkers       map[string]*treesitter.Walker
	Logger        *slog.Logger
}

type BuildOptions struct {
	Dir             string
	NoRecursive     bool
	ExcludePatterns []string
}

type BuildStats struct {
	TotalFiles int
	TotalNodes int
	TotalEdges int
}

func (s *GraphService) Build(ctx context.Context, opts BuildOptions) (BuildStats, error) {
	var stats BuildStats

	absDir, err := filepath.Abs(opts.Dir)
	if err != nil {
		return stats, fmt.Errorf("resolve path: %w", err)
	}

	s.Logger.Info("building graph", "dir", absDir)

	err = filepath.Walk(absDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		relPath, _ := filepath.Rel(absDir, path)

		if info.IsDir() {
			if path != absDir && opts.NoRecursive {
				return filepath.SkipDir
			}
			if skipDirs[info.Name()] || pathutil.MatchExcludes(opts.ExcludePatterns, relPath) {
				return filepath.SkipDir
			}
			return nil
		}

		if pathutil.MatchExcludes(opts.ExcludePatterns, relPath) {
			return nil
		}

		ext := strings.ToLower(filepath.Ext(path))
		walker, ok := s.Walkers[ext]
		if !ok {
			return nil
		}

		content, err := os.ReadFile(path)
		if err != nil {
			s.Logger.Warn("skip unreadable file", "path", path, "error", err)
			return nil
		}

		nodes, edges, tsComments, err := walker.ParseWithComments(relPath, content)
		if err != nil {
			s.Logger.Warn("parse failed", "path", relPath, "error", err)
			return nil
		}

		err = s.Store.WithTx(ctx, func(txStore store.GraphStore) error {
			if len(nodes) > 0 {
				if err := txStore.UpsertNodes(ctx, nodes); err != nil {
					return fmt.Errorf("upsert nodes for %s: %w", relPath, err)
				}
			}
			if len(edges) > 0 {
				if err := txStore.UpsertEdges(ctx, edges); err != nil {
					return fmt.Errorf("upsert edges for %s: %w", relPath, err)
				}
			}

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
					stored, err := txStore.GetNode(ctx, b.Node.QualifiedName)
					if err != nil || stored == nil {
						continue
					}
					b.Annotation.NodeID = stored.ID
					txStore.UpsertAnnotation(ctx, b.Annotation)
				}
			}
			return nil
		})

		if err != nil {
			return fmt.Errorf("transaction failed for %s: %w", relPath, err)
		}

		stats.TotalFiles++
		stats.TotalNodes += len(nodes)
		stats.TotalEdges += len(edges)

		return nil
	})
	if err != nil {
		return stats, fmt.Errorf("walk directory: %w", err)
	}

	// Batch load all nodes and annotations for indexing
	var indexNodes []model.Node
	annByNode := map[uint]*model.Annotation{}
	if s.DB != nil {
		s.DB.Where("kind IN ?", []string{"function", "class", "type", "test", "file"}).Find(&indexNodes)
		nodeIDs := make([]uint, len(indexNodes))
		for i, n := range indexNodes {
			nodeIDs[i] = n.ID
		}
		if len(nodeIDs) > 0 {
			var annotations []model.Annotation
			s.DB.Where("node_id IN ?", nodeIDs).Preload("Tags").Find(&annotations)
			for i := range annotations {
				annByNode[annotations[i].NodeID] = &annotations[i]
			}
		}
	}

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

	if s.SearchBackend != nil && s.DB != nil {
		s.DB.Where("1 = 1").Delete(&model.SearchDocument{})
		docs := make([]model.SearchDocument, 0, len(indexNodes))
		for _, n := range indexNodes {
			docs = append(docs, model.SearchDocument{
				NodeID:   n.ID,
				Content:  buildContent(n),
				Language: n.Language,
			})
		}
		if len(docs) > 0 {
			s.DB.CreateInBatches(docs, 100)
		}
		if err := s.SearchBackend.Rebuild(ctx, s.DB); err != nil {
			s.Logger.Warn("search index rebuild failed", "error", err)
		} else {
			s.Logger.Info("search index rebuilt", "documents", len(docs))
		}
	}

	s.Logger.Info("build complete", "files", stats.TotalFiles, "nodes", stats.TotalNodes, "edges", stats.TotalEdges)

	return stats, nil
}
