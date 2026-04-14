package mcp

import (
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/tae2089/trace"

	"github.com/imtaebin/code-context-graph/internal/model"
)

type handlers struct {
	deps  *Deps
	cache *Cache
}

func marshalJSON(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func makeCacheKey(prefix string, v any) (string, error) {
	key, err := marshalJSON(v)
	if err != nil {
		return "", err
	}
	return prefix + key, nil
}

func (h *handlers) logger() *slog.Logger {
	if h.deps.Logger != nil {
		return h.deps.Logger
	}
	return slog.Default()
}

type toolResultErr struct {
	message string
	result  *mcp.CallToolResult
}

func (e *toolResultErr) Error() string {
	return e.message
}

func newToolResultErr(message string) error {
	return &toolResultErr{
		message: message,
		result:  mcp.NewToolResultError(message),
	}
}

func missingParamResult(err error) (*mcp.CallToolResult, error) {
	return mcp.NewToolResultError(fmt.Sprintf("missing parameter: %v", err)), nil
}

func nodeNotFoundErr(qn string) error {
	return newToolResultErr(fmt.Sprintf("node %q not found", qn))
}

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

func finalizeToolResult(result string, err error) (*mcp.CallToolResult, error) {
	if err != nil {
		if toolResult, ok := unwrapToolResultErr(err); ok {
			return toolResult, nil
		}
		return nil, err
	}
	return mcp.NewToolResultText(result), nil
}

// cachedExecute는 캐시 조회 → 실행 → 캐시 저장 패턴을 추출한 헬퍼이다.
// cache가 nil이면 fn을 직접 실행한다.
func (h *handlers) cachedExecute(prefix string, params map[string]any, fn func() (string, error)) (string, error) {
	if h.cache == nil {
		return fn()
	}

	key, err := makeCacheKey(prefix, params)
	if err != nil {
		h.logger().Warn("failed to marshal cache key", "prefix", prefix, trace.SlogError(err))
		return fn()
	}
	if key != "" {
		if cached, ok := h.cache.Get(key); ok {
			h.logger().Debug("cache hit", "prefix", prefix)
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

func nodeToBasicMap(n model.Node) map[string]any {
	return map[string]any{
		"id":             n.ID,
		"qualified_name": n.QualifiedName,
		"kind":           n.Kind,
		"name":           n.Name,
		"file_path":      n.FilePath,
	}
}
