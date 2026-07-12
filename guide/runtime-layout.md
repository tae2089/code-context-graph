# Runtime Layout

CCG has one runtime composition tree and two binaries with deliberately different
dependency closures.

| Runtime owner | Path | Responsibility |
| --- | --- | --- |
| Shared runtime | `internal/runtime` | Parser registry, DB/schema, graph/search adapters, ingest transaction, migration, idempotent parser/DB close |
| MCP runtime | `internal/runtime/mcp` | Five grouped MCP dependency surfaces, cache, telemetry, stdio signals, idempotent MCP close |
| Remote runtime | `internal/runtime/remote` | Remote MCP, Wiki, webhook/repository-sync queue, and HTTP host composition |
| HTTP host | `internal/adapters/inbound/http` | Routes, auth/body limits, readiness/status mapping, listener, signals, bounded HTTP shutdown |

## Binary closure

### `ccg`

`cmd/ccg` selects the shared runtime, CLI adapter, and MCP runtime. It provides
one-shot build/update/search/docs/lint/status/migrate commands and `ccg serve`
over stdio. It does not link the HTTP host, Wiki HTTP, webhook adapter, or remote
runtime. This keeps local MCP use independent of remote hosting policy.

### `ccg-server`

`cmd/ccg-server` selects shared plus remote runtime. It exposes:

- `/mcp` — Streamable HTTP MCP
- `/health` — liveness
- `/ready` — DB and blocking-queue readiness
- `/status` — authenticated operational state
- `/wiki` and `/wiki/api/*` — optional built-in CCG Wiki
- `/webhook` — optional GitHub/Gitea repository sync

Both transports call `Runtime.MCPComponents()` and therefore register the same
17 tools and four prompts.

## Resource ownership

| Resource | Owner | Close rule |
| --- | --- | --- |
| Tree-sitter walkers | shared runtime | Deduplicate alias pointers, close once |
| DB connection | shared runtime | Close once after parser cleanup |
| MCP cache and telemetry | MCP Instance | Close once; telemetry gets a bounded shutdown context |
| Repository-sync context/workers | remote runtime | Cancel then drain once |
| HTTP listener | inbound HTTP host | Stop accepting, bounded `Shutdown`, then queue cleanup |
| Process exit | `cmd/*` | Log final error, invoke runtime close, choose exit code |

Partial startup follows stack discipline: MCP cleanup is registered immediately;
Wiki validation runs before queue creation; queue cleanup is registered immediately;
HTTP returns through the same idempotent cleanup functions on listener error or signal.

## Dependency direction

- Application and adapters never import runtime.
- `runtime/remote` imports inbound/outbound sides and passes `HostDeps` to HTTP.
- HTTP receives prebuilt handlers/checks/queue hooks and cannot construct DB,
  Wiki, webhook, Git, search, or telemetry implementations.
- Remote composition is a subpackage so importing shared runtime from local
  `ccg` cannot pull remote packages into its dependency closure.

`internal/archtest` checks these constraints and removed runtime-package absence.

## Workflows

Local MCP:

```bash
ccg build .
ccg serve
```

Browser Wiki:

```bash
ccg build .
ccg docs --out docs
ccg-server --http-addr 127.0.0.1:8080 --wiki-dir web/wiki/dist
```

Remote MCP:

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

The built-in Wiki remains a CCG capability. A future OpenWiki deployment is a
separate product boundary and does not replace these runtime routes.
