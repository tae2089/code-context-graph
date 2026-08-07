# Ranking golden set

A frozen record of how `rank.Rerank` orders real search candidates, so a later
change to the ranking has to justify every result it moves.

## What each file is

| File | Written by | Purpose |
| --- | --- | --- |
| `queries.json` | a human | 34 queries with the nodes a developer typing them would accept, and why |
| `candidates.json` | `TestCaptureGoldenCandidates` | the full-text candidate list each query produced, in retrieval order |
| `baseline.json` | `-update-golden` | where the first relevant node landed on the last accepted run |

`candidates.json` is captured through the production query path
(`SQLiteBackend.Query` with `rank.FetchLimit(10)`), so the ranker is scored on
exactly the pool it gets in production. Once captured it is never re-read from a
database, which is what makes a metric change attributable to the ranking code
and nothing else.

## Running it

```sh
# scoreboard, asserts nothing
CGO_ENABLED=1 go test -tags fts5 ./internal/app/search/rank/ -run TestGolden_Report -v

# the ratchet, part of the normal suite
CGO_ENABLED=1 go test -tags fts5 ./internal/app/search/rank/ -count=1
```

## When the ratchet fails

It reports one line per query that got worse. For each one, open its entry in
`queries.json` and read the `why`.

- **The judgment is right and the ranking is wrong** — fix the ranking.
- **The ranking is right and the judgment was wrong** — change the judgment and
  say so in the commit. Do not delete the query.
- **The new order is a deliberate trade** — record it with
  `-update-golden` and name the trade in the commit message.

Never widen a `relevant` list to make a run pass. The list answers "what did the
person want", which does not change because the code did.

## Rebuilding the candidate fixture

Only when retrieval itself changes — the tokenizer, `SanitizeFTS5`,
`promoteExactNameMatch`, the indexed document content. It needs a graph at the
repository root, which is build output and not tracked:

```sh
./ccg build .
CGO_ENABLED=1 go test -tags fts5 ./internal/adapters/outbound/searchsql/ \
  -run TestCaptureGoldenCandidates -capture-golden -count=1
```

A recapture can hide a retrieval regression by baking it into the fixture, so
diff `candidates.json` and re-read every judgment it touches before committing.

## What this measures, and what it does not

It measures **regression**, not quality. The same author wrote the ranker and
the judgments, so a good score here is not evidence the ranking is good — only
a drop is evidence that something broke.

It is also blind to score changes that move every candidate equally. Disabling
the acronym word-boundary rule, for example, lowers `HTTPServer`'s score without
changing who outranks whom on any of these 34 queries, so this set stays green
while the unit tests in `rank_test.go` fail. The two are complementary: unit
tests pin the scorer's behaviour, this set pins the resulting order.

`retrieved` and `rank` are kept apart on purpose. `retrieved: false` means
full-text search never returned a relevant node, so the ranker was never given
the chance and no ranking change can fix that query. Four queries sit there
today — `cfg` and the three typos — and they are a retrieval finding, not a
ranking one.
