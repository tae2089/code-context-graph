# Docker

[English](../docker.md)

## 이미지 빌드 (Build Image)

```bash
docker build -t ccg .
```

## MCP 서버로 실행 (Run as MCP Server)

```bash
# 외부 노출을 위해 Bearer 토큰을 설정합니다.
export CCG_HTTP_BEARER_TOKEN=replace-with-a-long-random-token

# 기본 로컬 SQLite 데이터베이스(ccg.db)의 경우, 첫 실행 시 스키마가 없는 경우에만
# 자동 마이그레이션이 수행됩니다.
# 프로젝트를 마운트하고 그래프를 빌드한 후 HTTP로 서빙합니다.
docker run -d -p 8080:8080 \
  -e CCG_HTTP_BEARER_TOKEN="$CCG_HTTP_BEARER_TOKEN" \
  -v $(pwd):/repo --entrypoint sh ccg \
  -c "ccg build /repo && ccg-server --http-addr :8080"
```

이미지의 기본 HTTP 명령어는 `:8080`에 바인딩되므로, 외부 접근을 위해 반드시 `CCG_HTTP_BEARER_TOKEN`을 제공해야 합니다.

이 컨테이너가 Ingress, 리버스 프록시 또는 로드 밸런서를 통해 노출되는 경우, 헬스 체크 및 상태 엔드포인트는 내부용으로 유지하십시오. `/health`, `/ready`, `/status`는 신뢰할 수 있는 운영 체크를 위한 것이며 공용 인터넷에 노출되어서는 안 됩니다. 엔드포인트 노출 가이드는 [운영 가이드](operations.md#http-exposure)를 참조하십시오.

리버스 프록시 정책 예시:

| 경로 | 공용 인터넷 | 내부 네트워크 |
|------|-------------|---------------|
| `/mcp` | Bearer 인증 및 네트워크 정책이 있을 때만 허용 | 허용 |
| `/wiki` | Wiki UI shell을 공개해도 되는 경우에만 허용 | 허용 |
| `/wiki/api/*` | Bearer 인증 및 네트워크 정책이 있을 때만 허용 | 허용 |
| `/webhook` | HMAC secret 및 repo allowlist가 있을 때만 허용 | 허용 |
| `/health` | 차단 | 허용 |
| `/ready` | 차단 | 허용 |
| `/status` | 차단 | 허용 |

웹훅 서비스 모드에서는 canonical clone base URL을 사용하고, repo 이름 중복이 없다는 보장이 없다면 CCG 인스턴스 하나에는 하나의 조직/owner만 연결하십시오.

```bash
docker run -d -p 8080:8080 \
  -e CCG_HTTP_BEARER_TOKEN="$CCG_HTTP_BEARER_TOKEN" \
  -e CCG_DB_DRIVER=postgres \
  -e CCG_DB_DSN="$CCG_DB_DSN" \
  -e CCG_REPO_ROOT=/data/repos \
  -v ccg-repos:/data/repos \
  --entrypoint ccg-server ccg \
  --http-addr :8080 \
    --allow-repo "acme/*" \
    --webhook-secret "$WEBHOOK_SECRET" \
    --repo-clone-base-url https://github.com
```

마운트된 기본 로컬 SQLite 데이터베이스의 경우, 기존 스키마에 대해 CCG를 업그레이드할 때는 명시적인 마이그레이션 명령어를 사용하십시오:

```bash
docker run --rm \
  -v $(pwd):/repo --entrypoint ccg ccg \
  migrate
```

PostgreSQL, 커스텀 SQLite DSN 또는 기타 비기본 런타임 설정을 사용하는 경우, 런타임 명령어를 시작하기 전에 일치하는 데이터베이스 드라이버와 DSN을 `ccg migrate`에 전달하십시오.

`.mcp.json`에서 연결하기:

```json
{
  "mcpServers": {
    "ccg": {
      "type": "streamable-http",
      "url": "http://localhost:8080/mcp",
      "headers": {
        "Authorization": "Bearer replace-with-a-long-random-token"
      }
    }
  }
}
```

## Wiki UI

Docker 이미지는 빌드된 Wiki UI를 `/usr/share/ccg/wiki`에 포함하며, 기본
컨테이너 명령은 `--wiki-dir /usr/share/ccg/wiki`로 이를 활성화합니다.
Standalone 바이너리는 Wiki asset을 embed하지 않습니다. 바이너리 배포에서는
release 페이지의 `ccg-wiki-dist.tar.gz`를 내려받아 압축을 풀고, 해당
디렉터리를 `ccg-server`에 전달하십시오:

```bash
ccg-server \
  --http-addr :8080 \
  --http-bearer-token "$CCG_HTTP_BEARER_TOKEN" \
  --wiki-dir ./wiki
```

정적 `/wiki` app shell은 브라우저에서 직접 열 수 있도록 요청 헤더 없이
서빙됩니다. `/wiki/api/*`는 `/mcp`와 같은 Bearer 토큰을 사용하며, UI는 API가
`401`을 반환하면 토큰 입력을 요청합니다.
Wiki를 열기 전에 각 네임스페이스에서 `ccg build`를 실행해 DB-backed tree
navigation, search, retrieve가 읽을 graph row를 준비하십시오. `ccg docs
--out docs`는 생성 Markdown, 수동 RAG-index workflow용 `doc-index.json`,
`wiki-index.json` 호환 snapshot이 필요할 때 계속 유용합니다. Wiki Graph 탭은
`/wiki/api/graph`를 통해 설정된 데이터베이스의 graph node와 edge를 직접
읽으므로 최신 `ccg build` 또는 webhook sync 상태를 반영합니다.

## SQLite vs PostgreSQL 선택 (Choosing SQLite vs PostgreSQL)

SQLite는 로컬 단일 사용자 워크플로우에 가장 간단한 선택입니다: 하나의 저장소, 수동 `ccg build` / `ccg update`, 그리고 필요할 때 재생성할 수 있는 데이터베이스 파일.

CCG를 서비스로 운영할 때는 PostgreSQL을 사용하십시오:

- 팀 공유 MCP 서버 또는 다수의 동시 MCP 클라이언트
- 지속적인 저장소 업데이트를 위한 웹훅 동기화 활성화
- 하나의 서버 데이터베이스에 여러 저장소 또는 네임스페이스 저장
- 운영상의 백업, 복구, 모니터링 또는 원격 접속 요구사항
- 약 5만 개 이상의 검색 문서 또는 10만 개 이상의 그래프 노드

규모가 큰 배포의 경우 PostgreSQL을 기본으로 취급해야 합니다. 그래프 노드가 약 30만 개 이상이거나, 항상 동기화되는 여러 저장소가 있거나, 빈번한 웹훅 업데이트가 발생하는 경우 SQLite는 운영상의 병목 현상이 될 가능성이 높습니다. 배포 프로필 및 규모 신호에 대해서는 [운영(Operations)](operations.md#database-choice)을 참조하십시오.

## PostgreSQL로 실행 (Run with PostgreSQL)

```bash
# PostgreSQL은 런타임 명령어 실행 전에 명시적인 마이그레이션 단계가 필요합니다.
docker run --rm \
  -e CCG_DB_DRIVER=postgres \
  -e CCG_DB_DSN="host=db user=ccg password=ccg dbname=ccg sslmode=disable" \
  --entrypoint ccg ccg \
  migrate

docker run -d -p 8080:8080 \
  -e CCG_HTTP_BEARER_TOKEN="$CCG_HTTP_BEARER_TOKEN" \
  -e CCG_DB_DRIVER=postgres \
  -e CCG_DB_DSN="host=db user=ccg password=ccg dbname=ccg sslmode=disable" \
  -v $(pwd):/repo --entrypoint sh ccg \
  -c "ccg build /repo && ccg-server --http-addr :8080"
```

위의 일회성 마이그레이션 명령어는 build, serve 또는 다른 런타임 명령어보다 먼저 실행되어야 합니다.

## Docker Compose

```bash
docker compose up -d
```

### 통합 테스트 (Gitea + PostgreSQL + ccg)

전체 파이프라인 테스트 또한 Docker Compose로 실행할 수 있습니다. 자세한 내용은 [개발 가이드](development.md#integration-test)를 참조하십시오.

```bash
docker compose -f docker-compose.integration.yml up -d --build
docker compose -f docker-compose.integration.yml down -v
```
