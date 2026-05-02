---
name: ccg-workspace
description: code-context-graph — namespace file management. Upload, list, and delete files in isolated namespace directories for MSA source management.
---

# code-context-graph — Namespace File Management

Manage namespace file directories for uploading, organizing, and deleting source files. Designed for MSA environments where each namespace represents a service. The skill name remains `ccg-workspace` as a legacy alias.

## MCP Tools (6)

| Tool | Description |
|------|-------------|
| `upload_file` | Upload a single file to namespace (base64 encoded content) |
| `upload_files` | Upload multiple files to namespaces in a single call (JSON array) |
| `list_namespaces` | List all namespaces |
| `list_workspaces` | Deprecated alias for `list_namespaces` |
| `list_files` | List files in a namespace |
| `delete_file` | Delete a single file from namespace |
| `delete_namespace` | Delete an entire namespace and all its files |
| `delete_workspace` | Deprecated alias for `delete_namespace` |

## Namespace Directory Structure

```
{namespace-root}/
├── payment-svc/
│   ├── handler.go
│   └── service.go
├── user-svc/
│   ├── auth.go
│   └── profile.go
└── gateway/
    └── router.go
```

- Canonical root flag is `--namespace-root <dir>` (default: `workspaces`); `--workspace-root` remains a deprecated alias
- Each namespace maps to a service/module directory: `{namespace}/{file}`
- File content is uploaded as base64-encoded strings

## Usage Examples

### Upload a single file
```
→ upload_file(namespace: "payment-svc", file_path: "handler.go", content: "<base64>")
```

### Bulk upload multiple files
```
→ upload_files(files: '[{"namespace":"payment-svc","file_path":"handler.go","content":"<base64>"},{"namespace":"payment-svc","file_path":"service.go","content":"<base64>"}]')
```

Note: `files` parameter is a JSON string containing an array of file entries.

### List all namespaces
```
→ list_namespaces()
→ Returns: ["payment-svc", "user-svc", "gateway"]
```

### List files in a namespace
```
→ list_files(namespace: "payment-svc")
→ Returns: ["handler.go", "service.go"]
```

### Delete a file
```
→ delete_file(namespace: "payment-svc", file_path: "handler.go")
```

### Delete entire namespace
```
→ delete_namespace(namespace: "payment-svc")
→ Removes payment-svc/ directory and all files within
```

## E2E Pipeline: Upload → Build → Search

After uploading files, build the graph and search:

```
1. upload_file(namespace: "payment-svc", file_path: "handler.go", content: "<base64>")
2. build_or_update_graph(path: "{namespace-root}/payment-svc")  — see /ccg skill
3. search(query: "payment")  — see /ccg skill
```

## E2E Pipeline: Upload Docs → RAG Index → Search → Read

Upload documentation files to a namespace directory, then build and query the RAG index:

```
1. upload_file(namespace: "my-service", file_path: "docs/internal/handler.go.md", content: "<base64>")
2. build_rag_index(namespace: "my-service")  — see /ccg-docs skill
3. search_docs(query: "handler", namespace: "my-service")
4. get_rag_tree(namespace: "my-service")
5. get_doc_content(namespace: "my-service", file_path: "docs/internal/handler.go.md")
```

## Security

- Path traversal attacks are blocked (`../` in namespace or file_path)
- File size is validated before writing
- Namespace names are sanitized
