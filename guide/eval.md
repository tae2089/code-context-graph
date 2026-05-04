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
