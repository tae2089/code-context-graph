# Docker

## Build Image

```bash
docker build -t ccg .
```

## Run as MCP Server

```bash
# Mount your project and serve over HTTP
docker run -d -p 8080:8080 -v $(pwd):/workspace --entrypoint sh ccg \
  -c "ccg build /workspace && ccg serve --transport streamable-http --http-addr :8080"
```

Connect from `.mcp.json`:

```json
{
  "mcpServers": {
    "ccg": {
      "type": "streamable-http",
      "url": "http://localhost:8080/mcp"
    }
  }
}
```

## Run with PostgreSQL

```bash
docker run -d -p 8080:8080 \
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
