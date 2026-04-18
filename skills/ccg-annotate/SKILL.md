---
name: ccg-annotate
description: code-context-graph — annotation system. AI-driven annotation workflow, tag reference, and annotation search.
---

# code-context-graph — Annotation System

AI-driven annotation workflow for adding structured metadata to code. Annotations are indexed and searchable via FTS.

## Subcommands

| Command | Description | Example |
|---------|-------------|---------|
| `annotate [file\|dir]` | AI-generate annotations for code | `ccg annotate internal/analysis/` |
| `example [language]` | Show annotation writing example | `ccg example go` |
| `tags` | Show all annotation tag reference | `ccg tags` |

## MCP Tools (1)

| Tool | Description |
|------|-------------|
| `get_annotation` | Get annotation and doc tags for a specific node |

## Available Tags

| Tag | Purpose | Example |
|-----|---------|---------|
| `@index` | File/package description | `@index Payment processing service` |
| `@intent` | Why this function exists | `@intent verify credentials before session creation` |
| `@domainRule` | Business rule | `@domainRule lock account after 5 failures` |
| `@sideEffect` | Side effects | `@sideEffect sends notification email` |
| `@mutates` | State changes | `@mutates user.FailedAttempts, session.Token` |
| `@requires` | Precondition | `@requires user.IsActive == true` |
| `@ensures` | Postcondition | `@ensures session != nil` |
| `@param` | Parameter description | `@param username the login ID` |
| `@return` | Return description | `@return JWT token on success` |
| `@see` | Related function | `@see SessionManager.Create` |

## AI Annotation Workflow

`ccg annotate` is NOT a CLI binary command — it is an AI-driven workflow executed by Claude.

When the user runs `ccg annotate [file|dir]`, Claude should:

### Step 1: Read target files
- If a file path is given, read that file
- If a directory is given, find all source files (`.go`, `.py`, `.ts`, `.java`, etc.)
- Skip test files, vendor, node_modules

### Step 2: Analyze each function/class/file
For each declaration, read the code and determine:
- **What it does** (→ summary line above declaration)
- **Why it exists** (→ `@intent`)
- **Business rules it enforces** (→ `@domainRule`)
- **Side effects** (→ `@sideEffect`: DB writes, API calls, file I/O, notifications)
- **What state it changes** (→ `@mutates`: fields, tables, caches)
- **Prerequisites** (→ `@requires`: auth, valid input, active state)
- **Guarantees** (→ `@ensures`: return conditions, post-state)
- **File/package purpose** (→ `@index` on package declaration)

### Step 3: Write annotations
- Add annotations as comments directly above the declaration
- Use the language's comment syntax (`//` for Go, `#` for Python, etc.)
- Do NOT overwrite existing annotations — only add missing ones
- Do NOT add trivial annotations (e.g., `@intent returns the name` for `getName()`)

### Step 4: Rebuild
After annotating, run `ccg build .` to re-index with new annotations.

## Annotation Quality Rules

- `@intent` should describe WHY, not WHAT (not "creates user" but "register new account for onboarding flow")
- `@domainRule` should be specific business logic, not generic validation
- `@sideEffect` only for actual side effects (DB, network, file, notification)
- `@index` should summarize the module's responsibility in one line
- Skip getters/setters/trivial functions — annotate functions with business meaning
- Write annotations in the same language as existing code comments (Korean if Korean, English if English)

## Example Output

```go
// @index User authentication and session management service.
package auth

// AuthenticateUser validates credentials and creates a session.
// Called from login API handler.
//
// @param username user login ID
// @param password plaintext password (hashed internally)
// @return JWT token on success
// @intent verify user identity before granting system access
// @domainRule lock account after 5 consecutive failed attempts
// @domainRule locked accounts auto-unlock after 30 minutes
// @sideEffect writes login attempt to audit_log table
// @sideEffect sends security alert email on 3rd failed attempt
// @mutates user.FailedAttempts, user.LockedUntil, user.LastLoginAt
// @requires user.IsActive == true
// @ensures err == nil implies valid JWT with 24h expiry
func AuthenticateUser(username, password string) (string, error) {
```

## Searching Annotations

Annotations are indexed in FTS and searchable via `ccg search` (see `/ccg` skill):
- `@intent` — function purpose/goal
- `@domainRule` — business rules
- `@sideEffect` — side effects
- `@mutates` — state changes
- `@index` — file/package level description

Example: user asks "결제 관련 코드" → `ccg search "결제"` finds functions annotated with payment-related @intent/@domainRule.
