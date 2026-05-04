# Eval

`ccg eval` measures parser accuracy and search quality against a local evaluation corpus. Treat it as an offline quality gate, not a live service metric.

## What It Measures

| Suite | Measures | Main Metrics |
|-------|----------|--------------|
| `parser` | Whether source files produce the expected graph nodes and edges | Node/edge precision, recall, F1 |
| `search` | Whether search returns expected results near the top of the ranking | P@1, P@3, P@5, R@5, MRR, nDCG@5 |

Parser eval compares the current parser output with `*.golden.json` files. Search eval runs queries from `queries.json` and compares ranked results with the `relevant` list.

## Corpus Layout

The default corpus is `testdata/eval`.

```text
testdata/eval/
  go/sample.go
  go/sample.go.golden.json
  python/sample.py
  python/sample.py.golden.json
  queries.json
```

Terms:

| Term | Meaning |
|------|---------|
| corpus | A dataset used for evaluation |
| golden | Expected output used as the answer key |
| golden corpus | Evaluation dataset that includes source inputs and expected outputs |
| expected | Data from the golden files or relevant search list |
| actual | Data produced by the current parser or search backend |

## Parser Metrics

Parser eval normalizes nodes as `kind:name@file` and edges as `kind:from->to`, then computes set-based classification metrics.

| Metric | Meaning |
|--------|---------|
| true positive | Exists in both expected and actual |
| false positive | Exists in actual but not expected |
| false negative | Exists in expected but not actual |
| precision | `true_positive / (true_positive + false_positive)` |
| recall | `true_positive / (true_positive + false_negative)` |
| F1 | Harmonic mean of precision and recall |

Low precision usually means the parser creates extra nodes or edges. Low recall usually means it misses expected nodes or edges.

## Search Metrics

Search eval uses `queries.json`.

Bare identifier queries may list multiple relevant IDs when the corpus intentionally contains the same symbol name across several languages. In that case, any of those exact-name matches counts as relevant. Language-qualified queries such as `getUser JavaScript` or `get_user Rust` should remain narrow and point to one intended fixture.

```json
{
  "queries": [
    {
      "query": "impact analysis",
      "relevant": ["function:ImpactRadius@internal/analysis/impact/impact.go"],
      "k": 5
    }
  ]
}
```

| Metric | Meaning |
|--------|---------|
| P@1 / P@3 / P@5 | Fraction of top-k results that are relevant |
| R@5 | Fraction of all relevant results found in the top 5 |
| MRR | Reciprocal rank of the first relevant result |
| nDCG@5 | Ranking quality in the top 5, rewarding relevant results near the top |
| negative_queries | Count of negative-control queries with `relevant: []` |
| negative_false_positives | Number of negative-control queries that still returned results |
| negative_pass_rate | Fraction of negative-control queries that correctly returned zero results |

### Negative controls

Search eval may include negative-control queries with `"relevant": []`. These cases are excluded from the ranking averages (`P@K`, `R@5`, `MRR`, `nDCG@5`) so the positive-query baseline keeps the same meaning. Instead, they are tracked through `negative_queries`, `negative_false_positives`, and `negative_pass_rate`.

When the corpus contains negative controls, the table output also shows:

```text
Negatives: 1
FP:        0
Pass Rate: 1.0000
```

This block is omitted when `negative_queries == 0`.

### Per-query diagnostics (JSON only)

JSON output now includes a `per_query` array under `search` so regressions can be traced back to individual queries without re-running them manually. Each entry records the query text, whether it was a `positive` or `negative` case, how many results were returned, and the per-query ranking metrics (or `false_positive` for negative controls).

Each `per_query` entry may also include `top_results`, capped at 5 values. This captures the first returned result keys directly in the eval JSON, which makes negative-control leaks and low-precision positives debuggable without re-running `ccg search` by hand.

## Commands

```bash
# Run parser and search eval
ccg eval

# Parser only
ccg eval --suite parser

# Search only
ccg eval --suite search

# Use another corpus
ccg eval --corpus testdata/eval

# Machine-readable output
ccg eval --format json
```

## Reproducible Baseline Workflow

Search eval depends on a database that already contains the eval corpus. On a fresh DB, run migrate and build first, then evaluate against the same DB and namespace.

The repository provides a wrapper for this flow:

```bash
scripts/eval.sh
```

The script runs this sequence against an isolated SQLite DB by default:

1. `ccg migrate`
2. `ccg build testdata/eval --namespace eval`
3. `ccg eval --corpus testdata/eval --namespace eval`

Useful overrides:

```bash
# Keep the generated DB for debugging
CCG_EVAL_KEEP_DB=1 scripts/eval.sh

# Use a specific DB path
CCG_EVAL_DB_DSN=.ccg-eval.db scripts/eval.sh

# Use go run instead of an installed ccg binary
CCG_BIN='go run -tags "fts5" ./cmd/ccg' scripts/eval.sh
```

Update golden files only after reviewing the parser diff and confirming the new output is intended.

```bash
ccg eval --suite parser --update
```

`--update` is for parser golden files. It records the current parser output as the new expected result.

## Interpreting Results

- `parser` precision drop: inspect newly created nodes or edges for false positives.
- `parser` recall drop: inspect missing nodes or edges for false negatives.
- `search` P@1/MRR drop: the right result exists but ranking got worse.
- `search` R@5 drop: relevant results are missing from the top search window.
- Strong eval numbers only prove quality for the corpus. Add representative cases when fixing parser or search behavior.
