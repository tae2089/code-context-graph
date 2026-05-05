// @index Typed decode/encode helpers for analysis MCP handlers (find_dead_code, find_suspect_fallback_edges, find_large_functions).
package mcp

import (
	"github.com/mark3labs/mcp-go/mcp"

	"github.com/tae2089/code-context-graph/internal/paging"
)

// findDeadCodeInput captures decoded request arguments for find_dead_code.
// @intent give findDeadCode a typed view of its request so the handler stays a thin adapter over deadcode.Service.
type findDeadCodeInput struct {
	Page       paging.Request
	Kinds      []string
	PathPrefix string
	Namespace  string
}

// findSuspectFallbackInput captures decoded request arguments for find_suspect_fallback_edges.
// @intent give findSuspectFallbackEdges a typed view of its request so the handler stays a thin adapter over the fallback analyzer.
type findSuspectFallbackInput struct {
	Page      paging.Request
	Namespace string
}

// findLargeFuncsInput captures decoded request arguments for find_large_functions.
// @intent give findLargeFunctions a typed view of its request so the handler stays a thin adapter over largefunc.Service.
type findLargeFuncsInput struct {
	MinLines   int
	Page       paging.Request
	PathPrefix string
	Namespace  string
}

// decodeFindDeadCodeRequest extracts and validates find_dead_code arguments.
// @intent isolate request parsing and pagination validation for find_dead_code.
// @ensures returned page request is normalized and limit/offset are non-negative.
func decodeFindDeadCodeRequest(request mcp.CallToolRequest) (findDeadCodeInput, error) {
	limit := request.GetInt("limit", 50)
	offset := request.GetInt("offset", 0)
	pageReq, err := normalizeListPaging(limit, offset)
	if err != nil {
		return findDeadCodeInput{}, err
	}
	return findDeadCodeInput{
		Page:       pageReq,
		Kinds:      request.GetStringSlice("kinds", nil),
		PathPrefix: request.GetString("path", ""),
		Namespace:  requestNamespace(request),
	}, nil
}

// decodeFindSuspectFallbackRequest extracts and validates find_suspect_fallback_edges arguments.
// @intent isolate request parsing and pagination validation for find_suspect_fallback_edges.
func decodeFindSuspectFallbackRequest(request mcp.CallToolRequest) (findSuspectFallbackInput, error) {
	limit := request.GetInt("limit", 50)
	offset := request.GetInt("offset", 0)
	pageReq, err := normalizeListPaging(limit, offset)
	if err != nil {
		return findSuspectFallbackInput{}, err
	}
	return findSuspectFallbackInput{
		Page:      pageReq,
		Namespace: requestNamespace(request),
	}, nil
}

// decodeFindLargeFuncsRequest extracts and validates find_large_functions arguments.
// @intent isolate request parsing and pagination validation for find_large_functions.
func decodeFindLargeFuncsRequest(request mcp.CallToolRequest) (findLargeFuncsInput, error) {
	limit := request.GetInt("limit", 50)
	offset := request.GetInt("offset", 0)
	pageReq, err := normalizeListPaging(limit, offset)
	if err != nil {
		return findLargeFuncsInput{}, err
	}
	return findLargeFuncsInput{
		MinLines:   request.GetInt("min_lines", 50),
		Page:       pageReq,
		PathPrefix: request.GetString("path", ""),
		Namespace:  requestNamespace(request),
	}, nil
}

// normalizeListPaging validates limit/offset and returns a normalized paging.Request.
// @intent share the limit-positive, offset-non-negative, normalize triple used by every analysis list handler.
func normalizeListPaging(limit, offset int) (paging.Request, error) {
	if err := validatePositiveLimit(limit); err != nil {
		return paging.Request{}, err
	}
	if err := validateOffset(offset); err != nil {
		return paging.Request{}, err
	}
	pageReq, err := paging.Normalize(paging.Request{Limit: limit, Offset: offset})
	if err != nil {
		return paging.Request{}, newToolResultErr(err.Error())
	}
	return pageReq, nil
}

// encodePagedListResponse serializes a paged list response with the legacy alias key plus shared items/count/pagination fields.
// @intent keep the {<legacyKey>, items, count, pagination} envelope identical across analysis list handlers.
// @param legacyKey is the historical response field name retained for backward compatibility.
func encodePagedListResponse(legacyKey string, items []map[string]any, pagination paging.Page) (string, error) {
	resp := map[string]any{
		legacyKey:    items,
		"items":      items,
		"count":      len(items),
		"pagination": pagination,
	}
	return marshalJSON(resp)
}
