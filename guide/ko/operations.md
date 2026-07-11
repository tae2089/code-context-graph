# 운영 (Operations)

[English](../operations.md)

이 가이드는 CCG가 로컬 도구에서 공유 서비스로 전환될 때 놓치기 쉬운 배포 및 운영 결정 사항들을 정리합니다.

## 배포 프로필 (Deployment Profiles)

| 프로필 | 권장 설정 | 참고 사항 |
|---------|-------------------|-------|
| 로컬 CLI | SQLite, 로컬 `ccg.db`, 수동 `ccg build` / `ccg update` | 데이터베이스를 일회성 캐시로 취급하십시오. |
| 개인용 MCP 서버 | 한 번에 하나의 사용자 및 저장소 업데이트 시 SQLite 허용 | 데이터베이스를 네트워크 볼륨이 아닌 로컬 디스크에 보관하십시오. |
| 팀용 MCP 서버 | PostgreSQL, 명시적 `ccg migrate`, HTTP Bearer 토큰 설정 | 백업/복구 및 연결 제한을 계획하십시오. |
| 웹훅 서비스 | PostgreSQL 권장, 저장소 허용 목록(allowlist), HMAC 웹훅 비밀키 | 운영 엔드포인트는 내부용으로 유지하십시오. |
| 다중 저장소 네임스페이스 서비스 | PostgreSQL, 저장소/서비스당 하나의 네임스페이스 | 네임스페이스 크기 및 사후 처리(postprocess) 비용을 모니터링하십시오. |

## Database Choice

다음 조건이 모두 충족될 때 SQLite를 사용하십시오:

- 한 명의 개발자 또는 하나의 로컬 자동화 프로세스가 데이터베이스를 소유함
- 하나의 소규모 또는 중규모 저장소가 인덱싱됨
- 로컬 캐시가 오래되었을 때 재빌드가 허용됨
- 웹훅 워커 풀이나 공유 HTTP 서비스가 없음

다음 중 하나라도 해당되면 PostgreSQL을 사용하십시오:

- CCG가 팀 또는 다수의 MCP 클라이언트에 제공됨
- 웹훅 동기화가 활성화됨
- 여러 저장소 또는 네임스페이스가 하나의 데이터베이스를 공유함
- 백업, 복구, 모니터링, 원격 접속 또는 제어된 마이그레이션이 중요함
- 네임스페이스에 약 5만 개 이상의 검색 문서 또는 10만 개 이상의 그래프 노드가 있음

그래프 노드가 30만 개 이상이거나, 항상 동기화되는 여러 저장소가 있거나, 빈번한 웹훅 업데이트가 발생하는 경우 PostgreSQL을 기본으로 사용해야 합니다. SQLite는 읽기 위주의 로컬 용도로는 여전히 작동할 수 있지만, 쓰기 직렬화 및 FTS 유지 관리가 운영상의 병목 현상이 됩니다.

## 네임스페이스 크기 (Namespace Size)

네임스페이스 크기는 쿼리 라우팅보다 사후 처리에 더 많은 영향을 미칩니다. 검색 및 커뮤니티 재생성은 네임스페이스 내부에서 작동하므로, 네임스페이스가 매우 크면 하나의 저장소만 변경되었더라도 업데이트가 느려집니다. 저장된 흐름(flow)은 `postprocess=full`을 포함한 `build_or_update_graph` 또는 `flows=true`를 포함한 `run_postprocess`를 통해 대량으로 재생성할 수 있습니다. `trace_flow`는 여전히 진입점별 쿼리 도구로 남습니다.

실무 지침:

- 서비스 간 그래프 쿼리가 주요 사용 사례가 아닌 한, 네임스페이스당 하나의 저장소 또는 서비스를 유지하십시오.
- 저장소의 일부만 에이전트에게 유용한 경우 `include_paths`를 사용하십시오.
- 네임스페이스가 약 5만 개의 검색 문서 또는 10만 개의 그래프 노드에 도달하면 PostgreSQL을 선호하십시오.
- 사후 처리 시간이나 웹훅 큐 대기 시간이 가끔 발생하는 큰 업데이트가 아니라 일상적인 운영상의 우려 사항이 되면 네임스페이스를 분리하십시오.

증분 업데이트는 영향을 받는 검색 문서와 FTS 행만 재생성합니다. 전체 빌드, 명시적인 `run_postprocess` 및 커뮤니티 재생성은 여전히 네임스페이스 전체에 걸쳐 이루어질 수 있으므로, 네임스페이스 경계가 주요 비용 제어 수단으로 남습니다.

## MCP 응답 예산 (MCP Response Budgets)

큰 네임스페이스는 명시적인 응답 예산으로 조회해야 합니다. 주요 그래프 탐색
도구는 페이지네이션을 제공하며 `has_more`를 반환합니다. `has_more`가
`true`이면 같은 요청을 `next_offset`으로 다시 호출하십시오.

에이전트 대상 쿼리에서는 다음 파라미터를 기본 운영 표면으로 사용하십시오:

| 도구 | 예산 파라미터 |
|------|-------------------|
| `query_graph` | `limit`, `offset` |
| `list_flows` | `limit`, `offset` |

페이지네이션 가능한 그래프 도구의 최대 페이지 크기는 500입니다. 호출자가 LLM
에이전트라면 50 또는 100처럼 더 작은 페이지부터 시작하십시오. 이렇게 하면
응답이 검토 가능해지고 컨텍스트 오염을 줄일 수 있습니다.

사용 전에 범위를 좁혀야 하는 고용량 표면:

- onboarding 같은 MCP prompt는 넓은 프로젝트 상태를 요약하므로 네임스페이스로 그래프를 좁힌 뒤 사용하는 것이 좋습니다.

공유 서비스에서는 광범위한 분석 요청보다 경로 필터, 네임스페이스 분리,
페이지네이션 도구를 우선하십시오. 예상보다 큰 도구 응답은 네임스페이스가 너무
넓거나 호출자가 더 좁은 첫 질문을 해야 한다는 운영 신호로 취급하십시오.

## 호출 해상도 오염 관리 (Call Resolution Hygiene)

CCG는 호출 엣지를 다음처럼 구분해 저장합니다.

- `calls`: 엄격하고 결정론적인 해상도 결과
- `fallback_calls`: 엄격 해상도가 모호한 경우에만 사용되는 best-effort 결과

`fallback_calls`는 커버리지는 늘리지만 오탐(과적합) 위험도 같이 높일 수 있으므로,
기본 모드가 아니라 품질 관리 신호로 운영해야 합니다.

### 운영 권장 정책

1. **기본은 strict 모드**
   - `ccg build`, `ccg update`는 기본적으로 `--fallback-calls` 없이 실행합니다.
   - CI, strict 검사, 서비스 운영에서는 이 모드를 기본으로 사용합니다.

2. **Fallback는 opt-in**
   - `--fallback-calls`는 통제된 복구 실행에서만 켭니다.
   - 전형적인 사용 사례는 초기 마이그레이션/부트스트랩이나 특정 언어·레포에서
     해상도 품질이 일시적으로 떨어지는 경우입니다.

3. **엄격 검사는 분리**
   - `--strict` lint 및 검증 게이트에서 fallback을 켜지 않습니다.
   - 호출 기반 쿼리 기능에서 fallback를 사용할지 여부를 워크플로우별로 분리해 사용합니다.

4. **오버핏 비율 게이팅**
   - 네임스페이스별로 주기적으로 아래 SQL로 비율을 확인합니다.

     ```sql
     SELECT namespace,
       SUM(CASE WHEN kind='calls' THEN 1 ELSE 0 END) AS calls_count,
       SUM(CASE WHEN kind='fallback_calls' THEN 1 ELSE 0 END) AS fallback_count
     FROM edges
     WHERE namespace = '...'
     GROUP BY namespace;
     ```

   - `fallback_count / (calls_count + fallback_count)`가 낮은 임계치(5~10% 정도)에서
     벗어나면 경고로 간주해 원인 분석을 시작합니다.
   - 고률이 반복되면(20%+) 운영 모드에서 fallback 비활성화 후 해상도 규칙 자체를 개선합니다.

5. **롤백 규칙**
   - fallback 실행 후 품질 저하가 확인되면 즉시 strict 모드로 되돌리고
     같은 네임스페이스에서 비율을 다시 점검합니다.

이 정책은 fallback를 정적 기본값이 아니라, 과적합을 제한한 임시 보정 수단으로
운영하기 위한 기준입니다.

## HTTP Exposure

Streamable HTTP MCP 엔드포인트는 외부에서 접근 가능할 때마다 `--http-bearer-token` 또는 `CCG_HTTP_BEARER_TOKEN`으로 보호되어야 합니다.

기본적으로 `ccg-server`는 `127.0.0.1:8080`에서 리슨합니다. 루프백이 아닌 주소에 바인딩하려면 `--http-bearer-token`을 설정하거나 명시적인 테스트용 옵션인 `--insecure-http`를 사용해야 합니다. Bearer 토큰 보호는 `/mcp`와 `/wiki/api/*`에 적용됩니다. `/health`, `/ready`, `/status`, `/wiki`, `/webhook`은 여전히 네트워크 레벨의 노출 제어가 필요합니다. `/wiki`는 정적 app shell만 제공하지만 의도적으로 노출해야 합니다.

다음 엔드포인트는 내부용으로 유지하십시오:

| 엔드포인트 | 노출 지침 |
|----------|-------------------|
| `/health` | 내부 Liveness Probe 전용 |
| `/ready` | 내부 Readiness Probe 전용 |
| `/status` | 내부 운영 진단 전용 |
| `/webhook` | HMAC 검증 및 저장소 허용 목록이 설정된 경우에만 공개 |
| `/mcp` | Bearer 인증 및 네트워크 제어 하에 노출 가능 |
| `/wiki` | 정적 Wiki app shell을 공개해야 하는 경우에만 공개 |
| `/wiki/api/*` | Bearer 인증 및 네트워크 제어 하에 노출 가능 |

CCG가 Ingress, 리버스 프록시 또는 로드 밸런서 뒤에 있는 경우, 공용 인터넷에서 `/health`, `/ready`, `/status`에 접근하지 못하도록 차단하십시오. 이러한 엔드포인트는 운영상 유용하지만 공개 API로 설계되지 않은 런타임 상태를 노출할 수 있습니다.

## 트레이싱 및 로그 상관관계 (Tracing and Log Correlation)

이제 CCG의 serve 모드는 inbound MCP/웹훅 요청과 그 뒤의 웹훅 sync 파이프라인에 대해 실제 OpenTelemetry SDK span을 생성합니다. 이 동작은 exporter가 없어도 활성화됩니다.

- 로컬 trace 연계 로그만 필요하다면 `--otel-endpoint` / `CCG_OTEL_ENDPOINT`를 비워 두십시오.
- collector로 span을 내보내려면 `--otel-endpoint`에 `http://collector:4318/v1/traces` 같은 전체 OTLP HTTP URL을 설정하십시오.
- trace가 연결된 컨텍스트에서 출력되는 로그에는 `trace_id`, `span_id`, `trace_sampled`가 포함됩니다.

운영 관점에서는 두 가지 모드로 생각하면 됩니다.

1. **로컬 전용 트레이싱** — 기본값. OTel collector 없이 단일 CCG 프로세스를 디버깅할 때 유용합니다.
2. **export 트레이싱** — opt-in. Langfuse, Jaeger, Tempo 또는 다른 OTLP 백엔드에서 서비스 간 trace 검색이 필요한 상시 MCP/웹훅 서비스에 적합합니다.

웹훅 sync span은 HTTP 요청이 반환된 뒤에도 이어지므로, 하나의 trace로 요청 수신, 큐 처리, clone/pull, 그래프 업데이트 작업까지 추적할 수 있습니다.

## 웹훅 운영 (Webhook Operations)

웹훅 배포는 다음 사항들을 설정해야 합니다:

- 명시적인 저장소 및 브랜치 허용 목록을 위한 `--allow-repo`
- HMAC 검증을 위한 `--webhook-secret`
- 복제 URL이 페이로드로부터 신뢰받는 대신 허용된 저장소 이름으로부터 재구성되도록 하는 `--repo-clone-base-url`
- 지속성 있는 로컬 스토리지를 위한 `--repo-root`
- 팀용 또는 상시 가동 배포를 위한 `--db-driver postgres`

웹훅 네임스페이스 추출은 마지막 저장소 이름을 사용합니다. 예를 들어 `acme/api`는 `api` 네임스페이스에 저장되고 `$REPO_ROOT/api`에 checkout됩니다. 이 전략은 단일 owner 웹훅 배포를 위한 것입니다. 허용 목록이 여러 owner를 포함하면 서버는 경고를 남깁니다. `acme/api`와 `external/api`가 같은 네임스페이스 및 checkout 경로에서 충돌할 수 있기 때문입니다.

권장 정책:

- 웹훅 CCG 인스턴스 하나에는 하나의 조직/owner를 사용하십시오.
- 인스턴스 안에서 마지막 저장소 이름이 중복되지 않게 유지하십시오.
- 서로 다른 조직에 같은 repo 이름이 있을 수 있으면 CCG 인스턴스를 분리하십시오.
- `*/*`는 일반 운영 allowlist가 아니라 개발 또는 격리된 환경에서만 신중히 사용하십시오.

SQLite 웹훅 배포는 `--webhook-workers` 또는 `CCG_WEBHOOK_WORKERS`가 명시적으로 설정되지 않으면 기본적으로 하나의 워커를 사용합니다. 이는 동일한 SQLite 데이터베이스에 대해 여러 동시 쓰기 작업이 발생하는 것을 방지하기 위함입니다. PostgreSQL의 경우, 저장소 업데이트 시간, 데이터베이스 용량 및 허용 가능한 큐 대기 시간에 따라 워커 수를 조정해야 합니다.

큐 메모리를 제한하려면 `--webhook-max-tracked-repos`를 사용하십시오. 큐가 가득 차면 새로운 저장소 요청은 `429 Too Many Requests`로 거부됩니다. 이러한 현상이 반복되면 규모 확장이나 범위 조정 문제로 취급해야 합니다.

웹훅 요청 본문 크기는 소스 파싱 크기와 별개입니다. 웹훅 페이로드는 작으며 HTTP 수신기에 의해 제한되지만, 복제/빌드 단계는 기본적으로 소스 파싱 예산이 없습니다. 대규모 저장소에 명시적인 파싱 예산이 필요한 경우 `include_paths`, `--max-file-bytes`, 또는 `--max-total-parsed-bytes`를 사용하십시오.

웹훅 그래프 업데이트 중에 읽을 수 없는 소스 파일은 기본적으로 로그에 기록되고 건너뜁니다. 부분적인 그래프가 허용되지 않고 동기화가 실패/재시도되어야 하는 경우 `--webhook-fail-on-unreadable`을 활성화하십시오.

잘못된 `.ccg.yaml` `include_paths`와 같은 유효하지 않은 저장소 설정은 현재 이벤트에 대해 재시도 불가능한 것으로 처리됩니다. 저장소 설정을 수정하고 다시 push하여 새로운 동기화를 트리거하십시오.

## 준비성 및 상태 (Readiness and Status)

`/ready`는 트래픽 제어용입니다. 데이터베이스를 사용할 수 없거나 웹훅 큐가 가득 찼거나 가장 오래된 항목이 지연되는 등 서비스 운영이 차단된 경우 실패해야 합니다.

`/status`는 진단용입니다. 최신 웹훅 동기화가 실패하더라도 큐가 향후 작업을 수락하고 처리할 수 있다면 `/ready`는 준비 상태를 유지하면서 `/status`가 `degraded`를 보고할 수 있습니다. `degraded`를 인스턴스를 서비스에서 제거해야 하는 신호가 아니라 운영자의 조치가 필요한 신호로 취급하십시오.

권장 체크 사항:

| 신호 | 의미 | 운영자 조치 |
|--------|---------|-----------------|
| `/ready`가 `not_ready` 반환 | DB 사용 불가, 큐 가득 참, 또는 차단 중인 큐 대기 시간 | 트래픽 전송을 중단하고 로그/상태를 조사하십시오. |
| `/status`가 `degraded` | 마지막 웹훅 상태 확인 필요 | 실패한 저장소/설정을 수정하고 새로운 push 또는 수동 업데이트로 재시도하십시오. |
| 큐 대기 시간이 꾸준히 증가함 | 워커가 유입되는 push를 감당하지 못함 | 저장소 범위를 줄이거나, PostgreSQL에서 워커를 늘리거나, 네임스페이스를 분리하십시오. |
| 검색 결과가 오래된 것 같음 | 검색 사후 처리가 실패했거나 건너뛰어졌을 수 있음 | `fts=true`로 `run_postprocess`를 실행하거나 네임스페이스를 재빌드/업데이트하십시오. |

알림(alerting)의 경우 다음 `/status.webhook` 필드들을 권장합니다:

| 필드 | 알림 용도 |
|-------|-----------|
| `oldest_queued_age` | 큐 지연 및 워커 용량 압박 (나노초 단위 JSON 숫자) |
| `oldest_processing_age` | 중단된 복제/업데이트 감지 (나노초 단위 JSON 숫자) |
| `queue_full_total` | 프로세스 시작 이후 용량 제한 도달 횟수 |
| `failure_total` | 프로세스 시작 이후 동기화 실패율 |
| `recent_repos[].last_error` | 저장소별 해결되지 않은 실패 |
| `recent_repos[].queued` / `processing` | 현재 대기 중이거나 실행 중인 저장소 확인 |

## 타임아웃 및 종료 (Timeouts and Shutdown)

웹훅 복제 및 빌드는 시도당 15분의 타임아웃을 공유합니다. 재시도는 지수 백오프를 사용하며 기본적으로 3회 시도하므로, 단일 이벤트에 대한 최대 소요 시간은 포기 전까지 약 46분입니다.

SIGINT/SIGTERM 수신 시:

1. HTTP는 `--webhook-shutdown-timeout`(기본 30초)의 종료 윈도우와 함께 새로운 요청 수락을 중단합니다.
2. 동기화 컨텍스트가 취소되고 진행 중인 복제/빌드 작업이 `context.Done()`을 감지합니다.
3. SyncQueue 워커는 드레인(drain)을 위해 최대 `--webhook-shutdown-timeout`의 시간을 갖습니다.

큐 종료가 시작된 뒤 들어온 웹훅 delivery는 `503 Service Unavailable`을 반환하므로 GitHub/Gitea가 이벤트를 성공 처리하지 않고 재시도할 수 있습니다. 종료 직전에 수락된 요청은 프로세스 종료로 취소될 수 있으므로, 새 push 또는 수동 네임스페이스 업데이트로 복구하십시오.

수동 복구:

```bash
ccg update /data/repos/api --namespace api
ccg status --namespace api
```

검색, 커뮤니티, 저장된 flow가 여전히 오래된 것처럼 보이면 namespace `api`에 대해 필요한 postprocess 플래그와 함께 MCP `run_postprocess` 도구를 호출하십시오.

Streamable HTTP 서버는 MCP 스트림이 장시간 유지될 수 있으므로 고정된 `WriteTimeout`을 사용하지 않습니다. 서비스가 인터넷에 노출된 경우 리버스 프록시에서 유휴 연결 제한 및 요청 버퍼링 정책을 설정하십시오.

기타 HTTP 서버 타임아웃은 현재 바이너리에 고정되어 있습니다: `ReadHeaderTimeout`은 10초, `ReadTimeout`은 30초, `IdleTimeout`은 120초입니다.

CCG는 현재 Prometheus 형식의 `/metrics` 엔드포인트를 제공하지 않습니다. 운영 프로브에는 `/health`, `/ready`, `/status`를 사용하고, 임시 성능 측정치는 라이브 서비스 메트릭이 아닌 오프라인 분석 결과로 취급하십시오.

## 마이그레이션 (Migrations)

기본 로컬 SQLite 데이터베이스(`ccg.db`)는 스키마가 없는 경우에만 자동 마이그레이션을 수행합니다. 기존 SQLite 스키마, PostgreSQL, 커스텀 SQLite DSN 및 제어된 업그레이드에는 명시적인 마이그레이션이 필요합니다:

```bash
ccg migrate --db-driver postgres --db-dsn "$CCG_DB_DSN"
```

PostgreSQL의 경우 마이그레이션을 별도의 배포 단계로 실행하십시오. 공유 서비스 데이터베이스를 업그레이드하기 위해 애플리케이션 시작에 의존하지 마십시오.

CCG를 업그레이드한 후, 기존의 기본 `ccg.db` 또한 기존 스키마로 취급하고 런타임 명령어를 재시작하기 전에 명시적으로 마이그레이션해야 합니다.

## 문제 해결 (Troubleshooting)

| 증상 | 가능성 높은 원인 | 확인 / 해결 방법 |
|---------|--------------|-------------|
| `401` 또는 MCP 초기화 실패 | Bearer 토큰 누락 또는 오류 | `Authorization: Bearer ...` 및 `CCG_HTTP_BEARER_TOKEN`을 확인하십시오. |
| 웹훅이 권한 없음(unauthorized) 반환 | HMAC 서명 누락/유효하지 않음 | `--webhook-secret` 및 제공자의 서명 헤더를 확인하십시오. |
| 웹훅이 금지됨(forbidden) 반환 | 허용되지 않은 저장소 또는 브랜치 | `--allow-repo` 패턴 및 브랜치 ref를 확인하십시오. |
| 웹훅이 너무 많은 요청 반환 | 동기화 큐가 가득 참 | `/status`를 확인하고, push 볼륨을 줄이거나 PostgreSQL에서 워커를 늘리십시오. |
| `/ready`가 `not_ready`임 | DB 또는 큐 차단 조건 | `/status` 및 서비스 로그를 조사하십시오. |
| `/status`가 `degraded`임 | 마지막 웹훅 실패 | 저장소 설정을 수정하거나 업데이트/사후 처리를 재실행하십시오. |
| 검색에서 최근 코드가 누락됨 | FTS/검색 문서가 오래됨 | `fts=true`로 `run_postprocess`를 실행하거나 네임스페이스를 재빌드하십시오. |
| 시작 시 마이그레이션 오류 | 스키마 버전 불일치, 마이그레이션 소스 불일치, 또는 스키마 drift | 배포된 바이너리 버전에서 `ccg migrate`를 실행하십시오. 여전히 실패하면 설정된 마이그레이션 소스와 스키마 drift를 확인하십시오. |
