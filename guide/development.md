# Development

## Build

```bash
CGO_ENABLED=1 go build -tags "fts5" -o ccg ./cmd/ccg/
CGO_ENABLED=1 go build -tags "fts5" -o ccg-server ./cmd/ccg-server/
```

Makefile shortcuts:

```bash
make build        # build stripped ccg and ccg-server binaries (same as make release)
make release      # stripped build with embedded version/commit/date
make build-debug  # unstripped ccg and ccg-server binaries with embedded version/commit/date
make wiki-db      # migrate the local Wiki DB and build the graph from WIKI_REPO
make wiki-run     # build Wiki UI, build graph, run ccg-server with DB-backed Wiki APIs
make wiki-run-indexed # build Wiki UI, build graph, generate docs/indexes, then run ccg-server
```

`make wiki-run` defaults to `127.0.0.1:8080` and `ccg.db`. Override values with
`WIKI_ADDR`, `WIKI_DB`, `WIKI_REPO`, and optionally `WIKI_TOKEN`:

```bash
WIKI_ADDR=127.0.0.1:18080 WIKI_TOKEN=dev-token make wiki-run
```

## Test

```bash
make test
```

`make test` runs both the Go test suite and the lightweight shell helper tests for the Docker integration harness.

### Reading a full run

A full run prints thousands of lines, so it is tempting to filter it down to the failures. Filter on the wrong thing and the failure survives as a name with nothing attached:

```bash
# Don't. `--- FAIL: TestX` arrives with no message under it, and the run is over.
go test -tags fts5 ./... -count=1 | grep -E '^(FAIL|---)'
```

The lines that say what broke are the ones indented under `--- FAIL`, and that filter drops every one of them. Keep the whole log and search it afterwards:

```bash
go test -tags fts5 ./... -count=1 2>&1 | tee /tmp/ccg-test.log
grep -B2 -A20 '^--- FAIL' /tmp/ccg-test.log
```

If you must filter live, keep the indented lines too:

```bash
go test -tags fts5 ./... -count=1 2>&1 | grep -E '^(FAIL|--- FAIL|ok )|^[[:space:]]'
```

This matters most for a test that fails once and never again: #78 lost the only message an intermittent failure ever produced, and no amount of re-running brought it back.

## PostgreSQL Tests

Tests behind the `postgres` build tag need a real server. Point `TEST_POSTGRES_DSN` at one and run:

```bash
docker run -d --name ccg-test-pg -p 5432:5432 \
  -e POSTGRES_PASSWORD=postgres -e POSTGRES_DB=ccg_test postgres:16

TEST_POSTGRES_DSN="host=localhost user=postgres password=postgres dbname=ccg_test port=5432 sslmode=disable" \
  CGO_ENABLED=1 go test -tags "fts5,postgres" ./... -count=1
```

Two things to know about that command:

- `TEST_POSTGRES_DSN` is the only variable read. With it unset or misspelled, every postgres test calls `t.Skip`, and the run prints `ok` for each package while having exercised nothing. Set `REQUIRE_POSTGRES=1` to turn that silence into a failure, as CI does.
- No `-p 1`. Packages run in parallel because each test creates its own schema through `internal/db/dbtest` and points `search_path` at it. A test that fails only when packages run together is reaching outside its schema; `-p 1` would hide that rather than fix it.

Writing a postgres test means calling one of two helpers instead of opening a connection yourself:

```go
db := dbtest.OpenIsolatedPostgres(t)   // a *gorm.DB scoped to this test's schema
dsn := dbtest.IsolatedPostgresDSN(t)   // the same schema as a DSN, for code that opens its own pool
```

Both create the schema, drop it when the test returns, and skip the test when no server answers. Notes on the design:

- The schema travels in the DSN, not in a `SET search_path` statement. `SET` reaches one connection; a pool opens several, and the rest keep writing to `public`.
- Never write `search_path` into the DSN by hand and never name `public` in a test. A schema-qualified name or a catalog query filtered on `'public'` reads another test's tables.
- Catalog queries need a schema condition. `SELECT count(*) FROM pg_indexes WHERE indexname = 'x'` counts every concurrent test's copy. Filter on `current_schema()`, or use `migration.PostgresIndexExists`.
- Extensions belong to a database, not a schema, so `pg_trgm` lives in a schema of its own that is appended to each test's `search_path`. That is what keeps an index using `gin_trgm_ops` working outside `public`.
- A schema left behind by a killed test is dropped by an age-based sweep on the next run. The cutoff is hours, far longer than any test, so a schema in use is never swept.

## Integration Test

Full-stack pipeline test: Gitea push → explicit `ccg migrate` → webhook → ccg clone → build → PostgreSQL → MCP verification:

```bash
./scripts/integration-test.sh
```

Lightweight shell helper tests cover the integration harness helpers without starting Docker:

```bash
make test-integration-helpers
```

### What It Does

1. Start 3 containers via Docker Compose (Gitea, PostgreSQL, ccg)
2. Run `ccg migrate` in the ccg container before starting the runtime service
3. Create Gitea admin user and API token
4. Create repository with sample Go code
5. Register webhook pointing to ccg
6. Push code to Gitea (triggers webhook)
7. Wait for ccg to complete clone, parse, and build
8. Verify graph data via MCP protocol (initialize → tools/call)
9. Capture debug artifacts on failure
10. Clean up all containers unless requested otherwise

### Debugging Integration Failures

The integration harness writes Docker diagnostics on failure. Use these environment variables for local debugging:

| Variable | Default | Description |
|----------|---------|-------------|
| `ARTIFACT_DIR` | `artifacts/integration-<timestamp>` | Directory for `compose-ps.txt`, `compose.log`, and per-service logs |
| `KEEP_CONTAINERS` | `0` | Set to `1` to skip `docker compose down -v` after the run |
| `DUMP_ON_SUCCESS` | `0` | Set to `1` to capture artifacts even when the run passes |
| `WEBHOOK_WAIT_SECONDS` | `60` | Maximum webhook/build wait per repository |
| `CCG_E2E_ALLOW_MCP_LOG_FALLBACK` | `0` | Local debugging only: set to `1` to allow log-based webhook smoke checks when MCP initialize fails. Default behavior fails because MCP verification is required. |

Examples:

```bash
KEEP_CONTAINERS=1 ARTIFACT_DIR=/tmp/ccg-e2e ./scripts/integration-test.sh
DUMP_ON_SUCCESS=1 ./scripts/integration-test.sh
```

Webhook waits prefer MCP-observable graph stats for the target namespace and only fall back to ccg logs when MCP is not ready or not yet showing data.
MCP initialization and tool responses are strict: malformed JSON, top-level JSON-RPC errors, `result.isError=true`, and missing `result.content[0].text` for content assertions fail the integration run. A run that cannot initialize MCP will not report the full integration test as passed unless the local debug override above is explicitly set, and that override skips MCP tool verification.

### Manual Container Management

```bash
make container-artifacts
CONTAINER_ARCH="$(go env GOARCH)" docker compose -f docker-compose.integration.yml up -d --build
docker compose -f docker-compose.integration.yml down -v
```

## Project Structure

```
cmd/ccg/              — Local CLI entry point (cobra, stdio MCP)
cmd/ccg-server/       — Self-hosted HTTP MCP/webhook server entry point
internal/
  analysis/           — Analysis engine (impact, flows, changes, incremental updates)
  annotation/         — Annotation parser
  cli/                — CLI command definitions
  core/               — Shared runtime wiring for parsers, DB, store, search, sync
  ctx/                — Request-context values (namespace isolation)
  docs/               — Documentation generation
  mcpruntime/         — Shared MCP runtime assembly, stdio runner, cache, telemetry
  mcp/                — MCP server (18 tools)
  wikiserver/         — ccg-server Wiki static serving and viewer API
  wikiindex/          — Wiki presentation index builder (`wiki-index.json`)
  model/              — DB models
  parse/treesitter/   — Tree-sitter parser (12 languages, including Lua/Luau)
  pathspec/           — Pure include/exclude and lexical path matching
  ragindex/           — Shared Wiki tree and documentation-search DTOs/helpers
  server/             — HTTP MCP server, health/status endpoints, webhook runtime
  service/            — Business logic
  store/              — GORM store
  webhook/            — Webhook handler, SyncQueue, RepoFilter
skills/               — Agent skill files
guide/                — Project documentation
docs/                 — Auto-generated docs (ccg docs)
scripts/              — Scripts (integration test, etc.)
```

The React/Tailwind Wiki UI lives in `web/wiki` and builds to `web/wiki/dist`.
The dist directory is ignored by git and packaged separately for releases:

```bash
make wiki-build
```

## Skill Contract

Each project-local skill under `skills/` declares:

- trigger-rich `name` and `description` frontmatter
- semantic `metadata.version`
- `metadata.openclaw.category` and `domain`
- required binaries and prerequisite skills under `metadata.requires`
- `metadata.cliHelp` only when the skill has a direct CLI help surface

Keep detailed variants in directly linked `references/` files and keep core
`SKILL.md` instructions host-neutral. Validate metadata, dependencies, direct
reference links, and removed-command drift with:

```bash
go test ./internal/adapters/inbound/cli -run TestProjectSkills -count=1
```

## Conventions

- TDD: Red → Green → Refactor
- Tidy First: Separate structural changes from behavioral changes
- Use GORM queries only (no raw SQL)
- Logging: `slog`
- CLI: `cobra` framework
- Build flags: `CGO_ENABLED=1 -tags "fts5"`

### Declaration order within a file

Follow the standard-library convention of **cohesion over kind-grouping**: keep a
type together with everything that operates on it, rather than sorting the file
into "all types, then all functions". Go does not care about declaration order at
compile time, so this rule exists purely for the reader.

Within a file, order top-level declarations as:

1. Package-level `const` / `var` blocks that configure the whole file, near the top
   (after imports).
2. For each type, a contiguous block: the `type` declaration → its interface-
   satisfaction assertion(s) → its `New*` constructor(s) → its methods. Do not let a
   free function or an unrelated type split a type's method set.
3. Free helper functions after the type they support, or grouped at the end of the
   file if they are shared.

Interface-satisfaction assertions go **above** the methods, not at the bottom of the
file, so the implemented contract is visible upfront:

- `var _ Iface = (*T)(nil)` sits immediately after the `type T` declaration.
- When `T` is declared in another file of the same package (e.g. the split
  `graphgorm.Store`), put the assertion at the top of the file — after imports,
  before that file's methods on `T`.

One deliberate exception stays next to what it describes (this *is* the cohesion
rule, not a violation of it):

- A package-level `var` (e.g. a compiled `regexp`) placed immediately above the
  single function that uses it.

There is no standard tool that enforces this ordering; `gofmt`/`gofumpt` handle
formatting only. It is a review-time convention.
