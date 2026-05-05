# Docker

## Build Image

```bash
docker build -t ccg .
```

## Run as MCP Server

```bash
# Set a bearer token for externally bound HTTP transport.
export CCG_HTTP_BEARER_TOKEN=replace-with-a-long-random-token

# For the default local SQLite database (ccg.db), first-run runtime commands
# auto-migrate only when the schema is missing.
# Mount your project, build the graph, and serve over HTTP.
docker run -d -p 8080:8080 \
  -e CCG_HTTP_BEARER_TOKEN="$CCG_HTTP_BEARER_TOKEN" \
  -v $(pwd):/repo --entrypoint sh ccg \
  -c "ccg build /repo && ccg-server --http-addr :8080"
```

The image's default HTTP command binds to `:8080`, so you must provide
`CCG_HTTP_BEARER_TOKEN` for normal external access.

If this container is exposed through an ingress, reverse proxy, or load
balancer, keep health and status endpoints internal. `/health`, `/ready`, and
`/status` are intended for trusted operational checks and should not be exposed
to the public internet. See the [Operations Guide](operations.md#http-exposure)
for endpoint exposure guidance.

Example reverse-proxy policy:

| Path | Public Internet | Internal Network |
|------|-----------------|------------------|
| `/mcp` | Allowed only with bearer auth and network policy | Allowed |
| `/wiki` | Allowed only when the Wiki UI shell should be public | Allowed |
| `/wiki/api/*` | Allowed only with bearer auth and network policy | Allowed |
| `/webhook` | Allowed only with HMAC secret and repo allowlist | Allowed |
| `/health` | Blocked | Allowed |
| `/ready` | Blocked | Allowed |
| `/status` | Blocked | Allowed |

For webhook service mode, use a canonical clone base URL and keep one
organization/owner per CCG instance unless the repo names are guaranteed unique:

```bash
docker run -d -p 8080:8080 \
  -e CCG_HTTP_BEARER_TOKEN="$CCG_HTTP_BEARER_TOKEN" \
  -e CCG_DB_DRIVER=postgres \
  -e CCG_DB_DSN="$CCG_DB_DSN" \
  -e CCG_REPO_ROOT=/data/repos \
  -v ccg-repos:/data/repos \
  --entrypoint ccg-server ccg \
  --http-addr :8080 \
    --allow-repo "acme/*" \
    --webhook-secret "$WEBHOOK_SECRET" \
    --repo-clone-base-url https://github.com
```

For the mounted default local SQLite database, use an explicit migration command
when upgrading CCG against an existing schema:

```bash
docker run --rm \
  -v $(pwd):/repo --entrypoint ccg ccg \
  migrate
```

For PostgreSQL, custom SQLite DSNs, or other non-default runtime setups, pass
the matching database driver and DSN to `ccg migrate` before starting runtime
commands.

Connect from `.mcp.json`:

```json
{
  "mcpServers": {
    "ccg": {
      "type": "streamable-http",
      "url": "http://localhost:8080/mcp",
      "headers": {
        "Authorization": "Bearer replace-with-a-long-random-token"
      }
    }
  }
}
```

## Wiki UI

Docker images include the built Wiki UI at `/usr/share/ccg/wiki`, and the
default container command enables it with `--wiki-dir /usr/share/ccg/wiki`.
Standalone binaries do not embed Wiki assets. For binary deployments, download
`ccg-wiki-dist.tar.gz` from the release page, extract it, and pass the extracted
directory to `ccg-server`:

```bash
ccg-server \
  --http-addr :8080 \
  --http-bearer-token "$CCG_HTTP_BEARER_TOKEN" \
  --wiki-dir ./wiki
```

The static `/wiki` app shell is served without request headers so browsers can
open it directly. `/wiki/api/*` uses the same bearer token as `/mcp`; the UI
prompts for that token when the API returns `401`.
Run `ccg docs --out docs` for each served namespace before opening the Wiki so
`.ccg/wiki-index.json` exists. The Wiki API reads `wiki-index.json`; MCP
retrieval continues to use the separate community-based `doc-index.json`. The
Wiki Graph tab reads graph nodes and edges directly from the configured
database via `/wiki/api/graph`, so it reflects the latest `ccg build` or
webhook sync state.

## Choosing SQLite vs PostgreSQL

SQLite is the simplest choice for local, single-user workflows: one repository,
manual `ccg build` / `ccg update`, and a database file that can be recreated if
needed.

Use PostgreSQL when CCG is operated as a service:

- Team-shared MCP server or multiple concurrent MCP clients
- Webhook sync enabled for ongoing repository updates
- Multiple repositories or namespaces in one server database
- Operational backup, restore, monitoring, or remote access requirements
- Roughly 50k+ search documents or 100k+ graph nodes

For larger deployments, PostgreSQL should be treated as the default. At around
300k+ graph nodes, multiple always-synced repositories, or frequent webhook
updates, SQLite is likely to become an operational bottleneck. See
[Operations](operations.md#database-choice) for deployment profiles and scale
signals.

## Run with PostgreSQL

```bash
# PostgreSQL requires an explicit migration step before runtime commands.
docker run --rm \
  -e CCG_DB_DRIVER=postgres \
  -e CCG_DB_DSN="host=db user=ccg password=ccg dbname=ccg sslmode=disable" \
  --entrypoint ccg ccg \
  migrate

docker run -d -p 8080:8080 \
  -e CCG_HTTP_BEARER_TOKEN="$CCG_HTTP_BEARER_TOKEN" \
  -e CCG_DB_DRIVER=postgres \
  -e CCG_DB_DSN="host=db user=ccg password=ccg dbname=ccg sslmode=disable" \
  -v $(pwd):/repo --entrypoint sh ccg \
  -c "ccg build /repo && ccg-server --http-addr :8080"
```

The one-shot migration command above should be run before build, serve, or
other runtime commands.

## Docker Compose

```bash
docker compose up -d
```

### Integration Test (Gitea + PostgreSQL + ccg)

The full-stack pipeline test can also be run with Docker Compose. See the [Development Guide](development.md#integration-test) for details.

```bash
docker compose -f docker-compose.integration.yml up -d --build
docker compose -f docker-compose.integration.yml down -v
```
