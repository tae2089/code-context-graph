# Webhook Sync

GitHub 또는 Gitea의 push 이벤트를 수신하여 자동으로 clone/pull → 코드 그래프 빌드를 수행합니다.

## Setup

```bash
ccg serve --transport streamable-http \
  --allow-repo "org/api:main,develop" \
  --allow-repo "org/web:main" \
  --webhook-secret "your-secret" \
  --repo-root /data/repos
```

### Endpoints

| Path | Method | Description |
|------|--------|-------------|
| `/mcp` | POST | MCP Streamable HTTP |
| `/health` | GET | Health check (`{"status":"ok"}`) |
| `/webhook` | POST | Webhook receiver (GitHub / Gitea push events) |

## Per-Repo Branch Filtering

`--allow-repo` 플래그로 레포지토리별 허용 브랜치를 설정합니다.

### Format

```
--allow-repo "REPO_PATTERN:BRANCH1,BRANCH2"
```

- `REPO_PATTERN`: glob 패턴 (`path.Match` 사용). 예: `org/*`, `org/api`, `*/*`
- `BRANCH1,BRANCH2`: 허용 브랜치 (쉼표 구분, glob 패턴 지원)
- 브랜치 미지정 시 기본값: `main`, `master`

### Examples

```bash
# org/api 레포의 main, develop 브랜치만 허용
--allow-repo "org/api:main,develop"

# org 아래 모든 레포, 기본 브랜치(main, master)
--allow-repo "org/*"

# release/* 패턴 브랜치 허용
--allow-repo "org/api:main,release/*"

# 여러 레포 설정
--allow-repo "org/api:main,develop" --allow-repo "org/web:main"
```

### Matching Rules

1. 첫 번째 매칭 룰 사용 (순서 중요)
2. 매칭되는 룰이 없으면 거부
3. Webhook payload의 `ref` 필드에서 `refs/heads/` 접두사 자동 제거

## Signature Verification

HMAC-SHA256으로 webhook payload를 검증합니다.

| Platform | Header | Format |
|----------|--------|--------|
| GitHub | `X-Hub-Signature-256` | `sha256=<hex>` |
| Gitea | `X-Gitea-Signature` | `<hex>` |

`--webhook-secret` 미설정 시 서명 검증을 건너뜁니다.

## Graceful Shutdown

SIGINT/SIGTERM 수신 시:

1. **HTTP 서버 종료** — 새 요청 수신 중단 (5초 타임아웃)
2. **sync context cancel** — 진행 중인 clone/build 작업에 `context.Done()` 전파
3. **worker drain** — SyncQueue 워커 종료 대기 (30초 타임아웃)

진행 중인 clone/build가 context cancel을 받으면 즉시 중단되므로, shutdown 대기 시간이 최소화됩니다.

## Pipeline

```
Push Event → HMAC Verify → RepoFilter.IsAllowedRef()
  → SyncQueue.Add() (dedup) → Worker
    → CloneOrPull (ctx, 10min timeout)
    → GraphService.Build (ctx, 10min timeout)
    → DB 저장
```

### Deduplication

동일 레포에 대한 연속 push는 SyncQueue에서 자동 병합됩니다:
- 처리 중인 레포에 새 push → dirty 플래그 → 완료 후 최신 payload로 재처리
- 대기열의 동일 레포 → payload만 최신으로 업데이트 (중복 enqueue 없음)

### Concurrency

- 기본 4개 워커
- 서로 다른 레포는 병렬 처리
- 같은 레포는 순차 처리 (dirty requeue)

## `.ccg.yaml` include_paths 자동 반영

Webhook 빌드 시 clone된 레포 내 `.ccg.yaml` 파일의 `include_paths` 설정을 자동으로 읽어 빌드 범위를 제한합니다.

```yaml
# 레포 내 .ccg.yaml
include_paths:
  - src/
  - lib/
```

- `.ccg.yaml` 파일이 없거나 `include_paths` 키가 없으면 전체 디렉토리를 빌드합니다
- CLI의 `--config` 플래그와 독립적으로 동작합니다 (viper 미사용, yaml 직접 파싱)

## Panic Recovery

모든 goroutine에 `defer recover()`가 적용되어 있어 개별 워커 panic이 전체 프로세스를 크래시시키지 않습니다:

- Signal handler goroutine
- HTTP server goroutine
- SyncQueue worker goroutine
- SyncQueue shutdown goroutine
