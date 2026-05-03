# Webhook Sync

Receives push events from GitHub or Gitea and automatically performs clone/pull → code graph build.

## Setup

```bash
ccg serve --transport streamable-http \
  --allow-repo "org/api:main,develop" \
  --allow-repo "org/web:main" \
  --webhook-secret "your-secret" \
  --repo-clone-base-url https://github.com \
  --repo-root /data/repos
```

For local testing only, you can explicitly opt into insecure mode instead of HMAC verification and canonical clone URL reconstruction:

```bash
ccg serve --transport streamable-http \
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

1. **HTTP server shutdown** — stops accepting new requests (5-second timeout)
2. **sync context cancel** — propagates `context.Done()` to in-progress clone/build operations
3. **worker drain** — waits for SyncQueue workers to finish (30-second timeout)

In-progress clone/build operations receive the context cancel and stop immediately, minimizing shutdown wait time.

## Pipeline

```
Push Event → HMAC Verify → RepoFilter.IsAllowedRef()
  → SyncQueue.Add() (dedup) → Worker
    → CloneOrPull (ctx, 15min timeout)
    → GraphService.Update (incremental, ctx, 15min timeout)
    → Save to DB
```

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

## Operational Signals

Use `/ready` for traffic gating and `/status` for diagnosis. A queue full or
stalled queue can make `/ready` return `not_ready`; a failed latest webhook sync
can make `/status` report `degraded` without necessarily removing the instance
from service.

For deployment profiles, database choice, namespace size guidance, and common
failure modes, see [Operations](operations.md).

## Panic Recovery

`defer recover()` is applied to all goroutines so individual worker panics do not crash the entire process:

- Signal handler goroutine
- HTTP server goroutine
- SyncQueue worker goroutine
- SyncQueue shutdown goroutine
