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
| `@see`        | Related function link               | `SessionManager.Create`                  |

## AI Workflow (`ccg annotate <path>`)

Not a CLI binary — **a workflow Claude executes.**

### Step 1: Pick targets

- File path → that file only
- Directory → all source files
- **Skip**: tests, vendor, node_modules, generated

### Step 2: Analyze each function

Read the code and determine:

- What it does → first summary line
- **Why it exists → `@intent`** (most important)
- Business rules → `@domainRule` (must be specific)
- Real side effects → `@sideEffect`
- State changes → `@mutates`

### Step 3: Write

- Add as comments directly above the declaration using language syntax (`//`, `#`)
- **Do NOT overwrite existing annotations**
- **Skip trivial functions** (getters/setters)
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
