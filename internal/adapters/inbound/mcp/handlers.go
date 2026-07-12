// @index Shared handler helpers and response utilities for MCP tools.
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/tae2089/trace"

	requestctx "github.com/tae2089/code-context-graph/internal/ctx"
	"github.com/tae2089/code-context-graph/internal/domain/graph"
	"github.com/tae2089/code-context-graph/internal/obs"
)

// handlers groups shared dependencies for MCP tool handlers.
// @intent group shared dependencies so individual MCP tool handlers can reuse the same services and cache.
type handlers struct {
	deps  *Deps
	cache *Cache
}

const maxPaginationLimit = 500

// pagination is the MCP wire metadata for one limit/offset result window.
// @intent keep pagination fields at the MCP boundary without exposing a shared internal paging contract.
type pagination struct {
	Limit      int  `json:"limit"`
	Offset     int  `json:"offset"`
	Returned   int  `json:"returned"`
	HasMore    bool `json:"has_more"`
	NextOffset *int `json:"next_offset,omitempty"`
}

// marshalJSON encodes a value into a JSON string.
// @intent serialize handler payloads into a stable JSON string for MCP responses and cache keys.
// @param v is the response payload or cache-key input value to encode.
// @return returns the serialized JSON string.
func marshalJSON(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// makeCacheKey builds a cache key from a prefix and JSON-encoded parameters.
// @intent turn request parameters into a stable string key so tool-result caching can reuse previous responses.
// @param prefix scopes the cache namespace for a tool family.
// @param v is the parameter set to embed into the cache key.
// @return returns the cache key built from prefix and serialized parameters.
// @see mcp.handlers.cachedExecute
func makeCacheKey(prefix string, v any) (string, error) {
	key, err := marshalJSON(v)
	if err != nil {
		return "", err
	}
	return prefix + key, nil
}

// logger returns the configured logger or the default logger.
// @intent give handlers a consistent logging interface without repeating nil checks.
// @return returns deps.Runtime.Logger when configured, otherwise the default slog logger.
func (h *handlers) logger() *slog.Logger {
	if h.deps.Runtime.Logger != nil {
		return h.deps.Runtime.Logger
	}
	return slog.Default()
}

// @intent attach the requested namespace to context before downstream stores and analyzers run.
// @ensures returns a context carrying the normalized namespace that this request should use.
func (h *handlers) applyNamespace(ctx context.Context, request mcp.CallToolRequest) context.Context {
	return requestctx.WithNamespace(ctx, resolveNamespace(ctx, requestNamespace(request)))
}

// @intent prefer an explicit request namespace while falling back to the namespace already carried on context.
// @domainRule an explicit request namespace always overrides the namespace already on context.
func resolveNamespace(ctx context.Context, namespace string) string {
	if namespace != "" {
		return requestctx.Normalize(namespace)
	}
	return requestctx.FromContext(ctx)
}

// @intent read the canonical namespace isolation argument.
func requestNamespace(request mcp.CallToolRequest) string {
	return request.GetString("namespace", "")
}

// toolResultErr carries an MCP tool result alongside an error value.
// @intent preserve the MCP error response that should be returned to the user inside normal Go error flow.
// @see mcp.unwrapToolResultErr
type toolResultErr struct {
	message string
	result  *mcp.CallToolResult
}

// Error returns the wrapped message for the tool result error.
// @intent satisfy the standard error interface for toolResultErr.
func (e *toolResultErr) Error() string {
	return e.message
}

// newToolResultErr creates an error carrying an MCP error tool result.
// @intent propagate tool failures upward together with the MCP error response that should be shown to callers.
// @param message is the error string exposed directly to the user.
// @return returns an error backed by toolResultErr.
func newToolResultErr(message string) error {
	return &toolResultErr{
		message: message,
		result:  mcp.NewToolResultError(message),
	}
}

// missingParamResult converts a missing-parameter error into an MCP result.
// @intent convert missing required parameters into one consistent user-input error response.
// @param err is the original error describing which required parameter is missing.
func missingParamResult(err error) (*mcp.CallToolResult, error) {
	return mcp.NewToolResultError(fmt.Sprintf("missing parameter: %v", err)), nil
}

// nodeNotFoundErr creates a standardized node-not-found tool error.
// @intent reuse one consistent node-not-found message across handlers.
// @param qn is the qualified node name that could not be resolved.
func nodeNotFoundErr(qn string) error {
	return newToolResultErr(fmt.Sprintf("node %q not found", qn))
}

// @intent reject zero and negative list limits before handlers hit database queries.
func validatePositiveLimit(limit int) error {
	if limit <= 0 {
		return newToolResultErr(fmt.Sprintf("limit must be > 0, got %d", limit))
	}
	return nil
}

// validateOffset validates non-negative pagination offsets.
// @intent reject negative offsets before handlers hit database queries.
func validateOffset(offset int) error {
	if offset < 0 {
		return newToolResultErr(fmt.Sprintf("offset must be >= 0, got %d", offset))
	}
	return nil
}

// unwrapToolResultErr extracts an embedded MCP tool result from an error.
// @intent recover user-facing MCP tool results from the internal error flow at one shared exit point.
// @return returns the embedded MCP result and true when err is a toolResultErr.
func unwrapToolResultErr(err error) (*mcp.CallToolResult, bool) {
	if err == nil {
		return nil, false
	}
	toolErr, ok := err.(*toolResultErr)
	if !ok {
		return nil, false
	}
	return toolErr.result, true
}

// finalizeToolResult converts a string result or toolResultErr into an MCP response.
// @intent normalize success strings and user-facing tool errors at one common handler exit path.
// @param result is the successful text payload to return.
// @return returns a text result on success or the embedded MCP error result for toolResultErr.
func finalizeToolResult(result string, err error) (*mcp.CallToolResult, error) {
	if err != nil {
		if toolResult, ok := unwrapToolResultErr(err); ok {
			return toolResult, nil
		}
		return nil, err
	}
	return mcp.NewToolResultText(result), nil
}

// cachedExecute extracts the cache lookup → execute → cache store flow.
// It runs fn directly when cache is nil.
// @intent centralize caching for read-oriented tool responses so repeated DB and analyzer work can be skipped.
// @param prefix is the tool-specific cache namespace prefix.
// @param params are the request parameters embedded in the cache key.
// @sideEffect may read and write the in-memory cache and emit debug logs.
// @domainRule namespace values are normalized before key generation.
// @mutates h.cache
func (h *handlers) cachedExecute(ctx context.Context, prefix string, params map[string]any, fn func() (string, error)) (string, error) {
	if h.cache == nil {
		return fn()
	}
	cacheParams := make(map[string]any, len(params))
	for k, v := range params {
		cacheParams[k] = v
	}
	if namespace, ok := cacheParams["namespace"].(string); ok {
		cacheParams["namespace"] = resolveNamespace(ctx, namespace)
	}

	key, err := makeCacheKey(prefix, cacheParams)
	if err != nil {
		h.logger().WarnContext(ctx, "failed to marshal cache key", append(obs.TraceLogArgs(ctx), "prefix", prefix, trace.SlogError(err))...)
		return fn()
	}
	if key != "" {
		if cached, ok := h.cache.Get(key); ok {
			h.logger().DebugContext(ctx, "cache hit", append(obs.TraceLogArgs(ctx), "prefix", prefix)...)
			return cached, nil
		}
	}

	result, err := fn()
	if err != nil {
		return "", err
	}
	if key != "" {
		h.cache.Set(key, result)
	}
	return result, nil
}

// nodeSummary is a compact node response payload shared by graph handlers.
// @intent reuse one typed node representation across multiple tool responses.
type nodeSummary struct {
	ID            uint           `json:"id"`
	QualifiedName string         `json:"qualified_name"`
	Kind          graph.NodeKind `json:"kind"`
	Name          string         `json:"name"`
	FilePath      string         `json:"file_path"`
}

// nodeToSummary converts a graph node into a compact typed response payload.
// @intent reuse one typed node representation across multiple tool responses.
// @param n is the graph node to include in an MCP response.
// @return returns a typed node summary containing identifier, name, kind, and file path fields.
func nodeToSummary(n graph.Node) nodeSummary {
	return nodeSummary{
		ID:            n.ID,
		QualifiedName: n.QualifiedName,
		Kind:          n.Kind,
		Name:          n.Name,
		FilePath:      n.FilePath,
	}
}
