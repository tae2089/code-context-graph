package incremental

import (
	"context"
	"log/slog"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/tae2089/code-context-graph/internal/edgeresolve"
	"github.com/tae2089/code-context-graph/internal/model"
	"github.com/tae2089/code-context-graph/internal/parse"
	"github.com/tae2089/code-context-graph/internal/parse/treesitter"
)

// Store defines persistence operations needed for incremental sync.
// @intent abstract graph storage so changed files can be reparsed and upserted
type Store interface {
	GetNodesByIDs(ctx context.Context, ids []uint) ([]model.Node, error)
	GetNodesByFile(ctx context.Context, filePath string) ([]model.Node, error)
	GetNodesByFiles(ctx context.Context, filePaths []string) (map[string][]model.Node, error)
	GetNodesByQualifiedNames(ctx context.Context, names []string) (map[string][]model.Node, error)
	UpsertNodes(ctx context.Context, nodes []model.Node) error
	UpsertEdges(ctx context.Context, edges []model.Edge) error
	DeleteNodesByFile(ctx context.Context, filePath string) error
}

// Parser parses one file into graph nodes and edges.
// @intent decouple incremental sync from language-specific parsing logic
type Parser interface {
	Parse(filePath string, content []byte) ([]model.Node, []model.Edge, error)
}

// AnnotatingParser exposes richer parse output needed to restore annotations.
// @intent allow incremental sync to reuse comment-aware parsing when available
type AnnotatingParser interface {
	Parser
	ParseWithComments(ctx context.Context, filePath string, content []byte) ([]model.Node, []model.Edge, []treesitter.CommentBlock, error)
	Language() string
}

// annotationWriter is the optional store capability needed to persist comment-derived annotations.
// @intent allow incremental sync to skip annotation writes when the underlying store does not support them.
type annotationWriter interface {
	UpsertAnnotation(ctx context.Context, ann *model.Annotation) error
}

// FileInfo holds change-tracking data for one file.
// @intent carry file content and hash so sync can detect modifications cheaply
type FileInfo struct {
	Hash    string
	Content []byte
	Force   bool
}

// SyncStats summarizes one incremental sync run.
// @intent report how many files were added, modified, skipped, or deleted
type SyncStats struct {
	Added      int
	Modified   int
	Skipped    int
	Deleted    int
	Unresolved edgeresolve.FilterResolvedDiagnostics
}

// Syncer incrementally updates graph data for changed files.
// @intent avoid full rebuilds by reparsing only files whose content hash changed
type Syncer struct {
	store   Store
	parser  Parser
	parsers map[string]Parser
	opts    edgeresolve.ResolveOptions
	logger  *slog.Logger
}

// parsedSyncFile holds parsed output and file metadata for one incremental sync input.
// @intent carry parsed nodes, edges, comments, and language state through the sync pipeline.
type parsedSyncFile struct {
	filePath    string
	info        FileInfo
	nodes       []model.Node
	edges       []model.Edge
	comments    []treesitter.CommentBlock
	language    string
	hadExisting bool
}

// releaseContent drops the in-memory file content for one path so the sync loop can free memory early.
// @intent prevent the FileInfo map from holding all source bytes after a file has been processed.
// @mutates files[filePath].Content
func releaseContent(files map[string]FileInfo, filePath string) {
	info, ok := files[filePath]
	if !ok {
		return
	}
	info.Content = nil
	files[filePath] = info
}

// SyncerOption configures a Syncer instance.
// @intent customize incremental sync behavior without expanding the constructor signature
type SyncerOption func(*Syncer)

// WithLogger sets the logger used during sync.
// @intent allow callers to observe incremental sync progress through structured logs
// @mutates Syncer.logger
func WithLogger(l *slog.Logger) SyncerOption {
	return func(s *Syncer) {
		s.logger = l
	}
}

// WithParsers sets extension-based parsers used during sync.
// @intent let incremental sync dispatch parsing per file extension for multi-language projects
func WithParsers(parsers map[string]Parser) SyncerOption {
	return func(s *Syncer) {
		s.parsers = parsers
	}
}

// WithResolveOptions sets edge-resolution options for this syncer instance.
// @intent keep call resolution behavior configurable without changing core interfaces.
func WithResolveOptions(opts edgeresolve.ResolveOptions) SyncerOption {
	return func(s *Syncer) {
		s.opts = opts
	}
}

// New creates an incremental syncer.
// @intent wire storage, parser, and optional configuration into a sync coordinator
// @ensures returned syncer always has a non-nil logger
func New(store Store, parser Parser, opts ...SyncerOption) *Syncer {
	s := &Syncer{store: store, parser: parser}
	for _, opt := range opts {
		opt(s)
	}
	if s.logger == nil {
		s.logger = slog.Default()
	}
	return s
}

// NewWithRegistry creates an incremental syncer with extension-based parser dispatch.
// @intent support multi-language incremental parsing without breaking the legacy single-parser constructor
func NewWithRegistry(store Store, parsers map[string]Parser, opts ...SyncerOption) *Syncer {
	opts = append([]SyncerOption{WithParsers(parsers)}, opts...)
	return New(store, nil, opts...)
}

// SetResolveOptions updates edge resolution policy for an existing syncer instance.
// @intent avoid rebuilding the syncer for every Build/Update invocation.
func (s *Syncer) SetResolveOptions(opts edgeresolve.ResolveOptions) {
	s.opts = opts
}

// Sync updates graph data using only the provided file snapshot.
// @intent run incremental parsing when only current files are known
// @param files current file snapshot keyed by repository-relative path
// @see incremental.Syncer.SyncWithExisting
func (s *Syncer) Sync(ctx context.Context, files map[string]FileInfo) (*SyncStats, error) {
	return s.SyncWithExisting(ctx, files, nil)
}

// SyncWithExisting updates graph data and removes files no longer present.
// @intent reconcile parsed graph state with the latest changed-file snapshot
// @param files current file snapshot keyed by repository-relative path
// @param existingFiles previously known file paths used to detect deletions
// @return counts of added, modified, skipped, and deleted files
// @sideEffect writes structured logs during sync execution
// @domainRule unchanged files are skipped when the stored hash matches the incoming hash
// @mutates graph storage by deleting stale nodes and upserting parsed nodes and edges
// @ensures deleted files are removed from storage when absent from files
func (s *Syncer) SyncWithExisting(ctx context.Context, files map[string]FileInfo, existingFiles []string) (*SyncStats, error) {
	return s.syncWithExisting(ctx, s.store, files, existingFiles)
}

// SyncWithExistingStore runs sync with the provided store without mutating the receiver.
// @intent let callers bind incremental sync to an existing transaction-scoped store
func (s *Syncer) SyncWithExistingStore(ctx context.Context, syncStore Store, files map[string]FileInfo, existingFiles []string) (*SyncStats, error) {
	if syncStore == nil {
		syncStore = s.store
	}
	return s.syncWithExisting(ctx, syncStore, files, existingFiles)
}

// syncWithExisting performs the actual diff-and-apply pass against the supplied store.
// @intent compare hashes for known files, parse new/changed ones, and remove deleted entries in one pass.
// @sideEffect upserts nodes/edges/annotations and deletes removed files through syncStore.
// @mutates graph nodes, edges, annotations
func (s *Syncer) syncWithExisting(ctx context.Context, syncStore Store, files map[string]FileInfo, existingFiles []string) (*SyncStats, error) {
	stats := &SyncStats{}

	s.logger.Info("sync started", "file_count", len(files), "existing_count", len(existingFiles))

	filePaths := make([]string, 0, len(files))
	for fp := range files {
		filePaths = append(filePaths, fp)
	}
	existingByFile, err := syncStore.GetNodesByFiles(ctx, filePaths)
	if err != nil {
		return nil, err
	}

	orderedPaths := sortedFilePaths(files)
	parsedFiles := make([]parsedSyncFile, 0, len(files))
	for _, filePath := range orderedPaths {
		info := files[filePath]
		existing := existingByFile[filePath]
		parser := s.resolveParser(filePath)
		if parser == nil {
			s.logger.Debug("file skipped (no parser)", "file", filePath)
			stats.Skipped++
			releaseContent(files, filePath)
			continue
		}

		if len(existing) > 0 && existing[0].Hash == info.Hash && !info.Force {
			s.logger.Debug("file skipped (unchanged)", "file", filePath)
			stats.Skipped++
			releaseContent(files, filePath)
			continue
		}

		parsed := parsedSyncFile{filePath: filePath, info: info, hadExisting: len(existing) > 0}

		if annotatingParser, ok := parser.(AnnotatingParser); ok {
			parsed.nodes, parsed.edges, parsed.comments, err = annotatingParser.ParseWithComments(ctx, filePath, info.Content)
			parsed.language = annotatingParser.Language()
		} else {
			parsed.nodes, parsed.edges, err = parser.Parse(filePath, info.Content)
		}
		if err != nil {
			return nil, err
		}
		setNodeHashes(parsed.nodes, info.Hash)
		parsedFiles = append(parsedFiles, parsed)
	}

	for _, parsed := range parsedFiles {
		if !parsed.hadExisting {
			s.logger.Debug("file added", "file", parsed.filePath)
			stats.Added++
			continue
		}
		if err := syncStore.DeleteNodesByFile(ctx, parsed.filePath); err != nil {
			return nil, err
		}
		s.logger.Debug("file modified", "file", parsed.filePath)
		stats.Modified++
	}

	for _, parsed := range parsedFiles {
		if len(parsed.nodes) > 0 {
			if err := syncStore.UpsertNodes(ctx, parsed.nodes); err != nil {
				return nil, err
			}
			if len(parsed.comments) > 0 {
				if err := s.restoreAnnotations(ctx, syncStore, parsed.filePath, parsed.info.Content, parsed.nodes, parsed.comments, parsed.language); err != nil {
					return nil, err
				}
			}
		}
	}
	if err := s.resolveAndUpsertEdges(ctx, syncStore, parsedFiles, stats); err != nil {
		return nil, err
	}
	for _, parsed := range parsedFiles {
		releaseContent(files, parsed.filePath)
	}

	for _, ep := range existingFiles {
		if _, stillPresent := files[ep]; !stillPresent {
			if err := syncStore.DeleteNodesByFile(ctx, ep); err != nil {
				return nil, err
			}
			s.logger.Debug("file deleted", "file", ep)
			stats.Deleted++
		}
	}

	s.logger.Info("sync completed",
		"added", stats.Added,
		"modified", stats.Modified,
		"skipped", stats.Skipped,
		"deleted", stats.Deleted,
		"unresolved_edges", stats.Unresolved.DroppedCount,
		"unresolved_by_kind", formatEdgeKindCounts(stats.Unresolved.ByKind),
		"unresolved_by_file", stats.Unresolved.ByFile,
		"unresolved_by_reason", stats.Unresolved.ByReason,
		"unresolved_samples", stats.Unresolved.Samples,
	)

	return stats, nil
}

// setNodeHashes records the file content hash on every node parsed from that file.
// @intent keep incremental hash comparisons aligned with the stored graph rows.
// @mutates nodes
func setNodeHashes(nodes []model.Node, hash string) {
	for i := range nodes {
		nodes[i].Hash = hash
	}
}

// sortedFilePaths returns the file map keys in deterministic order.
// @intent stabilize incremental sync traversal so logs, batching, and tests stay reproducible.
func sortedFilePaths(files map[string]FileInfo) []string {
	paths := make([]string, 0, len(files))
	for filePath := range files {
		paths = append(paths, filePath)
	}
	sort.Strings(paths)
	return paths
}

// resolveAndUpsertEdges resolves parsed edges in dependency-safe phases before persisting them.
// @intent preserve interface dispatch and import-backed call resolution during incremental sync updates.
// @sideEffect upserts resolved graph edges through the sync store.
// @mutates graph edges, stats.Unresolved
func (s *Syncer) resolveAndUpsertEdges(ctx context.Context, syncStore Store, parsedFiles []parsedSyncFile, stats *SyncStats) error {
	implementsEdges, otherByFile := partitionParsedSyncEdges(parsedFiles)
	for _, edgeChunk := range splitEdgeChunks(implementsEdges) {
		resolved, err := edgeresolve.ResolveWithOptions(ctx, syncStore, edgeChunk, s.opts)
		if err != nil {
			return err
		}
		resolved, diagnostics := edgeresolve.FilterResolvedWithDiagnosticsFiltered(resolved, shouldSuppressExternalImportUnresolved)
		mergeSyncUnresolvedDiagnostics(stats, diagnostics)
		if len(resolved) == 0 {
			continue
		}
		if err := syncStore.UpsertEdges(ctx, resolved); err != nil {
			return err
		}
	}
	importsByFile := importEdgesByFile(otherByFile)
	for _, parsed := range parsedFiles {
		edges := otherByFile[parsed.filePath]
		for _, edgeChunk := range splitEdgeChunks(edges) {
			resolveInput := chunkWithImportWarmup(edgeChunk, importsByFile[parsed.filePath])
			resolved, err := edgeresolve.ResolveWithOptions(ctx, syncStore, resolveInput, s.opts)
			if err != nil {
				return err
			}
			resolved, diagnostics := edgeresolve.FilterResolvedWithDiagnosticsFiltered(resolved[len(resolveInput)-len(edgeChunk):], shouldSuppressExternalImportUnresolved)
			mergeSyncUnresolvedDiagnostics(stats, diagnostics)
			if len(resolved) == 0 {
				continue
			}
			if err := syncStore.UpsertEdges(ctx, resolved); err != nil {
				return err
			}
		}
	}
	return nil
}

// mergeSyncUnresolvedDiagnostics folds one chunk's unresolved-edge diagnostics into sync totals.
// @intent keep incremental sync logging aligned with chunked edge resolution output.
// @mutates stats.Unresolved
func mergeSyncUnresolvedDiagnostics(stats *SyncStats, diagnostics edgeresolve.FilterResolvedDiagnostics) {
	if stats == nil || diagnostics.DroppedCount == 0 {
		return
	}
	stats.Unresolved.DroppedCount += diagnostics.DroppedCount
	if len(diagnostics.ByKind) > 0 {
		if stats.Unresolved.ByKind == nil {
			stats.Unresolved.ByKind = make(map[model.EdgeKind]int, len(diagnostics.ByKind))
		}
		for kind, count := range diagnostics.ByKind {
			stats.Unresolved.ByKind[kind] += count
		}
	}
	if len(diagnostics.ByFile) > 0 {
		if stats.Unresolved.ByFile == nil {
			stats.Unresolved.ByFile = make(map[string]int, len(diagnostics.ByFile))
		}
		for filePath, count := range diagnostics.ByFile {
			stats.Unresolved.ByFile[filePath] += count
		}
	}
	if len(diagnostics.ByReason) > 0 {
		if stats.Unresolved.ByReason == nil {
			stats.Unresolved.ByReason = make(map[string]int, len(diagnostics.ByReason))
		}
		for reason, count := range diagnostics.ByReason {
			stats.Unresolved.ByReason[reason] += count
		}
	}
	remaining := 5 - len(stats.Unresolved.Samples)
	if remaining <= 0 || len(diagnostics.Samples) == 0 {
		return
	}
	if remaining > len(diagnostics.Samples) {
		remaining = len(diagnostics.Samples)
	}
	stats.Unresolved.Samples = append(stats.Unresolved.Samples, diagnostics.Samples[:remaining]...)
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

// @intent 로컬 그래프에 의도적으로 없는 외부 패키지 import unresolved를 진단 집계에서 제외한다.
func shouldSuppressExternalImportUnresolved(edge model.Edge, _ string) bool {
	return edge.Kind == model.EdgeKindImportsFrom && edgeresolve.IsLikelyExternalImportEdge(edge)
}

// partitionParsedSyncEdges separates implements edges from per-file edges.
// @intent resolve interface fulfillment before file-local edge chunks that may depend on those relationships.
func partitionParsedSyncEdges(parsedFiles []parsedSyncFile) ([]model.Edge, map[string][]model.Edge) {
	var implementsEdges []model.Edge
	otherByFile := make(map[string][]model.Edge, len(parsedFiles))
	for _, parsed := range parsedFiles {
		for _, edge := range parsed.edges {
			if edge.Kind == model.EdgeKindImplements {
				implementsEdges = append(implementsEdges, edge)
				continue
			}
			otherByFile[parsed.filePath] = append(otherByFile[parsed.filePath], edge)
		}
	}
	return implementsEdges, otherByFile
}

// importEdgesByFile extracts import edges grouped by file path.
// @intent warm call-edge resolution with import context only for files that actually need it.
func importEdgesByFile(edgesByFile map[string][]model.Edge) map[string][]model.Edge {
	imports := make(map[string][]model.Edge, len(edgesByFile))
	for filePath, edges := range edgesByFile {
		for _, edge := range edges {
			if edge.Kind == model.EdgeKindImportsFrom {
				imports[filePath] = append(imports[filePath], edge)
			}
		}
	}
	return imports
}

// chunkWithImportWarmup prefixes one edge chunk with its file's import edges when needed.
// @intent ensure chunked call resolution sees import relationships before resolving dependent call edges.
// @domainRule import edges are prepended only when the chunk contains call edges.
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

// splitEdgeChunks breaks a large edge slice into bounded resolver chunks.
// @intent cap incremental resolution work so large files do not create oversized resolve batches.
func splitEdgeChunks(edges []model.Edge) [][]model.Edge {
	if len(edges) == 0 {
		return nil
	}
	const chunkSize = 400
	chunks := make([][]model.Edge, 0, (len(edges)+chunkSize-1)/chunkSize)
	for start := 0; start < len(edges); start += chunkSize {
		end := start + chunkSize
		if end > len(edges) {
			end = len(edges)
		}
		chunks = append(chunks, edges[start:end])
	}
	return chunks
}

// resolveParser picks an extension-specific parser when configured, otherwise the legacy single parser.
// @intent let multi-language projects sync without losing the single-parser fallback for callers using New.
func (s *Syncer) resolveParser(filePath string) Parser {
	if len(s.parsers) > 0 {
		ext := strings.ToLower(filepath.Ext(filePath))
		if parser, ok := s.parsers[ext]; ok {
			return parser
		}
	}
	return s.parser
}

// restoreAnnotations re-binds parsed comment blocks to the freshly persisted nodes for one file.
// @intent keep doc comments associated with their owning declarations after incremental reparses.
// @sideEffect upserts annotation rows through the store's annotation writer.
// @mutates graph annotations
func (s *Syncer) restoreAnnotations(ctx context.Context, syncStore Store, filePath string, content []byte, nodes []model.Node, comments []treesitter.CommentBlock, language string) error {
	writer, ok := syncStore.(annotationWriter)
	if !ok || language == "" {
		return nil
	}

	binder := parse.NewBinder()
	bindingComments := make([]parse.CommentBlock, len(comments))
	for i, c := range comments {
		bindingComments[i] = parse.CommentBlock{
			StartLine:      c.StartLine,
			EndLine:        c.EndLine,
			Text:           c.Text,
			IsDocstring:    c.IsDocstring,
			OwnerStartLine: c.OwnerStartLine,
		}
	}
	sourceLines := strings.Split(string(content), "\n")
	bindings := binder.Bind(bindingComments, nodes, language, sourceLines)
	if len(bindings) == 0 {
		return nil
	}

	storedNodes, err := syncStore.GetNodesByFile(ctx, filePath)
	if err != nil {
		return err
	}
	storedByKey := make(map[string]*model.Node, len(storedNodes))
	for i := range storedNodes {
		storedByKey[annotationBindingKey(storedNodes[i].QualifiedName, storedNodes[i].StartLine)] = &storedNodes[i]
	}

	for _, binding := range bindings {
		stored := storedByKey[annotationBindingKey(binding.Node.QualifiedName, binding.Node.StartLine)]
		if stored == nil {
			continue
		}
		binding.Annotation.NodeID = stored.ID
		if err := writer.UpsertAnnotation(ctx, binding.Annotation); err != nil {
			return err
		}
	}

	return nil
}

// annotationBindingKey produces a stable lookup key combining qualified name and start line.
// @intent disambiguate overloaded or repeated declarations sharing the same qualified name.
func annotationBindingKey(qualifiedName string, startLine int) string {
	return qualifiedName + ":" + strconv.Itoa(startLine)
}
