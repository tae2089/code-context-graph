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

		nodes, edges, tsComments, err := walker.ParseWithComments(ctx, relPath, content)
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
					if err := txStore.UpsertAnnotation(ctx, b.Annotation); err != nil {
						return fmt.Errorf("upsert annotation for %s: %w", stored.QualifiedName, err)
					}
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

	if s.SearchBackend != nil && s.DB != nil {
		if err := s.DB.Where("1 = 1").Delete(&model.SearchDocument{}).Error; err != nil {
			return stats, fmt.Errorf("clear search documents: %w", err)
		}

		var docs []model.SearchDocument
		annByNode := map[uint]*model.Annotation{}

		buildContent := func(n model.Node) string {
			var sb strings.Builder
			sb.WriteString(n.Name)
			sb.WriteByte(' ')
			sb.WriteString(n.QualifiedName)
			sb.WriteByte(' ')
			sb.WriteString(string(n.Kind))
			if ann := annByNode[n.ID]; ann != nil {
				if ann.Summary != "" {
					sb.WriteByte(' ')
					sb.WriteString(ann.Summary)
				}
				if ann.Context != "" {
					sb.WriteByte(' ')
					sb.WriteString(ann.Context)
				}
				for _, tag := range ann.Tags {
					sb.WriteByte(' ')
					sb.WriteString(tag.Value)
				}
			}
			return sb.String()
		}

		// FindInBatches로 노드를 500개씩 처리하여 대규모 IN 절 및 전체 메모리 로드 방지
		var batchNodes []model.Node
		result := s.DB.Where("kind IN ?", []string{"function", "class", "type", "test", "file"}).
			FindInBatches(&batchNodes, 500, func(tx *gorm.DB, batch int) error {
				nodeIDs := make([]uint, len(batchNodes))
				for i, n := range batchNodes {
					nodeIDs[i] = n.ID
				}
				var annotations []model.Annotation
				if err := s.DB.Where("node_id IN ?", nodeIDs).Preload("Tags").Find(&annotations).Error; err != nil {
					return fmt.Errorf("load annotations batch %d: %w", batch, err)
				}
				for i := range annotations {
					annByNode[annotations[i].NodeID] = &annotations[i]
				}
				for _, n := range batchNodes {
					docs = append(docs, model.SearchDocument{
						NodeID:   n.ID,
						Content:  buildContent(n),
						Language: n.Language,
					})
				}
				// 배치 처리 완료 후 annByNode 초기화로 메모리 반환
				for _, n := range batchNodes {
					delete(annByNode, n.ID)
				}
				return nil
			})
		if result.Error != nil {
			return stats, fmt.Errorf("load index nodes: %w", result.Error)
		}

		if len(docs) > 0 {
			if err := s.DB.CreateInBatches(docs, 100).Error; err != nil {
				return stats, fmt.Errorf("batch insert search documents: %w", err)
			}
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
