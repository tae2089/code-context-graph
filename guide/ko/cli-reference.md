# CLI 레퍼런스 (CLI Reference)

[English](../cli-reference.md)

## 전역 플래그 (Global Flags)

| 플래그 | 설명 |
|------|-------------|
| `--namespace <name>` | 데이터 격리를 위한 네임스페이스 (예: `--namespace backend`) |
| `--db-driver <driver>` | 데이터베이스 드라이버: `sqlite`, `postgres` (기본값 `sqlite`) |
| `--db-dsn <dsn>` | 데이터베이스 연결 문자열 (기본값 `ccg.db`; 기본 로컬 SQLite 데이터베이스는 스키마가 없는 경우에만 자동 마이그레이션됨) |
| `--log-level <level>` | 로그 레벨: `debug`, `info`, `warn`, `error` (기본값 `info`) |
| `--log-json` | 로그를 JSON 형식으로 출력 |
| `--config <path>` | 설정 파일 경로 (기본값: `./.ccg.yaml` 확인 후 `~/.config/ccg/` 확인) |

### 네임스페이스 (Namespace)

MSA 환경에서 단일 데이터베이스 내에 서비스별 코드 그래프를 격리하여 관리할 수 있습니다.

```bash
# 서비스별 빌드
ccg build ./backend --namespace backend
ccg build ./frontend --namespace frontend

# 특정 네임스페이스 내에서 검색
ccg search --namespace backend "auth"

# 네임스페이스를 사용한 증분 업데이트
ccg update ./backend --namespace backend
```

## 명령어 (Commands)

| 명령어 | 설명 |
|---------|-------------|
| `ccg init` | 현재 디렉토리에 기본 `.ccg.yaml` 생성 |
| `ccg init --project` | 현재 디렉토리에 `.ccg.yaml` 생성 (명시적) |
| `ccg init --user` | `~/.config/ccg/`에 `.ccg.yaml` 생성 (전역) |
| `ccg migrate` | 데이터베이스 스키마 및 검색 인덱스 마이그레이션 실행 |
| `ccg build [dir]` | 코드 그래프 파싱 및 빌드 |
| `ccg build --exclude <pat>` | 파일/경로 제외 (반복 가능) |
| `ccg build --no-recursive [dir]` | 최상위 디렉토리만 파싱 |
| `ccg update [dir]` | 증분 동기화(Incremental sync) |
| `ccg status` | 그래프 통계 출력 |
| `ccg search <query>` | 전체 텍스트 검색 |
| `ccg search --path <prefix> <query>` | 경로 접두사로 검색 범위 제한 |
| `ccg docs [--out dir]` | 마크다운 문서 생성 (기본적으로 그래프에 없는 generator-managed 문서를 prune) |
| `ccg docs --prune=false` | 기존 generator-managed 문서를 삭제하지 않고 문서만 다시 생성 |
| `ccg docs --exclude <pat>` | 문서 생성 대상에서 파일/경로 제외 (반복 가능) |
| `ccg index [--out dir]` | `index.md`만 재생성 |
| `ccg rag-index [--out dir]` | 생성된 문서와 커뮤니티 구조 기반 RAG 인덱스 생성 |
| `ccg languages` | 지원되는 언어 및 확장자 목록 출력 |
| `ccg example [language]` | 어노테이션 작성 예시 출력 |
| `ccg tags` | 모든 어노테이션 태그 레퍼런스 출력 |
| `ccg hooks install` | pre-commit git 훅 설치 |
| `ccg hooks install --lint-strict` | 문제가 있을 경우 커밋을 차단하는 훅 설치 |
| `ccg lint [--out dir]` | 8가지 카테고리의 문서 린트(lint) 실행 |
| `ccg lint --strict` | 문제 발생 시 종료 코드 1로 종료 (CI/pre-commit용) |
| `ccg version` | 빌드 버전, 커밋, 날짜 출력 |
| `ccg benchmark token-bench` | 토큰 감소율 측정: 일반 방식 vs 그래프 검색 (LLM 미사용) |

기본 로컬 SQLite 데이터베이스(`ccg.db`, `./ccg.db`, `ccg.db`로 끝나는 절대 경로 및 해당 파일에 대한 `file:` DSN 포함)의 경우, 실행 명령어는 스키마가 없는 경우에만 마이그레이션을 자동으로 실행합니다. 기존 SQLite 스키마, PostgreSQL, 커스텀 SQLite DSN 및 제어된 업그레이드에는 명시적인 `ccg migrate`가 필요합니다. 이전 버전의 CCG에서 생성된 기본 `ccg.db`가 이미 있는 경우, 이를 기존 스키마로 취급하고 업그레이드 후 `ccg migrate`를 실행하십시오.

### 데이터베이스 선택 (Database Choice)

데이터베이스가 하나의 소규모 또는 중규모 저장소를 위한 일회성 캐시인 로컬 단일 사용자 워크플로우에는 SQLite를 사용하십시오. CCG를 공유 MCP 또는 웹훅 서비스로 실행하거나, 하나의 서버 데이터베이스에 여러 네임스페이스를 저장하거나, 운영상의 백업/복구가 중요한 경우에는 PostgreSQL을 사용하십시오.

대략적인 규모 가이드로, 네임스페이스가 약 5만 개의 검색 문서 또는 10만 개의 그래프 노드에 도달하면 PostgreSQL을 고려하십시오. 30만 개 이상의 그래프 노드, 항상 동기화되는 여러 저장소, 또는 빈번한 웹훅 업데이트가 발생하는 경우에는 PostgreSQL을 기본으로 권장합니다. 배포 프로필 및 런타임 신호에 대해서는 [운영(Operations)](operations.md#database-choice)을 참조하십시오.

### Serve

| 명령어 | 설명 |
|---------|-------------|
| `ccg serve` | MCP 서버 시작 (기본값 stdio) |
| `ccg serve --transport streamable-http` | HTTP를 통해 MCP 서버 시작 |
| `ccg serve --cache-ttl <dur>` | MCP serve 세션 캐시 TTL (기본값 `5m`; 비활성화하려면 `0` 또는 `--no-cache` 사용) |
| `ccg serve --no-cache` | 메모리 내 MCP serve 세션 캐시 비활성화 |
| `ccg serve --http-addr 0.0.0.0:9090` | 커스텀 HTTP 리슨 주소 (기본값 `127.0.0.1:8080`) |
| `ccg serve --http-bearer-token <token>` | 설정된 경우 `/mcp`에 대한 MCP HTTP 요청에 Bearer 토큰 요구 |
| `ccg serve --insecure-http` | Bearer 토큰 없이 루프백이 아닌 HTTP 바인딩 허용 (테스트 전용) |
| `ccg serve --stateless` | 상태 비저장 세션 모드 (다중 인스턴스 배포용) |
| `ccg serve --namespace-root <dir>` | 파일 네임스페이스의 루트 디렉토리 (기본값 `workspaces`) |
| `ccg serve --workspace-root <dir>` | `--namespace-root`에 대한 사용 중단된 별칭 |
| `ccg serve --allow-repo <pat>` | 웹훅 동기화가 허용된 저장소 패턴 (예: `org/*`, `org/api:main,develop`) |
| `ccg serve --webhook-secret <s>` | 웹훅 서명 검증을 위한 HMAC 비밀키 |
| `ccg serve --insecure-webhook` | 로컬 테스트 전용으로 서명되지 않은 웹훅 요청 허용 |
| `ccg serve --repo-clone-base-url <url>` | 웹훅 복제 대상을 재구성하는 데 사용되는 정규 베이스 URL (반복 가능) |
| `ccg serve --repo-root <dir>` | 복제된 저장소의 루트 디렉토리 |
| `ccg serve --webhook-workers <n>` | 웹훅 동기화 워커 수 (기본값 `4`; SQLite 웹훅 배포는 명시적으로 설정하지 않으면 기본값 `1`) |
| `ccg serve --webhook-max-tracked-repos <n>` | 웹훅 동기화 큐에서 추적하는 최대 저장소 수 (기본값 `1024`) |
| `ccg serve --webhook-attempt-timeout <dur>` | 단일 웹훅 동기화 시도에 대한 타임아웃, 복제/pull 및 그래프 업데이트 포함 (기본값 `15m`) |
| `ccg serve --webhook-retry-attempts <n>` | 대기열 항목당 최대 웹훅 동기화 재시도 횟수 (기본값 `3`) |
| `ccg serve --webhook-retry-base-delay <dur>` | 초기 웹훅 재시도 지연 시간 (기본값 `1s`) |
| `ccg serve --webhook-retry-max-delay <dur>` | 최대 웹훅 재시도 지연 시간 (기본값 `30s`) |
| `ccg serve --webhook-fail-on-unreadable` | 소스 파일을 읽을 수 없을 때 경고하고 건너뛰는 대신 웹훅 동기화 시도를 실패 처리 |
| `ccg serve --max-file-bytes <bytes>` | 파싱된 소스 파일당 허용되는 최대 바이트 수 (`0`은 제한 없음) |
| `ccg serve --max-total-parsed-bytes <bytes>` | 소스 파일 전체에서 파싱된 최대 총 바이트 수 (`0`은 제한 없음) |

웹훅 관련 serve 플래그는 지원되는 경우 일치하는 환경 변수로도 설정할 수 있습니다: `CCG_WEBHOOK_WORKERS`, `CCG_WEBHOOK_MAX_TRACKED_REPOS`, `CCG_WEBHOOK_ATTEMPT_TIMEOUT`, `CCG_WEBHOOK_RETRY_ATTEMPTS`, `CCG_WEBHOOK_RETRY_BASE_DELAY`, `CCG_WEBHOOK_RETRY_MAX_DELAY`, `CCG_REPO_ROOT`.

`CCG_HTTP_BEARER_TOKEN`은 `--http-bearer-token`에 대해서도 지원됩니다. 이 토큰은 `/mcp`의 MCP HTTP 엔드포인트를 보호하지만, `/health`, `/ready`, `/status`, `/webhook` 자체를 비공개로 만들지는 않습니다.

### 벤치마크 (Benchmark)

LLM 없이 토큰 감소율을 직접 측정합니다. 일반 방식(전체 파일 읽기)과 CCG 그래프 검색 간의 토큰 수를 비교하고 동시에 재현율(recall)을 측정합니다.

| 명령어 | 설명 |
|---------|-------------|
| `ccg benchmark token-bench` | 토큰 감소율 + 재현율 측정 |
| `ccg benchmark token-bench --corpus <path>` | 코퍼스 YAML 파일 경로 (기본값: `testdata/benchmark/queries.yaml`) |
| `ccg benchmark token-bench --repo <dir>` | 일반 토큰 카운팅을 위한 저장소 루트 (기본값: `.`) |
| `ccg benchmark token-bench --exts .go,.ts` | 카운트할 소스 파일 확장자 (기본값: `.go`) |
| `ccg benchmark token-bench --limit 30` | 쿼리당 총 결과 예산 — 검색어 수에 따라 반비례하여 자동 분할 (기본값: `30`) |
| `ccg benchmark token-bench --out result.json` | 결과를 JSON 파일로 저장 |
| `ccg benchmark init` | `testdata/benchmark/queries.yaml` 템플릿 생성 |
| `ccg benchmark validate --corpus <path>` | 코퍼스 YAML 검증 |

**출력 필드:**

| 필드 | 설명 |
|-------|-------------|
| `naive_tokens` | 모든 소스 파일의 총 토큰 수 (최악의 경우의 기준선) |
| `graph_tokens` | CCG 검색 결과의 토큰 수 (1-hop 확장 포함) |
| `ratio` | `naive_tokens / graph_tokens` |
| `recall` | `(files_hit + symbols_hit) / (files_total + symbols_total)` |
| `files_hit` / `files_total` | 결과에서 발견된 `expected_files` 수 |
| `symbols_hit` / `symbols_total` | 결과에서 발견된 `expected_symbols` 수 |
| `search_elapsed_ms` | 검색 소요 시간 (ms) |

**코퍼스 YAML 형식:**

```yaml
version: "1"
queries:
  - id: router-01
    description: "HTTP router tree structure and route registration"
    expected_files:
      - gin.go
      - tree.go
    expected_symbols:
      - Engine
      - addRoute
    difficulty: hard
```

> **참고:** `description`에서 추출된 ASCII 단어만 FTS 검색에 사용됩니다. `expected_symbols`는 검색 쿼리가 아닌 재현율 계산에만 사용됩니다.

### Eval

| 명령어 | 설명 |
|---------|-------------|
| `ccg eval` | 골든 코퍼스에 대한 파서 정확도 및 검색 품질 평가 |
| `ccg eval --suite parser` | 파서 평가만 실행 |
| `ccg eval --suite search` | 검색 평가만 실행 |
| `ccg eval --update` | 현재 파서 출력으로 골든 파일 업데이트 |
| `ccg eval --corpus <dir>` | 골든 코퍼스 디렉토리 (기본값 `testdata/eval`) |
| `ccg eval --format json` | JSON 형식으로 출력 (기본값 `table`) |

## 설정 파일 (`.ccg.yaml`)

현재 디렉토리에서 자동으로 로드되는 프로젝트 수준의 기본 설정이며, `~/.config/ccg/.ccg.yaml`에 전역 폴백(fallback)이 있습니다.

```yaml
db:
  driver: sqlite   # sqlite | postgres
  dsn: ccg.db

exclude:
  - vendor
  - ".*\\.pb\\.go$"
  - ".*_test\\.go$"

include_paths:
  - src/
  - lib/

docs:
  out: docs
```

### `include_paths`

빌드 대상 경로를 제한합니다. 설정된 경우 지정된 디렉토리 하위의 경로만 파싱됩니다.

- CLI: `ccg build` 중에 `.ccg.yaml`의 `include_paths`가 자동으로 적용됩니다.
- 웹훅: 저장소를 복제한 후 빌드 범위를 제한하기 위해 `.ccg.yaml`의 `include_paths`를 자동으로 로드합니다.
- 증분 빌드(`ccg update`): 변경된 파일을 수집할 때 `include_paths` 필터가 적용됩니다.

```yaml
include_paths:
  - src/backend/
  - src/shared/
```

### 정규식 패턴 (Regex Patterns)

`exclude` 및 `rules` 패턴 필드는 정규 표현식을 지원합니다. `$`, `^`, `+`, `{}`, `|`, `\.`, `.*`를 포함하는 패턴은 자동으로 정규식으로 감지됩니다.

```yaml
rules:
  - pattern: "pkg/store/.*"
    category: unannotated
    action: ignore

  - pattern: ".*_generated\\.go::.*"
    category: incomplete
    action: warn
```

### 설정 검색 순서

1. `./.ccg.yaml` (프로젝트 로컬, 가장 높은 우선순위)
2. `~/.config/ccg/.ccg.yaml` (전역 폴백)

`ccg --config path/to/config.yaml`을 통해 재정의할 수 있습니다.

### Lint Categories

`ccg lint`는 8가지 카테고리를 체크합니다.

| 카테고리 | 설명 |
|----------|-------------|
| orphan | 대응하는 코드가 없는 문서 파일 |
| missing | 문서가 없는 코드 파일 |
| stale | 코드 변경 후 업데이트되지 않은 문서 |
| unannotated | 어노테이션이 없는 함수/타입 |
| contradiction | 코드와 문서 간의 불일치 |
| dead-ref | 존재하지 않는 대상을 가리키는 `@see` 태그 |
| incomplete | 불완전한 어노테이션 |
| drifted | 코드 변경 후 업데이트되지 않은 어노테이션 |

lint 규칙 매칭에서는 `drifted`와 `drift`를 같은 카테고리로 모두 받을 수 있습니다. 사용자에게 보이는 리포트 이름은 `drifted`이고, 내부 정규화나 generated state에서는 `drift`가 사용될 수 있습니다.

각 카테고리별 규칙, 중복 및 구현 정렬 의미에 대한 자세한 내용은 [Lint 가이드](lint.md)를 참조하십시오.

카테고리별 `action: ignore`는 `.ccg.yaml`의 `rules`에서 설정할 수 있습니다. `--strict` 모드에서는 `action: ignore` 규칙이 적용됩니다.

### Lint Policy vs Generated State

CCG는 이제 사람이 관리하는 린트 정책과 생성된 린트 상태를 분리합니다.

| 경로 | 소유자 | 용도 |
|------|-------|---------|
| `.ccg.yaml` | 사람 | 프로젝트 정책: 제외, 포함 경로, 수동 린트 규칙(`ignore` 등) |
| `.ccg/lint-history.json` | 생성됨 | Twice Rule 연속 발생 카운터 |
| `.ccg/auto-rules.yaml` | 생성됨 | Twice Rule에 의해 기록된 경고 전용 규칙 |

`ccg lint`는 더 이상 생성된 경고 규칙을 `.ccg.yaml`에 추가하지 않습니다. 반복되는 문제는 `.ccg/auto-rules.yaml`에 기록되며, `.ccg.yaml`은 수동 정책 결정을 위한 장소로 남습니다.

이전 버전의 저장소에 이미 `.ccg.yaml` 내부에 생성된 `auto: true` 규칙이 있는 경우, `ccg lint --migrate-auto-rules`를 한 번 실행하여 `.ccg/auto-rules.yaml`로 이동하십시오.
