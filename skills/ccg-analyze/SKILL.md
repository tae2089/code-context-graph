---
name: ccg-analyze
description: "Explicit-only deep analysis of algorithms, feature pipelines, and code relationships with CCG impact radius, bounded flow tracing, callers/callees, git-diff risk, affected stored flows, and cross-namespace references. Use only when the user explicitly names the ccg-analyze skill in the current request. Do not invoke merely because a task asks about a flow, pipeline, impact, caller, or relationship."
disable-model-invocation: true
metadata:
  version: 2.0.1
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

## Invocation Gate

Proceed only when the user explicitly names `ccg-analyze` in the current
request. A request about an algorithm, pipeline, impact, caller, or relationship
is not by itself an invocation. Without that explicit name, do not load this
workflow or run its traversal and impact-analysis tools; use the bounded `ccg`
discovery workflow instead.

## Intent → Tool Mapping

| User intent                          | Tool                                             | Notes                                         |
| ------------------------------------ | ------------------------------------------------ | --------------------------------------------- |
| "Impact of changing this function?"  | `get_impact_radius` (`depth=3`, `max_depth=3`)   | Raise both bounds to widen                    |
| "How does this algorithm or feature pipeline work?" | Pipeline workflow below | Graph-first, then source-verified |
| "Trace call flow from this function" | `trace_flow`                                     | If unexpectedly thin, verify the causes below; `cross_namespace: true` continues into referenced namespaces |
| "Who calls this function?"           | `query_graph` (callers_of)                       |                                               |
| "What does this function call?"      | `query_graph` (callees_of)                       |                                               |
| "Risk of this change"                | `detect_changes` + `get_affected_flows`          | git diff-based                                |
| "Which repos depend on this one?"    | `list_cross_refs` (direction inbound)            | Annotation `ccg://` refs, materialized; `direction` also takes outbound/both, plus a `status` filter |
| "Impact across repos?"               | `get_impact_radius` with `cross_namespace: true` | Crosses resolved `ccg://` refs both ways      |

## Pipeline Analysis Workflow

1. **Candidate discovery**: use `search` for both cases. When you cannot name
   the symbol yet, phrase the query as a question — it is scored against
   recorded `@intent`/`@domainRule` reasons as well as names, and every hit
   carries a `node_id` to walk from. Name the symbol once you can. A hit carries
   its own evidence — the `matched` signals and the node's `@intent` — so pick
   entry points from that rather than from position in the list; a short list
   means few justifiable files, and `weak_filtered` counts what was cut. Hits
   come grouped by file and a shown file is shown whole, so `limit` counts
   files. When `truncated` is true more files answered than this page reached,
   and the response's `next` field holds the exact calls — usually the same
   search at a higher `offset`; make those instead of re-searching with a
   larger `limit`. `pool_truncated` is a second, separate signal: true means the
   page ended at the edge of the candidates that were fetched, so page on even
   when `truncated` is false. Only `truncated: false` together with
   `pool_truncated: false` means the search is complete. When a question-shaped
   query comes back empty, read `annotation_coverage` before concluding
   anything: `with_reason: 0` means nobody recorded a reason in what you
   searched, so the empty answer says nothing about whether the code exists —
   annotate the area and ask again rather than rephrasing.
2. **Symbol identity**: confirm each entry point or major stage with `get_node`;
   continue with qualified names rather than display labels.
3. **Relationship and structure evidence**: use `query_graph` with
   `callers_of`/`callees_of` for direct call relations, and `describe` on a file
   or folder path for what is written there. This evidence does not prove
   runtime order.
4. **Call-chain evidence**: use `trace_flow` from a verified entry point and
   inspect truncation plus fallback-edge metadata. Treat the result as a bounded
   static chain, not a runtime trace.
5. **Runtime semantics**: read the narrowed source files to verify branch
   conditions, loops, callbacks, data transformations, error paths, and actual
   ordering. If graph and source disagree, report staleness, unresolved dynamic
   behavior, or the remaining uncertainty instead of silently choosing one.

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

## `get_impact_radius` Bounds

- `depth` requests the BFS hop count; its default is 1.
- `max_depth` caps `depth`; its default is 3. Setting only `depth=5` still
  returns at most three hops, so raise both values when widening.
- Start with `depth=3`, `max_depth=3`, and a bounded `max_nodes`.
- Inspect response metadata `truncated`, `max_depth`, `max_nodes`, and
  `returned_nodes` before interpreting the radius as complete.

If results are huge, narrow by namespace, starting symbol, depth, or edge mode
before concluding the implementation change itself is too broad. High-fanout
entry points can legitimately have a large radius.

## Analysis Result Bounds

Use the `ccg` skill's one-query discovery budget only for the initial entry-point
search. Once this explicit analysis workflow is active, use the analysis-specific
bounds below and read each additional source range needed to verify the selected
stages. Preserve per-page namespace labels and errors when accumulating
federated results.

`get_impact_radius` and `trace_flow` are bounded rather than paginated. Follow
their `truncated` metadata by narrowing the start/scope or deliberately raising
`max_nodes`; do not call a truncated response complete.

## Accuracy Limits (use with awareness)

- Interface calls may **over-predict** (expands to all implementations)
- Dynamic dispatch (reflection, plugins) → not captured
- Build-tag-split files → both registered (noise)
- Fallback call edges improve recall but may add false positives; use strict mode when evidence quality matters more than coverage
- Treat graph results as a static approximation; cross-check important conclusions against source.
- `repo_root` for `detect_changes` and `get_affected_flows` must be a
  server-visible, allowed path. If the MCP server cannot see the client path,
  report that constraint and use local git/source evidence instead.

## Boundary

- Start from a verified qualified name; do not infer a symbol from a display label alone.
- Scope namespace, path, traversal depth, and result limits before widening a query.
- Separate strict call edges from fallback edges when evidence quality matters.
- Do not treat missing graph edges as proof that runtime behavior is impossible.
- Do not hide per-namespace errors or truncation when federated/cross-namespace evidence is partial.

## Prerequisites

Use the `ccg` skill's Freshness Boundary before interpreting graph or
stored-flow results. If refresh is required, report the gap; do not invoke
`ccg-build` unless the user explicitly names it. The stored-flow tools — `list_flows`,
`get_affected_flows` — require flow postprocessing; an empty flow list is not
evidence of no flow until that state has been checked.

## Completion

Report the analyzed qualified names, namespace, discovery query, `query_graph`
patterns, `trace_flow` bounds and truncation, included edge modes,
fallback-edge counts, source files used to verify runtime semantics, any
graph/source disagreement or server-visible path limitation, and the evidence
supporting important conclusions.
