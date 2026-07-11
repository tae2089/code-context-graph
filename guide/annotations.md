# Custom Annotations

Add structured metadata to your code so that AI, generated docs, and focused
search can leverage business context. Annotations are indexed for `ccg search`
and are also rendered into generated Markdown.

For LLM-agent natural-language exploration, prefer the generated-docs path first:
`ccg docs`, then use MCP `search_docs` to find relevant docs and `get_doc_content`
to read one. Use `ccg search` when you
need a focused list of annotation/keyword-matched symbol candidates.

Annotation quality is validated by `ccg lint`. For category meanings such as `unannotated`, `incomplete`, `dead-ref`, `contradiction`, and `drifted`, see [Lint Guide](lint.md).

## File-level

```go
// @index User authentication and session management service.
package auth
```

## Function-level

```go
// AuthenticateUser validates credentials and creates a session.
// Called from login API handler.
//
// @param username user login ID
// @param password plaintext password
// @return JWT token on success
// @intent verify user identity before granting system access
// @domainRule lock account after 5 consecutive failed attempts
// @sideEffect writes login attempt to audit_log table
// @mutates user.FailedAttempts, user.LockedUntil
// @requires user.IsActive == true
// @ensures err == nil implies valid JWT with 24h expiry
func AuthenticateUser(username, password string) (string, error) {
```

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
| `@see` | Related function or CCG ref | `@see SessionManager.Create`, `@see ccg://auth-svc/internal/auth/token.go#ValidateToken` |

`@intent` is especially important because a symbol with annotations but no `@intent` is reported as `incomplete` by `ccg lint`. `@see` tags are also linted and can produce `dead-ref` findings if they point to non-existent symbols.

Use `ccg://{namespace}/{path}#{symbol}` in `@see` when a behavior depends on code in another namespace. Keep the reason in the semantic tag and the concrete target in `@see`:

```go
// @sideEffect records token validation audit in auth-svc.
// @see ccg://auth-svc/internal/audit/token_audit.go#RecordTokenAudit
```

The path and symbol are optional, so `ccg://auth-svc/internal/auth` can point at a package/path scope and `ccg://auth-svc/` can point at a whole namespace.

## Retrieval Quality

Annotations are retrieval features. `search_docs` and generated docs rank
file-level evidence from structured buckets such as `@index`, `@intent`,
`@domainRule`, `@sideEffect`, `@requires`, `@ensures`, and `@see`. Better
annotations make natural-language retrieval more precise, but only when the
tags describe real behavior.

Use tags by need:

| Situation | Preferred tags |
|-----------|----------------|
| File/package should be discoverable as a unit | `@index` |
| Public function, handler, CLI command, service method, or UI workflow has a clear purpose | `@intent` |
| Policy, constraint, false-positive/false-negative criterion, or operational rule matters | `@domainRule` |
| Code writes DB/files, calls network, changes cache, logs important events, or starts processes | `@sideEffect` |
| Receiver or input state is changed | `@mutates` |
| Input contract or output guarantee matters to callers | `@requires`, `@ensures` |
| Understanding requires jumping to another implementation or namespace | `@see` |

Good retrieval annotations use the words an engineer or LLM would naturally ask
for, while staying faithful to the code. For example, if a UI graph component
focuses a resolved `ccg://` node, the annotation should say so. Do not add
unrelated keywords just to increase score; broad or false terms make unrelated
files outrank the real implementation.

Avoid over-annotation:

- Skip trivial getters, setters, tiny wrappers, and obvious one-line helpers.
- Do not repeat the same keyword across many tags unless each tag adds distinct
  evidence.
- Prefer one accurate `@domainRule` over several vague `@intent` lines.
- Keep `@see` for real navigation edges, especially cross-namespace references.

## AI-Driven Annotation

Coding agents with the `/ccg-annotate` skill can analyze your codebase and
generate annotations:

```
You: "Add annotations to this project"
Agent: reads code → generates @intent, @domainRule, @sideEffect, @mutates
       → writes annotations → rebuilds index
       → now searchable by business context
```

### Skill

Use directly in a skill-capable coding agent with the `/ccg-annotate` skill:

```
/ccg-annotate annotate internal/   — AI-generate annotations
```

The project-local skill keeps its complete tag contract and comment-syntax
examples in [`skills/ccg-annotate/references/annotation-reference.md`](../skills/ccg-annotate/references/annotation-reference.md).
