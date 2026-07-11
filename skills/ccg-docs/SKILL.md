---
name: ccg-docs
description: "Generate, discover, read, and lint CCG documentation. Use when producing Markdown and Wiki snapshots, narrowing broad module questions with search_docs, reading generated docs with get_doc_content, or diagnosing orphan, missing, stale, incomplete, contradiction, dead-ref, and drift findings. Do not use for direct source annotation authoring or exact call-graph analysis."
metadata:
  version: 1.1.0
  openclaw:
    category: "code-intelligence"
    domain: "documentation"
  requires:
    bins:
      - ccg
    skills:
      - ccg
  cliHelp: "ccg docs --help"
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

Confirm the graph first. Use `ccg update .` after ordinary source edits or
`ccg build .` when the graph is missing or an intentional full rebuild is
needed. Generate files only when the task needs current Markdown or Wiki output:

```bash
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

1. Sparse results: add accurate `@intent` or `@index` annotations with the `ccg-annotate` skill.
2. Stale generated docs: refresh changed graph data with `ccg update .`, then rerun `ccg docs --out docs`.
3. Empty `search_docs` results: confirm the namespace and graph statistics, then build or update only if the graph is missing or stale.
4. Exact-answer needs: switch from documentation discovery to `get_node`, `query_graph`, or `trace_flow`.

## MCP Tools

| Tool | Use |
| ---- | --- |
| `search_docs` | Search DB-backed documentation candidates and evidence |
| `get_doc_content` | Safely read a selected generated Markdown file |

`search_docs` reads graph evidence without requiring a separately generated
retrieval index. `get_doc_content` still needs the selected Markdown file to
exist at the configured path. Local MCP clients use `ccg serve`; self-hosted
clients connect to `ccg-server` over Streamable HTTP.

## Boundary

- Treat `search_docs` as a narrowing layer, not a guaranteed Top-1 answer.
- Read selected generated docs before switching to exact graph tools.
- Do not hand-edit generator-managed Markdown when the source annotation or generator owns the content.
- Separate current lint results from pre-existing unrelated findings.

## Completion

Report generated output paths when generation was requested, list documents selected as evidence, state the namespace and graph freshness, and include the exact lint summary or explain why lint was not run.
