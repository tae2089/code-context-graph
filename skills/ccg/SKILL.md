---
name: ccg
description: "Inspect and search code-context-graph graphs without mutating them. Use when a task needs annotation-aware discovery, bounded symbol or relationship lookup, graph population or freshness assessment, hybrid CCG plus source verification before absence or completeness claims, or routing to docs, annotation, and namespace workflows. Do not invoke ccg-analyze, graph build, update, migration, or postprocessing; ccg-analyze and ccg-build are available only when the user explicitly names them."
metadata:
  version: 2.1.0
  openclaw:
    category: "code-intelligence"
    domain: "core"
  requires:
    bins:
      - ccg
  cliHelp: "ccg --help"
---

# ccg — Routing & Search

Use CCG for indexed intent and relationship evidence. Use grep/read for current
source evidence.

## Load Detail Only When Needed

Keep the default workflow small. Read an additional reference completely only
when its trigger applies:

| Trigger | Reference |
| --- | --- |
| Absence/completeness claim, exhaustive search, or no credible hit when a result signal must decide the next action | [`references/search-contract.md`](references/search-contract.md) |
| Supported language or extension question | [`references/supported-languages.md`](references/supported-languages.md) |

Do not load those references for an ordinary location lookup or a ranked search
whose credible candidate has already been verified in source.

## Task Routing and Entry

| User intent | Start with |
| --- | --- |
| Known filename, identifier, literal, or error text | Grep + Read |
| Code related to a keyword or recorded intent | `search` |
| Why something exists when the symbol is unknown | `search` with a plain-language question |
| Contents of a known file or folder | `describe` |
| Ordinary algorithm, pipeline, or feature explanation | One bounded `search`, then verify the selected source |
| Deep flow, change impact, or blast-radius analysis | Stop and report that the user must explicitly invoke `ccg-analyze` |
| Generated documentation | `ccg-docs` skill if available |
| Write or repair annotations | `ccg-annotate` skill if available |
| Multiple repositories or services | `ccg-namespace` skill if available |
| Build, update, migrate, postprocess, or scoped graph write | Stop and report that the user must explicitly invoke `ccg-build` |

For an unfamiliar MCP task, call `get_minimal_context` once and confirm the
namespace with `list_graph_stats`. Do not rebuild merely to begin a read-only
query.

Do not invoke `ccg-analyze` automatically. A request about algorithms, feature
pipelines, impact, callers, or flow does not itself authorize that workflow.
Keep ordinary discovery bounded; the user must name `ccg-analyze` when they want
its traversal and impact-analysis procedure.

## Core Commands

```bash
ccg search --limit 5 "<query>"  # Ranked files; annotations are indexed
ccg status                      # Population, not freshness
```

Use `ccg-server` for remote Streamable HTTP MCP. Use `ccg <command> --help` for
flags rather than relying on remembered syntax.

## Search Fast Path

CLI `ccg search` and MCP `search` query the same index: node names, paths, and
recorded `@intent`/`@domainRule` reasons.

Choose one query shape, not both:

1. **Known thing:** use a short identifier or rare keyword.

   ```bash
   ccg search --limit 5 "verifySignature"
   ccg search --limit 5 --path internal/auth "login"
   ```

   Every query word must occur in the same indexed document, so long strings of
   identifiers often return nothing.

2. **Unknown symbol:** ask the question in plain language.

   ```text
   search(query: "why do we verify the signature on a push", limit: 5)
   ```

   This also scores recorded reasons and returns `reason` and `matched_terms`
   when they justify a hit.

Start with five files. Verify the best credible candidate in current source and
stop when the task asks for an answer, not an inventory. Increase the limit or
page only when no candidate is credible or completeness matters. A returned
`node_id` can start `get_node` or one bounded `query_graph` lookup when an
ordinary answer needs relationship evidence. Reserve impact-radius and
flow-tracing workflows for an explicit `ccg-analyze` invocation.

## Hybrid Search Workflow

Grep/read and CCG are complementary:

1. With an exact clue, start with grep/read. Add CCG for intent, relationships,
   impact, or completeness.
2. Without a symbol, start with CCG. Feed its qualified names, paths, and rare
   reason terms into grep/read for source verification.
3. Shape 1 and Shape 2 are alternatives. For an ordinary discovery task, run
   both only when the task contains both kinds of clue. For absence or
   completeness, the single-shape query budget in `search-contract.md`
   overrides this rule even when the prompt names several examples.
4. Merge results by file and symbol. Preserve whether evidence came from exact
   text, an identifier, a recorded reason, or a graph relationship.

Treat current source as authority for text and location. Treat CCG as candidate
evidence for intent and relationships, then verify those claims with source or
the relevant graph query.

### Before an Absence or Completeness Claim

Read [`references/search-contract.md`](references/search-contract.md)
completely, then:

1. Spend one grouped grep/read pass and one distinct CCG query. Named examples
   are search terms, not permission to fan out into separate queries.
2. Verify that the graph is current against relevant source changes. Merely
   checking freshness and discovering a stale graph is not successful freshness
   verification. `ccg status` proves only population.
3. When either truncation signal is true, invoke the exact paging call in
   `next`; never calculate or modify `offset`.

A miss in either source search or CCG alone is not proof of absence.
`include_weak`, `describe`, and graph traversal remain conditional: use them
only when `next`, a known path, or a relationship claim respectively requires
them.

If freshness remains unverified, report only that the target was not found
within the checked evidence; never make an unqualified absence claim. An
exhaustive grep cannot compensate for unverified graph freshness. Never say
that a freshness gap does not affect the conclusion.

## Read a Known Path

Use `describe(target: <path>)` when a path or candidate scope is already known
and an unranked declaration inventory would answer the question. `describe`
removes ranking uncertainty but still reports only stored graph state, so
freshness limits remain. An unknown target returns `scope: "unknown"` plus the
places that actually declare or call that name.

## Freshness Boundary

`ccg status` and `list_graph_stats` prove that graph data exists, not that it
matches current source. Compare available graph provenance with relevant source
changes before relying on a miss or relationship result.

This skill is read-only. When the graph is missing or stale and the task needs a
current graph, stop the read workflow. Do not invoke `ccg-build` automatically.
Report the freshness gap and state that the user may explicitly name
`ccg-build` in a new request. Do not perform graph writes from this skill.

## Response Budget Rule

- Do not invoke this skill for one obvious file or exact string with no intent,
  relationship, or completeness question.
- Start ranked search at five files and graph-list queries at 50 items.
- For absence or completeness, one grouped source pass plus one distinct CCG
  query is the default budget; exact `next` paging does not consume another
  distinct query.
- Read only the source range needed to verify the candidate.
- Do not exhaust pages for an ordinary answer; disclose truncation if stopping.
- Do not echo raw result lists. Merge corroborating evidence and summarize it.
- Load only the reference whose trigger applies.

## Boundary

- `search` produces ranked candidates, not exact graph proof.
- Report stale or missing graph state instead of presenting it as current.
- Use specialized CCG skills for docs, annotations, and namespaces when their
  routing applies. Use `ccg-analyze` and `ccg-build` only when the user
  explicitly names the respective skill.

## Completion

For read-only work, report the namespace, freshness evidence or gap, commands or
tools used, result limit, truncation state, and whether grep/read corroborated
CCG when absence or completeness was claimed.

When asked which skill resources were read, report their resolved absolute
paths when available; do not substitute repeated basenames. A basename-only
skill report is incomplete.
