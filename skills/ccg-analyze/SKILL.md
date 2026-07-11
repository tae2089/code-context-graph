---
name: ccg-analyze
description: code-context-graph — impact analysis, flow tracing, changed-function risk, and affected-flow inspection.
---

# ccg-analyze — Code Analysis

Graph-based analysis for **change impact, call flow, and recent-change risk**.

## Intent → Tool Mapping

| User intent                          | Tool                                             | Notes                                         |
| ------------------------------------ | ------------------------------------------------ | --------------------------------------------- |
| "Impact of changing this function?"  | `get_impact_radius` (start with depth 3)         | Widen to depth 5 if too narrow                |
| "Trace call flow from this function" | `trace_flow`                                     | If broken at interfaces, see workaround below |
| "Who calls this function?"           | `query_graph` (callers_of)                       |                                               |
| "What does this function call?"      | `query_graph` (callees_of)                       |                                               |
| "Risk of this change"                | `detect_changes` + `get_affected_flows`          | git diff-based                                |

## trace_flow Limitations & Workaround

If `trace_flow` returns **only 1–2 starting nodes**, it broke at an interface dispatch boundary (Go's `h.deps.X.Method()`, Python duck typing, etc.).

**Workaround**:

```
1. Confirm starting point via trace_flow
2. Manually expand graph with callers_of / callees_of
3. When you hit interface methods:
   - Find the interface via search
   - Reverse-trace implementations with callers_of
```

Latest ccg added an interface dispatch resolver, but it doesn't cover every pattern. If results look thin, fall back to this manual pattern.

## get_impact_radius Tips

- **depth 1–2**: direct impact (immediate callers/callees)
- **depth 3**: recommended default — covers most tasks
- **depth 5+**: large monorepo propagation. Watch for noise.

If results are huge, the change scope is likely too wide. Reconsider the change unit based on node count.

## Pagination Defaults

Use explicit budgets for graph browsing tools when the namespace may be large:

| Tool | Parameters | Default starting point |
| ---- | ---------- | ---------------------- |
| `query_graph` | `limit`, `offset` | `limit=50`, `offset=0` |
| `list_flows` | `limit`, `offset` | `limit=50`, `offset=0` |

Paginated responses include `has_more`. If true, call again with `next_offset`.
Do not request the max page size first for LLM analysis; use 50 or 100 unless
the user specifically needs a bulk export.

## Accuracy Limits (use with awareness)

- Interface calls may **over-predict** (expands to all implementations)
- Dynamic dispatch (reflection, plugins) → not captured
- Build-tag-split files → both registered (noise)
- Fallback call edges improve recall but may add false positives; use strict mode when evidence quality matters more than coverage
- Don't trust 100%. For important decisions, cross-check with grep.

## Full MCP Tool List

| Tool                        | One-liner                    |
| --------------------------- | ---------------------------- |
| `get_impact_radius`         | BFS blast radius             |
| `trace_flow`                | Call chain trace             |
| `detect_changes`            | Git diff risk score          |
| `get_affected_flows`        | Flows affected by change     |
| `list_flows`                | Stored flow list, paginated  |

For detailed parameters, see MCP schema.

## Prerequisites

Requires `ccg build .` first. Schema error → `ccg migrate`, retry. (See `/ccg` skill.)
