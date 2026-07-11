# CLI Reference

## Global Flags

| Flag | Description |
|------|-------------|
| `--namespace <name>` | Namespace for data isolation (e.g. `--namespace backend`) |
| `--db-driver <driver>` | Database driver: `sqlite`, `postgres` (default `sqlite`) |
| `--db-dsn <dsn>` | Database connection string (default `ccg.db`; the default local SQLite database auto-migrates only when its schema is missing) |
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
| `ccg migrate` | Run database schema and search index migrations |
| `ccg build [dir]` | Parse and build code graph |
| `ccg build --exclude <pat>` | Exclude files/paths (repeatable) |
| `ccg build --no-recursive [dir]` | Only parse top-level directory |
| `ccg build --fallback-calls` | Enable best-effort fallback call resolution (strict by default) |
| `ccg update [dir]` | Incremental sync |
| `ccg update --fallback-calls` | Enable best-effort fallback call resolution for incremental sync |
| `ccg status` | Graph statistics and postprocess error summary |
| `ccg status --errors` | Include recent postprocess failure details |
| `ccg status --recent <n>` | Number of recent postprocess failures to inspect (default `5`) |
| `ccg search <query>` | Full-text search |
| `ccg search --path <prefix> <query>` | Scoped search by path prefix |
| `ccg docs [--out dir]` | Generate Markdown documentation, the `wiki-index.json` compatibility snapshot, and the default RAG index (prunes stale generator-managed docs by default) |
| `ccg docs --rag=false` | Generate Markdown and the `wiki-index.json` snapshot only, without rebuilding communities or the RAG index |
| `ccg docs --rag-refresh=false` | Rebuild the RAG index from existing community rows instead of refreshing communities |
| `ccg docs --rag-index-dir <dir>` | Override the `doc-index.json` and `wiki-index.json` output directory (default `.ccg` or `rag.index_dir`) |
| `ccg docs --prune=false` | Regenerate docs without deleting older generator-managed files |
| `ccg docs --exclude <pat>` | Exclude files/paths from generated docs (repeatable) |
| `ccg index [--out dir]` | Regenerate `index.md` only |
| `ccg rag-index [--out dir]` | Rebuild RAG index from generated docs and already-computed community structure |
| `ccg languages` | List supported languages and extensions |
| `ccg example [language]` | Show annotation writing example |
| `ccg tags` | Show all annotation tag reference |
| `ccg hooks install` | Install pre-commit git hook |
| `ccg hooks install --lint-strict` | Install hook that blocks commit on issues |
| `ccg lint [--out dir]` | 8-category docs lint |
| `ccg lint --strict` | Exit 1 on issues (for CI/pre-commit) |
| `ccg version` | Print build version, commit, date |
| `ccg benchmark token-bench` | Measure token reduction: naive vs graph search (no LLM) |

For the default local SQLite database (`ccg.db`, including `./ccg.db`, absolute paths ending in `ccg.db`, and `file:` DSNs for that file), runtime commands auto-run migrations only when the schema is missing. Existing SQLite schemas, PostgreSQL, custom SQLite DSNs, and controlled upgrades require an explicit `ccg migrate`. If you already have a default `ccg.db` from an older CCG version, treat it as an existing schema and run `ccg migrate` after upgrading.

### Search and RAG Routing

CCG has two search surfaces with different jobs:

| Use case | Preferred entrypoint |
|----------|----------------------|
| Natural-language code understanding, module exploration, architecture questions | `ccg docs`, then MCP `retrieve_docs`, `get_rag_tree`, `get_doc_content` |
| Exact symbol lookup, callers/callees, imports, bounded graph traversal | MCP `get_node`, `query_graph`, `get_minimal_context` |
| Impact analysis, flow tracing | MCP analysis tools such as `get_impact_radius`, `trace_flow` |
| Focused annotation/keyword candidate search | `ccg search` or MCP `search` |

For coding agents, the recommended natural-language path is:

```bash
ccg build .
ccg docs --out docs
```

`ccg docs` always writes `.ccg/wiki-index.json` as a ccg-server Wiki
compatibility snapshot. The Wiki API prefers the graph database for tree
navigation and search, then uses that snapshot only when DB-backed navigation
is unavailable. The snapshot is built directly from folders, packages, files,
and symbols; it does not depend on community postprocessing. Symbol
nodes carry structured annotation details so the browser Wiki can show params,
returns, rules, side effects, and other tags even when the symbol itself has no
generated Markdown file. Wiki indexes also store hidden annotation search text
so Wiki search can match non-intent tags without returning that hidden text in
normal tree payloads.
The browser Wiki also provides a Graph tab backed by `/wiki/api/graph`; it reads
the namespace's graph nodes and edges directly from the configured database and
opens clicked file/symbol nodes through the same document viewer.
Unless `--rag=false` is set, `ccg docs` also refreshes community structure and
writes the default `.ccg/doc-index.json` compatibility snapshot for manual RAG
index workflows.
Use `--rag-refresh=false` only when you intentionally want to reuse existing
community rows. The standalone `ccg rag-index` command remains available for
manual rebuilds from generated docs and already-computed communities.

Then use MCP `retrieve_docs` to retrieve file-level candidates and bounded
Markdown content with matched fields and graph evidence. Use `get_rag_tree` to expand the
module/community context and `get_doc_content` to read a specific generated doc
directly. `search_docs` and `ccg search` remain useful for quick keyword or
annotation matches, but they should not be treated as the primary answering
surface for broad natural-language questions.

### Database Choice

Use SQLite for local, single-user workflows where the database is a disposable
cache for one small or medium repository. Use PostgreSQL when running CCG as a
shared MCP or webhook service, when storing multiple namespaces in one server
database, or when operational backup/restore matters.

As a rough scale guide, consider PostgreSQL once a namespace reaches about 50k
search documents or 100k graph nodes. For 300k+ graph nodes, multiple
always-synced repositories, or frequent webhook updates, PostgreSQL is the
recommended default. See [Operations](operations.md#database-choice) for
deployment profiles and runtime signals.

### Build/Update Fallback Policy

`--fallback-calls` is intentionally off by default. Use it only when strict call
resolution is known to under-connect code graphs and you need temporary recall
recovery.

- Use fallback for one-off recovery, language-specific tuning, or migration bootstraps.
- Keep strict mode (`--fallback-calls` off) for CI, `--strict` checks, and
  production-serving workflows.
- If fallback is enabled for a long period, monitor `fallback_calls` share by
  namespace and rollback to strict when it grows unexpectedly.

### Serve

| Command | Description |
|---------|-------------|
| `ccg serve` | Start local MCP server over stdio |
| `ccg serve --cache-ttl <dur>` | TTL for MCP serve session cache (default `5m`; use `0` or `--no-cache` to disable) |
| `ccg serve --no-cache` | Disable the in-memory MCP serve session cache |
| `ccg serve --otel-endpoint <url>` | Enable OTLP HTTP trace export to the given full endpoint URL (for example `http://collector:4318/v1/traces`); when unset, CCG still creates SDK spans locally but does not export them |
| `ccg serve --namespace-root <dir>` | Root directory for file namespaces (default `namespaces`) |
| `ccg serve --max-file-bytes <bytes>` | Maximum bytes allowed per parsed source file (`0` disables the limit) |
| `ccg serve --max-total-parsed-bytes <bytes>` | Maximum total bytes parsed across source files (`0` disables the limit) |

HTTP MCP and webhook hosting now live in the dedicated `ccg-server` binary:

| Command | Description |
|---------|-------------|
| `ccg-server --http-addr 0.0.0.0:9090` | Start the self-hosted HTTP server (default `127.0.0.1:8080`) |
| `ccg-server --http-bearer-token <token>` | Require a bearer token for MCP HTTP requests on `/mcp` when set |
| `ccg-server --otel-endpoint <url>` | Enable OTLP HTTP trace export |
| `ccg-server --insecure-http` | Allow non-loopback HTTP binding without a bearer token (testing only) |
| `ccg-server --stateless` | Stateless session mode (multi-instance deployments) |
| `ccg-server --wiki-dir <dir>` | Enable the browser Wiki UI at `/wiki` using a built React dist directory; `/wiki/api/*` uses the same bearer token as `/mcp` |
| `ccg-server --namespace-root <dir>` | Root directory for file namespaces (default `namespaces`) |
| `ccg-server --allow-repo <pat>` | Allowed repo patterns for webhook sync (e.g. `org/*`, `org/api:main,develop`) |
| `ccg-server --webhook-secret <s>` | HMAC secret for webhook signature verification |
| `ccg-server --insecure-webhook` | Allow unsigned webhook requests for local testing only |
| `ccg-server --repo-clone-base-url <url>` | Canonical base URL used to reconstruct webhook clone targets (repeatable) |
| `ccg-server --repo-root <dir>` | Root directory for cloned repositories |
| `ccg-server --webhook-workers <n>` | Number of webhook sync workers (default `4`; SQLite webhook deployments default to `1` unless explicitly set) |
| `ccg-server --webhook-max-tracked-repos <n>` | Maximum repositories tracked by the webhook sync queue (default `1024`) |
| `ccg-server --webhook-attempt-timeout <dur>` | Timeout for one webhook sync attempt, covering clone/pull and graph update (default `15m`) |
| `ccg-server --webhook-retry-attempts <n>` | Maximum webhook sync attempts per queued item (default `3`) |
| `ccg-server --webhook-retry-base-delay <dur>` | Initial webhook retry delay (default `1s`) |
| `ccg-server --webhook-retry-max-delay <dur>` | Maximum webhook retry delay (default `30s`) |
| `ccg-server --webhook-fail-on-unreadable` | Fail webhook sync attempts when source files cannot be read instead of warning and skipping |
| `ccg-server --max-file-bytes <bytes>` | Maximum bytes allowed per parsed source file (`0` disables the limit) |
| `ccg-server --max-total-parsed-bytes <bytes>` | Maximum total bytes parsed across source files (`0` disables the limit) |

Webhook-related server flags can also be configured with matching environment
variables where supported: `CCG_WEBHOOK_WORKERS`,
`CCG_WEBHOOK_MAX_TRACKED_REPOS`, `CCG_WEBHOOK_ATTEMPT_TIMEOUT`,
`CCG_WEBHOOK_RETRY_ATTEMPTS`, `CCG_WEBHOOK_RETRY_BASE_DELAY`,
`CCG_WEBHOOK_RETRY_MAX_DELAY`, and `CCG_REPO_ROOT`.

`CCG_HTTP_BEARER_TOKEN` is also supported for `--http-bearer-token`, and `CCG_OTEL_ENDPOINT` is supported for `--otel-endpoint`.
This token
protects the MCP HTTP endpoint on `/mcp`; it does not make `/health`, `/ready`,
`/status`, or `/webhook` private by itself.

When `--otel-endpoint` or `CCG_OTEL_ENDPOINT` is unset, CCG still creates real
OpenTelemetry SDK spans for inbound MCP/webhook requests and internal webhook
sync work. Logs emitted from traced contexts include `trace_id`, `span_id`, and
`trace_sampled`, which is useful for local debugging even without an exporter.

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

For terminology, corpus layout, metrics, and interpretation guidance, see [Eval](eval.md).

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
| drifted | Annotation not updated after code change |

For lint rule matching, both `drifted` and `drift` are accepted for the same category. User-facing reports use `drifted`, while generated state and internal normalization may use `drift`.

See [Lint Guide](lint.md) for the exact per-category rules, overlaps, and implementation-aligned semantics.

Per-category `action: ignore` can be set in `.ccg.yaml`'s `rules`. In `--strict` mode, `action: ignore` rules are applied.

### Lint Policy vs Generated State

CCG now separates human-managed lint policy from generated lint state:

| Path | Owner | Purpose |
|------|-------|---------|
| `.ccg.yaml` | Human | Project policy: excludes, include paths, manual lint rules (`ignore`, etc.) |
| `.ccg/lint-history.json` | Generated | Twice Rule consecutive-occurrence counters |
| `.ccg/auto-rules.yaml` | Generated | Warn-only rules recorded by Twice Rule |

`ccg lint` no longer appends generated warn rules into `.ccg.yaml`. Repeated issues are recorded in `.ccg/auto-rules.yaml`, while `.ccg.yaml` remains the place for manual policy decisions.

If an older repository already has generated `auto: true` rules inside `.ccg.yaml`, run `ccg lint --migrate-auto-rules` once to move them into `.ccg/auto-rules.yaml`.
