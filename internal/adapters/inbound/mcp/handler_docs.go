// @index MCP handler for safe generated-document reads.
package mcp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"

	requestctx "github.com/tae2089/code-context-graph/internal/ctx"
)

// @intent resolve the base directory that stores generated documentation and Wiki index artifacts.
func (h *handlers) ragIndexRoot() string {
	dir := h.deps.Runtime.RagIndexDir
	if dir == "" {
		dir = ".ccg"
	}
	return dir
}

// getDocContent reads a generated documentation file by relative path.
// @intent Returns the content of a documentation file directly so agents can read detailed descriptions.
// @param request file_path is the relative documentation path based on the working directory.
// @requires file_path must be a relative path and must not contain path traversal.
// @ensures Returns the body of the documentation file as text on success.
// @domainRule Documentation files exceeding 1MB are not returned.
// @sideEffect Performs a filesystem read.
func (h *handlers) getDocContent(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	filePath, err := request.RequireString("file_path")
	if err != nil {
		return missingParamResult(err)
	}
	namespace := requestNamespace(request)

	clean := filepath.Clean(filePath)
	if filepath.IsAbs(clean) || strings.HasPrefix(clean, "..") {
		return mcp.NewToolResultError("invalid file_path: path traversal not allowed"), nil
	}

	// The default namespace maps to the shared docs root, mirroring resolvedRagIndexPath
	// and the doc-search path; only named namespaces resolve under namespaces/<ns>/.
	var resolvedPath string
	if namespace != "" && requestctx.Normalize(namespace) != requestctx.DefaultNamespace {
		if err := validateNamespacePath(namespace, filePath); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		resolvedPath, err = h.resolveNamespacePath(namespace, clean, false)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("resolve namespace path: %v", err)), nil
		}
	} else {
		resolvedPath, err = safePathUnderRoot(h.ragIndexRoot(), clean, "file_path", false, false)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
	}

	const maxDocFileSizeBytes = 1 << 20 // 1 MB

	var mtime int64
	if stat, statErr := os.Stat(resolvedPath); statErr == nil {
		if stat.Size() > maxDocFileSizeBytes {
			return mcp.NewToolResultError(fmt.Sprintf("file %q exceeds 1 MB size limit (%d bytes)", filePath, stat.Size())), nil
		}
		mtime = stat.ModTime().UnixNano()
	}

	return finalizeToolResult(h.cachedExecute(ctx, "get_doc_content:", map[string]any{"file_path": filePath, "namespace": namespace, "mtime": mtime}, func() (string, error) {
		content, err := os.ReadFile(resolvedPath)
		if err != nil {
			return "", newToolResultErr(fmt.Sprintf("read file %q: %v. Run 'ccg docs' to generate documentation files.", filePath, err))
		}
		return string(content), nil
	}))
}

// @intent normalize a docs/index root to an absolute, symlink-evaluated path before path checks.
// @requires root must be a filesystem path that can be resolved or created as needed.
// @ensures returned path is absolute, cleaned, and symlink-resolved when it exists.
// @domainRule safe-root containment checks must happen after symlink evaluation.
// @sideEffect may create the root directory on disk when create is true.
func resolveSafeRoot(root string, create bool) (string, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve safe root: %w", err)
	}
	if create {
		if err := os.MkdirAll(absRoot, 0o755); err != nil {
			return "", fmt.Errorf("create safe root: %w", err)
		}
	}
	if _, err := os.Stat(absRoot); err == nil {
		realRoot, err := filepath.EvalSymlinks(absRoot)
		if err != nil {
			return "", fmt.Errorf("resolve safe root symlinks: %w", err)
		}
		return filepath.Clean(realRoot), nil
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("stat safe root: %w", err)
	}
	return filepath.Clean(absRoot), nil
}

// @intent reject relative paths that would resolve outside the resolved docs root.
// @requires relPath must be a relative, traversal-free path fragment.
// @ensures returned path stays within the resolved safe root and has no symlink escape.
// @domainRule traversal checks happen before symlink evaluation, and containment checks happen after it.
// @sideEffect may create the configured root directory indirectly through resolveSafeRoot when createRoot is true.
func safePathUnderRoot(root, relPath, field string, createRoot bool, allowMissingLeaf bool) (string, error) {
	clean := filepath.Clean(relPath)
	if filepath.IsAbs(clean) || strings.HasPrefix(clean, "..") {
		return "", fmt.Errorf("invalid %s: path traversal not allowed", field)
	}
	base, err := resolveSafeRoot(root, createRoot)
	if err != nil {
		return "", err
	}
	target, err := ensureNoSymlinkInPath(base, clean, allowMissingLeaf)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", field, err)
	}
	target = filepath.Clean(target)
	if target != base && !strings.HasPrefix(target, base+string(os.PathSeparator)) {
		return "", fmt.Errorf("%s %q is outside configured safe root", field, relPath)
	}
	return target, nil
}
