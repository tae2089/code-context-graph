# MCP 도구 (MCP Tools)

[English](../mcp-tools.md)

code-context-graph는 33개의 MCP 도구를 제공합니다. `.mcp.json`을 설정하면
Codex 또는 Claude Code 같은 MCP 지원 코딩 에이전트가 연결할 수 있습니다.

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
| `find_suspect_fallback_edges` | 의심스러운 fallback 호출 엣지 품질 리포트, 페이지네이션 지원 |
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
| `get_rag_tree` | node ID 기반 RAG 문서 트리 탐색 (네임스페이스 지원) |
| `get_doc_content` | 문서 파일 내용 확인 (네임스페이스 지원) |
| `search_docs` | 키워드로 RAG 문서 트리 검색 (네임스페이스 지원) |
| `retrieve_docs` | DB-backed graph evidence에서 관련 문서를 찾고, doc-index fallback, evidence, 제한된 Markdown 본문 반환 |

자연어 기반 코드 이해에는 문서/RAG 도구를 먼저 사용하십시오.
`retrieve_docs`는 file-level graph evidence를 점수화하므로 여러 키워드가
관련 심볼에 나뉘어 있어도 같은 문서를 후보로 찾고, 제한된 Markdown 본문과
evidence를 반환합니다. DB retrieval을 사용할 수 없으면 생성된
`doc-index.json` snapshot으로 fallback합니다. `get_rag_tree`는 주변 모듈 구조를 펼칩니다. 먼저
인자 없이 호출해 tree를 받고, 반환된 `node_id`를 넘겨 `community`,
`package`, `file`, `symbol` 노드로 내려갑니다. 기존 `community_id`
파라미터는 `node_id`의 호환 alias로 유지됩니다. `get_doc_content`는
특정 생성 Markdown 파일을 직접 읽습니다. 이후 정확한
심볼, edge, flow, 영향 범위가 필요할 때 `get_node`, `query_graph`,
`trace_flow`, `get_impact_radius` 같은 graph 도구로 내려가십시오.
`search_docs` 또는 MCP `search`는 넓은 아키텍처 질문이나 "어떻게
동작하나?" 류의 기본 표면이 아니라, 어노테이션/키워드 기반 후보 검색에
사용하는 것을 권장합니다.

`retrieve_docs`의 score는 같은 query 결과 안에서 순위를 정하기 위한
신호이며, 절대적인 품질 점수가 아닙니다. 서로 다른 query의 score를 직접
비교하는 용도로는 사용하지 마십시오. 현재 DB-backed 점수는 정확한 심볼/파일
이름과 high-signal annotation bucket을 우선합니다. exact label, label
contains, `qualified_name`, `@intent`, `@index`, `@domainRule`, `@requires`,
`@ensures`, `@sideEffect`, `@mutates`, `@see`, generic annotation text가
가중치에 반영되고, 매칭된 고유 query term마다 ranking bonus가 추가됩니다.
`matched_fields`, `matched_terms`, `matches`가 어떤 field, term, graph node가
근거로 사용됐는지 보여줍니다.

RAG 인덱스 품질은 생성 문서와 비어 있지 않은 community postprocess 결과에
의존합니다. CLI `ccg docs` 명령은 community를 갱신하고 기본
`doc-index.json` 호환 snapshot을 자동으로 기록합니다. 또한 브라우저 Wiki를
위한 별도 `wiki-index.json` 호환 snapshot도 기록하며, MCP retrieval 도구는
DB를 우선 사용하고 snapshot은 fallback surface로만 사용합니다. MCP만 사용하는
워크플로우에서 community가 없을 수 있으면 `build_rag_index` 전에
`run_postprocess`를 `communities=true`, `flows=false`, `fts=false`로
호출하십시오.

### 네임스페이스 파일 관리 (Namespace File Management)

업로드 파일, 서비스별 그래프 데이터, 네임스페이스별 RAG 인덱스의 격리 단위는
`namespace`입니다.

| 도구 | 설명 |
|------|-------------|
| `upload_file` | 네임스페이스에 파일 업로드 (base64) |
| `upload_files` | 단일 호출로 여러 네임스페이스에 다수 파일 업로드 |
| `list_namespaces` | 모든 네임스페이스 목록 출력 |
| `list_files` | 네임스페이스 내 파일 목록 출력 |
| `delete_file` | 네임스페이스에서 파일 삭제 |
| `delete_namespace` | 네임스페이스 전체 및 관련 파일 모두 삭제 |

정식 예시:

```
upload_file(namespace: "payment-svc", file_path: "handler.go", content: "<base64>")
list_files(namespace: "payment-svc")
delete_namespace(namespace: "payment-svc")
```

## Agent Skills (5개)

| 스킬 | 설명 |
|-------|-------------|
| `/ccg` | 핵심 빌드 및 검색 — 파싱, 그래프 빌드, 쿼리, 검색 |
| `/ccg-analyze` | 코드 분석 — 영향 범위, 흐름 추적, 데드 코드, 아키텍처 |
| `/ccg-annotate` | 어노테이션 시스템 — AI 기반 어노테이션 워크플로우, 태그 레퍼런스 |
| `/ccg-docs` | 문서화 — 문서 생성, RAG 인덱싱, 린트 |
| `/ccg-namespace` | 네임스페이스 파일 관리 — 네임스페이스 파일 업로드, 목록 출력, 삭제 |

이 스킬 파일들은 `skills/`에 있으며 slash-command 스타일의 에이전트
워크플로우를 위해 작성되었습니다. 일반적인 코딩 에이전트 작업을 적절한
CLI 및 MCP 표면으로 라우팅합니다.

### 사용법

```
/ccg build .                     — 코드 그래프 빌드
/ccg status                      — 그래프 통계 및 사후 처리 오류 요약 확인
/ccg-docs docs                   — 문서와 기본 RAG 인덱스 생성
/ccg-docs rag-index              — 기존 문서/community 기반 RAG 인덱스 재생성
/ccg-docs lint                   — 문서 상태 및 어노테이션 커버리지 체크
/ccg search "query"              — 어노테이션/키워드 기반 후보 검색
/ccg languages                   — 지원 언어 목록 출력
/ccg-annotate annotate internal/ — AI 기반 어노테이션 생성
/ccg-namespace                   — 네임스페이스 파일 및 디렉토리 관리
```
