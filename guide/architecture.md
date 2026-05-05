# Architecture

## Data Flow

```
Source Code → Tree-sitter Parser → Nodes + Edges + Annotations
                                        ↓
                              SQLite / PostgreSQL (GORM)
                                        ↓
                                   FTS Search
                                        ↓
                         ccg serve          ccg-server
                         stdio MCP       Streamable HTTP
                            ↓             ↓          ↑
                     Coding Agents   Remote Clients  GitHub / Gitea Webhook
                                             push → clone → build → DB
```

## Components

### Parser (`internal/parse/treesitter/`)

Tree-sitter based code parser. Supports 12 languages. Each language has its own `LangSpec` that extracts functions, classes, types, imports, and call relationships.

**Supported languages**: Go, Python, TypeScript, Java, Ruby, JavaScript, C, C++, Rust, Kotlin, PHP, Lua/Luau

> The Lua parser also supports Luau (Roblox) syntax. Type annotations are silently ignored via tree-sitter error recovery. Extracts functions (global, local, method) and comments (single-line, block, `--!strict`).

### Store (`internal/store/gormstore/`)

GORM ORM-based store. Compatible with SQLite and PostgreSQL.

- **Node**: functions, classes, types, files, etc.
- **Edge**: calls, contains, tested_by, imports_from, etc.
- **SearchDocument**: documents for FTS search
- **Flow/FlowMembership**: execution flows

### Search (`internal/store/search/`)

Per-database full-text search backends:
- **SQLite**: FTS5
- **PostgreSQL**: tsvector + GIN index

Full builds and explicit postprocess runs rebuild namespace search state.
Incremental updates refresh only affected search documents and FTS rows, while
community postprocessing can still be namespace-wide. Persisted stored-flow
rebuild is implemented for full postprocess runs and explicit
`run_postprocess(flows=true)` calls; use `trace_flow` for per-entry-point flow
queries.

### Analysis (`internal/analysis/`)

| Module | Description |
|--------|-------------|
| `impact` | BFS blast-radius analysis |
| `flows` | Call-chain flow tracing |
| `deadcode` | Unused code detection |
| `community` | Directory-based module communities |
| `coupling` | Inter-module coupling analysis |
| `coverage` | Test coverage analysis |
| `largefunc` | Large function detection |
| `changes` | Git diff risk scoring |
| `query` | Graph queries (callers, callees, imports) |
| `incremental` | Incremental update |

### Eval (`internal/eval/`)

Golden corpus-based parser accuracy and search quality evaluation framework.

- **Parser eval**: Parses 12-language source files and compares against golden JSON to compute Node/Edge P/R/F1
- **Search eval**: Computes P@K, MRR, nDCG metrics for query corpus
- **Golden update**: `--update` mode saves current parser output as golden files
- **Corpus**: `testdata/eval/` directory with per-language sources + golden JSON + queries.json

### MCP Server (`internal/mcp/`)

Exposes 35 tools via MCP protocol. The local `ccg serve` command exposes these
tools over stdio. The self-hosted `ccg-server` binary exposes the same tool
surface over Streamable HTTP and adds health/status/webhook endpoints.

### Runtime Layout

CCG has separate runtime entry points for local and self-hosted use:

| Runtime | Code | Responsibility |
|---------|------|----------------|
| `ccg` | `cmd/ccg`, `internal/cli` | Local CLI commands and stdio MCP |
| `ccg-server` | `cmd/ccg-server`, `internal/server` | Streamable HTTP MCP, health/status endpoints, webhook sync |
| `ccg-core` | `internal/core` | Shared parser, DB, store, search, migration, and incremental-sync wiring |

See [Runtime Layout](runtime-layout.md) for ownership boundaries and migration
notes from `ccg serve --transport streamable-http` to `ccg-server`.

### Reliability

Panic recovery is applied to all goroutines for operational stability:

- **Signal handler**: logs error on panic then calls `os.Exit(2)`
- **HTTP server**: propagates panic to error channel for graceful shutdown flow
- **SyncQueue worker**: logs panic without affecting other workers
- **SyncQueue shutdown**: isolates panics during shutdown

### Webhook (`internal/webhook/`)

Receives GitHub/Gitea push events → automatic clone/build pipeline.

- **RepoFilter**: Per-repo branch filtering (`IsAllowedRef`)
- **SyncQueue**: Deduplication + concurrency-controlled worker queue. On handler failure, exponential backoff retry (default 3 attempts, 1s→2s→4s, max 30s)
- **CloneOrPull**: go-git based clone/pull (SSH key and app token support)

## Database Schema

### Core Tables

```
nodes                 — namespace, qualified_name, kind, file_path, start_line, end_line, language, ...
edges                 — namespace, from_node_id, to_node_id, kind, file_path, line, fingerprint
search_documents      — namespace, node_id, content, language (FTS indexed)
communities           — namespace, key, label, strategy, description
community_memberships — community_id, node_id
flows                 — namespace, name, description
flow_memberships      — namespace, flow_id, node_id, ordinal
```

### Namespace Isolation

Namespace is stored in the context and automatically extracted inside the store. Provides data isolation in multi-repo environments.
