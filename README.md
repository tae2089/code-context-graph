# code-context-graph

Local code analysis tool that parses codebases via Tree-sitter into a knowledge graph. Supports 16 languages, 18 MCP tools, custom annotation search, and pgvector semantic search.

Inspired by [code-review-graph](https://github.com/tirth8205/code-review-graph) — a Python-based code analysis tool. This project reimplements and extends the concept in Go with multi-DB support, custom annotation system, and MCP integration for AI-powered code understanding.

## Features

- **16 languages**: Go, Python, TypeScript, Java, Ruby, JavaScript, C, C++, Rust, C#, Kotlin, PHP, Swift, Scala, Lua, Bash
- **18 MCP tools**: parse, search, impact analysis, flow tracing, dead code detection, and more
- **Custom annotations**: `@intent`, `@domainRule`, `@sideEffect`, `@mutates`, `@index` — search code by business context
- **pgvector**: semantic similarity search via PostgreSQL pgvector extension
- **Multi-DB**: SQLite (local), PostgreSQL, MySQL
- **Full-text search**: FTS5 (SQLite), tsvector+GIN (PostgreSQL), FULLTEXT (MySQL)

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

## PostgreSQL + pgvector (Semantic Search)

### Setup

```bash
docker compose up pgvector -d
```

### Build with pgvector sync

```bash
PG_DSN="host=127.0.0.1 port=5455 dbname=ccg user=ccg password=ccg sslmode=disable" \
  ccg build --graph .
```

This stores nodes, edges, and annotation content in PostgreSQL with pgvector, enabling semantic similarity search via the pgvector MCP server.

## Claude Code Integration

### MCP Server

Add `.mcp.json` to your project:

```json
{
  "mcpServers": {
    "ccg": {
      "command": "ccg",
      "args": ["serve", "--db", "sqlite", "--dsn", "ccg.db"]
    },
    "pgvector": {
      "command": "npx",
      "args": ["-y", "mcp-pgvector-server"],
      "env": {
        "DATABASE_URL": "postgresql://ccg:ccg@localhost:5455/ccg"
      }
    }
  }
}
```

Claude Code automatically connects and gets access to 18 MCP tools + pgvector semantic search.

### Skill

The `/ccg` skill provides:

```
/ccg build .                    — Build code graph
/ccg status                     — Graph statistics
/ccg search "query"             — Full-text search
/ccg docs                       — Generate documentation
/ccg lint                       — Check docs health + annotation coverage
/ccg languages                  — List supported languages
/ccg example go                 — Annotation writing example
/ccg tags                       — Annotation tag reference
/ccg annotate internal/         — AI-generate annotations
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
                                   ↓              ↓
                            FTS Search      pgvector (semantic)
                                   ↓
                              MCP Server (18 tools)
                                        ↓
                                  Claude Code
```

## CLI Commands

| Command | Description |
|---------|-------------|
| `ccg build [dir]` | Parse and build code graph |
| `ccg build --graph [dir]` | Build + sync to PostgreSQL + pgvector |
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
| `ccg serve` | Start MCP server (stdio) |

### Config file (`.ccg.yaml`)

Project-level defaults loaded automatically from the current directory:

```yaml
db:
  driver: sqlite   # sqlite | postgres | mysql
  dsn: ccg.db

exclude:
  - vendor
  - "*.pb.go"
  - "*_test.go"

docs:
  out: docs
```

Override with `ccg --config path/to/config.yaml`.

## MCP Tools (18)

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

## Development

```bash
# Run tests
CGO_ENABLED=1 go test -tags "fts5" ./... -count=1

# Build
CGO_ENABLED=1 go build -tags "fts5" -o ccg ./cmd/ccg/

# Docker (PostgreSQL + pgvector + MySQL)
docker compose up -d
```

## Future Work

- **Contradiction precise mode**: Tree-sitter re-parse to extract function parameter names and compare 1:1 with @param tag names (currently detects signature changes via node hash + timestamp only)
- **`ccg lint --fix`**: Auto-fix stale (re-run docs), orphan (delete doc), dead-ref (remove @see tag)
- **Severity levels**: Per-category default severity (info/warn/error) configurable in .ccg.yaml

## License

MIT
