---
name: ccg
description: code-context-graph CLI — build code knowledge graphs, search by annotations, execute Cypher queries
user-invocable: true
---

# code-context-graph CLI Skill

A local code analysis tool that parses codebases via Tree-sitter into a knowledge graph with 15 language support, annotation-powered search, and Apache AGE Cypher queries.

## Subcommands

| Command | Description | Example |
|---------|-------------|---------|
| `build [dir]` | Parse directory, build graph + search index | `ccg build .` |
| `build --graph [dir]` | Build + sync to Apache AGE graph DB | `ccg build --graph .` |
| `build --embed [dir]` | Build + generate vector embeddings | `ccg build --embed .` |
| `update [dir]` | Incremental sync (changed files only) | `ccg update .` |
| `status` | Show graph statistics (nodes/edges/files) | `ccg status` |
| `search <query>` | FTS keyword search (includes @annotations) | `ccg search "authentication"` |
| `search --semantic <q>` | Vector similarity search | `ccg search --semantic "payment flow"` |
| `query <cypher>` | Execute Cypher query on AGE graph | `ccg query "MATCH (n:Function) RETURN n"` |

## Execution

Parse the user's input after `ccg` and run via Bash:

```bash
./ccg {subcommand} {args}
```

If the binary doesn't exist, build it first:

```bash
CGO_ENABLED=1 go build -tags "fts5" -o ccg ./cmd/ccg/
```

## When no arguments provided

Show available commands:

```
Available ccg commands:
  ccg build [dir]       — Build code knowledge graph
  ccg update [dir]      — Incremental update
  ccg status            — Graph statistics
  ccg search <query>    — Full-text search (annotations included)
  ccg query <cypher>    — Execute Cypher query (requires AGE)
```

## Smart Behaviors

### Auto-rebuild when stale
If `ccg.db` doesn't exist or the user asks to analyze the project, run `ccg build .` first.

### Suggest Cypher queries
When the user asks graph-related questions, suggest and execute appropriate Cypher:

| User intent | Suggested Cypher |
|-------------|------------------|
| "What calls this function?" | `MATCH (a)-[:CALLS]->(b {name: 'X'}) RETURN a.qualified_name` |
| "Impact of changing X" | `MATCH ({name: 'X'})-[*1..3]-(affected) RETURN DISTINCT affected.qualified_name` |
| "Path from A to B" | `MATCH path = (a {name: 'A'})-[:CALLS*]->(b {name: 'B'}) RETURN path` |
| "Dead code" | `MATCH (n:Function) WHERE NOT ()-[:CALLS]->(n) RETURN n.qualified_name` |
| "Most called functions" | `MATCH ()-[:CALLS]->(n) RETURN n.name, count(*) AS c ORDER BY c DESC LIMIT 10` |
| "Module dependencies" | `MATCH (a)-[:CALLS]->(b) WHERE a.file_path STARTS WITH 'X' RETURN DISTINCT b.file_path` |

### Annotation-aware search
When the user asks about business concepts, use FTS search which includes annotation content:
- `@intent` — function purpose/goal
- `@domainRule` — business rules
- `@sideEffect` — side effects
- `@mutates` — state changes
- `@index` — file/package level description

Example: user asks "결제 관련 코드" → `ccg search "결제"` finds functions annotated with payment-related @intent/@domainRule.

### Multi-column Cypher
When RETURN has multiple values, use `--columns N`:

```bash
ccg query "MATCH (a)-[:CALLS]->(b) RETURN a.name, b.name" --columns 2
```

## Graph Schema

Vertex labels: `Function`, `Class`, `Type`, `Test`, `File`

Edge labels: `CALLS`, `IMPORTS_FROM`, `INHERITS`, `IMPLEMENTS`, `CONTAINS`, `TESTED_BY`, `DEPENDS_ON`, `REFERENCES`

Vertex properties: `node_id`, `qualified_name`, `name`, `kind`, `file_path`, `language`, `start_line`, `end_line`

## Supported Languages (15)

Go, Python, TypeScript, Java, Ruby, JavaScript, C, C++, Rust, C#, Kotlin, PHP, Swift, Scala, Lua, Bash
