---
name: ccg-workspace
description: code-context-graph — workspace isolation for MSA / multi-project. Each workspace = one service, isolated graphs.
---

# ccg-workspace — Multi-Project Isolation

Manage multiple services/projects as isolated graphs. Use for **MSA environments or workflows spanning multiple repos.**

## When to Use

- Analyzing multiple microservices (separate graph per service)
- Working across multiple projects (namespace prevents collisions)
- Scoping AI context to one specific service

For single-project work, you don't need workspaces. Just use `ccg build .`.

## Folder Structure

```
{workspace-root}/
├── payment-svc/         # workspace 1
│   ├── handler.go
│   └── service.go
├── user-svc/            # workspace 2
│   └── auth.go
└── gateway/             # workspace 3
    └── router.go
```

Configured via `--workspace-root <dir>` (default: `workspaces/`).

## Core Patterns

### Pattern A: Upload, then build & search

```
upload_file(workspace: "payment-svc", file_path: "handler.go", content: "<base64>")
→ build_or_update_graph(path: "{root}/payment-svc")  # see /ccg
→ search(query: "payment")                            # see /ccg
```

### Pattern B: Wiki + RAG (per-service isolation)

```
upload_file(workspace: "my-service", file_path: "docs/handler.go.md", content: "<base64>")
→ build_rag_index(workspace: "my-service")          # see /ccg-docs
→ search_docs(workspace: "my-service", query: "handler")
→ get_doc_content(workspace: "my-service", file_path: "...")
```

### Bulk Upload

```
upload_files(files: '[{"workspace":"payment-svc","file_path":"a.go","content":"<base64>"},...]')
```

JSON string of an array. More efficient than many single uploads.

## MCP Tools

| Tool               | Use                            |
| ------------------ | ------------------------------ |
| `upload_file`      | Upload one file (base64)       |
| `upload_files`     | Upload many files (JSON array) |
| `list_workspaces`  | List all workspaces            |
| `list_files`       | List files in a workspace      |
| `delete_file`      | Delete one file                |
| `delete_workspace` | Delete entire workspace        |

## Security

- Path traversal (`../`) blocked
- File size validated
- Workspace names sanitized

## Operational Tips

- **Frequently changing service** → isolate workspace for incremental update efficiency
- **Reference-only service** → keep in separate workspace, rebuild rarely
- **Deprecated service** → `delete_workspace` to clean up search noise
