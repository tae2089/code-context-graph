// @index Atomic filesystem adapter for built-in Wiki compatibility snapshots.
package contentfiles

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/tae2089/trace"

	"github.com/tae2089/code-context-graph/internal/app/wiki"
	requestctx "github.com/tae2089/code-context-graph/internal/ctx"
)

// WikiIndexWriter maps namespaces to compatibility snapshot paths and atomically replaces JSON.
// @intent prevent readers from observing partial built-in Wiki index snapshots.
type WikiIndexWriter struct{ Root string }

var _ wiki.IndexWriter = (*WikiIndexWriter)(nil)

// NewWikiIndexWriter binds Wiki snapshots to the configured state directory.
// @intent preserve the default .ccg output root while allowing CLI-configured state paths.
func NewWikiIndexWriter(root string) *WikiIndexWriter { return &WikiIndexWriter{Root: root} }

// @intent map default and named namespaces to their compatibility snapshot location.
func (w *WikiIndexWriter) indexPath(namespace string) string {
	root := w.Root
	if root == "" {
		root = ".ccg"
	}
	if requestctx.Normalize(namespace) == requestctx.DefaultNamespace {
		return filepath.Join(root, "wiki-index.json")
	}
	return filepath.Join(root, namespace, "wiki-index.json")
}

// WriteWikiIndex writes indented JSON through a same-directory temporary file and rename.
// @intent preserve the versioned built-in Wiki snapshot format at its namespace-specific path.
// @sideEffect creates the namespace directory and atomically replaces wiki-index.json.
func (w *WikiIndexWriter) WriteWikiIndex(_ context.Context, namespace string, idx *wiki.Index) error {
	target := w.indexPath(namespace)
	dir := filepath.Dir(target)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return trace.Wrap(err, "mkdir wiki index dir")
	}
	f, err := os.CreateTemp(dir, "wiki-index-*.tmp")
	if err != nil {
		return trace.Wrap(err, "create temp wiki index")
	}
	tmpName := f.Name()
	defer os.Remove(tmpName)
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(idx); err != nil {
		_ = f.Close()
		return trace.Wrap(err, "encode wiki index")
	}
	if err := f.Close(); err != nil {
		return trace.Wrap(err, "close wiki index")
	}
	if err := os.Rename(tmpName, target); err != nil {
		return trace.Wrap(err, "rename wiki-index.json")
	}
	return nil
}

// LoadWikiIndex decodes one persisted built-in Wiki compatibility snapshot.
// @intent round-trip compatibility fixtures and fallback readers through the outbound file adapter.
// @sideEffect reads the configured JSON file from disk.
func LoadWikiIndex(path string) (*wiki.Index, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, trace.Wrap(err, "LoadWikiIndex open "+path)
	}
	defer f.Close()
	var idx wiki.Index
	if err := json.NewDecoder(f).Decode(&idx); err != nil {
		return nil, trace.Wrap(err, "LoadWikiIndex decode")
	}
	return &idx, nil
}
