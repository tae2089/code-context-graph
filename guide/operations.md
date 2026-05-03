# Operations

This guide collects deployment and operating decisions that are easy to miss
when CCG moves from a local tool to a shared service.

## Deployment Profiles

| Profile | Recommended Setup | Notes |
|---------|-------------------|-------|
| Local CLI | SQLite, local `ccg.db`, manual `ccg build` / `ccg update` | Treat the database as a disposable cache. |
| Personal MCP server | SQLite is acceptable if one user and one repository update at a time | Keep the database on local disk, not a network volume. |
| Team MCP server | PostgreSQL, explicit `ccg migrate`, bearer token on HTTP | Plan backup/restore and connection limits. |
| Webhook service | PostgreSQL preferred, repo allowlist, HMAC webhook secret | Keep operational endpoints internal. |
| Multi-repository namespace service | PostgreSQL, one namespace per repo/service | Watch namespace size and postprocess cost. |

## Database Choice

Use SQLite when all of these are true:

- One developer or one local automation process owns the database
- One small or medium repository is indexed
- Rebuilds are acceptable when the local cache gets stale
- There is no webhook worker pool or shared HTTP service

Use PostgreSQL when any of these are true:

- CCG is served to a team or multiple MCP clients
- Webhook sync is enabled
- Multiple repositories or namespaces share one database
- Backup, restore, monitoring, remote access, or controlled migrations matter
- A namespace has roughly 50k+ search documents or 100k+ graph nodes

For 300k+ graph nodes, multiple always-synced repositories, or frequent webhook
updates, PostgreSQL should be the default. SQLite can still work for
read-mostly local use, but write serialization and FTS maintenance become the
operational bottleneck.

## Namespace Size

Namespace size affects postprocessing more than query routing. Search and
community rebuilds operate inside the namespace, so very large namespaces make
updates slower even when only one repository changed. Stored flows can be
bulk-rebuilt via `build_or_update_graph` with `postprocess=full` or via
`run_postprocess` with `flows=true`; `trace_flow` remains the per-entry-point
query tool.

Practical guidance:

- Keep one repository or service per namespace unless cross-service graph
  queries are the main use case.
- Use `include_paths` when only part of a repository is useful to agents.
- Prefer PostgreSQL once a namespace reaches around 50k search documents or
  100k graph nodes.
- Split a namespace when postprocess time or webhook queue age becomes a normal
  operating concern rather than an occasional large update.

Incremental updates rebuild only affected search documents and FTS rows. Full
builds, explicit `run_postprocess`, and community rebuilds can still be
namespace-wide, so namespace boundaries remain the main cost control.

## HTTP Exposure

The Streamable HTTP MCP endpoint should be protected with
`--http-bearer-token` or `CCG_HTTP_BEARER_TOKEN` whenever it is externally
reachable.

By default, `ccg serve --transport streamable-http` listens on
`127.0.0.1:8080`. Binding to a non-loopback address requires either
`--http-bearer-token` or the explicit testing escape hatch `--insecure-http`.
Bearer token protection applies to `/mcp` only; `/health`, `/ready`, `/status`,
and `/webhook` still need network-level exposure control.

Keep these endpoints internal:

| Endpoint | Exposure Guidance |
|----------|-------------------|
| `/health` | Internal liveness probe only |
| `/ready` | Internal readiness probe only |
| `/status` | Internal operational diagnostics only |
| `/webhook` | Public only when HMAC verification and repo allowlist are configured |
| `/mcp` | May be exposed behind bearer auth and network controls |

If CCG is behind an ingress, reverse proxy, or load balancer, block
`/health`, `/ready`, and `/status` from public internet access. These endpoints
can expose runtime state that is useful operationally but not intended as a
public API.

## Webhook Operations

Webhook deployments should be configured with:

- `--allow-repo` for an explicit repository and branch allowlist
- `--webhook-secret` for HMAC verification
- `--repo-clone-base-url` so clone URLs are reconstructed from allowed repo
  names instead of trusted from payloads
- `--repo-root` on durable local storage
- `--db-driver postgres` for team or always-on deployments

SQLite webhook deployments default to one worker unless
`--webhook-workers` or `CCG_WEBHOOK_WORKERS` is explicitly set. This avoids
creating multiple concurrent writers against the same SQLite database. With
PostgreSQL, worker count should be sized by repository update time, database
capacity, and acceptable queue age.

Use `--webhook-max-tracked-repos` to bound queue memory. When the queue is at
capacity, new repositories are rejected with `429 Too Many Requests`; repeated
hits should be treated as a scaling or scoping problem.

Webhook request body size is separate from source parse size. The webhook
payload is small and capped by the HTTP receiver, but the clone/build step has
no default source parse budget. Use `include_paths`, `--max-file-bytes`, or
`--max-total-parsed-bytes` when large repositories need an explicit parsing
budget.

Unreadable source files are logged and skipped by default during webhook graph
updates. Enable `--webhook-fail-on-unreadable` when partial graphs are not
acceptable and the sync should fail/retry instead.

Invalid repository configuration, such as malformed `.ccg.yaml` `include_paths`,
is treated as non-retryable for the current event. Fix the repository config and
push again to trigger a fresh sync.

## Readiness and Status

`/ready` is meant for traffic gating. It should fail when the database is not
usable or when the webhook queue is blocking service operation, such as a full
queue or a stalled oldest item.

`/status` is meant for diagnosis. It can report `degraded` when the latest
webhook sync failed, while `/ready` may still stay ready if the queue can accept
and process future work. Treat `degraded` as an operator action signal, not
always as a reason to remove the instance from service.

Recommended checks:

| Signal | Meaning | Operator Action |
|--------|---------|-----------------|
| `/ready` returns `not_ready` | DB unavailable, queue full, or blocking queue age | Stop sending traffic and inspect logs/status. |
| `/status` is `degraded` | Last webhook or postprocess state needs attention | Inspect failed repo/config and retry with a new push or manual update. |
| Queue age grows steadily | Workers cannot keep up with incoming pushes | Reduce repo scope, increase workers on PostgreSQL, or split namespaces. |
| Search results look stale | Search postprocess may have failed or been skipped | Run `run_postprocess` with `fts=true` or rebuild/update the namespace. |

For alerting, prefer these `/status.webhook` fields:

| Field | Alert Use |
|-------|-----------|
| `oldest_queued_age` | Queue delay and worker capacity pressure (JSON number in nanoseconds) |
| `oldest_processing_age` | Stalled clone/update detection (JSON number in nanoseconds) |
| `queue_full_total` | Capacity limit hits since process start |
| `failure_total` | Sync failure rate since process start |
| `recent_repos[].last_error` | Repo-specific unresolved failures |
| `recent_repos[].queued` / `processing` | Which repository is waiting or running now |

## Timeouts and Shutdown

Webhook clone and build share a per-attempt timeout of 15 minutes. Retries use
exponential backoff and default to three attempts, so the maximum time for a
single event is roughly 46 minutes before it is abandoned.

On SIGINT/SIGTERM:

1. HTTP stops accepting new requests with a 5-second shutdown window.
2. The sync context is cancelled and in-progress clone/build work observes
   `context.Done()`.
3. SyncQueue workers get up to 30 seconds to drain.

The Streamable HTTP server does not use a fixed `WriteTimeout` because MCP
streams can be long-lived. Put idle connection limits and request buffering
policies at the reverse proxy if the service is internet-facing.

Other HTTP server timeouts are fixed in the current binary: `ReadHeaderTimeout`
is 10 seconds, `ReadTimeout` is 30 seconds, and `IdleTimeout` is 120 seconds.

CCG does not currently expose a Prometheus-style `/metrics` endpoint. Use
`/health`, `/ready`, and `/status` for operational probes, and treat eval or
benchmark metrics as offline analysis outputs rather than live service metrics.

## Migrations

The default local SQLite database (`ccg.db`) auto-migrates only when its schema
is missing. Existing SQLite schemas, PostgreSQL, custom SQLite DSNs, and
controlled upgrades require an explicit migration:

```bash
ccg migrate --db-driver postgres --db-dsn "$CCG_DB_DSN"
```

Run migrations as a separate deployment step for PostgreSQL. Do not rely on
application startup to upgrade a shared service database.

After upgrading CCG, an existing default `ccg.db` should also be treated as an
existing schema and migrated explicitly before restarting runtime commands.

## Troubleshooting

| Symptom | Likely Cause | Check / Fix |
|---------|--------------|-------------|
| `401` or MCP initialize fails | Missing or wrong bearer token | Confirm `Authorization: Bearer ...` and `CCG_HTTP_BEARER_TOKEN`. |
| Webhook returns unauthorized | Missing/invalid HMAC signature | Verify `--webhook-secret` and provider signature header. |
| Webhook returns forbidden | Repo or branch not allowed | Check `--allow-repo` patterns and branch refs. |
| Webhook returns too many requests | Sync queue is full | Check `/status`, reduce push volume, or increase workers on PostgreSQL. |
| `/ready` is `not_ready` | DB or queue blocking condition | Inspect `/status` and service logs. |
| `/status` is `degraded` | Last webhook or postprocess failed | Fix repo config or rerun update/postprocess. |
| Search misses recent code | FTS/search documents stale | Run `run_postprocess` with `fts=true` or rebuild the namespace. |
| Migration error at startup | Schema version mismatch, migration source mismatch, or schema drift | Run `ccg migrate` from the deployed binary version; if it still fails, verify the configured migration source and schema drift. |
