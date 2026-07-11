# 가이드 (Guide)

[English](../README.md)

code-context-graph의 문서 인덱스입니다. 한국어 문서는 영어 가이드를 미러링하며, 코드 및 CLI 식별자는 원문 그대로 유지합니다.

CCG는 GPT, Claude, Codex 같은 LLM 기반 코딩 에이전트가 개발 중 코드베이스 맥락을 정확하고 작게 가져오도록 만든 로컬/셀프호스트 context infrastructure입니다. 일반 SaaS 관리자용 제품이 아니라, CLI, MCP, 로그, 문서를 이해하는 개발자와 코딩 에이전트가 코드 검색, 영향 분석, 호출 그래프, 문서/RAG, bounded response를 활용하도록 설계되었습니다.

LLM 에이전트 워크플로우에서는 자연어 기반 코드 탐색을 docs/RAG 경로에서
시작하십시오. `search_docs`로 관련 문서를 찾은 뒤 `get_doc_content`로 하나를
읽습니다. Top1 정답을 강제하기보다 작은 파일 후보로 빠르게 경로를 좁힌 뒤
graph/search 도구로 정확한 위치와 관계를 확인하는 흐름을 권장합니다.

브라우저 Wiki는 `ccg-server`에서 `--wiki-dir`가 빌드된 React asset을
가리킬 때 제공됩니다. 표시는 graph database를 우선 사용하고,
`wiki-index.json`은 tree 호환 snapshot으로 사용합니다. runtime retrieve
모드는 DB-backed graph/annotation evidence를 사용하며, 시각적 Graph 탭은
`/wiki/api/graph`를 사용합니다.
개발자가 문서를 탐색하고 annotation-rich symbol card를 확인하거나, Context Tray Markdown을
모아 다른 LLM 도구에 붙여 넣고, graph edge를 시각적으로 살펴볼 때 사용합니다.

| 문서 | 설명 |
|------|------|
| [CLI 레퍼런스](cli-reference.md) | 모든 CLI 명령어, 옵션 및 설정 파일(`.ccg.yaml`) 안내 |
| [Eval](eval.md) | 파서/검색 품질 평가, golden corpus, 지표 설명 |
| [Lint](lint.md) | `ccg lint` 카테고리 상세 레퍼런스, 결과 해석 및 CI 활용법 |
| [MCP 도구](mcp-tools.md) | 33개의 MCP 도구, 에이전트 스킬, evidence-first 라우팅, AI 기반 어노테이션 |
| [어노테이션](annotations.md) | 커스텀 어노테이션 시스템 — 태그, 예시, 검색/RAG 품질 |
| [웹훅(Webhook)](webhook.md) | GitHub / Gitea 웹훅 동기화, 브랜치 필터링, Graceful Shutdown |
| [Docker](docker.md) | Docker 이미지 빌드, MCP 서버 설정, Wiki UI 배포, PostgreSQL 연동 |
| [운영(Operations)](operations.md) | 배포 프로필, 데이터베이스 선택, 준비성 체크, 웹훅 운영 및 문제 해결 |
| [사후 처리 실패 정책](postprocess-failure-policy.md) | 상태 규칙, 실패 원인, 빌드 및 사후 처리 도구의 자동 성능 저하(degraded)/폐쇄형 실패(fail_closed) 정책 |
| [런타임 구조](runtime-layout.md) | `ccg`, `ccg-server`, Wiki serving, 공용 `ccg-core` 소유권 경계 |
| [아키텍처](architecture.md) | 시스템 아키텍처, 데이터 흐름, DB 스키마 |
| [개발(Development)](development.md) | 빌드, 테스트, 통합 테스트(Gitea + PostgreSQL) |
| [Go 리팩터링 설계](go-refactoring-design.md) | Go 1.21-1.25 기능과 best practice 기반 리팩터링 계획 |
| [네임스페이스 마이그레이션](namespace-migration.md) | 기본 네임스페이스 변경 및 마이그레이션 가이드 |
| [CLAUDE.md 가이드](claude-md-guide.md) | CCG를 사용하는 프로젝트를 위한 CLAUDE.md 템플릿 |
