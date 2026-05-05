// @index MCP handlers for documentation RAG index build and retrieval over generated docs.
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/tae2089/code-context-graph/internal/paging"
	"github.com/tae2089/code-context-graph/internal/ragindex"
)

// @intent resolve the base directory that stores generated doc-index artifacts for MCP documentation tools.
func (h *handlers) ragIndexRoot() string {
	dir := h.deps.RagIndexDir
	if dir == "" {
		dir = ".ccg"
	}
	return dir
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

// @intent derive the safe doc-index.json path for either the shared docs root or one workspace-specific subtree.
func (h *handlers) resolvedRagIndexPath(workspace string) (string, error) {
	if workspace != "" {
		if err := validateWorkspacePath(workspace, ""); err != nil {
			return "", err
		}
		return safePathUnderRoot(h.ragIndexRoot(), filepath.Join(workspace, "doc-index.json"), "workspace", false, true)
	}
	return safePathUnderRoot(h.ragIndexRoot(), "doc-index.json", "file_path", false, true)
}

// buildRagIndex builds the documentation RAG index from generated docs and communities.
// @intent Regenerates the documentation traversal tree so MCP documentation tools see the latest structure.
// @param request out_dir and index_dir can override the documentation root and index output paths.
// @ensures Returns a summary of the number of communities and files generated on success.
// @sideEffect Writes the doc-index.json file and flushes the cache.
// @mutates documentation index state, h.cache
// @see mcp.handlers.getRagTree
func (h *handlers) buildRagIndex(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	outDir := request.GetString("out_dir", "")
	indexDir := request.GetString("index_dir", "")
	workspace := requestNamespace(request)

	if workspace != "" {
		if err := validateWorkspacePath(workspace, ""); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		outDir = filepath.Join(h.workspaceRoot(), workspace)
		ctx = h.applyWorkspace(ctx, request)
	}

	if indexDir == "" {
		indexDir = h.ragIndexRoot()
	} else {
		resolvedIndexDir, err := safePathUnderRoot(h.ragIndexRoot(), indexDir, "index_dir", true, true)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		indexDir = resolvedIndexDir
	}

	if workspace != "" {
		indexDir = filepath.Join(indexDir, workspace)
	}

	b := &ragindex.Builder{
		DB:          h.deps.DB,
		OutDir:      outDir,
		IndexDir:    indexDir,
		ProjectDesc: h.deps.RagProjectDesc,
	}
	communities, files, err := b.Build(ctx)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("build rag index: %v", err)), nil
	}
	if h.cache != nil {
		h.cache.Flush()
	}
	return mcp.NewToolResultText(fmt.Sprintf("Built doc-index: %d communities, %d files", communities, files)), nil
}

// getRagTree returns the documentation tree or a pruned community subtree.
// @intent Exposes the documentation RAG index in a tree format to enable exploratory lookups.
// @param request community_id is the starting point for a subtree, and depth limits the return depth.
// @requires doc-index.json must have been generated.
// @ensures Returns the TreeNode JSON for the requested range on success.
// @see mcp.handlers.buildRagIndex
func (h *handlers) getRagTree(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	communityID := request.GetString("community_id", "")
	depth := int(request.GetFloat("depth", 0))
	workspace := requestNamespace(request)
	indexPath, err := h.resolvedRagIndexPath(workspace)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	var indexMtime int64
	if stat, statErr := os.Stat(indexPath); statErr == nil {
		indexMtime = stat.ModTime().UnixNano()
	}

	return finalizeToolResult(h.cachedExecute(ctx, "get_rag_tree:", map[string]any{"community_id": communityID, "depth": depth, "namespace": workspace, "mtime": indexMtime}, func() (string, error) {
		idx, err := ragindex.LoadIndex(indexPath)
		if err != nil {
			return "", newToolResultErr(fmt.Sprintf("load doc-index: %v", err))
		}

		var node *ragindex.TreeNode
		if communityID == "" {
			node = idx.Root
		} else {
			node = ragindex.FindNode(idx.Root, communityID)
			if node == nil {
				return "", newToolResultErr(fmt.Sprintf("community_id %q not found", communityID))
			}
		}

		if depth > 0 {
			node = ragindex.PruneTree(node, depth)
		}

		// TreeNode fields are all basic types (string, []*TreeNode); json.Marshal cannot fail.
		b, _ := json.Marshal(node)
		return string(b), nil
	}))
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
	workspace := requestNamespace(request)

	clean := filepath.Clean(filePath)
	if filepath.IsAbs(clean) || strings.HasPrefix(clean, "..") {
		return mcp.NewToolResultError("invalid file_path: path traversal not allowed"), nil
	}

	var resolvedPath string
	if workspace != "" {
		if err := validateWorkspacePath(workspace, filePath); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		resolvedPath, err = h.resolveWorkspacePath(workspace, clean, false)
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

	return finalizeToolResult(h.cachedExecute(ctx, "get_doc_content:", map[string]any{"file_path": filePath, "namespace": workspace, "mtime": mtime}, func() (string, error) {
		content, err := os.ReadFile(resolvedPath)
		if err != nil {
			return "", newToolResultErr(fmt.Sprintf("read file %q: %v. Run 'ccg docs' to generate documentation files.", filePath, err))
		}
		return string(content), nil
	}))
}

// searchDocs searches the documentation tree by keyword.
// @intent Searches documentation node labels and summaries by keyword to quickly find relevant documents.
// @param request query is the required search term, and limit is the maximum number of results.
// @requires query must not consist only of whitespace.
// @ensures Returns an array of search results including breadcrumbs on success.
// @see mcp.handlers.getRagTree
func (h *handlers) searchDocs(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	query, err := request.RequireString("query")
	if err != nil {
		return missingParamResult(err)
	}
	if strings.TrimSpace(query) == "" {
		return mcp.NewToolResultError("query must not be empty"), nil
	}
	pageReq, err := paging.NormalizeWithDefault(paging.Request{Limit: int(request.GetFloat("limit", 10))}, 10)
	if err != nil {
		return finalizeToolResult("", newToolResultErr(err.Error()))
	}
	workspace := requestNamespace(request)
	indexPath, err := h.resolvedRagIndexPath(workspace)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	var indexMtime int64
	if stat, statErr := os.Stat(indexPath); statErr == nil {
		indexMtime = stat.ModTime().UnixNano()
	}

	return finalizeToolResult(h.cachedExecute(ctx, "search_docs:", map[string]any{"query": query, "limit": pageReq.Limit, "namespace": workspace, "mtime": indexMtime}, func() (string, error) {
		idx, err := ragindex.LoadIndex(indexPath)
		if err != nil {
			return "", newToolResultErr(fmt.Sprintf("load doc-index: %v", err))
		}

		results := ragindex.Search(idx.Root, query, pageReq.Limit)
		if results == nil {
			results = []ragindex.SearchResult{}
		}

		// SearchResult fields are all basic types (string, []string); json.Marshal cannot fail.
		b, _ := json.Marshal(results)
		return string(b), nil
	}))
}
