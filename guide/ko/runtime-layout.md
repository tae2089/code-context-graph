# 런타임 구조 (Runtime Layout)

[English](../runtime-layout.md)

CCG는 세 개의 런타임 레이어로 분리됩니다.

| 레이어 | 경로 | 책임 |
|--------|------|------|
| `ccg` | `cmd/ccg`, `internal/cli` | 로컬 CLI 명령과 stdio 기반 로컬 MCP |
| `ccg-server` | `cmd/ccg-server`, `internal/server` | 셀프호스트 Streamable HTTP MCP 서버, health/status 엔드포인트, 웹훅 동기화 |
| MCP runtime | `internal/mcpruntime` | 공용 MCP handler assembly, cache, telemetry, postprocess policy, stdio runner |
| `ccg-core` | `internal/core` | 공용 parser, DB, store, search, migration, incremental sync wiring |

이 분리는 로컬 에이전트 사용 경로를 작게 유지하고, 셀프호스트 배포의 HTTP
노출 및 웹훅 정책을 명시적으로 분리하기 위한 구조입니다.

## 바이너리

### `ccg`

`ccg`는 로컬 개발자와 에이전트를 위한 바이너리입니다. `build`, `update`,
`search`, `docs`, `lint`, `status`, `migrate`, `eval` 같은 일회성 명령을
담당합니다.

`ccg serve`는 stdio MCP만 시작합니다. 같은 머신에서 실행되는 Codex,
Claude Code 같은 로컬 MCP 클라이언트를 위한 경로입니다. HTTP와 웹훅
플래그는 이 명령에 포함하지 않습니다.

### `ccg-server`

`ccg-server`는 장시간 실행되는 셀프호스트 서비스입니다. 다음 엔드포인트를
제공합니다.

- `/mcp`: Streamable HTTP MCP
- `/health`: liveness
- `/ready`: readiness
- `/status`: 운영 진단
- `/wiki`: `--wiki-dir`가 빌드된 Wiki asset을 가리키는 경우
- `/wiki/api/*`: Wiki tree, docs, retrieval, context copy, 시각적 graph data
- `/webhook`: `--allow-repo`가 설정된 경우 웹훅 수신

원격 클라이언트, 팀 배포, 컨테이너 배포, GitHub/Gitea 웹훅 동기화에는
`ccg-server`를 사용합니다.

### `ccg-core`

`internal/core`는 공용 런타임 조립 계층입니다. 다음을 제공합니다.

- 언어 walker 등록
- 데이터베이스 열기 및 스키마 버전 검증
- 마이그레이션 실행
- GORM store 생성
- 검색 backend 선택
- incremental sync 생성
- parser 및 데이터베이스 cleanup

이 패키지는 명령 레이어가 아니라 런타임 wiring 계층입니다. CLI 플래그
파싱은 `internal/cli`, HTTP/웹훅 정책은 `internal/server`에 둡니다.

## 소유권 경계

| 관심사 | 소유 위치 |
|--------|-----------|
| Cobra 로컬 명령 정의 | `internal/cli` |
| 로컬 stdio MCP 명령 | `internal/cli/serve.go`, `internal/mcpruntime` |
| HTTP listen address, bearer token, stateless session | `internal/server`, `cmd/ccg-server` |
| Wiki 정적 파일 서빙 및 viewer API | `internal/wikiserver`, `web/wiki`, `internal/server` |
| 웹훅 allowlist, HMAC, clone base URL, repo root, retry 정책 | `internal/server`, `internal/webhook` |
| MCP tool handler 및 DTO | `internal/mcp` |
| transport-neutral 공용 MCP runtime | `internal/mcpruntime` |
| 공용 graph runtime dependency | `internal/core` |
| graph build/update 비즈니스 동작 | `internal/service` |
| Docker 기본 프로세스 | `ccg-server` |

## 주요 워크플로우

로컬 graph 작업:

```bash
ccg build .
ccg search "authentication"
ccg docs --out docs
ccg serve
```

`ccg docs`는 `/wiki` 호환 snapshot인 `.ccg/wiki-index.json`을 기록하고,
`--rag=false`가 설정되지 않은 경우 수동 RAG-index workflow용
`.ccg/doc-index.json`도 함께 기록합니다.

브라우저 Wiki:

```bash
ccg build .
ccg docs --out docs
ccg-server \
  --http-addr 127.0.0.1:8080 \
  --wiki-dir web/wiki/dist
```

Wiki tree, search, retrieve 모드는 설정된 데이터베이스를 우선 사용합니다.
`wiki-index.json`은 해당 DB-backed tree 경로를 사용할 수 없을 때만 쓰는
호환 snapshot이며, `doc-index.json`은 runtime retrieve가 아니라 수동 RAG-index
호환성을 위해 유지됩니다.
Graph 탭은 `/wiki/api/graph`를 통해 설정된 데이터베이스의 graph node와
edge를 직접 읽으므로 최신 `ccg build` 또는 webhook sync 상태를 반영합니다.
Context Tray copy는 생성 문서에 대해 `/wiki/api/context`를 사용하고,
doc-less symbol detail은 브라우저 payload에 유지합니다.

셀프호스트 HTTP MCP:

```bash
ccg-server \
  --http-addr 0.0.0.0:8080 \
  --http-bearer-token "$CCG_HTTP_BEARER_TOKEN"
```

웹훅 동기화:

```bash
ccg-server \
  --http-addr 0.0.0.0:8080 \
  --http-bearer-token "$CCG_HTTP_BEARER_TOKEN" \
  --allow-repo "org/api:main,develop" \
  --webhook-secret "$WEBHOOK_SECRET" \
  --repo-clone-base-url https://github.com \
  --repo-root /data/repos
```

Docker:

```bash
docker run -d -p 8080:8080 \
  -e CCG_HTTP_BEARER_TOKEN="$CCG_HTTP_BEARER_TOKEN" \
  ccg --http-addr :8080
```

Docker 이미지는 일회성 build/migrate 워크플로우를 위해 `ccg`도 포함하지만,
기본 entrypoint는 `ccg-server`입니다.

GitHub release archive와 npm 패키지는 두 실행 파일을 모두 포함합니다. 하나의
패키지를 설치한 뒤 런타임에서 `ccg` 또는 `ccg-server`를 선택해 사용합니다.

## 마이그레이션 메모

기존에 다음처럼 실행하던 배포는:

```bash
ccg serve --transport streamable-http ...
```

다음으로 변경해야 합니다.

```bash
ccg-server ...
```

`ccg serve --transport streamable-http`는 이제 HTTP를 시작하지 않고 안내
오류를 반환합니다. 기존 stdio MCP 클라이언트는 그대로 `ccg serve`를
사용할 수 있습니다.

로컬 `ccg` 바이너리는 `internal/server` 또는 `internal/webhook`을 import하지
않습니다. 두 바이너리는 여전히 `internal/mcpruntime`, `internal/mcp`,
parser, DB, analysis 패키지를 공유하므로 대부분의 코드 크기는 공통으로
남지만, HTTP/웹훅 코드는 `ccg-server`에만 링크됩니다.

## 설정 메모

두 바이너리는 동일한 DB 설정을 읽습니다.

- `--db-driver`, `CCG_DB_DRIVER`, `.ccg.yaml`의 `db.driver`
- `--db-dsn`, `CCG_DB_DSN`, `.ccg.yaml`의 `db.dsn`

`ccg-server`는 `CCG_HTTP_BEARER_TOKEN`, `CCG_OTEL_ENDPOINT`,
`CCG_WEBHOOK_WORKERS`, `CCG_WEBHOOK_MAX_TRACKED_REPOS`,
`CCG_WEBHOOK_ATTEMPT_TIMEOUT`, retry tuning 변수, `CCG_REPO_ROOT` 같은
지원 환경 변수도 server 기본값으로 읽습니다.
