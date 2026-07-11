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
docker compose -f docker-compose.integration.yml up -d --build
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
  ctxns/              — Context namespace
  docs/               — Documentation generation
  mcpruntime/         — Shared MCP runtime assembly, stdio runner, cache, telemetry
  mcp/                — MCP server (17 tools)
  wikiserver/         — ccg-server Wiki static serving and viewer API
  wikiindex/          — Wiki presentation index builder (`wiki-index.json`)
  model/              — DB models
  parse/treesitter/   — Tree-sitter parser (12 languages, including Lua/Luau)
  pathutil/           — Path utilities
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

## Conventions

- TDD: Red → Green → Refactor
- Tidy First: Separate structural changes from behavioral changes
- Use GORM queries only (no raw SQL)
- Logging: `slog`
- CLI: `cobra` framework
- Build flags: `CGO_ENABLED=1 -tags "fts5"`
