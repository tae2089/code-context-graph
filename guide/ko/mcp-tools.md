# MCP 도구 (MCP Tools)

[English](../mcp-tools.md)

code-context-graph는 33개의 MCP 도구를 제공합니다. `.mcp.json`을 설정하면 Claude Code에서 자동으로 연결됩니다.

## 설정 (Setup)

### stdio (로컬)

```json
{
  "mcpServers": {
    "ccg": {
      "command": "ccg",
      "args": ["serve", "--db-driver", "sqlite", "--db-dsn", "ccg.db"]
    }
  }
}
```

### Streamable HTTP (원격)

```json
{
  "mcpServers": {
    "ccg": {
      "type": "streamable-http",
      "url": "http://your-server:8080/mcp"
    }
  }
}
```

## 도구 (33개)

### 핵심 (Core)

| 도구 | 설명 |
|------|-------------|
| `parse_project` | 소스 파일 파싱 |
| `build_or_update_graph` | 사후 처리를 포함한 전체/증분 빌드 |
| `run_postprocess` | 저장된 흐름(flow), 커뮤니티 및 전체 텍스트 검색 파생 상태 재생성 |
| `get_postprocess_policy` | 자동 사후 처리 정책 상태 및 최근 실패 내역 확인 |
| `reset_postprocess_policy` | 특정 도구에 대한 fail-closed 연속 기록을 지우기 위한 리셋 마커 기록 |
| `get_node` | 정규화된 이름으로 노드 조회 |
| `search` | 전체 텍스트 검색 |
| `query_graph` | 정의된 그래프 쿼리(callers, callees, imports 등) |
| `list_graph_stats` | 노드/엣지/파일 수 확인 |
| `get_minimal_context` | AI 에이전트 진입점을 위한 경량 요약(~100 토큰) — 그래프 통계, 리스크, 주요 커뮤니티/흐름, 도구 추천 포함 |

`build_or_update_graph`와 `run_postprocess`는 모두 자동 사후 처리 실패 정책(postprocess failure policy)을 지원합니다. 명시적인 `postprocess_policy`가 제공되지 않으면 CCG는 기본적으로 `degraded` 모드를 사용하며, 동일한 `(namespace, tool)` 쌍에 대해 3회 연속 `degraded` 실행 시 자동으로 `fail_closed`로 격상합니다.

상태 표, 실패 원인, 건너뛰기 동작 및 `build_or_update_graph`와 `run_postprocess`에 대한 정책 격상 규칙 등 자세한 내용은 [사후 처리 실패 정책](postprocess-failure-policy.md)을 참조하십시오.

CCG는 아직 Prometheus `/metrics` 엔드포인트를 제공하지 않습니다. 사후 처리 작업에 대해서는 현재 기계가 읽을 수 있는 운영 인터페이스인 `get_postprocess_policy`와 HTTP `/status` 요약을 사용하십시오.

### 분석 (Analysis)

| 도구 | 설명 |
|------|-------------|
| `get_impact_radius` | BFS 영향 범위(blast-radius) 분석 |
| `trace_flow` | 호출 체인 흐름 추적 |
| `find_large_functions` | 라인 제한을 초과하는 함수 찾기; `limit` 지원 |
| `find_dead_code` | 사용되지 않는 코드 감지 |
| `detect_changes` | Git diff 리스크 점수 계산 |
| `get_affected_flows` | 변경 사항의 영향을 받는 흐름 확인 |
| `list_flows` | `limit` / `offset` 페이지네이션으로 추적된 흐름 목록 출력 |

### 커뮤니티 및 아키텍처 (Community & Architecture)

| 도구 | 설명 |
|------|-------------|
| `list_communities` | `limit` / `offset` 페이지네이션으로 모듈 커뮤니티 목록 출력 |
| `get_community` | 커뮤니티 상세 정보 및 커버리지 확인; 멤버 목록은 `member_limit` / `member_offset` 지원 |
| `get_architecture_overview` | 커뮤니티와 결합도(coupling)를 각각 페이지네이션하는 아키텍처 요약 |

### 페이지네이션 및 응답 예산

네임스페이스에 흐름, 커뮤니티, 멤버, 결합도 쌍이 많을 수 있으면
페이지네이션 가능한 그래프 도구를 사용하십시오. 페이지네이션 응답에는
`has_more`가 포함되며, 값이 `true`이면 같은 도구를 `next_offset`으로 다시
호출하십시오.

| 도구 | 페이지네이션 파라미터 | 참고 |
|------|-----------------------|-------|
| `query_graph` | `limit`, `offset` | `limit` 최대값은 500 |
| `list_flows` | `limit`, `offset` | 응답에 `pagination` 포함 |
| `list_communities` | `limit`, `offset` | 응답에 `pagination` 포함 |
| `get_community` | `member_limit`, `member_offset` | `include_members=true`일 때 적용되며 응답에 `members_pagination` 포함 |
| `get_architecture_overview` | `community_limit`, `community_offset`, `coupling_limit`, `coupling_offset` | 커뮤니티와 결합도에 별도 페이지네이션 객체 포함 |

일부 분석 도구는 아직 내부적으로 전체 결과를 조회합니다. 큰 네임스페이스에서는
`find_dead_code`, `find_suspect_fallback_edges`, 또는 광범위한 MCP prompt를
호출하기 전에 입력 범위를 좁히십시오. `find_large_functions`는 `limit`을
받지만 현재는 라인 기준 쿼리를 수행한 뒤 응답을 자릅니다.

### 어노테이션 및 문서화 (Annotation & Documentation)

| 도구 | 설명 |
|------|-------------|
| `get_annotation` | 어노테이션 및 문서 태그 확인 |
| `build_rag_index` | 문서 및 커뮤니티로부터 RAG 인덱스 빌드 (네임스페이스 지원) |
| `get_rag_tree` | RAG 문서 트리 탐색 (네임스페이스 지원) |
| `get_doc_content` | 문서 파일 내용 확인 (네임스페이스 지원) |
| `search_docs` | 키워드로 RAG 문서 트리 검색 (네임스페이스 지원) |

### 네임스페이스 파일 관리 (Namespace File Management)

격리 단위의 정식 용어는 `namespace`입니다. `workspace` 파라미터와
`list_workspaces` / `delete_workspace` 도구는 기존 호출자를 위한 사용
중단 별칭으로만 유지됩니다.

| 도구 | 설명 |
|------|-------------|
| `upload_file` | 네임스페이스에 파일 업로드 (base64) |
| `upload_files` | 단일 호출로 여러 네임스페이스에 다수 파일 업로드 |
| `list_namespaces` | 모든 네임스페이스 목록 출력 |
| `list_workspaces` | `list_namespaces`에 대한 사용 중단된 별칭 |
| `list_files` | 네임스페이스 내 파일 목록 출력 |
| `delete_file` | 네임스페이스에서 파일 삭제 |
| `delete_namespace` | 네임스페이스 전체 및 관련 파일 모두 삭제 |
| `delete_workspace` | `delete_namespace`에 대한 사용 중단된 별칭 |

정식 예시:

```
upload_file(namespace: "payment-svc", file_path: "handler.go", content: "<base64>")
list_files(namespace: "payment-svc")
delete_namespace(namespace: "payment-svc")
```

## Claude Code Skills (5개)

| 스킬 | 설명 |
|-------|-------------|
| `/ccg` | 핵심 빌드 및 검색 — 파싱, 그래프 빌드, 쿼리, 검색 |
| `/ccg-analyze` | 코드 분석 — 영향 범위, 흐름 추적, 데드 코드, 아키텍처 |
| `/ccg-annotate` | 어노테이션 시스템 — AI 기반 어노테이션 워크플로우, 태그 레퍼런스 |
| `/ccg-docs` | 문서화 — 문서 생성, RAG 인덱싱, 린트 |
| `/ccg-workspace` | 네임스페이스 파일 관리 — 네임스페이스 파일 업로드, 목록 출력, 삭제 (`workspace`는 사용 중단된 파라미터 별칭) |

### 사용법

```
/ccg build .                     — 코드 그래프 빌드
/ccg status                      — 그래프 통계 및 사후 처리 오류 요약 확인
/ccg search "query"              — 전체 텍스트 검색
/ccg-docs docs                   — 문서 생성
/ccg-docs lint                   — 문서 상태 및 어노테이션 커버리지 체크
/ccg languages                   — 지원 언어 목록 출력
/ccg-annotate annotate internal/ — AI 기반 어노테이션 생성
/ccg-workspace                   — 네임스페이스 파일 및 디렉토리 관리
```
