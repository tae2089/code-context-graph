---
name: ccg-annotate
description: code-context-graph — AI-driven annotation workflow. Add @intent/@domainRule/etc to code so search and RAG can find by business meaning.
---

# ccg-annotate — Annotation Workflow

Add structured business metadata to code. **This is what makes search and RAG actually useful.**

## Why Annotations Matter

- Enables domain search that text matching can't do: "payment" → finds functions with `@intent payment processing`
- Enriches RAG community summaries
- LLM can grasp intent without reading code → token savings
- Surfaces domain rules automatically during PR review

**Without annotations, ccg delivers only half its value.**

## Core Tags

| Tag           | WHAT vs WHY                         | Example                                  |
| ------------- | ----------------------------------- | ---------------------------------------- |
| `@intent`     | **WHY this function exists**        | `verify identity before granting access` |
| `@domainRule` | Specific business rule              | `lock account after 5 failures`          |
| `@sideEffect` | Real side effects (DB/network/file) | `writes to audit_log`                    |
| `@mutates`    | State changes                       | `user.FailedAttempts, session.Token`     |
| `@requires`   | Precondition                        | `user.IsActive == true`                  |
| `@ensures`    | Postcondition                       | `returns valid JWT with 24h expiry`      |
| `@index`      | One-line file/package summary       | `User authentication service`            |
| `@see`        | Related function or CCG ref         | `SessionManager.Create`, `ccg://auth-svc/internal/auth/token.go#ValidateToken` |

## Retrieval-Aware Tag Selection

Annotations are retrieval features, but they must stay truthful. Use the
specific tag that matches the code's role instead of stuffing keywords into
`@intent`.

| When you see this | Add this |
| ----------------- | -------- |
| File/package/module should be found as one unit | `@index` |
| Public function, handler, CLI command, service method, or UI workflow has a clear purpose | `@intent` |
| Policy, constraint, operational rule, or false-positive/false-negative criterion matters | `@domainRule` |
| DB/file/network/cache/log/process side effect exists | `@sideEffect` |
| Receiver or input state changes | `@mutates` |
| Caller-facing input/output contract matters | `@requires`, `@ensures` |
| Related implementation must be followed, especially across namespaces | `@see` |

Good retrieval annotations include the words a developer or LLM would naturally
use to ask for the code, while matching the implementation. For example, if a
graph component focuses a resolved `ccg://` node, say `graph viewer`, `ccg ref`,
and `node focus` in the appropriate `@index`/`@intent`. Do not add unrelated
terms just to raise score; broad terms make the wrong files rank higher.

## AI Workflow (`/ccg-annotate annotate <path>`)

This is an agent skill workflow, not a `ccg` CLI subcommand. The CLI provides
`ccg example <language>` and `ccg tags` for writing guidance; the agent reads
and edits code directly.

### Step 1: Pick targets

- File path → that file only
- Directory → all source files
- **Skip**: tests, vendor, node_modules, generated

### Step 2: Analyze each function

Read the code and determine:

- What it does → first summary line
- File/package discoverability → `@index` when the file itself is a useful search target
- **Why it exists → `@intent`** for meaningful public/workflow entry points
- Business or operational rules → `@domainRule` (must be specific)
- Real side effects → `@sideEffect`
- State changes → `@mutates`
- Caller-facing contracts → `@requires`, `@ensures` when they matter

For cross-namespace behavior, explain the reason in the semantic tag and put the target in `@see`:

```go
// @sideEffect records token validation audit in auth-svc.
// @see ccg://auth-svc/internal/audit/token_audit.go#RecordTokenAudit
```

### Step 3: Write

- Add as comments directly above the declaration using language syntax (`//`, `#`)
- **Do NOT overwrite existing annotations**
- **Skip trivial functions** (getters/setters, obvious one-line wrappers)
- Do not add tags that do not match real behavior
- Do not repeat the same keyword across tags unless each tag adds distinct evidence
- Match the language of existing comments (Korean for Korean codebases, English for English)

### Step 4: Rebuild

```bash
ccg build .   # re-index with annotations
```

## Quality Rules (this is what really matters)

❌ **Bad annotation**:

```go
// @intent creates a user
func CreateUser(...) {}
```

WHAT only. Function name already tells you that.

✅ **Good annotation**:

```go
// @intent register new account for onboarding flow
// @domainRule email must be unique across all tenants
// @domainRule password must satisfy NIST 800-63B
// @sideEffect sends verification email
// @mutates users table, audit_log
func CreateUser(...) {}
```

WHY + business rules + side effects. Search and RAG become powerful.

## Annotation Priority

Don't annotate everything. Prioritize:

1. **Tier 1**: domain core (auth, payment, billing — business logic)
2. **Tier 2**: frequently searched (entry points, public APIs)
3. **Tier 3**: complex functions (high cognitive load)
4. **Skip**: getters/setters, simple wrappers, generated code

Use `ccg lint` `unannotated` category and pick top-priority functions from there.

## Retrieval Quality Checks

After adding annotations for a feature area, run a few natural-language
retrieve/search probes that match how an LLM or engineer would ask:

```bash
ccg build .
ccg search "graph viewer ccg ref node focus"
```

For MCP/Web UI retrieval, use `retrieve_docs` or the Wiki Retrieve mode. If the
expected file is missing, prefer improving the precise `@index`, `@intent`,
`@domainRule`, or `@see` evidence on that file over changing global scoring.

## Search Integration

Once annotated, `ccg search` indexes annotation text alongside code (see `/ccg` skill):

```bash
ccg search "결제"        # finds functions with "결제" in @intent (Korean)
ccg search "lock"        # finds functions with "lock" in @domainRule
```

## MCP Tools

| Tool             | Use                                  |
| ---------------- | ------------------------------------ |
| `get_annotation` | Fetch annotation/doc tags for a node |

## Prerequisites

Requires `ccg build .` first. (See `/ccg` skill.)
