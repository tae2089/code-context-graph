// @index MCP handler that enumerates graph namespaces for cross-namespace discovery.
package mcp

import (
	"context"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/tae2089/trace"
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
	Pagination pagination       `json:"pagination"`
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
	if limit > maxPaginationLimit {
		return finalizeToolResult("", newToolResultErr(fmt.Sprintf("limit must be <= %d, got %d", maxPaginationLimit, limit)))
	}

	stored, hasMore, err := h.deps.Graph.Reader.NamespacesPage(ctx, limit, offset)
	if err != nil {
		return finalizeToolResult("", trace.Wrap(err, "list namespaces"))
	}
	rows := make([]namespaceCount, len(stored))
	for i, row := range stored {
		rows[i] = namespaceCount{Namespace: row.Namespace, NodeCount: row.NodeCount}
	}

	page := pagination{Limit: limit, Offset: offset, Returned: len(rows), HasMore: hasMore}
	if hasMore {
		nextOffset := offset + limit
		page.NextOffset = &nextOffset
	}
	return finalizeToolResult(marshalJSON(listNamespacesResponse{
		Namespaces: rows,
		Count:      len(rows),
		Pagination: page,
	}))
}
