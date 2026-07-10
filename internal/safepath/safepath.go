// @index safepath는 여러 핸들러가 공유하는 저수준 경로 안전 프리미티브를 단일화한다.
// 정책(prefix/Rel/walk 기반 containment)과 에러 문자열은 호출부에 남기고, 여기서는
// 공통 메커니즘(심링크 거부 walk, canonical 정규화, Rel 기반 containment)만 제공한다.
package safepath

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// EnsureNoSymlinkInPath walks each path segment from root to relPath rejecting symlinks.
// @intent prevent symlink traversal from escaping a trusted root before any filesystem mutation.
// @param allowMissingLeaf when true, returns the joined path even when the leaf does not yet exist.
func EnsureNoSymlinkInPath(root, relPath string, allowMissingLeaf bool) (string, error) {
	cleanRel := filepath.Clean(relPath)
	if cleanRel == "." {
		return root, nil
	}
	current := root
	segments := strings.Split(cleanRel, string(filepath.Separator))
	for i, segment := range segments {
		current = filepath.Join(current, segment)
		info, err := os.Lstat(current)
		if err != nil {
			if allowMissingLeaf && errors.Is(err, fs.ErrNotExist) && i == len(segments)-1 {
				return current, nil
			}
			if allowMissingLeaf && errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("symlink paths are not allowed")
		}
	}
	return current, nil
}

// Canonical resolves path to an absolute, symlink-free, cleaned filesystem path.
// @intent normalize user-supplied paths before containment comparison to prevent symlink-based escapes.
// @param allowMissingLeaf when true, a non-existent leaf is tolerated by resolving the parent and
// appending the unresolved base; when false, a missing path is an error (strict existence).
func Canonical(path string, allowMissingLeaf bool) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	clean := filepath.Clean(abs)
	real, err := filepath.EvalSymlinks(clean)
	if err == nil {
		return filepath.Clean(real), nil
	}
	if !allowMissingLeaf {
		return "", err
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	parent := filepath.Dir(clean)
	base := filepath.Base(clean)
	parentReal, parentErr := filepath.EvalSymlinks(parent)
	if parentErr != nil {
		return "", err
	}
	return filepath.Join(parentReal, base), nil
}

// IsWithinRoot reports whether target is the same as root or a descendant of it.
// @intent detect path traversal by checking the relative path does not escape upward.
// @requires root and target should already be canonicalized (see Canonical) for symlink-safe comparison.
func IsWithinRoot(root, target string) (bool, error) {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false, err
	}
	if rel == "." {
		return true, nil
	}
	if rel == ".." {
		return false, nil
	}
	if len(rel) >= 3 && rel[:3] == ".."+string(filepath.Separator) {
		return false, nil
	}
	if filepath.IsAbs(rel) {
		return false, nil
	}
	return true, nil
}
