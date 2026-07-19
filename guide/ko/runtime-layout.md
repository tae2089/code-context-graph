# 런타임 레이아웃

CCG는 하나의 runtime 조립 트리와 의도적으로 다른 의존성 폐쇄를 가진 두
바이너리를 사용합니다.

| Runtime owner | 경로 | 책임 |
| --- | --- | --- |
| 공용 runtime | `internal/runtime` | Parser registry, DB/schema, graph/search adapter, ingest transaction, migration, parser/DB idempotent close |
| MCP runtime | `internal/runtime/mcp` | 다섯 MCP 의존성 그룹, cache, telemetry, stdio signal, MCP idempotent close |
| 원격 runtime | `internal/runtime/remote` | 원격 MCP, Wiki, webhook/repository-sync queue, HTTP host 조립 |
| HTTP host | `internal/adapters/inbound/http` | Route, auth/body limit, readiness/status 매핑, listener, signal, bounded HTTP shutdown |

## 바이너리 의존성 폐쇄

### `ccg`

`cmd/ccg`는 공용 runtime, CLI adapter, MCP runtime을 선택합니다. 일회성
build/update/search/docs/lint/status/migrate와 stdio 기반 `ccg serve`를 제공합니다.
HTTP host, Wiki HTTP, webhook adapter, remote runtime은 링크하지 않습니다.

### `ccg-server`

`cmd/ccg-server`는 공용 runtime과 remote runtime을 선택하며 다음을 노출합니다.

- `/mcp` — Streamable HTTP MCP
- `/health` — liveness
- `/ready` — DB 및 blocking queue readiness
- `/status` — 인증된 운영 상태
- `/wiki`, `/wiki/api/*` — 선택적인 CCG 내장 Wiki
- `/webhook` — 선택적인 GitHub/Gitea repository sync

두 transport 모두 `Runtime.MCPComponents()`를 사용하므로 동일한 18개 tool과
4개 prompt를 등록합니다.

## 리소스 소유권

| 리소스 | 소유자 | 종료 규칙 |
| --- | --- | --- |
| Tree-sitter walker | 공용 runtime | alias pointer deduplicate 후 한 번만 close |
| DB connection | 공용 runtime | parser 정리 뒤 한 번만 close |
| MCP cache/telemetry | MCP Instance | 한 번만 close, telemetry는 bounded context 사용 |
| Repository-sync context/worker | remote runtime | cancel 후 한 번만 drain |
| HTTP listener | inbound HTTP host | accept 중단, bounded `Shutdown`, queue cleanup |
| Process exit | `cmd/*` | 최종 오류 기록, runtime close, exit code 선택 |

부분 초기화도 stack 순서를 따릅니다. MCP cleanup을 즉시 등록하고, Wiki 검증
뒤 queue를 생성하며, queue cleanup을 즉시 등록합니다. Listener 오류와 signal은
동일한 idempotent cleanup 함수로 수렴합니다.

## 의존성 방향

- Application과 adapter는 runtime을 import하지 않습니다.
- `runtime/remote`가 inbound/outbound를 조립하고 HTTP에 `HostDeps`를 전달합니다.
- HTTP는 완성된 handler/check/queue hook만 받아 DB, Wiki, webhook, Git, search,
  telemetry 구현을 만들 수 없습니다.
- 원격 조립을 subpackage로 분리해 로컬 `ccg`가 공용 runtime을 import해도 원격
  패키지가 의존성 폐쇄에 들어오지 않습니다.

`internal/archtest`가 이 규칙과 제거된 runtime 패키지의 부재를 검증합니다.

## 실행 예시

로컬 MCP:

```bash
ccg build .
ccg serve
```

브라우저 Wiki:

```bash
ccg build .
ccg docs --out docs
ccg-server --http-addr 127.0.0.1:8080 --wiki-dir web/wiki/dist
```

원격 MCP:

```bash
ccg-server --http-addr 0.0.0.0:8080 --http-bearer-token "$CCG_HTTP_BEARER_TOKEN"
```

Webhook sync:

이 명령을 시작하기 전에 배포 secret store를 통해 `CCG_WEBHOOK_SECRET`을 설정하십시오.

```bash
ccg-server \
  --http-addr 0.0.0.0:8080 \
  --http-bearer-token "$CCG_HTTP_BEARER_TOKEN" \
  --allow-repo "org/api:main,develop" \
  --repo-clone-base-url https://github.com \
  --repo-root /data/repos
```

내장 Wiki는 CCG 고유 기능입니다. 향후 OpenWiki는 별도 제품 경계이며 이 runtime
route를 대체하지 않습니다.
