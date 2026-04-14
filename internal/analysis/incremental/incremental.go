package incremental

import (
	"context"
	"log/slog"

	"github.com/imtaebin/code-context-graph/internal/model"
)

type Store interface {
	GetNodesByFile(ctx context.Context, filePath string) ([]model.Node, error)
	GetNodesByFiles(ctx context.Context, filePaths []string) (map[string][]model.Node, error)
	UpsertNodes(ctx context.Context, nodes []model.Node) error
	UpsertEdges(ctx context.Context, edges []model.Edge) error
	DeleteNodesByFile(ctx context.Context, filePath string) error
}

type Parser interface {
	Parse(filePath string, content []byte) ([]model.Node, []model.Edge, error)
}

type FileInfo struct {
	Hash    string
	Content []byte
}

type SyncStats struct {
	Added    int
	Modified int
	Skipped  int
	Deleted  int
}

type Syncer struct {
	store  Store
	parser Parser
	logger *slog.Logger
}

type SyncerOption func(*Syncer)

func WithLogger(l *slog.Logger) SyncerOption {
	return func(s *Syncer) {
		s.logger = l
	}
}

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

func (s *Syncer) Sync(ctx context.Context, files map[string]FileInfo) (*SyncStats, error) {
	return s.SyncWithExisting(ctx, files, nil)
}

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
