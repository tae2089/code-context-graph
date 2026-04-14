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

HTTP 모드 (`--transport streamable-http`)에서는 `/health` 엔드포인트도 제공합니다.

## CLI Skill

`/ccg` 슬래시 커맨드로 CLI를 직접 실행할 수 있습니다.

주요 커맨드:
- `ccg build [dir]` — 코드 그래프 빌드 (`--exclude`, `--no-recursive` 지원)
- `ccg docs [--out dir]` — 마크다운 문서 생성
- `ccg index [--out dir]` — index.md만 재생성
- `ccg languages` — 지원 언어 목록
- `ccg example [language]` — 어노테이션 작성 예시
- `ccg tags` — 태그 레퍼런스
- `ccg hooks install` — pre-commit 훅 설치

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
