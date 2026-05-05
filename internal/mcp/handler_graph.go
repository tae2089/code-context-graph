// @index MCP handlers for graph summaries: stored flows, communities, and architecture overviews.
package mcp

import (
	"context"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/tae2089/trace"

	"github.com/tae2089/code-context-graph/internal/ctxns"
	"github.com/tae2089/code-context-graph/internal/model"
	"github.com/tae2089/code-context-graph/internal/paging"
)

// graphFlowInfo represents a summarized flow response entry.
// @intent serialize listFlows results with the legacy response shape.
type graphFlowInfo struct {
	ID          uint   `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	NodeCount   int    `json:"node_count"`
}

// graphCommInfo represents a summarized community response entry.
// @intent serialize listCommunities results with the legacy response shape.
type graphCommInfo struct {
	ID        uint    `json:"id"`
	Label     string  `json:"label"`
	NodeCount int     `json:"node_count"`
	Cohesion  float64 `json:"cohesion"`
}

// listFlowsResponse holds the listFlows wire payload.
// @intent preserve the legacy listFlows response shape with typed fields.
type listFlowsResponse struct {
	Flows        []graphFlowInfo `json:"flows"`
	DerivedState map[string]any  `json:"derived_state"`
	Pagination   map[string]any  `json:"pagination"`
}

// listCommunitiesResponse holds the listCommunities wire payload.
// @intent preserve the legacy listCommunities response shape with typed fields.
type listCommunitiesResponse struct {
	Communities  []graphCommInfo `json:"communities"`
	DerivedState map[string]any  `json:"derived_state"`
	Pagination   map[string]any  `json:"pagination"`
}

// communityMemberSummary is a typed member entry for getCommunity.
// @intent preserve the legacy community member shape with typed fields.
type communityMemberSummary = nodeSummary

// getCommunityResponse holds the getCommunity wire payload.
// @intent preserve the legacy getCommunity response shape with typed fields.
type getCommunityResponse struct {
	ID                uint                     `json:"id"`
	Label             string                   `json:"label"`
	NodeCount         int64                    `json:"node_count"`
	DerivedState      map[string]any           `json:"derived_state"`
	Coverage          *float64                 `json:"coverage,omitempty"`
	Members           []communityMemberSummary `json:"members,omitempty"`
	MembersPagination map[string]any           `json:"members_pagination,omitempty"`
}

// archCommCount is a helper struct for counting community nodes in architecture overview.
// @intent support community node counting in getArchitectureOverview without polluting model.Community.
type archCommCount struct {
	ID        uint
	Label     string
	NodeCount int64
}

// architectureOverviewCommunity is a typed community entry for architecture overview.
// @intent preserve the legacy communities item shape with typed fields.
type architectureOverviewCommunity struct {
	ID        uint   `json:"id"`
	Label     string `json:"label"`
	NodeCount int64  `json:"node_count"`
}

// architectureOverviewCoupling is a typed coupling entry for architecture overview.
// @intent preserve the legacy coupling item shape with typed fields.
type architectureOverviewCoupling struct {
	From      string  `json:"from"`
	To        string  `json:"to"`
	EdgeCount int64   `json:"edge_count"`
	Strength  float64 `json:"strength"`
}

// architectureOverviewResponse holds the architecture overview wire payload.
// @intent preserve the legacy architecture overview response shape with typed fields.
type architectureOverviewResponse struct {
	Communities           []architectureOverviewCommunity `json:"communities"`
	CommunitiesPagination map[string]any                  `json:"communities_pagination"`
	Coupling              []architectureOverviewCoupling  `json:"coupling"`
	CouplingPagination    map[string]any                  `json:"coupling_pagination"`
	Warnings              []string                        `json:"warnings"`
	DerivedState          map[string]any                  `json:"derived_state"`
}

// communityRow is a helper struct for counting community nodes in listCommunities.
// @intent support community node counting in listCommunities without polluting model.Community.
type communityRow struct {
	ID        uint
	Label     string
	NodeCount int64
}

// flowRow is a helper struct for counting flow nodes in listFlows.
// @intent support flow node counting in listFlows without polluting model.Flow.
type flowRow struct {
	ID          uint
	Name        string
	Description string
	NodeCount   int64
}

// listFlows lists stored flows with optional sorting and truncation.
// @intent Exposes stored call flows in a summarized format to aid in exploration and prioritization.
// @param request sort_by and limit control the sorting method and maximum number of results.
// @ensures Returns a list including flow ID, name, description, and member count on success.
// @see mcp.handlers.traceFlow
func (h *handlers) listFlows(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ctx = h.applyWorkspace(ctx, request)
	log := h.logger()

	sortBy := request.GetString("sort_by", "name")
	limit := request.GetInt("limit", defaultQueryGraphLimit)
	offset := request.GetInt("offset", 0)
	if err := validateQueryGraphLimit(limit); err != nil {
		return finalizeToolResult("", err)
	}
	if err := validateOffset(offset); err != nil {
		return finalizeToolResult("", err)
	}

	log.Info("list_flows called", "sort_by", sortBy, "limit", limit, "offset", offset)

	return finalizeToolResult(h.cachedExecute(ctx, "list_flows:", map[string]any{"sort_by": sortBy, "limit": limit, "offset": offset, "namespace": requestNamespace(request)}, func() (string, error) {
		ns := ctxns.FromContext(ctx)
		var flowRows []flowRow
		flowQ := h.deps.DB.WithContext(ctx).
			Model(&model.Flow{}).
			Select("flows.id AS id, flows.name AS name, flows.description AS description, COALESCE(COUNT(flow_memberships.id),0) AS node_count").
			Joins("LEFT JOIN flow_memberships ON flow_memberships.flow_id = flows.id AND flow_memberships.namespace = flows.namespace").
			Where("flows.namespace = ?", ns).
			Group("flows.id, flows.name, flows.description")

		switch sortBy {
		case "node_count":
			flowQ = flowQ.
				Order("node_count DESC").
				Order("flows.name ASC").
				Order("flows.id ASC")
		default:
			flowQ = flowQ.
				Order("flows.name ASC").
				Order("flows.id ASC")
		}

		fetchLimit := limit + 1
		if err := flowQ.Limit(fetchLimit).Offset(offset).Find(&flowRows).Error; err != nil {
			return "", trace.Wrap(err, "find flows")
		}

		hasMore := len(flowRows) > limit
		if hasMore {
			flowRows = flowRows[:limit]
		}

		infos := make([]graphFlowInfo, len(flowRows))
		for i, f := range flowRows {
			infos[i] = graphFlowInfo{
				ID:          f.ID,
				Name:        f.Name,
				Description: f.Description,
				NodeCount:   int(f.NodeCount),
			}
		}

		result, err := marshalJSON(listFlowsResponse{
			Flows:        infos,
			DerivedState: derivedStateFlows(),
			Pagination:   buildPaginationMetadata(limit, offset, len(infos), hasMore),
		})
		if err != nil {
			return "", trace.Wrap(err, "marshal result")
		}
		return result, nil
	}))
}

// listCommunities lists communities with size-based filtering and sorting.
// @intent Provides a summarized list of the community structure based on size.
// @param request sort_by and min_size control the filtering and sorting of the response.
// @ensures Returns a list including community ID, label, and node count on success.
// @see mcp.handlers.getCommunity
func (h *handlers) listCommunities(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ctx = h.applyWorkspace(ctx, request)
	log := h.logger()

	sortBy := request.GetString("sort_by", "size")
	minSize := request.GetInt("min_size", 0)
	limit := request.GetInt("limit", defaultQueryGraphLimit)
	offset := request.GetInt("offset", 0)
	if err := validateQueryGraphLimit(limit); err != nil {
		return finalizeToolResult("", err)
	}
	if err := validateOffset(offset); err != nil {
		return finalizeToolResult("", err)
	}

	log.Info("list_communities called", "sort_by", sortBy, "min_size", minSize, "limit", limit, "offset", offset)

	return finalizeToolResult(h.cachedExecute(ctx, "list_communities:", map[string]any{"sort_by": sortBy, "min_size": minSize, "limit": limit, "offset": offset, "namespace": requestNamespace(request)}, func() (string, error) {
		ns := ctxns.FromContext(ctx)
		var communityRows []communityRow
		communityQ := h.deps.DB.WithContext(ctx).
			Model(&model.Community{}).
			Select("communities.id AS id, communities.label AS label, COALESCE(COUNT(community_memberships.id),0) AS node_count").
			Joins("LEFT JOIN community_memberships ON community_memberships.community_id = communities.id").
			Where("communities.namespace = ?", ns).
			Group("communities.id, communities.label")
		if minSize > 0 {
			communityQ = communityQ.Having("COUNT(community_memberships.id) >= ?", minSize)
		}

		switch sortBy {
		case "name":
			communityQ = communityQ.Order("communities.label ASC").Order("communities.id ASC")
		default:
			communityQ = communityQ.Order("node_count DESC").Order("communities.label ASC").Order("communities.id ASC")
		}

		fetchLimit := limit + 1
		if err := communityQ.Limit(fetchLimit).Offset(offset).Find(&communityRows).Error; err != nil {
			return "", trace.Wrap(err, "find communities")
		}

		hasMore := len(communityRows) > limit
		if hasMore {
			communityRows = communityRows[:limit]
		}

		infos := make([]graphCommInfo, len(communityRows))
		for i, c := range communityRows {
			infos[i] = graphCommInfo{
				ID:        c.ID,
				Label:     c.Label,
				NodeCount: int(c.NodeCount),
			}
		}

		result, err := marshalJSON(listCommunitiesResponse{
			Communities:  infos,
			DerivedState: derivedStateCommunities(),
			Pagination:   buildPaginationMetadata(limit, offset, len(infos), hasMore),
		})
		if err != nil {
			return "", trace.Wrap(err, "marshal result")
		}
		return result, nil
	}))
}

// getCommunity returns community metadata with optional members and coverage.
// @intent Enables detailed lookup of a specific community's size and members.
// @param request community_id is required, and include_members controls whether to include the member list.
// @requires request.community_id must point to an existing community.
// @ensures Returns basic community information along with optional coverage and members on success.
// @see mcp.handlers.listCommunities
func (h *handlers) getCommunity(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ctx = h.applyWorkspace(ctx, request)
	log := h.logger()

	communityID := request.GetInt("community_id", 0)
	if communityID == 0 {
		return mcp.NewToolResultError("missing parameter: community_id"), nil
	}
	includeMembers := request.GetBool("include_members", false)
	memberLimit := request.GetInt("member_limit", 100)
	memberOffset := request.GetInt("member_offset", 0)
	if err := validateQueryGraphLimit(memberLimit); err != nil {
		return finalizeToolResult("", err)
	}
	if err := validateOffset(memberOffset); err != nil {
		return finalizeToolResult("", err)
	}

	log.Info("get_community called", "community_id", communityID, "include_members", includeMembers, "member_limit", memberLimit, "member_offset", memberOffset)

	var comm model.Community
	commQ := h.deps.DB.WithContext(ctx).Where("namespace = ?", ctxns.FromContext(ctx))
	if err := commQ.First(&comm, communityID).Error; err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("community %d not found", communityID)), nil
	}

	return finalizeToolResult(h.cachedExecute(ctx, "get_community:", map[string]any{"community_id": communityID, "include_members": includeMembers, "member_limit": memberLimit, "member_offset": memberOffset, "namespace": requestNamespace(request)}, func() (string, error) {
		ns := ctxns.FromContext(ctx)
		var memberCount int64
		memberQ := h.deps.DB.WithContext(ctx).Model(&model.CommunityMembership{}).
			Joins("JOIN communities ON communities.id = community_memberships.community_id").
			Where("community_id = ?", comm.ID).
			Where("communities.namespace = ?", ns)
		if err := memberQ.Count(&memberCount).Error; err != nil {
			return "", trace.Wrap(err, "count community members")
		}

		gcData := getCommunityResponse{
			ID:           comm.ID,
			Label:        comm.Label,
			NodeCount:    memberCount,
			DerivedState: derivedStateCommunities(),
		}

		if h.deps.CoverageAnalyzer != nil {
			cc, err := h.deps.CoverageAnalyzer.ByCommunity(ctx, comm.ID)
			if err == nil && cc != nil {
				coverage := cc.Ratio
				gcData.Coverage = &coverage
			}
		}

		if includeMembers {
			var nodes []model.Node
			membersQ := h.deps.DB.WithContext(ctx).Model(&model.Node{}).
				Select("nodes.*").
				Joins("JOIN community_memberships ON community_memberships.node_id = nodes.id").
				Joins("JOIN communities ON communities.id = community_memberships.community_id").
				Where("community_memberships.community_id = ?", comm.ID).
				Where("communities.namespace = ?", ns).
				Where("nodes.namespace = ?", ns).
				Order("nodes.file_path ASC").
				Order("nodes.start_line ASC").
				Order("nodes.id ASC")

			fetchLimit := memberLimit + 1
			if err := membersQ.Limit(fetchLimit).Offset(memberOffset).Find(&nodes).Error; err != nil {
				return "", trace.Wrap(err, "find community nodes")
			}

			hasMore := len(nodes) > memberLimit
			if hasMore {
				nodes = nodes[:memberLimit]
			}

			members := make([]communityMemberSummary, len(nodes))
			for i, n := range nodes {
				members[i] = nodeToSummary(n)
			}
			gcData.Members = members
			gcData.MembersPagination = buildPaginationMetadata(memberLimit, memberOffset, len(members), hasMore)
		}

		result, err := marshalJSON(gcData)
		if err != nil {
			return "", trace.Wrap(err, "marshal result")
		}
		return result, nil
	}))
}

// getArchitectureOverview summarizes communities, coupling, and architecture warnings.
// @intent Summarizes codebase module boundaries and tightly coupled areas in a single response.
// @ensures Returns a list of communities, coupling pairs, and warning messages on success.
// @domainRule Pairs with coupling strength exceeding 0.8 are marked as warnings.
// @see mcp.handlers.listCommunities
func (h *handlers) getArchitectureOverview(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ctx = h.applyWorkspace(ctx, request)
	log := h.logger()
	communityLimit := request.GetInt("community_limit", defaultQueryGraphLimit)
	communityOffset := request.GetInt("community_offset", 0)
	couplingLimit := request.GetInt("coupling_limit", defaultQueryGraphLimit)
	couplingOffset := request.GetInt("coupling_offset", 0)
	if err := validateQueryGraphLimit(communityLimit); err != nil {
		return finalizeToolResult("", err)
	}
	if err := validateOffset(communityOffset); err != nil {
		return finalizeToolResult("", err)
	}
	if err := validateQueryGraphLimit(couplingLimit); err != nil {
		return finalizeToolResult("", err)
	}
	if err := validateOffset(couplingOffset); err != nil {
		return finalizeToolResult("", err)
	}

	log.Info("get_architecture_overview called", "community_limit", communityLimit, "community_offset", communityOffset, "coupling_limit", couplingLimit, "coupling_offset", couplingOffset)

	return finalizeToolResult(h.cachedExecute(ctx, "get_architecture_overview:", map[string]any{
		"community_limit":  communityLimit,
		"community_offset": communityOffset,
		"coupling_limit":   couplingLimit,
		"coupling_offset":  couplingOffset,
		"namespace":        requestNamespace(request),
	}, func() (string, error) {
		ns := ctxns.FromContext(ctx)

		var archCCRows []archCommCount
		archCountQ := h.deps.DB.WithContext(ctx).
			Model(&model.Community{}).
			Select("communities.id AS id, communities.label AS label, COALESCE(COUNT(community_memberships.id),0) AS node_count").
			Joins("LEFT JOIN community_memberships ON community_memberships.community_id = communities.id").
			Where("communities.namespace = ?", ns).
			Group("communities.id, communities.label").
			Order("node_count DESC, communities.label ASC, communities.id ASC")

		archFetchLimit := communityLimit + 1
		if err := archCountQ.Limit(archFetchLimit).Offset(communityOffset).Find(&archCCRows).Error; err != nil {
			return "", trace.Wrap(err, "find communities for architecture overview")
		}

		communityHasMore := len(archCCRows) > communityLimit
		if communityHasMore {
			archCCRows = archCCRows[:communityLimit]
		}

		commInfos := make([]architectureOverviewCommunity, len(archCCRows))
		for i, c := range archCCRows {
			commInfos[i] = architectureOverviewCommunity{ID: c.ID, Label: c.Label, NodeCount: c.NodeCount}
		}

		couplingPairs := []architectureOverviewCoupling{}
		warnings := []string{}
		if len(archCCRows) == 0 {
			warnings = []string{"No communities found. Run community rebuild first."}
		}

		couplingHasMore := false
		if h.deps.CouplingAnalyzer != nil {
			page, err := h.deps.CouplingAnalyzer.AnalyzePage(ctx, paging.Request{Limit: couplingLimit, Offset: couplingOffset})
			if err == nil {
				for _, cp := range page.Items {
					couplingPairs = append(couplingPairs, architectureOverviewCoupling{From: cp.FromCommunity, To: cp.ToCommunity, EdgeCount: cp.EdgeCount, Strength: cp.Strength})
					if cp.Strength > 0.8 {
						warnings = append(warnings, fmt.Sprintf("High coupling between %s and %s (strength: %.2f)", cp.FromCommunity, cp.ToCommunity, cp.Strength))
					}
				}
				couplingHasMore = page.Pagination.HasMore
			}
		}

		result, err := marshalJSON(architectureOverviewResponse{
			Communities:           commInfos,
			CommunitiesPagination: buildPaginationMetadata(communityLimit, communityOffset, len(commInfos), communityHasMore),
			Coupling:              couplingPairs,
			CouplingPagination:    buildPaginationMetadata(couplingLimit, couplingOffset, len(couplingPairs), couplingHasMore),
			Warnings:              warnings,
			DerivedState:          derivedStateSummary(),
		})
		if err != nil {
			return "", trace.Wrap(err, "marshal result")
		}
		return result, nil
	}))
}

// @intent describe community-membership freshness so callers know when to re-run postprocess.
func derivedStateCommunities() map[string]any {
	return map[string]any{
		"communities": map[string]any{
			"freshness":    "unknown",
			"source":       "stored_community_memberships",
			"refresh_hint": "run_postprocess with communities=true after graph changes",
		},
	}
}

// @intent describe flow-membership freshness so callers know when to re-run postprocess.
func derivedStateFlows() map[string]any {
	return map[string]any{
		"flows": map[string]any{
			"freshness":    "unknown",
			"source":       "stored_flow_memberships",
			"refresh_hint": "run_postprocess with flows=true after graph changes",
		},
	}
}

// @intent merge community and flow freshness hints into a single derived-state map for status responses.
func derivedStateSummary() map[string]any {
	state := derivedStateCommunities()
	for k, v := range derivedStateFlows() {
		state[k] = v
	}
	return state
}
