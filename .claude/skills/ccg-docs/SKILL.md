---
name: ccg-docs
description: code-context-graph — documentation generation, RAG indexing, and docs quality linting.
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
| `build_rag_index` | Build RAG index from docs and communities. Supports `workspace` parameter to read docs from a workspace directory. |
| `get_rag_tree` | Navigate RAG document tree. Supports `workspace` parameter to read workspace-specific doc-index.json. |
| `get_doc_content` | Get documentation file content. Supports `workspace` parameter to read from a workspace directory. |
| `search_docs` | Search RAG document tree by keyword. Supports `workspace` parameter to search workspace-specific doc-index.json. |

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

### Build RAG index from workspace docs
```
User: "my-service workspace 문서로 RAG 인덱스 만들어줘"
→ upload_file(workspace: "my-service", file_path: "docs/handler.go.md", content: "<base64>")
→ build_rag_index(workspace: "my-service")
→ search_docs(query: "handler")
→ get_doc_content(workspace: "my-service", file_path: "docs/handler.go.md")
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

## Lint Rules & Regex Patterns

`.ccg.yaml` rules support regex patterns for `pattern` field. Patterns containing `$`, `^`, `+`, `{}`, `|`, `\.`, or `.*` are auto-detected as regex:

```yaml
rules:
  # Exact match (legacy)
  - pattern: "pkg/auth.go::Login"
    category: unannotated
    action: ignore

  # Regex: ignore all symbols under pkg/store/
  - pattern: "pkg/store/.*"
    category: unannotated
    action: ignore

  # Regex: ignore all generated code
  - pattern: ".*_generated\\.go::.*"
    category: incomplete
    action: warn
```

## Prerequisites

Graph must be built first. If `ccg.db` doesn't exist, run `ccg build .` (see `/ccg` skill).
