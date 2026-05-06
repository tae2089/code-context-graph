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

// @intent derive the safe doc-index.json path for either the shared docs root or one namespace-specific subtree.
func (h *handlers) resolvedRagIndexPath(namespace string) (string, error) {
	if ctxns.Normalize(namespace) == ctxns.DefaultNamespace {
		return safePathUnderRoot(h.ragIndexRoot(), "doc-index.json", "file_path", false, true)
	}
	if namespace != "" {
		if err := validateNamespacePath(namespace, ""); err != nil {
			return "", err
		}
		return safePathUnderRoot(h.ragIndexRoot(), filepath.Join(namespace, "doc-index.json"), "namespace", false, true)
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
	namespace := requestNamespace(request)

	if namespace != "" {
		if err := validateNamespacePath(namespace, ""); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if outDir == "" {
			outDir = "docs"
		}
		if err := validateNamespacePath(namespace, outDir); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		ctx = h.applyNamespace(ctx, request)
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

	if namespace != "" {
		indexDir = filepath.Join(indexDir, namespace)
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

// getRagTree returns the documentation tree or a pruned node subtree.
// @intent Exposes the documentation RAG index in a tree format from DB graph evidence.
// @param request node_id is the starting point for a subtree, community_id is a deprecated alias, and depth limits the return depth.
// @ensures Returns the TreeNode JSON for the requested range on success.
// @see mcp.handlers.buildRagIndex
func (h *handlers) getRagTree(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	nodeID := request.GetString("node_id", "")
	if nodeID == "" {
		nodeID = request.GetString("community_id", "")
	}
	depth := int(request.GetFloat("depth", 0))
	namespace := resolveNamespace(ctx, requestNamespace(request))
	ctx = ctxns.WithNamespace(ctx, namespace)
	if h.deps.DB == nil {
		return mcp.NewToolResultError("DB is not configured"), nil
	}
	return finalizeToolResult(h.cachedExecute(ctx, "get_rag_tree:db:", map[string]any{"node_id": nodeID, "depth": depth, "namespace": namespace}, func() (string, error) {
		builder := &ragindex.Builder{
			DB:          h.deps.DB,
			OutDir:      "docs",
			ProjectDesc: h.deps.RagProjectDesc,
		}
		root, _, _, err := builder.BuildTree(ctx)
		if err != nil {
			return "", newToolResultErr(fmt.Sprintf("build rag tree from DB: %v", err))
		}
		node, err := selectRagTreeNode(root, nodeID)
		if err != nil {
			return "", err
		}
		node = ragindex.PruneTree(node, depth)

		// TreeNode fields are JSON-safe DTO fields; json.Marshal cannot fail for this payload.
		encoded, _ := json.Marshal(node)
		return string(encoded), nil
	}))
}

// @intent select the requested RAG tree node in DB-built trees.
func selectRagTreeNode(root *ragindex.TreeNode, nodeID string) (*ragindex.TreeNode, error) {
	if nodeID == "" {
		return root, nil
	}
	node := ragindex.FindNode(root, nodeID)
	if node == nil {
		return nil, newToolResultErr(fmt.Sprintf("node_id %q not found", nodeID))
	}
	return node, nil
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

	var resolvedPath string
	if namespace != "" {
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
	namespace := requestNamespace(request)
	ctx = h.applyNamespace(ctx, request)
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

// @intent keep MCP retrieve_docs response decoding compatible while sharing the canonical retrieval DTO.
type retrieveDocsResponse = retrieval.Response

// @intent keep MCP retrieve_docs result decoding compatible while sharing the canonical retrieval DTO.
type retrieveDocsResult = retrieval.Result

var _ = (*handlers).retrieveDocsFromDB

// @intent build a retrieve_docs response from persisted graph nodes and annotation tags.
// @requires SearchBackend and DB must be configured, and ctx must carry the desired namespace.
// @ensures returned results are grouped one-per-file in first-seen FTS order and keep retrieve_docs' stable JSON shape.
// @sideEffect queries the search backend and may read generated Markdown files for bounded content.
func (h *handlers) retrieveDocsFromDB(ctx context.Context, namespace, query string, limit, contentLimit int, explain bool) (retrieveDocsResponse, error) {
	service := retrieval.Service{DB: h.deps.DB, SearchBackend: h.deps.SearchBackend}
	retrieved, err := service.FromDBWithOptions(ctx, namespace, query, limit, contentLimit, func(_ context.Context, namespace, docPath string, limit int) (string, bool, error) {
		return h.readIndexedDocContent(namespace, docPath, limit)
	}, retrieval.Options{Explain: explain})
	if err != nil {
		return retrieveDocsResponse{Results: []retrieveDocsResult{}}, err
	}
	return retrieved, nil
}

// retrieveDocs retrieves generated Markdown docs using DB-backed graph evidence.
// @intent provide DB-primary document retrieval.
// @param request query is the natural-language retrieval prompt, limit bounds document count, and content_limit bounds each Markdown payload.
// @requires a configured DB and generated docs may exist.
// @ensures returns file-level matches with tree evidence and bounded document content.
// @sideEffect queries the graph DB and reads generated Markdown files.
func (h *handlers) retrieveDocs(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	query, err := request.RequireString("query")
	if err != nil {
		return missingParamResult(err)
	}
	if strings.TrimSpace(query) == "" {
		return mcp.NewToolResultError("query must not be empty"), nil
	}
	pageReq, err := paging.NormalizeWithDefault(paging.Request{Limit: int(request.GetFloat("limit", 5))}, 5)
	if err != nil {
		return finalizeToolResult("", newToolResultErr(err.Error()))
	}
	if pageReq.Limit > 50 {
		return mcp.NewToolResultError("limit must be <= 50"), nil
	}
	contentLimit := int(request.GetFloat("content_limit", 4000))
	if contentLimit < 0 {
		return mcp.NewToolResultError("content_limit must be >= 0"), nil
	}
	if contentLimit > 20000 {
		return mcp.NewToolResultError("content_limit must be <= 20000"), nil
	}
	explain := request.GetBool("explain", false)

	namespace := requestNamespace(request)
	ctx = h.applyNamespace(ctx, request)
	if h.deps.DB == nil {
		return mcp.NewToolResultError("DB is not configured"), nil
	}
	cacheKey := map[string]any{
		"query":         query,
		"limit":         pageReq.Limit,
		"content_limit": contentLimit,
		"namespace":     namespace,
		"explain":       explain,
	}
	return finalizeToolResult(h.cachedExecute(ctx, "retrieve_docs:db:", cacheKey, func() (string, error) {
		response, err := h.retrieveDocsFromDB(ctx, namespace, query, pageReq.Limit, contentLimit, explain)
		if err != nil {
			return "", newToolResultErr(err.Error())
		}

		b, _ := json.Marshal(response)
		return string(b), nil
	}))
}

// readIndexedDocContent reads a doc_path under the generated docs root.
// @intent let retrieve_docs return bounded Markdown content while keeping index-provided paths inside a safe root.
// @domainRule namespace doc paths are resolved from that namespace; shared doc paths are resolved from the parent of the RAG index root.
// @sideEffect reads a generated Markdown file.
func (h *handlers) readIndexedDocContent(namespace, docPath string, limit int) (string, bool, error) {
	resolvedPath, err := h.resolveIndexedDocPath(namespace, docPath)
	if err != nil {
		return "", false, err
	}
	content, err := os.ReadFile(resolvedPath)
	if err != nil {
		return "", false, fmt.Errorf("read doc_path %q: %w. Run 'ccg docs' to generate documentation files", docPath, err)
	}
	if len(content) <= limit {
		return string(content), false, nil
	}
	return string(content[:limit]), true, nil
}

// resolveIndexedDocPath resolves doc-index doc_path values without allowing traversal outside the docs root.
// @intent safely support both relative docs/... paths and absolute doc paths produced by custom docs output directories.
func (h *handlers) resolveIndexedDocPath(namespace, docPath string) (string, error) {
	if strings.TrimSpace(docPath) == "" {
		return "", fmt.Errorf("doc_path is empty")
	}
	if namespace != "" {
		if err := validateNamespacePath(namespace, docPath); err != nil {
			return "", err
		}
		return h.resolveNamespacePath(namespace, filepath.Clean(docPath), false)
	}
	indexRoot, err := resolveSafeRoot(h.ragIndexRoot(), false)
	if err != nil {
		return "", err
	}
	base := filepath.Dir(indexRoot)
	clean := filepath.Clean(docPath)
	if filepath.IsAbs(clean) {
		if realDocPath, err := filepath.EvalSymlinks(clean); err == nil {
			clean = realDocPath
		}
		rel, err := filepath.Rel(base, clean)
		if err != nil {
			return "", fmt.Errorf("resolve doc_path: %w", err)
		}
		if rel == "." {
			return base, nil
		}
		if strings.HasPrefix(rel, "..") {
			return "", fmt.Errorf("doc_path %q is outside configured docs root", docPath)
		}
		return safePathUnderRoot(base, rel, "doc_path", false, false)
	}
	return safePathUnderRoot(base, clean, "doc_path", false, false)
}
