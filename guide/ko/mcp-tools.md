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
| `search` | code node full-text search, 파일별로 묶여서 반환됩니다: `files[] {file_path, hit_count, hits[]}` 형태이고 나온 파일은 통째로 나옵니다. 두 가지 모양의 질의를 모두 받습니다: 이름을 댈 수 있는 symbol (identifier, type, 그런 단어 두세 개)과 평문 질문 ("그래프는 어떻게 빌드되나"). symbol은 index된 node에 매칭되며 모든 term이 같은 node에 있어야 하고, 질문은 추가로 작성자가 기록한 이유(`@intent`, `@domainRule`)에도 점수를 매겨 그 이유가 정당화하는 파일을 이름 매칭 뒤에 덧붙입니다. `limit`은 파일 수를 세고 `offset`도 파일 단위라 페이지가 파일 중간을 자르지 않습니다. 각 hit이 근거(`matched` 신호와 node의 `@intent`, 이유로 매칭된 hit은 `reason`과 `matched_terms`까지)를 함께 반환하고, 근거 없는 후보는 잘라내 `weak_filtered`로 개수만 보고합니다. `path`로 범위 제한, `include_weak: true`로 잘린 후보 조회, `namespaces: []`로 여러 namespace 연합 검색 (결과에 namespace 라벨). 연합 검색에서 `limit`과 `offset`은 namespace마다 따로 적용되므로, 한도가 namespace 수보다 작아도 히트가 있는 namespace는 전부 페이지에 나옵니다. `truncated`는 이 페이지가 못 읽은 파일이 남았는지를 알려 주고, `pool_truncated`는 이 페이지가 답의 끝이 아니라 가져온 후보의 끝에서 멈췄다는 별개의 신호이며, `next`는 그걸 가져오는 호출을 그대로 적어 줍니다. 두 신호가 모두 false일 때만 검색이 끝난 것입니다. `annotation_coverage`는 `with_reason`/`declarations`, 즉 검색 대상 declaration 중 `@intent`나 `@domainRule`을 기록한 것이 몇 개인지를 tag 개수가 아니라 declaration 개수로 보고합니다. `with_reason: 0`이면 아무도 이유를 기록하지 않은 index에 질문을 던진 것이므로, 빈 답은 코드가 없다는 뜻이 아니라 어노테이션이 없다는 뜻입니다. 페이지의 어떤 hit도 자기 근거를 대지 못했을 때는 `next`에 tool 대신 `skill`(`ccg-annotate`)을 지목하는 항목이 함께 붙습니다 |
| `describe` | 한 경로 아래 그래프가 담고 있는 것을 순위 없이 그대로 나열. 폴더를 주면 바로 아래 한 단계의 폴더와 파일을, 각각의 파일 수와 선언 수와 함께 반환합니다. 파일을 주면 그 안에 쓰인 모든 선언을 작성 순서대로, 각각의 줄 범위와 `node_id`, 기록된 `@intent`와 함께 반환합니다. `search`가 넘겨주는 대상이 바로 이 도구입니다. search는 무엇이 중요한지 추측하므로 틀릴 수 있지만, 이 도구는 무엇이 존재하는지만 보고하므로 틀릴 수 없습니다. 질의도 limit도 관련도도 없습니다. 그래프에 없는 대상은 `scope: "unknown"`과 그 이름이 실제로 선언된 위치로 답합니다. `query_graph`의 `children_of`와 `file_summary` 패턴을 대체했습니다 |
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
2. symbol 후보든 "왜 이렇게 만들었나" 평문 질문이든 `search`로 찾은 뒤, 반환된 `node_id`로 그래프를 걷거나 `get_doc_content`로 문서를 읽습니다.
3. 이미 경로를 알고 있다면 — 검색 결과, 스택 프레임, diff 어디서 왔든 — `describe`로 그 자리에 무엇이 있는지 순위 없이 읽습니다.
4. 정확한 symbol과 관계는 `get_node`, `query_graph`로 확인합니다.
5. 변경 분석에는 `get_impact_radius`, `trace_flow`, `detect_changes`, `get_affected_flows`를 사용합니다.

`search`는 DB-backed이며 생성 retrieval index를 필요로 하지 않습니다. SQLite와 PostgreSQL에서 같은 질의에 같은 답을 반환하며, backend parity 테스트가 이를 단언합니다. 위 표에 등록된 도구만 현재 MCP 계약에 포함됩니다. 별도의 `find_by_intent` 도구는 더 이상 없습니다 — 문장형 질문의 답은 `search`가 흡수했습니다.

`query_graph`와 `list_flows`에는 명시적인 `limit`, `offset`을 사용하십시오. 50 또는 100개로 시작하고 페이지네이션 metadata를 따라 확장합니다.

로컬 MCP client는 stdio 방식의 `ccg serve`를 시작하고, 원격 client는 `ccg-server`의 `/mcp` Streamable HTTP endpoint에 연결합니다. 두 런타임은 동일한 18개 도구를 등록합니다.
