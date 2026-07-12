// @index Symlink-safe atomic filesystem access rooted beneath one generated-content directory.
package contentfiles

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Root provides safe relative access below one generated-content directory.
// @intent centralize containment, symlink rejection, and atomic replacement for generated docs.
type Root struct{ dir string }

// NewRoot binds safe generated-file operations to one output directory.
// @intent prevent application policy from handling absolute output paths.
func NewRoot(dir string) *Root { return &Root{dir: dir} }

// @intent resolve a relative generated path only when every existing component remains inside the configured root and is not a symlink.
func (r *Root) path(relPath string) (string, error) {
	clean := filepath.Clean(filepath.FromSlash(relPath))
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("invalid file path %q: path traversal not allowed", relPath)
	}
	base, err := filepath.Abs(r.dir)
	if err != nil {
		return "", err
	}
	target := filepath.Join(base, clean)
	rel, err := filepath.Rel(base, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("invalid file path %q: outside output directory", relPath)
	}
	segments := strings.Split(clean, string(filepath.Separator))
	current := base
	for i, segment := range segments {
		current = filepath.Join(current, segment)
		info, statErr := os.Lstat(current)
		if errors.Is(statErr, fs.ErrNotExist) {
			break
		}
		if statErr != nil {
			return "", statErr
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("symlink paths are not allowed")
		}
		if !info.IsDir() && i < len(segments)-1 {
			return "", fmt.Errorf("non-directory path component %q", segment)
		}
	}
	return target, nil
}

// Validate checks a generated path before a multi-file write begins.
// @intent fail generation preflight before any output when a path could escape or traverse a symlink.
func (r *Root) Validate(rel string) error { _, err := r.path(rel); return err }

// Read returns a generated file and distinguishes absence from read failure.
// @intent support manifest and managed-file policy without exposing absolute paths.
func (r *Root) Read(rel string) ([]byte, bool, error) {
	p, err := r.path(rel)
	if err != nil {
		return nil, false, err
	}
	data, err := os.ReadFile(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	return data, err == nil, err
}

// Write atomically replaces one generated file below the root.
// @intent persist generated output only after safe-root validation and durable temporary-file completion.
// @sideEffect creates parent directories and renames a synced temporary file.
func (r *Root) Write(rel string, data []byte) error {
	p, err := r.path(rel)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(p), ".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err = f.Write(data); err == nil {
		err = f.Chmod(0o644)
	}
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err := os.Rename(tmp, p); err != nil {
		return err
	}
	syncDir(filepath.Dir(p))
	return nil
}

// Remove deletes one validated stale generated file.
// @intent prune only the relative generated path selected by application manifest policy.
// @domainRule missing files are treated as an already-complete prune.
// @sideEffect removes a file below the configured root.
func (r *Root) Remove(rel string) error {
	p, err := r.path(rel)
	if err != nil {
		return err
	}
	err = os.Remove(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// ModTime returns generated-file freshness metadata without exposing os.FileInfo.
// @intent let docs lint compare source and generated timestamps through a narrow port.
func (r *Root) ModTime(rel string) (time.Time, bool, error) {
	p, err := r.path(rel)
	if err != nil {
		return time.Time{}, false, err
	}
	info, err := os.Stat(p)
	if errors.Is(err, fs.ErrNotExist) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, err
	}
	return info.ModTime(), true, nil
}

// MarkdownFiles inventories generated Markdown paths and modification times.
// @intent provide default-namespace lint fallback when no manifest exists.
func (r *Root) MarkdownFiles() (map[string]time.Time, error) {
	result := map[string]time.Time{}
	if _, err := os.Stat(r.dir); errors.Is(err, fs.ErrNotExist) {
		return result, nil
	}
	err := filepath.Walk(r.dir, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		if !strings.HasSuffix(p, ".md") {
			return nil
		}
		rel, _ := filepath.Rel(r.dir, p)
		result[filepath.ToSlash(rel)] = info.ModTime()
		return nil
	})
	return result, err
}

// @intent best-effort directory fsync after atomic replacement to preserve prior generated-doc durability behavior.
func syncDir(dir string) {
	d, err := os.Open(dir)
	if err == nil {
		defer d.Close()
		_ = d.Sync()
	}
}
