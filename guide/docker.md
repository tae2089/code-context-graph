# Docker

## Build Image

```bash
docker build -t ccg .
```

## Run as MCP Server

```bash
# Set a bearer token for externally bound HTTP transport.
export CCG_HTTP_BEARER_TOKEN=replace-with-a-long-random-token

# Mount your project and serve over HTTP
docker run -d -p 8080:8080 \
  -e CCG_HTTP_BEARER_TOKEN="$CCG_HTTP_BEARER_TOKEN" \
  -v $(pwd):/workspace --entrypoint sh ccg \
  -c "ccg build /workspace && ccg serve --transport streamable-http --http-addr :8080"
```

The image's default HTTP command binds to `:8080`, so you must provide
`CCG_HTTP_BEARER_TOKEN` for normal external access.

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

## Run with PostgreSQL

```bash
docker run -d -p 8080:8080 \
  -e CCG_HTTP_BEARER_TOKEN="$CCG_HTTP_BEARER_TOKEN" \
  -e CCG_DB_DRIVER=postgres \
  -e CCG_DB_DSN="host=db user=ccg password=ccg dbname=ccg sslmode=disable" \
  -v $(pwd):/workspace --entrypoint sh ccg \
  -c "ccg build /workspace && ccg serve --transport streamable-http --http-addr :8080"
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
