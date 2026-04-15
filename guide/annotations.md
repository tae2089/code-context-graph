# Custom Annotations

코드에 구조화된 메타데이터를 추가하여 AI와 검색에서 비즈니스 컨텍스트를 활용합니다. 어노테이션은 인덱싱되어 `ccg search`로 검색 가능합니다.

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

Claude Code가 코드베이스를 분석하여 자동으로 어노테이션을 생성할 수 있습니다:

```
You: "이 프로젝트에 어노테이션 달아줘"
Claude: reads code → generates @intent, @domainRule, @sideEffect, @mutates
      → writes annotations → rebuilds index
      → now searchable by business context
```

### CLI

```bash
# 어노테이션 예시 보기
ccg example go
ccg example python

# 전체 태그 레퍼런스
ccg tags
```

### Skill

`/ccg-annotate` 스킬로 Claude Code에서 바로 사용:

```
/ccg-annotate annotate internal/   — AI-generate annotations
```
