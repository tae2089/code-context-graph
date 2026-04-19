# Webhook Sync

Receives push events from GitHub or Gitea and automatically performs clone/pull → code graph build.

## Setup

```bash
ccg serve --transport streamable-http \
  --allow-repo "org/api:main,develop" \
  --allow-repo "org/web:main" \
  --webhook-secret "your-secret" \
  --repo-root /data/repos
```

### Endpoints

| Path | Method | Description |
|------|--------|-------------|
| `/mcp` | POST | MCP Streamable HTTP |
| `/health` | GET | Health check (`{"status":"ok"}`) |
| `/webhook` | POST | Webhook receiver (GitHub / Gitea push events) |

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

1. First matching rule is used (order matters)
2. Rejected if no matching rule found
3. `refs/heads/` prefix is automatically stripped from the `ref` field in webhook payload

## Signature Verification

Verifies webhook payload with HMAC-SHA256.

| Platform | Header | Format |
|----------|--------|--------|
| GitHub | `X-Hub-Signature-256` | `sha256=<hex>` |
| Gitea | `X-Gitea-Signature` | `<hex>` |

Signature verification is skipped if `--webhook-secret` is not set.

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
    → CloneOrPull (ctx, 10min timeout)
    → GraphService.Build (ctx, 10min timeout)
    → Save to DB
```

### Deduplication

Consecutive pushes to the same repo are automatically merged in the SyncQueue:
- New push while repo is being processed → dirty flag → reprocessed with latest payload after completion
- Same repo already in queue → only payload is updated (no duplicate enqueue)

### Concurrency

- Default 4 workers
- Different repos are processed in parallel
- Same repo is processed sequentially (dirty requeue)

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

## Panic Recovery

`defer recover()` is applied to all goroutines so individual worker panics do not crash the entire process:

- Signal handler goroutine
- HTTP server goroutine
- SyncQueue worker goroutine
- SyncQueue shutdown goroutine
