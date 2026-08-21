# Search Execution Details

Use only the section required by the active branch of the `ccg` Core Loop.

## Structured Search Interfaces

Prefer MCP `search` with `limit: 5`. When MCP is unavailable, use JSON CLI
output so node IDs, declaration bounds, truncation, and continuation arguments
remain machine-readable:

```bash
ccg search --json --limit 5 "<query>"
```

For MCP, execute the returned `next` call verbatim. For CLI JSON, execute the
command represented by `next.args` without calculating offsets or changing the
query, limit, or namespace. Plain CLI output is not a substitute because it can
hide structured continuation and declaration data.

## Query Language

Use one short identifier or rare keyword for a known thing. When the symbol is
unknown, use one concise question that can match recorded `@intent` or
`@domainRule` text.

Write natural-language terms in the dominant language of repository comments
and annotations. Translate the user's intent when needed, but preserve exact
identifiers, paths, literals, and error messages. Do not combine translations
or prompt examples into one long query: every query word must occur in the same
indexed document.

## Candidate Classification

Classify candidates as `production`, `test`, or `unknown`.

Confirm `test` only from one of these signals, in priority order:

1. CCG test-node metadata;
2. explicit repository source-set or build rules;
3. established repository or language test path and filename conventions.

Fixtures, golden files, snapshots, testdata, and generated mocks are test
support only when those same signals confirm the role. A generic directory name
is not enough. Ambiguous candidates remain `unknown`.

For a non-test question, inspect production and unknown candidates first. If a
page contains only confirmed tests and has `next`, continue before reading the
tests. Tests may be used later as supporting evidence when production evidence
is insufficient.

## Declaration Evidence

When a result supplies a node ID, use `get_node` for exact declaration bounds.
Otherwise use the returned line, language-aware symbol navigation, or a
targeted locator search restricted to the returned paths. A locator grep should
not include broad context ranges.

Read the complete declaration and attached annotation or documentation. Read
multiple declarations as separate ranges rather than one large range containing
unrelated code. Preserve line numbers on the first read when citations may be
needed.

## Direct-Source Fallback

A source search that includes paths not returned by the current CCG page is a
fallback, even when the scope looks small.

For non-test questions, exclude confirmed test and test-support paths before
fallback matches are returned. Inspect production and unknown files first. The
initial fallback may search a task-relevant scope; every subsequent search must
narrow using a new concrete clue from the preceding result. Stop if the result
provides no new clue. Never turn a fallback miss into an absence or completeness
claim; route such claims to `ccg-search-verify`.
