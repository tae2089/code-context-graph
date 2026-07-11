// @index MCP handlers for graph summaries: stored flows, communities, and architecture overviews.
package mcp

import (
	"context"

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

// listFlowsResponse holds the listFlows wire payload.
// @intent preserve the legacy listFlows response shape with typed fields.
type listFlowsResponse struct {
	Flows        []graphFlowInfo `json:"flows"`
	DerivedState map[string]any  `json:"derived_state"`
	Pagination   paging.Page     `json:"pagination"`
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
	ctx = h.applyNamespace(ctx, request)
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
			Pagination:   paging.BuildPage(paging.Request{Limit: limit, Offset: offset}, len(infos), hasMore),
		})
		if err != nil {
			return "", trace.Wrap(err, "marshal result")
		}
		return result, nil
	}))
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
	return derivedStateFlows()
}
