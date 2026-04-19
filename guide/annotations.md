# Custom Annotations

Add structured metadata to your code so that AI and search can leverage business context. Annotations are indexed and searchable via `ccg search`.

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
| `@see` | Related function | `@see SessionManager.Create` |

## AI-Driven Annotation

Claude Code can analyze your codebase and automatically generate annotations:

```
You: "Add annotations to this project"
Claude: reads code → generates @intent, @domainRule, @sideEffect, @mutates
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

Use directly in Claude Code with the `/ccg-annotate` skill:

```
/ccg-annotate annotate internal/   — AI-generate annotations
```
