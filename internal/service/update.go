// @index Incremental update pipeline: spool replay, deletions, and affected-node tracking.
package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"gorm.io/gorm"

	"github.com/tae2089/trace"

	"github.com/tae2089/code-context-graph/internal/analysis/incremental"
	"github.com/tae2089/code-context-graph/internal/ctxns"
	"github.com/tae2089/code-context-graph/internal/edgeresolve"
	"github.com/tae2089/code-context-graph/internal/model"
	"github.com/tae2089/code-context-graph/internal/store"
)

// Update incrementally syncs changed files into the graph and optionally rebuilds search.
// @intent centralize file collection, include path, parse limit, and search policy for update callers
func (s *GraphService) Update(ctx context.Context, opts UpdateOptions) (*incremental.SyncStats, error) {
	if opts.Syncer == nil {
		return nil, trace.New("incremental syncer is not configured")
	}
	if syncerWithResolveOptions, ok := opts.Syncer.(interface {
		SetResolveOptions(edgeresolve.ResolveOptions)
	}); ok {
		syncerWithResolveOptions.SetResolveOptions(edgeresolve.ResolveOptions{FallbackCalls: opts.FallbackCalls})
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
	if err := upsertPackageNodes(ctx, txStore, spool.packages); err != nil {
		return nil, trace.Wrap(err, "upsert package nodes")
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
	changedFiles := affectedUpdateFiles(spool.currentHashes, existingNodesByFile, spool.forceFiles)
	if err := s.refreshPackageSemanticEdges(ctx, txStore, txDB, opts.Dir, spool.packages, changedFiles, deletedFiles, edgeresolve.ResolveOptions{FallbackCalls: opts.FallbackCalls}); err != nil {
		return nil, trace.Wrap(err, "refresh package semantic edges")
	}
	if err := upsertPackageContainsEdges(ctx, txStore, spool.packages); err != nil {
		return nil, trace.Wrap(err, "upsert package file edges")
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

// @intent run incremental sync without a shared DB transaction while replaying spooled file batches to bound memory.
func (s *GraphService) updateGraphWithoutTx(ctx context.Context, absDir string, opts UpdateOptions, packages map[string]languagePackageInfo, spool *updateSpool) (*incremental.SyncStats, error) {
	existingFiles, existingNodesByFile, err := existingGraphFileState(ctx, s.DB)
	if err != nil {
		return nil, trace.Wrap(err, "load existing graph files")
	}
	if !opts.Replace && len(opts.IncludePaths) > 0 {
		existingFiles, existingNodesByFile = filterExistingStateByInclude(existingFiles, existingNodesByFile, opts.IncludePaths)
	}
	forceFiles, err := forceReparseFiles(ctx, s.DB, existingNodesByFile, spool.currentHashes)
	if err != nil {
		return nil, trace.Wrap(err, "load edge source files for changed graph")
	}
	spool.forceFiles = forceFiles
	if s.Store != nil {
		if err := upsertPackageNodes(ctx, s.Store, packages); err != nil {
			return nil, trace.Wrap(err, "upsert package nodes")
		}
	}

	stats := &incremental.SyncStats{}
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
	deletedFiles := existingFilesMissingFromSet(spool.currentFiles, existingFiles)
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
	changedFiles := affectedUpdateFiles(spool.currentHashes, existingNodesByFile, spool.forceFiles)
	if err := s.refreshPackageSemanticEdges(ctx, s.Store, s.DB, absDir, packages, changedFiles, deletedFiles, edgeresolve.ResolveOptions{FallbackCalls: opts.FallbackCalls}); err != nil {
		return nil, trace.Wrap(err, "refresh package semantic edges")
	}
	if s.Store != nil {
		if err := upsertPackageContainsEdges(ctx, s.Store, packages); err != nil {
			return nil, trace.Wrap(err, "upsert package file edges")
		}
	}
	if !opts.SkipSearchRebuild {
		nodeIDs, err := affectedNodeIDsForUpdate(ctx, s.DB, existingNodesByFile, changedFiles, deletedFiles)
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
	mergeFilterResolvedDiagnostics(&dst.Unresolved, src.Unresolved)
}

// txDBForExistingFiles picks the transaction-scoped DB if available, otherwise falls back to the base handle.
// @intent keep existing-file lookups inside the same transaction as graph writes when one is active.
func txDBForExistingFiles(txDB, db *gorm.DB) *gorm.DB {
	if txDB != nil {
		return txDB
	}
	return db
}
