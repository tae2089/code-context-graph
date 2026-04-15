package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/tae2089/trace"

	"github.com/imtaebin/code-context-graph/internal/ctxns"
	"github.com/imtaebin/code-context-graph/internal/model"
)

// handlers groups shared dependencies for MCP tool handlers.
// @intent 개별 MCP 도구 핸들러가 공통 의존성과 캐시를 재사용하도록 묶는다.
type handlers struct {
	deps  *Deps
	cache *Cache
}

// marshalJSON encodes a value into a JSON string.
// @intent MCP 응답과 캐시 키 생성을 위해 값을 일관된 JSON 문자열로 직렬화한다.
// @param v JSON으로 직렬화할 응답 데이터 또는 캐시 키 입력값이다.
// @return 직렬화된 JSON 문자열을 반환한다.
func marshalJSON(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// makeCacheKey builds a cache key from a prefix and JSON-encoded parameters.
// @intent 요청 매개변수를 안정적인 문자열 키로 바꿔 도구 결과 캐시를 조회 가능하게 한다.
// @param prefix 도구별 캐시 네임스페이스를 구분한다.
// @param v 캐시 키에 포함할 요청 파라미터 집합이다.
// @return prefix와 직렬화된 파라미터를 결합한 캐시 키를 반환한다.
// @see mcp.handlers.cachedExecute
func makeCacheKey(prefix string, v any) (string, error) {
	key, err := marshalJSON(v)
	if err != nil {
		return "", err
	}
	return prefix + key, nil
}

// logger returns the configured logger or the default logger.
// @intent 핸들러가 nil 검사 없이 일관된 로깅 인터페이스를 사용하게 한다.
// @return deps.Logger가 없으면 기본 slog 로거를 반환한다.
func (h *handlers) logger() *slog.Logger {
	if h.deps.Logger != nil {
		return h.deps.Logger
	}
	return slog.Default()
}

func (h *handlers) applyWorkspace(ctx context.Context, request mcp.CallToolRequest) context.Context {
	if ws := request.GetString("workspace", ""); ws != "" {
		return ctxns.WithNamespace(ctx, ws)
	}
	return ctx
}

// toolResultErr carries an MCP tool result alongside an error value.
// @intent 일반 에러 흐름 안에서 사용자에게 반환할 MCP 오류 응답을 보존한다.
// @see mcp.unwrapToolResultErr
type toolResultErr struct {
	message string
	result  *mcp.CallToolResult
}

// Error returns the wrapped message for the tool result error.
// @intent toolResultErr가 표준 error 인터페이스를 만족하도록 한다.
func (e *toolResultErr) Error() string {
	return e.message
}

// newToolResultErr creates an error carrying an MCP error tool result.
// @intent 도구 오류를 MCP 응답과 함께 상위 공통 처리 로직으로 전달한다.
// @param message 사용자에게 그대로 노출할 오류 메시지다.
// @return toolResultErr 형태의 error를 반환한다.
func newToolResultErr(message string) error {
	return &toolResultErr{
		message: message,
		result:  mcp.NewToolResultError(message),
	}
}

// missingParamResult converts a missing-parameter error into an MCP result.
// @intent 필수 파라미터 누락을 일관된 사용자 입력 오류 응답으로 변환한다.
// @param err 누락된 파라미터 정보를 담은 원본 오류다.
func missingParamResult(err error) (*mcp.CallToolResult, error) {
	return mcp.NewToolResultError(fmt.Sprintf("missing parameter: %v", err)), nil
}

// nodeNotFoundErr creates a standardized node-not-found tool error.
// @intent 노드 조회 실패 메시지를 핸들러 전반에서 일관되게 재사용한다.
// @param qn 조회에 실패한 정규화 노드 이름이다.
func nodeNotFoundErr(qn string) error {
	return newToolResultErr(fmt.Sprintf("node %q not found", qn))
}

// unwrapToolResultErr extracts an embedded MCP tool result from an error.
// @intent 내부 에러 흐름에 실린 사용자용 MCP 결과를 공통 종료 지점에서 복원한다.
// @return toolResultErr인 경우 포함된 MCP 결과와 true를 반환한다.
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
// @intent 핸들러가 공통 종료 경로에서 성공 문자열과 사용자용 오류 응답을 정규화한다.
// @param result 성공 시 반환할 텍스트 응답이다.
// @return 성공 시 텍스트 결과를, toolResultErr면 해당 오류 결과를 반환한다.
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
// @intent 읽기 전용 도구 응답의 캐시 처리 흐름을 공통화해 중복 DB 조회를 줄인다.
// @param prefix 도구별 캐시 네임스페이스 접두사다.
// @param params 캐시 키에 포함할 요청 파라미터다.
// @sideEffect 캐시 조회/저장과 디버그 로그 기록을 수행할 수 있다.
// @mutates h.cache
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

// nodeToBasicMap converts a graph node into a compact response payload.
// @intent 여러 도구 응답에서 공통으로 쓰는 최소 노드 표현을 재사용한다.
// @param n MCP 응답에 포함할 그래프 노드다.
// @return 식별자, 이름, 종류, 파일 경로를 담은 맵을 반환한다.
func nodeToBasicMap(n model.Node) map[string]any {
	return map[string]any{
		"id":             n.ID,
		"qualified_name": n.QualifiedName,
		"kind":           n.Kind,
		"name":           n.Name,
		"file_path":      n.FilePath,
	}
}
