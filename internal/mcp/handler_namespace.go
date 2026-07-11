// @index MCP handler that enumerates graph namespaces for cross-namespace discovery.
package mcp

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/tae2089/trace"

	"github.com/tae2089/code-context-graph/internal/model"
	"github.com/tae2089/code-context-graph/internal/paging"
)

// namespaceCount pairs a namespace with how many graph nodes it holds.
// @intent give list_namespaces a typed row for the distinct-namespace aggregate.
type namespaceCount struct {
	Namespace string `json:"namespace"`
	NodeCount int64  `json:"node_count"`
}

// listNamespacesResponse is the wire payload for list_namespaces.
// @intent report which namespaces contain graph data so callers can scope later queries.
type listNamespacesResponse struct {
	Namespaces []namespaceCount `json:"namespaces"`
	Count      int              `json:"count"`
	Pagination paging.Page      `json:"pagination"`
}

// listNamespaces lists namespaces that hold graph nodes, with per-namespace node counts.
// @intent let agents discover available namespaces before scoping search or graph queries.
// @param request limit and offset control pagination; the query spans all namespaces.
// @ensures Returns namespaces sorted by name with node counts and pagination metadata.
func (h *handlers) listNamespaces(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	limit := request.GetInt("limit", 50)
	offset := request.GetInt("offset", 0)
	if err := validatePositiveLimit(limit); err != nil {
		return finalizeToolResult("", err)
	}
	if err := validateOffset(offset); err != nil {
		return finalizeToolResult("", err)
	}
	pageReq, err := paging.Normalize(paging.Request{Limit: limit, Offset: offset})
	if err != nil {
		return finalizeToolResult("", newToolResultErr(err.Error()))
	}

	rows := []namespaceCount{}
	if err := h.deps.DB.WithContext(ctx).
		Model(&model.Node{}).
		Select("namespace, COUNT(*) AS node_count").
		Group("namespace").
		Order("namespace ASC").
		Limit(pageReq.Limit + 1).
		Offset(pageReq.Offset).
		Scan(&rows).Error; err != nil {
		return finalizeToolResult("", trace.Wrap(err, "list namespaces"))
	}

	hasMore := len(rows) > pageReq.Limit
	if hasMore {
		rows = rows[:pageReq.Limit]
	}

	return finalizeToolResult(marshalJSON(listNamespacesResponse{
		Namespaces: rows,
		Count:      len(rows),
		Pagination: paging.BuildPage(pageReq, len(rows), hasMore),
	}))
}
