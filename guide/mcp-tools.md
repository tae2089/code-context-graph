# MCP Tools

code-context-graph는 29개 MCP 도구를 제공합니다. Claude Code에서 `.mcp.json` 설정 후 자동 연결됩니다.

## Setup

### stdio (로컬)

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

### Streamable HTTP (원격)

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

## Tools (29)

### Core

| Tool | Description |
|------|-------------|
| `parse_project` | Parse source files |
| `build_or_update_graph` | Full/incremental build with postprocessing |
| `run_postprocess` | Run flows/communities/search rebuild |
| `get_node` | Get node by qualified name |
| `search` | Full-text search |
| `query_graph` | Predefined graph queries (callers, callees, imports, etc.) |
| `list_graph_stats` | Node/edge/file counts |
| `get_minimal_context` | Lightweight summary (~100 tokens) for AI agent entry point — graph stats, risk, top communities/flows, tool suggestions |

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
| `build_rag_index` | Build RAG index from docs and communities (supports workspace) |
| `get_rag_tree` | Navigate RAG document tree (supports workspace) |
| `get_doc_content` | Get documentation file content (supports workspace) |
| `search_docs` | Search RAG document tree by keyword (supports workspace) |

### Workspace

| Tool | Description |
|------|-------------|
| `upload_file` | Upload file to workspace (base64) |
| `upload_files` | Upload multiple files to workspaces in a single call |
| `list_workspaces` | List all workspaces |
| `list_files` | List files in a workspace |
| `delete_file` | Delete file from workspace |
| `delete_workspace` | Delete an entire workspace and all its files |

## Claude Code Skills (5)

| Skill | Description |
|-------|-------------|
| `/ccg` | Core build & search — parse, build graph, query, search |
| `/ccg-analyze` | Code analysis — impact radius, flow tracing, dead code, architecture |
| `/ccg-annotate` | Annotation system — AI-driven annotation workflow, tag reference |
| `/ccg-docs` | Documentation — generate docs, RAG indexing, lint |
| `/ccg-workspace` | File workspace — upload, list, delete files and workspaces |

### Usage

```
/ccg build .                     — Build code graph
/ccg status                      — Graph statistics
/ccg search "query"              — Full-text search
/ccg-docs docs                   — Generate documentation
/ccg-docs lint                   — Check docs health + annotation coverage
/ccg languages                   — List supported languages
/ccg-annotate annotate internal/ — AI-generate annotations
/ccg-workspace                   — Manage file workspaces
```
