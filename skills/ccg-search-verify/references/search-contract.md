# CCG Search Result Contract

Read this file completely before an absence or completeness claim, and whenever
`truncated`, `pool_truncated`, `annotation_coverage`, `weak_filtered`, or `next`
can change the answer.

## Mandatory Absence Algorithm

This algorithm overrides the ordinary fast search workflow for absence or
completeness:

1. **Source pass:** combine the current task's exact clues into one alternation
   and run one recursive grep/read pass. Derive the pattern from the task; never
   reuse example vocabulary from this skill.
2. **CCG pass:** run exactly one distinct query at `limit: 5`:
   - Known term: choose one single rare domain anchor. Do not concatenate the
     prompt's examples because CCG requires every query word in one document.
   - Unknown symbol: ask one concise intent question.
   Do not switch shape, split terms, retry synonyms, or enable `include_weak`
   after a miss. Do not fan out. Only the exact call in `next` may extend this
   pass.
3. **Freshness:** set `graph_current=true` only when evidence proves the graph
   represents relevant current source. Clean git state proves nothing; known
   stale state means false. A freshness tool that accepts a path must receive a
   verified server-visible path. If that path is unknown, skip the call and set
   `graph_current=false` instead of trying the client-local path.
4. **Paging:** follow exact `next` calls until both truncation flags are false.

```text
absence_allowed = source_checked AND graph_current AND paging_complete
```

When false, conclude: "not found within the checked evidence; absence is not
confirmed because graph freshness or paging was not verified." Never say
"does not exist," "absence confirmed," or that the gap does not affect the
conclusion. Report every read skill/resource by resolved absolute path; a
basename-only report is incomplete. An exhaustive grep cannot compensate for
unverified graph freshness.

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

Exhaust all pages because absence, completeness, or exhaustive inventory is
part of the claim.

## Weak Candidates

Use `include_weak: true` only when `next` explicitly recommends it or the user
asks to inspect candidates withheld for weak evidence. Do not enable it merely
because `weak_filtered` is non-zero.

## Annotation Coverage

`annotation_coverage.with_reason` counts declarations carrying at least one
`@intent` or `@domainRule`; `declarations` is the indexed declaration count. It
counts declarations, not tags.

When `with_reason` is zero, an empty why-question says that nobody recorded a
reason in the index, not that the code is absent. `next` sometimes names a
`skill` instead of a `tool`; when it names `ccg-annotate`, report that suggestion
and its coverage gap. Do not author annotations or rebuild the graph unless the
user separately authorizes the relevant workflow. If no relevant area is known,
report that the suggestion is not actionable instead of inventing one.

## Empty Results

An empty response distinguishes no retrieved candidates, no justifiable
candidates, an offset past the end, and missing recorded reasons. Do not
collapse those states into "the code does not exist." Apply the mandatory
algorithm without fallback fan-out.

Every hit's `node_id` can start `get_node` or one bounded `query_graph` lookup
when a direct relationship fact is necessary. Do not invoke
`get_impact_radius` or `trace_flow`; those belong to the explicit-only
`ccg-analyze` skill.
