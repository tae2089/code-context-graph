---
name: ccg
description: code-context-graph CLI — build code knowledge graphs, search by annotations, generate documentation
user-invocable: true
---

# code-context-graph CLI Skill

A local code analysis tool that parses codebases via Tree-sitter into a knowledge graph with 16 language support and annotation-powered search.

## Subcommands

| Command | Description | Example |
|---------|-------------|---------|
| `build [dir]` | Parse directory, build graph + search index | `ccg build .` |
| `build --graph [dir]` | Build + sync to PostgreSQL + pgvector | `ccg build --graph .` |
| `build --exclude <pat>` | Exclude files/paths (repeatable) | `ccg build --exclude vendor` |
| `build --no-recursive [dir]` | Only parse top-level directory | `ccg build --no-recursive .` |
| `update [dir]` | Incremental sync (changed files only) | `ccg update .` |
| `status` | Show graph statistics (nodes/edges/files) | `ccg status` |
| `search <query>` | FTS keyword search (includes @annotations) | `ccg search "authentication"` |
| `docs [--out dir]` | Generate Markdown documentation | `ccg docs --out docs/` |
| `docs --exclude <pat>` | Exclude paths from docs (repeatable) | `ccg docs --exclude vendor` |
| `index [--out dir]` | Regenerate index.md only | `ccg index --out docs/` |
| `languages` | List all supported languages + extensions | `ccg languages` |
| `example [language]` | Show annotation example for a language | `ccg example go` |
| `tags` | Show all @tag reference with descriptions | `ccg tags` |
| `hooks install` | Install pre-commit git hook | `ccg hooks install` |
| `annotate [file\|dir]` | AI-generate annotations for code | `ccg annotate internal/analysis/` |

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
  ccg build [dir]            — Build code knowledge graph
  ccg update [dir]           — Incremental update
  ccg status                 — Graph statistics
  ccg search <query>         — Full-text search (annotations included)
  ccg docs [--out dir]       — Generate Markdown documentation
  ccg index [--out dir]      — Regenerate index.md only
  ccg languages              — List supported languages
  ccg example [language]     — Annotation writing example
  ccg tags                   — Annotation tag reference
  ccg hooks install          — Install pre-commit git hook
  ccg annotate [file|dir]    — AI-generate @annotations for code
```

## Config File (.ccg.yaml)

Project-level defaults loaded automatically from the working directory:

```yaml
db:
  driver: sqlite   # sqlite | postgres | mysql
  dsn: ccg.db

exclude:
  - vendor
  - "*.pb.go"

docs:
  out: docs
```

Override with `ccg --config path/to/.ccg.yaml`.

## Annotate Command

`ccg annotate` is NOT a CLI binary command — it is an AI-driven workflow executed by Claude.

When the user runs `ccg annotate [file|dir]`, Claude should:

### Step 1: Read target files
- If a file path is given, read that file
- If a directory is given, find all source files (`.go`, `.py`, `.ts`, `.java`, etc.)
- Skip test files, vendor, node_modules

### Step 2: Analyze each function/class/file
For each declaration, read the code and determine:
- **What it does** (→ summary line above declaration)
- **Why it exists** (→ `@intent`)
- **Business rules it enforces** (→ `@domainRule`)
- **Side effects** (→ `@sideEffect`: DB writes, API calls, file I/O, notifications)
- **What state it changes** (→ `@mutates`: fields, tables, caches)
- **Prerequisites** (→ `@requires`: auth, valid input, active state)
- **Guarantees** (→ `@ensures`: return conditions, post-state)
- **File/package purpose** (→ `@index` on package declaration)

### Step 3: Write annotations
- Add annotations as comments directly above the declaration
- Use the language's comment syntax (`//` for Go, `#` for Python, etc.)
- Do NOT overwrite existing annotations — only add missing ones
- Do NOT add trivial annotations (e.g., `@intent returns the name` for `getName()`)

### Step 4: Rebuild
After annotating, run `ccg build .` to re-index with new annotations.

### Annotation Quality Rules
- `@intent` should describe WHY, not WHAT (not "creates user" but "register new account for onboarding flow")
- `@domainRule` should be specific business logic, not generic validation
- `@sideEffect` only for actual side effects (DB, network, file, notification)
- `@index` should summarize the module's responsibility in one line
- Skip getters/setters/trivial functions — annotate functions with business meaning
- Write annotations in the same language as existing code comments (Korean if Korean, English if English)

### Example output

```go
// @index User authentication and session management service.
package auth

// AuthenticateUser validates credentials and creates a session.
// Called from login API handler.
//
// @param username user login ID
// @param password plaintext password (hashed internally)
// @return JWT token on success
// @intent verify user identity before granting system access
// @domainRule lock account after 5 consecutive failed attempts
// @domainRule locked accounts auto-unlock after 30 minutes
// @sideEffect writes login attempt to audit_log table
// @sideEffect sends security alert email on 3rd failed attempt
// @mutates user.FailedAttempts, user.LockedUntil, user.LastLoginAt
// @requires user.IsActive == true
// @ensures err == nil implies valid JWT with 24h expiry
func AuthenticateUser(username, password string) (string, error) {
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

## Supported Languages (16)

Go, Python, TypeScript, Java, Ruby, JavaScript, C, C++, Rust, C#, Kotlin, PHP, Swift, Scala, Lua, Bash

Run `ccg languages` for the full list with extensions.
