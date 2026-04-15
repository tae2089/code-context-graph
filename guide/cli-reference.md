# CLI Reference

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

docs:
  out: docs
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
