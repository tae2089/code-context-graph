package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/tae2089/trace"

	"github.com/imtaebin/code-context-graph/internal/analysis/community"
	"github.com/imtaebin/code-context-graph/internal/analysis/incremental"
	"github.com/imtaebin/code-context-graph/internal/pathutil"
)

// walkParseStats accumulates file parsing progress for build handlers.
// @intent 디렉터리 순회 중 생성된 파일·노드·엣지 수와 오류 수를 집계한다.
type walkParseStats struct {
	Files  int
	Nodes  int
	Edges  int
	Errors int
}

// walkAndParse walks a directory, parses supported files, and stores graph data.
// @intent 프로젝트 디렉터리를 순회하며 지원 언어만 파싱해 그래프 저장소를 채운다.
// @param dirPath 파싱할 프로젝트 루트 디렉터리다.
// @requires h.deps.Store와 h.deps.Walkers가 구성되어 있어야 한다.
// @ensures 반환 통계에는 처리된 파일과 저장된 노드/엣지 수가 반영된다.
// @sideEffect 파일 시스템 읽기와 그래프 저장소 쓰기를 수행한다.
// @mutates walkParseStats, graph store state
func (h *handlers) walkAndParse(ctx context.Context, dirPath string, includePaths ...string) (walkParseStats, error) {
	log := h.logger()
	var stats walkParseStats

	absDir, err := filepath.Abs(dirPath)
	if err != nil {
		return stats, trace.Wrap(err, "resolve path")
	}

	err = filepath.Walk(absDir, func(fp string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			if pathutil.ShouldSkipDir(info.Name()) {
				return filepath.SkipDir
			}
			if len(includePaths) > 0 && fp != absDir {
				relPath, _ := filepath.Rel(absDir, fp)
				if !pathutil.MatchIncludePaths(relPath, includePaths) {
					return filepath.SkipDir
				}
			}
			return nil
		}

		if len(includePaths) > 0 {
			relPath, _ := filepath.Rel(absDir, fp)
			if !pathutil.MatchIncludePaths(relPath, includePaths) {
				return nil
			}
		}

		ext := strings.ToLower(filepath.Ext(fp))
		walker, ok := h.deps.Walkers[ext]
		if !ok {
			return nil
		}

		content, err := os.ReadFile(fp)
		if err != nil {
			log.Warn("failed to read file", "file", fp, "error", err)
			stats.Errors++
			return nil
		}

		nodes, edges, err := walker.ParseWithContext(ctx, fp, content)
		if err != nil {
			log.Warn("failed to parse file", "file", fp, "error", err)
			stats.Errors++
			return nil
		}

		if len(nodes) > 0 {
			if err := h.deps.Store.UpsertNodes(ctx, nodes); err != nil {
				return trace.Wrap(err, "upsert nodes")
			}
			stats.Nodes += len(nodes)
		}
		if len(edges) > 0 {
			if err := h.deps.Store.UpsertEdges(ctx, edges); err != nil {
				return trace.Wrap(err, "upsert edges")
			}
			stats.Edges += len(edges)
		}
		stats.Files++
		return nil
	})
	if err != nil {
		return stats, trace.Wrap(err, "walk dir")
	}
	return stats, nil
}

// parseProject parses a project directory and stores discovered graph elements.
// @intent 단순 파싱 도구로 프로젝트 전체를 그래프 저장소에 적재한다.
// @param request path 파라미터에서 파싱 대상 디렉터리를 읽는다.
// @requires request.path가 유효한 디렉터리를 가리켜야 한다.
// @ensures 성공 시 파싱된 파일 수와 오류 수를 JSON으로 반환한다.
// @sideEffect 파일 시스템 읽기, 그래프 저장소 쓰기, 로그 기록을 수행한다.
func (h *handlers) parseProject(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ctx = h.applyWorkspace(ctx, request)
	log := h.logger()

	dirPath, err := request.RequireString("path")
	if err != nil {
		return missingParamResult(err)
	}

	log.Info("parse_project called", "path", dirPath)

	includePaths := request.GetStringSlice("include_paths", nil)
	stats, err := h.walkAndParse(ctx, dirPath, includePaths...)
	if err != nil {
		return nil, err
	}

	log.Info("parse_project completed", "parsed", stats.Files, "errors", stats.Errors)
	return mcp.NewToolResultText(fmt.Sprintf(`{"parsed":%d,"errors":%d}`, stats.Files, stats.Errors)), nil
}

// buildOrUpdateGraph builds the graph fully or incrementally and runs postprocessing.
// @intent 코드 그래프를 최신 상태로 맞추고 검색·커뮤니티 후처리를 함께 수행한다.
// @param request full_rebuild와 postprocess로 빌드 전략을 제어한다.
// @domainRule 증분 동기화기가 없으면 항상 전체 재빌드로 처리한다.
// @requires request.path가 접근 가능한 프로젝트 디렉터리여야 한다.
// @ensures 성공 시 처리 파일 수와 생성된 노드/엣지 수를 반환한다.
// @sideEffect 파일 시스템 읽기, 그래프 저장소 갱신, 검색 인덱스/커뮤니티 재빌드를 수행할 수 있다.
// @mutates graph store state, search index state, community state, h.cache
func (h *handlers) buildOrUpdateGraph(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ctx = h.applyWorkspace(ctx, request)
	log := h.logger()

	dirPath, err := request.RequireString("path")
	if err != nil {
		return missingParamResult(err)
	}

	fullRebuild := request.GetBool("full_rebuild", true)
	postprocess := request.GetString("postprocess", "full")
	includePaths := request.GetStringSlice("include_paths", nil)

	log.Info("build_or_update_graph called", "path", dirPath, "full_rebuild", fullRebuild, "postprocess", postprocess)

	start := time.Now()
	var nodeCount, edgeCount, fileCount int

	if fullRebuild || h.deps.Incremental == nil {
		stats, err := h.walkAndParse(ctx, dirPath, includePaths...)
		if err != nil {
			return nil, err
		}
		nodeCount = stats.Nodes
		edgeCount = stats.Edges
		fileCount = stats.Files
	} else {
		// 증분 빌드
		absDir, _ := filepath.Abs(dirPath)
		files := map[string]incremental.FileInfo{}
		err := filepath.Walk(dirPath, func(fp string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if info.IsDir() {
				name := info.Name()
				if pathutil.ShouldSkipDir(name) {
					return filepath.SkipDir
				}
				if len(includePaths) > 0 && fp != dirPath && fp != absDir {
					relPath, _ := filepath.Rel(absDir, fp)
					if !pathutil.MatchIncludePaths(relPath, includePaths) {
						return filepath.SkipDir
					}
				}
				return nil
			}
			if len(includePaths) > 0 {
				relPath, _ := filepath.Rel(absDir, fp)
				if !pathutil.MatchIncludePaths(relPath, includePaths) {
					return nil
				}
			}
			ext := strings.ToLower(filepath.Ext(fp))
			if _, ok := h.deps.Walkers[ext]; !ok {
				return nil
			}
			content, err := os.ReadFile(fp)
			if err != nil {
				return nil
			}
			hash := sha256.Sum256(content)
			files[fp] = incremental.FileInfo{
				Hash:    hex.EncodeToString(hash[:]),
				Content: content,
			}
			return nil
		})
		if err != nil {
			return nil, trace.Wrap(err, "walk error")
		}

		stats, err := h.deps.Incremental.Sync(ctx, files)
		if err != nil {
			return nil, trace.Wrap(err, "incremental sync error")
		}
		fileCount = stats.Added + stats.Modified
	}

	// 후처리
	switch postprocess {
	case "full":
		// flows 재빌드 (FlowTracer는 노드별이므로 스킵 — 전체 flow는 별도)
		// community 재빌드
		if h.deps.CommunityBuilder != nil {
			_, err := h.deps.CommunityBuilder.Rebuild(ctx, community.Config{Depth: 2})
			if err != nil {
				log.Warn("community rebuild failed", trace.SlogError(err))
			}
		}
		// search 재빌드
		if h.deps.SearchBackend != nil {
			if err := h.deps.SearchBackend.Rebuild(ctx, h.deps.DB); err != nil {
				log.Warn("search rebuild failed", trace.SlogError(err))
			}
		}
	case "minimal":
		// search만 재빌드
		if h.deps.SearchBackend != nil {
			if err := h.deps.SearchBackend.Rebuild(ctx, h.deps.DB); err != nil {
				log.Warn("search rebuild failed", trace.SlogError(err))
			}
		}
	case "none":
		// 스킵
	}

	elapsed := time.Since(start).Milliseconds()

	result := map[string]any{
		"status":        "ok",
		"files_parsed":  fileCount,
		"nodes_created": nodeCount,
		"edges_created": edgeCount,
		"elapsed_ms":    elapsed,
	}
	jsonStr, err := marshalJSON(result)
	if err != nil {
		return nil, trace.Wrap(err, "marshal result")
	}
	if h.cache != nil {
		h.cache.Flush()
	}
	return mcp.NewToolResultText(jsonStr), nil
}

// runPostprocess rebuilds selected graph-derived artifacts without reparsing code.
// @intent 기존 그래프 데이터에서 커뮤니티와 검색 인덱스를 독립적으로 재생성한다.
// @param request flows, communities, fts 플래그로 후처리 대상을 선택한다.
// @ensures 성공 시 수행된 후처리 결과 요약을 반환한다.
// @sideEffect 커뮤니티 재계산, 검색 인덱스 재생성, 캐시 비우기를 수행할 수 있다.
// @mutates community state, search index state, h.cache
func (h *handlers) runPostprocess(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ctx = h.applyWorkspace(ctx, request)
	log := h.logger()

	doFlows := request.GetBool("flows", true)
	doCommunities := request.GetBool("communities", true)
	doFTS := request.GetBool("fts", true)
	communityDepth := request.GetInt("community_depth", 2)

	log.Info("run_postprocess called", "flows", doFlows, "communities", doCommunities, "fts", doFTS)

	var communitiesCount, ftsIndexed int

	// TODO: doFlows — FlowTracer operates per-node; bulk rebuild not yet implemented

	if doCommunities && h.deps.CommunityBuilder != nil {
		stats, err := h.deps.CommunityBuilder.Rebuild(ctx, community.Config{Depth: communityDepth})
		if err != nil {
			log.Warn("community rebuild failed", trace.SlogError(err))
		} else {
			communitiesCount = len(stats)
		}
	}

	if doFTS && h.deps.SearchBackend != nil {
		if err := h.deps.SearchBackend.Rebuild(ctx, h.deps.DB); err != nil {
			log.Warn("search rebuild failed", trace.SlogError(err))
		} else {
			ftsIndexed = 1 // at least one rebuild happened
		}
	}

	result := map[string]any{
		"status":            "ok",
		"flows_count":       0,
		"communities_count": communitiesCount,
		"fts_indexed":       ftsIndexed,
	}
	jsonStr, err := marshalJSON(result)
	if err != nil {
		return nil, trace.Wrap(err, "marshal result")
	}
	if h.cache != nil {
		h.cache.Flush()
	}
	return mcp.NewToolResultText(jsonStr), nil
}
