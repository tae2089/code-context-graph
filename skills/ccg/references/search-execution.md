# Search Execution Details

Use only the section required by the active branch of the `ccg` Core Loop.

## Structured Search Interfaces

Use MCP when repository instructions provide explicit MCP or server-visible
routing. Otherwise prefer JSON CLI when local `ccg` and a repository-local
`.ccg.yaml` are available, so the current binary and repository configuration
answer directly. Fast discovery needs declaration bounds, rank evidence,
truncation, and continuation arguments; storage IDs are not required. Use the
compact response on either surface. The CLI form is:

```bash
ccg search --json --compact --limit 5 "<query>"
```

For MCP, pass `compact: true` and execute the returned `next` call verbatim. For
CLI JSON, execute the command represented by `next.args` without calculating offsets or changing the
query, limit, or namespace. Plain CLI output is not a substitute because it can
hide structured continuation and declaration data.

For MCP, include the namespace supplied by repository instructions or
`.ccg.yaml` in the initial call. This is not a freshness or namespace-list
preflight: it is the routing key a shared server needs to select the repository.
Keep that namespace in every returned continuation. If the namespace must be
read from `.ccg.yaml`, extract only the `namespace:` field (for example with a
targeted `rg`) rather than opening the whole file; database configuration may
contain credentials and is irrelevant to read-only search routing.

## Query Language

When the source path is unknown, use structured CCG search even when an exact
identifier, literal, or error fragment is available. Those clues make excellent
compact CCG queries; they do not justify an unbounded source scan. Direct grep
may start only when its source path is already known.

Use one exact identifier, path, literal, or error fragment for a known thing.
When the symbol is unknown, ask a focused natural-language question that names
the component, behavior, and distinguishing operational context. The search
engine removes English function words and first tries a precise all-term match.
If that is empty, it uses OR retrieval to collect candidates sharing any
meaningful term, rejects single-term coincidences, and applies shared BM25/IDF
ranking. A candidate ranks higher when it matches more distinct query terms and
when those terms are rarer in the indexed corpus. This is lexical matching, not
semantic synonym expansion. Preserve the question's useful evidence instead of
manually compressing it into an AND-friendly keyword list, and use vocabulary
that actually appears in the repository.

Write natural-language terms in the dominant language of repository comments
and annotations. Translate the user's intent when needed, but preserve exact
identifiers, paths, literals, and error messages. Do not mix translations in one
query; one focused query in the repository's vocabulary is cheaper and easier
to verify than parallel synonyms.

If an exact-identifier query returns no files and exposes no continuation, use a
path-only source lookup for that identifier. Do not repeat a lexical index miss
through another interface to the same graph. For a natural-language miss, a
single structured JSON CLI retry is useful only when it is evidenceably a
different runtime or graph — for example, remote MCP versus a repository-local
`.ccg.yaml`. Treat that retry as a replacement result stream and do not page both
MCP and CLI results. When MCP already runs the local `ccg` binary against the
same configuration, go directly to current-source fallback.

## Sufficiency Before Paging

One returned file can contain several declaration hits. Verify the relevant hit
ranges on the current page before following `next`; do not treat only the first
declaration in the first file as the whole page. A positive explanation is
complete when ranked intent or domain-rule evidence identifies the design reason
and current production source confirms the behavior being described. Additional
wiring, retries, tests, or examples may be useful context, but they are not a
reason to page or grep after the requested explanation is already supported.
The amount of evidence depends on the claim. Begin with the highest-ranked
relevant production evidence, then inspect another declaration, file, or helper
only when a material part of the requested answer remains unsupported. Before
each extra lookup, state the evidence gap and choose the narrowest operation that
can close it. If no such gap exists, answer instead of collecting optional
detail.

Treat a declaration hit's `start_line` and `end_line` as its ready-to-read source
range. Read selected ranges directly; do not spend another grep call locating a
symbol whose bounds CCG already returned. Use an in-file locator only for a file
hit, missing bounds, or a newly referenced helper outside the returned range.

After CCG returns a usable page, ordinary source verification stays within paths
on that page and paths directly referenced by a declaration already verified
there. There is one hybrid handoff: when verified source reveals an exact
identifier or literal that names a remaining usage, caller, or exposure question,
locate its references with a path-only lookup such as `rg -l`. Return file names,
not repository-wide match context; exclude test-like paths unless they are the
subject, then read the relevant production declarations. Prefer this exact
reference lookup before semantic paging because it follows a source-proven clue
instead of asking the broad question again. A truncation flag only says more
candidates are available; follow `next` when a specific part of the user's
question remains unsupported and neither the current page nor an exact
source-derived clue can close it.

When a returned production file is clearly relevant but its declaration bounds
do not include one referenced helper, run a targeted grep for that exact symbol
or phrase inside the returned file and read its declaration. This remains page
verification. Follow `next` only when the current page failed to identify a
usable production path or symbol, or when current-source verification disproved
the candidates; paging is not a substitute for looking precisely inside a known
candidate.

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
