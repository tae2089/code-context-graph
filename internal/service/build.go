// @index Full graph build pipeline: spool, batch persistence, and edge resolution.
package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gorm.io/gorm"

	"github.com/tae2089/trace"

	"github.com/tae2089/code-context-graph/internal/edgeresolve"
	"github.com/tae2089/code-context-graph/internal/model"
	"github.com/tae2089/code-context-graph/internal/obs"
	"github.com/tae2089/code-context-graph/internal/parse"
	"github.com/tae2089/code-context-graph/internal/parse/treesitter"
	"github.com/tae2089/code-context-graph/internal/store"
)

// parsedBuildNodeBatch carries per-file parsed nodes plus comment-binding inputs through the build pipeline.
// @intent keep node persistence and annotation binding aligned to the same source snapshot.
type parsedBuildNodeBatch struct {
	relPath     string
	nodes       []model.Node
	packageName string
	interfaces  []treesitter.PackageInterfaceInfo
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

// newParsedBuildNodeBatch packages parsed nodes plus comment metadata for later persistence.
// @intent defer comment binding until storage time while keeping per-file source line context available.
func newParsedBuildNodeBatch(relPath string, content []byte, nodes []model.Node, packageName string, interfaces []treesitter.PackageInterfaceInfo, tsComments []treesitter.CommentBlock, language string) parsedBuildNodeBatch {
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
	if s.onBatchRelease != nil {
		s.onBatchRelease(batches, idx)
	}
	return nil
}

// Build walks source files, stores parsed graph data, and rebuilds search docs.
// @intent perform a full graph build from the specified directory.
// @sideEffect 파일 시스템을 읽고 그래프 저장소·DB·검색 인덱스를 갱신한다.
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

	err = s.withBuildTx(ctx, opts, func(txStore store.GraphStore, txDB *gorm.DB) error {
		return s.applyBuildSpoolInTx(ctx, txStore, txDB, opts, spool)
	})
	if err != nil {
		return stats, err
	}

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

		nodes, edges, tsComments, meta, language, err := parseForBuild(ctx, parser, relPath, content)
		if err != nil {
			return trace.Wrap(err, "parse build file "+relPath)
		}
		hash := sha256.Sum256(content)
		hashString := hex.EncodeToString(hash[:])
		for i := range nodes {
			nodes[i].Hash = hashString
		}

		nodeBatch := newParsedBuildNodeBatch(relPath, content, nodes, meta.Package, meta.Interfaces, tsComments, language)
		record := spooledBuildRecord{
			RelPath:     relPath,
			Nodes:       nodes,
			PackageName: meta.Package,
			Interfaces:  meta.Interfaces,
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
	if err := upsertPackageNodes(ctx, txStore, spool.packages); err != nil {
		return trace.Wrap(err, "upsert package nodes")
	}

	batch := buildPersistBatch{}
	var edgeBatches []parsedBuildEdgeBatch
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
	edgeBatches = append(edgeBatches, s.packageSemanticEdgeBatches(packageNodeBatches)...)
	if err := upsertPackageContainsEdges(ctx, txStore, spool.packages); err != nil {
		return trace.Wrap(err, "upsert package file edges")
	}
	resolveOptions := edgeresolve.ResolveOptions{FallbackCalls: opts.FallbackCalls}
	if err := s.flushBuildEdges(ctx, txStore, edgeBatches, &spool.stats, resolveOptions); err != nil {
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

// packageSemanticEdgeBatches derives package-level semantic edges from grouped file batches.
// @intent regenerate package-scoped semantic relationships after node batches reveal the latest package contents.
func (s *GraphService) packageSemanticEdgeBatches(batches []parsedBuildNodeBatch) []parsedBuildEdgeBatch {
	contexts := make(map[string]*treesitter.PackageContext)
	filesByKey := make(map[string][]string)
	for _, batch := range batches {
		if batch.language == "" || batch.packageName == "" {
			continue
		}
		key := batch.language + ":" + batch.packageName
		ctx := contexts[key]
		if ctx == nil {
			ctx = &treesitter.PackageContext{Package: batch.packageName, Language: batch.language}
			contexts[key] = ctx
		}
		ctx.Files = append(ctx.Files, batch.relPath)
		ctx.Nodes = append(ctx.Nodes, batch.nodes...)
		ctx.Interfaces = append(ctx.Interfaces, batch.interfaces...)
		filesByKey[key] = append(filesByKey[key], batch.relPath)
	}
	var out []parsedBuildEdgeBatch
	for key, ctx := range contexts {
		semantics := treesitter.SemanticsForLanguage(ctx.Language)
		edges := treesitter.PackageEdgesFor(semantics, *ctx)
		if len(edges) == 0 {
			continue
		}
		anchorFiles := filesByKey[key]
		if len(anchorFiles) == 0 {
			continue
		}
		anchor := anchorFiles[0]
		resolved := make([]model.Edge, len(edges))
		copy(resolved, edges)
		for i := range resolved {
			resolved[i].Fingerprint = rewriteImplementsFingerprintScope(resolved[i].Fingerprint, anchor)
			resolved[i].FilePath = anchor
		}
		out = append(out, parsedBuildEdgeBatch{relPath: anchor, edges: resolved})
	}
	return out
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
func (s *GraphService) flushBuildEdges(ctx context.Context, txStore store.GraphStore, edgeBatches []parsedBuildEdgeBatch, stats *BuildStats, resolveOptions edgeresolve.ResolveOptions) error {
	implementsEdges, otherBatches := partitionBuildEdges(edgeBatches)
	importsByPath := importEdgesByFile(otherBatches)
	for start := 0; start < len(implementsEdges); start += buildEdgeResolveChunkSize {
		if err := ctx.Err(); err != nil {
			return err
		}
		end := min(start+buildEdgeResolveChunkSize, len(implementsEdges))
		resolved, err := s.edgeResolver()(ctx, txStore, implementsEdges[start:end], resolveOptions)
		if err != nil {
			s.logger().ErrorContext(ctx, "resolve deferred implements edges failed", append(obs.TraceLogArgs(ctx), "start", start, "end", end, "error", err)...)
			return trace.Wrap(err, "resolve deferred implements edges")
		}
		resolved, diagnostics := edgeresolve.FilterResolvedWithDiagnosticsFiltered(resolved, shouldSuppressExternalImportUnresolved)
		mergeBuildUnresolvedDiagnostics(stats, diagnostics)
		if diagnostics.DroppedCount > 0 {
			s.logger().DebugContext(ctx, "dropped unresolved implements edges", append(obs.TraceLogArgs(ctx), "count", diagnostics.DroppedCount, "by_kind", formatEdgeKindCounts(diagnostics.ByKind), "by_reason", diagnostics.ByReason)...)
		}
		if err := txStore.UpsertEdges(ctx, resolved); err != nil {
			s.logger().ErrorContext(ctx, "upsert deferred implements edges failed", append(obs.TraceLogArgs(ctx), "start", start, "end", end, "error", err)...)
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
			resolveInput := chunkWithImportWarmup(chunk, importsByPath[parsed.relPath])
			resolved, err := s.edgeResolver()(ctx, txStore, resolveInput, resolveOptions)
			if err != nil {
				s.logger().ErrorContext(ctx, "resolve deferred edges failed", append(obs.TraceLogArgs(ctx), "file", parsed.relPath, "error", err)...)
				return trace.Wrap(err, "resolve deferred edges for "+parsed.relPath)
			}
			resolvedChunk, diagnostics := edgeresolve.FilterResolvedWithDiagnosticsFiltered(resolved[len(resolveInput)-len(chunk):], shouldSuppressExternalImportUnresolved)
			mergeBuildUnresolvedDiagnostics(stats, diagnostics)
			if diagnostics.DroppedCount > 0 {
				s.logger().DebugContext(ctx, "dropped unresolved edges", append(obs.TraceLogArgs(ctx), "file", parsed.relPath, "count", diagnostics.DroppedCount, "by_kind", formatEdgeKindCounts(diagnostics.ByKind), "by_reason", diagnostics.ByReason)...)
			}
			if err := txStore.UpsertEdges(ctx, resolvedChunk); err != nil {
				s.logger().ErrorContext(ctx, "upsert deferred edges failed", append(obs.TraceLogArgs(ctx), "file", parsed.relPath, "error", err)...)
				return trace.Wrap(err, "upsert deferred edges for "+parsed.relPath)
			}
		}
	}
	return nil
}

// mergeBuildUnresolvedDiagnostics folds one chunk's unresolved-edge diagnostics into build totals.
// @intent keep build-time unresolved-edge reporting aligned with chunked edge resolution output.
// @mutates stats.Unresolved
func mergeBuildUnresolvedDiagnostics(stats *BuildStats, diagnostics edgeresolve.FilterResolvedDiagnostics) {
	if stats == nil || diagnostics.DroppedCount == 0 {
		return
	}
	mergeFilterResolvedDiagnostics(&stats.Unresolved, diagnostics)
}

// mergeFilterResolvedDiagnostics accumulates one diagnostics payload into another.
// @intent reuse the same unresolved-edge aggregation rules across build and incremental sync flows.
// @mutates dst
func mergeFilterResolvedDiagnostics(dst *edgeresolve.FilterResolvedDiagnostics, src edgeresolve.FilterResolvedDiagnostics) {
	if dst == nil || src.DroppedCount == 0 {
		return
	}
	dst.DroppedCount += src.DroppedCount
	if len(src.ByKind) > 0 {
		if dst.ByKind == nil {
			dst.ByKind = make(map[model.EdgeKind]int, len(src.ByKind))
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
func formatEdgeKindCounts(counts map[model.EdgeKind]int) map[string]int {
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
func shouldSuppressExternalImportUnresolved(edge model.Edge, _ string) bool {
	return edge.Kind == model.EdgeKindImportsFrom && edgeresolve.IsLikelyExternalImportEdge(edge)
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

// splitImplementsEdges separates implements edges from other relationship types.
// @intent ensure interface fulfillment edges are handled before call dispatch resolution.
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

// importEdgesByFile groups import-from edges by their source file path.
// @intent optimize edge resolution by pre-loading import context for each file.
func importEdgesByFile(edgeBatches []parsedBuildEdgeBatch) map[string][]model.Edge {
	byFile := make(map[string][]model.Edge, len(edgeBatches))
	for _, batch := range edgeBatches {
		for _, edge := range batch.edges {
			if edge.Kind != model.EdgeKindImportsFrom {
				continue
			}
			byFile[batch.relPath] = append(byFile[batch.relPath], edge)
		}
	}
	return byFile
}

// chunkWithImportWarmup combines a chunk of call edges with their file's import edges.
// @intent ensure the edge resolver has enough context to resolve call targets through imports.
func chunkWithImportWarmup(chunk []model.Edge, imports []model.Edge) []model.Edge {
	if len(chunk) == 0 {
		return nil
	}
	needsWarmup := false
	for _, edge := range chunk {
		if model.IsCallKind(edge.Kind) {
			needsWarmup = true
			break
		}
	}
	if !needsWarmup || len(imports) == 0 {
		return append([]model.Edge(nil), chunk...)
	}
	resolveInput := append([]model.Edge(nil), imports...)
	resolveInput = append(resolveInput, chunk...)
	return resolveInput
}
