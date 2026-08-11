---
name: ccg-build
description: "Build, update, migrate, and postprocess code-context-graph graphs from source visible to the CCG runtime. Use when a graph is missing or stale, source annotations must be reindexed, a namespace needs Git-backed synchronization, or a scoped graph write needs explicit replacement semantics. Do not use for ordinary read-only search, lookup, or analysis when the existing graph is sufficient; use the ccg skill instead."
metadata:
  version: 1.0.0
  openclaw:
    category: "code-intelligence"
    domain: "build"
  requires:
    bins:
      - ccg
  cliHelp: "ccg build --help"
---

# ccg-build — Graph Ingestion

Build or refresh graph state only from source paths the executing CCG runtime
can read.

## Mandatory Contract

Before any graph write, read
[`references/graph-maintenance.md`](references/graph-maintenance.md) completely.
Resolve the server-visible source path, namespace, update mode, scoped
replacement behavior, and required postprocessing before invoking a write.

## Ingestion Boundary

- Local CLI and stdio MCP may build from a local source path.
- A remote HTTP server ingests repositories through its configured Git
  webhook/sync path by default. Treat Git as the transport and source of truth.
- Do not invent or attempt database upload, graph-bundle upload, streaming
  upload, or client-filesystem transfer. MCP graph-write tools operate on paths
  already visible to the server; they are not upload APIs.
- If a remote server cannot access the source and no admitted Git sync path is
  available, report the limitation instead of trying a client-local path.

## Command Selection

```bash
ccg update <dir>  # Ordinary source edits; incremental synchronization
ccg build <dir>   # First use, intentional full rebuild, or recovery
ccg migrate       # Existing database schema upgrade when required
```

Over MCP:

- `build_or_update_graph(full_rebuild=false)` requests an incremental update.
- `build_or_update_graph(full_rebuild=true)` requests a full rebuild.
- `parse_project` writes parsed graph state without search postprocessing.
- `run_postprocess` refreshes selected derived artifacts after graph changes.

Do not call a write tool merely because it is registered. Confirm that the
user authorized graph mutation and that the target path and namespace are the
intended ones.

## Completion

Report the resolved source path, namespace, Git sync or local ingestion mode,
full versus incremental choice, scoped `replace` behavior, and every failed or
skipped postprocess step. State which verification was not run.
