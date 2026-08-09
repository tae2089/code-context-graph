---
name: ccg-namespace
description: "Isolate CCG graph build, search, documentation discovery, and analysis by namespace. Use when working across multiple repositories or services, preventing cross-project graph leakage, choosing safe scoped-update semantics, federating supported reads, or traversing materialized cross-namespace references. Do not use for ordinary single-repository work that fits the default namespace."
metadata:
  version: 1.3.1
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
find_by_intent(namespace: "payment", question: "why does checkout retry a failed capture")
```

## MCP Tools

| Tool | Use |
| ---- | --- |
| `list_namespaces` | List namespaces containing graph data and their node counts |
| `build_or_update_graph` | Build or incrementally update one namespace from a filesystem path |
| `search` | Search code nodes inside a namespace; `namespaces: []` federates across several with per-item labels. Candidates with no evidence are cut before the per-namespace quota runs, so a namespace's slots go to hits it can justify |
| `find_by_intent` | Ask why something was built inside one namespace; answers from recorded reasons and returns a `node_id` per entry. Single-namespace only |
| `get_doc_content` | Read a selected generated document, optionally namespace-scoped |
| `list_cross_refs` | List materialized `ccg://` refs for a namespace (`direction`: outbound/inbound/both) |

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
Treat `find_by_intent`, `get_node`, `get_annotation`, `get_doc_content`,
`list_flows`, and `list_cross_refs` as single-namespace operations.

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
