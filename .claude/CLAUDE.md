# code-context-graph

코드베이스를 Tree-sitter로 파싱하여 지식 그래프를 구축하는 로컬 코드 분석 도구.

## MCP 서버

`.mcp.json`에 등록된 ccg MCP 서버가 28개 도구를 제공합니다:
- `parse_project`, `build_or_update_graph`, `run_postprocess`
- `get_node`, `search`, `query_graph`, `list_graph_stats`
- `get_impact_radius`, `trace_flow`
- `find_large_functions`, `find_dead_code`
- `detect_changes`, `get_affected_flows`, `list_flows`
- `list_communities`, `get_community`, `get_architecture_overview`
- `get_annotation`
- `build_rag_index`, `get_rag_tree`, `get_doc_content`, `search_docs`
- `upload_file`, `upload_files`, `list_workspaces`, `list_files`, `delete_file`, `delete_workspace`

HTTP 모드 (`--transport streamable-http`)에서는 `/health` 및 `/webhook` 엔드포인트도 제공합니다.
Webhook은 `--allow-repo` 플래그로 허용 리포지토리를 설정하면 활성화됩니다.
레포별 브랜치 필터링: `--allow-repo "org/api:main,develop"` (glob 패턴, 미지정 시 main/master 기본).
GitHub (`X-Hub-Signature-256`) 및 Gitea (`X-Gitea-Signature`, `X-Gitea-Event`) 호환.
Push 이벤트 수신 → 자동 clone/pull → 그래프 빌드 → DB 저장 파이프라인.
Graceful shutdown: SIGINT/SIGTERM 시 진행 중인 clone/build에 context cancel 전파.

## CLI Skills (5개)

| Skill | 설명 |
|-------|------|
| `/ccg` | 코어 빌드 & 검색 — 파싱, 그래프 빌드, 쿼리, 검색 |
| `/ccg-analyze` | 코드 분석 — 영향 반경, 플로우 추적, 데드코드, 아키텍처 |
| `/ccg-annotate` | 어노테이션 시스템 — AI 어노테이션 워크플로우, 태그 레퍼런스 |
| `/ccg-docs` | 문서 — 문서 생성, RAG 인덱싱, lint |
| `/ccg-workspace` | 파일 워크스페이스 — 파일/워크스페이스 업로드, 목록, 삭제 |

주요 커맨드:
- `ccg build [dir]` — 코드 그래프 빌드 (`--exclude`, `--no-recursive` 지원)
- `ccg search <query>` — 전문 검색 (어노테이션 포함)
- `ccg docs [--out dir]` — 마크다운 문서 생성
- `ccg lint [--strict]` — 문서 품질 체크
- `ccg annotate [file|dir]` — AI 어노테이션 생성

`.ccg.yaml`로 exclude 패턴, DB 설정 등을 프로젝트 기본값으로 관리할 수 있습니다.

## 어노테이션 시스템

코드에 다음 태그를 사용하여 AI/비즈니스 컨텍스트를 기록합니다:
- `@index` — 파일/패키지 수준 설명
- `@intent` — 함수의 목적/의도
- `@domainRule` — 비즈니스 규칙
- `@sideEffect` — 부작용
- `@mutates` — 변경 대상
- `@requires` / `@ensures` — 사전/사후 조건
- `@param`, `@return`, `@see` — 표준 태그

## 개발 규칙

- TDD: Red → Green → Refactor
- Tidy First: 구조적 변경과 행위 변경 분리
- GORM 쿼리만 사용 (raw SQL 금지)
- 테스트: `CGO_ENABLED=1 go test -tags "fts5" ./... -count=1`
- Integration test: `./scripts/integration-test.sh` (Gitea + PostgreSQL + ccg Docker 전체 파이프라인)
