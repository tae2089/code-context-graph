package service

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gorm.io/gorm"

	"github.com/tae2089/trace"

	"github.com/tae2089/code-context-graph/internal/model"
	"github.com/tae2089/code-context-graph/internal/parse"
	"github.com/tae2089/code-context-graph/internal/parse/treesitter"
	"github.com/tae2089/code-context-graph/internal/pathutil"
	"github.com/tae2089/code-context-graph/internal/store"
	"github.com/tae2089/code-context-graph/internal/store/search"
)

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
	Dir             string
	NoRecursive     bool
	ExcludePatterns []string
	IncludePaths    []string
	// BinderMaxGap overrides the binder's comment-to-node gap threshold.
	// 0 means use the binder's built-in default (3).
	BinderMaxGap int
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

	s.Logger.Info("building graph", "dir", absDir)

	type deferredEdges struct {
		relPath string
		edges   []model.Edge
	}
	var allDeferred []deferredEdges

	err = filepath.Walk(absDir, func(path string, info os.FileInfo, err error) error {
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

		ext := strings.ToLower(filepath.Ext(path))
		walker, ok := s.Walkers[ext]
		if !ok {
			return nil
		}

		content, err := os.ReadFile(path)
		if err != nil {
			s.Logger.Warn("skip unreadable file", "path", path, trace.SlogError(err))
			return nil
		}

		nodes, edges, tsComments, err := walker.ParseWithComments(ctx, relPath, content)
		if err != nil {
			s.Logger.Warn("parse failed", "path", relPath, trace.SlogError(err))
			return nil
		}

		err = s.Store.WithTx(ctx, func(txStore store.GraphStore) error {
			if len(nodes) > 0 {
				if err := txStore.UpsertNodes(ctx, nodes); err != nil {
					return trace.Wrap(err, "upsert nodes for "+relPath)
				}
			}

			if len(tsComments) > 0 {
				binderComments := toBinderComments(tsComments)
				binder := parse.NewBinderFromConfig(opts.BinderMaxGap)
				bindings := binder.Bind(binderComments, nodes, walker.Language())

				qNames := make([]string, len(bindings))
				for i, b := range bindings {
					qNames[i] = b.Node.QualifiedName
				}
				storedMap, err := txStore.GetNodesByQualifiedNames(ctx, qNames)
				if err != nil {
					return trace.Wrap(err, "batch get nodes for annotations")
				}

				for _, b := range bindings {
					stored := storedMap[b.Node.QualifiedName]
					if stored == nil {
						continue
					}
					b.Annotation.NodeID = stored.ID
					if err := txStore.UpsertAnnotation(ctx, b.Annotation); err != nil {
						return trace.Wrap(err, "upsert annotation for "+stored.QualifiedName)
					}
				}
			}
			return nil
		})

		if err != nil {
			return trace.Wrap(err, "transaction failed for "+relPath)
		}

		if len(edges) > 0 {
			allDeferred = append(allDeferred, deferredEdges{relPath: relPath, edges: edges})
		}

		stats.TotalFiles++
		stats.TotalNodes += len(nodes)
		stats.TotalEdges += len(edges)

		return nil
	})
	if err != nil {
		return stats, trace.Wrap(err, "walk directory")
	}

	for _, d := range allDeferred {
		if err := s.Store.WithTx(ctx, func(txStore store.GraphStore) error {
			return txStore.UpsertEdges(ctx, d.edges)
		}); err != nil {
			return stats, trace.Wrap(err, "upsert deferred edges for "+d.relPath)
		}
	}

	if s.SearchBackend != nil && s.DB != nil {
		if err := s.DB.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&model.SearchDocument{}).Error; err != nil {
			return stats, trace.Wrap(err, "clear search documents")
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
					return trace.Wrap(err, "load annotations batch "+strconv.Itoa(batch))
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
			return stats, trace.Wrap(result.Error, "load index nodes")
		}

		if len(docs) > 0 {
			if err := s.DB.CreateInBatches(docs, 100).Error; err != nil {
				return stats, trace.Wrap(err, "batch insert search documents")
			}
		}
		if err := s.SearchBackend.Rebuild(ctx, s.DB); err != nil {
			s.Logger.Warn("search index rebuild failed", trace.SlogError(err))
		} else {
			s.Logger.Info("search index rebuilt", "documents", len(docs))
		}
	}

	s.Logger.Info("build complete", "files", stats.TotalFiles, "nodes", stats.TotalNodes, "edges", stats.TotalEdges)

	return stats, nil
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
