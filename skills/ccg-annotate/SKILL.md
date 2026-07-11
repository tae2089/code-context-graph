---
name: ccg-annotate
description: "Author and refine CCG annotations such as @intent, @domainRule, @sideEffect, @mutates, @index, and @see. Use when adding business meaning to code, improving annotation-aware code or documentation retrieval, fixing annotation lint findings, or documenting operational contracts. Do not use for generated Markdown editing or annotations that merely restate symbol names."
metadata:
  version: 1.1.0
  openclaw:
    category: "code-intelligence"
    domain: "annotation"
  requires:
    bins:
      - ccg
    skills:
      - ccg
---

# ccg-annotate — Annotation Workflow

Add structured business metadata to source comments so graph search and
generated documentation can retrieve intent and operational contracts.

## Why Annotations Matter

- Lets full-text retrieval match domain terms recorded in annotations even when symbol names differ
- Enriches generated docs and DB-backed documentation evidence

## Core Tags

| Tag           | WHAT vs WHY                         | Example                                  |
| ------------- | ----------------------------------- | ---------------------------------------- |
| `@intent`     | **WHY this function exists**        | `verify identity before granting access` |
| `@domainRule` | Specific business rule              | `lock account after 5 failures`          |
| `@sideEffect` | Real side effects (DB/network/file) | `writes to audit_log`                    |
| `@mutates`    | Receiver or argument state changes  | `user.FailedAttempts, session.Token`     |
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
| Receiver or argument object changes in memory | `@mutates` |
| Caller-facing input/output contract matters | `@requires`, `@ensures` |
| Related implementation must be followed, especially across namespaces | `@see` |

Good retrieval annotations include the words a developer or LLM would naturally
use to ask for the code, while matching the implementation. For example, if a
graph component focuses a resolved `ccg://` node, say `graph viewer`, `ccg ref`,
and `node focus` in the appropriate `@index`/`@intent`. Do not add unrelated
terms just to raise score; broad terms make the wrong files rank higher.

Read [`references/annotation-reference.md`](references/annotation-reference.md)
when checking the complete tag contract, aliases, multiline behavior, or
language-specific comment syntax.

## Annotation Workflow

This is an agent skill workflow, not a `ccg` CLI subcommand. The agent reads and
edits code directly.

### Step 1: Pick targets

- File path → that file only
- Directory → all source files
- **Skip**: vendor, dependencies, and generated code
- Skip tests by default; include a test only when it is itself important domain-contract evidence

### Step 2: Analyze each target

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

- Add comments directly above the declaration using the language's documentation-comment syntax
- Preserve accurate human-authored context; update or remove stale, false, or duplicate tags when refinement is requested
- Do not erase non-obvious rationale unless the code contradicts it; report such changes explicitly
- **Skip trivial functions** (getters/setters, obvious one-line wrappers)
- Do not add tags that do not match real behavior
- Do not repeat the same keyword across tags unless each tag adds distinct evidence
- Match the language of existing comments (Korean for Korean codebases, English for English)

### Step 4: Refresh and verify

```bash
ccg update .  # ordinary source edits: re-index changed files
ccg lint      # verify annotation quality and references
```

Use `ccg build .` instead when the graph does not exist, a full rebuild is
intentional, or incremental recovery is required.

## Quality Rules (this is what really matters)

❌ **Bad annotation**:

```go
// @intent creates a user
func CreateUser(...) {}
```

WHAT only. Function name already tells you that.

✅ **Good annotation**:

```go
// @intent register a normalized account for the onboarding flow
// @domainRule email must be unique across all tenants
// @sideEffect inserts the user and audit record, then sends a verification email
// @mutates input.NormalizedEmail
func CreateUser(input *SignupRequest) error { ... }
```

WHY + business rules + side effects. Code and documentation search become powerful.

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
ccg search "graph viewer ccg ref node focus"
```

For MCP/Web UI retrieval, use `search_docs` + `get_doc_content` or Wiki search. If the
expected file is missing, prefer improving the precise `@index`, `@intent`,
`@domainRule`, or `@see` evidence on that file over changing global scoring.

## MCP Tools

| Tool             | Use                                  |
| ---------------- | ------------------------------------ |
| `get_annotation` | Fetch annotation/doc tags for a node |

## Completion

List annotated files and meaningful tags added or deliberately revised, report
any existing rationale changed, refresh the graph with update or build as
appropriate, run retrieval probes for the affected concepts, and report
`ccg lint` results without claiming unrelated findings were fixed.
