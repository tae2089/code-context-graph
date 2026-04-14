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

type walkParseStats struct {
	Files  int
	Nodes  int
	Edges  int
	Errors int
}

func (h *handlers) walkAndParse(ctx context.Context, dirPath string) (walkParseStats, error) {
	log := h.logger()
	var stats walkParseStats

	err := filepath.Walk(dirPath, func(fp string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			if pathutil.ShouldSkipDir(info.Name()) {
				return filepath.SkipDir
			}
			return nil
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

func (h *handlers) parseProject(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	log := h.logger()

	dirPath, err := request.RequireString("path")
	if err != nil {
		return missingParamResult(err)
	}

	log.Info("parse_project called", "path", dirPath)

	stats, err := h.walkAndParse(ctx, dirPath)
	if err != nil {
		return nil, err
	}

	log.Info("parse_project completed", "parsed", stats.Files, "errors", stats.Errors)
	return mcp.NewToolResultText(fmt.Sprintf(`{"parsed":%d,"errors":%d}`, stats.Files, stats.Errors)), nil
}

func (h *handlers) buildOrUpdateGraph(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	log := h.logger()

	dirPath, err := request.RequireString("path")
	if err != nil {
		return missingParamResult(err)
	}

	fullRebuild := request.GetBool("full_rebuild", true)
	postprocess := request.GetString("postprocess", "full")

	log.Info("build_or_update_graph called", "path", dirPath, "full_rebuild", fullRebuild, "postprocess", postprocess)

	start := time.Now()
	var nodeCount, edgeCount, fileCount int

	if fullRebuild || h.deps.Incremental == nil {
		stats, err := h.walkAndParse(ctx, dirPath)
		if err != nil {
			return nil, err
		}
		nodeCount = stats.Nodes
		edgeCount = stats.Edges
		fileCount = stats.Files
	} else {
		// 증분 빌드
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
				return nil
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

func (h *handlers) runPostprocess(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
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
