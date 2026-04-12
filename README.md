# code-context-graph

Local code analysis tool that parses codebases via Tree-sitter into a knowledge graph. Supports 15 languages, 19 MCP tools, custom annotation search, and Apache AGE Cypher queries.

Inspired by [code-review-graph](https://github.com/tirth8205/code-review-graph) — a Python-based code analysis tool. This project reimplements and extends the concept in Go with multi-DB support, custom annotation system, and MCP integration for AI-powered code understanding.

## Features

- **15 languages**: Go, Python, TypeScript, Java, Ruby, JavaScript, C, C++, Rust, C#, Kotlin, PHP, Swift, Scala, Lua, Bash
- **19 MCP tools**: parse, search, impact analysis, flow tracing, dead code detection, Cypher queries, and more
- **Custom annotations**: `@intent`, `@domainRule`, `@sideEffect`, `@mutates`, `@index` — search code by business context
- **Apache AGE**: Cypher graph queries for path finding, blast-radius, pattern matching
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

## Apache AGE (Graph Queries)

### Setup

```bash
docker compose up age -d
```

### Build with graph sync

```bash
AGE_DSN="host=127.0.0.1 port=5455 dbname=ccg user=ccg password=ccg sslmode=disable" \
  ccg build --graph .
```

### Cypher queries

```bash
# All function call relationships
ccg query "MATCH (a:Function)-[:CALLS]->(b:Function) RETURN a.name, b.name" --columns 2

# Blast-radius (3 hops)
ccg query "MATCH ({name: 'Login'})-[*1..3]-(n) RETURN DISTINCT n.qualified_name"

# Call path between two functions
ccg query "MATCH path = (a {name: 'Handler'})-[:CALLS*]->(b {name: 'Save'}) RETURN path"

# Dead code
ccg query "MATCH (n:Function) WHERE NOT ()-[:CALLS]->(n) RETURN n.qualified_name"

# Most called functions
ccg query "MATCH ()-[:CALLS]->(n) RETURN n.name, count(*) AS c ORDER BY c DESC LIMIT 10"
```

### Graph Schema

**Vertices**: `Function`, `Class`, `Type`, `Test`, `File`

**Edges**: `CALLS`, `IMPORTS_FROM`, `INHERITS`, `IMPLEMENTS`, `CONTAINS`, `TESTED_BY`, `DEPENDS_ON`, `REFERENCES`

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

Claude Code automatically connects and gets access to 19 tools including `execute_cypher` for direct Cypher queries.

### Skill

The `/ccg` skill provides:

```
/ccg build .                  — Build code graph
/ccg status                   — Graph statistics
/ccg search "query"           — Full-text search
/ccg query "MATCH ..."        — Cypher query
/ccg annotate internal/       — AI-generate annotations
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
                            FTS Search      Apache AGE Graph
                                   ↓              ↓
                              MCP Server (19 tools)
                                        ↓
                                  Claude Code
```

## CLI Commands

| Command | Description |
|---------|-------------|
| `ccg build [dir]` | Parse and build code graph |
| `ccg build --graph [dir]` | Build + sync to Apache AGE |
| `ccg build --embed [dir]` | Build + vector embeddings |
| `ccg update [dir]` | Incremental sync |
| `ccg status` | Graph statistics |
| `ccg search <query>` | Full-text search |
| `ccg search --semantic <q>` | Semantic vector search |
| `ccg query <cypher>` | Execute Cypher query |
| `ccg serve` | Start MCP server (stdio) |

## MCP Tools (19)

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
| `execute_cypher` | Execute arbitrary Cypher queries |

## Development

```bash
# Run tests
CGO_ENABLED=1 go test -tags "fts5" ./... -count=1

# Build
CGO_ENABLED=1 go build -tags "fts5" -o ccg ./cmd/ccg/

# Docker (PostgreSQL + AGE + MySQL)
docker compose up -d
```

## License

MIT
