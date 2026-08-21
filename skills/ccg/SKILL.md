---
name: ccg
description: "Fast read-only code discovery with a bounded code-context-graph search and targeted source verification. Use when an ordinary positive lookup or explanation needs an entry point, recorded intent, known-path inventory, or direct relationship evidence. Do not use for absence, completeness, exhaustive inventory, deep flow or impact analysis, or graph writes; use ccg-search-verify for defensible negative claims, and require explicit invocation for ccg-analyze or ccg-build."
metadata:
  version: 4.9.0
  openclaw:
    category: "code-intelligence"
    domain: "core"
  requires:
    bins:
      - ccg
  cliHelp: "ccg --help"
---

# ccg — Fast Search

Find the smallest source-verified evidence set that answers an ordinary positive
code question.

## Route

| Request | Route |
| --- | --- |
| Source path already known | Targeted Grep + Read |
| Source path unknown, including an exact identifier | Core Loop |
| Known file or folder inventory | One `describe` call |
| One direct caller/callee fact | One bounded `query_graph` call |
| Absence, completeness, or exhaustive inventory | `ccg-search-verify` |
| Deep flow or impact analysis | Require explicit `ccg-analyze` invocation |
| Graph write or refresh | Require explicit `ccg-build` invocation |

An ordinary miss is not evidence that code does not exist. Route any negative or
complete claim to `ccg-search-verify`.

## Core Loop

1. **Search first when the path is unknown.** Run one initial structured CCG
   `search` with `limit: 5`. Use an exact identifier, literal, or error text as a
   compact query. If the request already contains one focused code question, use
   it verbatim after removing only command wrappers or output instructions. Do
   not compress a behavior or reason question into keywords. Otherwise ask one
   focused natural-language question in the repository's vocabulary. CCG tries
   the precise match first, then uses OR
   matching with BM25/IDF, rewards more distinct terms, and rejects single-term
   coincidences. Choose the structured surface from available routing evidence:
   use MCP when repository instructions provide explicit MCP or server-visible
   routing; otherwise, when local `ccg` and a repository-local `.ccg.yaml` exist,
   use JSON CLI with `ccg search --json --compact --limit 5 "<query>"`; use MCP
   when no usable local configuration exists. Pass `compact: true` to MCP search.
   Compact mode keeps paths, declaration bounds, evidence, and continuations
   while omitting redundant storage fields. MCP tool availability or MCP
   documentation alone is not routing evidence; explicit routing names the
   target namespace together with MCP or a server-visible repository path. JSON
   CLI reads its namespace and database from `.ccg.yaml`. For MCP, when
   configuration supplies a namespace, include it in the initial search
   arguments and every continuation. When a namespace must be extracted, read
   only the `namespace:` field, never the full configuration.
2. **Choose evidence from the whole page.** Compare the returned file paths,
   matched signals, reasons, and declaration hits. Start with the production hit
   that most directly addresses the question, not automatically the first row.
   Read each chosen declaration's exact range separately; do not span unrelated
   hits by reading from a file's earliest result to its latest. Do not re-locate
   a hit with grep when `start_line` and `end_line` are present. Use a targeted
   in-file locator only for missing bounds or a helper named by the verified
   declaration. For a production-behavior question, do not read tests to
   corroborate production behavior already established by current source. Tests
   become evidence only when the user asks about them or the production source
   leaves a material ambiguity.
3. **Stop by claim sufficiency.** Before another tool call, name the specific
   evidence gap in the user's question and state that gap in one sentence. If
   no material evidence gap can be named, answer. A verified current-source call
   site plus the invoked declaration or contract establishes that mechanism; do
   not trace constructor or dependency-injection wiring unless the user asks
   which runtime implementation or configuration is selected. Use these
   completion rules:
   - **Where:** the current-source declaration is enough.
   - **How/what:** the branch or contract directly implementing the requested
     behavior is enough; include public input or output only when asked.
   - **Why:** one directly relevant author-recorded design reason plus current
     source confirming its mechanism closes the rationale gap unless the user
     requests several reasons; secondary consequences are optional. Do not
     trace downstream work merely to prove optional consequences.
   - **One relationship:** the edge and the endpoint declaration needed to
     interpret it are enough.
   Mandatory stop: when the matching condition is satisfied, the next action
   must be the final answer. Another tool call is allowed only when current
   source contradicts the recorded evidence or the user explicitly requested
   additional distinct reasons or details.
4. **Follow a new exact clue before semantic paging.** When verified source
   reveals an identifier or literal that names a remaining usage, caller, or
   exposure gap, run a path-only reference lookup such as `rg -l` before semantic
   paging. Print file names only, exclude test-like paths unless relevant, then
   read the necessary production declarations. Do not create a new evidence gap
   from identifiers encountered after the original question is already answered.
   Never print repository-wide match context.
5. **Page only for an unresolved semantic gap.** A truncation flag makes `next`
   available; it does not require paging. If the current page and source-derived
   exact clues cannot close the named gap, follow the returned `next` call
   verbatim. Preserve query, limit, namespace, and offsets. Use at most three
   `next` calls.
6. **Handle a miss once.** An exact-identifier miss goes directly to a path-only
   source lookup. For a natural-language miss, retry local JSON CLI once only
   when it demonstrably uses a different graph or runtime than MCP. Do not repeat
   the same local graph query through another interface. If direct source search
   yields no new clue, stop without making a negative claim.

## Conditional Details

Do not load a reference merely because declaration bounds are missing; Step 2's
targeted in-file locator is the complete recovery rule. Read
[`references/search-execution.md`](references/search-execution.md) only for
language translation, uncertain test classification, or ambiguous continuation
metadata. Read
[`references/supported-languages.md`](references/supported-languages.md) only for
a language-support question.

## Boundaries

- Current source is authoritative for location and runtime semantics; CCG ranks
  entry points, intent, and relationships.
- Do not call `get_minimal_context`, namespace-list, or graph-stat tools when the
  namespace is already configured.
- After starting Core Loop search, do not add `query_graph`, `get_node`, flow, or
  impact calls merely to enrich an already supported answer.
- This workflow is read-only. Never build, update, migrate, or postprocess.
  Do not invoke `ccg-analyze` automatically; deeper analysis requires explicit
  `ccg-analyze` invocation.

## Completion

Return the answer with relevant paths or qualified symbols. Mention truncation
only when the continuation cap limits the answer. Do not append namespace,
freshness, or call-count reports unless requested.
