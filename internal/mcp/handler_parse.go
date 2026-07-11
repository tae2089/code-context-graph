// @index MCP handlers for source parsing, full/incremental graph builds, and postprocess orchestration.
package mcp

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/tae2089/trace"

	"github.com/tae2089/code-context-graph/internal/analysis/community"
	flowspkg "github.com/tae2089/code-context-graph/internal/analysis/flows"
	"github.com/tae2089/code-context-graph/internal/obs"
	postprocesspolicy "github.com/tae2089/code-context-graph/internal/postprocess/policy"
	"github.com/tae2089/code-context-graph/internal/safepath"
	"github.com/tae2089/code-context-graph/internal/service"
)

// @intent refresh search documents through the injected override, defaulting to the service impl.
func (h *handlers) refreshSearchDocuments(ctx context.Context) (int, error) {
	if h.deps.RefreshSearchDocuments != nil {
		return h.deps.RefreshSearchDocuments(ctx, h.deps.DB)
	}
	return service.RefreshSearchDocuments(ctx, h.deps.DB)
}

// @intent serialize build_or_update_graph results with a fixed JSON schema without changing the wire format.
type buildOrUpdateGraphResponse struct {
	Status            string   `json:"status"`
	FilesParsed       int      `json:"files_parsed"`
	NodesCreated      int      `json:"nodes_created"`
	EdgesCreated      int      `json:"edges_created"`
	ElapsedMS         int64    `json:"elapsed_ms"`
	PostprocessPolicy string   `json:"postprocess_policy"`
	PolicySource      string   `json:"policy_source"`
	FailedSteps       []string `json:"failed_steps"`
	SkippedSteps      []string `json:"skipped_steps"`
}

// @intent serialize run_postprocess results with a fixed JSON schema without changing the wire format.
type runPostprocessResponse struct {
	Status            string   `json:"status"`
	FlowsCount        int      `json:"flows_count"`
	CommunitiesCount  int      `json:"communities_count"`
	FTSIndexed        int      `json:"fts_indexed"`
	PostprocessPolicy string   `json:"postprocess_policy"`
	PolicySource      string   `json:"policy_source"`
	FailedSteps       []string `json:"failed_steps"`
	SkippedSteps      []string `json:"skipped_steps"`
}

// @intent apply per-request parse limits without mutating the shared handler dependency configuration.
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

// @intent assemble a short-lived GraphService view from injected MCP dependencies for one parse or update request.
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
// @intent Loads the entire project into the graph store using a simple parsing tool.
// @param request Reads the directory to be parsed from the path parameter.
// @requires request.path must point to a valid directory.
// @ensures Returns the number of parsed files and error count as JSON on success.
// @sideEffect Performs filesystem reads, graph store writes, and logging.
func (h *handlers) parseProject(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	h = h.withParseLimitsFromRequest(request)
	ctx = h.applyNamespace(ctx, request)
	log := h.logger()

	dirPath, err := request.RequireString("path")
	if err != nil {
		return missingParamResult(err)
	}

	log.InfoContext(ctx, "parse_project called", append(obs.TraceLogArgs(ctx), "path", dirPath)...)

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

	log.InfoContext(ctx, "parse_project completed", append(obs.TraceLogArgs(ctx), "parsed", stats.TotalFiles, "errors", 0)...)
	if h.cache != nil {
		h.cache.Flush()
	}
	return mcp.NewToolResultText(fmt.Sprintf(`{"parsed":%d,"errors":%d}`, stats.TotalFiles, 0)), nil
}

// buildOrUpdateGraph builds the graph fully or incrementally and runs postprocessing.
// @intent Synchronizes the code graph to the latest state and performs search and community post-processing.
// @param request Controls the build strategy via full_rebuild and postprocess.
// @domainRule Always performs a full rebuild if the incremental syncer is not available.
// @requires request.path must be an accessible project directory.
// @ensures Returns the number of processed files and created nodes/edges on success.
// @sideEffect May perform filesystem reads, graph store updates, and search index/community rebuilds.
// @mutates graph store state, search index state, community state, h.cache
func (h *handlers) buildOrUpdateGraph(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	h = h.withParseLimitsFromRequest(request)
	ctx = h.applyNamespace(ctx, request)
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
	buildSkipSearchRebuild := true

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

	// Postprocessing
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
				log.WarnContext(ctx, "flow rebuild failed", append(obs.TraceLogArgs(ctx), trace.SlogError(err))...)
				failedSteps = append(failedSteps, "flows")
			}
		} else {
			skippedSteps = append(skippedSteps, "flows")
		}
		// community rebuild
		if h.deps.CommunityBuilder != nil {
			_, err := h.deps.CommunityBuilder.Rebuild(ctx, community.Config{Depth: 2})
			if err != nil {
				if failClosed {
					failClosedErr = err
					failedSteps = append(failedSteps, "communities")
					break
				}
				log.WarnContext(ctx, "community rebuild failed", append(obs.TraceLogArgs(ctx), trace.SlogError(err))...)
				failedSteps = append(failedSteps, "communities")
			}
		} else {
			skippedSteps = appendUniqueStrings(skippedSteps, "communities")
		}
		// search rebuild
		if h.deps.SearchBackend != nil && h.deps.DB != nil {
			if _, err := h.refreshSearchDocuments(ctx); err != nil {
				if failClosed {
					failClosedErr = err
					failedSteps = append(failedSteps, "search_documents")
					break
				}
				log.WarnContext(ctx, "search document refresh failed", append(obs.TraceLogArgs(ctx), trace.SlogError(err))...)
				failedSteps = append(failedSteps, "search_documents")
			} else if err := h.deps.SearchBackend.Rebuild(ctx, h.deps.DB); err != nil {
				if failClosed {
					failClosedErr = err
					failedSteps = append(failedSteps, "fts")
					break
				}
				log.WarnContext(ctx, "search rebuild failed", append(obs.TraceLogArgs(ctx), trace.SlogError(err))...)
				failedSteps = append(failedSteps, "fts")
			}
		} else {
			skippedSteps = appendUniqueStrings(skippedSteps, "search_documents", "fts")
		}
	case "minimal":
		skippedSteps = appendUniqueStrings(skippedSteps, "communities", "flows")
		// search only rebuild
		if h.deps.SearchBackend != nil && h.deps.DB != nil {
			if _, err := h.refreshSearchDocuments(ctx); err != nil {
				if failClosed {
					failClosedErr = err
					failedSteps = append(failedSteps, "search_documents")
					break
				}
				log.WarnContext(ctx, "search document refresh failed", append(obs.TraceLogArgs(ctx), trace.SlogError(err))...)
				failedSteps = append(failedSteps, "search_documents")
			} else if err := h.deps.SearchBackend.Rebuild(ctx, h.deps.DB); err != nil {
				if failClosed {
					failClosedErr = err
					failedSteps = append(failedSteps, "fts")
					break
				}
				log.WarnContext(ctx, "search rebuild failed", append(obs.TraceLogArgs(ctx), trace.SlogError(err))...)
				failedSteps = append(failedSteps, "fts")
			}
		} else {
			skippedSteps = appendUniqueStrings(skippedSteps, "search_documents", "fts")
		}
	case "none":
		// skip
		skippedSteps = appendUniqueStrings(skippedSteps, "communities", "flows", "search_documents", "fts")
	}

	elapsed := time.Since(start).Milliseconds()
	status := "ok"
	if len(failedSteps) > 0 {
		status = "degraded"
	}

	result := buildOrUpdateGraphResponse{
		Status:            status,
		FilesParsed:       fileCount,
		NodesCreated:      nodeCount,
		EdgesCreated:      edgeCount,
		ElapsedMS:         elapsed,
		PostprocessPolicy: postprocessPolicy,
		PolicySource:      string(policySource),
		FailedSteps:       failedSteps,
		SkippedSteps:      skippedSteps,
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
// @intent Independently regenerates communities and search indexes from existing graph data and reports availability for flow bulk rebuilds.
// @param request Selects post-processing targets via flows, communities, and fts flags.
// @ensures Returns a summary of post-processing results on success.
// @sideEffect May recalculate communities, regenerate search indexes, and flush the cache.
// @mutates community state, search index state, h.cache
func (h *handlers) runPostprocess(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ctx = h.applyNamespace(ctx, request)
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
	skippedSteps := []string{}
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
		if h.deps.SearchBackend != nil && h.deps.DB != nil {
			if _, err := h.refreshSearchDocuments(ctx); err != nil {
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

	result := runPostprocessResponse{
		Status:            status,
		FlowsCount:        flowsCount,
		CommunitiesCount:  communitiesCount,
		FTSIndexed:        ftsIndexed,
		PostprocessPolicy: postprocessPolicy,
		PolicySource:      string(policySource),
		FailedSteps:       failedSteps,
		SkippedSteps:      skippedSteps,
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

// @intent append values to a slice while preserving uniqueness for skipped-step reporting.
func appendUniqueStrings(dst []string, values ...string) []string {
	for _, value := range values {
		if !slices.Contains(dst, value) {
			dst = append(dst, value)
		}
	}
	return dst
}

// @intent restrict parse and build requests to configured analysis roots before filesystem traversal begins.
// @domainRule only paths contained in configured analysis roots may be parsed or rebuilt.
// @ensures returned path is canonical, existing, and contained within an allowed analysis root.
func (h *handlers) validateAnalysisPath(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("path is required")
	}
	allowedRoots := configuredAnalysisRoots(h.deps.RepoRoot, h.namespaceRoot())
	if len(allowedRoots) == 0 {
		return "", fmt.Errorf("analysis root is not configured")
	}
	target, err := safepath.Canonical(path, true)
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
