---
name: ccg
description: code-context-graph — code knowledge graph for token-efficient code understanding. Routes user intent to the right tool (search/analyze/docs/annotate/namespace).
---

# ccg — Routing & Search

ccg is a Tree-sitter-based code graph tool. **Complementary to Grep/Read, not a replacement.** Choose based on task type.

## Task Routing (most important)

| User intent                                | Tool              | Why                               |
| ------------------------------------------ | ----------------- | --------------------------------- |
| "Where is X?" — simple location lookup     | Grep + Read       | Faster and cheaper than ccg       |
| "Find code related to X" — semantic search | `ccg search`      | Annotation/keyword semantic match |
| "What's affected if I change X?"           | `/ccg-analyze`    | Graph traversal                   |
| "Understand structure/architecture"        | `/ccg-docs` (RAG) | `retrieve_docs` first, then tree  |
| "Document intent/rules in code"            | `/ccg-annotate`   | AI annotation workflow            |
| "Manage multiple service codebases"        | `/ccg-workspace`  | MSA namespace isolation           |

**Don't use ccg when Grep is enough.** ccg MCP context costs hundreds to thousands of tokens per task. For trivial tasks, it's pure overhead.

## Core Commands

```bash
ccg build .          # Build graph + search index (first time or after big changes)
ccg update .         # Incremental — changed files only
ccg search "<query>" # FTS search (includes annotations)
ccg status           # Graph statistics
ccg docs --out docs  # Generate docs + default RAG index
ccg serve            # Start MCP server (stdio)
```

For remote or self-hosted MCP over Streamable HTTP, use `ccg-server` instead of
`ccg serve`. Local `ccg serve` is stdio-only.

For detailed flags, use `ccg <command> --help` or refer to MCP schema.

## ccg search Patterns

Search by domain keywords (Korean works too). Annotation tags (`@intent`, `@domainRule`) are indexed alongside code.

```bash
ccg search "결제"               # All payment-related functions (Korean)
ccg search "authentication"     # Auth-related
ccg search --path internal/auth "login"  # Path-scoped
```

**Difference from Grep**: Grep matches text, search matches semantic intent. Searching "결제" finds `payment` functions through their `@intent 결제 처리` annotation.

## Build Freshness

- `ccg build .` fails with schema error → run `ccg migrate`, retry
- PostgreSQL or upgrading existing DB → `ccg migrate` first
- Graph feels stale after code changes → `ccg update .`

## Core MCP Tools (commonly used)

| Tool               | When                                         |
| ------------------ | -------------------------------------------- |
| `search`           | Semantic search                              |
| `query_graph`      | Structured queries (callers/callees/imports) |
| `get_node`         | Lookup by qualified name                     |
| `list_graph_stats` | Graph size check                             |
| `get_minimal_context` | Compact project snapshot and tool suggestions |

For other tools, see `/ccg-analyze`, `/ccg-docs` skills.

## Agent Entry Pattern

For broad natural-language questions, start with generated docs/RAG before
pulling exact graph edges:

```bash
ccg build .
ccg docs --out docs
```

Then use MCP `retrieve_docs` for bounded Markdown evidence, `get_rag_tree` to
expand module context, and only then `query_graph`, `get_node`, or
`trace_flow` for exact symbols and relationships.

## Response Budget Rule

For LLM-agent use, prefer bounded graph queries. Start with `limit=50` or
`limit=100` and follow `has_more` / `next_offset` rather than asking for a bulk
result first.

Tools with explicit pagination:

| Tool | Parameters |
| ---- | ---------- |
| `query_graph` | `limit`, `offset` |
| `list_flows` | `limit`, `offset` |
| `list_communities` | `limit`, `offset` |
| `get_community` | `member_limit`, `member_offset` when `include_members=true` |
| `get_architecture_overview` | `community_limit`, `community_offset`, `coupling_limit`, `coupling_offset` |

High-volume analysis tools such as `find_dead_code`,
`find_suspect_fallback_edges`, and broad architecture/onboarding prompts should
be scoped by namespace, path, or a narrower first question before use.

## Trade-offs (verified)

- **Search tasks** → ccg dominates (50–60% token reduction vs rg)
- **Single location lookup** → Grep+Read is cheaper (ccg is overhead)
- **Miss-prevention matters** (PR review, etc.) → ccg catches domain rules Grep misses
- **Frequently changing code** → factor in graph rebuild cost
