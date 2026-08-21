---
name: ccg
description: "Fast read-only code discovery with mandatory structured CCG search for unknown entry points, followed by bounded source verification. Use for ordinary positive lookups, recorded intent, known-path inventory, or one direct relationship fact. Do not use for absence, completeness, exhaustive inventory, deep flow or impact analysis, or graph writes."
metadata:
  version: 4.0.0
  openclaw:
    category: "code-intelligence"
    domain: "core"
  requires:
    bins:
      - ccg
  cliHelp: "ccg --help"
---

# ccg — Fast Search

Find one useful CCG candidate, verify it in current source, and stop as soon as
the positive question is answered.

## Route

| User intent | Route |
| --- | --- |
| Known filename, identifier, literal, or error text | Grep + Read may start directly |
| Unknown entry point, behavior, reason, or keyword | Run the Core Loop below |
| Contents of a known file or folder | One `describe` call |
| One direct caller/callee fact | One bounded `query_graph` call |
| Absence, completeness, or exhaustive inventory | `ccg-search-verify` |
| Deep flow, change impact, or blast radius | Require explicit `ccg-analyze` invocation |
| Build, update, migrate, postprocess, or graph write | Require explicit `ccg-build` invocation |

An ordinary miss never authorizes a negative claim. If the answer would become
“not found,” “does not exist,” “complete,” or “all,” switch to
`ccg-search-verify`.

## Core Loop

For an unknown entry point, behavior, reason, or keyword, follow these steps in
order:

1. **Search.** Before grep or source browsing, run one structured CCG `search`
   with `limit: 5`. Prefer MCP. If MCP is unavailable, use
   `ccg search --json --limit 5 "<query>"`; never use the plain CLI display.
2. **Verify this page.** Inspect only relevant `production` and `unknown` paths
   returned on the current page. Each source read must verify one concrete
   claim. Read the exact declaration and its attached documentation, not broad
   file ranges.
3. **Decide.** If verified evidence answers the question, stop. If it does not
   and the response contains `next`, the next action must be that continuation
   verbatim. Do not grep a wider scope while an allowed continuation remains.
4. **Continue.** Repeat page verification and decision for at most three `next`
   calls. Preserve the query, limit, namespace, and offsets supplied by CCG;
   never calculate or rewrite them.
5. **Fallback.** Only when `next` is absent or three `next` calls have been used,
   search current source directly. The first fallback search may expand scope;
   every later search must narrow from a new concrete path, symbol, literal, or
   error text found by the preceding step.
6. **Stop.** Answer when the smallest verified evidence set is sufficient. If a
   search yields no new clue, stop and report that the entry point could not be
   located without claiming that the code does not exist.

Do not replace a returned path with a guessed directory, reread an examined
range, fan out into synonym searches, or echo raw candidate lists.

## Conditional Details

Read
[`references/search-execution.md`](references/search-execution.md) only when a
branch needs it:

- MCP is unavailable and CLI fallback is required;
- the user's language differs from repository annotations or comments;
- a candidate may be test-only or declaration bounds are missing;
- the direct-source fallback begins.

Read
[`references/supported-languages.md`](references/supported-languages.md) only
for a supported-language or extension question.

## Boundaries

- Current source is authoritative for text, location, and runtime semantics;
  CCG supplies ranked intent and relationship candidates.
- Positive evidence verified in current source needs no freshness preflight.
  A graph miss or stale result is never negative evidence.
- Do not call `get_minimal_context`, `list_namespaces`, or `list_graph_stats`
  when repository instructions or configuration already supply the namespace.
- Use `get_node` only for exact identity or declaration bounds, and follow
  another declaration only when the selected declaration directly references
  it.
- This skill is read-only. Never invoke graph build, update, migration,
  postprocessing, impact-radius, or flow-tracing tools from this workflow.

## Completion

Return the answer with the relevant path or qualified symbol. Mention
truncation only when the three-continuation cap limits the answer, and disclose
only uncertainty that materially changes the conclusion. Do not append tool,
namespace, freshness, or call-count reports unless requested.
