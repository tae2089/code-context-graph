// @index Filesystem walking, per-file parsing, and unreadable-file accounting.
package service

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/tae2089/code-context-graph/internal/model"
	"github.com/tae2089/code-context-graph/internal/parse"
	"github.com/tae2089/code-context-graph/internal/parse/treesitter"
	"github.com/tae2089/code-context-graph/internal/pathutil"
)

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
func parseForBuild(ctx context.Context, parser Parser, relPath string, content []byte) ([]model.Node, []model.Edge, []treesitter.CommentBlock, treesitter.ParseMetadata, string, error) {
	if mp, ok := parser.(metadataParserWithLanguage); ok {
		nodes, edges, comments, meta, err := mp.ParseWithCommentsAndMetadata(ctx, relPath, content)
		return nodes, edges, comments, meta, mp.Language(), err
	}
	if cp, ok := parser.(commentParserWithLanguage); ok {
		nodes, edges, comments, err := cp.ParseWithComments(ctx, relPath, content)
		return nodes, edges, comments, treesitter.ParseMetadata{}, cp.Language(), err
	}
	nodes, edges, err := parser.ParseWithContext(ctx, relPath, content)
	return nodes, edges, nil, treesitter.ParseMetadata{}, "", err
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
