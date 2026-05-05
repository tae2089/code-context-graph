# Custom Annotations

Add structured metadata to your code so that AI, generated docs, RAG, and
focused search can leverage business context. Annotations are indexed for
`ccg search` and are also rendered into generated Markdown that feeds the RAG
index.

For LLM-agent natural-language exploration, prefer the docs/RAG path first:
`ccg docs`, then MCP `retrieve_docs`, `get_rag_tree`, and `get_doc_content`.
Use `ccg search` when you need a focused list of
annotation/keyword-matched symbol candidates.

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

## AI-Driven Annotation

Coding agents with the `/ccg-annotate` skill can analyze your codebase and
generate annotations:

```
You: "Add annotations to this project"
Agent: reads code → generates @intent, @domainRule, @sideEffect, @mutates
       → writes annotations → rebuilds index
       → now searchable by business context
```

### CLI

```bash
# Show annotation examples
ccg example go
ccg example python

# Full tag reference
ccg tags
```

### Skill

Use directly in a skill-capable coding agent with the `/ccg-annotate` skill:

```
/ccg-annotate annotate internal/   — AI-generate annotations
```
