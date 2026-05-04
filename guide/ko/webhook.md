# 웹훅 동기화 (Webhook Sync)

[English](../webhook.md)

GitHub 또는 Gitea로부터 push 이벤트를 수신하여 자동으로 복제(clone)/pull 및 코드 그래프 빌드를 수행합니다.

## 설정 (Setup)

```bash
ccg serve --transport streamable-http \
  --allow-repo "org/api:main,develop" \
  --allow-repo "org/web:main" \
  --webhook-secret "your-secret" \
  --repo-clone-base-url https://github.com \
  --repo-root /data/repos
```

로컬 테스트용으로 HMAC 검증 및 정규 복제 URL 재구성을 생략하고 비보안 모드를 사용할 수 있습니다:

```bash
ccg serve --transport streamable-http \
  --allow-repo "org/*" \
  --insecure-webhook \
  --repo-root /data/repos
```

### 엔드포인트 (Endpoints)

| 경로 | 메서드 | 설명 |
|------|--------|-------------|
| `/mcp` | POST | MCP Streamable HTTP |
| `/health` | GET | 헬스 체크 (`{"status":"ok"}`) |
| `/ready` | GET | 준비성(Readiness) 체크 |
| `/status` | GET | 데이터베이스 및 웹훅 큐 상태를 포함한 운영 상태 |
| `/webhook` | POST | 웹훅 수신 (GitHub / Gitea push 이벤트) |

### HTTP 노출 (HTTP Exposure)

신뢰할 수 있는 네트워크 내에서만 HTTP 엔드포인트를 사용하십시오. MCP 엔드포인트는 `--http-bearer-token`으로 보호할 수 있지만, `/health`, `/ready`, `/status`와 같은 운영 엔드포인트는 내부 헬스 체크용이며 런타임 상태를 노출할 수 있습니다. Ingress, 리버스 프록시 또는 로드 밸런서 뒤에 배포할 때는 이러한 엔드포인트를 내부 호출자로 제한하거나 외부 인터넷 접근을 차단하십시오. Ingress 및 준비성 가이드는 [운영 가이드](operations.md#http-exposure)를 참조하십시오.

기본 Streamable HTTP 리슨 주소는 `127.0.0.1:8080`입니다. 루프백이 아닌 주소에 바인딩할 때는 `/mcp`에 대해 `--http-bearer-token`을 설정하거나, 로컬 테스트용으로만 `--insecure-http`를 사용하십시오.

## 저장소별 브랜치 필터링 (Per-Repo Branch Filtering)

`--allow-repo` 플래그를 사용하여 저장소별로 허용되는 브랜치를 설정합니다.

### 형식 (Format)

```
--allow-repo "REPO_PATTERN:BRANCH1,BRANCH2"
```

- `REPO_PATTERN`: glob 패턴 (`path.Match` 사용). 예: `org/*`, `org/api`, `*/*`
- `BRANCH1,BRANCH2`: 허용되는 브랜치 (쉼표로 구분, glob 패턴 지원)
- 브랜치를 지정하지 않을 경우 기본값: `main`, `master`

### 예시

```bash
# org/api에 대해 main 및 develop 브랜치만 허용
--allow-repo "org/api:main,develop"

# org 하위의 모든 저장소, 기본 브랜치(main, master) 허용
--allow-repo "org/*"

# release/* 패턴의 브랜치 허용
--allow-repo "org/api:main,release/*"

# 여러 저장소 설정
--allow-repo "org/api:main,develop" --allow-repo "org/web:main"
```

### 매칭 규칙

1. 나중에 설정된 매칭 규칙이 이전 규칙을 덮어씁니다 (순서가 중요함).
2. 일치하는 규칙이 없으면 거부됩니다.
3. 웹훅 페이로드의 `ref` 필드에서 `refs/heads/` 접두사는 자동으로 제거됩니다.

## 서명 검증 (Signature Verification)

HMAC-SHA256을 사용하여 웹훅 페이로드를 검증합니다.

| 플랫폼 | 헤더 | 형식 |
|----------|--------|--------|
| GitHub | `X-Hub-Signature-256` | `sha256=<hex>` |
| Gitea | `X-Gitea-Signature` | `<hex>` |

기본적으로 `--webhook-secret`이 설정되지 않으면 웹훅 요청은 실패(fail closed) 처리됩니다.

- `--webhook-secret`은 HMAC 검증을 활성화합니다.
- `--insecure-webhook`은 명시적인 테스트 전용 옵션이며 `--webhook-secret`과 함께 사용할 수 없습니다.
- 보안 모드에서 실행할 때는 `--repo-clone-base-url`이 필수이며, 서버는 웹훅 페이로드의 `clone_url`을 신뢰하는 대신 허용된 저장소 이름을 기반으로 복제 URL을 재구성합니다.

## 정상 종료 (Graceful Shutdown)

SIGINT/SIGTERM 수신 시:

1. **HTTP 서버 종료** — 새로운 요청 수락 중단 (5초 타임아웃)
2. **동기화 컨텍스트 취소** — 진행 중인 복제/빌드 작업에 `context.Done()` 전달
3. **워커 드레인(drain)** — SyncQueue 워커가 종료될 때까지 대기 (30초 타임아웃)

진행 중인 복제/빌드 작업은 컨텍스트 취소를 수신하고 즉시 중단되어 종료 대기 시간을 최소화합니다.

## 파이프라인 (Pipeline)

```
Push 이벤트 → HMAC 검증 → RepoFilter.IsAllowedRef()
  → SyncQueue.Add() (중복 제거) → 워커
    → CloneOrPull (ctx, 15분 타임아웃)
    → GraphService.Update (증분 빌드, ctx, 15분 타임아웃)
    → DB 저장
```

## 트레이싱 (Tracing)

CCG를 Streamable HTTP로 실행하면 exporter가 설정되지 않은 경우에도 웹훅 경로에서 실제 OpenTelemetry SDK span을 생성합니다. `/webhook`으로 들어온 `traceparent` 헤더는 server span의 부모가 되며, 같은 trace가 큐 처리, 재시도, clone/pull, 그래프 업데이트 작업까지 이어집니다.

- `--otel-endpoint` 또는 `CCG_OTEL_ENDPOINT` 미설정: span은 프로세스 내부에만 머물고 export되지 않음
- `--otel-endpoint` 설정: `http://collector:4318/v1/traces` 같은 전체 엔드포인트 URL로 OTLP HTTP export 수행
- trace가 연결된 웹훅 로그에는 `trace_id`, `span_id`, `trace_sampled`가 포함됨

따라서 웹훅 페이로드 형식을 바꾸지 않아도 HTTP 수신, 큐잉, 저장소 sync 로그를 하나의 trace로 연관 지어 장애를 추적할 수 있습니다.

### 중복 제거 (Deduplication)

동일한 저장소에 대한 연속적인 push는 SyncQueue에서 자동으로 병합됩니다:
- 저장소가 처리 중일 때 새로운 push 발생 → dirty 플래그 설정 → 완료 후 최신 페이로드로 재처리
- 동일한 저장소가 이미 큐에 대기 중 → 페이로드만 업데이트 (중복 엔큐 방지)

### 동시성 (Concurrency)

- 기본 4개 워커 사용
- SQLite 웹훅 배포는 `--webhook-workers` 또는 `CCG_WEBHOOK_WORKERS`를 명시적으로 설정하지 않으면 기본값으로 1개 워커를 사용합니다.
- 서로 다른 저장소는 병렬로 처리됩니다.
- 동일한 저장소는 순차적으로 처리됩니다 (dirty requeue).
- 팀 단위 또는 상시 가동 웹훅 배포의 경우 PostgreSQL 사용을 권장하며, 큐 대기 시간, 저장소 업데이트 시간 및 데이터베이스 용량에 따라 워커 수를 조정하십시오.
- `--webhook-max-tracked-repos` / `CCG_WEBHOOK_MAX_TRACKED_REPOS`는 큐 메모리를 제한하며, 새로운 저장소가 제한을 초과할 경우 `429`를 반환합니다.

### 재시도 / 백오프 (Retry / Backoff)

복제 또는 빌드 실패 시 지수 백오프(exponential backoff)를 사용하여 자동으로 재시도합니다:

| 설정 | 기본값 | 설명 |
|---------|---------|-------------|
| MaxAttempts | 3 | 최대 시도 횟수 (첫 시도 포함) |
| BaseDelay | 1s | 첫 재시도 전 대기 시간 |
| MaxDelay | 30s | 재시도 대기 시간의 상한선 |

- **시도당 타임아웃**: 복제와 빌드는 단일 15분 컨텍스트를 공유합니다. 합산 시간이 제한을 초과하면 해당 시도는 실패하고 재시도합니다.
- **최대 총 소요 시간**: 3회 시도 × 15분 + 백오프 (최대 ~30초) ≈ **46분**
- 지수적 증가: 1s → 2s → 4s → ... (MaxDelay에서 캡핑)
- 대기 중인 재시도는 컨텍스트 취소(서버 종료) 시 즉시 취소됩니다.
- 패닉(Panic)은 오류로 처리되어 재시도 대상이 됩니다.
- 잘못된 `.ccg.yaml` `include_paths` 설정과 같이 유효하지 않은 저장소 설정은 현재 이벤트에 대해 재시도 불가능한 오류로 처리됩니다.
- MaxAttempts를 초과하면 `ERROR` 로그를 남기고 동기화를 포기합니다 (다음 push 이벤트 시 재시도 가능).

이러한 기본값은 `--webhook-attempt-timeout`, `--webhook-retry-attempts`, `--webhook-retry-base-delay`, `--webhook-retry-max-delay`로 조정할 수 있습니다.

## `.ccg.yaml` include_paths 자동 적용

웹훅 빌드 중에 복제된 저장소 내부의 `.ccg.yaml`에 있는 `include_paths` 설정을 자동으로 읽어 빌드 범위를 제한합니다.

```yaml
# 저장소 내부의 .ccg.yaml
include_paths:
  - src/
  - lib/
```

- `.ccg.yaml`이 없거나 `include_paths` 키가 없는 경우 전체 디렉토리가 빌드됩니다.
- CLI의 `--config` 플래그와 무관하게 작동합니다 (YAML 직접 파싱).

## 파싱 크기 제한 (Parse Size Limits)

웹훅 요청 본문의 크기는 저장소 파싱과는 별도로 제한됩니다. 웹훅 페이로드는 서버에 의해 제한되지만, 이후의 복제/빌드 단계는 기본적으로 소스 파싱 크기 제한이 없습니다. 기본적으로 CCG는 `include_paths`로 범위를 좁히지 않는 한 복제된 저장소의 모든 일치하는 소스 파일을 빌드합니다.

대규모 저장소에 대해 파싱 예산이 필요한 경우, `--max-file-bytes`, `--max-total-parsed-bytes` 또는 일치하는 `.ccg.yaml` 설정을 명시적으로 구성하십시오. CCG는 기본 웹훅 파싱 제한을 두지 않습니다.

## 읽을 수 없는 파일 (Unreadable Files)

기본적으로 웹훅 그래프 업데이트 중에 읽을 수 없는 소스 파일은 로그에 기록되고 건너뜁니다. 이는 저장소에 깨진 심볼릭 링크, 권한이 없는 파일 또는 일시적인 읽기 오류가 포함되어 있어도 동기화의 복원력을 유지하게 해주지만, 해당 이벤트에 대해 부분적인 그래프를 생성할 수 있습니다.

부분 동기화가 허용되지 않는 경우 `--webhook-fail-on-unreadable`을 사용하십시오. 이 플래그를 사용하면 읽을 수 없는 소스 파일이 있을 때 웹훅 동기화 시도가 실패 처리됩니다. 재시도 가능한 실패는 일반적인 재시도/백오프 정책을 따르며 `/status`를 통해 확인할 수 있습니다.

## 운영 신호 (Operational Signals)

트래픽 제어에는 `/ready`를, 진단에는 `/status`를 사용하십시오. 큐가 가득 차거나 중단된 경우 `/ready`가 `not_ready`를 반환할 수 있습니다. 최신 웹훅 동기화가 실패한 경우 인스턴스를 서비스에서 제거하지 않고도 `/status`가 `degraded` 상태를 보고할 수 있습니다.

웹훅 동기화가 활성화된 경우 `/status`에 `webhook` 객체가 포함됩니다. 주요 필드:

| 필드 | 의미 |
|-------|---------|
| `queued`, `processing`, `dirty` | 현재 큐 및 워커 상태 |
| `tracked_repos`, `max_tracked_repos` | 큐 추적 용량; 가득 차면 새로운 저장소 거부됨 |
| `queue_full_total`, `failure_total` | 프로세스 시작 이후 누적 운영 카운터 |
| `oldest_queued_age`, `oldest_processing_age` | 준비성 체크에 사용되는 지연 신호 (나노초 단위 JSON 숫자) |
| `last_error`, `last_error_time`, `last_success_time` | 최근 성공/실패 상태 합계 |
| `recent_repos` | 최근, 대기 중 또는 처리 중인 최대 50개의 저장소 정보 (저장소명, 브랜치, 상태, 마지막 성공/오류 필드 포함) |

합산된 최신 실패가 해결되지 않았거나 최근 저장소 중 해결되지 않은 실패가 있는 경우 `/status`는 `degraded`를 보고합니다. 동일한 저장소에 대해 이후 동기화가 성공하면 해당 저장소의 오류 상태가 해제됩니다.

CCG는 현재 웹훅 운영을 위한 `/metrics` 엔드포인트를 제공하지 않습니다. `/status`를 주요 구조화된 런타임 뷰로 활용하십시오.

배포 프로필, 데이터베이스 선택, 네임스페이스 크기 가이드 및 일반적인 실패 모드에 대해서는 [운영(Operations)](operations.md)을 참조하십시오.

## 패닉 복구 (Panic Recovery)

개별 워커의 패닉이 전체 프로세스를 중단시키지 않도록 모든 고루틴에 `defer recover()`가 적용됩니다:

- 시그널 핸들러 고루틴
- HTTP 서버 고루틴
- SyncQueue 워커 고루틴
- SyncQueue 종료 고루틴
