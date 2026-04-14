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
// @intent 문서 인덱스 파일 위치를 한 곳에서 계산해 docs 도구들이 같은 경로를 사용하게 한다.
// @return doc-index.json의 실제 파일 경로를 반환한다.
func (h *handlers) ragIndexPath() string {
	dir := h.deps.RagIndexDir
	if dir == "" {
		dir = ".ccg"
	}
	return filepath.Join(dir, "doc-index.json")
}

// buildRagIndex builds the documentation RAG index from generated docs and communities.
// @intent 문서 탐색용 트리를 재생성해 MCP 문서 검색 도구들이 최신 구조를 보게 한다.
// @param request out_dir와 index_dir로 문서 루트와 인덱스 출력 경로를 덮어쓸 수 있다.
// @ensures 성공 시 생성된 커뮤니티 수와 파일 수를 요약해 반환한다.
// @sideEffect doc-index.json 파일을 기록하고 캐시를 비운다.
// @mutates documentation index state, h.cache
// @see mcp.handlers.getRagTree
func (h *handlers) buildRagIndex(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	outDir := request.GetString("out_dir", "")
	indexDir := request.GetString("index_dir", "")
	workspace := request.GetString("workspace", "")

	if workspace != "" {
		if err := validateWorkspacePath(workspace, ""); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		outDir = filepath.Join(h.workspaceRoot(), workspace)
	}

	if indexDir == "" {
		indexDir = h.deps.RagIndexDir
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
// @intent 문서 RAG 인덱스를 트리 형태로 노출해 탐색형 조회를 가능하게 한다.
// @param request community_id는 하위 트리 시작점이고 depth는 반환 깊이 제한이다.
// @requires doc-index.json이 생성되어 있어야 한다.
// @ensures 성공 시 요청한 범위의 TreeNode JSON을 반환한다.
// @see mcp.handlers.buildRagIndex
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

// getDocContent reads a generated documentation file by relative path.
// @intent 문서 파일 내용을 직접 반환해 에이전트가 세부 설명을 읽을 수 있게 한다.
// @param request file_path는 작업 디렉터리 기준 상대 문서 경로다.
// @requires file_path는 상대 경로여야 하며 경로 순회를 포함하면 안 된다.
// @ensures 성공 시 문서 파일 본문을 텍스트로 반환한다.
// @domainRule 1MB를 초과하는 문서 파일은 반환하지 않는다.
// @sideEffect 파일 시스템 읽기를 수행한다.
func (h *handlers) getDocContent(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	filePath, err := request.RequireString("file_path")
	if err != nil {
		return missingParamResult(err)
	}
	workspace := request.GetString("workspace", "")

	clean := filepath.Clean(filePath)
	if filepath.IsAbs(clean) || strings.HasPrefix(clean, "..") {
		return mcp.NewToolResultError("invalid file_path: path traversal not allowed"), nil
	}

	var resolvedPath string
	if workspace != "" {
		if err := validateWorkspacePath(workspace, filePath); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		resolvedPath = filepath.Join(h.workspaceRoot(), filepath.Clean(workspace), clean)
	} else {
		resolvedPath = clean
	}

	const maxDocFileSizeBytes = 1 << 20 // 1 MB

	var mtime int64
	if stat, statErr := os.Stat(resolvedPath); statErr == nil {
		if stat.Size() > maxDocFileSizeBytes {
			return mcp.NewToolResultError(fmt.Sprintf("file %q exceeds 1 MB size limit (%d bytes)", filePath, stat.Size())), nil
		}
		mtime = stat.ModTime().UnixNano()
	}

	return finalizeToolResult(h.cachedExecute("get_doc_content:", map[string]any{"file_path": filePath, "workspace": workspace, "mtime": mtime}, func() (string, error) {
		content, err := os.ReadFile(resolvedPath)
		if err != nil {
			return "", newToolResultErr(fmt.Sprintf("read file %q: %v. Run 'ccg docs' to generate documentation files.", filePath, err))
		}
		return string(content), nil
	}))
}

// searchDocs searches the documentation tree by keyword.
// @intent 문서 노드 라벨과 요약을 키워드로 검색해 관련 문서를 빠르게 찾게 한다.
// @param request query는 필수 검색어이고 limit는 최대 결과 수다.
// @requires query는 공백만으로 이루어질 수 없다.
// @ensures 성공 시 breadcrumb를 포함한 검색 결과 배열을 반환한다.
// @see mcp.handlers.getRagTree
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
