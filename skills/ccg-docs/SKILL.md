---
name: ccg-docs
description: code-context-graph — Markdown docs generation, RAG indexing, docs lint. Use when generating wiki, building searchable doc tree for AI, or auditing doc quality.
---

# ccg-docs — Documentation & RAG

Generate Markdown wiki from code graph. Build RAG tree for fast AI exploration. Auto-validate doc quality.

## RAG vs ccg search Routing (key)

| Task                                                   | Tool                                     |
| ------------------------------------------------------ | ---------------------------------------- |
| Broad natural-language question ("how does auth work?") | `search_docs`, then `get_doc_content`    |
| Semantic keyword search ("payment code")               | `ccg search` (`/ccg` skill)              |
| Module/domain exploration ("payment module structure") | `get_rag_tree` after `search_docs`       |
| Exact generated doc body                               | `get_doc_content`                        |
| Exact signature/location                               | `query_graph`, `get_node` (`/ccg` skill) |
| Dynamic call tracing                                   | `trace_flow` (`/ccg-analyze`)            |

**RAG wins when**: full structure overview, module hierarchy navigation, pre-formatted Markdown content needed. One call returns the tree.

**RAG fails when**: rapidly changing code (stale risk), no annotations (community summaries are weak), exact code metadata needed (ccg is more precise).

## Core Pipeline

```bash
ccg build .                  # 1. Graph nodes/edges
ccg docs --out docs          # 2. Generate Markdown
# Via MCP:
run_postprocess(communities=true, flows=false, fts=false)  # 3. Ensure communities exist
build_rag_index              # 4. Creates .ccg/doc-index.json
get_rag_tree(depth=1)        # 5. Verify the index is non-empty
```

`build_rag_index` builds from docs + communities. If communities are missing, it can
successfully create an empty `doc-index.json` such as `0 communities, 0 files`.
When the result is empty, run `run_postprocess(communities=true, flows=false, fts=false)`
and call `build_rag_index` again.

## RAG Usage Pattern

```
search_docs("auth flow")     # broad question → matching docs + evidence
get_rag_tree(community_id)   # expand specific community
get_doc_content(doc_path)    # fetch exact Markdown body
search_docs("auth")          # focused keyword → tree node candidates
```

## CLI Commands

| Command               | Use                               |
| --------------------- | --------------------------------- |
| `ccg docs --out docs` | Generate Markdown for all modules |
| `ccg index`           | Regenerate index.md only          |
| `ccg lint`            | 8-category quality check          |
| `ccg lint --strict`   | Exit 1 on issues (CI-friendly)    |
| `ccg hooks install`   | Install pre-commit hook           |

## Lint 8 Categories

| Category        | Meaning                          |
| --------------- | -------------------------------- |
| `orphan`        | Doc without matching code        |
| `missing`       | Code without doc                 |
| `stale`         | Code changed but doc didn't      |
| `unannotated`   | Missing `@intent`/`@domainRule`  |
| `contradiction` | Doc contradicts signature        |
| `dead-ref`      | Broken `@see` link               |
| `incomplete`    | Missing `@param`/`@return`       |
| `drift`         | Doc structure diverged from code |

Wire `ccg lint --strict` into CI to prevent the classic problem of wikis aging out of sync with code.

## Lint Rule Customization

`.ccg.yaml` supports regex patterns for `pattern` field:

```yaml
rules:
  - pattern: "pkg/store/.*" # auto-detected as regex
    category: unannotated
    action: ignore
  - pattern: ".*_generated\\.go::.*"
    category: incomplete
    action: warn
```

Patterns containing `$`, `^`, `+`, `{}`, `|`, `\.`, or `.*` trigger regex mode.

## RAG Quality Checkpoints

If RAG answer quality is low, usually one of:

1. **Sparse annotations** → boost `@intent`, `@index` via `/ccg-annotate`
2. **Stale** → re-run `ccg docs` + `build_rag_index`
3. **Wrong communities** → re-run `ccg build .` to recalculate
4. **Empty tree** → run `run_postprocess(communities=true, flows=false, fts=false)`, then retry `build_rag_index`

## MCP Tools

| Tool                  | Use                                                   |
| --------------------- | ----------------------------------------------------- |
| `run_postprocess`     | Rebuild communities before RAG indexing when missing  |
| `build_rag_index`     | Build doc-index.json for the default graph or a named `namespace` |
| `get_rag_tree`        | Navigate and verify the community tree                |
| `get_doc_content`     | Fetch Markdown body                                   |
| `search_docs`         | Keyword search the RAG tree                           |

When the user explicitly asks to build the RAG index through MCP, call
`build_rag_index` through MCP rather than substituting the CLI command. Use CLI
only when the user asks for a terminal command or when MCP is unavailable.

Use `search_docs` to find relevant docs for architecture or "how does
this work?" questions, then `get_doc_content` to read them. Use `search_docs` when the user needs a focused keyword
candidate list rather than a synthesized documentation context.

## Prerequisites

Requires `ccg build .` first. Schema error → `ccg migrate`. (See `/ccg` skill.)
Requires non-empty communities for a useful tree; create or refresh them with
`run_postprocess(communities=true, flows=false, fts=false)` before `build_rag_index`.

Local MCP clients should start CCG with `ccg serve` over stdio. Remote or
self-hosted MCP clients should connect to `ccg-server` over Streamable HTTP.
