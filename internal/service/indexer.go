package service

import (
	"context"
	"crypto/sha256"
	"encoding/gob"
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

type spooledBuildRecord struct {
	RelPath     string
	Nodes       []model.Node
	Comments    []treesitter.CommentBlock
	Language    string
	SourceLines []string
	Edges       []model.Edge
	Bytes       int64
}

type buildSpool struct {
	dir     string
	records []string
	stats   BuildStats
}

type spooledUpdateRecord struct {
	Files map[string]incremental.FileInfo
	Bytes int64
}

type updateSpool struct {
	dir           string
	records       []string
	currentFiles  map[string]struct{}
	currentHashes map[string]string
	forceFiles    map[string]struct{}
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

type transactionalIncrementalSyncer interface {
	SyncWithExistingStore(ctx context.Context, syncStore incremental.Store, files map[string]incremental.FileInfo, existingFiles []string) (*incremental.SyncStats, error)
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

	spool, err := s.prepareBuildSpool(ctx, absDir, opts)
	if err != nil {
		return stats, err
	}
	defer spool.cleanup(s.logger())
	stats = spool.stats

	err = s.withBuildTx(ctx, opts, func(txStore store.GraphStore, txDB *gorm.DB) error {
		return s.applyBuildSpoolInTx(ctx, txStore, txDB, opts, spool)
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

func (s *GraphService) prepareBuildSpool(ctx context.Context, absDir string, opts BuildOptions) (*buildSpool, error) {
	dir, err := os.MkdirTemp("", "ccg-build-spool-*")
	if err != nil {
		return nil, trace.Wrap(err, "create build spool")
	}
	spool := &buildSpool{dir: dir}
	var totalParsedBytes int64
	var seq int

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

		nodeBatch := newParsedBuildNodeBatch(relPath, content, nodes, tsComments, language)
		record := spooledBuildRecord{
			RelPath:     relPath,
			Nodes:       nodes,
			Comments:    tsComments,
			Language:    language,
			SourceLines: nodeBatch.sourceLines,
			Edges:       edges,
			Bytes:       contentBytes,
		}
		if err := spool.writeRecord(seq, record); err != nil {
			return err
		}
		seq++
		spool.stats.TotalFiles++
		spool.stats.TotalNodes += len(nodes)
		spool.stats.TotalEdges += len(edges)
		return nil
	}); err != nil {
		spool.cleanup(s.logger())
		return nil, trace.Wrap(err, "walk build directory")
	}

	return spool, nil
}

func (b *buildSpool) writeRecord(seq int, record spooledBuildRecord) error {
	path := filepath.Join(b.dir, fmt.Sprintf("%06d.gob", seq))
	file, err := os.Create(path)
	if err != nil {
		return trace.Wrap(err, "create build spool record")
	}
	encErr := gob.NewEncoder(file).Encode(record)
	closeErr := file.Close()
	if encErr != nil {
		return trace.Wrap(encErr, "encode build spool record")
	}
	if closeErr != nil {
		return trace.Wrap(closeErr, "close build spool record")
	}
	b.records = append(b.records, path)
	return nil
}

func (b *buildSpool) readRecord(path string) (spooledBuildRecord, error) {
	var record spooledBuildRecord
	file, err := os.Open(path)
	if err != nil {
		return record, trace.Wrap(err, "open build spool record")
	}
	decodeErr := gob.NewDecoder(file).Decode(&record)
	closeErr := file.Close()
	if decodeErr != nil {
		return record, trace.Wrap(decodeErr, "decode build spool record")
	}
	if closeErr != nil {
		return record, trace.Wrap(closeErr, "close build spool record")
	}
	return record, nil
}

func (b *buildSpool) cleanup(logger *slog.Logger) {
	if b == nil || b.dir == "" {
		return
	}
	if err := os.RemoveAll(b.dir); err != nil && logger != nil {
		logger.Warn("cleanup build spool failed", "dir", b.dir, "error", err)
	}
}

func (s *GraphService) applyBuildSpoolInTx(ctx context.Context, txStore store.GraphStore, txDB *gorm.DB, opts BuildOptions, spool *buildSpool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := txStore.DeleteGraph(ctx); err != nil {
		return trace.Wrap(err, "reset graph state before rebuild")
	}

	batch := buildPersistBatch{}
	for _, path := range spool.records {
		record, err := spool.readRecord(path)
		if err != nil {
			return err
		}
		batch.add(parsedBuildNodeBatch{
			relPath:     record.RelPath,
			nodes:       record.Nodes,
			tsComments:  record.Comments,
			language:    record.Language,
			sourceLines: record.SourceLines,
		}, parsedBuildEdgeBatch{
			relPath: record.RelPath,
			edges:   record.Edges,
		}, record.Bytes)
		if batch.shouldFlush() {
			if err := s.flushBuildBatch(ctx, txStore, &batch); err != nil {
				return err
			}
		}
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

	if _, ok := opts.Syncer.(transactionalIncrementalSyncer); !ok || s.Store == nil {
		return s.updateGraphWithoutTx(ctx, absDir, opts)
	}

	spool, err := s.prepareUpdateSpool(ctx, absDir, opts)
	if err != nil {
		return nil, err
	}
	defer spool.cleanup(s.logger())

	stats := &incremental.SyncStats{}
	err = s.withUpdateTx(ctx, opts, func(txStore store.GraphStore, txDB *gorm.DB) error {
		var err error
		stats, err = s.applyUpdateSpoolInTx(ctx, txStore, txDB, opts, spool)
		return err
	})
	if err != nil {
		return nil, err
	}
	return stats, nil
}

func (s *GraphService) withUpdateTx(ctx context.Context, opts UpdateOptions, fn func(store.GraphStore, *gorm.DB) error) error {
	if s.Store == nil {
		return fn(nil, s.DB)
	}
	if _, ok := opts.Syncer.(transactionalIncrementalSyncer); !ok {
		if !opts.SkipSearchRebuild && s.SearchBackend != nil && s.DB != nil {
			return trace.New("incremental syncer does not support transaction-scoped store")
		}
		return fn(nil, s.DB)
	}
	if txStore, ok := s.Store.(transactionalDBStore); ok && s.DB != nil {
		return txStore.WithTxDB(ctx, fn)
	}
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

func (s *GraphService) prepareUpdateSpool(ctx context.Context, absDir string, opts UpdateOptions) (*updateSpool, error) {
	dir, err := os.MkdirTemp("", "ccg-update-spool-*")
	if err != nil {
		return nil, trace.Wrap(err, "create update spool")
	}
	spool := &updateSpool{
		dir:           dir,
		currentFiles:  make(map[string]struct{}),
		currentHashes: make(map[string]string),
	}
	batch := make(map[string]incremental.FileInfo)
	var batchBytes int64
	var totalParsedBytes int64
	var seq int

	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		record := spooledUpdateRecord{Files: batch, Bytes: batchBytes}
		if err := spool.writeRecord(seq, record); err != nil {
			return err
		}
		seq++
		batch = make(map[string]incremental.FileInfo)
		batchBytes = 0
		return nil
	}

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
		contentBytes := int64(len(content))
		totalParsedBytes += contentBytes
		if err := CheckTotalParsedBytes(relPath, 0, totalParsedBytes, opts.MaxTotalParsedBytes); err != nil {
			return err
		}
		hash := sha256.Sum256(content)
		hashString := hex.EncodeToString(hash[:])
		batch[relPath] = incremental.FileInfo{
			Hash:    hashString,
			Content: content,
		}
		spool.currentFiles[relPath] = struct{}{}
		spool.currentHashes[relPath] = hashString
		batchBytes += contentBytes
		if len(batch) >= buildFlushFileBatchSize || batchBytes >= buildFlushParsedBytes {
			return flush()
		}
		return nil
	}); err != nil {
		spool.cleanup(s.logger())
		return nil, trace.Wrap(err, "walk update directory")
	}
	if err := flush(); err != nil {
		spool.cleanup(s.logger())
		return nil, err
	}
	return spool, nil
}

func (s *GraphService) applyUpdateSpoolInTx(ctx context.Context, txStore store.GraphStore, txDB *gorm.DB, opts UpdateOptions, spool *updateSpool) (*incremental.SyncStats, error) {
	if txStore == nil {
		return nil, trace.New("incremental update requires transaction-scoped store")
	}

	syncer := opts.Syncer
	stats := &incremental.SyncStats{}
	existingFiles, existingNodesByFile, err := existingGraphFileState(ctx, txDBForExistingFiles(txDB, s.DB))
	if err != nil {
		return nil, trace.Wrap(err, "load existing graph files")
	}
	if !opts.Replace && len(opts.IncludePaths) > 0 {
		existingFiles, existingNodesByFile = filterExistingStateByInclude(existingFiles, existingNodesByFile, opts.IncludePaths)
	}
	forceFiles, err := forceReparseFiles(ctx, txDBForExistingFiles(txDB, s.DB), existingNodesByFile, spool.currentHashes)
	if err != nil {
		return nil, trace.Wrap(err, "load edge source files for changed graph")
	}
	spool.forceFiles = forceFiles

	for _, path := range spool.records {
		record, err := spool.readRecord(path)
		if err != nil {
			return nil, err
		}
		normalFiles, _ := splitForcedFiles(record.Files, spool.forceFiles)
		if len(normalFiles) == 0 {
			continue
		}
		batchStats, err := syncIncrementalBatch(ctx, syncer, txStore, normalFiles, nil)
		if err != nil {
			return nil, trace.Wrap(err, "incremental sync")
		}
		addSyncStats(stats, batchStats)
	}

	deletedFiles := make([]string, 0, len(existingFiles))
	for _, fp := range existingFiles {
		if _, ok := spool.currentFiles[fp]; !ok {
			deletedFiles = append(deletedFiles, fp)
		}
	}
	if len(deletedFiles) > 0 {
		batchStats, err := syncIncrementalBatch(ctx, syncer, txStore, nil, deletedFiles)
		if err != nil {
			return nil, trace.Wrap(err, "incremental sync")
		}
		addSyncStats(stats, batchStats)
	}

	for _, path := range spool.records {
		record, err := spool.readRecord(path)
		if err != nil {
			return nil, err
		}
		_, forcedFiles := splitForcedFiles(record.Files, spool.forceFiles)
		if len(forcedFiles) == 0 {
			continue
		}
		batchStats, err := syncIncrementalBatch(ctx, syncer, txStore, forcedFiles, nil)
		if err != nil {
			return nil, trace.Wrap(err, "incremental force sync")
		}
		addSyncStats(stats, batchStats)
	}

	if !opts.SkipSearchRebuild && s.SearchBackend != nil && s.DB != nil {
		if txDB == nil {
			return nil, trace.New("search rebuild requires transaction DB handle")
		}
		if err := s.rebuildSearchWithDB(ctx, txDB); err != nil {
			return nil, err
		}
	}
	return stats, nil
}

func (u *updateSpool) writeRecord(seq int, record spooledUpdateRecord) error {
	path := filepath.Join(u.dir, fmt.Sprintf("%06d.gob", seq))
	file, err := os.Create(path)
	if err != nil {
		return trace.Wrap(err, "create update spool record")
	}
	encErr := gob.NewEncoder(file).Encode(record)
	closeErr := file.Close()
	if encErr != nil {
		return trace.Wrap(encErr, "encode update spool record")
	}
	if closeErr != nil {
		return trace.Wrap(closeErr, "close update spool record")
	}
	u.records = append(u.records, path)
	return nil
}

func (u *updateSpool) readRecord(path string) (spooledUpdateRecord, error) {
	var record spooledUpdateRecord
	file, err := os.Open(path)
	if err != nil {
		return record, trace.Wrap(err, "open update spool record")
	}
	decodeErr := gob.NewDecoder(file).Decode(&record)
	closeErr := file.Close()
	if decodeErr != nil {
		return record, trace.Wrap(decodeErr, "decode update spool record")
	}
	if closeErr != nil {
		return record, trace.Wrap(closeErr, "close update spool record")
	}
	return record, nil
}

func (u *updateSpool) cleanup(logger *slog.Logger) {
	if u == nil || u.dir == "" {
		return
	}
	if err := os.RemoveAll(u.dir); err != nil && logger != nil {
		logger.Warn("cleanup update spool failed", "dir", u.dir, "error", err)
	}
}

func (s *GraphService) updateGraphWithoutTx(ctx context.Context, absDir string, opts UpdateOptions) (*incremental.SyncStats, error) {
	files := make(map[string]incremental.FileInfo)
	currentHashes := make(map[string]string)
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
		hashString := hex.EncodeToString(hash[:])
		files[relPath] = incremental.FileInfo{
			Hash:    hashString,
			Content: content,
		}
		currentHashes[relPath] = hashString
		return nil
	}); err != nil {
		return nil, trace.Wrap(err, "walk update directory")
	}

	existingFiles, existingNodesByFile, err := existingGraphFileState(ctx, s.DB)
	if err != nil {
		return nil, trace.Wrap(err, "load existing graph files")
	}
	if !opts.Replace && len(opts.IncludePaths) > 0 {
		existingFiles, existingNodesByFile = filterExistingStateByInclude(existingFiles, existingNodesByFile, opts.IncludePaths)
	}
	forceFiles, err := forceReparseFiles(ctx, s.DB, existingNodesByFile, currentHashes)
	if err != nil {
		return nil, trace.Wrap(err, "load edge source files for changed graph")
	}

	normalFiles, forcedFiles := splitForcedFiles(files, forceFiles)
	stats, err := opts.Syncer.SyncWithExisting(ctx, normalFiles, existingFiles)
	if err != nil {
		return nil, trace.Wrap(err, "incremental sync")
	}
	if len(forcedFiles) > 0 {
		forcedStats, err := opts.Syncer.SyncWithExisting(ctx, forcedFiles, nil)
		if err != nil {
			return nil, trace.Wrap(err, "incremental force sync")
		}
		addSyncStats(stats, forcedStats)
	}
	if !opts.SkipSearchRebuild {
		if err := s.rebuildSearch(ctx); err != nil {
			return nil, err
		}
	}
	return stats, nil
}

func syncIncrementalBatch(ctx context.Context, syncer IncrementalSyncer, txStore store.GraphStore, files map[string]incremental.FileInfo, existingFiles []string) (*incremental.SyncStats, error) {
	if txStore != nil {
		txSyncer, ok := syncer.(transactionalIncrementalSyncer)
		if !ok {
			return nil, trace.New("incremental syncer does not support transaction-scoped store")
		}
		return txSyncer.SyncWithExistingStore(ctx, txStore, files, existingFiles)
	}
	return syncer.SyncWithExisting(ctx, files, existingFiles)
}

func addSyncStats(dst, src *incremental.SyncStats) {
	if dst == nil || src == nil {
		return
	}
	dst.Added += src.Added
	dst.Modified += src.Modified
	dst.Skipped += src.Skipped
	dst.Deleted += src.Deleted
}

func txDBForExistingFiles(txDB, db *gorm.DB) *gorm.DB {
	if txDB != nil {
		return txDB
	}
	return db
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
	filePaths, _, err := existingGraphFileState(ctx, db)
	return filePaths, err
}

func existingGraphFileState(ctx context.Context, db *gorm.DB) ([]string, map[string][]model.Node, error) {
	if db == nil {
		return nil, map[string][]model.Node{}, nil
	}

	ns := ctxns.FromContext(ctx)
	var nodes []model.Node
	if err := db.WithContext(ctx).
		Where("namespace = ?", ns).
		Find(&nodes).Error; err != nil {
		return nil, nil, err
	}
	nodesByFile := make(map[string][]model.Node)
	fileSeen := make(map[string]struct{})
	filePaths := make([]string, 0)
	for _, node := range nodes {
		nodesByFile[node.FilePath] = append(nodesByFile[node.FilePath], node)
		if _, ok := fileSeen[node.FilePath]; !ok {
			fileSeen[node.FilePath] = struct{}{}
			filePaths = append(filePaths, node.FilePath)
		}
	}
	return filePaths, nodesByFile, nil
}

func filterExistingStateByInclude(filePaths []string, nodesByFile map[string][]model.Node, includePaths []string) ([]string, map[string][]model.Node) {
	filteredFiles := make([]string, 0, len(filePaths))
	filteredNodes := make(map[string][]model.Node)
	for _, fp := range filePaths {
		if !pathutil.MatchIncludePaths(fp, includePaths) {
			continue
		}
		filteredFiles = append(filteredFiles, fp)
		filteredNodes[fp] = nodesByFile[fp]
	}
	return filteredFiles, filteredNodes
}

func forceReparseFiles(ctx context.Context, db *gorm.DB, existingNodesByFile map[string][]model.Node, currentHashes map[string]string) (map[string]struct{}, error) {
	forceFiles := make(map[string]struct{})
	if db == nil || len(existingNodesByFile) == 0 || len(currentHashes) == 0 {
		return forceFiles, nil
	}
	if !db.Migrator().HasTable(&model.Edge{}) {
		return forceFiles, nil
	}

	var changedNodeIDs []uint
	for filePath, nodes := range existingNodesByFile {
		if len(nodes) == 0 {
			continue
		}
		currentHash, stillPresent := currentHashes[filePath]
		if !stillPresent || nodes[0].Hash != currentHash {
			for _, node := range nodes {
				changedNodeIDs = append(changedNodeIDs, node.ID)
			}
		}
	}
	if len(changedNodeIDs) == 0 {
		return forceFiles, nil
	}

	ns := ctxns.FromContext(ctx)
	var edgeFiles []string
	if err := db.WithContext(ctx).
		Model(&model.Edge{}).
		Where("namespace = ? AND file_path <> '' AND (from_node_id IN ? OR to_node_id IN ?)", ns, changedNodeIDs, changedNodeIDs).
		Distinct().
		Pluck("file_path", &edgeFiles).Error; err != nil {
		return nil, err
	}
	for _, filePath := range edgeFiles {
		currentHash, stillPresent := currentHashes[filePath]
		if !stillPresent {
			continue
		}
		nodes := existingNodesByFile[filePath]
		if len(nodes) == 0 || nodes[0].Hash != currentHash {
			continue
		}
		forceFiles[filePath] = struct{}{}
	}
	return forceFiles, nil
}

func splitForcedFiles(files map[string]incremental.FileInfo, forceFiles map[string]struct{}) (map[string]incremental.FileInfo, map[string]incremental.FileInfo) {
	if len(files) == 0 {
		return nil, nil
	}
	if len(forceFiles) == 0 {
		return files, nil
	}
	normal := make(map[string]incremental.FileInfo, len(files))
	forced := make(map[string]incremental.FileInfo)
	for filePath, info := range files {
		if _, ok := forceFiles[filePath]; ok {
			info.Force = true
			forced[filePath] = info
			continue
		}
		normal[filePath] = info
	}
	return normal, forced
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
