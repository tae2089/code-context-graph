# 개발 (Development)

[English](../development.md)

## 빌드 (Build)

```bash
CGO_ENABLED=1 go build -tags "fts5" -o ccg ./cmd/ccg/
CGO_ENABLED=1 go build -tags "fts5" -o ccg-server ./cmd/ccg-server/
```

Makefile 단축 명령어:

```bash
make build        # stripped ccg 및 ccg-server 바이너리 빌드 (make release와 동일)
make release      # 버전/커밋/날짜 정보가 포함된 stripped 빌드
make build-debug  # 버전/커밋/날짜 정보가 포함된 unstripped ccg 및 ccg-server 바이너리 빌드
make wiki-db      # 로컬 Wiki DB 마이그레이션 및 WIKI_REPO 그래프 빌드
make wiki-run     # Wiki UI 빌드, 그래프 빌드, DB-backed Wiki API로 ccg-server 실행
make wiki-run-indexed # Wiki UI 빌드, 그래프/문서/index 생성 후 ccg-server 실행
```

`make wiki-run`은 기본값으로 `127.0.0.1:8080`과 `ccg.db`를 사용합니다.
`WIKI_ADDR`, `WIKI_DB`, `WIKI_REPO`, 필요 시 `WIKI_TOKEN`으로 값을 바꿀 수 있습니다:

```bash
WIKI_ADDR=127.0.0.1:18080 WIKI_TOKEN=dev-token make wiki-run
```

## 테스트 (Test)

```bash
make test
```

`make test`는 Go 테스트 스위트와 Docker 통합 테스트용 경량 셸 헬퍼 테스트를 모두 실행합니다.

## Integration Test

풀스택 파이프라인 테스트: Gitea push → 명시적 `ccg migrate` → 웹훅 → ccg 복제 → 빌드 → PostgreSQL → MCP 검증:

```bash
./scripts/integration-test.sh
```

경량 셸 헬퍼 테스트는 Docker를 시작하지 않고 통합 테스트용 헬퍼들을 테스트합니다:

```bash
make test-integration-helpers
```

### 테스트 수행 과정

1. Docker Compose를 통해 3개의 컨테이너(Gitea, PostgreSQL, ccg) 시작
2. 런타임 서비스 시작 전 ccg 컨테이너에서 `ccg migrate` 실행
3. Gitea 관리자 계정 및 API 토큰 생성
4. 샘플 Go 코드가 포함된 저장소 생성
5. ccg를 가리키는 웹훅 등록
6. Gitea에 코드 push (웹훅 트리거)
7. ccg가 복제, 파싱 및 빌드를 완료할 때까지 대기
8. MCP 프로토콜을 통해 그래프 데이터 검증 (초기화 → 도구 호출)
9. 실패 시 디버그 아티팩트(artifact) 캡처
10. 별도의 요청이 없는 한 모든 컨테이너 정리

### 통합 테스트 실패 디버깅

통합 테스트 도구는 실패 시 Docker 진단 정보를 기록합니다. 로컬 디버깅을 위해 다음 환경 변수들을 사용할 수 있습니다:

| 변수 | 기본값 | 설명 |
|----------|---------|-------------|
| `ARTIFACT_DIR` | `artifacts/integration-<timestamp>` | `compose-ps.txt`, `compose.log` 및 서비스별 로그 저장 디렉토리 |
| `KEEP_CONTAINERS` | `0` | 실행 후 `docker compose down -v`를 생략하려면 `1`로 설정 |
| `DUMP_ON_SUCCESS` | `0` | 성공 시에도 아티팩트를 캡처하려면 `1`로 설정 |
| `WEBHOOK_WAIT_SECONDS` | `60` | 저장소당 웹훅/빌드 최대 대기 시간 |
| `CCG_E2E_ALLOW_MCP_LOG_FALLBACK` | `0` | 로컬 디버깅용: MCP 초기화 실패 시 로그 기반 웹훅 체크를 허용하려면 `1`로 설정. 기본값은 MCP 검증이 필수이므로 실패 처리됨. |

예시:

```bash
KEEP_CONTAINERS=1 ARTIFACT_DIR=/tmp/ccg-e2e ./scripts/integration-test.sh
DUMP_ON_SUCCESS=1 ./scripts/integration-test.sh
```

웹훅 대기 시 대상 워크스페이스의 MCP 관측 가능 그래프 통계를 우선적으로 확인하며, MCP가 준비되지 않았거나 데이터를 아직 보여주지 않는 경우에만 ccg 로그로 폴백합니다.
MCP 초기화 및 도구 응답은 엄격하게 체크됩니다: 잘못된 형식의 JSON, 최상위 JSON-RPC 에러, `result.isError=true`, 내용 검증을 위한 `result.content[0].text` 누락 등은 통합 테스트 실패로 처리됩니다. MCP를 초기화할 수 없는 실행은 위에서 언급한 로컬 디버그 재정의가 설정되지 않는 한 성공으로 보고되지 않으며, 해당 재정의 설정 시 MCP 도구 검증은 건너뜁니다.

### 수동 컨테이너 관리

```bash
docker compose -f docker-compose.integration.yml up -d --build
docker compose -f docker-compose.integration.yml down -v
```

## 프로젝트 구조 (Project Structure)

```
cmd/ccg/              — 로컬 CLI 진입점 (cobra, stdio MCP)
cmd/ccg-server/       — 셀프호스트 HTTP MCP/웹훅 서버 진입점
internal/
  analysis/           — 분석 엔진 (impact, flows, changes, incremental update)
  annotation/         — 어노테이션 파서
  cli/                — CLI 명령어 정의
  core/               — parser, DB, store, search, sync 공용 런타임 wiring
  ctxns/              — 컨텍스트 네임스페이스
  docs/               — 문서 생성 로직
  mcpruntime/         — 공용 MCP runtime assembly, stdio runner, cache, telemetry
  mcp/                — MCP 서버 (17개 도구)
  wikiserver/         — ccg-server Wiki 정적 파일 서빙 및 viewer API
  wikiindex/          — Wiki 표시용 인덱스 생성기 (`wiki-index.json`)
  model/              — DB 모델
  parse/treesitter/   — Tree-sitter 파서 (Lua/Luau 포함 12개 언어)
  pathutil/           — 경로 유틸리티
  ragindex/           — 공용 Wiki tree 및 문서 검색 DTO/helper
  server/             — HTTP MCP 서버, health/status 엔드포인트, 웹훅 런타임
  service/            — 비즈니스 로직
  store/              — GORM 저장소
  webhook/            — 웹훅 핸들러, SyncQueue, RepoFilter
skills/               — 에이전트 스킬 파일
guide/                — 프로젝트 문서
docs/                 — 자동 생성된 문서 (ccg docs)
scripts/              — 스크립트 (통합 테스트 등)
```

React/Tailwind Wiki UI는 `web/wiki`에 있으며 `web/wiki/dist`로 빌드됩니다.
dist 디렉터리는 git에서 제외하고 release에서 별도 asset으로 패키징합니다:

```bash
make wiki-build
```

## 컨벤션 (Conventions)

- TDD: Red → Green → Refactor
- Tidy First: 구조적 변경과 행동 변경의 분리
- GORM 쿼리만 사용 (Raw SQL 사용 금지)
- 로깅: `slog`
- CLI: `cobra` 프레임워크
- 빌드 플래그: `CGO_ENABLED=1 -tags "fts5"`
