# MCP Tools

code-context-graph provides 31 MCP tools. Automatically connects from Claude Code after configuring `.mcp.json`.

## Setup

### stdio (local)

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

### Streamable HTTP (remote)

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

## Tools (31)

### Core

| Tool | Description |
|------|-------------|
| `parse_project` | Parse source files |
| `build_or_update_graph` | Full/incremental build with postprocessing |
| `run_postprocess` | Run communities/search rebuild and report stored flow rebuild as skipped |
| `get_node` | Get node by qualified name |
| `search` | Full-text search |
| `query_graph` | Predefined graph queries (callers, callees, imports, etc.) |
| `list_graph_stats` | Node/edge/file counts |
| `get_minimal_context` | Lightweight summary (~100 tokens) for AI agent entry point — graph stats, risk, top communities/flows, tool suggestions |

`run_postprocess` is primarily used to refresh derived state after graph
changes. Full-text search and communities are rebuilt when enabled; requested
stored flow rebuild is currently reported as skipped because persisted flows do
not yet have a bulk rebuild path. Use `trace_flow` for per-entry-point flow
tracing.

### Analysis

| Tool | Description |
|------|-------------|
| `get_impact_radius` | BFS blast-radius analysis |
| `trace_flow` | Call-chain flow tracing |
| `find_large_functions` | Functions exceeding line threshold |
| `find_dead_code` | Unused code detection |
| `detect_changes` | Git diff risk scoring |
| `get_affected_flows` | Flows affected by changes |
| `list_flows` | List all traced flows |

### Community & Architecture

| Tool | Description |
|------|-------------|
| `list_communities` | List module communities |
| `get_community` | Community details + coverage |
| `get_architecture_overview` | Architecture summary with coupling |

### Annotation & Documentation

| Tool | Description |
|------|-------------|
| `get_annotation` | Get annotation and doc tags |
| `build_rag_index` | Build RAG index from docs and communities (supports namespace) |
| `get_rag_tree` | Navigate RAG document tree (supports namespace) |
| `get_doc_content` | Get documentation file content (supports namespace) |
| `search_docs` | Search RAG document tree by keyword (supports namespace) |

### Namespace File Management

| Tool | Description |
|------|-------------|
| `upload_file` | Upload file to namespace (base64) |
| `upload_files` | Upload multiple files to namespaces in a single call |
| `list_namespaces` | List all namespaces |
| `list_workspaces` | Deprecated alias for `list_namespaces` |
| `list_files` | List files in a namespace |
| `delete_file` | Delete file from namespace |
| `delete_namespace` | Delete an entire namespace and all its files |
| `delete_workspace` | Deprecated alias for `delete_namespace` |

## Claude Code Skills (5)

| Skill | Description |
|-------|-------------|
| `/ccg` | Core build & search — parse, build graph, query, search |
| `/ccg-analyze` | Code analysis — impact radius, flow tracing, dead code, architecture |
| `/ccg-annotate` | Annotation system — AI-driven annotation workflow, tag reference |
| `/ccg-docs` | Documentation — generate docs, RAG indexing, lint |
| `/ccg-workspace` | Namespace file management — upload, list, and delete files and namespace directories (legacy skill name) |

### Usage

```
/ccg build .                     — Build code graph
/ccg status                      — Graph statistics
/ccg search "query"              — Full-text search
/ccg-docs docs                   — Generate documentation
/ccg-docs lint                   — Check docs health + annotation coverage
/ccg languages                   — List supported languages
/ccg-annotate annotate internal/ — AI-generate annotations
/ccg-workspace                   — Manage namespace files and directories (legacy skill name)
```
