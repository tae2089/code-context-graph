# Postprocess Failure Policy

This guide explains how CCG reports `ok`, `degraded`, `fail_closed`, and
`skipped_steps` for the two MCP tools that can rebuild derived graph state:

- `build_or_update_graph`
- `run_postprocess`

It also explains the automatic policy engine that chooses `degraded` or
`fail_closed` when the caller does not provide an explicit
`postprocess_policy`.

## Terms

| Term | Meaning |
|------|---------|
| `ok` | Requested work succeeded, or only skipped steps remained. |
| `degraded` | Some requested postprocess steps failed, but the tool still returned a structured success result. |
| `fail_closed` | A failure policy that does not tolerate postprocess step errors. When a requested step fails, the tool returns an error for that call. |
| `skipped_steps` | Steps that were not attempted because the caller disabled them, the selected mode did not include them, or the required builder/backend was not configured. |

## Automatic policy engine

When the caller omits `postprocess_policy`, CCG resolves the effective policy in
this order:

1. Explicit caller value wins when provided.
2. Otherwise, the automatic policy engine looks at recent runs for the same
   `(namespace, tool)` pair.
3. Default policy is `degraded`.
4. After **three consecutive `degraded` runs** for the same `(namespace, tool)`, the
   automatic policy escalates to `fail_closed`.
5. A later `ok` run resets the consecutive-failure streak.

Policy state is persisted as:

- current effective state in `ccg_postprocess_policy_state`
- append-only execution history in `ccg_postprocess_run_logs`

## Namespace isolation and shared state

Automatic policy decisions and postprocess results are scoped to the current
`namespace`.

- Policy escalation is tracked per `(namespace, tool)`.
- Derived state rebuilds (`flows`, `communities`, `search_documents`, `fts`) are
  applied only to the active namespace.
- A failure in one namespace does not directly mark another namespace as
  `degraded` or `fail_closed`.

In normal operation, this means a failed rebuild in namespace `A` can leave only
namespace `A` stale while namespace `B` continues to query normally.

The main exception is truly global operational state, such as schema migration
compatibility. For example, `SchemaVersion` is not namespace-scoped, so a global
schema mismatch can affect the whole deployment even though postprocess policy
and derived graph state are isolated per namespace.

## `build_or_update_graph`

`build_or_update_graph` has two phases:

1. graph build/update
2. optional postprocess work controlled by `postprocess`

If the build/update phase itself fails, the tool returns an error before any
postprocess status is produced.

### Input validation and hard failures

| Condition | Internal behavior | Result |
|-----------|-------------------|--------|
| `path` is missing | Request validation fails before execution | Error |
| `path` is outside the configured analysis root | Path validation fails | Error |
| `postprocess` is not `full`, `minimal`, or `none` | Request validation fails | Error |
| `postprocess_policy` is not `degraded` or `fail_closed` | Request validation fails | Error |
| Graph build/update fails | Build or incremental update returns an error | Error |

### Postprocess mode behavior

| `postprocess` value | Steps attempted | Steps skipped by design |
|---------------------|-----------------|--------------------------|
| `full` | `flows`, `communities`, `search_documents`, `fts` | none |
| `minimal` | `search_documents`, `fts` | `flows`, `communities` |
| `none` | none | `flows`, `communities`, `search_documents`, `fts` |

### Status table

| Situation | Example | Returned result |
|-----------|---------|-----------------|
| Build/update succeeded and all requested postprocess steps succeeded | Full rebuild + all enabled derived state refreshed | `status="ok"` |
| Requested step failed and effective policy is `degraded` | `communities` rebuild fails | `status="degraded"`, `failed_steps` contains `communities` |
| Requested step failed and effective policy is `fail_closed` | `fts` rebuild fails after auto escalation or explicit override | Tool returns an error for the call |
| Requested step is unavailable | `FlowBuilder == nil` or `SearchBackend == nil` | Step appears in `skipped_steps`; not an error by itself |
| Selected mode excludes a step | `postprocess="minimal"` or `postprocess="none"` | Step appears in `skipped_steps`; not an error by itself |

### Step-by-step failure map

| Step | Failure cause | `degraded` policy result | `fail_closed` policy result |
|------|---------------|--------------------------|-----------------------------|
| `flows` | `FlowBuilder.Rebuild()` returns an error | `failed_steps += ["flows"]`, tool still returns JSON result | Failure is recorded, then the tool returns an error |
| `communities` | `CommunityBuilder.Rebuild()` returns an error | `failed_steps += ["communities"]`, tool still returns JSON result | Failure is recorded, then the tool returns an error |
| `search_documents` | search document refresh fails | `failed_steps += ["search_documents"]`, tool still returns JSON result | Failure is recorded, then the tool returns an error |
| `fts` | `SearchBackend.Rebuild()` returns an error | `failed_steps += ["fts"]`, tool still returns JSON result | Failure is recorded, then the tool returns an error |

### Response fields to inspect

| Field | Meaning |
|-------|---------|
| `status` | Overall result: `ok` or `degraded` |
| `postprocess_policy` | Effective policy used for the call |
| `policy_source` | `explicit` when caller supplied the policy, `auto` otherwise |
| `failed_steps` | Requested steps that ran and failed |
| `skipped_steps` | Requested or mode-controlled steps that were intentionally not run |

## `run_postprocess`

`run_postprocess` operates only on already-built graph state. It does not parse
source files again.

### Input validation and hard failures

| Condition | Internal behavior | Result |
|-----------|-------------------|--------|
| `community_depth` is outside `1..8` | Request validation fails | Error |
| `postprocess_policy` is not `degraded` or `fail_closed` | Request validation fails | Error |

### Requested-step behavior

| Request flags | Steps attempted | Steps skipped |
|---------------|-----------------|---------------|
| `flows=true` | `flows` | none unless `FlowBuilder == nil` |
| `communities=true` | `communities` | none unless `CommunityBuilder == nil` |
| `fts=true` | `search_documents`, `fts` | none unless `SearchBackend == nil` |
| `flows=false` | none | `flows` |
| `communities=false` | none | `communities` |
| `fts=false` | none | `search_documents`, `fts` |

### Status table

| Situation | Example | Returned result |
|-----------|---------|-----------------|
| All requested steps succeeded | `flows=true, communities=true, fts=true` and all rebuilds succeed | `status="ok"` |
| Requested step failed and effective policy is `degraded` | `search_documents` refresh fails | `status="degraded"`, failed step is reported |
| Requested step failed and effective policy is `fail_closed` | `flows` rebuild fails after explicit or automatic fail-closed policy | Tool returns an error for the call |
| Requested step is unavailable | `CommunityBuilder == nil` or `SearchBackend == nil` | Step appears in `skipped_steps`; not an error by itself |
| Caller disabled a step | `flows=false` or `fts=false` | Step appears in `skipped_steps`; not an error by itself |

### Step-by-step failure map

| Step | Failure cause | `degraded` policy result | `fail_closed` policy result |
|------|---------------|--------------------------|-----------------------------|
| `flows` | `FlowBuilder.Rebuild()` returns an error | `failed_steps += ["flows"]`, tool still returns JSON result | Failure is recorded, then the tool returns an error |
| `communities` | `CommunityBuilder.Rebuild()` returns an error | `failed_steps += ["communities"]`, tool still returns JSON result | Failure is recorded, then the tool returns an error |
| `search_documents` | search document refresh fails | `failed_steps += ["search_documents"]`, tool still returns JSON result | Failure is recorded, then the tool returns an error |
| `fts` | `SearchBackend.Rebuild()` returns an error | `failed_steps += ["fts"]`, tool still returns JSON result | Failure is recorded, then the tool returns an error |

### Response fields to inspect

| Field | Meaning |
|-------|---------|
| `status` | Overall result: `ok` or `degraded` |
| `postprocess_policy` | Effective policy used for the call |
| `policy_source` | `explicit` when caller supplied the policy, `auto` otherwise |
| `failed_steps` | Requested steps that ran and failed |
| `skipped_steps` | Disabled or unavailable steps |
| `flows_count` | Number of stored flows rebuilt in this run |
| `communities_count` | Number of communities rebuilt in this run |
| `fts_indexed` | `1` when FTS rebuild completed, otherwise `0` |

## Operational reading

| Observation | Meaning | Typical next action |
|-------------|---------|---------------------|
| `status="degraded"` | The request completed, but some derived state may now be stale or partially refreshed | Inspect `failed_steps`, fix the failing backend/builder/config, then rerun the tool |
| Tool returned an error under `fail_closed` | The effective policy considered the failure non-tolerable for that call | Fix the underlying failure and rerun; if needed, temporarily use explicit `postprocess_policy="degraded"` |
| `skipped_steps` is non-empty | Requested work was intentionally not attempted | Check whether the caller disabled the step or whether the required backend/builder is missing |
| Auto policy switched to `fail_closed` | Recent failures for the same `(namespace, tool)` crossed the escalation threshold | Treat it as a persistent operational problem, not a one-off transient warning |
