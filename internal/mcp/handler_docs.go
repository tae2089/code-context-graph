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

	"github.com/tae2089/code-context-graph/internal/ctxns"
	"github.com/tae2089/code-context-graph/internal/paging"
	"github.com/tae2089/code-context-graph/internal/ragindex"
	"github.com/tae2089/code-context-graph/internal/retrieval"
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
	if namespace != "" && ctxns.Normalize(namespace) != ctxns.DefaultNamespace {
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
	namespace := resolveNamespace(ctx, requestNamespace(request))
	ctx = ctxns.WithNamespace(ctx, namespace)
	if h.deps.DB == nil {
		return mcp.NewToolResultError("DB is not configured"), nil
	}
	return finalizeToolResult(h.cachedExecute(ctx, "search_docs:db:", map[string]any{"query": query, "limit": pageReq.Limit, "namespace": namespace}, func() (string, error) {
		results, err := h.searchDocsFromDB(ctx, namespace, query, pageReq.Limit)
		if err != nil {
			return "", newToolResultErr(err.Error())
		}
		b, _ := json.Marshal(results)
		return string(b), nil
	}))
}

// @intent search persisted graph nodes directly from DB and search backend.
// @requires ctx must carry the selected namespace for SearchBackend.Query.
// @ensures returns SearchResult-compatible JSON items without requiring generated index files.
// @sideEffect queries the search backend and may scan graph annotations as a fallback.
func (h *handlers) searchDocsFromDB(ctx context.Context, namespace, query string, limit int) ([]ragindex.SearchResult, error) {
	if h.deps.DB == nil {
		return nil, fmt.Errorf("DB not configured")
	}
	if limit <= 0 {
		return []ragindex.SearchResult{}, nil
	}
	service := retrieval.Service{DB: h.deps.DB, SearchBackend: h.deps.SearchBackend}
	retrieved, err := service.FromDB(ctx, namespace, query, limit, 0, nil)
	if err != nil {
		return nil, err
	}
	results := make([]ragindex.SearchResult, 0, min(limit, len(retrieved.Results)))
	for _, result := range retrieved.Results {
		if len(results) >= limit {
			break
		}
		results = append(results, ragindex.SearchResult{
			ID:      result.ID,
			Label:   result.Label,
			Kind:    result.Kind,
			Summary: result.Summary,
			DocPath: result.DocPath,
			Path:    result.Path,
		})
	}
	if results == nil {
		results = []ragindex.SearchResult{}
	}
	return results, nil
}
