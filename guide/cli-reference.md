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

In MSA environments, you can isolate per-service code graphs in a single database.

```bash
# Build per service
ccg build ./backend --namespace backend
ccg build ./frontend --namespace frontend

# Search within a specific namespace
ccg search --namespace backend "auth"

# Incremental update with namespace
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

Measures token reduction directly without an LLM. Compares token counts between naive (full file read) and CCG graph search, and measures recall simultaneously.

| Command | Description |
|---------|-------------|
| `ccg benchmark token-bench` | Measure token reduction + recall |
| `ccg benchmark token-bench --corpus <path>` | Path to corpus YAML file (default: `testdata/benchmark/queries.yaml`) |
| `ccg benchmark token-bench --repo <dir>` | Repository root for naive token counting (default: `.`) |
| `ccg benchmark token-bench --exts .go,.ts` | Source file extensions to count (default: `.go`) |
| `ccg benchmark token-bench --limit 30` | Total result budget per query — auto-split inversely by term count (default: `30`) |
| `ccg benchmark token-bench --out result.json` | Save results to JSON file |
| `ccg benchmark init` | Generate `testdata/benchmark/queries.yaml` template |
| `ccg benchmark validate --corpus <path>` | Validate corpus YAML |

**Output fields:**

| Field | Description |
|-------|-------------|
| `naive_tokens` | Total token count of all source files (worst-case baseline) |
| `graph_tokens` | Token count of CCG search results (including 1-hop expansion) |
| `ratio` | `naive_tokens / graph_tokens` |
| `recall` | `(files_hit + symbols_hit) / (files_total + symbols_total)` |
| `files_hit` / `files_total` | Number of expected_files found in results |
| `symbols_hit` / `symbols_total` | Number of expected_symbols found in results |
| `search_elapsed_ms` | Search elapsed time (ms) |

**Corpus YAML format:**

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

> **Note:** Only ASCII words from `description` are used for FTS search. `expected_symbols` is used only for recall calculation, not as a search query.

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

Restricts the build target paths. When set, only paths under the specified directories are parsed.

- CLI: `.ccg.yaml`'s `include_paths` is automatically applied during `ccg build`
- Webhook: After cloning a repo, `.ccg.yaml`'s `include_paths` is auto-loaded to limit build scope
- Incremental build (`ccg update`): `include_paths` filter applied when collecting changed files

```yaml
include_paths:
  - src/backend/
  - src/shared/
```

### Regex Patterns

The `exclude` and `rules` pattern fields support regular expressions. Patterns containing `$`, `^`, `+`, `{}`, `|`, `\.`, `.*` are automatically detected as regex:

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

`ccg lint` checks 8 categories:

| Category | Description |
|----------|-------------|
| orphan | Documentation file with no corresponding code |
| missing | Code file with no documentation |
| stale | Documentation not updated after code change |
| unannotated | Function/type without annotation |
| contradiction | Mismatch between code and documentation |
| dead-ref | `@see` tag pointing to a non-existent target |
| incomplete | Incomplete annotation |
| drift | Tag not updated after signature change |

Per-category `action: ignore` can be set in `.ccg.yaml`'s `rules`. In `--strict` mode, `action: ignore` rules are applied.
