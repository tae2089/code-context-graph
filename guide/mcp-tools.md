# MCP Tools

code-context-graph exposes 18 MCP tools through both local `ccg serve` and the self-hosted `ccg-server` runtime.

## Parse and Build

| Tool | Purpose |
| ---- | ------- |
| `parse_project` | Parse source files and store graph nodes and edges |
| `build_or_update_graph` | Build or incrementally update a graph namespace from a filesystem path |
| `run_postprocess` | Rebuild stored flows and/or full-text search indexing |

## Query

| Tool | Purpose |
| ---- | ------- |
| `get_node` | Read one node by qualified name |
| `search` | Full-text search across code nodes, optionally scoped by path; `namespaces: []` federates across namespaces with per-item labels |
| `get_annotation` | Read annotations and documentation tags for one node |
| `query_graph` | Run callers, callees, imports, children, tests, inheritors, or file-summary queries; `namespaces: []` groups results per namespace |
| `list_graph_stats` | Report node and edge counts by kind and language; `namespaces: []` returns per-namespace groups |
| `list_namespaces` | List namespaces that contain graph data and their node counts |

## Analysis

| Tool | Purpose |
| ---- | ------- |
| `get_impact_radius` | Compute a bounded BFS blast radius around a node; `cross_namespace: true` follows resolved `ccg://` refs both ways |
| `trace_flow` | Trace a bounded call chain from a node; `cross_namespace: true` continues across resolved `ccg://` refs |
| `detect_changes` | Detect changed functions from git diff and calculate risk scores |
| `get_affected_flows` | List stored flows affected by recent changes |
| `list_flows` | List stored flows with bounded pagination |
| `list_cross_refs` | List materialized `ccg://` cross-namespace references (direction: outbound/inbound/both, status filter) |

## Documentation and Context

| Tool | Purpose |
| ---- | ------- |
| `search_docs` | Search DB-backed documentation candidates and graph evidence; `namespaces: []` groups results per namespace |
| `get_doc_content` | Safely read a selected generated Markdown file |
| `get_minimal_context` | Return a compact project/change summary and suggested next tools |

## Recommended Routing

1. Call `get_minimal_context` for an unfamiliar task.
2. Use `search_docs` to narrow broad module questions, then read a selected file with `get_doc_content`.
3. Use `search` for focused annotation or symbol candidates.
4. Use `get_node` and `query_graph` for exact symbols and relationships.
5. Use `get_impact_radius`, `trace_flow`, `detect_changes`, and `get_affected_flows` for change analysis.

`search_docs` is DB-backed and does not require a generated retrieval index. Only the tools registered in the tables above are part of the current MCP contract.

Use explicit `limit` and `offset` values for `query_graph` and `list_flows`. Start with 50 or 100 results and follow pagination metadata instead of requesting an unbounded result.

## Runtime

Local MCP clients start `ccg serve` over stdio. Remote clients connect to the `/mcp` Streamable HTTP endpoint served by `ccg-server`. Both runtimes register the same 18 tools.

For tool parameters and response schemas, inspect the MCP schema exposed by the running server; source registration lives under `internal/adapters/inbound/mcp/tools_*.go`.
