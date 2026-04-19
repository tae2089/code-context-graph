# Architecture

## Data Flow

```
Source Code → Tree-sitter Parser → Nodes + Edges + Annotations
                                        ↓
                              SQLite / PostgreSQL (GORM)
                                        ↓
                                   FTS Search
                                        ↓
                              MCP Server (29 tools)
                                    ↓         ↓
                              stdio       Streamable HTTP
                                ↓              ↓
                           Claude Code    Remote Clients
                                               ↑
                                GitHub / Gitea Webhook
                                    push → clone → build → DB
```

## Components

### Parser (`internal/parse/treesitter/`)

Tree-sitter 기반 코드 파서. 12개 언어를 지원하며 각 언어별 `LangSpec`으로 함수, 클래스, 타입, 임포트, 호출 관계를 추출합니다.

**지원 언어**: Go, Python, TypeScript, Java, Ruby, JavaScript, C, C++, Rust, Kotlin, PHP, Lua/Luau

> Lua 파서는 Luau(Roblox) 문법도 지원합니다. 타입 어노테이션은 tree-sitter 에러 복구로 무시되며, 함수(global, local, method), 주석(한줄, 블록, `--!strict`)을 모두 추출합니다.

### Store (`internal/store/gormstore/`)

GORM ORM 기반 저장소. SQLite와 PostgreSQL 호환.

- **Node**: 함수, 클래스, 타입, 파일 등
- **Edge**: calls, contains, tested_by, imports_from 등
- **SearchDocument**: FTS 검색용 문서
- **Flow/FlowMembership**: 실행 흐름

### Search (`internal/store/search/`)

DB별 전문 검색 백엔드:
- **SQLite**: FTS5
- **PostgreSQL**: tsvector + GIN 인덱스

### Analysis (`internal/analysis/`)

| Module | Description |
|--------|-------------|
| `impact` | BFS blast-radius 분석 |
| `flows` | Call-chain flow tracing |
| `deadcode` | 미사용 코드 감지 |
| `community` | Leiden 알고리즘 기반 모듈 커뮤니티 |
| `coupling` | 모듈 간 커플링 분석 |
| `coverage` | 테스트 커버리지 분석 |
| `largefunc` | 대형 함수 감지 |
| `changes` | Git diff 위험도 스코어링 |
| `query` | 그래프 쿼리 (callers, callees, imports) |
| `incremental` | 증분 업데이트 |

### Eval (`internal/eval/`)

Golden corpus 기반 파서 정확도 및 검색 품질 평가 프레임워크.

- **Parser eval**: 12개 언어 소스 파일을 파싱하고 golden JSON과 비교하여 Node/Edge P/R/F1 산출
- **Search eval**: 쿼리 corpus에 대해 P@K, MRR, nDCG 메트릭 산출
- **Golden update**: `--update` 모드로 현재 파서 출력을 golden 파일로 저장
- **Corpus**: `testdata/eval/` 디렉토리에 언어별 소스 + golden JSON + queries.json

### MCP Server (`internal/mcp/`)

29개 도구를 MCP 프로토콜로 노출. stdio와 Streamable HTTP 두 가지 전송 모드 지원.

### Reliability

운영 안정성을 위해 모든 goroutine에 panic recovery가 적용되어 있습니다:

- **Signal handler**: panic 시 에러 로깅 후 `os.Exit(2)`
- **HTTP server**: panic을 에러 채널로 전파하여 정상 종료 흐름 유지
- **SyncQueue worker**: panic 로깅 후 다른 워커에 영향 없이 계속 동작
- **SyncQueue shutdown**: shutdown 과정의 panic 격리

### Webhook (`internal/webhook/`)

GitHub/Gitea push 이벤트 수신 → 자동 clone/build 파이프라인.

- **RepoFilter**: 레포별 브랜치 필터링 (`IsAllowedRef`)
- **SyncQueue**: 중복 제거 + 동시성 제어 워커 큐. 핸들러 실패 시 exponential backoff retry (기본 3회, 1s→2s→4s, 최대 30s)
- **CloneOrPull**: go-git 기반 clone/pull (SSH key, app token 지원)

## Database Schema

### Core Tables

```
nodes        — qualified_name, kind, file_path, start_line, end_line, language, ...
edges        — source_id, target_id, kind, ...
search_docs  — node_id, content (FTS indexed)
flows        — name, entry_point, criticality
flow_members — flow_id, node_id, position
```

### Namespace Isolation

Context에 namespace를 넣어 store 내부에서 자동 추출. 멀티 레포 환경에서 데이터 격리.
