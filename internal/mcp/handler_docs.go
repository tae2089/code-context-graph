package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/imtaebin/code-context-graph/internal/ragindex"
)

// ragIndexPath는 doc-index.json의 실효 경로를 반환한다.
// deps.RagIndexDir이 비어 있으면 ".ccg"를 기본값으로 사용한다.
func (h *handlers) ragIndexPath() string {
	dir := h.deps.RagIndexDir
	if dir == "" {
		dir = ".ccg"
	}
	return filepath.Join(dir, "doc-index.json")
}

func (h *handlers) buildRagIndex(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	outDir := request.GetString("out_dir", "")
	indexDir := request.GetString("index_dir", "")

	// Fall back to deps defaults
	if indexDir == "" {
		indexDir = h.deps.RagIndexDir
	}

	b := &ragindex.Builder{
		DB:          h.deps.DB,
		OutDir:      outDir,   // empty string → Builder uses "docs" default
		IndexDir:    indexDir, // empty string → Builder uses ".ccg" default
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

func (h *handlers) getRagTree(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	communityID := request.GetString("community_id", "")
	depth := int(request.GetFloat("depth", 0))

	// doc-index.json mtime을 캐시 키에 포함
	var indexMtime int64
	if stat, statErr := os.Stat(h.ragIndexPath()); statErr == nil {
		indexMtime = stat.ModTime().UnixNano()
	}

	return finalizeToolResult(h.cachedExecute("get_rag_tree:", map[string]any{"community_id": communityID, "depth": depth, "mtime": indexMtime}, func() (string, error) {
		idx, err := ragindex.LoadIndex(h.ragIndexPath())
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

func (h *handlers) getDocContent(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	filePath, err := request.RequireString("file_path")
	if err != nil {
		return missingParamResult(err)
	}

	// Path traversal protection
	clean := filepath.Clean(filePath)
	if filepath.IsAbs(clean) || strings.HasPrefix(clean, "..") {
		return mcp.NewToolResultError("invalid file_path: path traversal not allowed"), nil
	}

	const maxDocFileSizeBytes = 1 << 20 // 1 MB

	// Include mtime in cache key to detect file changes; also enforce size limit
	var mtime int64
	if stat, statErr := os.Stat(clean); statErr == nil {
		if stat.Size() > maxDocFileSizeBytes {
			return mcp.NewToolResultError(fmt.Sprintf("file %q exceeds 1 MB size limit (%d bytes)", filePath, stat.Size())), nil
		}
		mtime = stat.ModTime().UnixNano()
	}

	return finalizeToolResult(h.cachedExecute("get_doc_content:", map[string]any{"file_path": filePath, "mtime": mtime}, func() (string, error) {
		content, err := os.ReadFile(clean)
		if err != nil {
			return "", newToolResultErr(fmt.Sprintf("read file %q: %v. Run 'ccg docs' to generate documentation files.", filePath, err))
		}
		return string(content), nil
	}))
}

func (h *handlers) searchDocs(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	query, err := request.RequireString("query")
	if err != nil {
		return missingParamResult(err)
	}
	if strings.TrimSpace(query) == "" {
		return mcp.NewToolResultError("query must not be empty"), nil
	}
	limit := int(request.GetFloat("limit", 10))
	if limit <= 0 {
		limit = 10
	}

	// doc-index.json mtime을 캐시 키에 포함
	var indexMtime int64
	if stat, statErr := os.Stat(h.ragIndexPath()); statErr == nil {
		indexMtime = stat.ModTime().UnixNano()
	}

	return finalizeToolResult(h.cachedExecute("search_docs:", map[string]any{"query": query, "limit": limit, "mtime": indexMtime}, func() (string, error) {
		idx, err := ragindex.LoadIndex(h.ragIndexPath())
		if err != nil {
			return "", newToolResultErr(fmt.Sprintf("load doc-index: %v", err))
		}

		results := ragindex.Search(idx.Root, query, limit)
		if results == nil {
			results = []ragindex.SearchResult{}
		}

		// SearchResult fields are all basic types (string, []string); json.Marshal cannot fail.
		b, _ := json.Marshal(results)
		return string(b), nil
	}))
}
