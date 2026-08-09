# MCP Tools

code-context-graph exposes 19 MCP tools through both local `ccg serve` and the self-hosted `ccg-server` runtime.

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
| `search` | Full-text search across code nodes, grouped by file: results arrive as `files[] {file_path, hit_count, hits[]}` and a file that appears appears whole. `limit` counts files and `offset` pages by files, so a page never splits one. Every hit carries its evidence (`matched` signals plus the node's `@intent`); unjustifiable candidates are cut and counted in `weak_filtered`. Optionally scoped by `path`; `include_weak: true` returns the cut ones; `namespaces: []` federates across namespaces with per-item labels. `truncated` says whether more files answered than this page reached, and `next` names the calls that retrieve them |
| `find_by_intent` | Ask in plain language why something was built. Scored only against recorded `@intent`/`@domainRule`, never against names or paths, so a sentence-shaped question gets an answer where `search` gets none. Returns `files[] {file_path, entries[]}` where each entry carries a `node_id` to hand to `get_node`, `query_graph`, `get_impact_radius`, or `trace_flow`. There is no fallback to name search; `coverage` reports how many searchable declarations have a recorded reason at all. Each entry also carries `matched_terms`, and the answer carries `terms` + `reasons_searched`, so a caller can see whether a long file list rests on one very common word |
| `describe` | List what the graph holds under one path, with no ranking. A folder answers with the folders and files directly inside, one level down, each with its file and declaration counts; a file answers with every declaration written in it, in written order, each carrying its line range, `node_id`, and recorded `@intent`. This is what `search` and `find_by_intent` hand off to: those rank and can be wrong, this one only reports what exists. No query, no limit, no relevance. A target the graph does not hold answers with `scope: "unknown"` plus the places that name is actually declared. It replaced the `children_of` and `file_summary` patterns of `query_graph` |
| `get_annotation` | Read annotations and documentation tags for one node |
| `query_graph` | Run callers, callees, imports, importers, tests, or inheritors queries; `namespaces: []` groups results per namespace. For what is written inside a file or folder, use `describe` |
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
| `get_doc_content` | Safely read a selected generated Markdown file |
| `get_minimal_context` | Return a compact project/change summary and suggested next tools |

## Recommended Routing

1. Call `get_minimal_context` for an unfamiliar task.
2. Use `find_by_intent` for a plain-language question about why something exists, then walk from the `node_id` it returns or read a file with `get_doc_content`.
3. Use `search` for focused annotation or symbol candidates.
4. Use `describe` on any path you already hold — a search hit, a stack frame, a diff — to read what is there without ranking it.
5. Use `get_node` and `query_graph` for exact symbols and relationships.
6. Use `get_impact_radius`, `trace_flow`, `detect_changes`, and `get_affected_flows` for change analysis.

`find_by_intent` is DB-backed and does not require a generated retrieval index. Only the tools registered in the tables above are part of the current MCP contract.

Use explicit `limit` and `offset` values for `query_graph` and `list_flows`. Start with 50 or 100 results and follow pagination metadata instead of requesting an unbounded result.

## Runtime

Local MCP clients start `ccg serve` over stdio. Remote clients connect to the `/mcp` Streamable HTTP endpoint served by `ccg-server`. Both runtimes register the same 19 tools.

For tool parameters and response schemas, inspect the MCP schema exposed by the running server; source registration lives under `internal/adapters/inbound/mcp/tools_*.go`.
