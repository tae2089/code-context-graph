---
name: ccg-analyze
description: code-context-graph — impact analysis, flow tracing, dead code, architecture. Use when answering "what's affected", "how does X flow", "what's unused", "module structure".
---

# ccg-analyze — Code Analysis

Graph-based analysis for **change impact, call flow, dead code, module structure** — anything about "relationships."

## Intent → Tool Mapping

| User intent                          | Tool                                             | Notes                                         |
| ------------------------------------ | ------------------------------------------------ | --------------------------------------------- |
| "Impact of changing this function?"  | `get_impact_radius` (start with depth 3)         | Widen to depth 5 if too narrow                |
| "Trace call flow from this function" | `trace_flow`                                     | If broken at interfaces, see workaround below |
| "Who calls this function?"           | `query_graph` (callers_of)                       |                                               |
| "What does this function call?"      | `query_graph` (callees_of)                       |                                               |
| "Unused code"                        | `find_dead_code`                                 | Interface methods may give false positives    |
| "Large functions"                    | `find_large_functions`                           | Refactoring candidates                        |
| "Risk of this change"                | `detect_changes` + `get_affected_flows`          | git diff-based                                |
| "Module structure"                   | `list_communities` + `get_architecture_overview` | First time on a codebase                      |
| "Test coverage gaps"                 | `get_community` (with coverage)                  |                                               |

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

## Accuracy Limits (use with awareness)

- Interface calls may **over-predict** (expands to all implementations)
- Dynamic dispatch (reflection, plugins) → not captured
- Build-tag-split files → both registered (noise)
- `fallback_calls` are treated as best-effort when present in graph data; strict workflows should treat them as noisy signals.
- Don't trust 100%. For important decisions, cross-check with grep.

## Fallback-Mode Analysis Guidance

`fallback_calls` can hide unresolved edges when strict call resolution is too strict, but they increase false-positive risk.

- If `ccg build`/`ccg update` was run without `--fallback-calls`, treat missing flow paths as strict failures first.
- If the same run used `--fallback-calls`, validate critical paths by filtering for `kind=calls` edges only (if tooling allows), or re-run with strict mode for confirmation.
- In CI or high-confidence reviews, prefer strict-mode results and use `fallback_calls` only for recovery hypotheses.

## Full MCP Tool List

| Tool                        | One-liner                    |
| --------------------------- | ---------------------------- |
| `get_impact_radius`         | BFS blast radius             |
| `trace_flow`                | Call chain trace             |
| `find_large_functions`      | Above line threshold         |
| `find_dead_code`            | No callers                   |
| `detect_changes`            | Git diff risk score          |
| `get_affected_flows`        | Flows affected by change     |
| `list_flows`                | Stored flow list             |
| `list_communities`          | Louvain module clusters      |
| `get_community`             | Community details + coverage |
| `get_architecture_overview` | Coupling summary             |

For detailed parameters, see MCP schema.

## Prerequisites

Requires `ccg build .` first. Schema error → `ccg migrate`, retry. (See `/ccg` skill.)
