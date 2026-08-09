# Search golden set

A frozen record of what the two search paths return for real queries, so a later
change has to justify every result it moves.

There are two harnesses over one set of queries:

| Harness | What it replays | What a result is |
| --- | --- | --- |
| `golden_test.go` | the MCP `search` tool — `rank.Rerank` over full-text candidates | a **node** |
| `golden_docs_test.go` | `wiki_search` — the Wiki web UI's search box, `retrieval.FromDB`, full-text plus the namespace scan | a **file** |

A third harness scores `find_by_intent` and lives somewhere else, because it
needs a live index rather than a frozen result list — see [The intent golden
set](#the-intent-golden-set) at the bottom.

`wiki_search` is a name for the second path, not a tool anyone can call. The MCP
`search_docs` tool that used to sit on top of it was removed; `retrieval.FromDB`
stayed because the Wiki server still serves it at
`internal/adapters/inbound/wikihttp/server.go`.

They exist as a pair because the two paths have different jobs. `search` needs
every term of the query inside one indexed node, so it answers "name the symbol"
queries. `wiki_search` also scans namespace text and ORs its terms, so it answers
questions written as sentences. Scoring only one of them made the other's
behaviour invisible.

## What each file is

| File | Written by | Purpose |
| --- | --- | --- |
| `queries.json` | a human | 44 queries with the answers a developer typing them would accept, and why |
| `candidates.json` | `TestCaptureGoldenCandidates` | the full-text candidates for `search`, in retrieval order, at `rank.FetchLimit(10)` = 50 |
| `docs_candidates.json` | `TestCaptureDocsGoldenCandidates` | the scan pool plus the full-text candidates for `wiki_search`, at `retrieval.DBCandidateLimit(10)` = 100 |
| `baseline.json` | `-update-golden` | where `search` put the first relevant node on the last accepted run |
| `baseline_docs.json` | `-update-docs-golden` | where `wiki_search` put the first relevant file, and how many files it returned |

Both fixtures are captured through the production query path, so each tool is
scored on exactly the pool it gets in production. Once captured they are never
re-read from a database, which is what makes a metric change attributable to the
code and nothing else.

`docs_candidates.json` is a separate file rather than a reuse of
`candidates.json` because the two limits differ. Five queries — `treesitter
semantics`, `package discovery`, `mcp`, `discovery`, `annot` — fill
`candidates.json` to its ceiling of 50, so replaying `wiki_search` from it would
score the tool on a truncated pool.

## Running it

```sh
make search-eval   # both scoreboards, asserts nothing
make test          # includes both ratchets, which is what fails a build
```

There is no `ccg eval` command and there should not be one. Measuring search is a
development concern, so it stays in the test suite rather than shipping in the
binary.

## When a ratchet fails

It reports one line per query that got worse. For each one, open its entry in
`queries.json` and read the `why`.

- **The judgment is right and the ranking is wrong** — fix the ranking.
- **The ranking is right and the judgment was wrong** — change the judgment and
  say so in the commit. Do not delete the query.
- **The new order is a deliberate trade** — record it with `-update-golden` or
  `-update-docs-golden` and name the trade in the commit message.

Never widen a `relevant` list to make a run pass. The list answers "what did the
person want", which does not change because the code did.

The `wiki_search` ratchet asserts the negative query's result count did not
**grow**, rather than asserting it is zero. It is zero today, and the assertion
is written the looser way on purpose: the scan matches on substrings, so a
future recapture or a new annotation can put a file back on that page, and the
useful thing to catch is the direction, not the exact number.

## Rebuilding the candidate fixtures

Only when retrieval itself changes — the tokenizer, `SanitizeFTS5`,
`promoteExactNameMatch`, the scan's kind filter, the indexed document content. It
needs a graph at the repository root, which is build output and not tracked:

```sh
make wiki-db              # builds ./ccg.db, which both captures read
make search-eval-capture  # rewrites candidates.json and docs_candidates.json
```

A recapture can hide a retrieval regression by baking it into the fixture, so
diff both files and re-read every judgment they touch before committing.

## Labels

`relevant` entries are written `path/to/file.go@pkg.Symbol`. `search` is scored
on the symbol after the `@`; `wiki_search` is scored on the path before it.

`out_of_scope` is a list of the tools that decline the query, not a boolean. A
query can be answerable by one tool and out of scope for the other:

```json
"out_of_scope": ["search"]            // the five question queries
"out_of_scope": ["search", "wiki_search"]  // cfg and the three typos
```

`retrieved` (in `search`) and `reachable` (in `wiki_search`) are kept apart from
`rank` on purpose. False means the candidate pool never held a relevant answer,
so the ranking was never given the chance and no ranking change can fix that
query.

## The two totals

```
search       ALL  43   34/43  0.745  top1 32  top3 34  MRR 0.767
search       ANSWERABLE  34   33/34  0.900  top1 31  top3 33  MRR 0.941

wiki_search  ALL  43   39/43  0.778  top1 30  top3 37  MRR 0.780
wiki_search  ANSWERABLE  39   38/39  0.917  top1 30  top3 36  MRR 0.847
```

`queries.json` holds 44 entries and both scoreboards count 43. The missing one is
`zzz nonexistent symbol qqq`, whose `relevant` list is empty; a query with no
right answer has no rank to average, so the report skips it and lists it
separately. Nothing is silently dropped.

`ALL` includes the out-of-scope queries, so it can never reach 1.0 however good
the code gets. `ANSWERABLE` drops the ones that tool declines, and is the number
to read when asking how the code is doing. The two columns differ because the
declined sets differ: `search` declines nine, `wiki_search` four.

`search`'s 0.941 is not comparable to the number this file used to print. Until
the five question queries were reclassified, they were scored against `search`
and dragged it down. Moving them is bookkeeping, not a code improvement — with
the same judgments as before, `search` scores 0.846, unchanged by any code in
this round.

## What else came back

Every number above answers "where did the right answer land". None of them
answers "how much else came with it", which is what every complaint about this
tool has actually been about. `TestGoldenDocs_Report` prints a second table for
that:

```
bucket           n  returned  found  unvouched  precision  ceiling
question         5     50       4      46        0.080     0.100
ALL             43    288      56     232        0.194     0.219
ANSWERABLE      39    285      55     230        0.193     0.211
```

`precision` is `found / returned`. Read its **movement**, never its value: the
`relevant` lists name the files a developer would accept, not every file that
could reasonably appear, so an unvouched file is not proven junk.

`ceiling` is what precision would be with a perfect ranking and the same page
length. It is the honest reading of this table, and it says something the older
metrics could not:

**precision 0.194, ceiling 0.219 — the gap is 0.025.** Ordering is close to
exhausted. Of the 232 unvouched files, a perfect ranker removes 7. The rest are
there because the tool fills the page to `limit` whenever it can, and 21 of the
43 queries come back with exactly 10 files. The noise is page length, not order.

That is the number to judge the next change by. A change that returns fewer
files while `found` holds is a win this table shows immediately; a change that
only reorders cannot move it far enough to matter.

`TestGoldenDocs_HasNotRegressed` enforces the same thing per query: if
`returned` grows while `found` does not, the run fails. Until now only the
negative query was watched for growth, which is why two rounds of noise work
could land without any number moving.

## The four kept-red queries

`cfg` and the three typos — `sanitze`, `retreival`, `anotation` — are declined by
both tools. They are neither a retrieval finding nor a ranking one. They record a
decision: search does not correct spelling and does not expand abbreviations.
Both tools are driven by an agent quoting identifiers out of code it has already
read, so a query that matches nothing exactly is naming something that does not
exist. Answering it approximately would turn "no such thing" into a confident
wrong answer. They are kept, and kept red, so the decision stays visible and
anyone who reverses it inherits the measurements in each `why`.

## The eyeball record

Metrics say where the right answer landed. They say nothing about what sat above
it. This is a one-time manual read of the top ten files `wiki_search` returned
for the five question queries, taken before and after function words stopped
counting as matched terms.

**Before.** The scan ORs its terms and the score adds 10 per matched term, so
words like `how`, `do`, `the`, `a`, and `i` bought real points:

- `how does the graph get built` — `handler_query.go` first on
  `terms=[get graph how the the_graph]`; `build.go` sixth.
- `where do search results get ranked` — all ten files carried `do`, and
  `rank.go` was not among them.
- `how do i follow a call chain` — `flow.go` first, but on
  `terms=[a a_call call call_chain chain do follow how i]`.
- `zzz nonexistent symbol qqq` — ten files, all on the substring `symbol`.

**After** `MatchedTerms` drops function words before building adjacency aliases
(`internal/app/search/retrieval/helpers.go`):

- `how does the graph get built` — `terms=[get graph]`, `build.go` second.
- `what happens when a webhook arrives` — `terms=[webhook]`, `handler.go` first.
- `how do i follow a call chain` — `terms=[call call_chain chain follow follow_call]`,
  `flow.go` second behind `semantics_js_ts.go`, which matches `call chain` on its
  own surface.
- `zzz nonexistent symbol qqq` — still ten files, all on `symbol`.

The headline is a wash, and that is the honest reading: question Recall@10 stayed
0.800, question MRR moved 0.633 to 0.600 (ranks `[6,1,0,1,1]` to `[2,1,0,2,1]`),
top1 3 to 2, top3 3 to 4, ANSWERABLE MRR 0.851 to 0.847. One big win, one loss,
and much less noise in what the caller reads.

## Saying "nothing here"

The eyeball above left the negative query untouched: ten files, all on the single
word `symbol`. Cleaning up which words count could not fix that, because the
scan keeps a node that matched **one** term, so one common word carries a whole
page on its own.

The fix is `mostlyNamesNothing` in `retrieval/service.go`: if more than half the
words the caller wrote appear nowhere in the namespace, the scan contributes
nothing. Full-text results are untouched, so this can only remove scan-only
noise.

The rule counts dead words rather than demanding each file match more of the
query, and the measurement is why. Counting per file kills real answers:
`what happens when a webhook arrives` is answered by `webhook/handler.go`, which
matches one word of three, because `happens` and `arrives` are prose that no
identifier here spells. Counting dead words separates the two cases with room to
spare — how often each word of a question appears in this namespace:

| query | words, and how many of the 158 files hold each |
| --- | --- |
| how does the graph get built | graph 91, get 55, built 15 — **0 dead** |
| what happens when a webhook arrives | webhook 16, happens 3, arrives 0 — **1 of 3 dead** |
| where do search results get ranked | get 55, search 39, results 29, ranked 4 — **0 dead** |
| how do i follow a call chain | call 67, follow 8, chain 5 — **0 dead** |
| what stops the server accepting new work | new 61, work 40, server 25, stops 3, accepting 1 — **0 dead** |
| zzz nonexistent symbol qqq | symbol 27, zzz 0, nonexistent 0, qqq 0 — **3 of 4 dead** |

Only the invented query has more than one dead word. Note also that a share-based
rule would not have worked: `symbol` covers 17% of files and `webhook` 10%, which
is too close to draw a line between.

Counting alone was not enough, and the second half of the rule is what keeps it
from refusing real questions. Run against thirty plausible questions about this
codebase, counting alone refused one: **`why is the queue draining slowly`**,
where `draining` and `slowly` are dead and only `queue` survives. That question
has an answer. Denying a real answer is worse for an agent than handing it a
noisy page, so one refusal in thirty was not acceptable.

The valve is the file path. `wiki_search` returns files, so a surviving word that
appears in a path is pointing at a result, and the query goes through. The two
cases separate cleanly on exactly this:

| word | files it appears in | in a **path** | in a **name** | only in annotation prose |
| --- | --- | --- | --- | --- |
| `queue` | 5 | 31 nodes | 8 | 6 |
| `symbol` | 27 | **0** | 9 | 62 |

Matching a node *name* is not enough — `symbol` matches nine names and a
name-based valve reopens the hole. Path only.

With the valve: `why is the queue draining slowly` is answered, all thirty prose
questions are answered, and `zzz nonexistent symbol qqq` still returns nothing.

Result: the negative query went from 10 files to 0, and **no other number moved**
— every bucket, both totals, and every per-query rank are identical.

`prose_test.go` keeps the thirty questions as a standing check, because this is
the failure mode that hides: an empty answer looks the same whether the thing is
missing or the rule misjudged. The golden set's five question-shaped queries were
too few to catch it — counting alone passed the whole golden set while refusing a
real question.

Queries under two words are exempt: one word either exists or it does not, and
the per-node filter already decides that.

## What this measures, and what it does not

It measures **regression**, not quality. The same author wrote the code and the
judgments, so a good score here is not evidence the search is good — only a drop
is evidence that something broke.

It is also blind to score changes that move every candidate equally. Disabling
the acronym word-boundary rule, for example, lowers `HTTPServer`'s score without
changing who outranks whom on any of these queries, so this set stays green while
the unit tests in `rank_test.go` fail. The two are complementary: unit tests pin
the scorer's behaviour, this set pins the resulting order.

## The intent golden set

`find_by_intent` answers a question written as a sentence — "what stops two
pushes for the same repository from building at the same time" — from recorded
`@intent` and `@domainRule` text only. It is scored by a third harness, and its
files live beside it in `internal/adapters/outbound/searchsql/testdata/`, not
here.

### Why it freezes something different

The two harnesses above freeze the **result** of a query, because what is under
test runs after retrieval: `rank.Rerank` reorders a candidate pool, so freezing
the pool leaves the reordering free to move.

`find_by_intent` has no reranker. The order comes straight out of the FTS index,
so freezing the query output would freeze the only thing being measured. The
intent harness freezes the **corpus** instead — every annotated declaration and
its tags — then rebuilds a real `intent_fts` index from it in memory and asks the
real question through the real service. Nothing below `intent.Service.Find` is
stubbed: the sanitizer, the bm25 order, the file grouping, and the coverage count
all run.

| File | Written by | Purpose |
| --- | --- | --- |
| `intent_questions.json` | a human | 47 questions somebody would ask during an incident, the files that count as a place to start, and why |
| `intent_corpus.json` | `TestCaptureIntentCorpus` | all 1880 searchable declarations, their `@intent`/`@domainRule` tags, and their edge counts |
| `intent_baseline.json` | `-update-intent` | where the first acceptable file landed on the last accepted SQLite run |
| `intent_baseline_postgres.json` | `-update-intent` with `-tags "fts5,postgres"` | the same, measured on PostgreSQL |

### What counts as success

Not "the right file". A question is scored on whether the answer contains a file
the reader could **start walking the graph from**, because that is what the tool
is for: during an incident you need an entry point, not a verdict. So a judgment
lists every file that qualifies, and any one of them at any position is a hit —
rank is kept only as a regression guard.

Three numbers come out of it:

- **hit** — the share of questions whose answer contained an acceptable file.
- **MRR / top1 / top3** — where that file landed among the files shown.
- **dead-end rate** — the share of returned declarations with no edge in either
  direction. A topically correct file the reader cannot walk from has not
  delivered an entry point. On this corpus the rate is structurally 0: only 4 of
  1880 declarations have no edge at all and none of them carry a reason, so the
  metric currently finds nothing. It stays as a guard on edge resolution, which
  is the thing that would make it fire.

The report also prints coverage — 1751 of 1880 searchable declarations carry a
recorded reason — because an empty answer means something different at 93% than
it would at 40%.

### Running it

```
make intent-eval                 # print the SQLite scoreboard
make intent-eval-postgres        # the same on PostgreSQL (needs a throwaway server)
make intent-eval-capture         # recapture the corpus from ./ccg.db and re-record
```

`make intent-eval-capture` runs both steps on purpose. A corpus captured from a
newer graph without a re-recorded baseline fails the ratchet for reasons that
have nothing to do with the code.

`make intent-eval-postgres` **drops and recreates the `public` schema** of
whatever `TEST_POSTGRES_DSN` names, defaulting to `localhost:5432/ccg_test`.
Point it at a database you are willing to lose. Without a server the PostgreSQL
tests skip, so `make test` still passes on a laptop with nothing running.

### Both backends, because they used not to rank alike

SQLite ordered by FTS5's `rank`, which is bm25: a word written in many recorded
reasons counts for less. PostgreSQL ordered by `ts_rank`, which reads one
document at a time and never learns that a word is common. Same index contents,
same question, different answer — and the golden score was being measured on
SQLite while the deployed server runs PostgreSQL, so the number being tracked
described a system nobody used.

Measured on the sixteen questions the set held at the time. Two of these three
configurations no longer exist, so the table cannot be re-measured on the set as
it stands now.

| on the same corpus and questions | answered | hit | top1 | top3 | MRR |
| --- | --- | --- | --- | --- | --- |
| SQLite, FTS5 bm25 | 16/16 | 16/16 | 10 | 13 | 0.740 |
| PostgreSQL, `ts_rank` | 16/16 | 15/16 | 7 | 12 | 0.603 |
| either backend, scored in Go | 16/16 | 16/16 | 10 | 13 | 0.740 |

The question PostgreSQL used to lose entirely was "what decides which
repositories and branches are allowed to sync": twenty files came back and
`admission.go` was not among them. Widening the answer did not recover it — the
acceptable declaration ranked below every row `ts_rank` returned first. That was
the shape of the gap: not noise at the bottom of a list, but the answer off it.

#### Ranking moved to Go

`intentrank.Rank` scores BM25 in process, and both backends now do retrieval
only: `Backend.MatchIntent` returns every matching reason unordered, and
`Reader.QueryIntent` scores, sorts, and truncates. The change is worth its cost
for three reasons.

- **One answer to judge.** Both backends now put the reader in the same place on
  every question, which
  `TestGoldenIntentPostgres_StartsTheReaderInTheSamePlaceAsSQLite` holds them to.
  A laptop measurement now says something about the server.
- **The corpus statistics come free.** An intent question matches on any term, so
  every document holding any query term is already a candidate. Counting the
  candidates counts the corpus exactly for those terms — no statistics table, no
  `ts_stat` job, nothing to keep in step. The one number that cannot be derived
  that way is how many documents exist at all, and that is a `COUNT`.
- **Ties stop moving.** `ts_rank` is coarse enough that nine reasons scored
  exactly `0.020264236` on one question, and changing the limit reordered them.
  Scoring sorts by score then `node_id`, so asking for one more row extends the
  answer instead of reshuffling it.

What it costs: every matching reason crosses the database boundary, capped by
`maxIntentCandidates` (10000). That cap is a runaway guard, not a page size — a
truncated candidate set makes a term look rarer than it is and quietly changes
the ranking. At 1751 documents the whole corpus fits well inside it; a corpus an
order of magnitude larger would need real statistics instead.

The two baseline files stay separate. They now agree on every rank, and differ
by one entry on two questions, because FTS5 and `to_tsvector('simple')` tokenize
prose slightly differently and so admit slightly different candidates. That is a
retrieval difference, which is the databases' job, and it is small.

### The debt this closed

The oldest negative case — "why does an invoice get a loyalty discount" —
expects an empty answer, because nothing in this codebase is about invoices.
It used to return two files. `get` survived the function-word list, and prefix
expansion then reached `getAnnotation`, `getImpactRadius`, and
`getAffectedFlows` spelled inside recorded reasons. Frequency was not the
culprit and no stopword list would have caught it — `get*` reaches only 3 of
1751 reasons.

Short ASCII terms in an intent question now match exactly instead
(`intentrank.MatchesByPrefix`, applied both where the query is built and where
the match is scored, so the two cannot drift), and both backends answer that
question with nothing. Two other prefix accidents went with it: `we*` was matching
`webhook` across 52 reasons, which is what had been putting the right file on
top of one incident question for the wrong reason. The SQLite MRR fell from
0.759 to 0.740 when that crutch was removed — a lower number measuring the same
thing more honestly.

Every negative case records a measured count rather than asserting zero, so that
a leak reopening is what fails the ratchet.

### What the set says at 47 questions

The set grew from 17 to 47. The 30 new questions were written by a different
model against the code rather than against the recorded reasons, which is the
point: a question paraphrased from an `@intent` line is a question the index was
always going to answer, and the first sixteen judgments were written by whoever
was also tuning the ranker.

| SQLite or PostgreSQL, scored in Go | n | answered | hit | top1 | top3 | MRR |
| --- | --- | --- | --- | --- | --- | --- |
| behavior | 16 | 16/16 | 15/16 | 5 | 12 | 0.539 |
| incident | 16 | 16/16 | 16/16 | 9 | 10 | 0.633 |
| korean | 4 | 4/4 | 1/4 | 1 | 1 | 0.250 |
| policy | 7 | 7/7 | 6/7 | 2 | 4 | 0.410 |
| **all** | **43** | **43/43** | **38/43** | **17** | **27** | **0.526** |

MRR fell from 0.740 to 0.526. Nothing in the ranker changed between those two
numbers — the questions got harder and less self-serving, and 0.526 is what this
ranker was always scoring. The four negative cases are excluded from the table
and reported separately, so a negative can never flatter the score.

Three weaknesses the wider set exposed, none of them fixed:

- **Nothing decides that an answer is too weak to give.** Retrieval matches on
  any term, and there is no score floor, so a question sharing one ordinary word
  with any recorded reason gets twenty files back. Three of the four negatives
  now answer that way: "which team owns the changed code and should review it"
  matches on `code`, `changed`, and `review`. The invoice question still returns
  nothing only because none of its words appear anywhere. A negative case is a
  question the tool should decline, and the tool still has no way to decline.

  What it does now is show its working. Every answer reports which words of the
  question matched each declaration and how many recorded reasons hold each of
  those words, so the report prints the negative above as `code 24, changed 19,
  owns 7, review 5, team 0` out of 1751 — four ordinary words and one nobody
  wrote down. That is enough for a reader to throw the answer out, and it costs
  no tuned constant: the counts are recounted against whatever corpus is being
  searched. It is not a fix for the missing floor, only the evidence a floor
  would have to be built from.
- **Korean holds at 1 of 4.** The original Korean question still answers at rank
  1 and the three added ones miss. Prefix matching handles a noun carrying a
  particle, which was the failure mode that had been anticipated; what it does
  not handle is a Korean question whose nouns are simply written differently
  from the Korean nouns in the reason.
- **A quarter of the set is buried.** Eleven of the 43 scored questions put the
  first acceptable file below rank 4, six of them below rank 6, one at 16. Both
  backends bury the same eleven, so this is the scorer's doing rather than a
  retrieval difference.

A word on tuning against this number. Every constant that would raise it — a
score floor, a stopword list, a length penalty — would be fitted to one
codebase's vocabulary, and there is no way to tell an improvement from an
overfit while the only measurement comes from that same codebase. A second
golden set on an unrelated repository is what would tell them apart, and until
one exists, prefer changes that are either plain correctness fixes or expressed
in terms the runtime recomputes per corpus.

### The exclude pattern that hid four files

Four of the thirty questions could not be answered when they arrived, and the
reason was not the ranker. They point at `internal/app/docs/generator.go` and
`internal/app/docs/lint.go`, and neither file was in the graph.

`.ccg.yaml` excluded `docs/.*`, meaning the generated `./docs` output directory.
A pattern containing `.*` is treated as a regular expression, and a regular
expression matches anywhere in the path, so it also dropped `internal/app/docs/`.
Four files produced no node, no error and no warning. The only symptom was that
questions about documentation generation had no answer — which is exactly what a
coverage gap looks like, so it would have been read as one.

The patterns are now anchored (`^docs/.*` for a root directory, `(^|.*/)testdata/.*`
for any depth), the corpus was recaptured at 1880 declarations, and the four
questions are in the set.
`TestProjectExcludesKeepOwnSourceInTheGraph` in `internal/archtest` holds this
repository's own config to the rule that it must not hide this repository's own
code.
