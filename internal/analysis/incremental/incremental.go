package incremental

import (
	"context"
	"log/slog"

	"github.com/imtaebin/code-context-graph/internal/model"
)

// Store defines persistence operations needed for incremental sync.
// @intent abstract graph storage so changed files can be reparsed and upserted
type Store interface {
	GetNodesByFile(ctx context.Context, filePath string) ([]model.Node, error)
	GetNodesByFiles(ctx context.Context, filePaths []string) (map[string][]model.Node, error)
	UpsertNodes(ctx context.Context, nodes []model.Node) error
	UpsertEdges(ctx context.Context, edges []model.Edge) error
	DeleteNodesByFile(ctx context.Context, filePath string) error
}

// Parser parses one file into graph nodes and edges.
// @intent decouple incremental sync from language-specific parsing logic
type Parser interface {
	Parse(filePath string, content []byte) ([]model.Node, []model.Edge, error)
}

// FileInfo holds change-tracking data for one file.
// @intent carry file content and hash so sync can detect modifications cheaply
type FileInfo struct {
	Hash    string
	Content []byte
}

// SyncStats summarizes one incremental sync run.
// @intent report how many files were added, modified, skipped, or deleted
type SyncStats struct {
	Added    int
	Modified int
	Skipped  int
	Deleted  int
}

// Syncer incrementally updates graph data for changed files.
// @intent avoid full rebuilds by reparsing only files whose content hash changed
type Syncer struct {
	store  Store
	parser Parser
	logger *slog.Logger
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
	stats := &SyncStats{}

	s.logger.Info("sync started", "file_count", len(files), "existing_count", len(existingFiles))

	filePaths := make([]string, 0, len(files))
	for fp := range files {
		filePaths = append(filePaths, fp)
	}
	existingByFile, err := s.store.GetNodesByFiles(ctx, filePaths)
	if err != nil {
		return nil, err
	}

	for filePath, info := range files {
		existing := existingByFile[filePath]

		if len(existing) > 0 && existing[0].Hash == info.Hash {
			s.logger.Debug("file skipped (unchanged)", "file", filePath)
			stats.Skipped++
			continue
		}

		if len(existing) > 0 {
			if err := s.store.DeleteNodesByFile(ctx, filePath); err != nil {
				return nil, err
			}
			s.logger.Debug("file modified", "file", filePath)
			stats.Modified++
		} else {
			s.logger.Debug("file added", "file", filePath)
			stats.Added++
		}

		nodes, edges, err := s.parser.Parse(filePath, info.Content)
		if err != nil {
			return nil, err
		}
		if len(nodes) > 0 {
			if err := s.store.UpsertNodes(ctx, nodes); err != nil {
				return nil, err
			}
		}
		if len(edges) > 0 {
			if err := s.store.UpsertEdges(ctx, edges); err != nil {
				return nil, err
			}
		}
	}

	for _, ep := range existingFiles {
		if _, stillPresent := files[ep]; !stillPresent {
			if err := s.store.DeleteNodesByFile(ctx, ep); err != nil {
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
	)

	return stats, nil
}
