---
name: ccg-analyze
description: "Analyze code relationships with CCG impact radius, flow tracing, callers/callees, git-diff risk, and affected stored flows. Use when a task asks what a change affects, how a call path flows, who calls a symbol, or which flows recent changes touch. Do not use for simple text lookup, documentation generation, or annotation authoring."
metadata:
  version: 1.1.0
  openclaw:
    category: "code-intelligence"
    domain: "analysis"
  requires:
    bins:
      - ccg
    skills:
      - ccg
---

# ccg-analyze — Code Analysis

Graph-based analysis for **change impact, call flow, and recent-change risk**.

## Intent → Tool Mapping

| User intent                          | Tool                                             | Notes                                         |
| ------------------------------------ | ------------------------------------------------ | --------------------------------------------- |
| "Impact of changing this function?"  | `get_impact_radius` (start with depth 3)         | Widen to depth 5 if too narrow                |
| "Trace call flow from this function" | `trace_flow`                                     | If unexpectedly thin, verify the causes below |
| "Who calls this function?"           | `query_graph` (callers_of)                       |                                               |
| "What does this function call?"      | `query_graph` (callees_of)                       |                                               |
| "Risk of this change"                | `detect_changes` + `get_affected_flows`          | git diff-based                                |
| "Which repos depend on this one?"    | `list_cross_refs` (direction inbound)            | Annotation `ccg://` refs, materialized        |
| "Impact across repos?"               | `get_impact_radius` with `cross_namespace: true` | Crosses resolved `ccg://` refs both ways      |

## Thin `trace_flow` Results

One or two returned nodes do not prove an interface-dispatch failure. The start
may be a real leaf, the selected namespace or qualified name may be wrong, the
graph may be stale, strict edges may be sparse, or dynamic dispatch may be
unresolved.

Verify in this order:

```
1. Confirm the exact node with get_node.
2. Confirm namespace population and graph freshness.
3. Compare query_graph callers_of/callees_of with and without fallback calls.
4. Read the relevant source around unresolved interface or dynamic calls.
```

Report which explanation is supported; do not label a thin trace as an
interface boundary without source or edge evidence.

## get_impact_radius Tips

- **depth 1–2**: direct impact (immediate callers/callees)
- **depth 3**: recommended default — covers most tasks
- **depth 5+**: large monorepo propagation. Watch for noise.

If results are huge, narrow by namespace, starting symbol, depth, or edge mode
before concluding the implementation change itself is too broad. High-fanout
entry points can legitimately have a large radius.

## Pagination Defaults

Use explicit budgets for graph browsing tools when the namespace may be large:

| Tool | Parameters | Default starting point |
| ---- | ---------- | ---------------------- |
| `query_graph` | `limit`, `offset` | `limit=50`, `offset=0` |
| `list_flows` | `limit`, `offset` | `limit=50`, `offset=0` |
| `detect_changes` | `limit`, `offset` | `limit=50`, `offset=0` |
| `get_affected_flows` | `limit`, `offset` | `limit=50`, `offset=0` |

Paginated responses include `has_more`. If true, call again with `next_offset`.
Do not request the max page size first for LLM analysis; use 50 or 100 unless
the user specifically needs a bulk export.

## Accuracy Limits (use with awareness)

- Interface calls may **over-predict** (expands to all implementations)
- Dynamic dispatch (reflection, plugins) → not captured
- Build-tag-split files → both registered (noise)
- Fallback call edges improve recall but may add false positives; use strict mode when evidence quality matters more than coverage
- Treat graph results as a static approximation; cross-check important conclusions against source.

## Boundary

- Start from a verified qualified name; do not infer a symbol from a display label alone.
- Scope namespace, path, traversal depth, and result limits before widening a query.
- Separate strict call edges from fallback edges when evidence quality matters.
- Do not treat missing graph edges as proof that runtime behavior is impossible.

## Analysis MCP Tools

| Tool                        | One-liner                    |
| --------------------------- | ---------------------------- |
| `get_impact_radius`         | BFS blast radius; `cross_namespace: true` follows resolved `ccg://` refs |
| `trace_flow`                | Call chain trace; `cross_namespace: true` continues into referenced namespaces |
| `detect_changes`            | Git diff risk score          |
| `get_affected_flows`        | Flows affected by change     |
| `list_flows`                | Stored flow list, paginated  |
| `list_cross_refs`           | Repository dependency map from materialized `ccg://` refs (`direction`: outbound/inbound/both, `status` filter) |

For detailed parameters, see MCP schema.

## Prerequisites

Confirm that the selected namespace contains a current graph. Build only when
the graph is missing or a full rebuild is intentional; after ordinary code
changes, prefer `ccg update .`. Stored-flow tools also require flow
postprocessing; an empty flow list is not evidence of no flow until that state
has been checked. Use the `ccg` skill for freshness and postprocessing guidance.

## Completion

Report the analyzed qualified name, namespace, depth/limits, included edge modes, returned impact or flow evidence, and any source-level cross-check used for an important conclusion.
