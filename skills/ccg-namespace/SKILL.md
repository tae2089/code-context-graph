---
name: ccg-namespace
description: "Isolate CCG graph build, search, documentation discovery, and analysis by namespace. Use when working across multiple repositories or services, preventing cross-project graph leakage, choosing safe scoped-update semantics, federating supported reads, or traversing materialized cross-namespace references. Do not use for ordinary single-repository work that fits the default namespace."
metadata:
  version: 1.4.0
  openclaw:
    category: "code-intelligence"
    domain: "namespace"
  requires:
    bins:
      - ccg
    skills:
      - ccg
  cliHelp: "ccg build --help"
---

# ccg-namespace — Graph Namespace Isolation

Use namespaces to isolate graph rows for different services or repositories. CCG no longer manages uploaded namespace files; callers provide a filesystem path to `build_or_update_graph` or use CLI build/update commands with `--namespace`.

## Core Pattern

```bash
ccg build ./services/payment --namespace payment
ccg build ./services/users --namespace users
ccg search --namespace payment "checkout"
ccg status --namespace users
```

Through MCP:

```text
build_or_update_graph(path: "/repos/payment", namespace: "payment", full_rebuild: true)
list_namespaces()
search(namespace: "payment", query: "checkout")
search(namespace: "payment", query: "why does checkout retry a failed capture")
```

`list_namespaces` reports every namespace holding graph data with its node
count — the first call before interpreting any namespace-scoped result. Both
`search` query shapes work namespace-scoped: keywords and plain-language
questions answered from recorded reasons.

## Scoped Update Decision

Classify a partial incremental update as either an authoritative snapshot or a
maintenance work slice. Use the `ccg` skill's Scoped Update Safety for the
exact `include_paths` and replacement arguments, then record which behavior was
chosen for the namespace.

## Operational Guidance

- Use one namespace per service or repository when graph state must remain isolated.
- Use the default namespace for ordinary single-repository local work.
- Pass the same namespace consistently to build, search, docs, and analysis tools.
- Namespace deletion and file upload are not MCP capabilities; manage source directories outside CCG and rebuild graph state as needed.
- A graph namespace does not generate or copy Markdown files. Generate and place docs separately before expecting namespace-scoped `get_doc_content` reads to succeed.

## Federation Boundaries

Only `search`, `query_graph`, and `list_graph_stats` accept `namespaces: []`.
Treat `get_node`, `get_annotation`, `get_doc_content`,
`list_flows`, and `list_cross_refs` as single-namespace operations.

A federated `search` labels every hit with its namespace, and candidates with
no visible evidence are cut before the per-namespace quota runs — a
namespace's slots go to hits it can justify, not to padding.

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

State the namespace used for every build/search/analysis step, confirm it with
`list_namespaces` or graph statistics, report the `replace` choice for scoped
updates, label federated errors, and state whether cross-namespace evidence was
intentionally included.
