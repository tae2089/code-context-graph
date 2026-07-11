# 아키텍처 (Architecture)

[English](../architecture.md)

## 데이터 흐름 (Data Flow)

```
소스 코드 → Tree-sitter 파서 → 노드 + 엣지 + 어노테이션
                                        ↓
                               SQLite / PostgreSQL (GORM)
                                        ↓
                                   FTS 검색
                                        ↓
                          ccg serve          ccg-server
                          stdio MCP       Streamable HTTP
                             ↓             ↓          ↑
                      코딩 에이전트    원격 클라이언트  GitHub / Gitea 웹훅
                                              push → 복제 → 빌드 → DB
```

## 구성 요소 (Components)

### 파서 (Parser) (`internal/parse/treesitter/`)

Tree-sitter 기반 코드 파서입니다. 12개 언어를 지원하며, 각 언어는 함수, 클래스, 타입, 임포트 및 호출 관계를 추출하는 고유한 `LangSpec`을 가집니다.

**지원 언어**: Go, Python, TypeScript, Java, Ruby, JavaScript, C, C++, Rust, Kotlin, PHP, Lua/Luau

> Lua 파서는 Luau(Roblox) 문법도 지원합니다. 타입 어노테이션은 Tree-sitter의 에러 복구를 통해 조용히 무시됩니다. 함수(전역, 로컬, 메서드) 및 주석(한 줄, 블록, `--!strict`)을 추출합니다.

### 저장소 (Store) (`internal/store/gormstore/`)

GORM ORM 기반 저장소입니다. SQLite 및 PostgreSQL과 호환됩니다.

- **Node**: 함수, 클래스, 타입, 파일 등
- **Edge**: 호출, 포함(contains), 테스트 대상(tested_by), 임포트 원천(imports_from) 등
- **SearchDocument**: FTS 검색을 위한 문서들
- **Flow/FlowMembership**: 실행 흐름(flow) 정보

### 검색 (Search) (`internal/store/search/`)

데이터베이스별 전체 텍스트 검색 백엔드:
- **SQLite**: FTS5
- **PostgreSQL**: tsvector + GIN 인덱스

검색 콘텐츠는 camelCase 식별자의 서브토큰을 색인합니다(`getUserById`는 `get`,
`user`, `by`, `id`도 색인) — 내부 단어로도 검색됩니다. 이 콘텐츠는 색인 시점에
생성되므로, 기존 그래프는 재색인된 노드에 대해서만 서브토큰을 갖습니다. 업그레이드
후 `ccg build`로 한 번 전체 재빌드하십시오 — 순수 증분 업데이트만 반복하면 변경된
노드만 갱신되어, 건드리지 않은 노드는 변경 전까지 서브토큰이 없습니다.

전체 빌드 및 명시적인 사후 처리 실행은 네임스페이스 검색 상태를 재생성합니다. 증분 업데이트는 영향을 받는 검색 문서와 FTS 행만 갱신하는 반면, 커뮤니티 사후 처리는 여전히 네임스페이스 전체에 걸쳐 이루어질 수 있습니다. 영구 저장된 흐름(stored-flow)의 재생성은 전체 사후 처리 실행 및 명시적인 `run_postprocess(flows=true)` 호출 시 구현되어 있으며, 진입점별 흐름 쿼리에는 `trace_flow`를 사용하십시오.

### 분석 (Analysis) (`internal/analysis/`)

| 모듈 | 설명 |
|--------|-------------|
| `impact` | BFS 영향 범위 분석 |
| `flows` | 호출 체인 흐름 추적 |
| `changes` | Git diff 리스크 점수 계산 |
| `query` | 그래프 쿼리 (callers, callees, imports) |
| `incremental` | 증분 업데이트 |

### 평가 (Eval) (`internal/eval/`)

골든 코퍼스 기반의 파서 정확도 및 검색 품질 평가 프레임워크입니다.

- **파서 평가**: 12개 언어의 소스 파일을 파싱하고 골든 JSON과 비교하여 노드/엣지의 P/R/F1을 계산합니다.
- **검색 평가**: 쿼리 코퍼스에 대해 P@K, MRR, nDCG 메트릭을 계산합니다.
- **골든 업데이트**: `--update` 모드는 현재 파서 출력을 골든 파일로 저장합니다.
- **코퍼스**: 언어별 소스 + 골든 JSON + queries.json이 포함된 `testdata/eval/` 디렉토리입니다.

### MCP 서버 (MCP Server) (`internal/mcp/`)

MCP 프로토콜을 통해 33개의 도구를 노출합니다. 로컬 `ccg serve` 명령은 이
도구들을 stdio로 노출합니다. 셀프호스트 `ccg-server` 바이너리는 같은 도구
surface를 Streamable HTTP로 노출하고 health/status/webhook 엔드포인트를
추가합니다.

### 런타임 구조 (Runtime Layout)

CCG는 로컬 사용과 셀프호스트 사용을 위한 런타임 진입점을 분리합니다.

| Runtime | 코드 | 책임 |
|---------|------|------|
| `ccg` | `cmd/ccg`, `internal/cli` | 로컬 CLI 명령과 stdio MCP |
| `ccg-server` | `cmd/ccg-server`, `internal/server` | Streamable HTTP MCP, health/status 엔드포인트, 웹훅 동기화 |
| `ccg-core` | `internal/core` | 공용 parser, DB, store, search, migration, incremental sync wiring |

`ccg serve --transport streamable-http`에서 `ccg-server`로 전환하는
마이그레이션 메모와 소유권 경계는 [런타임 구조](runtime-layout.md)를
참조하십시오.

### 안정성 (Reliability)

운영 안정성을 위해 모든 고루틴에 패닉 복구가 적용됩니다:

- **시그널 핸들러**: 패닉 발생 시 에러를 기록하고 `os.Exit(2)`를 호출합니다.
- **HTTP 서버**: 패닉을 에러 채널로 전달하여 정상 종료 흐름을 수행합니다.
- **SyncQueue 워커**: 다른 워커에 영향을 주지 않고 패닉을 기록합니다.
- **SyncQueue 종료**: 종료 중 발생하는 패닉을 격리합니다.

### 웹훅 (Webhook) (`internal/webhook/`)

GitHub/Gitea push 이벤트를 수신하여 자동 복제/빌드 파이프라인을 실행합니다.

- **RepoFilter**: 저장소별 브랜치 필터링 (`IsAllowedRef`)
- **SyncQueue**: 중복 제거 및 동시성이 제어된 워커 큐. 핸들러 실패 시 지수 백오프 재시도(기본 3회, 1s→2s→4s, 최대 30s)
- **CloneOrPull**: go-git 기반 복제/pull (SSH 키 및 앱 토큰 지원)

## 데이터베이스 스키마 (Database Schema)

### 핵심 테이블 (Core Tables)

```
nodes                 — namespace, qualified_name, kind, file_path, start_line, end_line, language 등
edges                 — namespace, from_node_id, to_node_id, kind, file_path, line, fingerprint
search_documents      — namespace, node_id, content, language (FTS 인덱싱됨)
communities           — namespace, key, label, strategy, description
community_memberships — community_id, node_id
flows                 — namespace, name, description
flow_memberships      — namespace, flow_id, node_id, ordinal
```

### 네임스페이스 격리 (Namespace Isolation)

네임스페이스는 컨텍스트에 저장되며 저장소 내부에서 자동으로 추출됩니다. 다중 저장소 환경에서 데이터 격리를 제공합니다.
