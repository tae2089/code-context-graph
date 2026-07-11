---
name: ccg-docs
description: code-context-graph — Markdown generation, DB-backed documentation discovery, generated document reads, and docs lint.
---

# ccg-docs — Documentation Discovery

Generate Markdown from the code graph, find relevant generated documents through DB-backed evidence, read exact document bodies, and validate documentation quality.

## Routing

| Task | Tool |
| ---- | ---- |
| Broad question about a module | `search_docs`, then `get_doc_content` |
| Focused annotation or symbol keyword | `ccg search` or MCP `search` |
| Exact generated Markdown body | `get_doc_content` |
| Exact signature or relationship | `get_node` or `query_graph` |
| Regenerate Markdown and Wiki snapshot | `ccg docs --out docs` |
| Audit generated docs | `ccg lint` |

`search_docs` is a DB-backed narrowing layer. It returns candidate files and graph evidence; it does not read a separately generated retrieval index. Read the selected Markdown with `get_doc_content`, then use graph tools for exact symbols and relationships.

## Core Pipeline

```bash
ccg build .
ccg docs --out docs
ccg lint
```

Then use MCP:

```text
search_docs(query: "auth flow", limit: 5)
get_doc_content(file_path: "docs/internal/auth/service.go.md")
query_graph(pattern: "callers_of", target: "auth.Service.Login")
```

## CLI Commands

| Command | Use |
| ------- | --- |
| `ccg docs --out docs` | Generate Markdown and `wiki-index.json` compatibility snapshot |
| `ccg index` | Regenerate `index.md` only |
| `ccg lint` | Run documentation quality checks |
| `ccg lint --strict` | Exit 1 when lint reports actionable issues |

## Lint Categories

| Category | Meaning |
| -------- | ------- |
| `orphan` | Generated doc without matching code |
| `missing` | Code without generated doc |
| `stale` | Code changed but doc did not |
| `unannotated` | Missing required intent/domain annotation |
| `contradiction` | Doc contradicts the current signature |
| `dead-ref` | Broken `@see` reference |
| `incomplete` | Missing required parameter or return documentation |
| `drift` | Documentation structure diverged from code |

## Quality Checkpoints

1. Sparse results: add accurate `@intent` or `@index` annotations through `/ccg-annotate`.
2. Stale generated docs: rerun `ccg build .` and `ccg docs --out docs`.
3. Empty `search_docs` results: confirm the namespace and graph statistics, then rebuild the graph.
4. Exact-answer needs: switch from documentation discovery to `get_node`, `query_graph`, or `trace_flow`.

## MCP Tools

| Tool | Use |
| ---- | --- |
| `search_docs` | Search DB-backed documentation candidates and evidence |
| `get_doc_content` | Safely read a selected generated Markdown file |

Requires `ccg build .` first. Local MCP clients use `ccg serve`; self-hosted clients connect to `ccg-server` over Streamable HTTP.
