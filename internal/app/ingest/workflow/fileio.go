// @index Filesystem walking, per-file parsing, and unreadable-file accounting.
package workflow

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	ingestapp "github.com/tae2089/code-context-graph/internal/app/ingest"
	"github.com/tae2089/code-context-graph/internal/app/ingest/binding"
	"github.com/tae2089/code-context-graph/internal/domain/graph"
	"github.com/tae2089/code-context-graph/internal/pathspec"
)

// inspectRegularSourceFile reads source metadata without following symlinks.
// @intent reject symlink and non-regular source paths before any parser or package discoverer can read target bytes.
func inspectRegularSourceFile(path string) (os.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("source symlink is not allowed: %s", path)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("source path is not a regular file: %s", path)
	}
	return info, nil
}

// openRegularSourceFile opens a source only when its no-follow identity remains stable across inspection and open.
// @intent prevent replacement races from turning a validated regular path into a followed symlink before reading.
func openRegularSourceFile(path string) (*os.File, os.FileInfo, error) {
	before, err := inspectRegularSourceFile(path)
	if err != nil {
		return nil, nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	after, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, nil, err
	}
	if !after.Mode().IsRegular() || !os.SameFile(before, after) {
		_ = file.Close()
		return nil, nil, fmt.Errorf("source file changed while opening: %s", path)
	}
	return file, after, nil
}

// readRegularSourceFile reads bytes from a verified regular-file descriptor.
// @intent keep all secondary source reads on the same no-follow path as build and update ingestion.
func readRegularSourceFile(path string) ([]byte, error) {
	file, _, err := openRegularSourceFile(path)
	if err != nil {
		return nil, err
	}
	content, readErr := io.ReadAll(file)
	closeErr := file.Close()
	if readErr != nil {
		return nil, readErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	return content, nil
}

// shouldSkipDir reports whether the source walker must skip a directory name.
// @intent keep default source traversal exclusions local to the ingest workflow.
// @domainRule .git, vendor, node_modules, and hidden directories except . are skipped.
func shouldSkipDir(name string) bool {
	switch name {
	case ".git", "vendor", "node_modules":
		return true
	}
	return name != "." && strings.HasPrefix(name, ".")
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
			if shouldSkipDir(info.Name()) || pathspec.MatchExcludes(opts.ExcludePatterns, relPath) {
				return filepath.SkipDir
			}
			if len(opts.IncludePaths) > 0 && path != absDir && !pathspec.MatchIncludePaths(relPath, opts.IncludePaths) {
				return filepath.SkipDir
			}
			return nil
		}

		if pathspec.MatchExcludes(opts.ExcludePatterns, relPath) {
			return nil
		}
		if len(opts.IncludePaths) > 0 && !pathspec.MatchIncludePaths(relPath, opts.IncludePaths) {
			return nil
		}
		return fn(path, relPath)
	})
}

// parseForBuild parses one source file using the comment-aware parser when available.
// @intent surface comment blocks and language alongside nodes/edges so the binder can attach annotations.
func parseForBuild(ctx context.Context, parser Parser, relPath string, content []byte) ([]graph.Node, []graph.Edge, []ingestapp.CommentBlock, ingestapp.ParseMetadata, string, error) {
	if mp, ok := parser.(metadataParserWithLanguage); ok {
		nodes, edges, comments, meta, err := mp.ParseWithCommentsAndMetadata(ctx, relPath, content)
		return nodes, edges, comments, meta, mp.Language(), err
	}
	nodes, edges, err := parser.ParseWithContext(ctx, relPath, content)
	return nodes, edges, nil, ingestapp.ParseMetadata{}, "", err
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
func toBinderComments(tsComments []ingestapp.CommentBlock) []binding.CommentBlock {
	out := make([]binding.CommentBlock, len(tsComments))
	for i, c := range tsComments {
		out[i] = binding.CommentBlock{
			StartLine:      c.StartLine,
			EndLine:        c.EndLine,
			Text:           c.Text,
			IsDocstring:    c.IsDocstring,
			OwnerStartLine: c.OwnerStartLine,
		}
	}
	return out
}
