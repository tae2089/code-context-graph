package mcp

import (
	"context"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/tae2089/trace"

	"github.com/tae2089/code-context-graph/internal/ctxns"
	"github.com/tae2089/code-context-graph/internal/model"
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

// archCommCount is a helper struct for counting community nodes in architecture overview.
// @intent support community node counting in getArchitectureOverview without polluting model.Community.
type archCommCount struct {
	ID        uint
	Label     string
	NodeCount int64
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
// @intent 저장된 호출 흐름을 요약 형태로 노출해 탐색과 우선순위 판단을 돕는다.
// @param request sort_by와 limit로 정렬 방식과 최대 개수를 제어한다.
// @ensures 성공 시 flow id, 이름, 설명, 멤버 수를 포함한 목록을 반환한다.
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

		result, err := marshalJSON(map[string]any{
			"flows":         infos,
			"derived_state": derivedStateFlows(),
			"pagination":    buildPaginationMetadata(limit, offset, len(infos), hasMore),
		})
		if err != nil {
			return "", trace.Wrap(err, "marshal result")
		}
		return result, nil
	}))
}

// listCommunities lists communities with size-based filtering and sorting.
// @intent 커뮤니티 구조를 크기 기준으로 훑어볼 수 있게 요약 목록을 제공한다.
// @param request sort_by와 min_size로 응답 필터링 방식을 제어한다.
// @ensures 성공 시 커뮤니티 id, 라벨, 노드 수를 포함한 목록을 반환한다.
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

		result, err := marshalJSON(map[string]any{
			"communities":   infos,
			"derived_state": derivedStateCommunities(),
			"pagination":    buildPaginationMetadata(limit, offset, len(infos), hasMore),
		})
		if err != nil {
			return "", trace.Wrap(err, "marshal result")
		}
		return result, nil
	}))
}

// getCommunity returns community metadata with optional members and coverage.
// @intent 특정 커뮤니티의 규모와 구성원을 상세 조회할 수 있게 한다.
// @param request community_id는 필수이며 include_members가 멤버 포함 여부를 제어한다.
// @requires request.community_id가 존재하는 커뮤니티를 가리켜야 한다.
// @ensures 성공 시 커뮤니티 기본 정보와 선택적 coverage/members를 반환한다.
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

		gcData := map[string]any{
			"id":            comm.ID,
			"label":         comm.Label,
			"node_count":    memberCount,
			"derived_state": derivedStateCommunities(),
		}

		if h.deps.CoverageAnalyzer != nil {
			cc, err := h.deps.CoverageAnalyzer.ByCommunity(ctx, comm.ID)
			if err == nil && cc != nil {
				gcData["coverage"] = cc.Ratio
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

			members := make([]map[string]any, len(nodes))
			for i, n := range nodes {
				members[i] = nodeToBasicMap(n)
			}
			gcData["members"] = members
			gcData["members_pagination"] = buildPaginationMetadata(memberLimit, memberOffset, len(members), hasMore)
		}

		result, err := marshalJSON(gcData)
		if err != nil {
			return "", trace.Wrap(err, "marshal result")
		}
		return result, nil
	}))
}

// getArchitectureOverview summarizes communities, coupling, and architecture warnings.
// @intent 코드베이스의 모듈 경계와 강결합 구간을 한 응답으로 요약한다.
// @ensures 성공 시 커뮤니티 목록, 결합도 쌍, 경고 메시지를 반환한다.
// @domainRule 결합 강도 0.8 초과 쌍은 경고로 표기한다.
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

		commInfos := make([]map[string]any, len(archCCRows))
		for i, c := range archCCRows {
			commInfos[i] = map[string]any{
				"id":         c.ID,
				"label":      c.Label,
				"node_count": c.NodeCount,
			}
		}

		couplingPairs := []map[string]any{}
		warnings := []string{}
		if len(archCCRows) == 0 {
			warnings = []string{"No communities found. Run community rebuild first."}
		}

		if h.deps.CouplingAnalyzer != nil {
			pairs, err := h.deps.CouplingAnalyzer.Analyze(ctx)
			if err == nil {
				for _, cp := range pairs {
					couplingPairs = append(couplingPairs, map[string]any{
						"from":       cp.FromCommunity,
						"to":         cp.ToCommunity,
						"edge_count": cp.EdgeCount,
						"strength":   cp.Strength,
					})
					if cp.Strength > 0.8 {
						warnings = append(warnings, fmt.Sprintf("High coupling between %s and %s (strength: %.2f)", cp.FromCommunity, cp.ToCommunity, cp.Strength))
					}
				}
			}
		}

		couplingTotal := len(couplingPairs)
		couplingHasMore := false
		if couplingOffset > couplingTotal {
			couplingOffset = couplingTotal
		}
		couplingEnd := couplingOffset + couplingLimit
		if couplingEnd > couplingTotal {
			couplingEnd = couplingTotal
		}
		couplingPairs = couplingPairs[couplingOffset:couplingEnd]
		if couplingTotal > couplingEnd {
			couplingHasMore = true
		}

		result, err := marshalJSON(map[string]any{
			"communities":            commInfos,
			"communities_pagination": buildPaginationMetadata(communityLimit, communityOffset, len(commInfos), communityHasMore),
			"coupling":               couplingPairs,
			"coupling_pagination":    buildPaginationMetadata(couplingLimit, couplingOffset, len(couplingPairs), couplingHasMore),
			"warnings":               warnings,
			"derived_state":          derivedStateSummary(),
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
