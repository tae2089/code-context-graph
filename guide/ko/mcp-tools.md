# MCP 도구

code-context-graph는 로컬 `ccg serve`와 셀프호스트 `ccg-server` 런타임 모두에서 18개의 MCP 도구를 제공합니다.

## 파싱 및 빌드

| 도구 | 용도 |
| ---- | ---- |
| `parse_project` | 소스 파일을 파싱해 graph node와 edge 저장 |
| `build_or_update_graph` | 파일시스템 경로에서 graph namespace 전체 또는 증분 빌드 |
| `run_postprocess` | 저장 flow 및 full-text search index 재생성 |

## 조회

| 도구 | 용도 |
| ---- | ---- |
| `get_node` | qualified name으로 node 하나 조회 |
| `search` | path scope를 선택적으로 적용하는 code node full-text search; `namespaces: []`로 여러 namespace 연합 검색 (결과에 namespace 라벨) |
| `get_annotation` | node의 annotation과 문서 tag 조회 |
| `query_graph` | callers, callees, imports, children, tests, inheritors, file summary 조회; `namespaces: []`는 namespace별 그룹 응답 |
| `list_graph_stats` | node와 edge를 kind 및 language별로 집계; `namespaces: []`는 namespace별 그룹 응답 |
| `list_namespaces` | graph data가 있는 namespace와 node count 목록 조회 |

## 분석

| 도구 | 용도 |
| ---- | ---- |
| `get_impact_radius` | node 주변의 제한된 BFS 영향 반경 계산; `cross_namespace: true`면 resolved `ccg://` ref를 양방향으로 통과 |
| `trace_flow` | node에서 시작하는 제한된 call chain 추적; `cross_namespace: true`면 resolved `ccg://` ref를 넘어 계속 추적 |
| `detect_changes` | git diff 기반 변경 함수 탐지 및 risk score 계산 |
| `get_affected_flows` | 최근 변경의 영향을 받는 저장 flow 조회 |
| `list_flows` | 저장 flow를 페이지네이션해 조회 |
| `list_cross_refs` | 실체화된 `ccg://` cross-namespace 참조 목록 (direction: outbound/inbound/both, status 필터) |

## 문서 및 컨텍스트

| 도구 | 용도 |
| ---- | ---- |
| `search_docs` | DB-backed 문서 후보와 graph evidence 검색; `namespaces: []`는 namespace별 그룹 응답 |
| `get_doc_content` | 선택한 생성 Markdown 파일을 안전하게 읽기 |
| `get_minimal_context` | 작은 프로젝트/변경 요약과 다음 도구 제안 반환 |

## 권장 라우팅

1. 익숙하지 않은 작업은 `get_minimal_context`로 시작합니다.
2. 넓은 모듈 질문은 `search_docs`로 좁힌 뒤 `get_doc_content`로 선택한 문서를 읽습니다.
3. annotation 또는 symbol 후보는 `search`로 찾습니다.
4. 정확한 symbol과 관계는 `get_node`, `query_graph`로 확인합니다.
5. 변경 분석에는 `get_impact_radius`, `trace_flow`, `detect_changes`, `get_affected_flows`를 사용합니다.

`search_docs`는 DB-backed이며 생성 retrieval index를 필요로 하지 않습니다. 위 표에 등록된 도구만 현재 MCP 계약에 포함됩니다.

`query_graph`와 `list_flows`에는 명시적인 `limit`, `offset`을 사용하십시오. 50 또는 100개로 시작하고 페이지네이션 metadata를 따라 확장합니다.

로컬 MCP client는 stdio 방식의 `ccg serve`를 시작하고, 원격 client는 `ccg-server`의 `/mcp` Streamable HTTP endpoint에 연결합니다. 두 런타임은 동일한 18개 도구를 등록합니다.
