---
name: ccg-docs
description: code-context-graph — documentation generation, RAG indexing, and docs quality linting.
user-invocable: true
---

# code-context-graph — Documentation & RAG Indexing

Generate Markdown documentation from code graphs, build RAG indexes for AI consumption, and lint documentation quality.

## Subcommands

| Command | Description | Example |
|---------|-------------|---------|
| `docs [--out dir]` | Generate Markdown documentation | `ccg docs --out docs` |
| `index [--out dir]` | Regenerate index.md only | `ccg index` |
| `lint [--out dir]` | 8-category docs lint | `ccg lint` |
| `lint --strict` | Exit 1 on issues (for CI/pre-commit) | `ccg lint --strict` |
| `hooks install` | Install pre-commit git hook | `ccg hooks install` |
| `hooks install --lint-strict` | Install hook that blocks commit on issues | `ccg hooks install --lint-strict` |

## MCP Tools (4)

| Tool | Description |
|------|-------------|
| `build_rag_index` | Build RAG index from docs and communities |
| `get_rag_tree` | Navigate RAG document tree |
| `get_doc_content` | Get documentation file content |
| `search_docs` | Search RAG document tree by keyword |

## Lint Categories (8)

| Category | Description |
|----------|-------------|
| orphan | Doc files with no matching source code |
| missing | Source files with no documentation |
| stale | Docs outdated vs source (hash/timestamp mismatch) |
| unannotated | Functions lacking @intent/@domainRule annotations |
| contradiction | Doc content contradicting code signatures |
| dead-ref | @see tags pointing to non-existent functions |
| incomplete | Partial documentation (missing @param, @return) |
| drift | Doc structure diverged from code structure |

## Usage Examples

### Generate documentation
```
User: "문서 생성해줘"
→ ccg docs --out docs
→ Generates Markdown files for all modules
```

### Build RAG index for AI
```
User: "RAG 인덱스 만들어줘"
→ build_rag_index via MCP
→ Creates searchable document tree from docs + communities
```

### Check documentation quality
```
User: "문서 상태 체크해줘"
→ ccg lint
→ Returns 8-category report: orphan, missing, stale, unannotated, etc.
```

### CI integration
```yaml
# .github/workflows/docs.yml
- run: ccg lint --strict  # Fails build on documentation issues
```

## Prerequisites

Graph must be built first. If `ccg.db` doesn't exist, run `ccg build .` (see `/ccg` skill).
