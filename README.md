# code-context-graph

Local code analysis tool that parses codebases via Tree-sitter into a knowledge graph. Supports 12 languages, 28 MCP tools, and custom annotation search.

Inspired by [code-review-graph](https://github.com/tirth8205/code-review-graph) — a Python-based code analysis tool. This project reimplements and extends the concept in Go with multi-DB support, custom annotation system, and MCP integration for AI-powered code understanding.

## Features

- **12 languages**: Go, Python, TypeScript, Java, Ruby, JavaScript, C, C++, Rust, Kotlin, PHP, Lua
- **28 MCP tools**: parse, search, impact analysis, flow tracing, dead code detection, file workspace management, and more
- **Custom annotations**: `@intent`, `@domainRule`, `@sideEffect`, `@mutates`, `@index` — search code by business context
- **Multi-DB**: SQLite (local), PostgreSQL
- **Full-text search**: FTS5 (SQLite), tsvector+GIN (PostgreSQL)

## Installation

### npm / bun (recommended)

```bash
npm install -g code-context-graph
# or
bun install -g code-context-graph
```

### go install

```bash
go install github.com/tae2089/code-context-graph/cmd/ccg@latest
```

### Build from source

```bash
CGO_ENABLED=1 go build -tags "fts5" -o ccg ./cmd/ccg/
```

## Quick Start

### Parse your project

```bash
ccg build .
```

```
Build complete: 70 files, 749 nodes, 7387 edges
```

### Search

```bash
# Keyword search (includes annotations)
ccg search "authentication"

# Search by business context
ccg search "결제"       # finds functions with @intent/@domainRule about payments
ccg search "dead code"  # finds deadcode.Find via @intent annotation

# Scope to a specific path prefix (token-efficient)
ccg search --path internal/auth "login"
ccg search --path internal/payment "handle" --limit 5
```

### Status

```bash
ccg status
```

```
Nodes: 747
Edges: 6780
Files: 70

Node kinds:
  function: 238
  test: 339
  class: 83
  type: 17
  file: 70

Edge kinds:
  calls: 5124
  contains: 679
  tested_by: 543
  imports_from: 434
```

## Custom Annotations

Add structured metadata to your code. These are indexed and searchable.

### File-level

```go
// @index User authentication and session management service.
package auth
```

### Function-level

```go
// AuthenticateUser validates credentials and creates a session.
// Called from login API handler.
//
// @param username user login ID
// @param password plaintext password
// @return JWT token on success
// @intent verify user identity before granting system access
// @domainRule lock account after 5 consecutive failed attempts
// @sideEffect writes login attempt to audit_log table
// @mutates user.FailedAttempts, user.LockedUntil
// @requires user.IsActive == true
// @ensures err == nil implies valid JWT with 24h expiry
func AuthenticateUser(username, password string) (string, error) {
```

### Available Tags

| Tag | Purpose | Example |
|-----|---------|---------|
| `@index` | File/package description | `@index Payment processing service` |
| `@intent` | Why this function exists | `@intent verify credentials before session creation` |
| `@domainRule` | Business rule | `@domainRule lock account after 5 failures` |
| `@sideEffect` | Side effects | `@sideEffect sends notification email` |
| `@mutates` | State changes | `@mutates user.FailedAttempts, session.Token` |
| `@requires` | Precondition | `@requires user.IsActive == true` |
| `@ensures` | Postcondition | `@ensures session != nil` |
| `@param` | Parameter description | `@param username the login ID` |
| `@return` | Return description | `@return JWT token on success` |
| `@see` | Related function | `@see SessionManager.Create` |

## Claude Code Integration

### MCP Server

Add `.mcp.json` to your project:

```json
{
  "mcpServers": {
    "ccg": {
      "command": "ccg",
      "args": ["serve", "--db", "sqlite", "--dsn", "ccg.db"]
    }
  }
}
```

For remote HTTP mode:

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

Health check endpoint (HTTP mode only):

```bash
curl http://localhost:8080/health
# {"status":"ok"}
```

Claude Code automatically connects and gets access to 28 MCP tools.

### Skills (5)

| Skill | Description |
|-------|-------------|
| `/ccg` | Core build & search — parse, build graph, query, search |
| `/ccg-analyze` | Code analysis — impact radius, flow tracing, dead code, architecture |
| `/ccg-annotate` | Annotation system — AI-driven annotation workflow, tag reference |
| `/ccg-docs` | Documentation — generate docs, RAG indexing, lint |
| `/ccg-workspace` | File workspace — upload, list, delete files and workspaces |

```
/ccg build .                    — Build code graph
/ccg status                     — Graph statistics
/ccg search "query"             — Full-text search
/ccg-docs docs                  — Generate documentation
/ccg-docs lint                  — Check docs health + annotation coverage
/ccg languages                  — List supported languages
/ccg-annotate annotate internal/— AI-generate annotations
/ccg-workspace                  — Manage file workspaces
```

### AI-Driven Annotation

Claude can auto-generate annotations for your codebase:

```
You: "이 프로젝트에 어노테이션 달아줘"
Claude: reads code → generates @intent, @domainRule, @sideEffect, @mutates
      → writes annotations → rebuilds index
      → now searchable by business context
```

## Architecture

```
Source Code → Tree-sitter Parser → Nodes + Edges + Annotations
                                        ↓
                              SQLite / PostgreSQL (GORM)
                                        ↓
                                   FTS Search
                                        ↓
                              MCP Server (28 tools)
                                    ↓         ↓
                              stdio       Streamable HTTP
                                ↓              ↓
                           Claude Code    Remote Clients
```

## CLI Commands

| Command | Description |
|---------|-------------|
| `ccg init` | Generate default .ccg.yaml in current directory |
| `ccg init --project` | Generate .ccg.yaml in current directory (explicit) |
| `ccg init --user` | Generate .ccg.yaml in ~/.config/ccg/ (global) |
| `ccg build [dir]` | Parse and build code graph |
| `ccg build --exclude <pat>` | Exclude files/paths (repeatable) |
| `ccg build --no-recursive [dir]` | Only parse top-level directory |
| `ccg update [dir]` | Incremental sync |
| `ccg status` | Graph statistics |
| `ccg search <query>` | Full-text search |
| `ccg search --path <prefix> <query>` | Scoped search by path prefix |
| `ccg docs [--out dir]` | Generate Markdown documentation |
| `ccg index [--out dir]` | Regenerate index.md only |
| `ccg languages` | List supported languages and extensions |
| `ccg example [language]` | Show annotation writing example |
| `ccg tags` | Show all annotation tag reference |
| `ccg hooks install` | Install pre-commit git hook |
| `ccg hooks install --lint-strict` | Install hook that blocks commit on issues |
| `ccg lint [--out dir]` | 8-category docs lint (orphan, missing, stale, unannotated, contradiction, dead-ref, incomplete, drift) |
| `ccg lint --strict` | Exit 1 on issues (for CI/pre-commit); ignores rules with `action: ignore` |
| `ccg serve` | Start MCP server (stdio by default) |
| `ccg serve --transport streamable-http` | Start MCP server over HTTP |
| `ccg serve --http-addr :9090` | Custom HTTP listen address (default `:8080`) |
| `ccg serve --stateless` | Stateless session mode (multi-instance deployments) |
| `ccg serve --workspace-root <dir>` | Root directory for file workspaces (default `workspaces`) |

### Config file (`.ccg.yaml`)

Project-level defaults loaded automatically from the current directory, with a global fallback at `~/.config/ccg/.ccg.yaml`:

```yaml
db:
  driver: sqlite   # sqlite | postgres
  dsn: ccg.db

exclude:
  - vendor
  - ".*\\.pb\\.go$"
  - ".*_test\\.go$"

docs:
  out: docs
```

Both `exclude` and `rules` pattern fields support regex. Patterns containing `$`, `^`, `+`, `{}`, `|`, `\.`, or `.*` are auto-detected as regex:

```yaml
rules:
  - pattern: "pkg/store/.*"
    category: unannotated
    action: ignore

  - pattern: ".*_generated\\.go::.*"
    category: incomplete
    action: warn
```

Config search order:
1. `./.ccg.yaml` (project-local, highest priority)
2. `~/.config/ccg/.ccg.yaml` (global fallback)

Override with `ccg --config path/to/config.yaml`.

## MCP Tools (28)

| Tool | Description |
|------|-------------|
| `parse_project` | Parse source files |
| `build_or_update_graph` | Full/incremental build with postprocessing |
| `run_postprocess` | Run flows/communities/search rebuild |
| `get_node` | Get node by qualified name |
| `search` | Full-text search |
| `query_graph` | Predefined graph queries (callers, callees, imports, etc.) |
| `list_graph_stats` | Node/edge/file counts |
| `get_impact_radius` | BFS blast-radius analysis |
| `trace_flow` | Call-chain flow tracing |
| `find_large_functions` | Functions exceeding line threshold |
| `find_dead_code` | Unused code detection |
| `detect_changes` | Git diff risk scoring |
| `get_affected_flows` | Flows affected by changes |
| `list_flows` | List all traced flows |
| `list_communities` | List module communities |
| `get_community` | Community details + coverage |
| `get_architecture_overview` | Architecture summary with coupling |
| `get_annotation` | Get annotation and doc tags |
| `build_rag_index` | Build RAG index from docs and communities (supports workspace) |
| `get_rag_tree` | Navigate RAG document tree (supports workspace) |
| `get_doc_content` | Get documentation file content (supports workspace) |
| `search_docs` | Search RAG document tree by keyword (supports workspace) |
| `upload_file` | Upload file to workspace (base64) |
| `upload_files` | Upload multiple files to workspaces in a single call |
| `list_workspaces` | List all workspaces |
| `list_files` | List files in a workspace |
| `delete_file` | Delete file from workspace |
| `delete_workspace` | Delete an entire workspace and all its files |

## Docker

### Build image

```bash
docker build -t ccg .
```

### Run as MCP server

```bash
# Mount your project and serve over HTTP
docker run -d -p 8080:8080 -v $(pwd):/workspace --entrypoint sh ccg \
  -c "ccg build /workspace && ccg serve --transport streamable-http --http-addr :8080"
```

Then connect from `.mcp.json`:

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

### Run with PostgreSQL

```bash
docker run -d -p 8080:8080 \
  -e CCG_DB_DRIVER=postgres \
  -e CCG_DB_DSN="host=db user=ccg password=ccg dbname=ccg sslmode=disable" \
  -v $(pwd):/workspace --entrypoint sh ccg \
  -c "ccg build /workspace && ccg serve --transport streamable-http --http-addr :8080"
```

## Development

```bash
# Run tests
CGO_ENABLED=1 go test -tags "fts5" ./... -count=1

# Build
CGO_ENABLED=1 go build -tags "fts5" -o ccg ./cmd/ccg/

# Docker (PostgreSQL)
docker compose up -d
```

## Future Work

- **Contradiction precise mode**: Tree-sitter re-parse to extract function parameter names and compare 1:1 with @param tag names (currently detects signature changes via node hash + timestamp only)
- **`ccg lint --fix`**: Auto-fix stale (re-run docs), orphan (delete doc), dead-ref (remove @see tag)
- **Severity levels**: Per-category default severity (info/warn/error) configurable in .ccg.yaml

## License

MIT
