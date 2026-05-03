package incremental

import (
	"context"
	"log/slog"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/tae2089/code-context-graph/internal/model"
	"github.com/tae2089/code-context-graph/internal/parse"
	"github.com/tae2089/code-context-graph/internal/parse/treesitter"
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
	Added    int
	Modified int
	Skipped  int
	Deleted  int
}

// Syncer incrementally updates graph data for changed files.
// @intent avoid full rebuilds by reparsing only files whose content hash changed
type Syncer struct {
	store   Store
	parser  Parser
	parsers map[string]Parser
	logger  *slog.Logger
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

	for filePath, info := range files {
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

		var nodes []model.Node
		var edges []model.Edge
		var comments []treesitter.CommentBlock
		language := ""

		if annotatingParser, ok := parser.(AnnotatingParser); ok {
			nodes, edges, comments, err = annotatingParser.ParseWithComments(ctx, filePath, info.Content)
			language = annotatingParser.Language()
		} else {
			nodes, edges, err = parser.Parse(filePath, info.Content)
		}
		if err != nil {
			return nil, err
		}

		if len(existing) > 0 {
			if err := syncStore.DeleteNodesByFile(ctx, filePath); err != nil {
				return nil, err
			}
			s.logger.Debug("file modified", "file", filePath)
			stats.Modified++
		} else {
			s.logger.Debug("file added", "file", filePath)
			stats.Added++
		}

		if len(nodes) > 0 {
			if err := syncStore.UpsertNodes(ctx, nodes); err != nil {
				return nil, err
			}
			if len(comments) > 0 {
				if err := s.restoreAnnotations(ctx, syncStore, filePath, info.Content, nodes, comments, language); err != nil {
					return nil, err
				}
			}
		}
		if len(edges) > 0 {
			if err := syncStore.UpsertEdges(ctx, edges); err != nil {
				return nil, err
			}
		}
		releaseContent(files, filePath)
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
	)

	return stats, nil
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
