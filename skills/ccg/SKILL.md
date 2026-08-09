---
name: ccg
description: "Build, update, inspect, and search code-context-graph graphs and route to specialized CCG workflows. Use when a task needs CCG setup, graph freshness, algorithm or feature-pipeline understanding, exact symbol or relationship lookup, annotation-aware full-text search, safe full or scoped synchronization, MCP graph queries, or selection among CCG analysis, docs, annotation, and namespace skills. Do not use for a simple file or string lookup when grep/read is sufficient."
metadata:
  version: 1.3.1
  openclaw:
    category: "code-intelligence"
    domain: "core"
  requires:
    bins:
      - ccg
  cliHelp: "ccg --help"
---

# ccg — Routing & Search

Use CCG for current graph evidence and use grep/read for direct source evidence.

## Task Routing and Entry

| User intent                                | Tool              | Why                               |
| ------------------------------------------ | ----------------- | --------------------------------- |
| "Where is X?" — simple location lookup     | Grep + Read       | Direct source lookup              |
| "Find code related to X" — keyword/intent search | `ccg search` | Indexed code and annotations |
| "How does this algorithm or feature pipeline work?" | `ccg-analyze` skill | Graph-first narrowing, then source verification |
| "What's affected if I change X?"           | `ccg-analyze` skill   | Graph traversal                       |
| "Understand a module from generated docs"  | `ccg-docs` skill      | `search_docs`, then `get_doc_content` |
| "Document intent/rules in code"            | `ccg-annotate` skill  | AI annotation workflow                |
| "Manage multiple service codebases"        | `ccg-namespace` skill | MSA namespace isolation               |

For an unfamiliar MCP task, call `get_minimal_context` once, then confirm the
selected namespace with `list_graph_stats`. For broad module questions, use
`search_docs` and `get_doc_content`; switch to `query_graph`, `get_node`, or
`trace_flow` when the answer needs exact symbols or relationships.

Do not rebuild the graph or regenerate docs merely to start a read-only query.
Refresh only when the graph is missing, relevant source changed, or requested
artifacts must be regenerated.

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
ccg search --include-weak "retry"        # Also show candidates with no visible evidence
```

**Read the result list as evidence, then decide.** Each hit prints on one
unindented line, with its `@intent` and the signals the query matched on an
indented line below it:

```text
webhook.WebhookHandler.verifySignature	function	internal/adapters/inbound/webhook/handler.go:177
    authenticate webhook payloads before the sync pipeline trusts their repository metadata. [name path intent]
```

`[name path intent]` names where the query landed: the node name, a whole
segment of the file path, or the node's own `@intent`. A candidate that matches
none of the three is dropped and only counted, so the list never pads itself to
`limit` with hits it cannot justify; `--include-weak` (MCP `include_weak: true`)
brings them back. Hits arrive grouped by file, and a file that appears appears
whole — every hit it answered with is printed, however many that is. `limit`
counts files, not hits, and `--offset` (MCP `offset`) moves to the next files,
so paging never splits a file. An empty result says which kind of empty it is —
nothing retrieved, nothing justifiable, or an offset past the last file.

Order comes from name similarity, then path overlap. `@intent` is shown for the
reader to judge; it does not move a result up.

Over MCP the answer also says what it withheld. Results come back as
`files[] {file_path, hit_count, hits[]}`. `truncated` is true when more files
answered the query than this page reached — never about hits, since a shown file
is shown whole. `limits` gives `files` (the page size), `offset` (where it
started), and `hit_budget` (the point past which a page stops taking on *more*
files; it never trims one, and the first file of a page is always included).
`next` lists ready-to-make calls: more files become
`search(query: <same query>, limit: <same>, offset: <next>)`, and cut candidates
become `search(query: <same query>, include_weak: true)`. Make the call in
`next` rather than inventing one.

**Difference from Grep**: Grep scans source text directly. CCG full-text search
queries indexed symbol fields and annotations together. Searching "결제" can find
a `payment` function when its annotation contains "결제 처리"; search does not
infer translations or arbitrary synonyms that are absent from the index. Every
query word must appear in the same node, so a long sentence usually returns
nothing — common English function words (`the`, `how`, `what`, …) are stripped
first, but a short, rare query is still the reliable form.

## Graph Freshness

1. Inspect namespace population with `ccg status` or MCP `list_graph_stats`;
   counts prove population, not freshness.
2. Use `ccg build .` for first use, an intentional full rebuild, or recovery.
3. Use `ccg update .` after ordinary source edits.
4. Refresh stored flows separately with MCP `run_postprocess(flows=true)` after
   graph changes when `list_flows` or `get_affected_flows` must be current.
5. If a command reports schema drift, or when upgrading PostgreSQL/an existing
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
incremental update.

## Scoped Update Safety

For scoped incremental updates, choose replacement semantics deliberately:

- `include_paths` with the default `replace=true` treats the selected scope as
  authoritative and removes previously indexed out-of-scope files from the
  namespace.
- Pass `replace=false` to preserve out-of-scope files while still reconciling
  deletions inside the selected scope.
- Omit `include_paths` when the entire source root is authoritative.

Use `parse_project` only when a full graph write without search postprocessing
is intentional, then call `run_postprocess` if flows or FTS must be current.
The registered `communities` option is ignored by the current
`run_postprocess` handler; do not report community state as rebuilt. Inspect
`failed_steps` and `skipped_steps` before treating any postprocess result as
current.

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
- Treat algorithm, feature-flow, and pipeline questions as relationship analysis rather than simple location lookup.
- Use `ccg search` for intent and annotation candidates, not exact graph proof.
- Use specialized CCG skills for analysis, docs, annotations, or namespaces.
- Report stale or missing graph state instead of presenting it as current.
- Never assume a degraded postprocess result refreshed every requested artifact.

## Completion

Before finishing, state the namespace and freshness evidence used (or say it
was not verified), name the evidence-producing tools or commands, report result
limits and truncation, record the chosen `replace` behavior for scoped updates,
and list any failed/skipped postprocess step, source fallback, or verification
that was not run.
