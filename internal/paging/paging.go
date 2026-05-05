// @index 공통 pagination 타입과 limit/offset 정규화 규칙을 제공한다.
package paging

import "fmt"

const (
	DefaultLimit = 50
	MaxLimit     = 500
)

// Request describes a limit/offset pagination request.
// @intent carry bounded pagination inputs between handlers and services.
type Request struct {
	Limit  int
	Offset int
}

// Page describes one bounded result window.
// @intent expose stable pagination metadata to MCP clients and prompts.
type Page struct {
	Limit      int  `json:"limit"`
	Offset     int  `json:"offset"`
	Returned   int  `json:"returned"`
	HasMore    bool `json:"has_more"`
	NextOffset *int `json:"next_offset,omitempty"`
}

// Normalize applies defaulting and validates a pagination request.
// @intent centralize bounded pagination validation so handlers and services share one rule set.
func Normalize(req Request) (Request, error) {
	return NormalizeWithDefault(req, DefaultLimit)
}

// NormalizeWithDefault applies validation using a caller-provided default limit.
// @intent support tool-specific defaults while preserving the shared max-limit and offset rules.
func NormalizeWithDefault(req Request, defaultLimit int) (Request, error) {
	if defaultLimit <= 0 {
		defaultLimit = DefaultLimit
	}
	if req.Limit <= 0 {
		req.Limit = defaultLimit
	}
	if req.Limit > MaxLimit {
		return Request{}, fmt.Errorf("limit must be <= %d, got %d", MaxLimit, req.Limit)
	}
	if req.Offset < 0 {
		return Request{}, fmt.Errorf("offset must be >= 0, got %d", req.Offset)
	}
	return req, nil
}

// BuildPage creates pagination metadata for one returned page.
// @intent generate one consistent pagination envelope after services compute has_more.
func BuildPage(req Request, returned int, hasMore bool) Page {
	page := Page{
		Limit:    req.Limit,
		Offset:   req.Offset,
		Returned: returned,
		HasMore:  hasMore,
	}
	if hasMore {
		nextOffset := req.Offset + req.Limit
		page.NextOffset = &nextOffset
	}
	return page
}
