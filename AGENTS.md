# code-context-graph

코드베이스를 Tree-sitter로 파싱하여 지식 그래프를 구축하는 로컬 코드 분석 도구.

## MCP 서버

`.mcp.json`에 등록된 ccg MCP 서버가 35개 도구를 제공합니다:

- `parse_project`, `build_or_update_graph`, `run_postprocess`
- `get_postprocess_policy`, `reset_postprocess_policy`
- `get_node`, `search`, `query_graph`, `list_graph_stats`, `get_minimal_context`
- `get_impact_radius`, `trace_flow`
- `find_large_functions`, `find_dead_code`
- `detect_changes`, `get_affected_flows`, `list_flows`
- `list_communities`, `get_community`, `get_architecture_overview`
- `get_annotation`
- `build_rag_index`, `get_rag_tree`, `get_doc_content`, `search_docs`, `retrieve_docs`
- `upload_file`, `upload_files`, `list_namespaces`, `list_files`, `delete_file`, `delete_namespace`
- `list_workspaces`, `delete_workspace` (deprecated aliases)

HTTP 모드 (`--transport streamable-http`)에서는 `/health` 및 `/webhook` 엔드포인트도 제공합니다.
Webhook은 `--allow-repo` 플래그로 허용 리포지토리를 설정하면 활성화됩니다.
레포별 브랜치 필터링: `--allow-repo "org/api:main,develop"` (glob 패턴, 미지정 시 main/master 기본).
GitHub (`X-Hub-Signature-256`) 및 Gitea (`X-Gitea-Signature`, `X-Gitea-Event`) 호환.
Push 이벤트 수신 → 자동 clone/pull → 그래프 빌드 → DB 저장 파이프라인.
Graceful shutdown: SIGINT/SIGTERM 시 진행 중인 clone/build에 context cancel 전파.

## CLI Skills (5개)

| Skill            | 설명                                                                              |
| ---------------- | --------------------------------------------------------------------------------- |
| `/ccg`           | 코어 빌드 & 검색 — 파싱, 그래프 빌드, 쿼리, 검색                                  |
| `/ccg-analyze`   | 코드 분석 — 영향 반경, 플로우 추적, 데드코드, 아키텍처                            |
| `/ccg-annotate`  | 어노테이션 시스템 — AI 어노테이션 워크플로우, 태그 레퍼런스                       |
| `/ccg-docs`      | 문서 — 문서 생성, RAG 인덱싱, lint                                                |
| `/ccg-workspace` | 네임스페이스 파일 관리 — 파일 업로드, 목록, 삭제 (`workspace`는 deprecated alias) |

주요 커맨드:

- `ccg build [dir]` — 코드 그래프 빌드 (`--exclude`, `--no-recursive` 지원)
- `ccg docs [--out dir]` — 마크다운 문서 및 기본 RAG 인덱스 생성
- `ccg rag-index [--out dir]` — 이미 계산된 커뮤니티와 생성 문서 기반 RAG 인덱스 생성
- `ccg search <query>` — 전문 검색 (어노테이션 포함)
- `ccg lint [--strict]` — 문서 품질 체크
- `ccg annotate [file|dir]` — AI 어노테이션 생성

`.ccg.yaml`로 exclude 패턴, DB 설정 등을 프로젝트 기본값으로 관리할 수 있습니다.

## 코드 검색 규칙

코드 위치, 관련 구현, 호출 관계, 영향 범위, 아키텍처 맥락을 찾을 때는 먼저 ccg MCP 도구와 CLI Skills를 활용합니다.

- 자연어 기반 코드 이해, 모듈 탐색, 아키텍처 맥락 수집은 `/ccg-docs` skill과 `retrieve_docs`, `get_rag_tree`, `get_doc_content`를 우선 사용합니다.
- 정확한 심볼 위치, 호출 관계, 그래프 메타데이터 확인은 ccg MCP `query_graph`, `get_node`, `get_minimal_context` 또는 `/ccg` skill을 사용합니다.
- 어노테이션/키워드 기반 후보 검색은 ccg MCP `search` 또는 `ccg search`를 보조로 사용합니다.
- 영향 범위, 플로우, 데드코드, 구조 분석은 `/ccg-analyze` skill과 관련 MCP 도구(`get_impact_radius`, `trace_flow`, `find_dead_code`, `get_architecture_overview`)를 우선 사용합니다.
- 단순 문자열 확인, 파일 존재 확인, ccg 인덱스가 없거나 오래된 경우에는 `rg`를 보조로 사용하고, 필요한 경우 `ccg build .` 또는 `ccg update .`로 그래프를 갱신합니다.

## 문서

상세 문서는 `guide/` 디렉토리를 참조하세요:

- [CLI Reference](guide/cli-reference.md) — 전체 명령어, 플래그, 설정 파일
- [MCP Tools](guide/mcp-tools.md) — 35개 MCP 도구, Skills, AI-Driven Annotation
- [Annotations](guide/annotations.md) — 어노테이션 태그, 예시, 검색
- [Webhook](guide/webhook.md) — Webhook sync, 브랜치 필터링, HMAC, graceful shutdown
- [Docker](guide/docker.md) — Docker 빌드, MCP 서버, PostgreSQL 배포
- [Development](guide/development.md) — 개발 가이드, Integration test, 프로젝트 구조
- [Architecture](guide/architecture.md) — 데이터 흐름, 컴포넌트, DB 스키마

## 개발 규칙

- TDD: Red → Green → Refactor
- Tidy First: 구조적 변경과 행위 변경 분리
- GORM 쿼리만 사용 (raw SQL 금지)
- 테스트: `CGO_ENABLED=1 go test -tags "fts5" ./... -count=1`
- Integration test: `./scripts/integration-test.sh` (Gitea + PostgreSQL + ccg Docker 전체 파이프라인)

## 코드 작성 규칙

새 코드를 생성하거나 기존 코드의 의미 있는 동작을 바꿀 때는 CCG annotation을 함께 작성합니다.

우선순위:

- 패키지/파일의 역할이 드러나야 하면 `// @index ...`
- 새 public type/function/method, MCP handler, CLI command, service method에는 `// @intent ...`
- 입력/출력 계약이 중요한 함수에는 `// @param`, `// @return`
- 사전 조건/보장 조건이 중요한 경우 `// @requires`, `// @ensures`
- 파일, DB, 네트워크, 캐시, 로그, 프로세스 등 외부 상태를 바꾸면 `// @sideEffect`
- receiver나 인자로 받은 값을 변경하면 `// @mutates`
- 비즈니스 규칙, 운영 정책, false-positive/false-negative 판단 기준은 `// @domainRule`
- 관련 핸들러/서비스/모델이 있으면 `// @see`

Annotation은 코드의 동작과 맞아야 하며, 설명을 부풀리지 않습니다. 단순 getter/setter나 자명한 한 줄 helper에는 억지로 붙이지 않습니다.

## 작업 완료 체크

코드 생성/수정 작업을 완료하면 기본적으로 다음을 실행합니다.

```bash
ccg build .
ccg docs --out docs
ccg lint
```

동작 변경이나 DB/검색/파서/MCP 핸들러 변경이 있으면 Go 테스트도 함께 실행합니다.

```bash
CGO_ENABLED=1 go test -tags "fts5" ./... -count=1
```

문서만 수정한 작업은 `ccg docs` 재생성과 `ccg lint`를 우선하고, 코드 테스트는 변경 범위에 따라 생략할 수 있습니다.
