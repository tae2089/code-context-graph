---
name: ccg-namespace
description: "Isolate CCG graph build, search, documentation discovery, and analysis by namespace. Use when working across multiple repositories or services, preventing cross-project graph leakage, listing populated namespaces, or applying one namespace consistently across MCP and CLI operations. Do not use for ordinary single-repository work that fits the default namespace."
metadata:
  version: 1.2.0
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
build_or_update_graph(path: "/repos/payment", namespace: "payment")
list_namespaces()
search(namespace: "payment", query: "checkout")
search_docs(namespace: "payment", query: "payment flow")
```

## MCP Tools

| Tool | Use |
| ---- | --- |
| `list_namespaces` | List namespaces containing graph data and their node counts |
| `build_or_update_graph` | Build or incrementally update one namespace from a filesystem path |
| `search` | Search code nodes inside a namespace; `namespaces: []` federates across several with per-item labels |
| `search_docs` | Find documentation candidates inside a namespace; `namespaces: []` groups results per namespace |
| `get_doc_content` | Read a selected generated document, optionally namespace-scoped |
| `list_cross_refs` | List materialized `ccg://` refs for a namespace (`direction`: outbound/inbound/both) |

## Operational Guidance

- Use one namespace per service or repository when graph state must remain isolated.
- Keep one canonical source root per namespace; reusing a namespace for an unrelated root makes later update/replace behavior ambiguous.
- Use the default namespace for ordinary single-repository local work.
- Pass the same namespace consistently to build, search, docs, and analysis tools.
- Namespace deletion and file upload are not MCP capabilities; manage source directories outside CCG and rebuild graph state as needed.
- A graph namespace does not generate or copy Markdown files. Generate and place docs separately before expecting namespace-scoped `get_doc_content` reads to succeed.

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
- Federated reads (`namespaces: []` on `search`, `query_graph`,
  `list_graph_stats`, `search_docs`) fan out per namespace and label every
  result; they never merge counts across namespaces.

## Boundary

- Use the default namespace for one local repository unless isolation is required.
- Never combine evidence from different namespaces without labeling each source; federated and cross-namespace tools label results for you.
- Keep filesystem source ownership outside CCG; namespaces isolate graph state, not repository permissions.
- Verify the selected namespace has graph rows before interpreting an empty search as no match.

## Completion

State the namespace used for every build/search/analysis step, confirm it with `list_namespaces` or graph statistics, and report whether any cross-namespace evidence was intentionally included.
