---
name: ccg-namespace
description: "Isolate read-only CCG search, documentation discovery, and analysis by namespace. Use when working across multiple repositories or services, preventing cross-project graph leakage, federating supported reads, or traversing materialized cross-namespace references. Do not use for graph writes or scoped updates; the user must explicitly invoke ccg-build for those operations."
metadata:
  version: 2.0.0
  openclaw:
    category: "code-intelligence"
    domain: "namespace"
  requires:
    bins:
      - ccg
    skills:
      - ccg
  cliHelp: "ccg search --help"
---

# ccg-namespace — Graph Namespace Isolation

Use namespaces to isolate graph rows for different services or repositories.
This skill reads existing namespace state and never writes it.

## Core Pattern

```bash
ccg search --namespace payment "checkout"
ccg status --namespace users
```

Through MCP:

```text
list_namespaces()
search(namespace: "payment", query: "checkout")
search(namespace: "payment", query: "why does checkout retry a failed capture")
```

`list_namespaces` reports every namespace holding graph data with its node
count — the first call before interpreting any namespace-scoped result. Both
`search` query shapes work namespace-scoped: keywords and plain-language
questions answered from recorded reasons.

## Write Boundary

Do not plan or execute a namespace build, update, migration, postprocess, or
scoped replacement from this skill. A request that needs one of those operations
is not enough to invoke `ccg-build`: report that the user must explicitly name
`ccg-build` in the current request.

## Operational Guidance

- Use one namespace per service or repository when graph state must remain isolated.
- Use the default namespace for ordinary single-repository local work.
- Pass the same namespace consistently to search, docs, and analysis tools.
- Namespace deletion and file upload are not MCP capabilities.
- A graph namespace does not generate or copy Markdown files. Generate and place docs separately before expecting namespace-scoped `get_doc_content` reads to succeed.

## Federation Boundaries

Only `search`, `query_graph`, and `list_graph_stats` accept `namespaces: []`.
Treat `get_node`, `get_annotation`, `get_doc_content`,
`list_flows`, and `list_cross_refs` as single-namespace operations.

A federated `search` labels every hit with its namespace, and candidates with
no visible evidence are cut before the page is bounded, so a namespace's files
are hits it can justify rather than padding.

`limit` and `offset` are per namespace here, not shared across them. `limit: 5`
over three namespaces means up to five files from each, and every namespace
with a hit appears however small the limit is — a limit below the namespace
count no longer silences the ones at the back. `truncated` counts the files
left off across all of them, and `annotation_coverage` adds up across all of
them too — one fraction over every namespace searched, not one namespace's. Page with the `offset` the response's `next` gives
you: it moves every namespace through its own list at once, so it is not this
page's offset plus the file count you were handed.

`get_impact_radius` and `trace_flow` also start in one namespace; enable
`cross_namespace=true` only when resolved `ccg://` references should extend the
traversal. Federation runs independent reads and labels them; cross-namespace
analysis follows materialized reference edges. Do not treat the two modes as
equivalent.

## Cross-Namespace Links

Annotation `@see ccg://{namespace}/{path}#{symbol}` tags are materialized into
queryable cross-namespace references on every build/update:

- `list_cross_refs(namespace, direction)` returns the repository-level
  dependency map (outbound = declared dependencies, inbound = dependents).
- `get_impact_radius(..., cross_namespace: true)` and
  `trace_flow(..., cross_namespace: true)` traverse resolved refs across
  namespace boundaries; result nodes carry a namespace label.
- Refs re-resolve automatically after either side rebuilds; `status: dead`
  marks targets that no longer exist (also reported by `ccg lint` as
  `dead-ref`).
- Federated reads fan out per namespace and label every result; they never
  merge counts across namespaces.

## Boundary

- Use the default namespace for one local repository unless isolation is required.
- Never combine evidence from different namespaces without labeling each source; federated and cross-namespace tools label results for you.
- Keep filesystem source ownership outside CCG; namespaces isolate graph state, not repository permissions.
- Verify the selected namespace has graph rows before interpreting an empty search as no match.
- Preserve per-namespace errors; one successful federated branch does not make the whole request complete.

## Completion

State the namespace used for every search or analysis step, confirm it with
`list_namespaces` or graph statistics, label federated errors, and state whether
cross-namespace evidence was intentionally included. Report any required graph
write as awaiting explicit `ccg-build` invocation.
