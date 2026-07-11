---
name: ccg-namespace
description: code-context-graph — namespace isolation for graph build, search, documentation discovery, and multi-repository workflows.
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
| `search` | Search code nodes inside a namespace |
| `search_docs` | Find documentation candidates inside a namespace |
| `get_doc_content` | Read a selected generated document, optionally namespace-scoped |

## Operational Guidance

- Use one namespace per service or repository when graph state must remain isolated.
- Use the default namespace for ordinary single-repository local work.
- Pass the same namespace consistently to build, search, docs, and analysis tools.
- Namespace deletion and file upload are not MCP capabilities; manage source directories outside CCG and rebuild graph state as needed.
