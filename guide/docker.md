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
  -v $(pwd):/workspace --entrypoint sh ccg \
  -c "ccg build /workspace && ccg serve --transport streamable-http --http-addr :8080"
```

The image's default HTTP command binds to `:8080`, so you must provide
`CCG_HTTP_BEARER_TOKEN` for normal external access.

If this container is exposed through an ingress, reverse proxy, or load
balancer, keep health and status endpoints internal. `/health`, `/ready`, and
`/status` are intended for trusted operational checks and should not be exposed
to the public internet. See the [Operations Guide](operations.md#http-exposure)
for endpoint exposure guidance.

For the mounted default local SQLite database, use an explicit migration command
when upgrading CCG against an existing schema:

```bash
docker run --rm \
  -v $(pwd):/workspace --entrypoint ccg ccg \
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
  -v $(pwd):/workspace --entrypoint sh ccg \
  -c "ccg build /workspace && ccg serve --transport streamable-http --http-addr :8080"
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
