---
name: ccg
description: "Fast read-only code discovery with a bounded code-context-graph search and targeted source verification. Use when an ordinary positive lookup or explanation needs an entry point, recorded intent, known-path inventory, or direct relationship evidence. Do not use for absence, completeness, exhaustive inventory, deep flow or impact analysis, or graph writes; use ccg-search-verify for defensible negative claims, and require explicit invocation for ccg-analyze or ccg-build."
metadata:
  version: 3.0.2
  openclaw:
    category: "code-intelligence"
    domain: "core"
  requires:
    bins:
      - ccg
  cliHelp: "ccg --help"
---

# ccg — Fast Search

Use CCG to find one directly relevant entry point, then verify it in current
source. Keep ordinary discovery small enough that it beats broad grep
exploration.

## Task Routing and Entry

| User intent | Start with |
| --- | --- |
| Known filename, identifier, literal, or error text | Grep + Read |
| Unknown entry point, recorded intent, or keyword | One bounded `search` |
| Contents of a known file or folder | `describe` |
| One direct caller/callee fact | One bounded `query_graph` lookup |
| Absence, completeness, or exhaustive inventory | `ccg-search-verify` skill if available |
| Deep flow, change impact, or blast radius | Stop and report that the user must explicitly invoke `ccg-analyze` |
| Generated documentation | `ccg-docs` skill if available |
| Write or repair annotations | `ccg-annotate` skill if available |
| Multiple repositories or services | `ccg-namespace` skill if available |
| Build, update, migrate, postprocess, or scoped graph write | Stop and report that the user must explicitly invoke `ccg-build` |

An ordinary miss is not permission to make a negative claim. When the answer
would become “not found,” “does not exist,” “complete,” or “all,” switch to
`ccg-search-verify` instead of widening this workflow.

Do not invoke `ccg-analyze` automatically. A request about algorithms,
pipelines, impact, callers, or flow does not itself authorize its traversal and
impact-analysis procedure.

## Fast Workflow

1. Reuse a namespace already supplied by repository instructions, configuration,
   or the user. Do not call `get_minimal_context`, `list_namespaces`, or
   `list_graph_stats` for an ordinary search when that information is already
   known.
2. With an exact clue, use grep/read. Otherwise start one `search` with
   `limit: 5`:
   - known thing: one short identifier or rare keyword;
   - unknown symbol: one concise plain-language question that can match recorded
     `@intent` or `@domainRule` reasons.
3. On the current result page, inspect all returned paths, symbols, and
   summaries. Collect the candidates related to the requested component or
   behavior, then run one targeted grep across only those returned paths. Limit
   that candidate check to 20 matching lines, and read at most the strongest one
   or two source ranges. Stop when the verified source provides enough evidence
   to answer. This candidate-set check is not the repository-wide fallback.
4. If the candidate check is insufficient and the response supplies a
   continuation, follow that exact `next` call and repeat step 3. Preserve the
   query, limit, namespace, and continuation offsets supplied by CCG; do not
   calculate offsets or reformulate the query. Perform at most three such
   continuation-and-check cycles, stopping if source evidence answers the
   question or `next` disappears. Do not run repository-wide grep during these
   cycles.
5. Only after `next` disappears or three continuation calls have been consumed,
   use one bounded grep fallback if the question is still unanswered. Restrict
   it to production source where possible, exclude tests by default, and return
   at most 20 matching lines. Read only the best matching source range. Do not
   make a negative claim if this fallback also misses.
6. Use `get_node`, `describe`, or one bounded `query_graph` call only when the
   answer needs exact identity, an unranked known-path inventory, or one direct
   relationship fact.

Every query word must occur in the same indexed document, so do not concatenate
the prompt’s examples into a long query. If the search pages have no qualifying
result, do not fan out into synonyms inside this fast workflow; use the single
bounded grep/read fallback and do not make a negative claim.

```bash
ccg search --limit 5 "<query>"
ccg status  # population only; not proof of freshness
```

Use `ccg <command> --help` rather than relying on remembered flags.

## Freshness Boundary

Current source is authoritative for text, branches, runtime semantics, and
location. CCG supplies indexed intent and relationship candidates.

Positive evidence verified in current source can answer an ordinary question
without a separate freshness preflight. A graph miss, stale result, or missing
namespace cannot support a negative claim; route that task to
`ccg-search-verify`. Call `list_graph_stats` only when the user asks about graph
population or graph state itself.

This skill is read-only. Report a missing or stale graph instead of invoking
`ccg-build`. The user must explicitly name `ccg-build` before any graph write.

## Response Budget Rule

- Start one CCG `search` with `limit: 5`; follow its exact `next` continuation
  at most three times and stop early when verified source evidence answers the
  question.
- For each result page, inspect all returned candidate metadata, run one
  targeted grep across the relevant returned paths with at most 20 matching
  lines, and read only the strongest one or two source ranges.
- Do not run repository-wide grep while an actionable CCG continuation remains.
  After continuation ends, allow only one grep fallback with tests excluded and
  at most 20 matching lines.
- Do not reread a source range solely to obtain line numbers. Preserve line
  numbers on the first read when the response needs source citations.
- Disclose truncation only when the three-continuation cap limits the answer.
- Do not echo raw result lists or mandatory operational reports. Return the
  answer and its relevant paths or symbols.
- Read [`references/supported-languages.md`](references/supported-languages.md)
  only for a supported-language or extension question.

## Boundary

- `search` returns ranked candidates, not proof of absence or completeness.
- Reserve impact-radius and flow-tracing workflows for an explicit
  `ccg-analyze` invocation.
- Use `ccg-search-verify` for defensible negative or exhaustive claims.
- Use `ccg-build` only when explicitly invoked.

## Completion

Answer from the smallest verified evidence set. Name the selected path or
qualified symbol and disclose only uncertainty that materially limits the
answer. Do not append namespace, freshness, limit, or tool-call accounting
unless the user requests it or it changes the conclusion.
