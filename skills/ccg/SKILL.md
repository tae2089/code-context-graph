---
name: ccg
description: "Build, update, inspect, and search code-context-graph graphs and route to specialized CCG workflows. Use when a task needs CCG setup, graph freshness, exact symbol or relationship lookup, annotation-aware full-text search, MCP graph queries, or selection among CCG analysis, docs, annotation, and namespace skills. Do not use for a simple file or string lookup when grep/read is sufficient."
metadata:
  version: 1.1.0
  openclaw:
    category: "code-intelligence"
    domain: "core"
  requires:
    bins:
      - ccg
  cliHelp: "ccg --help"
---

# ccg — Routing & Search

ccg is a Tree-sitter-based code graph tool. **Complementary to Grep/Read, not a replacement.** Choose based on task type.

## Task Routing (most important)

| User intent                                | Tool              | Why                               |
| ------------------------------------------ | ----------------- | --------------------------------- |
| "Where is X?" — simple location lookup     | Grep + Read       | Faster and cheaper than ccg       |
| "Find code related to X" — keyword/intent search | `ccg search` | Full-text match over code and annotations |
| "What's affected if I change X?"           | `ccg-analyze` skill   | Graph traversal                       |
| "Understand a module from generated docs"  | `ccg-docs` skill      | `search_docs`, then `get_doc_content` |
| "Document intent/rules in code"            | `ccg-annotate` skill  | AI annotation workflow                |
| "Manage multiple service codebases"        | `ccg-namespace` skill | MSA namespace isolation               |

**Don't use ccg when Grep is enough.** Graph queries add setup and response context that a direct file or string lookup does not need.

## Core Commands

```bash
ccg build .          # Full graph + search-index rebuild
ccg update .         # Incremental — changed files only
ccg search "<query>" # FTS search (includes annotations)
ccg status           # Graph statistics
ccg docs --out docs  # Generate docs + Wiki compatibility index
ccg serve            # Start MCP server (stdio)
```

For remote or self-hosted MCP over Streamable HTTP, use `ccg-server` instead of
`ccg serve`. Local `ccg serve` is stdio-only.

For detailed flags, use `ccg <command> --help` or refer to MCP schema.

When the task asks which languages or file extensions CCG supports, read
[`references/supported-languages.md`](references/supported-languages.md).

## ccg search Patterns

Search by code, domain, or annotation keywords. Annotation tags (`@intent`,
`@domainRule`) are indexed alongside code.

```bash
ccg search "결제"               # Candidates containing the term in code/annotations
ccg search "authentication"     # Auth-related
ccg search --path internal/auth "login"  # Path-scoped
```

**Difference from Grep**: Grep scans source text directly. CCG full-text search
queries indexed symbol fields and annotations together. Searching "결제" can find
a `payment` function when its annotation contains "결제 처리"; search does not
infer translations or arbitrary synonyms that are absent from the index.

## Graph Freshness

1. Inspect namespace population with `ccg status` or MCP `list_graph_stats`;
   counts alone do not prove freshness.
2. Use `ccg build .` for first use, an intentional full rebuild, or recovery.
3. Use `ccg update .` after ordinary source edits.
4. If a command reports schema drift, or when upgrading PostgreSQL/an existing
   database, run `ccg migrate` and retry.

## Core MCP Tools (commonly used)

| Tool                    | When                                                  |
| ----------------------- | ----------------------------------------------------- |
| `get_minimal_context`   | Choose a bounded next tool for an unfamiliar task     |
| `list_graph_stats`      | Confirm namespace population before interpreting data |
| `parse_project`         | Full parse/write that skips search postprocessing      |
| `build_or_update_graph` | Build or incrementally synchronize through MCP        |
| `run_postprocess`       | Refresh stored flows and/or FTS without reparsing      |
| `search`                | Annotation-aware full-text candidate search           |
| `query_graph`           | Structured queries (callers/callees/imports)          |
| `get_node`              | Lookup by qualified name                              |

For other tools, use the `ccg-analyze` or `ccg-docs` skill when available.

Prefer `build_or_update_graph` for normal MCP synchronization. Its
`full_rebuild` default is true, so pass `full_rebuild=false` explicitly for an
incremental update. Use `parse_project` only when a full graph write without
search postprocessing is intentional, then call `run_postprocess` if flows or
FTS must be current. The registered `communities` option is not implemented by
the current `run_postprocess` handler; do not report community state as rebuilt.

## Agent Entry Pattern

When MCP is available, call `get_minimal_context` once for an unfamiliar task,
then confirm the selected namespace with `list_graph_stats`. For broad module
questions, use `search_docs` to find candidate generated docs and
`get_doc_content` to read a selected file. Switch to `query_graph`, `get_node`,
or `trace_flow` when the answer needs exact symbols or relationships.

Do not rebuild the graph or regenerate docs merely to start a read-only query.
Refresh only when the graph is missing, the relevant source changed, or the
requested output must be regenerated.

## Response Budget Rule

For LLM-agent use, prefer bounded graph queries. Start with `limit=50` or
`limit=100` and follow `has_more` / `next_offset` rather than asking for a bulk
result first.

Tools with explicit pagination:

| Tool | Parameters |
| ---- | ---------- |
| `query_graph` | `limit`, `offset` |
| `list_flows` | `limit`, `offset` |
| `detect_changes` | `limit`, `offset` |
| `get_affected_flows` | `limit`, `offset` |

Broad architecture/onboarding prompts should start with a namespace or path and
a narrow question before expanding through graph queries.

## Boundary

- Use grep/read for a known filename, exact string, or one obvious location.
- Use `ccg search` for intent and annotation candidates, not as a substitute for exact graph evidence.
- Use specialized CCG skills when the task is analysis-, docs-, annotation-, or namespace-specific.
- Report stale or missing graph state instead of presenting it as current evidence.

## Trade-offs

- Annotation-aware full-text search and graph traversal reduce broad source reading when the graph is current.
- Single-location lookup is cheaper with grep/read.
- Annotation search can surface domain rules that plain string matching misses.
- Frequently changing code requires graph freshness checks or incremental updates.

## Completion

Before finishing a CCG task, state the namespace and freshness evidence used
(or say freshness was not verified), name the tools or commands that supplied
evidence, keep result limits bounded, and report any fallback to grep/read or
verification that was not run.
