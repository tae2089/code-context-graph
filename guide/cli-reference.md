# CLI Reference

## Global Flags

| Flag | Description |
|------|-------------|
| `--namespace <name>` | Namespace for data isolation (e.g. `--namespace backend`) |
| `--db-driver <driver>` | Database driver: `sqlite`, `postgres` (default `sqlite`) |
| `--db-dsn <dsn>` | Database connection string (default `ccg.db`) |
| `--log-level <level>` | Log level: `debug`, `info`, `warn`, `error` (default `info`) |
| `--log-json` | Output logs in JSON format |
| `--config <path>` | Config file path (default: `.ccg.yaml` in `./` then `~/.config/ccg/`) |

### Namespace

MSA 환경에서 서비스별 코드 그래프를 하나의 DB에 격리 저장할 수 있습니다.

```bash
# 서비스별 빌드
ccg build ./backend --namespace backend
ccg build ./frontend --namespace frontend

# 특정 namespace 내에서만 검색
ccg search --namespace backend "auth"

# 증분 업데이트도 namespace 적용
ccg update ./backend --namespace backend
```

## Commands

| Command | Description |
|---------|-------------|
| `ccg init` | Generate default `.ccg.yaml` in current directory |
| `ccg init --project` | Generate `.ccg.yaml` in current directory (explicit) |
| `ccg init --user` | Generate `.ccg.yaml` in `~/.config/ccg/` (global) |
| `ccg build [dir]` | Parse and build code graph |
| `ccg build --exclude <pat>` | Exclude files/paths (repeatable) |
| `ccg build --no-recursive [dir]` | Only parse top-level directory |
| `ccg update [dir]` | Incremental sync |
| `ccg status` | Graph statistics |
| `ccg search <query>` | Full-text search |
| `ccg search --path <prefix> <query>` | Scoped search by path prefix |
| `ccg docs [--out dir]` | Generate Markdown documentation |
| `ccg index [--out dir]` | Regenerate `index.md` only |
| `ccg languages` | List supported languages and extensions |
| `ccg example [language]` | Show annotation writing example |
| `ccg tags` | Show all annotation tag reference |
| `ccg hooks install` | Install pre-commit git hook |
| `ccg hooks install --lint-strict` | Install hook that blocks commit on issues |
| `ccg lint [--out dir]` | 8-category docs lint |
| `ccg lint --strict` | Exit 1 on issues (for CI/pre-commit) |
| `ccg version` | Print build version, commit, date |
| `ccg benchmark token-bench` | Measure token reduction: naive vs graph search (no LLM) |

### Serve

| Command | Description |
|---------|-------------|
| `ccg serve` | Start MCP server (stdio by default) |
| `ccg serve --transport streamable-http` | Start MCP server over HTTP |
| `ccg serve --http-addr :9090` | Custom HTTP listen address (default `:8080`) |
| `ccg serve --stateless` | Stateless session mode (multi-instance deployments) |
| `ccg serve --workspace-root <dir>` | Root directory for file workspaces (default `workspaces`) |
| `ccg serve --allow-repo <pat>` | Allowed repo patterns for webhook sync (e.g. `org/*`, `org/api:main,develop`) |
| `ccg serve --webhook-secret <s>` | HMAC secret for webhook signature verification |
| `ccg serve --repo-root <dir>` | Root directory for cloned repositories |

### Benchmark

토큰 절감 효과를 LLM 없이 직접 측정합니다. naive(전체 파일 읽기) 대비 CCG 그래프 검색의 토큰 수를 비교하고 정답률(recall)을 함께 측정합니다.

| Command | Description |
|---------|-------------|
| `ccg benchmark token-bench` | 토큰 절감 + recall 측정 |
| `ccg benchmark token-bench --corpus <path>` | corpus YAML 파일 경로 (기본: `testdata/benchmark/queries.yaml`) |
| `ccg benchmark token-bench --repo <dir>` | naive 토큰 계산 대상 레포 루트 (기본: `.`) |
| `ccg benchmark token-bench --exts .go,.ts` | 집계할 소스 파일 확장자 (기본: `.go`) |
| `ccg benchmark token-bench --limit 30` | 쿼리당 총 결과 예산 — 단어 수에 반비례해 단어당 limit 자동 조정 (기본: `30`) |
| `ccg benchmark token-bench --out result.json` | 결과를 JSON 파일로 저장 |
| `ccg benchmark init` | `testdata/benchmark/queries.yaml` 템플릿 생성 |
| `ccg benchmark validate --corpus <path>` | corpus YAML 유효성 검사 |

**출력 필드:**

| 필드 | 설명 |
|------|------|
| `naive_tokens` | 전체 소스 파일 토큰 합계 (worst-case baseline) |
| `graph_tokens` | CCG 검색 결과 토큰 수 (1-hop 확장 포함) |
| `ratio` | `naive_tokens / graph_tokens` |
| `recall` | `(files_hit + symbols_hit) / (files_total + symbols_total)` |
| `files_hit` / `files_total` | expected_files 중 결과에 포함된 수 |
| `symbols_hit` / `symbols_total` | expected_symbols 중 결과에 포함된 수 |
| `search_elapsed_ms` | 검색 소요 시간 (ms) |

**corpus YAML 형식:**

```yaml
version: "1"
queries:
  - id: router-01
    description: "HTTP 라우터 트리 구조와 경로 등록 방식"
    expected_files:
      - gin.go
      - tree.go
    expected_symbols:
      - Engine
      - addRoute
    difficulty: hard
```

> **참고:** `description`의 ASCII 단어만 FTS 검색에 사용됩니다. `expected_symbols`는 검색 쿼리가 아닌 recall 계산에만 사용됩니다.

### Eval

| Command | Description |
|---------|-------------|
| `ccg eval` | Evaluate parser accuracy and search quality against golden corpus |
| `ccg eval --suite parser` | Run parser evaluation only |
| `ccg eval --suite search` | Run search evaluation only |
| `ccg eval --update` | Update golden files from current parser output |
| `ccg eval --corpus <dir>` | Golden corpus directory (default `testdata/eval`) |
| `ccg eval --format json` | Output in JSON format (default `table`) |

## Config File (`.ccg.yaml`)

Project-level defaults loaded automatically from the current directory, with a global fallback at `~/.config/ccg/.ccg.yaml`.

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

빌드 대상 경로를 제한합니다. 설정 시 지정된 경로 하위만 파싱됩니다.

- CLI: `ccg build` 시 `.ccg.yaml`의 `include_paths` 자동 적용
- Webhook: 레포 clone 후 `.ccg.yaml`의 `include_paths`를 자동 로딩하여 빌드 범위 제한
- 증분 빌드(`ccg update`): 변경 파일 수집 시 `include_paths` 필터 적용

```yaml
include_paths:
  - src/backend/
  - src/shared/
```

### Regex Patterns

`exclude`와 `rules` 패턴 필드는 정규표현식을 지원합니다. `$`, `^`, `+`, `{}`, `|`, `\.`, `.*` 를 포함하는 패턴은 자동으로 regex로 감지됩니다:

```yaml
rules:
  - pattern: "pkg/store/.*"
    category: unannotated
    action: ignore

  - pattern: ".*_generated\\.go::.*"
    category: incomplete
    action: warn
```

### Config Search Order

1. `./.ccg.yaml` (project-local, highest priority)
2. `~/.config/ccg/.ccg.yaml` (global fallback)

Override with `ccg --config path/to/config.yaml`.

### Lint Categories

`ccg lint`는 8개 카테고리를 검사합니다:

| Category | Description |
|----------|-------------|
| orphan | 코드 없는 문서 파일 |
| missing | 문서 없는 코드 파일 |
| stale | 코드 변경 후 업데이트 안 된 문서 |
| unannotated | 어노테이션 없는 함수/타입 |
| contradiction | 코드와 문서 불일치 |
| dead-ref | 존재하지 않는 대상의 `@see` 태그 |
| incomplete | 불완전한 어노테이션 |
| drift | 시그니처 변경 후 미반영 태그 |

`.ccg.yaml`의 `rules`에서 카테고리별 `action: ignore`로 무시 가능. `--strict` 모드에서는 `action: ignore` 룰이 적용됩니다.
