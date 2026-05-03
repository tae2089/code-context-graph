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
	"github.com/tae2089/code-context-graph/internal/edgeresolve"
	"github.com/tae2089/code-context-graph/internal/model"
	"github.com/tae2089/code-context-graph/internal/parse"
	"github.com/tae2089/code-context-graph/internal/parse/treesitter"
	"github.com/tae2089/code-context-graph/internal/pathutil"
	"github.com/tae2089/code-context-graph/internal/store"
	"github.com/tae2089/code-context-graph/internal/store/search"
)

// parsedBuildNodeBatch carries per-file parsed nodes plus comment-binding inputs through the build pipeline.
// @intent keep node persistence and annotation binding aligned to the same source snapshot.
type parsedBuildNodeBatch struct {
	relPath     string
	nodes       []model.Node
	tsComments  []treesitter.CommentBlock
	language    string
	sourceLines []string
}

// parsedBuildEdgeBatch carries per-file parsed edges deferred until after node upserts.
// @intent persist edges only after their referenced nodes exist in the graph.
type parsedBuildEdgeBatch struct {
	relPath string
	edges   []model.Edge
}

// spooledBuildRecord is the on-disk representation of one parsed file used by the build spool.
// @intent let the build transaction stream parsed input from disk instead of holding all files in memory.
type spooledBuildRecord struct {
	RelPath     string
	Nodes       []model.Node
	Comments    []treesitter.CommentBlock
	Language    string
	SourceLines []string
	Edges       []model.Edge
	Bytes       int64
}

// buildSpool is the temporary on-disk staging area for parsed build records.
// @intent decouple parsing from the build transaction so the DB tx only opens once parsing succeeds.
type buildSpool struct {
	dir     string
	records []string
	stats   BuildStats
}

// spooledUpdateRecord is one batch of file inputs persisted before the incremental update transaction starts.
// @intent stream incremental sync inputs from disk to bound peak memory.
type spooledUpdateRecord struct {
	Files map[string]incremental.FileInfo
	Bytes int64
}

// updateSpool is the temporary on-disk staging area for an incremental update pass.
// @intent capture the current file set, hashes, and force-reparse decisions before the update transaction begins.
type updateSpool struct {
	dir           string
	records       []string
	currentFiles  map[string]struct{}
	currentHashes map[string]string
	forceFiles    map[string]struct{}
}

var (
	testBuildBatchReleaseHook func([]parsedBuildNodeBatch, int)
	resolveBuildEdges          = edgeresolve.Resolve
)

const (
	buildFlushFileBatchSize   = 100
	buildFlushParsedBytes     = 16 << 20
	buildEdgeResolveChunkSize = 400
	forceReparseEdgeChunkSize = 400
	scopedINQueryChunkSize    = 400
)

// graphFileNodeState is a minimal projection of model.Node used to discover existing graph files cheaply.
// @intent avoid loading full node rows when only id, file path, and content hash are needed.
type graphFileNodeState struct {
	ID       uint
	FilePath string
	Hash     string
}

// newParsedBuildNodeBatch packages parsed nodes plus comment metadata for later persistence.
// @intent defer comment binding until storage time while keeping per-file source line context available.
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

// newParsedBuildEdgeBatch wraps parsed edges so they can be persisted after their owning nodes.
// @intent defer edge upserts until referenced nodes exist in the graph store.
func newParsedBuildEdgeBatch(relPath string, edges []model.Edge) parsedBuildEdgeBatch {
	return parsedBuildEdgeBatch{relPath: relPath, edges: edges}
}

// bindAndReleaseNodeBatch upserts a parsed file's nodes and binds its comment annotations within a transaction-scoped store.
// @intent persist nodes and their annotation bindings atomically per file before releasing comment buffers.
// @sideEffect writes graph nodes and annotation rows via the transaction-scoped store.
// @mutates graph nodes and annotations
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

// commentParserWithLanguage is the optional contract a Parser may satisfy to expose comment blocks plus its source language.
// @intent let the build pipeline collect docstring/comment blocks alongside nodes and edges when the parser supports it.
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

// @intent allow incremental sync implementations to participate in the same transaction-scoped store used by GraphService updates.
type transactionalIncrementalSyncer interface {
	SyncWithExistingStore(ctx context.Context, syncStore incremental.Store, files map[string]incremental.FileInfo, existingFiles []string) (*incremental.SyncStats, error)
}

// @intent expose a graph-store transaction that also hands back the matching gorm DB handle for coupled search rebuilds.
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
	Syncer           IncrementalSyncer
	Replace          bool
	FailOnUnreadable bool
}

// UnreadableFilesError signals that the update path encountered files it could
// not stat or read while FailOnUnreadable was enabled.
// @intent give webhook/server callers a structured failure they can surface instead of silent partial sync
type UnreadableFilesError struct {
	Files []string
}

// Error formats the unreadable file failure with a count and a representative sample path.
// @intent give operators a stable, single-line summary they can grep instead of dumping every path.
func (e *UnreadableFilesError) Error() string {
	if e == nil || len(e.Files) == 0 {
		return "unreadable files encountered during update"
	}
	sample := e.Files[0]
	return fmt.Sprintf("unreadable files encountered during update: count=%d sample=%s", len(e.Files), sample)
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

// withBuildTx opens the appropriate transaction scope for a build, picking a DB-aware tx when search rebuild is enabled.
// @intent reuse one transaction across graph writes and the coupled search index rebuild.
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

// @intent pre-parse eligible files into spool records so the later build transaction can persist graph state from a stable snapshot.
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

// writeRecord encodes one parsed file as a gob-serialized spool record on disk.
// @intent persist parsed input for later transactional replay without holding it in memory.
// @sideEffect creates a file under the spool directory.
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

// readRecord decodes a previously-written build spool record from disk.
// @intent stream parsed input back into the build transaction one file at a time.
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

// cleanup removes the spool directory and logs a warning on failure.
// @intent reclaim spool disk space whether the build succeeded or failed.
// @sideEffect deletes the temporary spool directory.
func (b *buildSpool) cleanup(logger *slog.Logger) {
	if b == nil || b.dir == "" {
		return
	}
	if err := os.RemoveAll(b.dir); err != nil && logger != nil {
		logger.Warn("cleanup build spool failed", "dir", b.dir, "error", err)
	}
}

// applyBuildSpoolInTx replays spool records into the transaction-scoped graph store and triggers search rebuild.
// @intent rebuild the graph from scratch atomically so partial failures cannot leave stale state.
// @sideEffect resets and repopulates graph nodes/edges/annotations and may rebuild search documents.
// @mutates graph nodes, edges, annotations, and search_documents
func (s *GraphService) applyBuildSpoolInTx(ctx context.Context, txStore store.GraphStore, txDB *gorm.DB, opts BuildOptions, spool *buildSpool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := txStore.DeleteGraph(ctx); err != nil {
		return trace.Wrap(err, "reset graph state before rebuild")
	}

	batch := buildPersistBatch{}
	var edgeBatches []parsedBuildEdgeBatch
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
		}, record.Bytes)
		edgeBatches = append(edgeBatches, parsedBuildEdgeBatch{
			relPath: record.RelPath,
			edges:   record.Edges,
		})
		if batch.shouldFlush() {
			if err := s.flushBuildBatch(ctx, txStore, &batch); err != nil {
				return err
			}
		}
	}
	if err := s.flushBuildBatch(ctx, txStore, &batch); err != nil {
		return err
	}
	if err := s.flushBuildEdges(ctx, txStore, edgeBatches); err != nil {
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

// buildPersistBatch accumulates parsed file batches until a flush threshold is reached.
// @intent amortize transaction overhead by persisting groups of files together while bounding memory.
type buildPersistBatch struct {
	nodeBatches []parsedBuildNodeBatch
	files       int
	bytes       int64
}

// add appends one parsed file's nodes to the in-flight build batch and tracks size.
// @intent accumulate work between flushes so persistence happens in bounded chunks.
// @mutates batch.nodeBatches, batch.files, batch.bytes
func (b *buildPersistBatch) add(nodeBatch parsedBuildNodeBatch, parsedBytes int64) {
	b.nodeBatches = append(b.nodeBatches, nodeBatch)
	b.files++
	b.bytes += parsedBytes
}

// shouldFlush reports whether the batch reached the file count or parsed byte threshold.
// @intent bound transaction size so long builds do not balloon memory or transaction logs.
func (b *buildPersistBatch) shouldFlush() bool {
	return b.files >= buildFlushFileBatchSize || b.bytes >= buildFlushParsedBytes
}

// reset clears the batch for reuse after a successful flush.
// @intent recycle the batch struct without reallocating to keep build loops allocation-light.
// @mutates batch.nodeBatches, batch.files, batch.bytes
func (b *buildPersistBatch) reset() {
	b.nodeBatches = nil
	b.files = 0
	b.bytes = 0
}

// flushBuildBatch persists the buffered nodes for the current batch.
// @intent persist nodes before all edges so foreign-key style references can resolve.
// @sideEffect upserts graph nodes and annotations through the transaction-scoped store.
// @mutates graph nodes and annotations
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

	batch.reset()
	return nil
}

// flushBuildEdges resolves and persists all deferred edges after graph nodes exist.
// @intent attach parsed relationships to stored node IDs without depending on build batch order.
// @sideEffect upserts graph edges through the transaction-scoped store.
// @mutates graph edges
func (s *GraphService) flushBuildEdges(ctx context.Context, txStore store.GraphStore, edgeBatches []parsedBuildEdgeBatch) error {
	implementsEdges, otherBatches := partitionBuildEdges(edgeBatches)
	for start := 0; start < len(implementsEdges); start += buildEdgeResolveChunkSize {
		if err := ctx.Err(); err != nil {
			return err
		}
		end := min(start+buildEdgeResolveChunkSize, len(implementsEdges))
		resolved, err := resolveBuildEdges(ctx, txStore, implementsEdges[start:end])
		if err != nil {
			return trace.Wrap(err, "resolve deferred implements edges")
		}
		if err := txStore.UpsertEdges(ctx, resolved); err != nil {
			return trace.Wrap(err, "upsert deferred implements edges")
		}
	}

	for _, parsed := range otherBatches {
		if err := ctx.Err(); err != nil {
			return err
		}
		for start := 0; start < len(parsed.edges); start += buildEdgeResolveChunkSize {
			end := min(start+buildEdgeResolveChunkSize, len(parsed.edges))
			chunk := parsed.edges[start:end]
			resolveInput := append(append([]model.Edge(nil), implementsEdges...), chunk...)
			resolved, err := resolveBuildEdges(ctx, txStore, resolveInput)
			if err != nil {
				return trace.Wrap(err, "resolve deferred edges for "+parsed.relPath)
			}
			if err := txStore.UpsertEdges(ctx, resolved[len(implementsEdges):]); err != nil {
				return trace.Wrap(err, "upsert deferred edges for "+parsed.relPath)
			}
		}
	}
	return nil
}

// partitionBuildEdges keeps implements edges available before resolving call edges in later bounded chunks.
// @intent preserve Go interface dispatch resolution after build edge resolution starts streaming by file.
func partitionBuildEdges(edgeBatches []parsedBuildEdgeBatch) ([]model.Edge, []parsedBuildEdgeBatch) {
	var implementsEdges []model.Edge
	otherBatches := make([]parsedBuildEdgeBatch, 0, len(edgeBatches))
	otherByPath := make(map[string][]model.Edge, len(edgeBatches))
	var paths []string
	for _, parsed := range edgeBatches {
		parsedImplements, otherEdges := splitImplementsEdges(parsed.edges)
		implementsEdges = append(implementsEdges, parsedImplements...)
		if len(otherEdges) > 0 {
			otherByPath[parsed.relPath] = append(otherByPath[parsed.relPath], otherEdges...)
			paths = append(paths, parsed.relPath)
		}
	}
	for _, path := range paths {
		otherBatches = append(otherBatches, parsedBuildEdgeBatch{relPath: path, edges: otherByPath[path]})
	}
	return implementsEdges, otherBatches
}

func splitImplementsEdges(edges []model.Edge) ([]model.Edge, []model.Edge) {
	var implementsEdges []model.Edge
	var otherEdges []model.Edge
	for _, edge := range edges {
		if edge.Kind == model.EdgeKindImplements {
			implementsEdges = append(implementsEdges, edge)
			continue
		}
		otherEdges = append(otherEdges, edge)
	}
	return implementsEdges, otherEdges
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

// withUpdateTx selects the right transaction scope for incremental update based on syncer and store capability.
// @intent prefer a single coupled tx for graph and search rebuild while gracefully degrading when the syncer or store cannot participate.
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

// @intent capture the current update input set and file hashes before transactional incremental sync begins.
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
	unreadable := unreadableFileSummary{}

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
			unreadable.add(relPath)
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
			unreadable.add(relPath)
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
	unreadable.log(s.logger(), "update")
	if opts.FailOnUnreadable {
		if errUnreadable := unreadable.asError(); errUnreadable != nil {
			spool.cleanup(s.logger())
			return nil, errUnreadable
		}
	}
	return spool, nil
}

// applyUpdateSpoolInTx replays the update spool through the incremental syncer and refreshes affected search docs.
// @intent stage normal-changed files first, then deletions, then forced reparses so edge-source files always observe up-to-date nodes.
// @sideEffect adds, modifies, and deletes graph nodes/edges/annotations for changed files and refreshes affected search documents.
// @mutates graph nodes, edges, annotations, and search_documents
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
		nodeIDs, err := affectedNodeIDsForUpdate(ctx, txDB, existingNodesByFile, affectedUpdateFiles(spool.currentHashes, existingNodesByFile, spool.forceFiles), deletedFiles)
		if err != nil {
			return nil, trace.Wrap(err, "load affected search nodes")
		}
		if err := s.rebuildSearchNodesWithDB(ctx, txDB, nodeIDs); err != nil {
			return nil, err
		}
	}
	return stats, nil
}

// writeRecord encodes one batch of update inputs as a gob-serialized spool record on disk.
// @intent persist update inputs for transactional replay without holding all batches in memory.
// @sideEffect creates a file under the spool directory.
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

// readRecord decodes a previously-written update spool record from disk.
// @intent stream update inputs back into the update transaction in batches.
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

// cleanup removes the update spool directory and logs a warning on failure.
// @intent reclaim spool disk space whether the update succeeded or failed.
// @sideEffect deletes the temporary spool directory.
func (u *updateSpool) cleanup(logger *slog.Logger) {
	if u == nil || u.dir == "" {
		return
	}
	if err := os.RemoveAll(u.dir); err != nil && logger != nil {
		logger.Warn("cleanup update spool failed", "dir", u.dir, "error", err)
	}
}

// @intent run incremental sync without a shared DB transaction when the configured syncer or store cannot provide one.
func (s *GraphService) updateGraphWithoutTx(ctx context.Context, absDir string, opts UpdateOptions) (*incremental.SyncStats, error) {
	files := make(map[string]incremental.FileInfo)
	currentHashes := make(map[string]string)
	var totalParsedBytes int64
	unreadable := unreadableFileSummary{}
	if err := walkMatchingFiles(ctx, absDir, opts.BuildOptions, func(path, relPath string) error {
		if _, ok := s.parserForExt(strings.ToLower(filepath.Ext(path))); !ok {
			return nil
		}

		info, err := os.Stat(path)
		if err != nil {
			unreadable.add(relPath)
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
			unreadable.add(relPath)
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
	unreadable.log(s.logger(), "update")
	if opts.FailOnUnreadable {
		if errUnreadable := unreadable.asError(); errUnreadable != nil {
			return nil, errUnreadable
		}
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
		nodeIDs, err := affectedNodeIDsForUpdate(ctx, s.DB, existingNodesByFile, affectedUpdateFiles(currentHashes, existingNodesByFile, forceFiles), existingFilesMissingFrom(files, existingFiles))
		if err != nil {
			return nil, trace.Wrap(err, "load affected search nodes")
		}
		if err := s.rebuildSearchNodes(ctx, nodeIDs); err != nil {
			return nil, err
		}
	}
	return stats, nil
}

// affectedUpdateFiles selects files whose stored hash differs from the current input or that are forced to reparse.
// @intent identify which files contributed nodes that need to be re-indexed for search after an incremental update.
func affectedUpdateFiles(currentHashes map[string]string, existingNodesByFile map[string][]model.Node, forceFiles map[string]struct{}) []string {
	files := make([]string, 0)
	for filePath, hash := range currentHashes {
		existing := existingNodesByFile[filePath]
		_, forced := forceFiles[filePath]
		if forced || len(existing) == 0 || existing[0].Hash != hash {
			files = append(files, filePath)
		}
	}
	return files
}

// existingFilesMissingFrom returns paths that were previously stored but are no longer present on disk.
// @intent identify deletions so the incremental syncer can remove their nodes and edges.
func existingFilesMissingFrom(files map[string]incremental.FileInfo, existingFiles []string) []string {
	deleted := make([]string, 0)
	for _, fp := range existingFiles {
		if _, ok := files[fp]; !ok {
			deleted = append(deleted, fp)
		}
	}
	return deleted
}

// affectedNodeIDsForUpdate collects node IDs whose search documents must be refreshed for a given change set.
// @intent merge previously stored node IDs with newly created ones so the search index sees both removals and additions.
func affectedNodeIDsForUpdate(ctx context.Context, db *gorm.DB, existingNodesByFile map[string][]model.Node, changedFiles, deletedFiles []string) ([]uint, error) {
	seen := make(map[uint]struct{})
	add := func(id uint) {
		if id != 0 {
			seen[id] = struct{}{}
		}
	}
	for _, fp := range changedFiles {
		for _, node := range existingNodesByFile[fp] {
			add(node.ID)
		}
	}
	for _, fp := range deletedFiles {
		for _, node := range existingNodesByFile[fp] {
			add(node.ID)
		}
	}
	currentIDs, err := currentNodeIDsForFiles(ctx, db, changedFiles)
	if err != nil {
		return nil, err
	}
	for _, id := range currentIDs {
		add(id)
	}
	ids := make([]uint, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	return ids, nil
}

// currentNodeIDsForFiles loads node IDs for the given file paths in the active namespace using chunked IN queries.
// @intent avoid SQL parameter limits while collecting node IDs that need search index refresh.
func currentNodeIDsForFiles(ctx context.Context, db *gorm.DB, filePaths []string) ([]uint, error) {
	if db == nil || len(filePaths) == 0 {
		return nil, nil
	}
	ns := ctxns.FromContext(ctx)
	var ids []uint
	for start := 0; start < len(filePaths); start += scopedINQueryChunkSize {
		end := min(start+scopedINQueryChunkSize, len(filePaths))
		chunk := filePaths[start:end]
		var chunkIDs []uint
		if err := db.WithContext(ctx).Model(&model.Node{}).Where("namespace = ? AND file_path IN ?", ns, chunk).Pluck("id", &chunkIDs).Error; err != nil {
			return nil, err
		}
		ids = append(ids, chunkIDs...)
	}
	return ids, nil
}

// syncIncrementalBatch dispatches one batch to the configured incremental syncer using a transaction store when available.
// @intent route changes through the transactional syncer so all updates land in the same DB transaction as graph writes.
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

// addSyncStats sums batch-level sync counters into a running total.
// @intent let the update loop aggregate per-batch results without each call site touching every field.
// @mutates dst.Added, dst.Modified, dst.Skipped, dst.Deleted
func addSyncStats(dst, src *incremental.SyncStats) {
	if dst == nil || src == nil {
		return
	}
	dst.Added += src.Added
	dst.Modified += src.Modified
	dst.Skipped += src.Skipped
	dst.Deleted += src.Deleted
}

// txDBForExistingFiles picks the transaction-scoped DB if available, otherwise falls back to the base handle.
// @intent keep existing-file lookups inside the same transaction as graph writes when one is active.
func txDBForExistingFiles(txDB, db *gorm.DB) *gorm.DB {
	if txDB != nil {
		return txDB
	}
	return db
}

// logger returns the configured slog.Logger or the process default when none was supplied.
// @intent keep service code logging-safe even when callers leave Logger nil.
func (s *GraphService) logger() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}

// parserForExt resolves a Parser for the given file extension, preferring an explicit Parsers map over Walkers.
// @intent let tests inject custom parsers while still using the production walker registry by default.
func (s *GraphService) parserForExt(ext string) (Parser, bool) {
	if s.Parsers != nil {
		parser, ok := s.Parsers[ext]
		return parser, ok
	}
	parser, ok := s.Walkers[ext]
	return parser, ok
}

// @intent rebuild the namespace-scoped search index after graph mutations when a search backend is configured.
func (s *GraphService) rebuildSearch(ctx context.Context) error {
	if s.SearchBackend == nil || s.DB == nil {
		return nil
	}
	return s.rebuildSearchWithDB(ctx, s.DB)
}

// rebuildSearchNodes refreshes search documents for the given node IDs when a search backend is configured.
// @intent perform incremental search index updates after changed-node sets are known.
func (s *GraphService) rebuildSearchNodes(ctx context.Context, nodeIDs []uint) error {
	if s.SearchBackend == nil || s.DB == nil {
		return nil
	}
	return s.rebuildSearchNodesWithDB(ctx, s.DB, nodeIDs)
}

// rebuildSearchWithDB refreshes all search documents and rebuilds the backend index against the supplied DB handle.
// @intent let build paths share one transaction across search document refresh and FTS rebuild.
// @sideEffect rewrites search_documents and rebuilds the search backend index.
// @mutates search_documents
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

// rebuildSearchNodesWithDB refreshes search documents for the given node IDs and updates the backend index scope.
// @intent keep the FTS index incrementally consistent with the latest changed nodes.
// @sideEffect rewrites the affected search_documents rows and updates the search backend.
// @mutates search_documents
func (s *GraphService) rebuildSearchNodesWithDB(ctx context.Context, db *gorm.DB, nodeIDs []uint) error {
	if s.SearchBackend == nil || db == nil || len(nodeIDs) == 0 {
		return nil
	}
	docCount, err := RefreshSearchDocumentsFor(ctx, db, nodeIDs)
	if err != nil {
		return err
	}
	if err := s.SearchBackend.RebuildNodes(ctx, db, nodeIDs); err != nil {
		return trace.Wrap(err, "rebuild scoped search index")
	}
	s.logger().Info("search index partially rebuilt", "documents", docCount, "nodes", len(nodeIDs))
	return nil
}

// @intent walk candidate source files once while applying recursion, exclude, and include-path policy before parsing.
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

// parseForBuild parses one source file using the comment-aware parser when available.
// @intent surface comment blocks and language alongside nodes/edges so the binder can attach annotations.
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

// existingGraphFileState loads the namespace-scoped current graph state grouped by file path.
// @intent provide both deletion-scope file paths and per-file node projections from a single query.
func existingGraphFileState(ctx context.Context, db *gorm.DB) ([]string, map[string][]model.Node, error) {
	if db == nil {
		return nil, map[string][]model.Node{}, nil
	}

	ns := ctxns.FromContext(ctx)
	var nodes []graphFileNodeState
	if err := db.WithContext(ctx).
		Model(&model.Node{}).
		Select("id", "file_path", "hash").
		Where("namespace = ?", ns).
		Find(&nodes).Error; err != nil {
		return nil, nil, err
	}
	nodesByFile := make(map[string][]model.Node)
	fileSeen := make(map[string]struct{})
	filePaths := make([]string, 0)
	for _, node := range nodes {
		minimalNode := model.Node{ID: node.ID, FilePath: node.FilePath, Hash: node.Hash}
		nodesByFile[node.FilePath] = append(nodesByFile[node.FilePath], minimalNode)
		if _, ok := fileSeen[node.FilePath]; !ok {
			fileSeen[node.FilePath] = struct{}{}
			filePaths = append(filePaths, node.FilePath)
		}
	}
	return filePaths, nodesByFile, nil
}

// filterExistingStateByInclude restricts existing graph state to file paths that match the include filter.
// @intent prevent partial-scope updates from deleting files that live outside the requested include paths.
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

// forceReparseFiles finds files whose edges reference nodes from changed files and therefore must be reparsed.
// @intent keep cross-file edges consistent by reparsing edge-source files when their referenced nodes change.
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
	edgeFileSeen := make(map[string]struct{})
	for start := 0; start < len(changedNodeIDs); start += forceReparseEdgeChunkSize {
		end := min(start+forceReparseEdgeChunkSize, len(changedNodeIDs))
		chunk := changedNodeIDs[start:end]
		var chunkFiles []string
		if err := db.WithContext(ctx).
			Model(&model.Edge{}).
			Where("namespace = ? AND file_path <> '' AND (from_node_id IN ? OR to_node_id IN ?)", ns, chunk, chunk).
			Distinct().
			Pluck("file_path", &chunkFiles).Error; err != nil {
			return nil, err
		}
		for _, filePath := range chunkFiles {
			edgeFileSeen[filePath] = struct{}{}
		}

		var relatedImplements []model.Edge
		if err := db.WithContext(ctx).
			Model(&model.Edge{}).
			Where("namespace = ? AND kind = ? AND file_path <> '' AND (from_node_id IN ? OR to_node_id IN ?)", ns, model.EdgeKindImplements, chunk, chunk).
			Find(&relatedImplements).Error; err != nil {
			return nil, err
		}
		var relatedTypeIDs []uint
		seenTypeID := make(map[uint]struct{}, len(relatedImplements)*2)
		for _, edge := range relatedImplements {
			for _, id := range []uint{edge.FromNodeID, edge.ToNodeID} {
				if id == 0 {
					continue
				}
				if _, ok := seenTypeID[id]; ok {
					continue
				}
				seenTypeID[id] = struct{}{}
				relatedTypeIDs = append(relatedTypeIDs, id)
			}
		}
		for relatedStart := 0; relatedStart < len(relatedTypeIDs); relatedStart += forceReparseEdgeChunkSize {
			relatedEnd := min(relatedStart+forceReparseEdgeChunkSize, len(relatedTypeIDs))
			relatedChunk := relatedTypeIDs[relatedStart:relatedEnd]
			var dispatchFiles []string
			if err := db.WithContext(ctx).
				Model(&model.Edge{}).
				Where("namespace = ? AND kind = ? AND file_path <> '' AND (from_node_id IN ? OR to_node_id IN ?)", ns, model.EdgeKindCalls, relatedChunk, relatedChunk).
				Distinct().
				Pluck("file_path", &dispatchFiles).Error; err != nil {
				return nil, err
			}
			for _, filePath := range dispatchFiles {
				edgeFileSeen[filePath] = struct{}{}
			}
		}
	}
	for filePath := range edgeFileSeen {
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

// splitForcedFiles partitions inputs into normal and forced-reparse buckets and marks the forced ones.
// @intent process unchanged-hash forced files separately so the syncer can bypass its hash short-circuit.
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

// unreadableFileSummary aggregates files that could not be stat-ed or read during a build or update pass.
// @intent let callers surface a single structured failure or warning instead of one log entry per file.
type unreadableFileSummary struct {
	count  int
	sample string
	files  []string
}

// add records one more unreadable file, keeping the first occurrence as the sample.
// @intent collect every offending path while keeping summary output bounded for logs.
// @mutates s.count, s.sample, s.files
func (s *unreadableFileSummary) add(relPath string) {
	s.count++
	if s.sample == "" {
		s.sample = relPath
	}
	s.files = append(s.files, relPath)
}

// log emits a single warning describing how many files were skipped during a phase.
// @intent prevent log spam by collapsing per-file warnings into one phase-tagged entry.
// @sideEffect writes a warn-level log entry when the summary is non-empty.
func (s unreadableFileSummary) log(logger *slog.Logger, phase string) {
	if s.count == 0 || logger == nil {
		return
	}
	logger.Warn("skipped unreadable files", "phase", phase, "count", s.count, "sample", s.sample)
}

// asError converts the summary into an UnreadableFilesError when at least one file failed.
// @intent let callers escalate skipped reads into a structured failure when FailOnUnreadable is set.
func (s unreadableFileSummary) asError() error {
	if s.count == 0 {
		return nil
	}
	files := append([]string(nil), s.files...)
	return &UnreadableFilesError{Files: files}
}

// @intent reject individual files that exceed the configured per-file parse budget before loading them into memory.
func CheckParseFileSize(relPath string, size int64, maxFileBytes int64) error {
	if maxFileBytes > 0 && size > maxFileBytes {
		return fmt.Errorf("parse file %s exceeds max file bytes: %d > %d", relPath, size, maxFileBytes)
	}
	return nil
}

// @intent stop one build or update pass once cumulative parsed bytes would exceed the configured safety limit.
func CheckTotalParsedBytes(relPath string, current int64, next int64, maxTotalBytes int64) error {
	if maxTotalBytes > 0 && current+next > maxTotalBytes {
		return fmt.Errorf("parse file %s exceeds max total parsed bytes: %d > %d", relPath, current+next, maxTotalBytes)
	}
	return nil
}

// RefreshSearchDocuments rebuilds namespace-scoped search_documents from current graph nodes.
// @intent keep derived search documents consistent with graph state before FTS rebuilds
func RefreshSearchDocuments(ctx context.Context, db *gorm.DB) (int, error) {
	return refreshSearchDocuments(ctx, db, nil, false)
}

// RefreshSearchDocumentsFor rebuilds search_documents for the specified node IDs only.
// @intent incremental update 경로에서 영향받은 문서만 갱신한다.
func RefreshSearchDocumentsFor(ctx context.Context, db *gorm.DB, nodeIDs []uint) (int, error) {
	if len(nodeIDs) == 0 {
		return 0, nil
	}
	return refreshSearchDocuments(ctx, db, nodeIDs, true)
}

// refreshSearchDocuments rebuilds the namespace-scoped search_documents table either fully or for a node-id scope.
// @intent regenerate FTS content from the latest nodes and annotations in batches to bound memory.
// @sideEffect deletes and re-inserts rows in search_documents within a DB transaction.
// @mutates search_documents
func refreshSearchDocuments(ctx context.Context, db *gorm.DB, nodeIDs []uint, scoped bool) (int, error) {
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
		if scoped {
			for start := 0; start < len(nodeIDs); start += scopedINQueryChunkSize {
				chunk := scopedNodeIDsForChunk(nodeIDs, start)
				if err := tx.WithContext(ctx).Where("namespace = ?", ns).Where("node_id IN ?", chunk).Delete(&model.SearchDocument{}).Error; err != nil {
					return trace.Wrap(err, "clear search documents")
				}
			}
		} else {
			if err := docsQ.Delete(&model.SearchDocument{}).Error; err != nil {
				return trace.Wrap(err, "clear search documents")
			}
		}

		loadNodes := func(query *gorm.DB) error {
			var batchNodes []model.Node
			result := query.FindInBatches(&batchNodes, 500, func(batchTx *gorm.DB, batch int) error {
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
		}

		if scoped {
			for start := 0; start < len(nodeIDs); start += scopedINQueryChunkSize {
				chunk := scopedNodeIDsForChunk(nodeIDs, start)
				chunkNodesQ := tx.WithContext(ctx).
					Where("kind IN ?", []string{"function", "class", "type", "test", "file"}).
					Where("namespace = ?", ns).
					Where("id IN ?", chunk)
				if err := loadNodes(chunkNodesQ); err != nil {
					return err
				}
			}
			return nil
		}
		return loadNodes(nodesQ)
	})
	if err != nil {
		return 0, err
	}
	return count, nil
}

// scopedNodeIDsForChunk slices a node ID list using the configured IN-query chunk size.
// @intent keep search rebuild SQL within the SQLite/Postgres parameter limit.
func scopedNodeIDsForChunk(nodeIDs []uint, start int) []uint {
	end := min(start+scopedINQueryChunkSize, len(nodeIDs))
	return nodeIDs[start:end]
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
