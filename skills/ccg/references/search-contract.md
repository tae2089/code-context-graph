# CCG Search Result Contract

Read this file completely before an absence or completeness claim, and whenever
`truncated`, `pool_truncated`, `annotation_coverage`, `weak_filtered`, or `next`
can change the answer.

## Mandatory Absence Gate

This gate overrides the general hybrid workflow when the task asks for absence
or completeness. Apply it before doing broad discovery or writing the
conclusion:

1. **Query budget:** start with one grouped grep/read pass over current source
   and one distinct CCG query in the appropriate shape. A prompt listing several
   examples does not authorize one query per example. Following the exact
   `next` paging call is not a new query.
2. Do not add the other shape, split the query into individual words, build a
   synonym grid, or enable `include_weak` merely because the first query missed.
   An additional distinct query requires a concrete vocabulary mismatch or
   ambiguous result that you can name before making the extra call. If you
   cannot name one, stop instead of expanding.
3. A confirmed absence requires verified graph freshness. An exhaustive grep
   cannot compensate for unverified graph freshness in a hybrid absence claim.
4. If freshness is unverified, the strongest allowed conclusion is: "not found
   within the checked evidence; absence is not confirmed because graph
   freshness was not verified."

The sentence in item 4 must be the conclusion, not a caveat after a stronger
claim. Never say the freshness gap "does not affect the conclusion." Before
returning, count distinct queries; if the budget was exceeded, report the
concrete expansion reason. Also verify that every read skill/resource is
reported by resolved absolute path, not basename.

## Evidence Shape

CLI `ccg search` and MCP `search` query the same index. MCP groups results as
`files[] {file_path, hit_count, hits[]}`. A hit reports its `node_id`, source
identity, recorded reason when one matched, and the evidence fields that
justified it.

`[name path intent]` means the query landed in the node name, a whole file-path
segment, or the node's own `@intent`. A candidate matching none of those is
withheld and counted as `weak_filtered`; it is not silently used as evidence.

Hits are grouped by file. A shown file is shown whole, so `limit` counts files,
not hits, and a page never splits one file. Across `namespaces: []`, the limit
and offset apply independently per namespace and every namespace with a hit is
represented.

Order comes from name similarity and path overlap. Recorded intent explains a
result; use graph queries or source reads to prove relationships.

## Paging and Candidate-Pool Signals

Read both signals before calling a search complete:

| `truncated` | `pool_truncated` | Meaning |
| --- | --- | --- |
| `false` | `false` | The complete answer is within the fetched pool. |
| `true` | either | More files answered; page on. |
| `false` | `true` | The fetched candidate pool ended before the answer; page on. |

One file can fill the candidate pool by itself, producing the last row even
when `truncated` is false. `limits` reports the file limit, current offset, and
hit budget.

Use the exact paging call returned in `next`. Never calculate or modify
`offset`: federated searches advance every namespace through its own list, so a
locally computed offset can skip one namespace and repeat another.

For an ordinary ranked answer, it is acceptable to stop after a credible hit is
verified in source; report that the result was truncated. Exhaust all pages only
when absence, completeness, or exhaustive inventory is part of the claim.

## Weak Candidates

Use `include_weak: true` only when `next` explicitly recommends it or the user
asks to inspect candidates withheld for weak evidence. Do not enable it merely
because `weak_filtered` is non-zero.

## Annotation Coverage

`annotation_coverage.with_reason` counts declarations carrying at least one
`@intent` or `@domainRule`; `declarations` is the indexed declaration count. It
counts declarations, not tags.

When `with_reason` is zero, an empty why-question says that nobody recorded a
reason in the index, not that the code is absent. `next` sometimes names a `skill` instead of a `tool`;
when it names `ccg-annotate`, use that skill if available: annotate the relevant
area, rebuild the graph, and ask the same question again. If no relevant area is
known, report that the suggestion is not actionable instead of inventing one.

## Empty Results

An empty response distinguishes no retrieved candidates, no justifiable
candidates, an offset past the end, and missing recorded reasons. Do not collapse
those states into "the code does not exist."

Even for an absence investigation, start with the one query shape appropriate
to the available clue. Do not fan out merely because the first query is empty.

Before an absence claim:

1. Verify relevant current source with grep/read.
2. Use the one CCG query shape appropriate to the clue.
3. Verify graph freshness against source changes.
4. Exhaust paging signals through the exact calls in `next`.
5. Explain any annotation-coverage gap or conditional tool not run.

If freshness was not verified, say "not found within the checked evidence;
absence is not confirmed because graph freshness was not verified." Never pair
an unqualified "does not exist" conclusion with a later freshness caveat.

Every hit's `node_id` can start `get_node`, `query_graph`,
`get_impact_radius`, or `trace_flow`. Traverse only when the answer makes a
relationship, flow, or impact claim.
