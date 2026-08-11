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

## The tuning rule

Do not tune constants against these numbers. Every constant that would raise
them — a score floor, a stopword list, a length penalty — is fitted to one
codebase's vocabulary, and a single corpus cannot tell an improvement from an
overfit. That is why `testdata/corpora/` holds two more frozen corpora,
captured from unrelated open-source codebases:

| Corpus | Source | Vocabulary |
| --- | --- | --- |
| `ccg` (primary, `testdata/` itself) | this repository | code analysis, graphs, annotations |
| `gorm` | `gorm.io/gorm v1.31.1` | ORM and database plumbing |
| `cobra` | `github.com/spf13/cobra v1.10.2` | CLI flags and completion |
| `context-diary` | `github.com/tae2089/context-diary @ 1054ca6` | git hooks, commit trailers, webhook bots |

The rule they enforce: **a change that moves a ranking number must hold or
improve it on every corpus.** A gain on one corpus paid for by a loss on
another is an overfit by definition, and the ratchet fails it. Changes that
are plain correctness fixes, or expressed in terms the runtime recomputes per
corpus, are the ones that pass everywhere.

`gorm` and `cobra` carry no ccg annotations, so their intent index is empty
and they score the identifier path alone — retrieval, rerank, and evidence
cut. They already earned their keep: the `levenshtein` query on cobra showed
that a docstring-retrieved hit dies at the evidence cut on any corpus without
`@intent` tags (recorded in `knownHiddenRelevant`, not tuned away).

`context-diary` closes the gap the other two leave: it carries 84
`@intent`/`@domainRule` lines written by its own author, so it is the first
corpus outside this repository that scores the question path — intent
retrieval, the `CanAnswer` gate, and reason-matched evidence. Until it was
added, every constant on that path was validated against one codebase's
vocabulary; a question-path change must now hold here too. Its questions were
written while reading the annotations, so they share vocabulary with the
reasons they target — treat its numbers as a floor for "the mechanism works",
not as evidence the phrasing gap is solved.

## Backend parity

The corpora do double duty. `TestBackendParity_SearchAnswersAreIdentical`
(in `internal/adapters/outbound/searchsql/`, `-tags "fts5 postgres"`, needs
`TEST_POSTGRES_DSN`) rebuilds each corpus from the frozen candidate pools,
seeds it into a SQLite FTS5 database and a PostgreSQL database through the
production document builders, replays every query through the full search
service on both, and requires the two answers to be identical — same files,
same order, same hits, same withheld counts.

The promise holds only where the candidate pool is complete. When more rows
match than `rank.FetchLimit` fetches, each backend keeps its own top slice,
ordered by bm25 or `ts_rank` — scores with no shared definition — so
membership itself becomes backend-specific. The test probes the pool per
query and skips truncated ones with a log line instead of asserting on them.
Everything the reranker can reach is asserted; what retrieval relevance alone
decides is documented here as backend-specific by design.

Two production pieces exist because of this test: migration
`000019_tokenize_identifier_separators` (PostgreSQL translates `/`, `.`, `_`
to spaces before `to_tsvector`, matching FTS5's unicode61 splitting) and the
identity tie-break in `rank.go` (structural ties order by file path and
qualified name, not by the backend's own retrieval rank).

There used to be a third path here. `wiki_search` named the Wiki web UI's search
box — `retrieval.FromDB`, full-text plus a namespace scan — and had its own
frozen pool and baseline. That pipeline was deleted along with
`internal/app/search/retrieval`; the Wiki `/api/retrieve` route now answers from
the unified search core, so scoring `search` scores it too. Entries in
`queries.json` that name `wiki_search` — the `label_format` note and two
`out_of_scope` lists — are dormant judgment data, kept because the judgments
themselves are still true and were made against that retired pipeline's own
scope rules.

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

Never widen a `relevant` list to make a run pass, and never narrow one either.
The list answers "what did the person want", which does not change because the
code did. Narrowing is the worse of the two: Recall is `found/relevant`, so
deleting an answer nobody found raises the score without a line of code
changing. `TestGolden_BaselineIsFullyGuarded` fails that now.

## What the guard adds

The ratchet compares this run against the baseline. `golden_guard_test.go` asks
a different question — whether the baseline is still being compared at all —
because a green ratchet says nothing about an entry it never reaches. There were
three holes.

**It walked the run, not the baseline.** Deleting a hard query from
`queries.json` left its baseline entry sitting there, unvisited and unmissed.
The guard walks the baseline and fails on any entry no query answers to.

**It recorded the answer key's size without comparing it.** `relevant` was
written into `baseline.json` on every update and read back never. The guard
fails when it shrinks.

**It could not fail an entry that already scores nothing.** The ratchet holds an
entry with two assertions — `found` may not drop, `rank` may not rise — and both
are dead at zero. Nineteen entries sat there: 16 on `ccg`, 2 on `cobra`, 1 on
`gorm`. Each now carries a class and a reason in `zeroScoreNotes`, and the guard
fails on one that has neither.

The two classes are not interchangeable, and the candidate pool is what
separates them.

| Class | Means | Test |
| --- | --- | --- |
| `out of scope` | a recorded decision not to answer | `queries.json` must list `search` in the query's `out_of_scope` |
| `known gap` | the search means to answer this and cannot yet | anything else |

`retrieved: true` forces `known gap`. If the pool already held the judged
answer, nothing was declined — the ranker left it off the page, and that is a
debt to work off. Four entries are in that state today, all on `ccg`, all
answered by the intent index and all missing from the page of ten files. The
other five `known gap` entries never got the answer out of retrieval, so no
reordering can pay them: `mcp`, for one, wants a package node the name index
does not carry.

An entry is a debt, not a permission. The guard also fails when a listed entry
starts scoring, so the list cannot rot into a silent excuse — the same rule
`knownHiddenRelevant` runs under.

One limit, stated plainly: `-update-golden` rewrites `relevant` from the run, so
re-recording after narrowing a judgment still passes. What the guard buys is
that the narrowing cannot be silent — it has to arrive as a `baseline.json` diff
a reviewer reads.

## Rebuilding the candidate fixture

Only when candidate retrieval itself changes — the tokenizer, `SanitizeFTS5`,
`promoteExactNameMatch`, the indexed document content. It needs a graph at the
repository root, which is build output and not tracked:

```sh
make wiki-db              # builds ./ccg.db, which the capture reads
make search-eval-capture  # rewrites candidates.json and intent_candidates.json
```

An external corpus is recaptured the same way, from a graph built out of the
Go module cache (the version is pinned by go.mod, so the sources are
reproducible):

```sh
ccg --db-dsn /tmp/corpora.db --namespace gorm migrate
ccg --db-dsn /tmp/corpora.db --namespace gorm \
  build "$(go env GOMODCACHE)/gorm.io/gorm@v1.31.1" --exclude tests --exclude '*_test.go'
go test -tags fts5 ./internal/adapters/outbound/searchsql/ \
  -run TestCaptureGoldenCandidates -capture-golden \
  -corpus gorm -graph /tmp/corpora.db -count=1
```

(cobra: namespace `cobra`, `github.com/spf13/cobra@v1.10.2`, excludes
`*_test.go` and `site`.)

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
bucket           n  retrieved  Recall@10   top1  top3   MRR
ALL             86   74/86      0.738 (124/168)  50    63  0.661
ANSWERABLE      78   73/78      0.831 (123/148)  49    62  0.716
```

That block is `make search-eval`'s own output for the `ccg` corpus, copied
whole. Rewriting a number here by hand is how it drifted from the harness once
already.

`queries.json` holds 91 entries and the scoreboard counts 86. The missing five
are the negative cases — queries whose `relevant` and `relevant_files` lists are
empty; a query with no right answer has no rank to average, so the report skips
them and lists any that return noise separately. Nothing is silently dropped.

`ALL` includes the out-of-scope queries, so it can never reach 1.0 however good
the code gets. `ANSWERABLE` drops the eight queries `search` declines (`cfg`,
the three typos, and the four Korean questions), and is the number to read when
asking how the code is doing.

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

## The declined queries

Eight queries on `ccg` are declined, and they record two decisions. Seven of
them score nothing and are kept red on purpose; the eighth is declined and
answers anyway, which is explained at the end.

**Spelling and abbreviations.** `cfg` and the three typos — `sanitze`,
`retreival`, `anotation`. They are neither a retrieval finding nor a ranking
one: search does not correct spelling and does not expand abbreviations. The
tool is driven by an agent quoting identifiers out of code it has already read,
so a query that matches nothing exactly is naming something that does not
exist. Answering it approximately would turn "no such thing" into a confident
wrong answer.

**Language.** The four Korean questions in the `korean` bucket. Search answers
questions written in English. Answering a Korean one means teaching the query
pipeline Korean morphology — splitting the particle off `파일을` so it can reach
`파일`, which scores 22 in this corpus where `파일을` scores 3 — and a Korean
function-word list mirroring the English one in `queryterm`. Neither
generalises: doing it for Korean commits the project to doing it for the next
language somebody types in, and `gorm`, `cobra` and `context-diary` hold no
Hangul, so any such change could only ever be measured against the four `ccg`
queries it was written against. That is the overfit the tuning rule above
forbids. Three of the four return nothing — under half their terms reach a
recorded reason, so `intent.Result.CanAnswer` drops every hit — and the fourth
answers at rank 1. All four are declined together: marking only the failures
out of scope would be fitting the rule to the results.

A declined query is kept in the set, and kept red where it scores nothing, so
each decision stays visible and anyone who reverses it inherits the
measurements in each `why`.

These eight, plus `Excute` on `cobra` and `Preloda` on `gorm`, are the whole of
the recorded declining. Ten queries, nine `zeroScoreNotes` entries: the fourth
Korean question is declined and still scores, so it has no zero to explain and
the guard would call a note on it stale. `out_of_scope` in `queries.json` is
where a decision is recorded; `zeroScoreNotes` only explains the zeros. Every
other entry that scores nothing is a `known gap` — nobody decided against
answering it.

## What this measures, and what it does not

It measures **regression**, not quality. The same author wrote the code and the
judgments, so a good score here is not evidence the search is good — only a drop
is evidence that something broke.

It is also blind to score changes that move every candidate equally. Disabling
the acronym word-boundary rule, for example, lowers `HTTPServer`'s score without
changing who outranks whom on any of these queries, so this set stays green while
the unit tests in `rank_test.go` fail. The two are complementary: unit tests pin
the scorer's behaviour, this set pins the resulting order.
