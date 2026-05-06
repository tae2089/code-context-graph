# code-context-graph

Local code analysis tool that parses codebases via Tree-sitter into a knowledge graph. Supports 12 languages, 33 MCP tools, and custom annotation search.

CCG is built primarily for GPT, Claude, Codex, and other LLM-based coding agents. It acts as local or self-hosted context infrastructure: agents can search code by intent, inspect call graphs, trace impact, retrieve docs, and keep responses bounded instead of reading entire repositories into context.

This is a developer-operated tool, not a general SaaS admin product. The expected users are coding agents and developers who can run CLI commands, configure MCP, read logs, and use the generated graph/docs to guide code changes.

Inspired by [code-review-graph](https://github.com/tirth8205/code-review-graph) — a Python-based code analysis tool. This project reimplements and extends the concept in Go with multi-DB support, custom annotation system, and MCP integration for AI-powered code understanding.

## Features

- **12 languages**: Go, Python, TypeScript, Java, Ruby, JavaScript, C, C++, Rust, Kotlin, PHP, Lua/Luau
- **33 MCP tools**: parse, search, impact analysis, flow tracing, dead code detection, postprocess operations, namespace file management, and more
- **RAG-first code exploration**: generated docs + community structure let LLM agents answer natural-language questions before drilling into exact graph nodes
- **Browser Wiki UI**: `ccg-server` can serve generated docs, tree search, PageIndex-style retrieval, Context Tray copying, and an Obsidian-style graph viewer
- **Custom annotations**: `@intent`, `@domainRule`, `@sideEffect`, `@mutates`, `@index` — search code by business context ([details](guide/annotations.md))
- **Webhook sync**: GitHub / Gitea push events → auto clone + build with per-repo branch filtering and `.ccg.yaml` `include_paths` auto-loading ([details](guide/webhook.md))
- **Eval**: Golden corpus-based parser accuracy (P/R/F1) and search quality (P@K, MRR, nDCG) evaluation ([details](guide/eval.md))
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

# Build docs and the default vectorless RAG index for agent-oriented exploration
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

# Evaluate parser accuracy (12 languages)
ccg eval --suite parser

# Update golden corpus
ccg eval --suite parser --update
```

`ccg docs` writes generated Markdown plus `.ccg/wiki-index.json` as a browser
Wiki compatibility snapshot. The Wiki prefers the graph database for tree
navigation and search, then falls back to `wiki-index.json` when DB-backed
navigation is unavailable. By default `ccg docs` also refreshes community structure and writes
`.ccg/doc-index.json` as a compatibility retrieval snapshot. Runtime
`retrieve_docs` paths prefer the graph database when it is configured and
queryable, then fall back to `doc-index.json` when DB-backed retrieval is
unavailable. Use `--rag=false` when you only want Markdown and the Wiki
snapshot, or `--rag-refresh=false` when you want to rebuild the RAG index from
existing community rows without recalculating communities.

For LLM agents, treat generated docs and the RAG index as the primary entrypoint
for natural-language questions such as "how does webhook sync work?" or "where
are the operational risks?". Use graph tools for exact symbols, call relations,
impact radius, and pagination-safe result sets. Use `ccg search` as a focused
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
- Keyword search and DB-primary `retrieve_docs`, with generated indexes kept as
  compatibility fallbacks
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

Use `make wiki-run-indexed` when you also want generated Markdown,
`wiki-index.json` snapshot, and the compatibility `doc-index.json` before
starting the server.

For self-hosted deployments, run `ccg-server --wiki-dir <dist-dir>` and protect
`/wiki/api/*` with the same bearer token policy used for `/mcp`. See
[Docker](guide/docker.md#wiki-ui) and [Runtime Layout](guide/runtime-layout.md)
for deployment details.

## Demo

Actual output from CCG parsing its own codebase.

### 1. Parse the Codebase

```
$ ccg build .
Build complete: 127 files, 1220 nodes, 12222 edges
```

### 2. Graph Statistics

```
$ ccg status
Nodes: 1220
Edges: 12222
Files: 127

Node kinds:
  class:    124
  file:     127
  function: 405
  test:     543
  type:      21

Edge kinds:
  calls:        9245
  contains:     1097
  imports_from: 1128
  inherits:        1
  tested_by:     751

Postprocess:
  Status: ok
  Fail-closed: 0
  Recent failures: 0
```

### 3. Code Search

```
$ ccg search "impact analysis"
internal/analysis/impact/impact_test.go  file      internal/analysis/impact/impact_test.go:1
internal/analysis/impact/impact.go       file      internal/analysis/impact/impact.go:1
mcp.ImpactAnalyzer                       type      internal/mcp/server.go:36
impact.EdgeReader                        type      internal/analysis/impact/impact.go:12
impact.Analyzer.ImpactRadius             function  internal/analysis/impact/impact.go:42
internal/mcp/handler_analysis.go         file      internal/mcp/handler_analysis.go:1

$ ccg search "dead code"
deadcode.Options          class     internal/analysis/deadcode/service.go:14
deadcode.Service.Find     function  internal/analysis/deadcode/service.go:38
mcp.handlers.findDeadCode function  internal/mcp/handler_analysis.go:273
```

### 4. Agent Integration via MCP

After configuring `.mcp.json`, you can ask an MCP-capable coding agent directly:

> **"Explain the webhook sync flow in this project"**

The agent calls CCG MCP tools and answers directly from the graph:

```
trace_flow(qualified_name: "webhook.WebhookHandler.ServeHTTP")
→ WebhookHandler.ServeHTTP
  → SyncQueue.Enqueue
    → safeHandle (retry loop: max 3 attempts, exponential backoff 1s→30s)
      → clone (git clone, 15min timeout)
      → build (ccg build, same context)
```

> **"Where is the authentication-related code?"**

```
search(query: "authentication")
→ internal/webhook/handler.go  (HMAC signature validation)
→ cmd/ccg-server/main.go       (--webhook-secret flag)
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
33 MCP tools. See [MCP Tools Reference](guide/mcp-tools.md) for the full list.

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
| [Eval](guide/eval.md) | Parser/search quality evaluation, golden corpus, and metrics |
| [Lint](guide/lint.md) | Detailed `ccg lint` category reference, interpretation guide, and CI usage |
| [MCP Tools](guide/mcp-tools.md) | 33 MCP tools, agent skills, AI-Driven Annotation |
| [Annotations](guide/annotations.md) | Annotation system — tags, examples, search |
| [Webhook](guide/webhook.md) | Webhook sync, branch filtering, HMAC, graceful shutdown |
| [Docker](guide/docker.md) | Docker build, MCP server, Wiki UI, PostgreSQL deployment |
| [Operations](guide/operations.md) | Deployment profiles, database choice, readiness, webhook operations |
| [Postprocess Failure Policy](guide/postprocess-failure-policy.md) | Status rules, failure causes, and automatic degraded/fail_closed policy for build and postprocess tools |
| [Runtime Layout](guide/runtime-layout.md) | `ccg`, `ccg-server`, Wiki serving, and shared `ccg-core` ownership boundaries |
| [Development](guide/development.md) | Dev guide, integration test, project structure |
| [Namespace Migration](guide/namespace-migration.md) | Default namespace change and migration guide |
| [Architecture](guide/architecture.md) | Data flow, components, DB schema |
| [CLAUDE.md Guide](guide/claude-md-guide.md) | Template for projects using CCG |

## License

MIT
