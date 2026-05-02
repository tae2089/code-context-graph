package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gorm.io/gorm"

	"github.com/tae2089/trace"

	"github.com/tae2089/code-context-graph/internal/analysis/incremental"
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
	language    string
	sourceLines []string
}

type parsedBuildEdgeBatch struct {
	relPath string
	edges   []model.Edge
}

var testBuildBatchReleaseHook func([]parsedBuildNodeBatch, int)

const (
	buildFlushFileBatchSize = 100
	buildFlushParsedBytes   = 16 << 20
)

func newParsedBuildNodeBatch(relPath string, content []byte, nodes []model.Node, tsComments []treesitter.CommentBlock, language string) parsedBuildNodeBatch {
	out := parsedBuildNodeBatch{
		relPath:    relPath,
		nodes:      nodes,
		tsComments: tsComments,
		language:   language,
	}
	if len(tsComments) > 0 {
		out.sourceLines = strings.Split(string(content), "\n")
	}
	return out
}

func newParsedBuildEdgeBatch(relPath string, edges []model.Edge) parsedBuildEdgeBatch {
	return parsedBuildEdgeBatch{relPath: relPath, edges: edges}
}

func (s *GraphService) bindAndReleaseNodeBatch(ctx context.Context, txStore store.GraphStore, batches []parsedBuildNodeBatch, idx int) error {
	parsed := &batches[idx]

	if err := txStore.UpsertNodes(ctx, parsed.nodes); err != nil {
		return trace.Wrap(err, "upsert nodes for "+parsed.relPath)
	}

	if len(parsed.tsComments) > 0 {
		binderComments := toBinderComments(parsed.tsComments)
		binder := parse.NewBinder()
		bindings := binder.Bind(binderComments, parsed.nodes, parsed.language, parsed.sourceLines)

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

	parsed.tsComments = nil
	parsed.sourceLines = nil
	if testBuildBatchReleaseHook != nil {
		testBuildBatchReleaseHook(batches, idx)
	}
	return nil
}

// Parser defines the minimum parser behavior required by graph builds.
// @intent let CLI, MCP, and tests share the same build policy with their parser implementations
type Parser interface {
	Parse(filePath string, content []byte) ([]model.Node, []model.Edge, error)
	ParseWithContext(ctx context.Context, filePath string, content []byte) ([]model.Node, []model.Edge, error)
}

type commentParserWithLanguage interface {
	Parser
	ParseWithComments(ctx context.Context, filePath string, content []byte) ([]model.Node, []model.Edge, []treesitter.CommentBlock, error)
	Language() string
}

// IncrementalSyncer defines the sync operation GraphService.Update delegates to.
// @intent keep filesystem/build policy in service while preserving the existing incremental engine
type IncrementalSyncer interface {
	SyncWithExisting(ctx context.Context, files map[string]incremental.FileInfo, existingFiles []string) (*incremental.SyncStats, error)
}

type transactionalDBStore interface {
	WithTxDB(ctx context.Context, fn func(store.GraphStore, *gorm.DB) error) error
}

// GraphService orchestrates graph building and search document refresh.
// @intent 파싱 결과 저장과 검색 인덱스 재구성을 하나의 서비스로 묶는다.
type GraphService struct {
	Store         store.GraphStore
	DB            *gorm.DB
	SearchBackend search.Backend
	Walkers       map[string]*treesitter.Walker
	Parsers       map[string]Parser
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
	SkipSearchRebuild   bool
}

// BuildStats reports how much content a build processed.
// @intent CLI와 호출자가 빌드 결과 규모를 사용자에게 보여줄 수 있게 한다.
type BuildStats struct {
	TotalFiles int
	TotalNodes int
	TotalEdges int
}

// UpdateOptions configures one incremental graph sync run.
// @intent reuse GraphService traversal and parse limit policy for CLI and MCP updates
type UpdateOptions struct {
	BuildOptions
	Syncer  IncrementalSyncer
	Replace bool
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

	s.logger().Info("building graph", "dir", absDir)

	if err := ctx.Err(); err != nil {
		return stats, err
	}

	err = s.withBuildTx(ctx, opts, func(txStore store.GraphStore, txDB *gorm.DB) error {
		return s.buildGraphInTx(ctx, txStore, txDB, absDir, opts, &stats)
	})
	if err != nil {
		return stats, err
	}

	s.logger().Info("build complete", "files", stats.TotalFiles, "nodes", stats.TotalNodes, "edges", stats.TotalEdges)

	return stats, nil
}

func (s *GraphService) withBuildTx(ctx context.Context, opts BuildOptions, fn func(store.GraphStore, *gorm.DB) error) error {
	if opts.SkipSearchRebuild || s.SearchBackend == nil || s.DB == nil {
		return s.Store.WithTx(ctx, func(txStore store.GraphStore) error {
			return fn(txStore, nil)
		})
	}

	txStore, ok := s.Store.(transactionalDBStore)
	if !ok {
		return trace.New("graph store does not support DB transaction handle for search rebuild")
	}
	return txStore.WithTxDB(ctx, fn)
}

func (s *GraphService) buildGraphInTx(ctx context.Context, txStore store.GraphStore, txDB *gorm.DB, absDir string, opts BuildOptions, stats *BuildStats) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := txStore.DeleteGraph(ctx); err != nil {
		return trace.Wrap(err, "reset graph state before rebuild")
	}

	batch := buildPersistBatch{}
	var totalParsedBytes int64
	if err := walkMatchingFiles(ctx, absDir, opts, func(path, relPath string) error {
		parser, ok := s.parserForExt(strings.ToLower(filepath.Ext(path)))
		if !ok {
			return nil
		}

		info, err := os.Stat(path)
		if err != nil {
			return trace.Wrap(err, "stat build file "+relPath)
		}
		if err := CheckParseFileSize(relPath, info.Size(), opts.MaxFileBytes); err != nil {
			return err
		}
		if err := CheckTotalParsedBytes(relPath, totalParsedBytes, info.Size(), opts.MaxTotalParsedBytes); err != nil {
			return err
		}

		content, err := os.ReadFile(path)
		if err != nil {
			return trace.Wrap(err, "read build file "+relPath)
		}
		contentBytes := int64(len(content))
		totalParsedBytes += contentBytes
		if err := CheckTotalParsedBytes(relPath, 0, totalParsedBytes, opts.MaxTotalParsedBytes); err != nil {
			return err
		}

		nodes, edges, tsComments, language, err := parseForBuild(ctx, parser, relPath, content)
		if err != nil {
			return trace.Wrap(err, "parse build file "+relPath)
		}

		batch.add(newParsedBuildNodeBatch(relPath, content, nodes, tsComments, language), newParsedBuildEdgeBatch(relPath, edges), contentBytes)
		stats.TotalFiles++
		stats.TotalNodes += len(nodes)
		stats.TotalEdges += len(edges)

		if batch.shouldFlush() {
			return s.flushBuildBatch(ctx, txStore, &batch)
		}
		return nil
	}); err != nil {
		return trace.Wrap(err, "walk build directory")
	}
	if err := s.flushBuildBatch(ctx, txStore, &batch); err != nil {
		return err
	}

	if !opts.SkipSearchRebuild && s.SearchBackend != nil && s.DB != nil {
		if txDB == nil {
			return trace.New("search rebuild requires transaction DB handle")
		}
		if err := s.rebuildSearchWithDB(ctx, txDB); err != nil {
			return err
		}
	}

	return nil
}

type buildPersistBatch struct {
	nodeBatches []parsedBuildNodeBatch
	edgeBatches []parsedBuildEdgeBatch
	files       int
	bytes       int64
}

func (b *buildPersistBatch) add(nodeBatch parsedBuildNodeBatch, edgeBatch parsedBuildEdgeBatch, parsedBytes int64) {
	b.nodeBatches = append(b.nodeBatches, nodeBatch)
	b.edgeBatches = append(b.edgeBatches, edgeBatch)
	b.files++
	b.bytes += parsedBytes
}

func (b *buildPersistBatch) shouldFlush() bool {
	return b.files >= buildFlushFileBatchSize || b.bytes >= buildFlushParsedBytes
}

func (b *buildPersistBatch) reset() {
	b.nodeBatches = nil
	b.edgeBatches = nil
	b.files = 0
	b.bytes = 0
}

func (s *GraphService) flushBuildBatch(ctx context.Context, txStore store.GraphStore, batch *buildPersistBatch) error {
	if batch.files == 0 {
		return nil
	}
	for i := range batch.nodeBatches {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := s.bindAndReleaseNodeBatch(ctx, txStore, batch.nodeBatches, i); err != nil {
			return err
		}
	}

	for _, parsed := range batch.edgeBatches {
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

	batch.reset()
	return nil
}

// Update incrementally syncs changed files into the graph and optionally rebuilds search.
// @intent centralize file collection, include path, parse limit, and search policy for update callers
func (s *GraphService) Update(ctx context.Context, opts UpdateOptions) (*incremental.SyncStats, error) {
	if opts.Syncer == nil {
		return nil, trace.New("incremental syncer is not configured")
	}

	absDir, err := filepath.Abs(opts.Dir)
	if err != nil {
		return nil, trace.Wrap(err, "resolve path")
	}
	if _, err := os.Stat(absDir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, trace.Wrap(err, "update root does not exist")
		}
		return nil, trace.Wrap(err, "stat update root")
	}

	s.logger().Info("incremental update", "dir", absDir)

	files := make(map[string]incremental.FileInfo)
	var totalParsedBytes int64
	if err := walkMatchingFiles(ctx, absDir, opts.BuildOptions, func(path, relPath string) error {
		if _, ok := s.parserForExt(strings.ToLower(filepath.Ext(path))); !ok {
			return nil
		}

		info, err := os.Stat(path)
		if err != nil {
			s.logger().Warn("skip unreadable update file", "file", relPath, "error", err)
			return nil
		}
		if err := CheckParseFileSize(relPath, info.Size(), opts.MaxFileBytes); err != nil {
			return err
		}
		if err := CheckTotalParsedBytes(relPath, totalParsedBytes, info.Size(), opts.MaxTotalParsedBytes); err != nil {
			return err
		}

		content, err := os.ReadFile(path)
		if err != nil {
			s.logger().Warn("skip unreadable update file", "file", relPath, "error", err)
			return nil
		}
		totalParsedBytes += int64(len(content))
		if err := CheckTotalParsedBytes(relPath, 0, totalParsedBytes, opts.MaxTotalParsedBytes); err != nil {
			return err
		}
		hash := sha256.Sum256(content)
		files[relPath] = incremental.FileInfo{
			Hash:    hex.EncodeToString(hash[:]),
			Content: content,
		}
		return nil
	}); err != nil {
		return nil, trace.Wrap(err, "walk update directory")
	}

	existingFiles, err := ExistingGraphFiles(ctx, s.DB)
	if err != nil {
		return nil, trace.Wrap(err, "load existing graph files")
	}
	if !opts.Replace && len(opts.IncludePaths) > 0 {
		filtered := make([]string, 0, len(existingFiles))
		for _, fp := range existingFiles {
			if pathutil.MatchIncludePaths(fp, opts.IncludePaths) {
				filtered = append(filtered, fp)
			}
		}
		existingFiles = filtered
	}

	stats, err := opts.Syncer.SyncWithExisting(ctx, files, existingFiles)
	if err != nil {
		return nil, trace.Wrap(err, "incremental sync")
	}
	if !opts.SkipSearchRebuild {
		if err := s.rebuildSearch(ctx); err != nil {
			return nil, err
		}
	}
	return stats, nil
}

func (s *GraphService) logger() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}

func (s *GraphService) parserForExt(ext string) (Parser, bool) {
	if s.Parsers != nil {
		parser, ok := s.Parsers[ext]
		return parser, ok
	}
	parser, ok := s.Walkers[ext]
	return parser, ok
}

func (s *GraphService) rebuildSearch(ctx context.Context) error {
	if s.SearchBackend == nil || s.DB == nil {
		return nil
	}
	return s.rebuildSearchWithDB(ctx, s.DB)
}

func (s *GraphService) rebuildSearchWithDB(ctx context.Context, db *gorm.DB) error {
	if s.SearchBackend == nil || db == nil {
		return nil
	}
	docCount, err := RefreshSearchDocuments(ctx, db)
	if err != nil {
		return err
	}
	if err := s.SearchBackend.Rebuild(ctx, db); err != nil {
		return trace.Wrap(err, "rebuild search index")
	}
	s.logger().Info("search index rebuilt", "documents", docCount)
	return nil
}

func walkMatchingFiles(ctx context.Context, absDir string, opts BuildOptions, fn func(path, relPath string) error) error {
	return filepath.Walk(absDir, func(path string, info os.FileInfo, err error) error {
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
		return fn(path, relPath)
	})
}

func parseForBuild(ctx context.Context, parser Parser, relPath string, content []byte) ([]model.Node, []model.Edge, []treesitter.CommentBlock, string, error) {
	if cp, ok := parser.(commentParserWithLanguage); ok {
		nodes, edges, comments, err := cp.ParseWithComments(ctx, relPath, content)
		return nodes, edges, comments, cp.Language(), err
	}
	nodes, edges, err := parser.ParseWithContext(ctx, relPath, content)
	return nodes, edges, nil, "", err
}

// ExistingGraphFiles returns namespace-scoped graph file paths currently stored.
// @intent share deletion-scope discovery across CLI and MCP incremental updates
func ExistingGraphFiles(ctx context.Context, db *gorm.DB) ([]string, error) {
	if db == nil {
		return nil, nil
	}

	ns := ctxns.FromContext(ctx)
	query := db.WithContext(ctx).Model(&model.Node{}).Where("namespace = ?", ns)

	var filePaths []string
	if err := query.Distinct().Pluck("file_path", &filePaths).Error; err != nil {
		return nil, err
	}
	return filePaths, nil
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
