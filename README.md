# code-context-graph

Local code analysis tool that parses codebases via Tree-sitter into a knowledge graph. Supports 12 languages, 17 MCP tools, and custom annotation search.

CCG is built primarily for GPT, Claude, Codex, and other LLM-based coding agents. It acts as local or self-hosted context infrastructure: agents can search code by intent, inspect call graphs, trace impact, retrieve docs, and keep responses bounded instead of reading entire repositories into context.

This is a developer-operated tool, not a general SaaS admin product. The expected users are coding agents and developers who can run CLI commands, configure MCP, read logs, and use the generated graph/docs to guide code changes.

Inspired by [code-review-graph](https://github.com/tirth8205/code-review-graph) — a Python-based code analysis tool. This project reimplements and extends the concept in Go with multi-DB support, custom annotation system, and MCP integration for AI-powered code understanding.

## Features

- **12 languages**: Go, Python, TypeScript, Java, Ruby, JavaScript, C, C++, Rust, Kotlin, PHP, Lua/Luau
- **17 MCP tools**: parse, search, graph queries, impact analysis, flow tracing, change detection, documentation discovery, and more
- **Evidence-driven code exploration**: DB-backed retrieval returns small file-level candidates with matched fields, evidence nodes, and optional docs before agents drill into exact graph nodes
- **Browser Wiki UI**: `ccg-server` can serve generated docs, tree search, DB-backed retrieval, Context Tray copying, and an Obsidian-style graph viewer
- **Custom annotations**: `@intent`, `@domainRule`, `@sideEffect`, `@mutates`, `@index` — search code by business context ([details](guide/annotations.md))
- **Webhook sync**: GitHub / Gitea push events → auto clone + build with per-repo branch filtering and `.ccg.yaml` `include_paths` auto-loading ([details](guide/webhook.md))
- **Multi-DB**: SQLite (local), PostgreSQL
- **Full-text search**: FTS5 (SQLite), tsvector+GIN (PostgreSQL)

## Installation

### npm / bun (recommended)

```bash
npm install -g code-context-graph
# or
bun install -g code-context-graph
```

The npm package installs both `ccg` and `ccg-server`.

### go install

```bash
go install github.com/tae2089/code-context-graph/cmd/ccg@latest
go install github.com/tae2089/code-context-graph/cmd/ccg-server@latest
```

### Build from source

```bash
CGO_ENABLED=1 go build -tags "fts5" -o ccg ./cmd/ccg/
CGO_ENABLED=1 go build -tags "fts5" -o ccg-server ./cmd/ccg-server/

# Or use Makefile (injects version from git tag automatically)
make build
# Local stripped release-style build
make release
```

## Quick Start

```bash
# Parse your project. For the default local SQLite database (ccg.db), runtime
# commands create and migrate the database automatically on first use only when
# the schema is missing.
ccg build .
# Build complete: 70 files, 749 nodes, 7387 edges

# Search (includes annotations)
ccg search "authentication"

# Search by business context
ccg search "payment"    # finds functions with @intent/@domainRule about payments

# Build generated docs and the browser Wiki compatibility index
ccg docs --out docs

# Serve the browser Wiki UI from built assets; builds the graph for DB-backed APIs
make wiki-run

# Graph statistics
ccg status

# Version info
ccg version

# Namespace isolation (MSA)
ccg build ./backend --namespace backend
ccg search --namespace backend "auth"

```

`ccg docs` writes generated Markdown plus `.ccg/wiki-index.json` as a browser
Wiki compatibility snapshot. The Wiki prefers the graph database for tree
navigation and search, then uses `wiki-index.json` only when DB-backed
navigation is unavailable. Runtime `search_docs` uses DB-backed graph and
annotation evidence; it does not depend on a separately generated retrieval index.

For LLM agents, use DB-backed `search_docs` as the first stop for broad
natural-language questions such as "how does webhook sync work?" or "where are
the operational risks?". It is not a Top1 search engine; it is an
evidence-driven narrowing layer that should return a small set of relevant
files with `matched_fields`, `matched_terms`, and evidence nodes. Read the
narrowed docs with `get_doc_content`, then use `get_node`, `query_graph`,
`trace_flow`, and impact tools only after the route is narrowed. Use `ccg search` as a focused
annotation/keyword candidate search rather than the first tool for broad code
understanding.

If you use PostgreSQL, a custom SQLite DSN, an existing schema, or a controlled
upgrade workflow, run `ccg migrate` explicitly before runtime commands. This
also applies when upgrading CCG against an existing default `ccg.db` created by
an older version. See the [CLI Reference](guide/cli-reference.md) for the full
migration contract.

## Browser Wiki

`ccg-server` can serve a React-based Wiki UI at `/wiki` when `--wiki-dir` points
at a built `web/wiki/dist` directory. Docker images include that built UI at
`/usr/share/ccg/wiki`; standalone binaries keep the assets separate so binary
size stays small.

The Wiki is meant for developers and agents inspecting a generated codebase:

- Tree navigation over folders, packages, files, and annotated symbols
- Keyword search and DB-backed `search_docs` with matched evidence and small
  file-level result sets
- Rich symbol detail cards from CCG annotations even when a symbol has no
  generated Markdown file
- Context Tray for collecting files and doc-less symbols into one Markdown
  bundle that can be copied into another LLM tool
- Graph tab backed by `/wiki/api/graph`, showing namespace nodes and edges with
  filters for structure, calls, imports, types, and symbols

Local development shortcut:

```bash
make wiki-run
```

Use `make wiki-run-indexed` when you also want generated Markdown and the
`wiki-index.json` compatibility snapshot before starting the server.

For self-hosted deployments, run `ccg-server --wiki-dir <dist-dir>` and protect
`/wiki/api/*` with the same bearer token policy used for `/mcp`. See
[Docker](guide/docker.md#wiki-ui) and [Runtime Layout](guide/runtime-layout.md)
for deployment details.

## Demo

Actual output from CCG parsing its own codebase.

### 1. Parse the Codebase

```
$ ccg build .
Build complete: 145 files, 1654 nodes, 10489 edges
```

### 2. Graph Statistics

```
$ ccg status
Nodes: 1654
Edges: 3767
Files: 192

Node kinds:
  class: 253
  file: 145
  function: 1111
  package: 47
  type: 98

Edge kinds:
  calls: 1822
  contains: 1597
  imports_from: 278
  implements: 66
  inherits: 4

Fallback call analysis:
  calls: 1823
  fallback_calls: 0
  fallback_ratio: 0.00%
```

### 3. Code Search

```
$ ccg search "impact analysis"
internal/app/analyze/impact/impact.go              file      internal/app/analyze/impact/impact.go:1
mcp.impactRadiusResponse                           class     internal/adapters/inbound/mcp/handler_analysis.go:36
internal/adapters/inbound/mcp/handler_analysis.go  file      internal/adapters/inbound/mcp/handler_analysis.go:1
mcp.ImpactAnalyzer                                 type      internal/adapters/inbound/mcp/deps.go:40
mcp.impactRadiusMetadata                           class     internal/adapters/inbound/mcp/handler_analysis.go:27
mcp.handlers.getImpactRadius                       function  internal/adapters/inbound/mcp/handler_analysis.go:109

$ ccg search "repository sync"
reposync.Service.Sync                               function  internal/app/reposync/service.go:27
remote.buildRepoSyncHTTP                            function  internal/runtime/remote/http.go:88
reposync.syncPayload                                class     internal/app/reposync/queue.go:94
reposync.SyncHandlerFunc                            type      internal/app/reposync/queue.go:21
reposync.SyncQueue                                  class     internal/app/reposync/queue.go:118
webhook.SyncFunc                                    type      internal/adapters/inbound/webhook/handler.go:22
```

### 4. Agent Integration via MCP

After configuring `.mcp.json`, you can ask an MCP-capable coding agent directly:

> **"Explain the webhook sync flow in this project"**

The agent calls CCG MCP tools and answers directly from the graph:

```
trace_flow(qualified_name: "webhook.WebhookHandler.ServeHTTP")
→ WebhookHandler.ServeHTTP
  → SyncQueue.Add
    → safeHandle (retry loop: max 3 attempts, exponential backoff 1s→30s)
      → clone (git clone, 15min timeout)
      → build (ccg build, same context)
```

> **"Where is the authentication-related code?"**

```
search(query: "authentication")
→ internal/adapters/inbound/webhook/handler.go  (HMAC signature validation)
→ cmd/ccg-server/main.go       (CCG_WEBHOOK_SECRET / --webhook-secret)
```

## MCP Server

Add `.mcp.json` to your project:

```json
{
  "mcpServers": {
    "ccg": {
      "command": "ccg",
      "args": ["serve", "--db-driver", "sqlite", "--db-dsn", "ccg.db"]
    }
  }
}
```

For remote HTTP mode:

Run the self-hosted server with `ccg-server` and connect to `/mcp`. The same
server can also expose `/wiki` when `--wiki-dir` is configured:

```json
{
  "mcpServers": {
    "ccg": {
      "type": "streamable-http",
      "url": "http://your-server:8080/mcp"
    }
  }
}
```

MCP-capable clients such as Codex or Claude Code can connect and get access to
17 MCP tools. See [MCP Tools Reference](guide/mcp-tools.md) for the full list.

## Architecture

```
Source Code → Tree-sitter Parser → Nodes + Edges + Annotations
                                        ↓
                              SQLite / PostgreSQL (GORM)
                                        ↓
                                   FTS Search
                                        ↓
                         ccg serve                ccg-server
                         stdio MCP        Streamable HTTP + Wiki UI
                            ↓              ↓          ↓          ↑
                     Coding Agents   Remote Clients  Browser   GitHub / Gitea Webhook
                                                       Wiki      push → clone → build → DB
```

See [Architecture Details](guide/architecture.md) for component breakdown and DB schema.

## Documentation

| Guide | Description |
|-------|-------------|
| [Korean Guide](guide/ko/README.md) | 한국어 문서 인덱스 (Korean Documentation Index) |
| [CLI Reference](guide/cli-reference.md) | All commands, flags, and config file (`.ccg.yaml`) |
| [Lint](guide/lint.md) | Detailed `ccg lint` category reference, interpretation guide, and CI usage |
| [MCP Tools](guide/mcp-tools.md) | 17 MCP tools, agent skills, AI-Driven Annotation |
| [Annotations](guide/annotations.md) | Annotation system — tags, examples, search |
| [Webhook](guide/webhook.md) | Webhook sync, branch filtering, HMAC, graceful shutdown |
| [Docker](guide/docker.md) | Docker build, MCP server, Wiki UI, PostgreSQL deployment |
| [Operations](guide/operations.md) | Deployment profiles, database choice, readiness, webhook operations |
| [Runtime Layout](guide/runtime-layout.md) | `ccg`, `ccg-server`, Wiki serving, and shared `ccg-core` ownership boundaries |
| [Development](guide/development.md) | Dev guide, integration test, project structure |
| [Namespace Migration](guide/namespace-migration.md) | Default namespace change and migration guide |
| [Architecture](guide/architecture.md) | Data flow, components, DB schema |
| [CLAUDE.md Guide](guide/claude-md-guide.md) | Template for projects using CCG |

## License

MIT
