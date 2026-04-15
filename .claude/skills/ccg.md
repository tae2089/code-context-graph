---
name: ccg
description: code-context-graph — build code knowledge graphs and search. Core entry point for parsing, building, and querying code graphs.
user-invocable: true
---

# code-context-graph — Core Build & Search

Local code analysis tool that parses codebases via Tree-sitter into a knowledge graph with 12 language support and annotation-powered search.

## Subcommands

| Command | Description | Example |
|---------|-------------|---------|
| `build [dir]` | Parse directory, build graph + search index | `ccg build .` |
| `build --exclude <pat>` | Exclude files/paths (repeatable) | `ccg build --exclude vendor` |
| `build --no-recursive [dir]` | Only parse top-level directory | `ccg build --no-recursive .` |
| `update [dir]` | Incremental sync (changed files only) | `ccg update .` |
| `status` | Show graph statistics (nodes/edges/files) | `ccg status` |
| `search <query>` | FTS keyword search (includes @annotations) | `ccg search "authentication"` |
| `search --path <prefix> <query>` | Scoped search by path prefix | `ccg search --path internal/auth "login"` |
| `languages` | List supported languages and extensions | `ccg languages` |
| `example [language]` | Show annotation writing example | `ccg example go` |
| `tags` | Show all annotation tag reference | `ccg tags` |
| `serve` | Start MCP server (stdio by default) | `ccg serve` |
| `serve --transport streamable-http` | Start MCP server over HTTP | `ccg serve --transport streamable-http` |
| `serve --http-addr :9090` | Custom HTTP listen address | `ccg serve --http-addr :9090` |
| `serve --stateless` | Stateless session mode | `ccg serve --stateless` |
| `serve --allow-repo <pat>` | Allowed repos for webhook sync (repeatable) | `ccg serve --allow-repo "org/*"` |
| `serve --webhook-secret <s>` | HMAC secret for webhook verification | `ccg serve --webhook-secret mysecret` |
| `serve --repo-root <dir>` | Root dir for cloned repos | `ccg serve --repo-root /data/repos` |

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
  ccg build [dir]           — Build code knowledge graph
  ccg update [dir]          — Incremental update
  ccg status                — Graph statistics
  ccg search <query>        — Full-text search (annotations included)
  ccg languages             — List supported languages
  ccg serve                 — Start MCP server

Related skills:
  /ccg-analyze              — Code analysis & architecture
  /ccg-annotate             — Annotation system & AI workflow
  /ccg-docs                 — Documentation & RAG indexing
  /ccg-workspace            — File workspace management
```

## MCP Tools (7)

| Tool | Description |
|------|-------------|
| `parse_project` | Parse source files |
| `build_or_update_graph` | Full/incremental build with postprocessing |
| `run_postprocess` | Run flows/communities/search rebuild |
| `get_node` | Get node by qualified name |
| `search` | Full-text search |
| `query_graph` | Predefined graph queries (callers, callees, imports, etc.) |
| `list_graph_stats` | Node/edge/file counts |

## Smart Behaviors

### Auto-rebuild when stale
If `ccg.db` doesn't exist or the user asks to analyze the project, run `ccg build .` first.

### Annotation-aware search
When the user asks about business concepts, use FTS search which includes annotation content:
- `@intent` — function purpose/goal
- `@domainRule` — business rules
- `@sideEffect` — side effects
- `@mutates` — state changes
- `@index` — file/package level description

Example: user asks "결제 관련 코드" → `ccg search "결제"` finds functions annotated with payment-related @intent/@domainRule.

## Graph Schema

Node kinds: `function`, `class`, `type`, `test`, `file`

Edge kinds: `calls`, `imports_from`, `inherits`, `implements`, `contains`, `tested_by`, `depends_on`, `references`

## Supported Languages (12)

Go, Python, TypeScript, Java, Ruby, JavaScript, C, C++, Rust, Kotlin, PHP, Lua

## HTTP Endpoints (Streamable HTTP mode)

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/mcp` | POST/GET/DELETE | MCP protocol endpoint (session-based) |
| `/health` | GET | Health check — returns `{"status":"ok"}` |
| `/webhook` | POST | GitHub / Gitea webhook receiver (when `--allow-repo` configured). Supports `X-Hub-Signature-256` and `X-Gitea-Signature` HMAC verification. |
