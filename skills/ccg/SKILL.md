---
name: ccg
description: "Fast read-only code discovery with a bounded code-context-graph search and targeted source verification. Use when an ordinary positive lookup or explanation needs an entry point, recorded intent, known-path inventory, or direct relationship evidence. Do not use for absence, completeness, exhaustive inventory, deep flow or impact analysis, or graph writes; use ccg-search-verify for defensible negative claims, and require explicit invocation for ccg-analyze or ccg-build."
metadata:
  version: 3.0.5
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
3. Classify every returned candidate as `production`, `test`, or `unknown`.
   Confirm `test` only from, in priority order: CCG test-node metadata; explicit
   repository source-set or build rules; or a repository/language test path or
   filename convention. If none applies, keep the candidate `unknown` rather
   than guessing. For a question not about tests, defer confirmed test
   candidates while any unverified production or unknown candidate remains. If
   the page contains only tests and has `next`, continue before reading tests.
4. On the current result page, collect the active production and unknown
   candidates related to the requested component or behavior. A candidate check
   may search only paths explicitly returned on that page; do not replace them
   with parent directories or guessed paths. Inspect the strongest one or two
   files, starting with narrow source ranges and extending within those files
   only when the declaration or control flow continues. Stop when the verified
   source provides enough evidence to answer. For a test-focused question,
   include confirmed test candidates from the start.
5. If the candidate evidence is insufficient and the response supplies a
   continuation, the next action must be that exact `next` call. Preserve the
   query, limit, namespace, and continuation offsets supplied by CCG; do not
   calculate offsets or reformulate the query. Repeat steps 3 and 4 after each
   page for at most three continuation calls, stopping if source evidence
   answers the question or `next` disappears.
6. Any search that includes a path not explicitly returned on the current CCG
   page is a fallback search, regardless of the path's name or apparent size.
   Enter the fallback phase only when `next` is absent or three continuation
   calls have been consumed. In that single phase, run one bounded grouped
   search and, only if its results are ambiguous, one narrower refinement.
   Apply the same three-way classification to direct-search results and inspect
   production and unknown files first. If they still provide insufficient
   evidence, confirmed tests may be read as supporting evidence. Do not make a
   negative claim if fallback also misses.
7. Use `get_node`, `describe`, or one bounded `query_graph` call only when the
   answer needs exact identity, an unranked known-path inventory, or one direct
   relationship fact.

Every query word must occur in the same indexed document, so do not concatenate
the prompt’s examples into a long query. Do not fan out into synonym searches
inside this fast workflow.

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
- For each result page, search only paths explicitly returned on that page and
  inspect at most the strongest one or two files. Start with narrow ranges and
  extend within those files only when required by the code structure.
- For non-test questions, prioritize production and unknown candidates. Defer
  confirmed tests until CCG continuations and direct production-source evidence
  are insufficient; never classify an ambiguous path as test merely to skip it.
- Treat every scope expansion beyond returned paths as fallback. Enter fallback
  once, only after `next` is absent or three continuations have been consumed;
  allow one grouped search plus at most one narrower refinement.
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
