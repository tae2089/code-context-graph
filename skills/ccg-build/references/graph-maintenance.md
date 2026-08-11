# CCG Graph Maintenance Contract

Read this file completely before building, updating, migrating, postprocessing,
or making a scoped graph write.

## Source Path Identity

Before any graph write, resolve the project as a server-visible path. The MCP
server can run in a different filesystem or working directory from the client.

- Never pass `path: "."` unless the server-side working directory has been
  verified as the intended repository.
- When project configuration or instructions provide a server-visible path,
  use that exact path.
- Confirm the source path, namespace, and `include_paths` all refer to the same
  repository. If that identity cannot be established, stop before writing.
- Pass the intended namespace explicitly on graph writes; do not rely on a
  client or server default when multiple namespaces exist.

Report the resolved server-visible path after a graph write. When asked which
skill resources were read, report this file by resolved absolute path, not
basename.

## Freshness

1. `ccg status` or MCP `list_graph_stats` proves population, not freshness.
   Compare the graph with relevant source changes before relying on a miss.
2. Use `ccg build .` for first use, an intentional full rebuild, or recovery.
3. Use `ccg update .` after ordinary source edits.
4. Over MCP, use `build_or_update_graph(full_rebuild=false)` for an ordinary
   incremental update; its default is a full rebuild when the argument is
   omitted.
5. If a command reports schema drift, or an existing SQLite/PostgreSQL database
   is being upgraded, run `ccg migrate` and retry.

Do not rebuild merely to start a read-only query. Refresh when the graph is
missing, relevant source changed, or the requested artifact must be regenerated.

## Scoped Update Safety

Choose replacement semantics deliberately:

- `include_paths` with default `replace=true` makes the selected scope
  authoritative and removes previously indexed files outside that scope.
- Pass `replace=false` to preserve out-of-scope files while reconciling changes
  and deletions inside the selected scope.
- Omit `include_paths` when the entire source root is authoritative.

`replace=false` protects only the incremental update path. The current MCP
handler silently falls back to a full Build when its incremental syncer is not
configured, even if `full_rebuild=false`; full Build deletes the namespace graph
and repopulates only `include_paths`, so `replace` is not applied. There is no
MCP preflight for this capability.

When preserving out-of-scope data is required, execute a scoped
`build_or_update_graph` only when server configuration independently proves the
incremental syncer is available. Otherwise stop and report that safe scoped
preservation cannot be guaranteed. A backup does not make an unverified call
safe to execute.

Record the chosen `replace` behavior in the completion report.

## Postprocessing

Use `parse_project` alone only when a graph write without search
postprocessing is intentional. Run `run_postprocess` when FTS or stored flows
must be current.

Stored flows need `run_postprocess(flows=true)` after graph changes. The
registered `communities` option is currently ignored by the handler; never
report community state as rebuilt from that flag.

Inspect and report every `failed_steps` and `skipped_steps` entry before calling
postprocessed state current.
