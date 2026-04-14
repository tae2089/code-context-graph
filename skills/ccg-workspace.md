---
name: ccg-workspace
description: code-context-graph — file workspace management. Upload, list, and delete files in isolated workspaces for MSA source management.
user-invocable: true
---

# code-context-graph — File Workspace Management

Manage file workspaces for uploading, organizing, and deleting source files. Designed for MSA environments where each workspace represents a service.

## MCP Tools (6)

| Tool | Description |
|------|-------------|
| `upload_file` | Upload a single file to workspace (base64 encoded content) |
| `upload_files` | Upload multiple files to workspaces in a single call (JSON array) |
| `list_workspaces` | List all workspaces |
| `list_files` | List files in a workspace |
| `delete_file` | Delete a single file from workspace |
| `delete_workspace` | Delete an entire workspace and all its files |

## File Storage Structure

```
{workspace-root}/
├── payment-svc/
│   ├── handler.go
│   └── service.go
├── user-svc/
│   ├── auth.go
│   └── profile.go
└── gateway/
    └── router.go
```

- Workspace root is configured via `--workspace-root <dir>` (default: `workspaces`)
- Each workspace maps to a service/module directory: `{workspace}/{file}.md`
- File content is uploaded as base64-encoded strings

## Usage Examples

### Upload a single file
```
→ upload_file(workspace: "payment-svc", file_path: "handler.go", content: "<base64>")
```

### Bulk upload multiple files
```
→ upload_files(files: '[{"workspace":"payment-svc","file_path":"handler.go","content":"<base64>"},{"workspace":"payment-svc","file_path":"service.go","content":"<base64>"}]')
```

Note: `files` parameter is a JSON string containing an array of file entries.

### List all workspaces
```
→ list_workspaces()
→ Returns: ["payment-svc", "user-svc", "gateway"]
```

### List files in a workspace
```
→ list_files(workspace: "payment-svc")
→ Returns: ["handler.go", "service.go"]
```

### Delete a file
```
→ delete_file(workspace: "payment-svc", file_path: "handler.go")
```

### Delete entire workspace
```
→ delete_workspace(workspace: "payment-svc")
→ Removes payment-svc/ directory and all files within
```

## E2E Pipeline: Upload → Build → Search

After uploading files, build the graph and search:

```
1. upload_file(workspace: "payment-svc", file_path: "handler.go", content: "<base64>")
2. build_or_update_graph(path: "{workspace-root}/payment-svc")  — see /ccg skill
3. search(query: "payment")  — see /ccg skill
```

## Security

- Path traversal attacks are blocked (`../` in workspace or file_path)
- File size is validated before writing
- Workspace names are sanitized
