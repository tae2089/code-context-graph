---
name: ccg-namespace
description: code-context-graph — namespace file management for MSA / multi-project isolation.
---

# ccg-namespace — Namespace File Management

Manage uploaded source or documentation files in isolated **namespaces**. Use this for MSA environments or workflows spanning multiple repositories.

## Terminology

| Term | Status | Meaning |
| ---- | ------ | ------- |
| `namespace` | Canonical | Isolation key for graph data, uploaded files, RAG indexes, and postprocess policy |
| `--namespace-root` | Canonical | Root directory that stores namespace file trees |

For single-project local work, you usually do not need a named namespace. Use `ccg build .` and the default namespace.

## Folder Structure

```
{namespace-root}/
├── payment-svc/
│   ├── handler.go
│   └── service.go
├── user-svc/
│   └── auth.go
└── gateway/
    └── router.go
```

Configured via `ccg serve --namespace-root <dir>` for local stdio MCP, or
`ccg-server --namespace-root <dir>` for self-hosted HTTP MCP (default:
`namespaces/`).

## Core Patterns

### Upload, Then Build And Search

```
upload_file(namespace: "payment-svc", file_path: "handler.go", content: "<base64>")
→ build_or_update_graph(namespace: "payment-svc", path: "{namespace-root}/payment-svc")
→ search(namespace: "payment-svc", query: "payment")
```

### Wiki And RAG Per Namespace

```
upload_file(namespace: "my-service", file_path: "docs/handler.go.md", content: "<base64>")
→ build_rag_index(namespace: "my-service")
→ search_docs(namespace: "my-service", query: "handler")
→ get_doc_content(namespace: "my-service", file_path: "docs/handler.go.md")
```

### Bulk Upload

```
upload_files(files: '[{"namespace":"payment-svc","file_path":"a.go","content":"<base64>"},...]')
```

`files` is a JSON string containing an array. Prefer bulk upload over many single-file calls.

## MCP Tools

| Tool | Use |
| ---- | --- |
| `upload_file` | Upload one file to a namespace (base64) |
| `upload_files` | Upload many files to namespaces (JSON array) |
| `list_namespaces` | List all namespaces |
| `list_files` | List files in a namespace |
| `delete_file` | Delete one file from a namespace |
| `delete_namespace` | Delete a namespace and its files |

## Security

- Path traversal (`../`) is blocked
- Namespace names must be single safe path segments
- Symlink traversal is rejected before file writes
- File size and bulk request size are capped

## Operational Tips

- Use one namespace per service or repository when graph/search state should stay isolated.
- Keep reference-only services in separate namespaces and rebuild them rarely.
- Delete retired service state with `delete_namespace` to remove graph, RAG, and search noise.
