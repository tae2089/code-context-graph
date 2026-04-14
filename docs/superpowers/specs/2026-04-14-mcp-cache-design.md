# MCP Serve Hot Cache Design

## Goal

`ccg serve` 세션 내 반복 MCP 툴 호출의 DB 쿼리를 인메모리 TTL 캐시로 제거하여 응답 속도를 개선한다.

## Context

`ccg serve`는 stdio 기반 MCP 서버 프로세스를 실행한다. Claude Code나 IDE가 이 프로세스에 연결하여 `get_node`, `search` 등의 툴을 반복 호출하는 패턴이 일반적이다. 현재 모든 호출이 SQLite DB 쿼리를 실행하므로, 동일한 인자로 반복 호출 시 불필요한 I/O가 발생한다.

캐시는 프로세스 수명(세션) 동안만 유지된다. 프로세스 재시작 시 cold start. 이는 의도된 동작으로, stale 데이터 문제를 피할 수 있다.

## Architecture

```
ccg serve
  └── NewServer(deps + cache)
        └── handlers{deps, cache}
              ├── 15개 read-only 툴 → cache.Get() → miss → DB → cache.Set()
              └── 3개 write 툴    → 캐시 없이 직접 DB (parse_project, build_or_update_graph, run_postprocess)
```

## Cache 구조 (`internal/mcp/cache.go`)

```go
type Cache struct {
    mu      sync.RWMutex
    entries map[string]entry
    ttl     time.Duration
}

type entry struct {
    value     string
    expiresAt time.Time
}

func NewCache(ttl time.Duration) *Cache
func (c *Cache) Get(key string) (string, bool)
func (c *Cache) Set(key string, value string)
func (c *Cache) Invalidate(key string)
func (c *Cache) cleanup()  // 내부 goroutine, 30s 주기
```

**캐시 키 형식:** `"tool_name:{json_args}"`

예: `get_node:{"qualified_name":"internal/mcp/server.go::NewServer"}`

args가 없는 툴 (예: `list_graph_stats`, `get_architecture_overview`)은 `"tool_name:{}"` 고정 키 사용.

## Handler 통합 (`internal/mcp/handlers.go`)

`handlers` struct에 `cache *Cache` 필드 추가:

```go
type handlers struct {
    deps  *Deps
    cache *Cache  // nil이면 캐시 비활성화
}
```

캐시 적용 패턴 (read-only 툴):

```go
func (h *handlers) getNode(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
    qn, err := request.RequireString("qualified_name")
    if err != nil { ... }

    key := "get_node:" + mustJSON(map[string]any{"qualified_name": qn})
    if h.cache != nil {
        if cached, ok := h.cache.Get(key); ok {
            return mcp.NewToolResultText(cached), nil
        }
    }

    // DB 쿼리 (기존 로직)
    node, err := h.deps.Store.GetNode(ctx, qn)
    ...

    result := string(b)
    if h.cache != nil {
        h.cache.Set(key, result)
    }
    return mcp.NewToolResultText(result), nil
}
```

`mustJSON`: `json.Marshal` 래퍼, 패닉 없이 항상 유효한 JSON 반환.

## 캐시 대상 툴 (15개 read-only)

| 툴 | 캐시 키 인자 |
|----|-------------|
| `get_node` | qualified_name |
| `get_impact_radius` | qualified_name, depth |
| `search` | query, limit, path |
| `get_annotation` | qualified_name |
| `trace_flow` | qualified_name |
| `query_graph` | pattern, target |
| `list_graph_stats` | (없음) |
| `find_large_functions` | min_lines, limit, path |
| `detect_changes` | repo_root, base |
| `get_affected_flows` | repo_root, base |
| `list_flows` | sort_by, limit |
| `list_communities` | sort_by, min_size |
| `get_community` | community_id, include_members |
| `get_architecture_overview` | (없음) |
| `find_dead_code` | path |

## 캐시 제외 툴 (3개 write)

- `parse_project` — DB에 nodes/edges upsert
- `build_or_update_graph` — 전체 그래프 rebuild/sync
- `run_postprocess` — flows, communities, FTS 재구성

## CLI 플래그 (`internal/cli/serve.go`)

```go
type ServeConfig struct {
    CacheTTL time.Duration  // default: 5m
    NoCache  bool
}
```

플래그:
- `--cache-ttl duration` (기본값: `5m`)
- `--no-cache` (캐시 비활성화)

`--no-cache` 또는 `--cache-ttl 0`이면 `handlers.cache = nil` → 캐시 패스스루 없음.

`NewServer(deps)` → `NewServerWithCache(deps, cache *Cache)` 시그니처 변경 또는 `Deps`에 `Cache` 필드 추가. **결정: `Deps`에 `Cache *Cache` 필드 추가** — 기존 `NewServer` 시그니처 유지, 하위 호환성 보장.

## 파일 구조

| 파일 | 변경 |
|------|------|
| `internal/mcp/cache.go` | 신규 |
| `internal/mcp/cache_test.go` | 신규 |
| `internal/mcp/handlers.go` | `handlers` struct에 `cache` 필드, 15개 툴에 캐시 패턴 |
| `internal/mcp/handlers_test.go` | 캐시 히트/미스 테스트 추가 |
| `internal/mcp/server.go` | `Deps`에 `Cache *Cache` 추가, `handlers` 초기화 시 전달 |
| `internal/cli/serve.go` | `ServeConfig`에 `CacheTTL`, `NoCache` 추가, 플래그 등록 |

`serve.go`에서 `ServeFunc`를 통해 `Cache` 객체를 생성하여 `Deps`에 주입하는 책임은 `main.go` 또는 `cmd` 패키지에 있다.

## 테스트 전략

### `cache_test.go`
- `TestCache_GetMiss`: 빈 캐시에서 Get → false
- `TestCache_SetGet`: Set 후 Get → 동일 값 반환
- `TestCache_TTLExpiry`: 짧은 TTL(50ms)로 Set → sleep → Get → false
- `TestCache_Concurrent`: goroutine 10개 동시 Set/Get → `go test -race` 통과
- `TestCache_NilSafe`: cache가 nil인 경우 핸들러 패닉 없음 (통합 수준에서 검증)

### `handlers_test.go` 추가
- `TestGetNode_CacheHit`: 동일 인자로 두 번 호출 → 두 번째는 Store.GetNode 미호출 (mock store 사용)
- `TestGetNode_NoCache`: `handlers.cache = nil` → 항상 Store.GetNode 호출

## 미구현 (추후)

- **캐시 무효화 훅**: `build_or_update_graph` 완료 시 전체 캐시 flush
- **캐시 통계**: `list_graph_stats`에 hit/miss 수 포함
- **분산 캐시**: 멀티 프로세스 환경 (현재 단일 프로세스 전제)
