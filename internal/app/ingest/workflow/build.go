// @index Full graph build pipeline: spool, batch persistence, and edge resolution.
package workflow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tae2089/trace"

	ingestapp "github.com/tae2089/code-context-graph/internal/app/ingest"
	"github.com/tae2089/code-context-graph/internal/app/ingest/binding"
	"github.com/tae2089/code-context-graph/internal/app/ingest/resolve"
	"github.com/tae2089/code-context-graph/internal/domain/graph"
	"github.com/tae2089/code-context-graph/internal/obs"
)

// parsedBuildNodeBatch carries per-file parsed nodes plus comment-binding inputs through the build pipeline.
// @intent keep node persistence and annotation binding aligned to the same source snapshot.
type parsedBuildNodeBatch struct {
	relPath     string
	nodes       []graph.Node
	packageName string
	interfaces  []ingestapp.PackageInterfaceInfo
	tsComments  []ingestapp.CommentBlock
	language    string
	sourceLines []string
}

// parsedBuildEdgeBatch carries per-file parsed edges deferred until after node upserts.
// @intent persist edges only after their referenced nodes exist in the graph.
type parsedBuildEdgeBatch struct {
	relPath string
	edges   []graph.Edge
}

// buildEdgeBatchSource replays deferred edge batches from the beginning on every invocation.
// @intent let edge resolution make ordered passes over either in-memory batches or disk-backed build records.
type buildEdgeBatchSource func(yield func(parsedBuildEdgeBatch) error) error

// buildParseInput is one validated source file assigned to a parse worker.
// @intent keep deterministic input sequencing separate from concurrent filesystem and parser work.
type buildParseInput struct {
	seq         int
	path        string
	relPath     string
	parser      Parser
	contextHash string
}

// buildParseResult is one parsed source file returned to the spool coordinator.
// @intent let workers finish out of order while the coordinator preserves record order.
type buildParseResult struct {
	seq       int
	record    spooledBuildRecord
	err       error
	cacheHit  bool
	cacheMiss bool
}

// newParsedBuildNodeBatch packages parsed nodes plus comment metadata for later persistence.
// @intent defer comment binding until storage time while keeping per-file source line context available.
func newParsedBuildNodeBatch(relPath string, content []byte, nodes []graph.Node, packageName string, interfaces []ingestapp.PackageInterfaceInfo, tsComments []ingestapp.CommentBlock, language string) parsedBuildNodeBatch {
	out := parsedBuildNodeBatch{
		relPath:     relPath,
		nodes:       nodes,
		packageName: packageName,
		interfaces:  interfaces,
		tsComments:  tsComments,
		language:    language,
	}
	if len(tsComments) > 0 {
		out.sourceLines = strings.Split(string(content), "\n")
	}
	return out
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

// bindAndReleaseNodeBatch binds a parsed file's comments after its nodes have persisted, then releases comment buffers.
// @intent preserve per-file annotation binding and release behavior after the enclosing flush persists all nodes together.
// @sideEffect writes annotation rows via the transaction-scoped store.
// @mutates graph annotations
func (s *Service) bindAndReleaseNodeBatch(ctx context.Context, txStore ingestapp.GraphStore, storedNodesByFile map[string][]graph.Node, batches []parsedBuildNodeBatch, idx int) error {
	parsed := &batches[idx]

	if len(parsed.tsComments) > 0 {
		binderComments := toBinderComments(parsed.tsComments)
		binder := binding.NewBinder()
		bindings := binder.Bind(binderComments, parsed.nodes, parsed.language, parsed.sourceLines)

		storedNodes := storedNodesByFile[parsed.relPath]
		storedMap := make(map[string]*graph.Node, len(storedNodes))
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
	if s.onBatchRelease != nil {
		s.onBatchRelease(batches, idx)
	}
	return nil
}

// Build walks source files, stores parsed graph data, and rebuilds search docs.
// @intent perform a full graph build from the specified directory.
// @sideEffect 파일 시스템을 읽고 그래프 저장소·DB·검색 인덱스를 갱신한다.
func (s *Service) Build(ctx context.Context, opts BuildOptions) (BuildStats, error) {
	var stats BuildStats
	totalStart := time.Now()

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

	packages := s.collectLanguagePackages(ctx, absDir, opts)
	ctx = s.withImportPackageContext(ctx, packages)

	spool, err := s.prepareBuildSpool(ctx, absDir, opts)
	if err != nil {
		return stats, err
	}
	defer spool.cleanup(s.logger())
	spool.packages = packages
	stats = spool.stats
	stats.TotalNodes += len(packageNodes(packages))
	stats.TotalEdges += packageContainsEdgeCount(packages)
	stats.Timing.ParseMS = time.Since(totalStart).Milliseconds()

	err = s.withBuildTx(ctx, func(tx ingestapp.Transaction) error {
		return s.applyBuildSpoolInTx(ctx, tx, opts, spool, &stats.Timing)
	})
	if err != nil {
		return stats, err
	}

	stats.Timing.TotalMS = time.Since(totalStart).Milliseconds()
	s.logger().Info("build complete",
		"files", stats.TotalFiles,
		"nodes", stats.TotalNodes,
		"edges", stats.TotalEdges,
		"unresolved_edges", stats.Unresolved.DroppedCount,
		"unresolved_by_kind", formatEdgeKindCounts(stats.Unresolved.ByKind),
		"unresolved_by_file", stats.Unresolved.ByFile,
		"unresolved_by_reason", stats.Unresolved.ByReason,
		"unresolved_samples", stats.Unresolved.Samples,
	)
	s.logger().Debug("build timing",
		"parse_ms", stats.Timing.ParseMS,
		"parse_cache_hits", stats.Timing.ParseCacheHits,
		"parse_cache_misses", stats.Timing.ParseCacheMisses,
		"persist_nodes_ms", stats.Timing.PersistNodesMS,
		"resolve_edges_ms", stats.Timing.ResolveEdgesMS,
		"resolver_calls", stats.Timing.Resolve.Resolver.Calls,
		"resolver_ms", stats.Timing.Resolve.Resolver.MS,
		"resolve_nodes_by_ids_calls", stats.Timing.Resolve.NodesByIDs.Calls,
		"resolve_nodes_by_ids_ms", stats.Timing.Resolve.NodesByIDs.MS,
		"resolve_nodes_by_files_calls", stats.Timing.Resolve.NodesByFiles.Calls,
		"resolve_nodes_by_files_ms", stats.Timing.Resolve.NodesByFiles.MS,
		"resolve_nodes_by_qn_calls", stats.Timing.Resolve.NodesByQualifiedNames.Calls,
		"resolve_nodes_by_qn_ms", stats.Timing.Resolve.NodesByQualifiedNames.MS,
		"resolve_import_file_nodes_calls", stats.Timing.Resolve.ImportFileNodes.Calls,
		"resolve_import_file_nodes_ms", stats.Timing.Resolve.ImportFileNodes.MS,
		"resolve_edges_to_nodes_calls", stats.Timing.Resolve.EdgesToNodes.Calls,
		"resolve_edges_to_nodes_ms", stats.Timing.Resolve.EdgesToNodes.MS,
		"resolve_edge_upsert_calls", stats.Timing.Resolve.UpsertEdges.Calls,
		"resolve_edge_upsert_ms", stats.Timing.Resolve.UpsertEdges.MS,
		"search_rebuild_ms", stats.Timing.SearchRebuildMS,
		"total_ms", stats.Timing.TotalMS,
	)

	return stats, nil
}

// withBuildTx opens the application-owned unit-of-work boundary for a build.
// @intent reuse one transaction across graph writes and the coupled search index rebuild.
func (s *Service) withBuildTx(ctx context.Context, fn func(ingestapp.Transaction) error) error {
	if s.UnitOfWork == nil {
		return trace.New("ingest unit of work is not configured")
	}
	return s.UnitOfWork.WithinTransaction(ctx, fn)
}

// @intent pre-parse eligible files into spool records so the later build transaction can persist graph state from a stable snapshot.
func (s *Service) prepareBuildSpool(ctx context.Context, absDir string, opts BuildOptions) (*buildSpool, error) {
	dir, err := os.MkdirTemp("", "ccg-build-spool-*")
	if err != nil {
		return nil, trace.Wrap(err, "create build spool")
	}
	spool := &buildSpool{dir: dir}
	inputs, err := s.collectBuildParseInputs(ctx, absDir, opts, parseSemanticContextHash(ctx))
	if err != nil {
		spool.cleanup(s.logger())
		return nil, err
	}

	var totalParsedBytes int64
	err = s.parseBuildInputs(ctx, inputs, func(result buildParseResult) error {
		if err := CheckTotalParsedBytes(result.record.RelPath, 0, totalParsedBytes+result.record.Bytes, opts.MaxTotalParsedBytes); err != nil {
			return err
		}
		totalParsedBytes += result.record.Bytes
		if err := spool.writeRecord(result.seq, result.record); err != nil {
			return err
		}
		spool.stats.TotalFiles++
		spool.stats.TotalNodes += len(result.record.Nodes)
		spool.stats.TotalEdges += len(result.record.Edges)
		if result.cacheHit {
			spool.stats.Timing.ParseCacheHits++
		}
		if result.cacheMiss {
			spool.stats.Timing.ParseCacheMisses++
		}
		return nil
	})
	if err != nil {
		spool.cleanup(s.logger())
		return nil, err
	}

	return spool, nil
}

// collectBuildParseInputs walks eligible files once and validates their scheduled parse budget.
// @intent preserve build traversal policy and deterministic file order before concurrent parsing starts.
func (s *Service) collectBuildParseInputs(ctx context.Context, absDir string, opts BuildOptions, contextHash string) ([]buildParseInput, error) {
	var inputs []buildParseInput
	var scheduledParsedBytes int64
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
		if err := CheckTotalParsedBytes(relPath, scheduledParsedBytes, info.Size(), opts.MaxTotalParsedBytes); err != nil {
			return err
		}
		scheduledParsedBytes += info.Size()
		inputs = append(inputs, buildParseInput{
			seq:         len(inputs),
			path:        path,
			relPath:     relPath,
			parser:      parser,
			contextHash: contextHash,
		})
		return nil
	}); err != nil {
		return nil, trace.Wrap(err, "walk build directory")
	}
	return inputs, nil
}

// parseBuildInputs runs a bounded worker pool and emits successful records in input order.
// @intent parallelize CPU-bound parsing without changing spool order or retaining every parsed file in memory.
// @sideEffect reads source files and invokes parser implementations concurrently.
func (s *Service) parseBuildInputs(ctx context.Context, inputs []buildParseInput, emit func(buildParseResult) error) error {
	if len(inputs) == 0 {
		return nil
	}

	workerCount := min(buildParseWorkerCount, len(inputs))
	workerCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan buildParseInput)
	results := make(chan buildParseResult, workerCount)
	var workers sync.WaitGroup
	var stopOnce sync.Once
	for range workerCount {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for {
				select {
				case <-workerCtx.Done():
					return
				case input, ok := <-jobs:
					if !ok {
						return
					}
					result := s.parseBuildInput(workerCtx, input)
					select {
					case results <- result:
					case <-workerCtx.Done():
						return
					}
				}
			}
		}()
	}

	stopWorkers := func() {
		stopOnce.Do(func() {
			cancel()
			close(jobs)
			workers.Wait()
		})
	}
	defer stopWorkers()

	nextSubmit := 0
	nextEmit := 0
	inFlight := 0
	pending := make(map[int]buildParseResult, workerCount)
	submitUntilWindowFull := func() error {
		for nextSubmit < len(inputs) && nextSubmit-nextEmit < workerCount {
			select {
			case jobs <- inputs[nextSubmit]:
				nextSubmit++
				inFlight++
			case <-workerCtx.Done():
				if err := ctx.Err(); err != nil {
					return err
				}
				return trace.New("build parse workers stopped")
			}
		}
		return nil
	}
	if err := submitUntilWindowFull(); err != nil {
		stopWorkers()
		return err
	}

	for inFlight > 0 {
		select {
		case <-workerCtx.Done():
			stopWorkers()
			if err := ctx.Err(); err != nil {
				return err
			}
			return trace.New("build parse workers stopped")
		case result := <-results:
			inFlight--
			pending[result.seq] = result
			for {
				next, ok := pending[nextEmit]
				if !ok {
					break
				}
				delete(pending, nextEmit)
				if next.err != nil {
					stopWorkers()
					return next.err
				}
				if err := emit(next); err != nil {
					stopWorkers()
					return err
				}
				nextEmit++
			}
			if err := submitUntilWindowFull(); err != nil {
				stopWorkers()
				return err
			}
		}
	}

	if nextEmit != len(inputs) {
		stopWorkers()
		return trace.New("build parse workers completed without all records")
	}
	return nil
}

// parseBuildInput reads and parses one source file for the spool coordinator.
// @intent keep each worker's filesystem, parser, and hash work isolated from shared build state.
func (s *Service) parseBuildInput(ctx context.Context, input buildParseInput) buildParseResult {
	result := buildParseResult{seq: input.seq}
	if err := ctx.Err(); err != nil {
		result.err = err
		return result
	}
	content, err := os.ReadFile(input.path)
	if err != nil {
		result.err = trace.Wrap(err, "read build file "+input.relPath)
		return result
	}
	if err := ctx.Err(); err != nil {
		result.err = err
		return result
	}
	hash := sha256.Sum256(content)
	hashString := hex.EncodeToString(hash[:])
	cacheKey, cacheable := parseResultCacheKey(input, hashString)
	if cacheable && s.ParseCache != nil {
		payload, found, loadErr := s.ParseCache.LoadParseResult(ctx, cacheKey)
		if loadErr != nil {
			s.logger().DebugContext(ctx, "parse cache load failed; parsing source", "file", input.relPath, "error", loadErr)
		}
		if found && loadErr == nil {
			cached, decodeErr := decodeCachedParseRecord(payload)
			if decodeErr == nil {
				setBuildNodeHashes(cached.Nodes, hashString)
				result.record = cached.toSpooledRecord(input.relPath, content)
				result.cacheHit = true
				return result
			}
			s.logger().DebugContext(ctx, "parse cache decode failed; parsing source", "file", input.relPath, "error", decodeErr)
		}
		result.cacheMiss = true
	}
	nodes, edges, tsComments, meta, language, err := parseForBuild(ctx, input.parser, input.relPath, content)
	if err != nil {
		result.err = trace.Wrap(err, "parse build file "+input.relPath)
		return result
	}
	setBuildNodeHashes(nodes, hashString)
	nodeBatch := newParsedBuildNodeBatch(input.relPath, content, nodes, meta.Package, meta.Interfaces, tsComments, language)
	result.record = spooledBuildRecord{
		RelPath:     input.relPath,
		Nodes:       nodes,
		PackageName: meta.Package,
		Interfaces:  meta.Interfaces,
		Comments:    tsComments,
		Language:    language,
		SourceLines: nodeBatch.sourceLines,
		Edges:       edges,
		Bytes:       int64(len(content)),
	}
	if cacheable && s.ParseCache != nil {
		payload, encodeErr := encodeCachedParseRecord(cachedParseRecordFrom(result.record))
		if encodeErr != nil {
			s.logger().DebugContext(ctx, "parse cache encode failed", "file", input.relPath, "error", encodeErr)
		} else if storeErr := s.ParseCache.StoreParseResult(ctx, cacheKey, payload); storeErr != nil {
			s.logger().DebugContext(ctx, "parse cache store failed", "file", input.relPath, "error", storeErr)
		}
	}
	return result
}

// applyBuildSpoolInTx replays spool records into the transaction-scoped graph store and triggers search rebuild.
// @intent rebuild the graph from scratch atomically so partial failures cannot leave stale state.
// @sideEffect resets and repopulates graph nodes/edges/annotations and may rebuild search documents.
// @mutates graph nodes, edges, annotations, and search_documents
func (s *Service) applyBuildSpoolInTx(ctx context.Context, tx ingestapp.Transaction, opts BuildOptions, spool *buildSpool, timing *BuildTiming) error {
	txStore := tx.Graph()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := txStore.DeleteGraph(ctx); err != nil {
		return trace.Wrap(err, "reset graph state before rebuild")
	}
	if err := upsertPackageNodes(ctx, txStore, spool.packages); err != nil {
		return trace.Wrap(err, "upsert package nodes")
	}

	batch := buildPersistBatch{}
	nodePersistStart := time.Now()
	var packageNodeBatches []parsedBuildNodeBatch
	for _, path := range spool.records {
		record, err := spool.readRecord(path)
		if err != nil {
			s.logger().ErrorContext(ctx, "read build spool record failed", append(obs.TraceLogArgs(ctx), "path", path, "error", err)...)
			return err
		}
		batch.add(parsedBuildNodeBatch{
			relPath:     record.RelPath,
			nodes:       record.Nodes,
			packageName: record.PackageName,
			interfaces:  record.Interfaces,
			tsComments:  record.Comments,
			language:    record.Language,
			sourceLines: record.SourceLines,
		}, record.Bytes)
		packageNodeBatches = append(packageNodeBatches, parsedBuildNodeBatch{
			relPath:     record.RelPath,
			nodes:       record.Nodes,
			packageName: record.PackageName,
			interfaces:  record.Interfaces,
			language:    record.Language,
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
	if timing != nil {
		timing.PersistNodesMS = time.Since(nodePersistStart).Milliseconds()
	}
	packageEdgeBatches := s.packageSemanticEdgeBatches(packageNodeBatches)
	packageNodeBatches = nil
	if err := upsertPackageContainsEdges(ctx, txStore, spool.packages); err != nil {
		return trace.Wrap(err, "upsert package file edges")
	}
	edgeResolveStart := time.Now()
	resolveOptions := resolve.ResolveOptions{FallbackCalls: opts.FallbackCalls}
	var resolveTiming *BuildResolveTiming
	if timing != nil {
		resolveTiming = &timing.Resolve
	}
	if err := s.flushBuildEdgeSourceWithTiming(ctx, txStore, spool.edgeBatchSource(ctx, packageEdgeBatches), &spool.stats, resolveOptions, resolveTiming); err != nil {
		return err
	}
	if unresolvedStore, ok := txStore.(ingestapp.UnresolvedEdgeStore); ok {
		if version, versioned := s.unresolvedIndexVersion(); versioned {
			if err := unresolvedStore.MarkUnresolvedIndexReady(ctx, version); err != nil {
				return trace.Wrap(err, "mark unresolved edge index ready")
			}
		}
	}
	if timing != nil {
		timing.ResolveEdgesMS = time.Since(edgeResolveStart).Milliseconds()
	}

	if !opts.SkipSearchRebuild {
		searchStart := time.Now()
		if err := tx.Search().RebuildAll(ctx); err != nil {
			return err
		}
		if timing != nil {
			timing.SearchRebuildMS = time.Since(searchStart).Milliseconds()
		}
	}

	return nil
}

// packageSemanticEdgeBatches derives package-level semantic edges from grouped file batches.
// @intent regenerate package-scoped semantic relationships after node batches reveal the latest package contents.
func (s *Service) packageSemanticEdgeBatches(batches []parsedBuildNodeBatch) []parsedBuildEdgeBatch {
	contexts := make(map[string]*ingestapp.PackageContext)
	filesByKey := make(map[string][]string)
	for _, batch := range batches {
		if batch.language == "" || batch.packageName == "" {
			continue
		}
		key := batch.language + ":" + batch.packageName
		ctx := contexts[key]
		if ctx == nil {
			ctx = &ingestapp.PackageContext{Package: batch.packageName, Language: batch.language}
			contexts[key] = ctx
		}
		ctx.Files = append(ctx.Files, batch.relPath)
		ctx.Nodes = append(ctx.Nodes, batch.nodes...)
		ctx.Interfaces = append(ctx.Interfaces, batch.interfaces...)
		filesByKey[key] = append(filesByKey[key], batch.relPath)
	}
	var out []parsedBuildEdgeBatch
	for key, ctx := range contexts {
		builder := s.packageEdgeBuilder(ctx.Language)
		if builder == nil {
			continue
		}
		edges := builder.PackageEdges(*ctx)
		if len(edges) == 0 {
			continue
		}
		anchorFiles := filesByKey[key]
		if len(anchorFiles) == 0 {
			continue
		}
		anchor := anchorFiles[0]
		resolved := make([]graph.Edge, len(edges))
		copy(resolved, edges)
		for i := range resolved {
			resolved[i].Fingerprint = rewriteImplementsFingerprintScope(resolved[i].Fingerprint, anchor)
			resolved[i].FilePath = anchor
		}
		out = append(out, parsedBuildEdgeBatch{relPath: anchor, edges: resolved})
	}
	return out
}

// flushBuildBatch persists the buffered nodes for the current bounded batch.
// @intent persist all batch nodes before annotations and all edges so references can resolve with fewer store operations.
// @sideEffect upserts graph nodes and annotations through the transaction-scoped store.
// @mutates graph nodes and annotations
func (s *Service) flushBuildBatch(ctx context.Context, txStore ingestapp.GraphStore, batch *buildPersistBatch) error {
	if batch.files == 0 {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	nodeCount := 0
	annotationFilePaths := make([]string, 0, len(batch.nodeBatches))
	for _, parsed := range batch.nodeBatches {
		nodeCount += len(parsed.nodes)
		if len(parsed.tsComments) > 0 {
			annotationFilePaths = append(annotationFilePaths, parsed.relPath)
		}
	}
	nodes := make([]graph.Node, 0, nodeCount)
	for _, parsed := range batch.nodeBatches {
		nodes = append(nodes, parsed.nodes...)
	}
	if len(nodes) > 0 {
		if err := txStore.UpsertNodes(ctx, nodes); err != nil {
			return trace.Wrap(err, "upsert batch nodes")
		}
	}

	var storedNodesByFile map[string][]graph.Node
	if len(annotationFilePaths) > 0 {
		stored, err := txStore.GetNodesByFiles(ctx, annotationFilePaths)
		if err != nil {
			return trace.Wrap(err, "get stored nodes for annotations")
		}
		storedNodesByFile = stored
	}

	for i := range batch.nodeBatches {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := s.bindAndReleaseNodeBatch(ctx, txStore, storedNodesByFile, batch.nodeBatches, i); err != nil {
			return err
		}
	}

	batch.reset()
	return nil
}

// buildResolveLookup memoizes immutable file-suffix lookups for one edge-resolution phase.
// @intent avoid repeatedly scanning persisted file nodes for identical import paths during a full build.
type buildResolveLookup struct {
	ingestapp.GraphStore
	fileNodesBySuffix map[string][]graph.Node
	importFileIndex   *resolve.ImportFileIndex
	timing            *BuildResolveTiming
}

// newBuildResolveLookup creates the build-scoped read-through lookup cache.
// @intent share immutable import file-node results across all resolver chunks in one build.
func newBuildResolveLookup(store ingestapp.GraphStore) *buildResolveLookup {
	return newBuildResolveLookupWithTiming(store, nil)
}

// newBuildResolveLookupWithTiming creates a build-scoped lookup cache that records store-read timings when requested.
// @intent retain the existing resolver lookup contract while making individual database-read costs observable.
func newBuildResolveLookupWithTiming(store ingestapp.GraphStore, timing *BuildResolveTiming) *buildResolveLookup {
	return &buildResolveLookup{
		GraphStore:        store,
		fileNodesBySuffix: make(map[string][]graph.Node),
		timing:            timing,
	}
}

// add records one resolver operation after its underlying work completes.
// @intent accumulate a single build-scoped timing without changing the observed operation result.
func (t *BuildResolveOperationTiming) add(duration time.Duration) {
	if t == nil {
		return
	}
	t.Calls++
	t.MS += duration.Milliseconds()
}

// GetNodesByIDs delegates the resolver's node-ID lookup and records its store cost.
// @intent measure node-ID store reads while preserving the resolver lookup contract.
func (l *buildResolveLookup) GetNodesByIDs(ctx context.Context, ids []uint) ([]graph.Node, error) {
	started := time.Now()
	nodes, err := l.GraphStore.GetNodesByIDs(ctx, ids)
	if l.timing != nil {
		l.timing.NodesByIDs.add(time.Since(started))
	}
	return nodes, err
}

// GetNodesByFiles delegates the resolver's file-node lookup and records its store cost.
// @intent measure file-node store reads while preserving the resolver lookup contract.
func (l *buildResolveLookup) GetNodesByFiles(ctx context.Context, filePaths []string) (map[string][]graph.Node, error) {
	started := time.Now()
	nodes, err := l.GraphStore.GetNodesByFiles(ctx, filePaths)
	if l.timing != nil {
		l.timing.NodesByFiles.add(time.Since(started))
	}
	return nodes, err
}

// GetNodesByQualifiedNames delegates the resolver's qualified-name lookup and records its store cost.
// @intent measure qualified-name store reads while preserving the resolver lookup contract.
func (l *buildResolveLookup) GetNodesByQualifiedNames(ctx context.Context, names []string) (map[string][]graph.Node, error) {
	started := time.Now()
	nodes, err := l.GraphStore.GetNodesByQualifiedNames(ctx, names)
	if l.timing != nil {
		l.timing.NodesByQualifiedNames.add(time.Since(started))
	}
	return nodes, err
}

// GetEdgesToNodes delegates the resolver's implements lookup and records its store cost.
// @intent measure implements-edge store reads while preserving the resolver lookup contract.
func (l *buildResolveLookup) GetEdgesToNodes(ctx context.Context, nodeIDs []uint) ([]graph.Edge, error) {
	started := time.Now()
	edges, err := l.GraphStore.GetEdgesToNodes(ctx, nodeIDs)
	if l.timing != nil {
		l.timing.EdgesToNodes.add(time.Since(started))
	}
	return edges, err
}

// GetFileNodesByPathSuffix returns a cached suffix lookup or reads and stores it once.
// @intent eliminate repeated store scans while preserving the GraphStore lookup contract.
func (l *buildResolveLookup) GetFileNodesByPathSuffix(ctx context.Context, suffix string) ([]graph.Node, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if nodes, ok := l.fileNodesBySuffix[suffix]; ok {
		return nodes, nil
	}
	if l.importFileIndex == nil {
		started := time.Now()
		nodes, err := l.GraphStore.ListImportFileNodes(ctx)
		if l.timing != nil {
			l.timing.ImportFileNodes.add(time.Since(started))
		}
		if err != nil {
			return nil, err
		}
		l.importFileIndex = resolve.NewImportFileIndex(nodes)
	}
	nodes := l.importFileIndex.Find(suffix)
	l.fileNodesBySuffix[suffix] = nodes
	return nodes, nil
}

// flushBuildEdges resolves and persists all deferred edges after graph nodes exist.
// @intent attach parsed relationships to stored node IDs without depending on build batch order.
// @sideEffect upserts graph edges through the transaction-scoped store.
// @mutates graph edges
func (s *Service) flushBuildEdges(ctx context.Context, txStore ingestapp.GraphStore, edgeBatches []parsedBuildEdgeBatch, stats *BuildStats, resolveOptions resolve.ResolveOptions) error {
	return s.flushBuildEdgesWithTiming(ctx, txStore, edgeBatches, stats, resolveOptions, nil)
}

// flushBuildEdgesWithTiming resolves and persists deferred edges while recording each resolver operation when timing is supplied.
// @intent measure edge-resolution database reads and writes without altering the existing resolution order or transaction.
func (s *Service) flushBuildEdgesWithTiming(ctx context.Context, txStore ingestapp.GraphStore, edgeBatches []parsedBuildEdgeBatch, stats *BuildStats, resolveOptions resolve.ResolveOptions, timing *BuildResolveTiming) error {
	source := buildEdgeBatchSource(func(yield func(parsedBuildEdgeBatch) error) error {
		for _, batch := range edgeBatches {
			if err := yield(batch); err != nil {
				return err
			}
		}
		return nil
	})
	return s.flushBuildEdgeSourceWithTiming(ctx, txStore, source, stats, resolveOptions, timing)
}

// flushBuildEdgeSourceWithTiming resolves a re-iterable edge source in implements-first order using bounded buffers.
// @intent avoid retaining or repartitioning every build edge while preserving resolver ordering and timing contracts.
// @sideEffect upserts graph edges through the transaction-scoped store.
// @mutates graph edges
func (s *Service) flushBuildEdgeSourceWithTiming(ctx context.Context, txStore ingestapp.GraphStore, source buildEdgeBatchSource, stats *BuildStats, resolveOptions resolve.ResolveOptions, timing *BuildResolveTiming) error {
	lookup := newBuildResolveLookupWithTiming(txStore, timing)
	var pendingImplements []graph.Edge
	flushImplements := func() error {
		if len(pendingImplements) == 0 {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		resolveStarted := time.Now()
		resolved, err := s.edgeResolver()(ctx, lookup, pendingImplements, resolveOptions)
		if timing != nil {
			timing.Resolver.add(time.Since(resolveStarted))
		}
		if err != nil {
			s.logger().ErrorContext(ctx, "resolve deferred implements edges failed", append(obs.TraceLogArgs(ctx), "edges", len(pendingImplements), "error", err)...)
			return trace.Wrap(err, "resolve deferred implements edges")
		}
		resolved, unresolved, diagnostics := resolve.PartitionResolvedWithDiagnosticsFiltered(resolved, shouldSuppressExternalImportUnresolved)
		mergeBuildUnresolvedDiagnostics(stats, diagnostics)
		if err := persistBuildUnresolvedEdges(ctx, txStore, unresolved); err != nil {
			return err
		}
		if diagnostics.DroppedCount > 0 {
			s.logger().DebugContext(ctx, "dropped unresolved implements edges", append(obs.TraceLogArgs(ctx), "count", diagnostics.DroppedCount, "by_kind", formatEdgeKindCounts(diagnostics.ByKind), "by_reason", diagnostics.ByReason)...)
		}
		upsertStarted := time.Now()
		err = txStore.UpsertEdges(ctx, resolved)
		if timing != nil {
			timing.UpsertEdges.add(time.Since(upsertStarted))
		}
		if err != nil {
			s.logger().ErrorContext(ctx, "upsert deferred implements edges failed", append(obs.TraceLogArgs(ctx), "edges", len(resolved), "error", err)...)
			return trace.Wrap(err, "upsert deferred implements edges")
		}
		pendingImplements = nil
		return nil
	}
	if err := source(func(parsed parsedBuildEdgeBatch) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		for _, edge := range parsed.edges {
			if edge.Kind != graph.EdgeKindImplements {
				continue
			}
			if len(pendingImplements) == buildEdgeResolveChunkSize {
				if err := flushImplements(); err != nil {
					return err
				}
			}
			pendingImplements = append(pendingImplements, edge)
		}
		return nil
	}); err != nil {
		return trace.Wrap(err, "replay deferred implements edges")
	}
	if err := flushImplements(); err != nil {
		return err
	}

	// actualEdgeRange identifies parsed edges within a resolver input that also contains import warmup edges.
	// @intent persist only original parsed edges after using import edges to enrich resolution context.
	type actualEdgeRange struct{ start, end int }
	var pending []graph.Edge
	var actualRanges []actualEdgeRange
	flushPending := func() error {
		if len(pending) == 0 {
			return nil
		}
		resolveStarted := time.Now()
		resolved, err := s.edgeResolver()(ctx, lookup, pending, resolveOptions)
		if timing != nil {
			timing.Resolver.add(time.Since(resolveStarted))
		}
		if err != nil {
			s.logger().ErrorContext(ctx, "resolve deferred edge batch failed", append(obs.TraceLogArgs(ctx), "edges", len(pending), "error", err)...)
			return trace.Wrap(err, "resolve deferred edge batch")
		}
		resolvedEdges := make([]graph.Edge, 0, len(resolved))
		var unresolvedEdges []graph.Edge
		for _, actual := range actualRanges {
			if actual.end > len(resolved) {
				actual.end = len(resolved)
			}
			if actual.start >= actual.end {
				continue
			}
			resolvedChunk, unresolvedChunk, diagnostics := resolve.PartitionResolvedWithDiagnosticsFiltered(resolved[actual.start:actual.end], shouldSuppressExternalImportUnresolved)
			mergeBuildUnresolvedDiagnostics(stats, diagnostics)
			resolvedEdges = append(resolvedEdges, resolvedChunk...)
			unresolvedEdges = append(unresolvedEdges, unresolvedChunk...)
		}
		if err := persistBuildUnresolvedEdges(ctx, txStore, unresolvedEdges); err != nil {
			return err
		}
		if len(resolvedEdges) > 0 {
			upsertStarted := time.Now()
			err = txStore.UpsertEdges(ctx, resolvedEdges)
			if timing != nil {
				timing.UpsertEdges.add(time.Since(upsertStarted))
			}
			if err != nil {
				s.logger().ErrorContext(ctx, "upsert deferred edge batch failed", append(obs.TraceLogArgs(ctx), "edges", len(resolvedEdges), "error", err)...)
				return trace.Wrap(err, "upsert deferred edge batch")
			}
		}
		pending = nil
		actualRanges = nil
		return nil
	}
	if err := source(func(parsed parsedBuildEdgeBatch) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		_, otherEdges := splitImplementsEdges(parsed.edges)
		imports := importEdges(otherEdges)
		for start := 0; start < len(otherEdges); start += buildEdgeResolveChunkSize {
			end := min(start+buildEdgeResolveChunkSize, len(otherEdges))
			chunk := otherEdges[start:end]
			resolveInput := chunkWithImportWarmup(chunk, imports)
			if len(pending) > 0 && len(pending)+len(resolveInput) > buildEdgeResolveChunkSize {
				if err := flushPending(); err != nil {
					return err
				}
			}
			actualStart := len(pending) + len(resolveInput) - len(chunk)
			pending = append(pending, resolveInput...)
			actualRanges = append(actualRanges, actualEdgeRange{start: actualStart, end: actualStart + len(chunk)})
		}
		return nil
	}); err != nil {
		return trace.Wrap(err, "replay deferred edges")
	}
	if err := flushPending(); err != nil {
		return err
	}
	return nil
}

// persistBuildUnresolvedEdges records syntax candidates separately from traversable graph edges.
// @intent populate the reverse index during full builds while keeping stores without the optional capability compatible.
// @sideEffect writes unresolved candidate rows when supported by the graph store.
func persistBuildUnresolvedEdges(ctx context.Context, store ingestapp.GraphStore, edges []graph.Edge) error {
	if len(edges) == 0 {
		return nil
	}
	unresolvedStore, ok := store.(ingestapp.UnresolvedEdgeStore)
	if !ok {
		return nil
	}
	if err := unresolvedStore.UpsertUnresolvedEdges(ctx, resolve.BuildUnresolvedCandidates(edges)); err != nil {
		return trace.Wrap(err, "persist unresolved build edges")
	}
	return nil
}

// importEdges selects file-local import warmup edges from one streamed edge batch.
// @intent preserve import-aware call resolution without retaining a build-wide import map.
func importEdges(edges []graph.Edge) []graph.Edge {
	var imports []graph.Edge
	for _, edge := range edges {
		if edge.Kind == graph.EdgeKindImportsFrom {
			imports = append(imports, edge)
		}
	}
	return imports
}

// rewriteImplementsFingerprintScope rewrites an implements fingerprint to use a package anchor file.
// @intent keep synthesized package semantic edges idempotent even when they are rebuilt from different files.
func rewriteImplementsFingerprintScope(fingerprint, scope string) string {
	if !strings.HasPrefix(fingerprint, "implements:") {
		return fingerprint
	}
	rest := strings.TrimPrefix(fingerprint, "implements:")
	idx := strings.Index(rest, ":")
	if idx < 0 {
		return fingerprint
	}
	return "implements:" + scope + ":" + rest[idx+1:]
}

// mergeBuildUnresolvedDiagnostics folds one chunk's unresolved-edge diagnostics into build totals.
// @intent keep build-time unresolved-edge reporting aligned with chunked edge resolution output.
// @mutates stats.Unresolved
func mergeBuildUnresolvedDiagnostics(stats *BuildStats, diagnostics resolve.FilterResolvedDiagnostics) {
	if stats == nil || diagnostics.DroppedCount == 0 {
		return
	}
	mergeFilterResolvedDiagnostics(&stats.Unresolved, diagnostics)
}

// mergeFilterResolvedDiagnostics accumulates one diagnostics payload into another.
// @intent reuse the same unresolved-edge aggregation rules across build and incremental sync flows.
// @mutates dst
func mergeFilterResolvedDiagnostics(dst *resolve.FilterResolvedDiagnostics, src resolve.FilterResolvedDiagnostics) {
	if dst == nil || src.DroppedCount == 0 {
		return
	}
	dst.DroppedCount += src.DroppedCount
	if len(src.ByKind) > 0 {
		if dst.ByKind == nil {
			dst.ByKind = make(map[graph.EdgeKind]int, len(src.ByKind))
		}
		for kind, count := range src.ByKind {
			dst.ByKind[kind] += count
		}
	}
	if len(src.ByFile) > 0 {
		if dst.ByFile == nil {
			dst.ByFile = make(map[string]int, len(src.ByFile))
		}
		for filePath, count := range src.ByFile {
			dst.ByFile[filePath] += count
		}
	}
	if len(src.ByReason) > 0 {
		if dst.ByReason == nil {
			dst.ByReason = make(map[string]int, len(src.ByReason))
		}
		for reason, count := range src.ByReason {
			dst.ByReason[reason] += count
		}
	}
	remaining := 5 - len(dst.Samples)
	if remaining <= 0 || len(src.Samples) == 0 {
		return
	}
	if remaining > len(src.Samples) {
		remaining = len(src.Samples)
	}
	dst.Samples = append(dst.Samples, src.Samples[:remaining]...)
}

// formatEdgeKindCounts rewrites edge-kind counters into string-keyed log fields.
// @intent serialize EdgeKind counters into diagnostics-friendly logging output.
func formatEdgeKindCounts(counts map[graph.EdgeKind]int) map[string]int {
	if len(counts) == 0 {
		return nil
	}
	formatted := make(map[string]int, len(counts))
	for kind, count := range counts {
		formatted[string(kind)] = count
	}
	return formatted
}

// shouldSuppressExternalImportUnresolved suppresses unresolved imports that are likely external modules.
// @intent reduce false-positive warning volume when dependency code is intentionally absent in the local graph.
func shouldSuppressExternalImportUnresolved(edge graph.Edge, _ string) bool {
	return edge.Kind == graph.EdgeKindImportsFrom && resolve.IsLikelyExternalImportEdge(edge)
}

// splitImplementsEdges separates implements edges from other relationship types.
// @intent ensure interface fulfillment edges are handled before call dispatch resolution.
func splitImplementsEdges(edges []graph.Edge) ([]graph.Edge, []graph.Edge) {
	var implementsEdges []graph.Edge
	var otherEdges []graph.Edge
	for _, edge := range edges {
		if edge.Kind == graph.EdgeKindImplements {
			implementsEdges = append(implementsEdges, edge)
			continue
		}
		otherEdges = append(otherEdges, edge)
	}
	return implementsEdges, otherEdges
}

// chunkWithImportWarmup combines a chunk of call edges with their file's import edges.
// @intent ensure the edge resolver has enough context to resolve call targets through imports.
func chunkWithImportWarmup(chunk []graph.Edge, imports []graph.Edge) []graph.Edge {
	if len(chunk) == 0 {
		return nil
	}
	needsWarmup := false
	for _, edge := range chunk {
		if graph.IsCallKind(edge.Kind) {
			needsWarmup = true
			break
		}
	}
	if !needsWarmup || len(imports) == 0 {
		return append([]graph.Edge(nil), chunk...)
	}
	resolveInput := append([]graph.Edge(nil), imports...)
	resolveInput = append(resolveInput, chunk...)
	return resolveInput
}
