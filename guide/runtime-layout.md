# Runtime Layout

CCG is split into three runtime layers:

| Layer | Path | Responsibility |
|-------|------|----------------|
| `ccg` | `cmd/ccg`, `internal/cli` | Local CLI commands and local MCP over stdio |
| `ccg-server` | `cmd/ccg-server`, `internal/server` | Self-hosted Streamable HTTP MCP server, health/status endpoints, and webhook sync |
| `ccg-core` | `internal/core` | Shared parser, DB, store, search, migration, and incremental-sync wiring |

This split keeps local agent usage small while letting self-hosted deployments
own HTTP exposure and webhook policy explicitly.

## Binaries

### `ccg`

`ccg` is the local developer and agent binary. It owns one-shot commands such as
`build`, `update`, `search`, `docs`, `lint`, `status`, `migrate`, and `eval`.

`ccg serve` starts MCP over stdio only. It is intended for local MCP clients
such as Codex or Claude Code launched on the same machine. HTTP and webhook
flags are intentionally not part of this command.

### `ccg-server`

`ccg-server` is the long-running self-hosted service. It serves:

- `/mcp` for Streamable HTTP MCP
- `/health` for liveness
- `/ready` for readiness
- `/status` for operational diagnostics
- `/webhook` when `--allow-repo` is configured

Use `ccg-server` for remote clients, team deployments, container deployments,
and GitHub/Gitea webhook sync.

### `ccg-core`

`internal/core` is the shared runtime assembly layer. It provides:

- language walker registration
- database opening and schema-version validation
- migration execution
- GORM store construction
- search backend selection
- incremental sync construction
- parser and database cleanup

The package is intentionally runtime wiring, not a command layer. CLI flag
parsing stays in `internal/cli` and HTTP/webhook policy stays in
`internal/server`.

## Ownership Boundaries

| Concern | Owner |
|---------|-------|
| Cobra local command definitions | `internal/cli` |
| Local stdio MCP command | `internal/cli/serve.go` |
| HTTP listen address, bearer token, stateless sessions | `internal/server` and `cmd/ccg-server` |
| Webhook allowlist, HMAC, clone base URL, repo root, retry policy | `internal/server` and `internal/webhook` |
| MCP tool handlers and DTOs | `internal/mcp` |
| Shared graph runtime dependencies | `internal/core` |
| Business graph build/update behavior | `internal/service` |
| Docker default process | `ccg-server` |

## Common Workflows

Local graph work:

```bash
ccg build .
ccg search "authentication"
ccg docs --out docs
ccg serve
```

Self-hosted HTTP MCP:

```bash
ccg-server \
  --http-addr 0.0.0.0:8080 \
  --http-bearer-token "$CCG_HTTP_BEARER_TOKEN"
```

Webhook sync:

```bash
ccg-server \
  --http-addr 0.0.0.0:8080 \
  --http-bearer-token "$CCG_HTTP_BEARER_TOKEN" \
  --allow-repo "org/api:main,develop" \
  --webhook-secret "$WEBHOOK_SECRET" \
  --repo-clone-base-url https://github.com \
  --repo-root /data/repos
```

Docker:

```bash
docker run -d -p 8080:8080 \
  -e CCG_HTTP_BEARER_TOKEN="$CCG_HTTP_BEARER_TOKEN" \
  ccg --http-addr :8080
```

The Docker image still includes `ccg` for one-shot build/migrate workflows, but
the default entrypoint is `ccg-server`.

GitHub release archives and the npm package include both executables. Install
one package and choose `ccg` or `ccg-server` at runtime.

## Migration Notes

Previous deployments that used:

```bash
ccg serve --transport streamable-http ...
```

should switch to:

```bash
ccg-server ...
```

`ccg serve --transport streamable-http` now returns guidance instead of starting
HTTP. Existing stdio MCP clients can keep using `ccg serve`.

## Config Notes

Both binaries read the same DB settings:

- `--db-driver`, `CCG_DB_DRIVER`, or `.ccg.yaml` `db.driver`
- `--db-dsn`, `CCG_DB_DSN`, or `.ccg.yaml` `db.dsn`

`ccg-server` also reads server defaults from supported environment variables,
including `CCG_HTTP_BEARER_TOKEN`, `CCG_OTEL_ENDPOINT`,
`CCG_WEBHOOK_WORKERS`, `CCG_WEBHOOK_MAX_TRACKED_REPOS`,
`CCG_WEBHOOK_ATTEMPT_TIMEOUT`, retry tuning variables, and `CCG_REPO_ROOT`.
