# Development

## Build

```bash
CGO_ENABLED=1 go build -tags "fts5" -o ccg ./cmd/ccg/
```

## Test

```bash
CGO_ENABLED=1 go test -tags "fts5" ./... -count=1
```

### Eval Test

Parser accuracy evaluation against 12-language golden corpus:

```bash
# Update golden files (after parser changes)
ccg eval --suite parser --update

# Compare accuracy
ccg eval --suite parser

# JSON format output
ccg eval --format json
```

## Integration Test

Full-stack pipeline test: Gitea push → webhook → ccg clone → build → PostgreSQL → MCP verification:

```bash
./scripts/integration-test.sh
```

### What It Does

1. Start 3 containers via Docker Compose (Gitea, PostgreSQL, ccg)
2. Create Gitea admin user and API token
3. Create repository with sample Go code
4. Register webhook pointing to ccg
5. Push code to Gitea (triggers webhook)
6. Wait for ccg to complete clone, parse, and build
7. Verify graph data via MCP protocol (initialize → tools/call)
8. Clean up all containers

### Manual Container Management

```bash
docker compose -f docker-compose.integration.yml up -d --build
docker compose -f docker-compose.integration.yml down -v
```

## Project Structure

```
cmd/ccg/              — CLI entry point (cobra)
internal/
  analysis/           — Analysis engine (impact, flows, deadcode, community, ...)
  annotation/         — Annotation parser
  benchmark/          — Token reduction benchmark (naive vs graph, recall measurement)
  cli/                — CLI command definitions
  ctxns/              — Context namespace
  docs/               — Documentation generation
  eval/               — Parser/search quality evaluation (golden corpus, P/R/F1, P@K, MRR, nDCG)
  mcp/                — MCP server (29 tools)
  model/              — DB models
  parse/treesitter/   — Tree-sitter parser (12 languages, including Lua/Luau)
  pathutil/           — Path utilities
  ragindex/           — RAG index
  service/            — Business logic
  store/              — GORM store
  webhook/            — Webhook handler, SyncQueue, RepoFilter
skills/               — Claude Code skill files
guide/                — Project documentation
docs/                 — Auto-generated docs (ccg docs)
testdata/eval/        — Eval golden corpus (12-language sources + golden JSON)
scripts/              — Scripts (integration test, etc.)
```

## Token Benchmark

Measures how much CCG reduces context tokens delivered to an LLM compared to naive full-file reading.

```bash
ccg benchmark token-bench \
  --db-dsn ./ccg.db \
  --corpus testdata/benchmark/queries.yaml \
  --repo /path/to/target-repo \
  --exts .go \
  --limit 30
```

### Measurement Method

| Field | Description |
|-------|-------------|
| `naive_tokens` | Total token count of all source files in the repo (`len(text)/4` estimate) |
| `graph_tokens` | Token count of nodes collected via description → FTS search → 1-hop expansion |
| `ratio` | `naive / graph` |
| `recall` | Hit rate for expected_files + expected_symbols (0–1) |

### Design Principles

- **Search uses description**: Using `expected_symbols` directly as a search query is oracle cheating. Only ASCII words extracted from the natural-language description are passed to FTS.
- **Per-term limit auto-adjustment**: `limitPerTerm = max(3, limit / len(terms))` — maintains total result budget regardless of term count.
- **1-hop expansion included**: Realistic `graph_tokens` measurement via gormstore expander, including neighbor nodes and annotations.
- **Ratio without recall is meaningless**: Queries with recall < 0.5 cannot be trusted even if graph_tokens is small.

### gin-gonic Benchmark Results (limit=30)

| query | ratio | recall |
|-------|-------|--------|
| router | ~54x | 0.6 |
| context | ~54x | 0.5 |
| middleware | ~79x | 1.0 |
| binding | ~35x | 0.75 |
| recovery | ~46x | 1.0 |

Comparable or better than the code-review-graph paper baseline of 49x, based on honest measurement.

## Conventions

- TDD: Red → Green → Refactor
- Tidy First: Separate structural changes from behavioral changes
- Use GORM queries only (no raw SQL)
- Logging: `slog`
- CLI: `cobra` framework
- Build flags: `CGO_ENABLED=1 -tags "fts5"`
