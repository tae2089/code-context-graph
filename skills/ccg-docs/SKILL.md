---
name: ccg-docs
description: "Generate, discover, read, and lint CCG documentation. Use when producing Markdown and Wiki snapshots, narrowing broad module questions with search, reading generated docs from configured content roots with get_doc_content, or diagnosing orphan, missing, stale, incomplete, contradiction, dead-ref, and drift findings. Do not use for direct source annotation authoring or exact call-graph analysis."
metadata:
  version: 1.2.1
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

Separate DB-backed discovery from generated-file reads, then validate both the
selected evidence and documentation quality.

## Routing

| Task | Tool |
| ---- | ---- |
| Broad question about a module | MCP `search`, phrased as a question, then `get_doc_content` |
| Focused annotation or symbol keyword | `ccg search` or MCP `search` |
| Exact generated Markdown body | `get_doc_content` |
| Exact signature or relationship | `get_node` or `query_graph` |
| Regenerate Markdown and Wiki snapshot | `ccg docs --out docs` |
| Audit generated docs | `ccg lint` |

`search` is a DB-backed narrowing layer. A question is scored against recorded `@intent`/`@domainRule` reasons as well as names, and every hit carries a `node_id`; it does not read a separately generated retrieval index. Read a file's Markdown with `get_doc_content`, then use graph tools for exact symbols and relationships.

## Discovery Pipeline

Use the `ccg` skill's Graph Freshness workflow before relying on graph evidence.
`search` can narrow candidates without generated Markdown:

```text
search(query: "how does a caller get authenticated", limit: 5)
```

Generate files only when the task needs current Markdown, Wiki output, or
`get_doc_content`:

```bash
ccg docs --out docs
ccg lint
```

## Content-Root Contract

`search` returns DB-backed candidates with source `file_path` values; it
does not prove that a corresponding generated Markdown file exists in the
filesystem root used by `get_doc_content`.

- For the default namespace, `get_doc_content` resolves `file_path` beneath the
  MCP server's configured `rag.index_dir` (default `.ccg`).
- For a named namespace, it resolves beneath
  `{namespace_root}/{namespace}`.
- `ccg docs --out docs` writes Markdown to `docs`, but it does not make those files readable beneath the default `.ccg` content root.
- To support default-namespace MCP reads, either generate with
  `ccg docs --out .ccg/docs --rag-index-dir .ccg`, configure `rag.index_dir` to
  a root that already contains `docs`, or use a direct local file read.
- For a named namespace, place generated docs beneath that namespace directory
  before calling `get_doc_content`.

Pass the selected result's relative generated-doc path to `get_doc_content`. If the read
fails, report the configured-root mismatch; do not guess paths outside the
allowed root.

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
2. Stale generated docs: use the core Graph Freshness workflow, then rerun `ccg docs --out docs`.
3. Empty answers to a question-shaped `search`: an empty answer usually means nobody recorded a reason in the area, not that the graph is stale. Confirm namespace statistics and refresh only when the graph is missing or stale.
4. Missing `get_doc_content` file: compare the generated-doc path with the configured content root before regenerating.
5. Exact-answer needs: switch from documentation discovery to `get_node`, `query_graph`, or `trace_flow`.

## MCP Tools

| Tool | Use |
| ---- | --- |
| `search` | Symbol and question search in one tool; a question answers from recorded reasons, and every hit carries a `node_id` |
| `get_doc_content` | Safely read a selected generated Markdown file |

`search` reads graph evidence without a separately generated retrieval
index. `get_doc_content` still requires the selected Markdown beneath its
configured root. Local MCP clients use `ccg serve`; self-hosted clients connect
to `ccg-server` over Streamable HTTP.

## Boundary

- Treat `search` as a narrowing layer, not a guaranteed Top-1 answer.
- Confirm the selected generated-doc path is readable before treating its body as evidence.
- Do not hand-edit generator-managed Markdown when the source annotation or generator owns the content.
- Separate current lint results from pre-existing unrelated findings.
- Never infer that successful generation to an arbitrary `--out` directory made the file MCP-readable.

## Completion

Report generated Markdown and Wiki-index paths when generation was requested,
the configured content root and selected generated-doc path for each document read,
namespace and graph freshness, and the exact lint summary or why lint was not
run. Record any discovery/read mismatch rather than claiming a candidate body
was inspected.
