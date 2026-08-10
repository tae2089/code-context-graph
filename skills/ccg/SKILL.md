---
name: ccg
description: "Build, update, inspect, and search code-context-graph graphs and route to specialized CCG workflows. Use when a task needs CCG setup, graph freshness, algorithm or feature-pipeline understanding, exact symbol or relationship lookup, annotation-aware full-text search, safe full or scoped synchronization, MCP graph queries, or selection among CCG analysis, docs, annotation, and namespace skills. Do not use for a simple file or string lookup when grep/read is sufficient."
metadata:
  version: 1.4.0
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
| "Why was this built?" / "where do I start?" — you cannot name the symbol | MCP `search`, phrased as a question | Also scores the reasons authors recorded, then hands back node IDs to walk from |
| "What is in this folder/file?" — you already have a path | MCP `describe` | Lists what the graph holds there, exactly, with no ranking to second-guess |
| "How does this algorithm or feature pipeline work?" | `ccg-analyze` skill | Graph-first narrowing, then source verification |
| "What's affected if I change X?"           | `ccg-analyze` skill   | Graph traversal                       |
| "Understand a module from generated docs"  | `ccg-docs` skill      | `search`, then `get_doc_content` |
| "Document intent/rules in code"            | `ccg-annotate` skill  | AI annotation workflow                |
| "Manage multiple service codebases"        | `ccg-namespace` skill | MSA namespace isolation               |

For an unfamiliar MCP task, call `get_minimal_context` once, then confirm the
selected namespace with `list_graph_stats`. For broad module questions, use
`search` and `get_doc_content`; switch to `query_graph`, `get_node`, or
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

## Searching

One tool, two query shapes. `ccg search` on the CLI and MCP `search` answer
from the same index: node names, file paths, and the reasons authors recorded
as `@intent`/`@domainRule` annotations.

**Shape 1 — you can name the thing.** Quote the identifier or keyword:

```bash
ccg search "결제"               # Candidates containing the term in code/annotations
ccg search "authentication"     # Auth-related
ccg search --path internal/auth "login"  # Path-scoped
ccg search --include-weak "retry"        # Also show candidates with no visible evidence
ccg search --json "login"       # Answer as JSON, same shape the MCP tool returns
```

`--json` is the stable form for scripts and pipelines; the text form is for
reading. Every query word must appear in the same node, so a long sentence of
identifiers usually returns nothing — a short, rare query is the reliable form.

**Shape 2 — you cannot name the symbol yet.** Ask the question in plain words,
as when looking at an incident or unfamiliar code:

```text
search(query: "why do we verify the signature on a push")
```

A question is scored against the recorded reasons as well as names, and the
files those reasons justify are appended after any name matches. A
reason-matched hit carries the recorded `reason` and `matched_terms`, so you
can see whether it answered your question or merely shared a common word with
it. Common English function words (`the`, `how`, `what`, …) are stripped first.

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
so paging never splits a file. Across several repositories (MCP `namespaces:
[]`) both are per repository: `limit: 5` over three repositories is five files
from each, and every repository with a hit is on the page whatever the limit is.
An empty result says which kind of empty it is — nothing retrieved, nothing
justifiable, or an offset past the last file.

Order comes from name similarity, then path overlap. `@intent` is shown for the
reader to judge; it does not move a result up.

Over MCP the answer also says what it withheld, in two separate signals.
Results come back as `files[] {file_path, hit_count, hits[]}`. `truncated` is
true when more files answered the query than this page reached — never about
hits, since a shown file is shown whole. `pool_truncated` is true when the
candidate pool behind this page came back full: the backend held at least as
many candidates as the pool had room for, and may have held more.

Read both before calling a search complete:

| `truncated` | `pool_truncated` | what it means |
| ----------- | ---------------- | ------------------------------------------------------------------------------------------------------- |
| `false`     | `false`          | this is the whole answer |
| `true`      | either           | more files answered this query; page on |
| `false`     | `true`           | this page ended at the edge of the candidates that were fetched, not at the end of the answer; page on anyway |

That last row is the one that used to read as "that is everything". One file
holding a page's worth of hits can fill the candidate pool by itself, so there
are no further files left to count as `truncated` while later files are still
waiting to be fetched.

`limits` gives `files` (the page size), `offset` (where it
started), and `hit_budget` (the point past which a page stops taking on *more*
files; it never trims one, and the first file of a page is always included).
`next` lists ready-to-make calls: whichever of the two signals is up, the next
page becomes `search(query: <same query>, limit: <same>, offset: <next>)`, and
cut candidates become `search(query: <same query>, include_weak: true)`. Make
the call in `next` rather than inventing one. Over several repositories that
matters more than usual: `offset` moves every repository through its own list,
so it is not this page's offset plus the files you were shown, and computing it
that way skips files in one repository while repeating them in another.

Every hit carries a `node_id`. Hand that ID straight to `get_node`,
`query_graph`, `get_impact_radius`, or `trace_flow` — the answer exists to
start a graph walk, not to finish the investigation.

**When a question comes back empty or weak, the search is not broken — the
reasons were never written down.** A question mostly made of words nobody
recorded gets no intent hits by design. Hand off to the `ccg-annotate` skill:
annotate the area under investigation, rebuild the graph, then re-ask the same
question. That loop, not query rephrasing, is what makes questions answerable.

**Difference from Grep**: Grep scans source text directly. CCG full-text search
queries indexed symbol fields and annotations together. Searching "결제" can find
a `payment` function when its annotation contains "결제 처리"; search does not
infer translations or arbitrary synonyms that are absent from the index.

## Reading a Path You Already Have

`search` ranks; it can be wrong about what matters.
`describe` does not rank, so it cannot be. Once either tool hands back a path —
or a stack frame or a diff does — read it with `describe`:

```text
describe(target: "internal/app/search")        # folders and files one level down
describe(target: "internal/app/search/intent/intent.go")  # every declaration in it
```

A folder answer collapses to its immediate children, each with how many files
and declarations sit under it, so you descend one step at a time. A file answer
lists every declaration in written order with its line range, its `node_id`, and
its recorded `@intent`. There is no limit and no relevance: what comes back is
what is stored.

A target the graph does not hold comes back as `scope: "unknown"` with the
places that name is actually declared, plus the calls that find it — not as an
error and not as an empty result you have to interpret.

## Graph Freshness

1. Inspect namespace population with `ccg status` or MCP `list_graph_stats`;
   counts prove population, not freshness.
2. Use `ccg build .` for first use, an intentional full rebuild, or recovery.
3. Use `ccg update .` after ordinary source edits.
4. Refresh stored flows separately with MCP `run_postprocess(flows=true)` after
   graph changes when `list_flows` or `get_affected_flows` must be current.
5. If a command reports schema drift, or when upgrading PostgreSQL/an existing
   database, run `ccg migrate` and retry.

Over MCP, prefer `build_or_update_graph` for normal synchronization. Its
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
result first. `query_graph`, `list_flows`, `detect_changes`, and
`get_affected_flows` all take `limit` and `offset`.

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
