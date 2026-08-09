# MCP 도구

code-context-graph는 로컬 `ccg serve`와 셀프호스트 `ccg-server` 런타임 모두에서 19개의 MCP 도구를 제공합니다.

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
| `search` | code node full-text search, 파일별로 묶여서 반환됩니다: `files[] {file_path, hit_count, hits[]}` 형태이고 나온 파일은 통째로 나옵니다. `limit`은 파일 수를 세고 `offset`도 파일 단위라 페이지가 파일 중간을 자르지 않습니다. 각 hit이 근거(`matched` 신호와 node의 `@intent`)를 함께 반환하고, 근거 없는 후보는 잘라내 `weak_filtered`로 개수만 보고합니다. `path`로 범위 제한, `include_weak: true`로 잘린 후보 조회, `namespaces: []`로 여러 namespace 연합 검색 (결과에 namespace 라벨). `truncated`는 이 페이지가 못 읽은 파일이 남았는지를 알려 주고, `next`는 그걸 가져오는 호출을 그대로 적어 줍니다 |
| `find_by_intent` | 무엇이 왜 만들어졌는지 평문으로 질문. 이름이나 경로가 아니라 기록된 `@intent`/`@domainRule`에만 점수를 매기므로, `search`가 답하지 못하는 문장형 질문에 답합니다. `files[] {file_path, entries[]}`를 반환하고 각 entry의 `node_id`를 `get_node`, `query_graph`, `get_impact_radius`, `trace_flow`에 그대로 넘길 수 있습니다. 이름 검색으로의 fallback은 없으며, `coverage`가 이 namespace에서 기록된 이유를 가진 선언이 얼마나 되는지 알려 줍니다. 각 entry는 `matched_terms`를, 응답은 `terms`와 `reasons_searched`를 함께 반환하므로, 긴 파일 목록이 흔한 단어 하나에만 걸린 것인지 호출자가 직접 판단할 수 있습니다 |
| `describe` | 한 경로 아래 그래프가 담고 있는 것을 순위 없이 그대로 나열. 폴더를 주면 바로 아래 한 단계의 폴더와 파일을, 각각의 파일 수와 선언 수와 함께 반환합니다. 파일을 주면 그 안에 쓰인 모든 선언을 작성 순서대로, 각각의 줄 범위와 `node_id`, 기록된 `@intent`와 함께 반환합니다. `search`와 `find_by_intent`가 넘겨주는 대상이 바로 이 도구입니다. 그 둘은 무엇이 중요한지 추측하므로 틀릴 수 있지만, 이 도구는 무엇이 존재하는지만 보고하므로 틀릴 수 없습니다. 질의도 limit도 관련도도 없습니다. 그래프에 없는 대상은 `scope: "unknown"`과 그 이름이 실제로 선언된 위치로 답합니다. `query_graph`의 `children_of`와 `file_summary` 패턴을 대체했습니다 |
| `get_annotation` | node의 annotation과 문서 tag 조회 |
| `query_graph` | callers, callees, imports, importers, tests, inheritors 조회; `namespaces: []`는 namespace별 그룹 응답. 파일이나 폴더 안에 무엇이 쓰여 있는지는 `describe`를 사용합니다 |
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
| `get_doc_content` | 선택한 생성 Markdown 파일을 안전하게 읽기 |
| `get_minimal_context` | 작은 프로젝트/변경 요약과 다음 도구 제안 반환 |

## 권장 라우팅

1. 익숙하지 않은 작업은 `get_minimal_context`로 시작합니다.
2. "왜 이렇게 만들었나"에 해당하는 평문 질문은 `find_by_intent`로 좁힌 뒤, 반환된 `node_id`로 그래프를 걷거나 `get_doc_content`로 문서를 읽습니다.
3. annotation 또는 symbol 후보는 `search`로 찾습니다.
4. 이미 경로를 알고 있다면 — 검색 결과, 스택 프레임, diff 어디서 왔든 — `describe`로 그 자리에 무엇이 있는지 순위 없이 읽습니다.
5. 정확한 symbol과 관계는 `get_node`, `query_graph`로 확인합니다.
6. 변경 분석에는 `get_impact_radius`, `trace_flow`, `detect_changes`, `get_affected_flows`를 사용합니다.

`find_by_intent`는 DB-backed이며 생성 retrieval index를 필요로 하지 않습니다. 위 표에 등록된 도구만 현재 MCP 계약에 포함됩니다.

`query_graph`와 `list_flows`에는 명시적인 `limit`, `offset`을 사용하십시오. 50 또는 100개로 시작하고 페이지네이션 metadata를 따라 확장합니다.

로컬 MCP client는 stdio 방식의 `ccg serve`를 시작하고, 원격 client는 `ccg-server`의 `/mcp` Streamable HTTP endpoint에 연결합니다. 두 런타임은 동일한 19개 도구를 등록합니다.
