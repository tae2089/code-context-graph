# Annotation Reference

Use this reference when choosing a less-common tag, checking syntax, or adapting annotations to a supported language.

## Tag Contracts

| Tag | Contract |
| --- | -------- |
| `@index <description>` | File or package role used for module-level discovery |
| `@intent <reason>` | Why a meaningful symbol or workflow entry point exists |
| `@domainRule <rule>` | Business, policy, operational, or correctness rule implemented or required by the symbol |
| `@sideEffect <effect>` | External I/O or observable effect such as DB, file, network, cache, log, or process mutation |
| `@mutates <target>` | Receiver or argument state directly modified in memory; use `@sideEffect` for DB, file, network, cache, log, or process changes |
| `@requires <condition>` | Caller-visible precondition |
| `@ensures <condition>` | Guarantee on successful return |
| `@param <name> <description>` | Parameter contract; JSDoc `{Type}` and YARD `[Type]` prefixes are accepted |
| `@return <description>` | Return contract; `@returns` is an alias and optional `{Type}` or `[Type]` prefixes are accepted |
| `@throws <description>` | Error/exception contract; `@exception` is an alias |
| `@typedef <description>` | Named type documentation carried into generated docs/search evidence |
| `@see <target>` | Related qualified symbol, `file::symbol`, or `ccg://namespace/path#symbol` reference |

The parser implementation in `internal/annotation/parser.go` is the source of truth for recognized tags and aliases.

## Comment Syntax

Write annotations in the language's ordinary documentation comments immediately above the declaration. CCG strips the comment prefix before parsing.

Go, JavaScript/TypeScript, Java/Kotlin, C/C++, Rust, and PHP:

```go
// @intent authorize checkout before payment capture
// @domainRule suspended accounts cannot submit orders
// @sideEffect writes the authorization decision to audit_log
func AuthorizeCheckout() error { return nil }
```

Python and Ruby:

```python
# @intent authorize checkout before payment capture
# @domainRule suspended accounts cannot submit orders
def authorize_checkout(...):
    ...
```

Lua/Luau:

```lua
-- @intent authorize checkout before payment capture
-- @domainRule suspended accounts cannot submit orders
local function authorizeCheckout(...)
end
```

## Multiline Form

Text before the first tag becomes the summary and context paragraphs. A tag value continues on following non-empty, non-tag lines.

```go
// Validates a checkout authorization request.
//
// Used by both synchronous checkout and retry workers.
//
// @param request request submitted by the checkout workflow
//   and normalized before policy evaluation
// @return the authorization decision and audit identifier
```

Unknown tags are reported by the parser and are not stored. Keep annotations truthful and omit tags that merely repeat the declaration name.
