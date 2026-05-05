# MCP Tools

code-context-graph provides 35 MCP tools. Automatically connects from Claude Code after configuring `.mcp.json`.

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

## Tools (33)

### Core

| Tool | Description |
|------|-------------|
| `parse_project` | Parse source files |
| `build_or_update_graph` | Full/incremental build with postprocessing |
| `run_postprocess` | Rebuild stored flows, communities, and/or full-text search derived state |
| `get_postprocess_policy` | Inspect automatic postprocess policy state and recent failures |
| `reset_postprocess_policy` | Record a reset marker to clear fail-closed streak for one tool |
| `get_node` | Get node by qualified name |
| `search` | Full-text search |
| `query_graph` | Predefined graph queries (callers, callees, imports, etc.) |
| `list_graph_stats` | Node/edge/file counts |
| `get_minimal_context` | Lightweight summary (~100 tokens) for AI agent entry point — graph stats, risk, top communities/flows, tool suggestions |

`build_or_update_graph` and `run_postprocess` both support an automatic
postprocess failure policy. When no explicit `postprocess_policy` is provided,
CCG defaults to `degraded` and automatically escalates to `fail_closed` after
three consecutive `degraded` runs for the same `(namespace, tool)` pair.

See [Postprocess Failure Policy](postprocess-failure-policy.md) for the detailed
status tables, failure causes, skip behavior, and policy escalation rules for
`build_or_update_graph` and `run_postprocess`.

CCG does not expose a Prometheus `/metrics` endpoint yet. For postprocess
operations, use `get_postprocess_policy` and the HTTP `/status` summary as the
current machine-readable operational surfaces.

### Analysis

| Tool | Description |
|------|-------------|
| `get_impact_radius` | BFS blast-radius analysis |
| `trace_flow` | Call-chain flow tracing |
| `find_large_functions` | Functions exceeding line threshold; supports `limit` |
| `find_dead_code` | Unused code detection |
| `detect_changes` | Git diff risk scoring |
| `get_affected_flows` | Flows affected by changes |
| `list_flows` | List traced flows with `limit` / `offset` pagination |

### Community & Architecture

| Tool | Description |
|------|-------------|
| `list_communities` | List module communities with `limit` / `offset` pagination |
| `get_community` | Community details + coverage; member listing supports `member_limit` / `member_offset` |
| `get_architecture_overview` | Architecture summary with community and coupling pagination |

### Pagination and Response Budgets

Use paginated graph tools when a namespace may contain many flows,
communities, members, or coupling pairs. Paginated responses include
`has_more`; when it is true, call the same tool again with `next_offset`.

| Tool | Pagination Parameters | Notes |
|------|-----------------------|-------|
| `query_graph` | `limit`, `offset` | Max `limit` is 500 |
| `list_flows` | `limit`, `offset` | Response includes `pagination` |
| `list_communities` | `limit`, `offset` | Response includes `pagination` |
| `get_community` | `member_limit`, `member_offset` | Applies when `include_members=true`; response includes `members_pagination` |
| `get_architecture_overview` | `community_limit`, `community_offset`, `coupling_limit`, `coupling_offset` | Response includes separate community and coupling pagination objects |

Some analysis tools still return full result sets internally. On large
namespaces, prefer scoped inputs before calling `find_dead_code`,
`find_suspect_fallback_edges`, or broad MCP prompts. `find_large_functions`
accepts `limit`, but it currently performs the line-threshold query before
truncating the response.

### Annotation & Documentation

| Tool | Description |
|------|-------------|
| `get_annotation` | Get annotation and doc tags |
| `build_rag_index` | Build RAG index from docs and communities (supports namespace) |
| `get_rag_tree` | Navigate RAG document tree (supports namespace) |
| `get_doc_content` | Get documentation file content (supports namespace) |
| `search_docs` | Search RAG document tree by keyword (supports namespace) |
| `retrieve_docs` | Retrieve relevant docs from the RAG tree with matched evidence and bounded Markdown content |

Use the documentation/RAG tools as the first stop for natural-language code
understanding. `retrieve_docs` scores file subtrees so multi-keyword queries can
match across related symbols and returns bounded Markdown content with tree
evidence. `get_rag_tree` expands the surrounding module structure, and
`get_doc_content` reads one generated Markdown file directly. After that, use
graph tools such as `get_node`, `query_graph`, `trace_flow`, or
`get_impact_radius` when the task needs exact symbols, edges, flows, or impact
sets. Use `search_docs` or MCP `search` for focused keyword/annotation
candidate search, not as the default surface for broad architecture or "how does
this work?" questions.

RAG index quality depends on generated docs and non-empty community
postprocessing. The CLI `ccg docs` command refreshes communities and writes the
default RAG index automatically. In MCP-only workflows, run `run_postprocess`
with `communities=true`, `flows=false`, and `fts=false` before
`build_rag_index` when communities may be missing.

### Namespace File Management

Use `namespace` as the canonical isolation term. The `workspace` parameter and
`list_workspaces` / `delete_workspace` tools remain only as deprecated aliases
for older callers.

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

Canonical examples:

```
upload_file(namespace: "payment-svc", file_path: "handler.go", content: "<base64>")
list_files(namespace: "payment-svc")
delete_namespace(namespace: "payment-svc")
```

## Claude Code Skills (5)

| Skill | Description |
|-------|-------------|
| `/ccg` | Core build & search — parse, build graph, query, search |
| `/ccg-analyze` | Code analysis — impact radius, flow tracing, dead code, architecture |
| `/ccg-annotate` | Annotation system — AI-driven annotation workflow, tag reference |
| `/ccg-docs` | Documentation — generate docs, RAG indexing, lint |
| `/ccg-workspace` | Namespace file management — upload, list, and delete namespace files (`workspace` remains a deprecated parameter alias) |

### Usage

```
/ccg build .                     — Build code graph
/ccg status                      — Graph statistics and postprocess error summary
/ccg-docs docs                   — Generate documentation and the default RAG index
/ccg-docs rag-index              — Rebuild RAG index from existing docs/communities
/ccg-docs lint                   — Check docs health + annotation coverage
/ccg search "query"              — Focused annotation/keyword candidate search
/ccg languages                   — List supported languages
/ccg-annotate annotate internal/ — AI-generate annotations
/ccg-workspace                   — Manage namespace files and directories
```
