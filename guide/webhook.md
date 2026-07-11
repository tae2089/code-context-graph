# Webhook Sync

Receives push events from GitHub or Gitea and automatically performs clone/pull → code graph build.

## Setup

```bash
ccg-server \
  --allow-repo "org/api:main,develop" \
  --allow-repo "org/web:main" \
  --webhook-secret "your-secret" \
  --repo-clone-base-url https://github.com \
  --repo-root /data/repos
```

For local testing only, you can explicitly opt into insecure mode instead of HMAC verification and canonical clone URL reconstruction:

```bash
ccg-server \
  --allow-repo "org/*" \
  --insecure-webhook \
  --repo-root /data/repos
```

### Endpoints

| Path | Method | Description |
|------|--------|-------------|
| `/mcp` | POST | MCP Streamable HTTP |
| `/health` | GET | Health check (`{"status":"ok"}`) |
| `/ready` | GET | Readiness check |
| `/status` | GET | Operational status, including database and webhook queue state |
| `/webhook` | POST | Webhook receiver (GitHub / Gitea push events) |

### HTTP Exposure

Use the HTTP endpoints inside a trusted network only. The MCP endpoint can be
protected with `--http-bearer-token`, but operational endpoints such as
`/health`, `/ready`, and `/status` are intended for internal health checks and
may expose runtime state. When deploying behind an ingress, reverse proxy, or
load balancer, restrict those endpoints to internal callers or block them from
public internet access. See the [Operations Guide](operations.md#http-exposure)
for ingress and readiness guidance.

The default Streamable HTTP listen address is `127.0.0.1:8080`. When binding to
non-loopback addresses, configure `--http-bearer-token` for `/mcp` or use
`--insecure-http` only for local testing.

## Per-Repo Branch Filtering

Use the `--allow-repo` flag to configure allowed branches per repository.

### Format

```
--allow-repo "REPO_PATTERN:BRANCH1,BRANCH2"
```

- `REPO_PATTERN`: glob pattern (uses `path.Match`). e.g. `org/*`, `org/api`, `*/*`
- `BRANCH1,BRANCH2`: allowed branches (comma-separated, glob patterns supported)
- Default when no branch specified: `main`, `master`

### Examples

```bash
# Allow only main and develop branches for org/api
--allow-repo "org/api:main,develop"

# All repos under org, default branches (main, master)
--allow-repo "org/*"

# Allow release/* pattern branches
--allow-repo "org/api:main,release/*"

# Multiple repo configurations
--allow-repo "org/api:main,develop" --allow-repo "org/web:main"
```

### Matching Rules

1. Later matching rules override earlier matching rules (order matters)
2. Rejected if no matching rule found
3. `refs/heads/` prefix is automatically stripped from the `ref` field in webhook payload

### Namespace Strategy and Single-Org Operation

Webhook sync derives the graph namespace and checkout directory from the
repository name portion of `org/repo`. For example, `acme/api` maps to the
`api` namespace and `/data/repos/api` checkout.

This keeps single-organization deployments short and predictable, which is the
recommended operating model. If the allowlist spans multiple owners, such as
`acme/*` plus `external/shared` or `*/*`, CCG logs a startup warning because
repositories with the same final name can collide:

| Repo | Derived namespace |
|------|-------------------|
| `acme/api` | `api` |
| `external/api` | `api` |

Operational policy:

- Prefer one owner/organization per webhook CCG instance.
- Do not allow two repositories with the same final repo name in the same
  instance.
- If multi-owner sync becomes necessary, run separate CCG instances or change
  the namespace strategy before enabling those rules.

## Signature Verification

Verifies webhook payload with HMAC-SHA256.

| Platform | Header | Format |
|----------|--------|--------|
| GitHub | `X-Hub-Signature-256` | `sha256=<hex>` |
| Gitea | `X-Gitea-Signature` | `<hex>` |

By default, webhook requests fail closed unless `--webhook-secret` is configured.

- `--webhook-secret` enables HMAC verification.
- `--insecure-webhook` is an explicit testing-only escape hatch and is mutually exclusive with `--webhook-secret`.
- When running in secure mode, `--repo-clone-base-url` is required and the server reconstructs clone URLs from the allowed repository name instead of trusting `clone_url` from the webhook payload.

## Graceful Shutdown

On SIGINT/SIGTERM:

1. **HTTP server shutdown** — stops accepting new requests (`--webhook-shutdown-timeout`, default 30 seconds)
2. **sync context cancel** — propagates `context.Done()` to in-progress clone/build operations
3. **worker drain** — waits for SyncQueue workers to finish (`--webhook-shutdown-timeout`, default 30 seconds)

In-progress clone/build operations receive the context cancel and stop immediately, minimizing shutdown wait time.
After shutdown begins, new webhook deliveries are rejected with `503 Service Unavailable` so providers can retry instead of treating the event as successfully accepted.

## Pipeline

```
Push Event → HMAC Verify → RepoFilter.IsAllowedRef()
  → SyncQueue.Add() (dedup) → Worker
    → CloneOrPull (ctx, 15min timeout)
    → GraphService.Update (incremental, ctx, 15min timeout)
    → Save to DB
```

## Tracing

When CCG runs with Streamable HTTP, the webhook path creates real
OpenTelemetry SDK spans even if no exporter is configured. Incoming
`traceparent` headers on `/webhook` become the parent of a server span, and the
same trace continues through queue processing, retry attempts, clone/pull, and
graph update work.

- `--otel-endpoint` or `CCG_OTEL_ENDPOINT` unset: spans stay local to the
  process and are not exported
- `--otel-endpoint` set: spans are exported through OTLP HTTP to the given full
  endpoint URL, such as `http://collector:4318/v1/traces`
- traced webhook logs include `trace_id`, `span_id`, and `trace_sampled`

This means webhook failures can be correlated across HTTP intake, queueing, and
repository sync logs without changing the webhook payload format.

### Deduplication

Consecutive pushes to the same repo are automatically merged in the SyncQueue:
- New push while repo is being processed → dirty flag → reprocessed with latest payload after completion
- Same repo already in queue → only payload is updated (no duplicate enqueue)

### Concurrency

- Default 4 workers
- SQLite webhook deployments default to 1 worker unless `--webhook-workers` or `CCG_WEBHOOK_WORKERS` is set explicitly
- Different repos are processed in parallel
- Same repo is processed sequentially (dirty requeue)
- For team or always-on webhook deployments, prefer PostgreSQL and size workers by queue age, repository update time, and database capacity
- `--webhook-max-tracked-repos` / `CCG_WEBHOOK_MAX_TRACKED_REPOS` bounds queue memory and returns `429` when a new repo would exceed the limit

### Retry / Backoff

On clone or build failure, automatically retries with exponential backoff:

| Setting | Default | Description |
|---------|---------|-------------|
| MaxAttempts | 3 | Maximum attempt count (including first attempt) |
| BaseDelay | 1s | Wait time before first retry |
| MaxDelay | 30s | Upper bound for retry wait time |

- **Per-attempt timeout**: clone and build share a single 15-minute context — if the combined time exceeds the limit, the attempt fails and retries
- **Maximum total time**: 3 attempts × 15 minutes + backoff (max ~30s) ≈ **46 minutes**
- Exponential growth: 1s → 2s → 4s → ... (capped at MaxDelay)
- Pending retries are immediately cancelled on context cancellation (server shutdown)
- Panics are treated as errors and are eligible for retry
- Invalid repository config, such as malformed `.ccg.yaml` `include_paths`, is treated as non-retryable for the current event
- After exceeding MaxAttempts, logs an `ERROR` and abandons the sync (retryable on next push event)

These defaults can be tuned with `--webhook-attempt-timeout`,
`--webhook-retry-attempts`, `--webhook-retry-base-delay`, and
`--webhook-retry-max-delay`.

## `.ccg.yaml` include_paths Auto-Apply

During webhook builds, the `include_paths` setting from `.ccg.yaml` inside the cloned repo is automatically read to restrict build scope.

```yaml
# .ccg.yaml inside the repo
include_paths:
  - src/
  - lib/
```

- If `.ccg.yaml` is absent or has no `include_paths` key, the entire directory is built
- Operates independently of the CLI's `--config` flag (direct YAML parsing, no viper)

## Parse Size Limits

Webhook request bodies are limited separately from repository parsing. The
webhook payload is capped by the server, but the subsequent clone/build step has
no default source parse size limit. By default, CCG builds every matching source
file in the cloned repository unless `include_paths` narrows the scope.

If a deployment needs a parse budget for large repositories, configure it
explicitly with `--max-file-bytes`, `--max-total-parsed-bytes`, or the matching
`.ccg.yaml` parse settings. CCG does not impose default webhook parse limits.

## Unreadable Files

By default, unreadable source files during webhook graph update are logged and
skipped. This keeps sync resilient when a repository contains broken symlinks,
permission-denied files, or transient read errors, but it can produce a partial
graph for that event.

Use `--webhook-fail-on-unreadable` when partial sync is not acceptable. With
this flag, unreadable source files fail the webhook sync attempt; retryable
failures follow the normal retry/backoff policy and remain visible through
`/status`.

## Operational Signals

Use `/ready` for traffic gating and `/status` for diagnosis. A queue full or
stalled queue can make `/ready` return `not_ready`; a failed latest webhook sync
can make `/status` report `degraded` without necessarily removing the instance
from service.

`/status` includes a `webhook` object when webhook sync is enabled. Important
fields:

| Field | Meaning |
|-------|---------|
| `queued`, `processing`, `dirty` | Current queue and worker state |
| `tracked_repos`, `max_tracked_repos` | Queue tracking capacity; new repos are rejected when full |
| `queue_full_total`, `failure_total` | Cumulative operational counters since process start |
| `oldest_queued_age`, `oldest_processing_age` | Delay signals used by readiness checks (JSON numbers in nanoseconds) |
| `last_error`, `last_error_time`, `last_success_time` | Aggregate latest success/failure state |
| `recent_repos` | Up to 50 recent, queued, or processing repositories with repo, branch, queued/processing state, and last success/error fields |

`/status` reports `degraded` when the aggregate latest failure is unresolved or
when any recent repo has an unresolved failure. A later successful sync for the
same repo clears that repo's error state.

CCG does not currently expose a `/metrics` endpoint for webhook operations.
Treat `/status` as the primary structured runtime view.

### Recovery Runbook

Use this runbook when `/status` reports a degraded webhook sync, queue age keeps
growing, or a deployment restart may have interrupted an accepted event.

1. Check `/status.webhook.recent_repos` and logs for `repo`, `branch`, and
   `last_error`.
2. Fix non-retryable repository configuration failures, such as malformed
   `.ccg.yaml` `include_paths`.
3. Trigger a new push on the same branch when the upstream provider should
   drive recovery.
4. For manual recovery, update the namespace from the checkout directory:

   ```bash
   ccg update /data/repos/api --namespace api
   ccg status --namespace api
   ```

5. If search, communities, or saved flows still look stale, call the MCP
   `run_postprocess` tool for namespace `api` with the needed postprocess flags.
6. Recheck `/status`; a successful sync for the same repo clears the repo-level
   unresolved error.

For deployment profiles, database choice, namespace size guidance, and common
failure modes, see [Operations](operations.md).

## Panic Recovery

`defer recover()` is applied to all goroutines so individual worker panics do not crash the entire process:

- Signal handler goroutine
- HTTP server goroutine
- SyncQueue worker goroutine
- SyncQueue shutdown goroutine
