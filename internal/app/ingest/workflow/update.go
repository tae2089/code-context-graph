// @index Incremental update pipeline: spool replay, deletions, and affected-node tracking.
package workflow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/tae2089/trace"

	"github.com/tae2089/code-context-graph/internal/app/ingest"
	"github.com/tae2089/code-context-graph/internal/app/ingest/resolve"
	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

// Update incrementally syncs changed files into the graph and optionally rebuilds search.
// @intent centralize file collection, include path, parse limit, and search policy for update callers
func (s *Service) Update(ctx context.Context, opts UpdateOptions) (*ingest.SyncStats, error) {
	if opts.Syncer == nil {
		return nil, trace.New("incremental syncer is not configured")
	}
	if syncerWithResolveOptions, ok := opts.Syncer.(interface {
		SetResolveOptions(resolve.ResolveOptions)
	}); ok {
		syncerWithResolveOptions.SetResolveOptions(resolve.ResolveOptions{FallbackCalls: opts.FallbackCalls})
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
	packages := s.collectLanguagePackages(ctx, absDir, opts.BuildOptions)
	ctx = s.withImportPackageContext(ctx, packages)

	if _, ok := opts.Syncer.(transactionalIncrementalSyncer); !ok || s.Store == nil {
		spool, err := s.prepareUpdateSpool(ctx, absDir, opts)
		if err != nil {
			return nil, err
		}
		defer spool.cleanup(s.logger())
		spool.packages = packages
		return s.updateGraphWithoutTx(ctx, absDir, opts, packages, spool)
	}

	spool, err := s.prepareUpdateSpool(ctx, absDir, opts)
	if err != nil {
		return nil, err
	}
	defer spool.cleanup(s.logger())
	spool.packages = packages

	outcome := updateOutcome{stats: &ingest.SyncStats{}}
	err = s.withUpdateTx(ctx, func(tx ingest.Transaction) error {
		outcome, err = s.applyUpdateSpoolInTx(ctx, tx, opts, spool)
		return err
	})
	if err != nil {
		return nil, err
	}
	if outcome.fullBuild {
		spool.cleanup(s.logger())
		return s.buildForUpdate(ctx, opts.BuildOptions, outcome.stats)
	}
	return outcome.stats, nil
}

// @intent carry the transaction-scoped update decision out to the orchestration layer without exposing a public result type.
type updateOutcome struct {
	stats     *ingest.SyncStats
	fullBuild bool
}

// buildForUpdate executes a safe full-build fallback while preserving incremental change-count semantics.
// @intent use the faster full-build write path for new packages without changing the Update result contract.
// @sideEffect rebuilds the graph and search index through Service.Build.
func (s *Service) buildForUpdate(ctx context.Context, opts BuildOptions, stats *ingest.SyncStats) (*ingest.SyncStats, error) {
	s.logger().Info("new package or source directory detected; switching update to full build", "dir", opts.Dir)
	buildStats, err := s.Build(ctx, opts)
	if err != nil {
		return nil, trace.Wrap(err, "full build fallback")
	}
	stats.Unresolved = buildStats.Unresolved
	return stats, nil
}

// canBuildForUpdate reports whether a full rebuild preserves the caller's requested graph scope.
// @intent prevent partial non-replacing updates from deleting graph data outside their include paths.
func (s *Service) canBuildForUpdate(opts UpdateOptions) bool {
	return s.Store != nil && s.UnitOfWork != nil && (opts.Replace || len(opts.IncludePaths) == 0)
}

// classifyUpdateSnapshot preserves incremental file outcome counts when execution switches to a full build.
// @intent keep Added/Modified/Skipped/Deleted based on source changes rather than the chosen persistence path.
func classifyUpdateSnapshot(existingFiles []string, existingNodesByFile map[string][]graph.Node, currentHashes map[string]string) *ingest.SyncStats {
	stats := &ingest.SyncStats{}
	for filePath, currentHash := range currentHashes {
		existing := existingNodesByFile[filePath]
		switch {
		case len(existing) == 0:
			stats.Added++
		case existing[0].Hash == currentHash:
			stats.Skipped++
		default:
			stats.Modified++
		}
	}
	for _, filePath := range existingFiles {
		if _, present := currentHashes[filePath]; !present {
			stats.Deleted++
		}
	}
	return stats
}

// withUpdateTx selects the right transaction scope for incremental update based on syncer and store capability.
// @intent prefer a single coupled tx for graph and search rebuild while gracefully degrading when the syncer or store cannot participate.
func (s *Service) withUpdateTx(ctx context.Context, fn func(ingest.Transaction) error) error {
	if s.UnitOfWork == nil {
		return trace.New("ingest unit of work is not configured")
	}
	return s.UnitOfWork.WithinTransaction(ctx, fn)
}

// @intent capture the current update input set and file hashes before transactional incremental sync begins.
func (s *Service) prepareUpdateSpool(ctx context.Context, absDir string, opts UpdateOptions) (*updateSpool, error) {
	dir, err := os.MkdirTemp("", "ccg-update-spool-*")
	if err != nil {
		return nil, trace.Wrap(err, "create update spool")
	}
	spool := &updateSpool{
		dir:           dir,
		currentFiles:  make(map[string]struct{}),
		currentHashes: make(map[string]string),
	}
	batch := make(map[string]ingest.FileInfo)
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
		batch = make(map[string]ingest.FileInfo)
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
		batch[relPath] = ingest.FileInfo{
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
// @intent prefer staged reconciliation so every changed or forced file node is current before any cross-file edge is resolved.
// @sideEffect adds, modifies, and deletes graph nodes/edges/annotations for changed files and refreshes affected search documents.
// @mutates graph nodes, edges, annotations, and search_documents
func (s *Service) applyUpdateSpoolInTx(ctx context.Context, tx ingest.Transaction, opts UpdateOptions, spool *updateSpool) (updateOutcome, error) {
	txStore := tx.Graph()
	if txStore == nil {
		return updateOutcome{}, trace.New("incremental update requires transaction-scoped store")
	}

	syncer := opts.Syncer
	stats := &ingest.SyncStats{}
	existingFiles, existingNodesByFile, err := existingGraphFileState(ctx, txStore)
	if err != nil {
		return updateOutcome{}, trace.Wrap(err, "load existing graph files")
	}
	if !opts.Replace && len(opts.IncludePaths) > 0 {
		existingFiles, existingNodesByFile = filterExistingStateByInclude(existingFiles, existingNodesByFile, opts.IncludePaths)
	}
	forceFiles, err := forceReparseFiles(ctx, txStore, existingNodesByFile, spool.currentHashes)
	if err != nil {
		return updateOutcome{}, trace.Wrap(err, "load edge source files for changed graph")
	}
	requiresFullBuild := addUnchangedPeersForAddedFiles(forceFiles, spool.packages, existingNodesByFile, spool.currentHashes)
	useSemiNaiveReplay := false
	if requiresFullBuild && s.canBuildForUpdate(opts) {
		version, versioned := s.unresolvedIndexVersion()
		if unresolvedStore, ok := txStore.(ingest.UnresolvedEdgeStore); ok && versioned {
			ready, readyErr := unresolvedStore.UnresolvedIndexReady(ctx, version)
			if readyErr != nil {
				return updateOutcome{}, trace.Wrap(readyErr, "check unresolved edge index readiness")
			}
			useSemiNaiveReplay = ready
		}
	}
	if requiresFullBuild && s.canBuildForUpdate(opts) && !useSemiNaiveReplay {
		return updateOutcome{
			stats:     classifyUpdateSnapshot(existingFiles, existingNodesByFile, spool.currentHashes),
			fullBuild: true,
		}, nil
	}
	if requiresFullBuild && !useSemiNaiveReplay {
		addAllUnchangedFiles(forceFiles, existingNodesByFile, spool.currentHashes)
	}
	if err := upsertPackageNodes(ctx, txStore, spool.packages); err != nil {
		return updateOutcome{}, trace.Wrap(err, "upsert package nodes")
	}
	spool.forceFiles = forceFiles
	deletedFiles := make([]string, 0, len(existingFiles))
	for _, fp := range existingFiles {
		if _, ok := spool.currentFiles[fp]; !ok {
			deletedFiles = append(deletedFiles, fp)
		}
	}
	if stagedSyncer, ok := syncer.(transactionalBatchIncrementalSyncer); ok {
		stats, err = stagedSyncer.SyncBatchesWithExistingStore(ctx, txStore, newUpdateSpoolBatchSource(ctx, spool), deletedFiles)
		if err != nil {
			return updateOutcome{}, trace.Wrap(err, "staged incremental sync")
		}
	} else {
		for _, path := range spool.records {
			record, err := spool.readRecord(path)
			if err != nil {
				return updateOutcome{}, err
			}
			normalFiles, _ := splitForcedFiles(record.Files, spool.forceFiles)
			if len(normalFiles) == 0 {
				continue
			}
			batchStats, err := syncIncrementalBatch(ctx, syncer, txStore, normalFiles, nil)
			if err != nil {
				return updateOutcome{}, trace.Wrap(err, "incremental sync")
			}
			addSyncStats(stats, batchStats)
		}
		if len(deletedFiles) > 0 {
			batchStats, err := syncIncrementalBatch(ctx, syncer, txStore, nil, deletedFiles)
			if err != nil {
				return updateOutcome{}, trace.Wrap(err, "incremental sync")
			}
			addSyncStats(stats, batchStats)
		}

		for _, path := range spool.records {
			record, err := spool.readRecord(path)
			if err != nil {
				return updateOutcome{}, err
			}
			_, forcedFiles := splitForcedFiles(record.Files, spool.forceFiles)
			if len(forcedFiles) == 0 {
				continue
			}
			batchStats, err := syncIncrementalBatch(ctx, syncer, txStore, forcedFiles, nil)
			if err != nil {
				return updateOutcome{}, trace.Wrap(err, "incremental force sync")
			}
			addSyncStats(stats, batchStats)
		}
	}
	changedFiles := affectedUpdateFiles(spool.currentHashes, existingNodesByFile, spool.forceFiles)
	if err := s.refreshPackageSemanticEdges(ctx, txStore, opts.Dir, spool.packages, changedFiles, deletedFiles, resolve.ResolveOptions{FallbackCalls: opts.FallbackCalls}); err != nil {
		return updateOutcome{}, trace.Wrap(err, "refresh package semantic edges")
	}
	if err := upsertPackageContainsEdges(ctx, txStore, spool.packages); err != nil {
		return updateOutcome{}, trace.Wrap(err, "upsert package file edges")
	}
	if useSemiNaiveReplay {
		addedFiles := addedUpdateFiles(existingNodesByFile, spool.currentHashes)
		if err := replayUnresolvedEdgesForAddedFiles(ctx, txStore, addedFiles, resolve.ResolveOptions{FallbackCalls: opts.FallbackCalls}, stats); err != nil {
			return updateOutcome{}, err
		}
	}

	if !opts.SkipSearchRebuild {
		nodeIDs, err := affectedNodeIDsForUpdate(ctx, txStore, existingNodesByFile, affectedUpdateFiles(spool.currentHashes, existingNodesByFile, spool.forceFiles), deletedFiles)
		if err != nil {
			return updateOutcome{}, trace.Wrap(err, "load affected search nodes")
		}
		if err := tx.Search().RebuildNodes(ctx, nodeIDs); err != nil {
			return updateOutcome{}, err
		}
	}
	return updateOutcome{stats: stats}, nil
}

// addedUpdateFiles returns current paths that had no persisted nodes before the update.
// @intent seed semi-naive unresolved lookup from newly introduced source files only.
func addedUpdateFiles(existingNodesByFile map[string][]graph.Node, currentHashes map[string]string) []string {
	files := make([]string, 0)
	for filePath := range currentHashes {
		if len(existingNodesByFile[filePath]) == 0 {
			files = append(files, filePath)
		}
	}
	slices.Sort(files)
	return files
}

// replayUnresolvedEdgesForAddedFiles resolves only unchanged-source candidates selected by newly added symbols.
// @intent replace graph-wide reparsing with reverse-index-driven edge reconciliation for new packages.
// @sideEffect reads unresolved candidates, upserts newly resolved edges, and removes resolved candidate rows.
func replayUnresolvedEdgesForAddedFiles(ctx context.Context, graphStore ingest.GraphStore, addedFiles []string, options resolve.ResolveOptions, stats *ingest.SyncStats) error {
	unresolvedStore, ok := graphStore.(ingest.UnresolvedEdgeStore)
	if !ok || len(addedFiles) == 0 {
		return nil
	}
	nodesByFile, err := graphStore.GetNodesByFiles(ctx, addedFiles)
	if err != nil {
		return trace.Wrap(err, "load added nodes for unresolved replay")
	}
	var addedNodes []graph.Node
	for _, filePath := range addedFiles {
		addedNodes = append(addedNodes, nodesByFile[filePath]...)
	}
	matched, err := unresolvedStore.FindUnresolvedEdgesByLookupKeys(ctx, resolve.LookupKeysForNodes(addedNodes))
	if err != nil {
		return trace.Wrap(err, "select unresolved edges by added symbols")
	}
	if len(matched) == 0 {
		return nil
	}
	fileSet := make(map[string]struct{})
	for _, edge := range matched {
		fileSet[edge.FilePath] = struct{}{}
	}
	files := make([]string, 0, len(fileSet))
	for filePath := range fileSet {
		files = append(files, filePath)
	}
	slices.Sort(files)
	candidates, err := unresolvedStore.FindUnresolvedEdgesByFiles(ctx, files)
	if err != nil {
		return trace.Wrap(err, "load affected unresolved source edges")
	}
	implementsEdges, otherEdges := splitImplementsEdges(candidates)
	for _, phase := range [][]graph.Edge{implementsEdges, otherEdges} {
		if len(phase) == 0 {
			continue
		}
		resolvedEdges, err := resolve.ResolveWithOptions(ctx, graphStore, phase, options)
		if err != nil {
			return trace.Wrap(err, "resolve semi-naive edge candidates")
		}
		resolvedEdges, _, diagnostics := resolve.PartitionResolvedWithDiagnosticsFiltered(resolvedEdges, shouldSuppressExternalImportUnresolved)
		mergeFilterResolvedDiagnostics(&stats.Unresolved, diagnostics)
		if len(resolvedEdges) == 0 {
			continue
		}
		if err := graphStore.UpsertEdges(ctx, resolvedEdges); err != nil {
			return trace.Wrap(err, "upsert semi-naive resolved edges")
		}
		fingerprints := make([]string, 0, len(resolvedEdges))
		for _, edge := range resolvedEdges {
			fingerprints = append(fingerprints, edge.Fingerprint)
		}
		if err := unresolvedStore.DeleteUnresolvedEdgesByFingerprints(ctx, fingerprints); err != nil {
			return trace.Wrap(err, "delete resolved semi-naive candidates")
		}
	}
	return nil
}

// @intent run incremental sync without a shared DB transaction while replaying spooled file batches to bound memory.
func (s *Service) updateGraphWithoutTx(ctx context.Context, absDir string, opts UpdateOptions, packages map[string]languagePackageInfo, spool *updateSpool) (*ingest.SyncStats, error) {
	existingFiles, existingNodesByFile, err := existingGraphFileState(ctx, s.Store)
	if err != nil {
		return nil, trace.Wrap(err, "load existing graph files")
	}
	if !opts.Replace && len(opts.IncludePaths) > 0 {
		existingFiles, existingNodesByFile = filterExistingStateByInclude(existingFiles, existingNodesByFile, opts.IncludePaths)
	}
	forceFiles, err := forceReparseFiles(ctx, s.Store, existingNodesByFile, spool.currentHashes)
	if err != nil {
		return nil, trace.Wrap(err, "load edge source files for changed graph")
	}
	requiresFullBuild := addUnchangedPeersForAddedFiles(forceFiles, packages, existingNodesByFile, spool.currentHashes)
	if requiresFullBuild {
		addAllUnchangedFiles(forceFiles, existingNodesByFile, spool.currentHashes)
	}
	spool.forceFiles = forceFiles
	if s.Store != nil {
		if err := upsertPackageNodes(ctx, s.Store, packages); err != nil {
			return nil, trace.Wrap(err, "upsert package nodes")
		}
	}

	deletedFiles := existingFilesMissingFromSet(spool.currentFiles, existingFiles)
	stats := &ingest.SyncStats{}
	if stagedSyncer, ok := opts.Syncer.(batchIncrementalSyncer); ok {
		stats, err = stagedSyncer.SyncBatchesWithExisting(ctx, newUpdateSpoolBatchSource(ctx, spool), deletedFiles)
		if err != nil {
			return nil, trace.Wrap(err, "staged incremental sync")
		}
	} else {
		// Normal batches must never carry existingFiles: SyncWithExisting deletes existing files
		// absent from the batch, so a multi-record spool would delete files belonging to later
		// batches (then re-add them, churning node IDs and stats). Deletions are handled once,
		// explicitly, below — mirroring the transactional path (applyUpdateSpoolInTx).
		for _, path := range spool.records {
			record, err := spool.readRecord(path)
			if err != nil {
				return nil, err
			}
			normalFiles, _ := splitForcedFiles(record.Files, spool.forceFiles)
			if len(normalFiles) == 0 {
				continue
			}
			batchStats, err := opts.Syncer.SyncWithExisting(ctx, normalFiles, nil)
			if err != nil {
				return nil, trace.Wrap(err, "incremental sync")
			}
			addSyncStats(stats, batchStats)
		}
		if len(deletedFiles) > 0 {
			batchStats, err := opts.Syncer.SyncWithExisting(ctx, nil, deletedFiles)
			if err != nil {
				return nil, trace.Wrap(err, "incremental delete sync")
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
			batchStats, err := opts.Syncer.SyncWithExisting(ctx, forcedFiles, nil)
			if err != nil {
				return nil, trace.Wrap(err, "incremental force sync")
			}
			addSyncStats(stats, batchStats)
		}
	}
	changedFiles := affectedUpdateFiles(spool.currentHashes, existingNodesByFile, spool.forceFiles)
	if err := s.refreshPackageSemanticEdges(ctx, s.Store, absDir, packages, changedFiles, deletedFiles, resolve.ResolveOptions{FallbackCalls: opts.FallbackCalls}); err != nil {
		return nil, trace.Wrap(err, "refresh package semantic edges")
	}
	if s.Store != nil {
		if err := upsertPackageContainsEdges(ctx, s.Store, packages); err != nil {
			return nil, trace.Wrap(err, "upsert package file edges")
		}
	}
	if !opts.SkipSearchRebuild {
		nodeIDs, err := affectedNodeIDsForUpdate(ctx, s.Store, existingNodesByFile, changedFiles, deletedFiles)
		if err != nil {
			return nil, trace.Wrap(err, "load affected search nodes")
		}
		if s.Search != nil {
			if err := s.Search.RebuildNodes(ctx, nodeIDs); err != nil {
				return nil, err
			}
		}
	}
	return stats, nil
}

// newUpdateSpoolBatchSource replays all current update inputs while marking forced reparses in their original batch.
// @intent let a staged syncer own cross-batch ordering without loading the entire source snapshot into memory.
func newUpdateSpoolBatchSource(ctx context.Context, spool *updateSpool) ingest.FileBatchSource {
	return func(visitor ingest.FileBatchVisitor) error {
		for _, path := range spool.records {
			if err := ctx.Err(); err != nil {
				return err
			}
			record, err := spool.readRecord(path)
			if err != nil {
				return err
			}
			for filePath, info := range record.Files {
				if _, forced := spool.forceFiles[filePath]; forced {
					info.Force = true
					record.Files[filePath] = info
				}
			}
			if err := visitor(record.Files); err != nil {
				return err
			}
		}
		return nil
	}
}

// affectedUpdateFiles selects files whose stored hash differs from the current input or that are forced to reparse.
// @intent identify which files contributed nodes that need to be re-indexed for search after an incremental update.
func affectedUpdateFiles(currentHashes map[string]string, existingNodesByFile map[string][]graph.Node, forceFiles map[string]struct{}) []string {
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

// @intent detect deleted paths from the spool snapshot without rebuilding a full in-memory FileInfo map.
func existingFilesMissingFromSet(currentFiles map[string]struct{}, existingFiles []string) []string {
	deleted := make([]string, 0)
	for _, fp := range existingFiles {
		if _, ok := currentFiles[fp]; !ok {
			deleted = append(deleted, fp)
		}
	}
	return deleted
}

// affectedNodeIDsForUpdate collects node IDs whose search documents must be refreshed for a given change set.
// @intent merge previously stored node IDs with newly created ones so the search index sees both removals and additions.
func affectedNodeIDsForUpdate(ctx context.Context, graphStore ingest.GraphStore, existingNodesByFile map[string][]graph.Node, changedFiles, deletedFiles []string) ([]uint, error) {
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
	currentIDs, err := currentNodeIDsForFiles(ctx, graphStore, changedFiles)
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
func currentNodeIDsForFiles(ctx context.Context, graphStore ingest.GraphStore, filePaths []string) ([]uint, error) {
	if graphStore == nil || len(filePaths) == 0 {
		return nil, nil
	}
	var ids []uint
	for start := 0; start < len(filePaths); start += scopedINQueryChunkSize {
		end := min(start+scopedINQueryChunkSize, len(filePaths))
		chunk := filePaths[start:end]
		nodesByFile, err := graphStore.GetNodesByFiles(ctx, chunk)
		if err != nil {
			return nil, err
		}
		for _, nodes := range nodesByFile {
			for _, node := range nodes {
				ids = append(ids, node.ID)
			}
		}
	}
	return ids, nil
}

// syncIncrementalBatch dispatches one batch to the configured incremental syncer using a transaction store when available.
// @intent route changes through the transactional syncer so all updates land in the same DB transaction as graph writes.
func syncIncrementalBatch(ctx context.Context, syncer IncrementalSyncer, txStore ingest.GraphStore, files map[string]ingest.FileInfo, existingFiles []string) (*ingest.SyncStats, error) {
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
func addSyncStats(dst, src *ingest.SyncStats) {
	if dst == nil || src == nil {
		return
	}
	dst.Added += src.Added
	dst.Modified += src.Modified
	dst.Skipped += src.Skipped
	dst.Deleted += src.Deleted
	mergeFilterResolvedDiagnostics(&dst.Unresolved, src.Unresolved)
}
