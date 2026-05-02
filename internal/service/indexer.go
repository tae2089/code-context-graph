package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gorm.io/gorm"

	"github.com/tae2089/trace"

	"github.com/tae2089/code-context-graph/internal/ctxns"
	"github.com/tae2089/code-context-graph/internal/model"
	"github.com/tae2089/code-context-graph/internal/parse"
	"github.com/tae2089/code-context-graph/internal/parse/treesitter"
	"github.com/tae2089/code-context-graph/internal/pathutil"
	"github.com/tae2089/code-context-graph/internal/store"
	"github.com/tae2089/code-context-graph/internal/store/search"
)

type parsedBuildNodeBatch struct {
	relPath     string
	nodes       []model.Node
	tsComments  []treesitter.CommentBlock
	sourceLines []string
}

type parsedBuildEdgeBatch struct {
	relPath string
	edges   []model.Edge
}

func newParsedBuildNodeBatch(relPath string, content []byte, nodes []model.Node, tsComments []treesitter.CommentBlock) parsedBuildNodeBatch {
	out := parsedBuildNodeBatch{
		relPath:    relPath,
		nodes:      nodes,
		tsComments: tsComments,
	}
	if len(tsComments) > 0 {
		out.sourceLines = strings.Split(string(content), "\n")
	}
	return out
}

func newParsedBuildEdgeBatch(relPath string, edges []model.Edge) parsedBuildEdgeBatch {
	return parsedBuildEdgeBatch{relPath: relPath, edges: edges}
}

// GraphService orchestrates graph building and search document refresh.
// @intent 파싱 결과 저장과 검색 인덱스 재구성을 하나의 서비스로 묶는다.
type GraphService struct {
	Store         store.GraphStore
	DB            *gorm.DB
	SearchBackend search.Backend
	Walkers       map[string]*treesitter.Walker
	Logger        *slog.Logger
}

// BuildOptions configures one graph build run.
// @intent 빌드 대상 경로와 탐색 범위를 호출자에서 제어하게 한다.
type BuildOptions struct {
	Dir                 string
	NoRecursive         bool
	ExcludePatterns     []string
	IncludePaths        []string
	MaxFileBytes        int64
	MaxTotalParsedBytes int64
}

// BuildStats reports how much content a build processed.
// @intent CLI와 호출자가 빌드 결과 규모를 사용자에게 보여줄 수 있게 한다.
type BuildStats struct {
	TotalFiles int
	TotalNodes int
	TotalEdges int
}

// Build walks source files, stores parsed graph data, and rebuilds search docs.
// @intent 지원 언어 소스를 그래프와 검색 문서로 일괄 동기화한다.
// @sideEffect 파일 시스템을 읽고 그래프 저장소·DB·검색 인덱스를 갱신한다.
// @requires s.Store, s.Walkers가 초기화되어 있어야 한다.
// @mutates 그래프 노드/엣지/어노테이션, search_documents
func (s *GraphService) Build(ctx context.Context, opts BuildOptions) (BuildStats, error) {
	var stats BuildStats

	absDir, err := filepath.Abs(opts.Dir)
	if err != nil {
		return stats, trace.Wrap(err, "resolve path")
	}
	if _, err := os.Stat(absDir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return stats, trace.Wrap(err, "build root does not exist")
		}
		return stats, trace.Wrap(err, "stat build root")
	}

	s.Logger.Info("building graph", "dir", absDir)

	var nodeBatches []parsedBuildNodeBatch
	var edgeBatches []parsedBuildEdgeBatch
	var walkCandidates []string

	err = filepath.Walk(absDir, func(path string, info os.FileInfo, err error) error {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if err != nil {
			return err
		}

		relPath, _ := filepath.Rel(absDir, path)

		if info.IsDir() {
			if path != absDir && opts.NoRecursive {
				return filepath.SkipDir
			}
			if pathutil.ShouldSkipDir(info.Name()) || pathutil.MatchExcludes(opts.ExcludePatterns, relPath) {
				return filepath.SkipDir
			}
			if len(opts.IncludePaths) > 0 && path != absDir && !pathutil.MatchIncludePaths(relPath, opts.IncludePaths) {
				return filepath.SkipDir
			}
			return nil
		}

		if pathutil.MatchExcludes(opts.ExcludePatterns, relPath) {
			return nil
		}
		if len(opts.IncludePaths) > 0 && !pathutil.MatchIncludePaths(relPath, opts.IncludePaths) {
			return nil
		}
		walkCandidates = append(walkCandidates, path)
		return nil
	})
	if err != nil {
		return stats, trace.Wrap(err, "preflight walk directory")
	}

	var totalParsedBytes int64
	for _, path := range walkCandidates {
		if err := ctx.Err(); err != nil {
			return stats, err
		}
		relPath, _ := filepath.Rel(absDir, path)

		ext := strings.ToLower(filepath.Ext(path))
		walker, ok := s.Walkers[ext]
		if !ok {
			continue
		}

		info, err := os.Stat(path)
		if err != nil {
			return stats, trace.Wrap(err, "stat build file "+relPath)
		}
		if err := CheckParseFileSize(relPath, info.Size(), opts.MaxFileBytes); err != nil {
			return stats, err
		}
		if err := CheckTotalParsedBytes(relPath, totalParsedBytes, info.Size(), opts.MaxTotalParsedBytes); err != nil {
			return stats, err
		}

		content, err := os.ReadFile(path)
		if err != nil {
			return stats, trace.Wrap(err, "read build file "+relPath)
		}
		totalParsedBytes += int64(len(content))
		if err := CheckTotalParsedBytes(relPath, 0, totalParsedBytes, opts.MaxTotalParsedBytes); err != nil {
			return stats, err
		}

		nodes, edges, tsComments, err := walker.ParseWithComments(ctx, relPath, content)
		if err != nil {
			return stats, trace.Wrap(err, "parse build file "+relPath)
		}

		nodeBatches = append(nodeBatches, newParsedBuildNodeBatch(relPath, content, nodes, tsComments))
		edgeBatches = append(edgeBatches, newParsedBuildEdgeBatch(relPath, edges))
		stats.TotalFiles++
		stats.TotalNodes += len(nodes)
		stats.TotalEdges += len(edges)
	}

	if err := ctx.Err(); err != nil {
		return stats, err
	}

	err = s.Store.WithTx(ctx, func(txStore store.GraphStore) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := txStore.DeleteGraph(ctx); err != nil {
			return trace.Wrap(err, "reset graph state before rebuild")
		}

		for _, parsed := range nodeBatches {
			if err := ctx.Err(); err != nil {
				return err
			}
			walker := s.Walkers[strings.ToLower(filepath.Ext(parsed.relPath))]

			if err := txStore.UpsertNodes(ctx, parsed.nodes); err != nil {
				return trace.Wrap(err, "upsert nodes for "+parsed.relPath)
			}

			if len(parsed.tsComments) > 0 {
				binderComments := toBinderComments(parsed.tsComments)
				binder := parse.NewBinder()
				bindings := binder.Bind(binderComments, parsed.nodes, walker.Language(), parsed.sourceLines)

				storedNodes, err := txStore.GetNodesByFile(ctx, parsed.relPath)
				if err != nil {
					return trace.Wrap(err, "get stored nodes for annotations")
				}
				storedMap := make(map[string]*model.Node, len(storedNodes))
				for i := range storedNodes {
					key := storedNodes[i].QualifiedName + ":" + strconv.Itoa(storedNodes[i].StartLine)
					storedMap[key] = &storedNodes[i]
				}

				for _, b := range bindings {
					key := b.Node.QualifiedName + ":" + strconv.Itoa(b.Node.StartLine)
					stored := storedMap[key]
					if stored == nil {
						continue
					}
					b.Annotation.NodeID = stored.ID
					if err := txStore.UpsertAnnotation(ctx, b.Annotation); err != nil {
						return trace.Wrap(err, "upsert annotation for "+stored.QualifiedName)
					}
				}
			}
		}

		nodeBatches = nil

		for _, parsed := range edgeBatches {
			if err := ctx.Err(); err != nil {
				return err
			}
			if len(parsed.edges) == 0 {
				continue
			}
			if err := txStore.UpsertEdges(ctx, parsed.edges); err != nil {
				return trace.Wrap(err, "upsert deferred edges for "+parsed.relPath)
			}
		}

		return nil
	})
	if err != nil {
		return stats, err
	}

	if s.SearchBackend != nil && s.DB != nil {
		docCount, err := RefreshSearchDocuments(ctx, s.DB)
		if err != nil {
			return stats, err
		}
		if err := s.SearchBackend.Rebuild(ctx, s.DB); err != nil {
			return stats, trace.Wrap(err, "rebuild search index")
		}
		s.Logger.Info("search index rebuilt", "documents", docCount)
	}

	s.Logger.Info("build complete", "files", stats.TotalFiles, "nodes", stats.TotalNodes, "edges", stats.TotalEdges)

	return stats, nil
}

func CheckParseFileSize(relPath string, size int64, maxFileBytes int64) error {
	if maxFileBytes > 0 && size > maxFileBytes {
		return fmt.Errorf("parse file %s exceeds max file bytes: %d > %d", relPath, size, maxFileBytes)
	}
	return nil
}

func CheckTotalParsedBytes(relPath string, current int64, next int64, maxTotalBytes int64) error {
	if maxTotalBytes > 0 && current+next > maxTotalBytes {
		return fmt.Errorf("parse file %s exceeds max total parsed bytes: %d > %d", relPath, current+next, maxTotalBytes)
	}
	return nil
}

// RefreshSearchDocuments rebuilds namespace-scoped search_documents from current graph nodes.
// @intent keep derived search documents consistent with graph state before FTS rebuilds
func RefreshSearchDocuments(ctx context.Context, db *gorm.DB) (int, error) {
	ns := ctxns.FromContext(ctx)
	buildContent := func(n model.Node, annByNode map[uint]*model.Annotation) string {
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
	count := 0
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		docsQ := tx.WithContext(ctx).Where("namespace = ?", ns)
		nodesQ := tx.WithContext(ctx).
			Where("kind IN ?", []string{"function", "class", "type", "test", "file"}).
			Where("namespace = ?", ns)
		if err := docsQ.Delete(&model.SearchDocument{}).Error; err != nil {
			return trace.Wrap(err, "clear search documents")
		}

		var batchNodes []model.Node
		result := nodesQ.FindInBatches(&batchNodes, 500, func(batchTx *gorm.DB, batch int) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			_ = batchTx
			nodeIDs := make([]uint, len(batchNodes))
			for i, n := range batchNodes {
				nodeIDs[i] = n.ID
			}
			annByNode := map[uint]*model.Annotation{}
			if len(nodeIDs) > 0 {
				var annotations []model.Annotation
				annQ := tx.Session(&gorm.Session{NewDB: true}).WithContext(ctx).Model(&model.Annotation{})
				if err := annQ.Where("node_id IN ?", nodeIDs).Find(&annotations).Error; err != nil {
					return trace.Wrap(err, "load annotations batch "+strconv.Itoa(batch))
				}
				if len(annotations) > 0 {
					annotationIDs := make([]uint, len(annotations))
					for i := range annotations {
						annotationIDs[i] = annotations[i].ID
					}
					var tags []model.DocTag
					tagsQ := tx.Session(&gorm.Session{NewDB: true}).WithContext(ctx).Model(&model.DocTag{})
					if err := tagsQ.Where("annotation_id IN ?", annotationIDs).Order("annotation_id, ordinal").Find(&tags).Error; err != nil {
						return trace.Wrap(err, "load annotation tags batch "+strconv.Itoa(batch))
					}
					tagsByAnnotation := make(map[uint][]model.DocTag, len(annotations))
					for _, tag := range tags {
						tagsByAnnotation[tag.AnnotationID] = append(tagsByAnnotation[tag.AnnotationID], tag)
					}
					for i := range annotations {
						annotations[i].Tags = tagsByAnnotation[annotations[i].ID]
					}
				}
				for i := range annotations {
					annByNode[annotations[i].NodeID] = &annotations[i]
				}
			}
			docs := make([]model.SearchDocument, 0, len(batchNodes))
			for _, n := range batchNodes {
				docs = append(docs, model.SearchDocument{
					Namespace: n.Namespace,
					NodeID:    n.ID,
					Content:   buildContent(n, annByNode),
					Language:  n.Language,
				})
			}
			if len(docs) > 0 {
				if err := tx.WithContext(ctx).CreateInBatches(docs, 100).Error; err != nil {
					return trace.Wrap(err, "batch insert search documents")
				}
			}
			count += len(docs)
			return nil
		})
		if result.Error != nil {
			return trace.Wrap(result.Error, "load index nodes")
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return count, nil
}

// toBinderComments converts walker comment blocks into binder comment blocks,
// preserving docstring bookkeeping required by the Python docstring binding path.
// @intent keep IsDocstring and OwnerStartLine in sync between walker and binder types
func toBinderComments(tsComments []treesitter.CommentBlock) []parse.CommentBlock {
	out := make([]parse.CommentBlock, len(tsComments))
	for i, c := range tsComments {
		out[i] = parse.CommentBlock{
			StartLine:      c.StartLine,
			EndLine:        c.EndLine,
			Text:           c.Text,
			IsDocstring:    c.IsDocstring,
			OwnerStartLine: c.OwnerStartLine,
		}
	}
	return out
}
