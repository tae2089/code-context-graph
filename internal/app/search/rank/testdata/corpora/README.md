# External corpora

Three codebases nobody tuned this ranker against: `gorm`, `cobra`,
`context-diary`. Each directory is one frozen corpus — `queries.json` holds the
queries and the judgments, `candidates.json` and `intent_candidates.json` hold
the exact pools both indexes returned when the corpus was captured, and
`baseline.json` holds the score of the last accepted run.

They exist for one reason: a scoring constant fitted to this repository's own
vocabulary should show up as a loss somewhere else. That argument only works if
each corpus can actually tell a good ranking from a bad one.

## Why these corpora were changed

Until 2026-08-10 they could not. Deleting the structural ranker outright — using
full-text search's own order and nothing else — left all three scoring exactly
as before. A test suite that scores the same with the thing it measures removed
is not measuring it. `context-diary` was the clearest case: every one of its
nineteen queries put the first relevant file at rank 1, so there was no room for
a worse ranking to show up as a worse number.

Three things were going on:

1. **The pools were too narrow.** Most queries left one to six files standing
   after the evidence cut, and often the judged file was the only real
   candidate. Order cannot matter when there is nothing to order.
2. **Exact names never reach the ranker's judgment.** The search backend
   promotes an exact node-name match to the front of the candidate list before
   ranking runs, so `AutoMigrate`, `ExecuteC`, `SaveEntries` and friends land at
   rank 1 either way.
3. **Questions bypass ranking entirely.** Intent hits are appended after the
   name pool in intent order; the structural ranker never touches them. Eleven
   of `context-diary`'s queries are questions answered that way, which is why
   that corpus was completely insensitive.

So each corpus gained two queries built to depend on the order:

| corpus | query | first relevant file, ranked | …with ranking removed |
| --- | --- | --- | --- |
| gorm | `table name` | `schema/naming.go` (1) | 2 |
| gorm | `association` | `association.go` (1) | 2 |
| cobra | `valid args` | `args.go` (1) | 2 |
| cobra | `flag error` | `command.go` (1) | 2 |
| context-diary | `rescan commits` | `internal/gitlog/gitlog.go` (1) | 3 |
| context-diary | `explain` | `cmd/context-diary/explain.go` (1) | 2 |

Two per corpus, not one, so no single query carries the whole check.

`TestGolden_EveryCorpusFailsWithoutStructuralRanking` in
`golden_mutation_test.go` is what enforces this. It replays every corpus with
the rerank step replaced by a function that returns the pool untouched, and a
corpus that still passes its own baseline is reported as a failure.

## What "made harder" does and does not mean here

The queries were **selected** for wide candidate pools where several files
plausibly match, because that is the only situation in which ordering decides
anything. That selection bias is deliberate and is the design criterion for a
ranking corpus.

Each query's **judgment** was written by reading the two codebases, not by
reading the ranked output. Every `why` field says what the judged symbol does
and what the symbol full-text search prefers does instead, so the judgment can
be argued with on its own terms. Some examples:

- `cobra.Command.ValidateArgs` is not judged relevant to `valid args` because it
  never reads `ValidArgs` — it calls whatever validator the `Args` field holds.
- `migrator.TableType.Name` is not judged relevant to `table name` because it
  reports the name of a table that already exists, rather than deciding what
  table a model maps to.
- `clause.Association` is not judged relevant to `association` because it
  describes an operation inside a generated statement; Association Mode is the
  thing the word names.

The rule the whole golden set runs on still holds: **a judgment is never edited
to make a run pass.** If one of these calls is wrong, change it because the
reading is wrong, and take whatever score follows.

Note also that the corpora were not made to score *lower*. All six new queries
score rank 1 with ranking on. A perfect score was the symptom, not the disease;
the fix is that a worse ranking now produces a worse number.

## Known place where ranking scores worse than raw order

`cobra`'s existing `shell completion` query judges `completions.go`, the
shell-agnostic engine. Structural ranking puts it third, behind
`shell_completions.go` and `powershell_completions.go`; full-text order puts it
first. The baseline records rank 3, so the query is honest about it. It is left
as it is — it is a real measurement, and the ratchet will notice if it gets
worse.

## Fields

`queries.json` entries carry `discriminates: "ranking"` on the queries added for
the reason above. It is documentation only — the harness does not read it — and
is there so a maintainer pruning the set can see which entries are load-bearing
for `TestGolden_EveryCorpusFailsWithoutStructuralRanking`.

## Recapturing

```sh
# build one graph holding all three namespaces; --db-driver is passed explicitly
# because this repository's own .ccg.yaml points at postgres
ccg build $(go env GOMODCACHE)/gorm.io/gorm@v1.31.1 --exclude tests --exclude '*_test.go' \
  --db-driver sqlite --db-dsn /tmp/corpora.db --namespace gorm

# add only the queries that have no candidates yet; existing entries stay frozen
CGO_ENABLED=1 go test -tags fts5 ./internal/adapters/outbound/searchsql/ \
  -run TestCaptureMissingGoldenCandidates -capture-missing \
  -corpus gorm -graph /tmp/corpora.db -count=1 -v

# record the score of the accepted run
CGO_ENABLED=1 go test -tags fts5 ./internal/app/search/rank/ \
  -run TestGolden_RankingHasNotRegressed -update-golden -count=1 -v
```
