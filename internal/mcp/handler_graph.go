package mcp

import (
	"context"
	"fmt"
	"sort"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/tae2089/trace"

	"github.com/tae2089/code-context-graph/internal/ctxns"
	"github.com/tae2089/code-context-graph/internal/model"
)

// listFlows lists stored flows with optional sorting and truncation.
// @intent 저장된 호출 흐름을 요약 형태로 노출해 탐색과 우선순위 판단을 돕는다.
// @param request sort_by와 limit로 정렬 방식과 최대 개수를 제어한다.
// @ensures 성공 시 flow id, 이름, 설명, 멤버 수를 포함한 목록을 반환한다.
// @see mcp.handlers.traceFlow
func (h *handlers) listFlows(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ctx = h.applyWorkspace(ctx, request)
	log := h.logger()

	sortBy := request.GetString("sort_by", "name")
	limit := request.GetInt("limit", 50)

	log.Info("list_flows called", "sort_by", sortBy, "limit", limit)

	// flowCount stores aggregated membership counts per flow.
	// @intent flow_id별 멤버 수 집계 결과를 스캔하기 위한 임시 구조체다.
	type flowCount struct {
		FlowID uint
		Count  int
	}

	return finalizeToolResult(h.cachedExecute("list_flows:", map[string]any{"sort_by": sortBy, "limit": limit, "workspace": request.GetString("workspace", "")}, func() (string, error) {
		var fcRows []flowCount
		if err := h.deps.DB.WithContext(ctx).
			Model(&model.FlowMembership{}).
			Select("flow_id, COUNT(*) as count").
			Group("flow_id").
			Scan(&fcRows).Error; err != nil {
			return "", trace.Wrap(err, "group flow memberships")
		}

		fcMap := make(map[uint]int, len(fcRows))
		for _, r := range fcRows {
			fcMap[r.FlowID] = r.Count
		}

		var flowList []model.Flow
		if err := h.deps.DB.WithContext(ctx).Find(&flowList).Error; err != nil {
			return "", trace.Wrap(err, "find flows")
		}

		// flowInfo represents a summarized flow response entry.
		// @intent MCP 응답에서 각 flow의 핵심 정보만 직렬화한다.
		type flowInfo struct {
			ID          uint   `json:"id"`
			Name        string `json:"name"`
			Description string `json:"description"`
			NodeCount   int    `json:"node_count"`
		}

		infos := make([]flowInfo, len(flowList))
		for i, f := range flowList {
			infos[i] = flowInfo{
				ID:          f.ID,
				Name:        f.Name,
				Description: f.Description,
				NodeCount:   fcMap[f.ID],
			}
		}

		switch sortBy {
		case "node_count":
			sort.Slice(infos, func(i, j int) bool {
				return infos[i].NodeCount > infos[j].NodeCount
			})
		default:
			sort.Slice(infos, func(i, j int) bool {
				return infos[i].Name < infos[j].Name
			})
		}

		if len(infos) > limit {
			infos = infos[:limit]
		}

		result, err := marshalJSON(map[string]any{"flows": infos})
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

	log.Info("list_communities called", "sort_by", sortBy, "min_size", minSize)

	// commCount stores aggregated membership counts per community.
	// @intent community_id별 멤버 수 집계 결과를 스캔하기 위한 임시 구조체다.
	type commCount struct {
		CommunityID uint
		Count       int
	}

	return finalizeToolResult(h.cachedExecute("list_communities:", map[string]any{"sort_by": sortBy, "min_size": minSize, "workspace": request.GetString("workspace", "")}, func() (string, error) {
		var ccRows []commCount
		if err := h.deps.DB.WithContext(ctx).
			Model(&model.CommunityMembership{}).
			Select("community_id, COUNT(*) as count").
			Group("community_id").
			Scan(&ccRows).Error; err != nil {
			return "", trace.Wrap(err, "group community memberships")
		}

		ccMap := make(map[uint]int, len(ccRows))
		for _, r := range ccRows {
			ccMap[r.CommunityID] = r.Count
		}

		var communities []model.Community
		if err := h.deps.DB.WithContext(ctx).Find(&communities).Error; err != nil {
			return "", trace.Wrap(err, "find communities")
		}

		// commInfo represents a summarized community response entry.
		// @intent MCP 응답에서 커뮤니티의 핵심 메타데이터만 직렬화한다.
		type commInfo struct {
			ID        uint    `json:"id"`
			Label     string  `json:"label"`
			NodeCount int     `json:"node_count"`
			Cohesion  float64 `json:"cohesion"`
		}

		infos := make([]commInfo, 0, len(communities))
		for _, c := range communities {
			cnt := ccMap[c.ID]
			if cnt < minSize {
				continue
			}
			infos = append(infos, commInfo{
				ID:        c.ID,
				Label:     c.Label,
				NodeCount: cnt,
			})
		}

		switch sortBy {
		case "name":
			sort.Slice(infos, func(i, j int) bool {
				return infos[i].Label < infos[j].Label
			})
		default:
			sort.Slice(infos, func(i, j int) bool {
				return infos[i].NodeCount > infos[j].NodeCount
			})
		}

		result, err := marshalJSON(map[string]any{"communities": infos})
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

	log.Info("get_community called", "community_id", communityID, "include_members", includeMembers)

	var comm model.Community
	if err := h.deps.DB.WithContext(ctx).First(&comm, communityID).Error; err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("community %d not found", communityID)), nil
	}

	return finalizeToolResult(h.cachedExecute("get_community:", map[string]any{"community_id": communityID, "include_members": includeMembers, "workspace": request.GetString("workspace", "")}, func() (string, error) {
		var memberCount int64
		if err := h.deps.DB.WithContext(ctx).Model(&model.CommunityMembership{}).
			Where("community_id = ?", comm.ID).Count(&memberCount).Error; err != nil {
			return "", trace.Wrap(err, "count community members")
		}

		gcData := map[string]any{
			"id":         comm.ID,
			"label":      comm.Label,
			"node_count": memberCount,
		}

		if h.deps.CoverageAnalyzer != nil {
			cc, err := h.deps.CoverageAnalyzer.ByCommunity(ctx, comm.ID)
			if err == nil && cc != nil {
				gcData["coverage"] = cc.Ratio
			}
		}

		if includeMembers {
			var memberships []model.CommunityMembership
			if err := h.deps.DB.WithContext(ctx).Where("community_id = ?", comm.ID).Find(&memberships).Error; err != nil {
				return "", trace.Wrap(err, "find community memberships")
			}

			nodeIDs := make([]uint, len(memberships))
			for i, m := range memberships {
				nodeIDs[i] = m.NodeID
			}

			var nodes []model.Node
			if len(nodeIDs) > 0 {
				nodesQ := h.deps.DB.WithContext(ctx).Where("id IN ?", nodeIDs)
				if ns := ctxns.FromContext(ctx); ns != "" {
					nodesQ = nodesQ.Where("namespace = ?", ns)
				}
				if err := nodesQ.Find(&nodes).Error; err != nil {
					return "", trace.Wrap(err, "find community nodes")
				}
			}

			members := make([]map[string]any, len(nodes))
			for i, n := range nodes {
				members[i] = nodeToBasicMap(n)
			}
			gcData["members"] = members
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
	log.Info("get_architecture_overview called")

	return finalizeToolResult(h.cachedExecute("get_architecture_overview:", map[string]any{"workspace": request.GetString("workspace", "")}, func() (string, error) {
		var communities []model.Community
		if err := h.deps.DB.WithContext(ctx).Find(&communities).Error; err != nil {
			return "", trace.Wrap(err, "find communities for architecture overview")
		}

		if len(communities) == 0 {
			result, err := marshalJSON(map[string]any{
				"communities": []any{},
				"coupling":    []any{},
				"warnings":    []string{"No communities found. Run community rebuild first."},
			})
			if err != nil {
				return "", trace.Wrap(err, "marshal result")
			}
			return result, nil
		}

		// archCommCount stores aggregated membership counts for overview output.
		// @intent 아키텍처 개요에서 community별 노드 수를 계산하기 위한 임시 구조체다.
		type archCommCount struct {
			CommunityID uint
			Count       int
		}
		var archCCRows []archCommCount
		if err := h.deps.DB.WithContext(ctx).
			Model(&model.CommunityMembership{}).
			Select("community_id, COUNT(*) as count").
			Group("community_id").
			Scan(&archCCRows).Error; err != nil {
			return "", trace.Wrap(err, "group community memberships for architecture overview")
		}

		archCCMap := make(map[uint]int, len(archCCRows))
		for _, r := range archCCRows {
			archCCMap[r.CommunityID] = r.Count
		}

		commInfos := make([]map[string]any, len(communities))
		for i, c := range communities {
			commInfos[i] = map[string]any{
				"id":         c.ID,
				"label":      c.Label,
				"node_count": archCCMap[c.ID],
			}
		}

		couplingPairs := []map[string]any{}
		warnings := []string{}

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

		result, err := marshalJSON(map[string]any{
			"communities": commInfos,
			"coupling":    couplingPairs,
			"warnings":    warnings,
		})
		if err != nil {
			return "", trace.Wrap(err, "marshal result")
		}
		return result, nil
	}))
}
