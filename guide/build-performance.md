# Large Build Performance

This guide records the measured changes that make a full CCG build practical on
large source trees. It is scoped to parsing, graph persistence, edge resolution,
and search-document rebuilding; it is not a general database tuning guide.

## Benchmark scope

The representative workload was a Kotlin corpus with 1,592 files (about 4.86
MB). Every comparison used a fresh SQLite database, the same binary and build
options, and the same source snapshot. The figures are single fresh-build
measurements: they identify the bottleneck, not a statistically rigorous
throughput claim.

The final graph contained 9,419 nodes, 6,942 persisted edges, and 9,402 search
documents.

| Stage | Before import-file index | After import-file index |
| --- | ---: | ---: |
| Parse and spool | 8,682 ms | 8,728 ms |
| Node persistence | 594 ms | 560 ms |
| Edge resolution | 16,712 ms | 888 ms |
| Search rebuild | 315 ms | 307 ms |
| Total | 26,442 ms | 10,603 ms |

The import-file index reduced total build time by about 59.9% and edge
resolution by about 94.7% for this workload. The parser worker pool had already
reduced an earlier parse stage from 11,123 ms to 8,716 ms (about 21.6%).

## What changed

### Stage timing

`BuildStats.Timing` and the build-complete log report parse, node persistence,
edge resolution, search rebuild, and total duration. Profile a representative
fresh build before changing concurrency, batches, or database configuration.

### Bounded parsing concurrency

The parse-and-spool stage uses four workers. A coordinator preserves input and
spool-record order; database writes, edge resolution, and search rebuilding
remain sequential inside their existing transaction. This keeps failure and
cancellation behavior unchanged while using available CPU for parsing.

### Edge batches

Edges are resolved and upserted in batches capped at 4,000 edges. A one-off
8,000-edge measurement was slightly slower (29,147 ms versus 29,107 ms total),
so 4,000 remains the production limit. Treat a larger batch as a hypothesis to
measure, not an automatic improvement.

### Build-scoped import-file index

The decisive bottleneck was import suffix resolution. Before the index,
`GetFileNodesByPathSuffix` ran 1,983 times and spent 14,742 ms reading all file
nodes in the namespace and comparing paths in Go.

During full-build edge resolution, CCG now reads the namespace's real file
nodes once, then builds an in-memory index for the transaction lifetime:

- an exact directory map;
- a directory-suffix map; and
- the existing priority rule: exact directories win, otherwise every match at
  the longest suffix is returned.

The index is created after file nodes are persisted and discarded when the full
build's edge-resolution phase ends. Incremental builds and ordinary suffix
queries retain their existing paths. This avoids a long-lived store cache and
its invalidation requirements.

## Correctness checks

Performance changes were accepted only after comparing independently built,
fresh databases. In both directions, natural-key comparisons found no
differences in the final 9,419 nodes, 6,942 persisted edges, or 9,402 search
documents.

Focused tests also cover:

- exact-directory priority;
- ambiguous longest-suffix matches;
- namespace and `kind=file` filtering; and
- a single import-file read per build resolver.

## Reproducing a measurement

Use a disposable database and a stable source snapshot. For example:

```bash
ccg --db-driver sqlite --db-dsn /tmp/ccg-benchmark.db --log-json build /path/to/repository
```

Read the build-complete JSON log fields for the stage durations. Repeat enough
times to account for local CPU, filesystem cache, and database differences.
Compare graph contents as well as elapsed time; a faster build that changes
nodes, edges, or search documents is a regression.

## PostgreSQL note

PostgreSQL was not benchmarked for this change because no local PostgreSQL
instance was available during the measurement. It was not the immediate next
optimization: the former path transferred and scanned all file nodes 1,983
times, so changing database drivers alone would retain that work and may make
it more expensive over a network.

If PostgreSQL is the deployment target, repeat the same fresh-build and
content-equivalence procedure against its real topology before tuning indexes,
connection settings, or write concurrency.
