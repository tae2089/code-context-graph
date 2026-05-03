package mcp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/tae2089/trace"

	"github.com/tae2089/code-context-graph/internal/analysis/community"
	flowspkg "github.com/tae2089/code-context-graph/internal/analysis/flows"
	postprocesspolicy "github.com/tae2089/code-context-graph/internal/postprocess/policy"
	"github.com/tae2089/code-context-graph/internal/service"
)

var refreshSearchDocuments = service.RefreshSearchDocuments

func (h *handlers) withParseLimitsFromRequest(request mcp.CallToolRequest) *handlers {
	maxFileBytes := int64(request.GetInt("max_file_bytes", int(h.deps.MaxFileBytes)))
	maxTotalParsedBytes := int64(request.GetInt("max_total_parsed_bytes", int(h.deps.MaxTotalParsedBytes)))
	if maxFileBytes == h.deps.MaxFileBytes && maxTotalParsedBytes == h.deps.MaxTotalParsedBytes {
		return h
	}
	depsCopy := *h.deps
	depsCopy.MaxFileBytes = maxFileBytes
	depsCopy.MaxTotalParsedBytes = maxTotalParsedBytes
	hCopy := *h
	hCopy.deps = &depsCopy
	return &hCopy
}

func (h *handlers) graphService() *service.GraphService {
	walkers := make(map[string]service.Parser, len(h.deps.Walkers))
	for ext, parser := range h.deps.Walkers {
		walkers[ext] = parser
	}
	return &service.GraphService{
		Store:         h.deps.Store,
		DB:            h.deps.DB,
		SearchBackend: h.deps.SearchBackend,
		Parsers:       walkers,
		Logger:        h.logger(),
	}
}

// parseProject parses a project directory and stores discovered graph elements.
// @intent 단순 파싱 도구로 프로젝트 전체를 그래프 저장소에 적재한다.
// @param request path 파라미터에서 파싱 대상 디렉터리를 읽는다.
// @requires request.path가 유효한 디렉터리를 가리켜야 한다.
// @ensures 성공 시 파싱된 파일 수와 오류 수를 JSON으로 반환한다.
// @sideEffect 파일 시스템 읽기, 그래프 저장소 쓰기, 로그 기록을 수행한다.
func (h *handlers) parseProject(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	h = h.withParseLimitsFromRequest(request)
	ctx = h.applyWorkspace(ctx, request)
	log := h.logger()

	dirPath, err := request.RequireString("path")
	if err != nil {
		return missingParamResult(err)
	}

	log.Info("parse_project called", "path", dirPath)

	validatedPath, err := h.validateAnalysisPath(dirPath)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	dirPath = validatedPath

	includePaths := request.GetStringSlice("include_paths", nil)
	stats, err := h.graphService().Build(ctx, service.BuildOptions{
		Dir:                 dirPath,
		IncludePaths:        includePaths,
		MaxFileBytes:        h.deps.MaxFileBytes,
		MaxTotalParsedBytes: h.deps.MaxTotalParsedBytes,
		SkipSearchRebuild:   true,
	})
	if err != nil {
		return nil, err
	}

	log.Info("parse_project completed", "parsed", stats.TotalFiles, "errors", 0)
	if h.cache != nil {
		h.cache.Flush()
	}
	return mcp.NewToolResultText(fmt.Sprintf(`{"parsed":%d,"errors":%d}`, stats.TotalFiles, 0)), nil
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
	h = h.withParseLimitsFromRequest(request)
	ctx = h.applyWorkspace(ctx, request)
	log := h.logger()

	dirPath, err := request.RequireString("path")
	if err != nil {
		return missingParamResult(err)
	}

	fullRebuild := request.GetBool("full_rebuild", true)
	postprocess := request.GetString("postprocess", "full")
	postprocessPolicy := request.GetString("postprocess_policy", "")
	policySource := postprocesspolicy.SourceExplicit
	if postprocessPolicy == "" {
		policySource = postprocesspolicy.SourceAuto
	}
	includePaths := request.GetStringSlice("include_paths", nil)
	replace := request.GetBool("replace", true)

	if postprocessPolicy != "" && postprocessPolicy != postprocesspolicy.PolicyDegraded && postprocessPolicy != postprocesspolicy.PolicyFailClosed {
		return mcp.NewToolResultError("postprocess_policy must be degraded or fail_closed"), nil
	}
	if postprocess != "full" && postprocess != "minimal" && postprocess != "none" {
		return mcp.NewToolResultError("postprocess must be full, minimal, or none"), nil
	}
	if h.deps.PostprocessPolicy != nil {
		resolvedPolicy, resolvedSource, err := h.deps.PostprocessPolicy.Resolve(ctx, postprocesspolicy.DecisionInput{
			Tool:           postprocesspolicy.ToolBuildOrUpdateGraph,
			ExplicitPolicy: postprocessPolicy,
		})
		if err != nil {
			return nil, err
		}
		postprocessPolicy = resolvedPolicy
		policySource = resolvedSource
	} else if postprocessPolicy == "" {
		postprocessPolicy = postprocesspolicy.PolicyDegraded
	}
	failClosed := postprocessPolicy == postprocesspolicy.PolicyFailClosed && postprocess != "none"

	log.Info("build_or_update_graph called", "path", dirPath, "full_rebuild", fullRebuild, "postprocess", postprocess, "postprocess_policy", postprocessPolicy)

	validatedPath, err := h.validateAnalysisPath(dirPath)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	dirPath = validatedPath

	start := time.Now()
	var nodeCount, edgeCount, fileCount int
	buildSkipSearchRebuild := postprocess == "none"

	if fullRebuild || h.deps.Incremental == nil {
		stats, err := h.graphService().Build(ctx, service.BuildOptions{
			Dir:                 dirPath,
			IncludePaths:        includePaths,
			MaxFileBytes:        h.deps.MaxFileBytes,
			MaxTotalParsedBytes: h.deps.MaxTotalParsedBytes,
			SkipSearchRebuild:   buildSkipSearchRebuild,
		})
		if err != nil {
			return nil, err
		}
		nodeCount = stats.TotalNodes
		edgeCount = stats.TotalEdges
		fileCount = stats.TotalFiles
	} else {
		stats, err := h.graphService().Update(ctx, service.UpdateOptions{
			BuildOptions: service.BuildOptions{
				Dir:                 dirPath,
				IncludePaths:        includePaths,
				MaxFileBytes:        h.deps.MaxFileBytes,
				MaxTotalParsedBytes: h.deps.MaxTotalParsedBytes,
				SkipSearchRebuild:   buildSkipSearchRebuild,
			},
			Syncer:  h.deps.Incremental,
			Replace: replace,
		})
		if err != nil {
			return nil, err
		}
		fileCount = stats.Added + stats.Modified
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// 후처리
	var failedSteps []string
	var skippedSteps []string
	var failClosedErr error
	switch postprocess {
	case "full":
		if h.deps.FlowBuilder != nil {
			if _, err := h.deps.FlowBuilder.Rebuild(ctx, flowspkg.Config{}); err != nil {
				if failClosed {
					failClosedErr = err
					failedSteps = append(failedSteps, "flows")
					break
				}
				log.Warn("flow rebuild failed", trace.SlogError(err))
				failedSteps = append(failedSteps, "flows")
			}
		} else {
			skippedSteps = append(skippedSteps, "flows")
		}
		// community 재빌드
		if h.deps.CommunityBuilder != nil {
			_, err := h.deps.CommunityBuilder.Rebuild(ctx, community.Config{Depth: 2})
			if err != nil {
				if failClosed {
					failClosedErr = err
					failedSteps = append(failedSteps, "communities")
					break
				}
				log.Warn("community rebuild failed", trace.SlogError(err))
				failedSteps = append(failedSteps, "communities")
			}
		} else {
			skippedSteps = appendUniqueStrings(skippedSteps, "communities")
		}
		// search 재빌드
		if h.deps.SearchBackend != nil {
			if _, err := refreshSearchDocuments(ctx, h.deps.DB); err != nil {
				if failClosed {
					failClosedErr = err
					failedSteps = append(failedSteps, "search_documents")
					break
				}
				log.Warn("search document refresh failed", trace.SlogError(err))
				failedSteps = append(failedSteps, "search_documents")
			}
			if err := h.deps.SearchBackend.Rebuild(ctx, h.deps.DB); err != nil {
				if failClosed {
					failClosedErr = err
					failedSteps = append(failedSteps, "fts")
					break
				}
				log.Warn("search rebuild failed", trace.SlogError(err))
				failedSteps = append(failedSteps, "fts")
			}
		} else {
			skippedSteps = appendUniqueStrings(skippedSteps, "search_documents", "fts")
		}
	case "minimal":
		skippedSteps = appendUniqueStrings(skippedSteps, "communities", "flows")
		// search만 재빌드
		if h.deps.SearchBackend != nil {
			if _, err := refreshSearchDocuments(ctx, h.deps.DB); err != nil {
				if failClosed {
					failClosedErr = err
					failedSteps = append(failedSteps, "search_documents")
					break
				}
				log.Warn("search document refresh failed", trace.SlogError(err))
				failedSteps = append(failedSteps, "search_documents")
			}
			if err := h.deps.SearchBackend.Rebuild(ctx, h.deps.DB); err != nil {
				if failClosed {
					failClosedErr = err
					failedSteps = append(failedSteps, "fts")
					break
				}
				log.Warn("search rebuild failed", trace.SlogError(err))
				failedSteps = append(failedSteps, "fts")
			}
		} else {
			skippedSteps = appendUniqueStrings(skippedSteps, "search_documents", "fts")
		}
	case "none":
		// 스킵
		skippedSteps = appendUniqueStrings(skippedSteps, "communities", "flows", "search_documents", "fts")
	}

	elapsed := time.Since(start).Milliseconds()
	status := "ok"
	if len(failedSteps) > 0 {
		status = "degraded"
	}

	result := map[string]any{
		"status":        status,
		"files_parsed":  fileCount,
		"nodes_created": nodeCount,
		"edges_created": edgeCount,
		"elapsed_ms":    elapsed,
		"postprocess_policy": postprocessPolicy,
		"policy_source":      policySource,
		"failed_steps":  failedSteps,
		"skipped_steps": skippedSteps,
	}
	if h.deps.PostprocessPolicy != nil {
		if err := h.deps.PostprocessPolicy.RecordRun(ctx, postprocesspolicy.RunRecord{
			Tool:         postprocesspolicy.ToolBuildOrUpdateGraph,
			Policy:       postprocessPolicy,
			Source:       policySource,
			Status:       status,
			FailedSteps:  failedSteps,
			SkippedSteps: skippedSteps,
		}); err != nil {
			return nil, err
		}
	}
	if failClosedErr != nil {
		return mcp.NewToolResultError(failClosedErr.Error()), nil
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
// @intent 기존 그래프 데이터에서 커뮤니티와 검색 인덱스를 독립적으로 재생성하고 flow bulk rebuild 가용성을 보고한다.
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
	postprocessPolicy := request.GetString("postprocess_policy", "")
	policySource := postprocesspolicy.SourceExplicit
	if postprocessPolicy == "" {
		policySource = postprocesspolicy.SourceAuto
	}
	communityDepth := request.GetInt("community_depth", 2)
	if communityDepth < 1 || communityDepth > 8 {
		return mcp.NewToolResultError("community_depth must be between 1 and 8"), nil
	}
	if postprocessPolicy != "" && postprocessPolicy != postprocesspolicy.PolicyDegraded && postprocessPolicy != postprocesspolicy.PolicyFailClosed {
		return mcp.NewToolResultError("postprocess_policy must be degraded or fail_closed"), nil
	}
	if h.deps.PostprocessPolicy != nil {
		resolvedPolicy, resolvedSource, err := h.deps.PostprocessPolicy.Resolve(ctx, postprocesspolicy.DecisionInput{
			Tool:           postprocesspolicy.ToolRunPostprocess,
			ExplicitPolicy: postprocessPolicy,
		})
		if err != nil {
			return nil, err
		}
		postprocessPolicy = resolvedPolicy
		policySource = resolvedSource
	} else if postprocessPolicy == "" {
		postprocessPolicy = postprocesspolicy.PolicyDegraded
	}
	failClosed := postprocessPolicy == postprocesspolicy.PolicyFailClosed

	log.Info("run_postprocess called", "flows", doFlows, "communities", doCommunities, "fts", doFTS)

	var flowsCount, communitiesCount, ftsIndexed int
	var failedSteps []string
	var skippedSteps []string
	var failClosedErr error

	if doFlows {
		if h.deps.FlowBuilder != nil {
			stats, err := h.deps.FlowBuilder.Rebuild(ctx, flowspkg.Config{})
			if err != nil {
				if failClosed {
					failClosedErr = err
					failedSteps = append(failedSteps, "flows")
				}
				if failClosedErr == nil {
					log.Warn("flow rebuild failed", trace.SlogError(err))
					failedSteps = append(failedSteps, "flows")
				}
			} else {
				flowsCount = len(stats)
			}
		} else {
			skippedSteps = appendUniqueStrings(skippedSteps, "flows")
		}
	} else {
		skippedSteps = appendUniqueStrings(skippedSteps, "flows")
	}

	if doCommunities {
		if h.deps.CommunityBuilder != nil {
			stats, err := h.deps.CommunityBuilder.Rebuild(ctx, community.Config{Depth: communityDepth})
			if err != nil {
				if failClosed {
					failClosedErr = err
					failedSteps = append(failedSteps, "communities")
				} else {
					log.Warn("community rebuild failed", trace.SlogError(err))
					failedSteps = append(failedSteps, "communities")
				}
			} else {
				communitiesCount = len(stats)
			}
		} else {
			skippedSteps = appendUniqueStrings(skippedSteps, "communities")
		}
	} else {
		skippedSteps = appendUniqueStrings(skippedSteps, "communities")
	}

	if doFTS {
		if h.deps.SearchBackend != nil {
			if _, err := refreshSearchDocuments(ctx, h.deps.DB); err != nil {
				if failClosed {
					failClosedErr = err
					failedSteps = append(failedSteps, "search_documents")
				} else {
					log.Warn("search document refresh failed", trace.SlogError(err))
					failedSteps = append(failedSteps, "search_documents")
				}
			} else if err := h.deps.SearchBackend.Rebuild(ctx, h.deps.DB); err != nil {
				if failClosed {
					failClosedErr = err
					failedSteps = append(failedSteps, "fts")
				} else {
					log.Warn("search rebuild failed", trace.SlogError(err))
					failedSteps = append(failedSteps, "fts")
				}
			} else {
				ftsIndexed = 1 // at least one rebuild happened
			}
		} else {
			skippedSteps = appendUniqueStrings(skippedSteps, "search_documents", "fts")
		}
	} else {
		skippedSteps = appendUniqueStrings(skippedSteps, "search_documents", "fts")
	}

	status := "ok"
	if len(failedSteps) > 0 {
		status = "degraded"
	}

	result := map[string]any{
		"status":            status,
		"flows_count":       flowsCount,
		"communities_count": communitiesCount,
		"fts_indexed":       ftsIndexed,
		"postprocess_policy": postprocessPolicy,
		"policy_source":      policySource,
		"failed_steps":      failedSteps,
		"skipped_steps":     skippedSteps,
	}
	if h.deps.PostprocessPolicy != nil {
		if err := h.deps.PostprocessPolicy.RecordRun(ctx, postprocesspolicy.RunRecord{
			Tool:         postprocesspolicy.ToolRunPostprocess,
			Policy:       postprocessPolicy,
			Source:       policySource,
			Status:       status,
			FailedSteps:  failedSteps,
			SkippedSteps: skippedSteps,
		}); err != nil {
			return nil, err
		}
	}
	if failClosedErr != nil {
		return mcp.NewToolResultError(failClosedErr.Error()), nil
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

func appendUniqueStrings(dst []string, values ...string) []string {
	for _, value := range values {
		if !slices.Contains(dst, value) {
			dst = append(dst, value)
		}
	}
	return dst
}

func (h *handlers) validateAnalysisPath(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("path is required")
	}
	allowedRoots := configuredAnalysisRoots(h.deps.RepoRoot, h.workspaceRoot())
	if len(allowedRoots) == 0 {
		return "", fmt.Errorf("analysis root is not configured")
	}
	target, err := canonicalExistingPath(path)
	if err != nil {
		return "", fmt.Errorf("invalid path: %w", err)
	}
	allowed, err := validatePathWithinAllowedRoots(target, allowedRoots)
	if err != nil {
		return "", fmt.Errorf("invalid configured analysis root: %w", err)
	}
	if !allowed {
		return "", fmt.Errorf("path %q is outside configured analysis root", path)
	}
	return target, nil
}

func canonicalExistingPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	clean := filepath.Clean(abs)
	real, err := filepath.EvalSymlinks(clean)
	if err == nil {
		return filepath.Clean(real), nil
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
