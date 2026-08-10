# Search golden set

A frozen record of what `search` returns for real queries, so a later change has
to justify every result it moves.

`golden_test.go` replays the whole production pipeline — `searchapp.Service`
over both frozen index answers, including the intent merge and the evidence cut
— and scores what each query's shown list contains.

The 47 questions of the intent golden set were merged into `queries.json` when
search absorbed `find_by_intent`'s answers. Their judgments were made at file
granularity ("somewhere the reader can start walking"), so they live in
`relevant_files` rather than being re-judged down to declarations. The retired
harness — a corpus-freezing eval in `internal/adapters/outbound/searchsql/` —
was deleted along with the tool; this set is now the only measurement of
question-shaped queries.

A word on tuning against these numbers, inherited from that harness: every
constant that would raise them — a score floor, a stopword list, a length
penalty — would be fitted to one codebase's vocabulary, and there is no way to
tell an improvement from an overfit while the only measurement comes from that
same codebase. A second golden set on an unrelated repository is what tells
them apart; until one exists, prefer changes that are either plain correctness
fixes or expressed in terms the runtime recomputes per corpus.

There used to be a third path here. `wiki_search` named the Wiki web UI's search
box — `retrieval.FromDB`, full-text plus a namespace scan — and had its own
frozen pool and baseline. That pipeline was deleted along with
`internal/app/search/retrieval`; the Wiki `/api/retrieve` route answers 501
until the unified search core takes it over. Entries in `queries.json` that name
`wiki_search` — the `label_format` note and two `out_of_scope` lists — are
dormant judgment data, kept because the judgments themselves are still true and
the unified service will be scored against them.

## What each file is

| File | Written by | Purpose |
| --- | --- | --- |
| `queries.json` | a human | 91 queries with the answers a developer typing them would accept, and why |
| `candidates.json` | `TestCaptureGoldenCandidates` | the full-text candidates for `search`, in retrieval order, at `rank.FetchLimit(10)` |
| `intent_candidates.json` | `TestCaptureGoldenCandidates` | what the intent index said per query: ranked hits, every scored term with its reason count, and the corpus size |
| `baseline.json` | `-update-golden` | where `search` put the first relevant node on the last accepted run |

`intent_candidates.json` keeps the term counts, not only the hits, because
membership is gated on them: `intent.Result.CanAnswer` drops every intent hit
when fewer than half the question's scored terms appear in any recorded reason.
A replay without the terms would score a search that thinks every question is
answerable.

The fixture is captured through the production query path, so the tool is scored
on exactly the pool it gets in production. Once captured it is never re-read
from a database, which is what makes a metric change attributable to the code
and nothing else.

## Running it

```sh
make search-eval   # the scoreboard, asserts nothing
make test          # includes the ratchet, which is what fails a build
```

There is no `ccg eval` command and there should not be one. Measuring search is a
development concern, so it stays in the test suite rather than shipping in the
binary.

## When the ratchet fails

It reports one line per query that got worse. For each one, open its entry in
`queries.json` and read the `why`.

- **The judgment is right and the ranking is wrong** — fix the ranking.
- **The ranking is right and the judgment was wrong** — change the judgment and
  say so in the commit. Do not delete the query.
- **The new order is a deliberate trade** — record it with `-update-golden` and
  name the trade in the commit message.

Never widen a `relevant` list to make a run pass. The list answers "what did the
person want", which does not change because the code did.

## Rebuilding the candidate fixture

Only when candidate retrieval itself changes — the tokenizer, `SanitizeFTS5`,
`promoteExactNameMatch`, the indexed document content. It needs a graph at the
repository root, which is build output and not tracked:

```sh
make wiki-db              # builds ./ccg.db, which the capture reads
make search-eval-capture  # rewrites candidates.json and intent_candidates.json
```

A recapture can hide a retrieval regression by baking it into the fixture, so
diff the file and re-read every judgment it touches before committing.

## Labels

`relevant` entries are written `kind:pkg.Symbol@path/to/file.go` and judge at
node granularity. `relevant_files` entries are bare file paths and judge at
file granularity; a judged file counts once however many hits it answered with.
A query may use either or both.

`out_of_scope` is a list of the tools that decline the query, not a boolean, so
one judgment file can score more than one tool. Today only `search` is scored
from it; the lists that also name `wiki_search` are dormant, as above.

`retrieved` is kept apart from `rank` on purpose. False means the candidate pool
never held a relevant answer, so the ranking was never given the chance and no
ranking change can fix that query.

## The two totals

```
search       ALL         86   74/86  0.732 (123/168)  top1 49  top3 62  MRR 0.650
search       ANSWERABLE  82   74/82  0.804 (123/153)  top1 49  top3 62  MRR 0.681
```

`queries.json` holds 91 entries and the scoreboard counts 86. The missing five
are the negative cases — queries whose `relevant` and `relevant_files` lists are
empty; a query with no right answer has no rank to average, so the report skips
them and lists any that return noise separately. Nothing is silently dropped.

`ALL` includes the out-of-scope queries, so it can never reach 1.0 however good
the code gets. `ANSWERABLE` drops the four queries `search` declines (`cfg` and
the three typos), and is the number to read when asking how the code is doing.

These totals are not comparable to what this file printed before the intent
questions were merged in: 47 harder, file-judged questions joined the average.
On the pre-merge queries alone the numbers did not move, and on the migrated
questions parity with the retired intent harness held exactly within the
10-file page (top1 17, top3 27, MRR 0.524 against 0.526) — the three hits it
lost sat at old ranks 11–16, past the page search shows.

## Negative cases

A negative query's right answer is nothing. Three of the five still return
results, because their words are ordinary codebase vocabulary and the reasons
"speak their language" without answering them — the same debt the retired
intent golden set carried. The baseline records each negative's measured count
rather than asserting zero: growth fails the ratchet, and a negative that
reaches zero must be re-recorded so the improvement cannot silently regress.
`intent.Result.CanAnswer` is what keeps the other two at zero — a question
mostly made of words nobody ever wrote down gets no intent hits at all.

## The four kept-red queries

`cfg` and the three typos — `sanitze`, `retreival`, `anotation` — are declined.
They are neither a retrieval finding nor a ranking one. They record a decision:
search does not correct spelling and does not expand abbreviations. The tool is
driven by an agent quoting identifiers out of code it has already read, so a
query that matches nothing exactly is naming something that does not exist.
Answering it approximately would turn "no such thing" into a confident wrong
answer. They are kept, and kept red, so the decision stays visible and anyone
who reverses it inherits the measurements in each `why`.

## What this measures, and what it does not

It measures **regression**, not quality. The same author wrote the code and the
judgments, so a good score here is not evidence the search is good — only a drop
is evidence that something broke.

It is also blind to score changes that move every candidate equally. Disabling
the acronym word-boundary rule, for example, lowers `HTTPServer`'s score without
changing who outranks whom on any of these queries, so this set stays green while
the unit tests in `rank_test.go` fail. The two are complementary: unit tests pin
the scorer's behaviour, this set pins the resulting order.
