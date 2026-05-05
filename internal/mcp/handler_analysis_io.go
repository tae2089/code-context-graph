// @index Typed decode/encode helpers for analysis MCP handlers (find_dead_code, find_suspect_fallback_edges, find_large_functions).
package mcp

import (
	"encoding/json"

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
	pageReq, err := decodeListPageRequest(request, 50)
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
	pageReq, err := decodeListPageRequest(request, 50)
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
	pageReq, err := decodeListPageRequest(request, 50)
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

// decodeListPageRequest extracts shared limit/offset arguments and returns a normalized paging request.
// @intent keep MCP list-style handlers from repeating the same pagination decode boilerplate.
func decodeListPageRequest(request mcp.CallToolRequest, defaultLimit int) (paging.Request, error) {
	return normalizeListPaging(request.GetInt("limit", defaultLimit), request.GetInt("offset", 0))
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

// pagedListResponse preserves the shared {legacyKey, items, count, pagination} list envelope.
// @intent let analysis handlers reuse one typed pagination DTO while keeping historical alias fields.
type pagedListResponse[T any] struct {
	LegacyKey  string
	Items      []T         `json:"items"`
	Count      int         `json:"count"`
	Pagination paging.Page `json:"pagination"`
}

// MarshalJSON emits both the legacy alias key and the shared paging fields.
// @intent preserve backward-compatible response keys while allowing handlers to work with typed slices.
// @domainRule the temporary map allocation remains because the legacy alias key is dynamic per handler and must coexist with the shared typed envelope.
// @return returns a JSON object containing the legacy alias, items, count, and pagination fields.
func (r pagedListResponse[T]) MarshalJSON() ([]byte, error) {
	resp := map[string]any{
		r.LegacyKey:  r.Items,
		"items":      r.Items,
		"count":      r.Count,
		"pagination": r.Pagination,
	}
	return json.Marshal(resp)
}

// encodePagedListResponse serializes a paged list response with the legacy alias key plus shared items/count/pagination fields.
// @intent keep the {<legacyKey>, items, count, pagination} envelope identical across analysis list handlers while allowing typed DTO slices.
// @param legacyKey is the historical response field name retained for backward compatibility.
func encodePagedListResponse[T any](legacyKey string, items []T, pagination paging.Page) (string, error) {
	return marshalJSON(pagedListResponse[T]{LegacyKey: legacyKey, Items: items, Count: len(items), Pagination: pagination})
}
