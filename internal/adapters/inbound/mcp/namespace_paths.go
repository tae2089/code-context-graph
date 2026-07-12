// @index Namespace filesystem path resolution shared by namespace-scoped doc handlers.
package mcp

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/tae2089/code-context-graph/internal/safepath"
)

// namespaceRoot returns the filesystem root used for namespace-scoped storage.
// @intent give namespace path resolution one shared root, defaulting to "namespaces".
func (h *handlers) namespaceRoot() string {
	root := h.deps.Runtime.NamespaceRoot
	if root == "" {
		root = "namespaces"
	}
	return root
}

// validateNamespacePath rejects namespace and file inputs that could escape the namespace root.
// @intent keep namespace path validation in one place shared across handler files.
func validateNamespacePath(namespace, filePath string) error {
	return safepath.ValidateNamespacePath(namespace, filePath)
}

// ensureNoSymlinkInPath walks each segment from root to relPath rejecting symlinks.
// @intent prevent symlink traversal from escaping the namespace root before a read.
func ensureNoSymlinkInPath(root, relPath string, allowMissingLeaf bool) (string, error) {
	return safepath.EnsureNoSymlinkInPath(root, relPath, allowMissingLeaf)
}

// safeNamespaceRoot returns the absolute, symlink-resolved namespace root, creating it if needed.
// @intent resolve namespace paths under a trusted, real filesystem location.
// @sideEffect creates the namespace root directory when it does not yet exist.
func (h *handlers) safeNamespaceRoot() (string, error) {
	root := h.namespaceRoot()
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve namespace root: %w", err)
	}
	if err := os.MkdirAll(absRoot, 0o755); err != nil {
		return "", fmt.Errorf("create namespace root: %w", err)
	}
	realRoot, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		return "", fmt.Errorf("resolve namespace root symlinks: %w", err)
	}
	return realRoot, nil
}

// resolveNamespacePath resolves a namespace-relative file path under the trusted namespace root.
// @intent reject path traversal and symlink escapes before any namespace-scoped filesystem read.
// @param filePath relative path inside the namespace ("" returns the namespace dir).
// @param allowMissingLeaf when true, allow the leaf to not yet exist.
func (h *handlers) resolveNamespacePath(namespace, filePath string, allowMissingLeaf bool) (string, error) {
	if err := validateNamespacePath(namespace, filePath); err != nil {
		return "", err
	}
	root, err := h.safeNamespaceRoot()
	if err != nil {
		return "", err
	}
	wsDir, err := ensureNoSymlinkInPath(root, filepath.Clean(namespace), false)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			wsDir = filepath.Join(root, filepath.Clean(namespace))
		} else {
			return "", err
		}
	}
	if filePath == "" {
		return wsDir, nil
	}
	rel := filepath.Join(filepath.Clean(namespace), filepath.Clean(filePath))
	return ensureNoSymlinkInPath(root, rel, allowMissingLeaf)
}
