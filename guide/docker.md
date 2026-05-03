# Docker

## Build Image

```bash
docker build -t ccg .
```

## Run as MCP Server

```bash
# Set a bearer token for externally bound HTTP transport.
export CCG_HTTP_BEARER_TOKEN=replace-with-a-long-random-token

# Mount your project, build the graph, and serve over HTTP
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

Run migrations only when creating a new database or upgrading CCG:

```bash
docker run --rm \
  -v $(pwd):/workspace --entrypoint ccg ccg \
  migrate
```

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
docker run -d -p 8080:8080 \
  -e CCG_HTTP_BEARER_TOKEN="$CCG_HTTP_BEARER_TOKEN" \
  -e CCG_DB_DRIVER=postgres \
  -e CCG_DB_DSN="host=db user=ccg password=ccg dbname=ccg sslmode=disable" \
  -v $(pwd):/workspace --entrypoint sh ccg \
  -c "ccg build /workspace && ccg serve --transport streamable-http --http-addr :8080"
```

For PostgreSQL, run the migration as a separate one-shot command when needed:

```bash
docker run --rm \
  -e CCG_DB_DRIVER=postgres \
  -e CCG_DB_DSN="host=db user=ccg password=ccg dbname=ccg sslmode=disable" \
  --entrypoint ccg ccg \
  migrate
```

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
