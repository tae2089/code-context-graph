# code-context-graph

Local code analysis tool that parses codebases via Tree-sitter into a knowledge graph. Supports 12 languages, 29 MCP tools, and custom annotation search.

Inspired by [code-review-graph](https://github.com/tirth8205/code-review-graph) — a Python-based code analysis tool. This project reimplements and extends the concept in Go with multi-DB support, custom annotation system, and MCP integration for AI-powered code understanding.

## Features

- **12 languages**: Go, Python, TypeScript, Java, Ruby, JavaScript, C, C++, Rust, Kotlin, PHP, Lua/Luau
- **29 MCP tools**: parse, search, impact analysis, flow tracing, dead code detection, file workspace management, and more
- **Custom annotations**: `@intent`, `@domainRule`, `@sideEffect`, `@mutates`, `@index` — search code by business context ([details](guide/annotations.md))
- **Webhook sync**: GitHub / Gitea push events → auto clone + build with per-repo branch filtering and `.ccg.yaml` `include_paths` auto-loading ([details](guide/webhook.md))
- **Eval**: Golden corpus 기반 파서 정확도(P/R/F1) 및 검색 품질(P@K, MRR, nDCG) 평가 ([details](guide/cli-reference.md#eval))
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

# Or use Makefile (injects version from git tag automatically)
make build
```

## Quick Start

```bash
# Parse your project
ccg build .
# Build complete: 70 files, 749 nodes, 7387 edges

# Search (includes annotations)
ccg search "authentication"

# Search by business context
ccg search "결제"       # finds functions with @intent/@domainRule about payments

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

## Demo

CCG가 자기 자신의 코드베이스를 파싱한 실제 출력입니다.

### 1. 코드베이스 파싱

```
$ ccg build .
Build complete: 127 files, 1220 nodes, 12222 edges
```

### 2. 그래프 통계

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
```

### 3. 코드 검색

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

### 4. MCP를 통한 Claude 연동 예시

`.mcp.json` 설정 후 Claude Code에서 바로 질문할 수 있습니다:

> **"이 프로젝트의 webhook sync 흐름을 설명해줘"**

Claude가 CCG MCP 도구를 호출해 그래프에서 직접 답변:

```
ccg_trace_flow("webhook.WebhookHandler.ServeHTTP")
→ WebhookHandler.ServeHTTP
  → SyncQueue.Enqueue
    → safeHandle (retry loop: max 3회, exponential backoff 1s→30s)
      → clone (git clone, 15분 timeout)
      → build (ccg build, 동일 context)
```

> **"인증 관련 코드가 어디 있어?"**

```
ccg_search("authentication")
→ internal/webhook/handler.go  (HMAC signature validation)
→ cmd/ccg/main.go              (--webhook-secret flag)
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

Claude Code automatically connects and gets access to 29 MCP tools. See [MCP Tools Reference](guide/mcp-tools.md) for the full list.

## Architecture

```
Source Code → Tree-sitter Parser → Nodes + Edges + Annotations
                                        ↓
                              SQLite / PostgreSQL (GORM)
                                        ↓
                                   FTS Search
                                        ↓
                              MCP Server (29 tools)
                                    ↓         ↓
                              stdio       Streamable HTTP
                                ↓              ↓
                           Claude Code    Remote Clients
                                               ↑
                                GitHub / Gitea Webhook
                                    push → clone → build → DB
```

See [Architecture Details](guide/architecture.md) for component breakdown and DB schema.

## Documentation

| Guide | Description |
|-------|-------------|
| [CLI Reference](guide/cli-reference.md) | All commands, flags, and config file (`.ccg.yaml`) |
| [MCP Tools](guide/mcp-tools.md) | 29 MCP tools, Skills, AI-Driven Annotation |
| [Annotations](guide/annotations.md) | Annotation system — tags, examples, search |
| [Webhook](guide/webhook.md) | Webhook sync, branch filtering, HMAC, graceful shutdown |
| [Docker](guide/docker.md) | Docker build, MCP server, PostgreSQL deployment |
| [Development](guide/development.md) | Dev guide, integration test, project structure |
| [Architecture](guide/architecture.md) | Data flow, components, DB schema |
| [CLAUDE.md Guide](guide/claude-md-guide.md) | Template for projects using CCG |

## License

MIT
