# Lint Guide

Detailed reference for `ccg lint` categories, how the report is computed, and how to interpret the results.

## Overview

`ccg lint` cross-checks three things:

1. Generated Markdown docs under the configured docs output directory
2. Source files and symbols already stored in the graph database
3. Structured annotations attached to graph symbols

The command reports documentation coverage issues and annotation quality issues together.

## Quick Usage

```bash
# Check docs and annotation health
ccg lint

# Fail the command when strict-mode rules require it
ccg lint --strict

# Lint a non-default docs directory
ccg lint --out docs
```

For CLI flags and lint rules in `.ccg.yaml`, see [CLI Reference](cli-reference.md).

## How `ccg lint` Works

At a high level, the linter performs these steps:

1. Walk the docs output directory and collect Markdown files
2. Query graph nodes from the database and derive the source files currently represented in the graph
3. Cross-check docs against graph files
4. Load symbol annotations and their tags
5. Classify each issue into one of the lint categories

Notable implementation details:

- `index.md` is skipped because it is not treated as a per-file doc.
- `missing` and `stale` are file-level categories.
- `unannotated`, `contradiction`, `dead-ref`, `incomplete`, and `drifted` are symbol-level categories.
- Tests are included when computing graph-backed file docs coverage, but tests are excluded from `unannotated` symbol checks.

## Categories

`ccg lint` reports these categories.

### `orphan`

A documentation file exists, but the graph no longer contains a matching source file.

- Typical cause: code was deleted or moved, but the generated doc file remained.
- Practical meaning: the doc is now detached from the live codebase.
- Typical fix: regenerate docs or delete the stale doc file.

### `missing`

A source file exists in the graph, but there is no matching Markdown doc file.

- Typical cause: docs were never generated for that file, or the docs directory is incomplete.
- Practical meaning: file-level documentation coverage is missing.
- Typical fix: run docs generation for the current graph state.

### `stale`

A documentation file exists, but its modification time is older than the latest update time recorded for the corresponding source file in the graph.

- Typical cause: code changed after the last docs generation.
- Practical meaning: the doc may still exist, but it may no longer describe the current code accurately.
- Typical fix: regenerate docs after rebuilding or updating the graph.

### `unannotated`

A function, class, or type symbol has no annotation record at all.

- Typical cause: the symbol has no structured annotation such as `@intent`.
- Practical meaning: search and AI context can still use the code, but they lose the explicit human-written intent layer.
- Typical fix: add structured annotation comments above the declaration.

### `contradiction`

An annotation exists, includes a detail tag such as `@param`, and the code node was updated after that annotation was written.

- Typical cause: signature or behavior changed, but detailed annotation text was not refreshed.
- Practical meaning: the detailed annotation is likely unreliable.
- Typical fix: review the symbol and update the detailed annotation tags.

This is intentionally stricter than general drift because detail tags are more likely to become wrong when code changes.

### `dead-ref`

An `@see` tag points to a qualified name that does not exist in the graph.

- Typical cause: referenced symbol was renamed, removed, or moved.
- Practical meaning: cross-reference navigation is broken.
- Typical fix: update or remove the `@see` tag.

### `incomplete`

An annotation exists, but it does not contain an `@intent` tag.

- Typical cause: a doc comment was added without the intent annotation, or only partial tags were written.
- Practical meaning: the symbol has some documentation, but it is missing the most important structured explanation of why it exists.
- Typical fix: add `@intent` to the annotation block.

### `drifted`

An annotation exists, but the code node was updated after the annotation was written.

- Typical cause: code evolved and the annotation was not refreshed.
- Practical meaning: the annotation may be directionally correct, but it is no longer guaranteed to match the current implementation.
- Typical fix: review and refresh the annotation.

`drifted` is broader than `contradiction`. A symbol can be drifted even if it has no detailed tags such as `@param`.

## Category Relationships

Some categories overlap, and some do not.

| Category A | Category B | Relationship |
|---|---|---|
| `contradiction` | `drifted` | Every contradiction is also a kind of drift, but not every drift is a contradiction |
| `unannotated` | `incomplete` | Mutually exclusive in practice: incomplete means an annotation exists, unannotated means none exists |
| `missing` | `stale` | Mutually exclusive per file: a file cannot both lack a doc and have an out-of-date doc |

## File-Level vs Symbol-Level Categories

### File-level

- `orphan`
- `missing`
- `stale`

These categories are about whether generated Markdown files match the current graph-backed source files.

### Symbol-level

- `unannotated`
- `contradiction`
- `dead-ref`
- `incomplete`
- `drifted`

These categories are about the quality and freshness of structured annotations attached to symbols in the graph.

## Scope Modifiers

Lint results can be narrowed by graph scope and path filtering.

### Namespace scope

If lint runs with a namespace, node queries and `@see` resolution are scoped to that namespace.

Practical effect:

- `dead-ref` checks only look for referenced symbols inside the same namespace scope.
- file and symbol category counts can differ between a full-repo lint and a namespace-scoped lint.

### Exclude filters

Excluded file paths are skipped when collecting graph-backed source files and symbol candidates.

Practical effect:

- excluded files do not contribute to `missing`, `stale`, or annotation-related categories
- generated code or intentionally ignored paths can be filtered out through lint rules and config

## Field Mapping

The user-facing category labels map to `LintReport` fields like this:

| Category label | `LintReport` field |
|---|---|
| `orphan` | `Orphans` |
| `missing` | `Missing` |
| `stale` | `Stale` |
| `unannotated` | `Unannotated` |
| `contradiction` | `Contradictions` |
| `dead-ref` | `DeadRefs` |
| `incomplete` | `Incomplete` |
| `drifted` | `Drifted` |

If you are reading the implementation, the exact logic lives in `internal/docs/lint.go`.

## Interpreting Results in Practice

### If `missing` or `stale` is high

This usually means docs generation has not been run recently, or code changed after docs were generated.

Typical response:

1. rebuild or update the graph
2. regenerate docs
3. rerun lint

### If `unannotated` is high

This means the code is present, but structured intent metadata is still sparse.

Typical response:

1. prioritize high-value symbols first
2. add English annotations consistently
3. rerun lint to measure coverage improvement

### If `incomplete` is non-zero

This usually means comments were added, but `@intent` was forgotten.

Typical response:

1. locate the listed symbols
2. add `@intent`
3. rerun lint

### If `contradiction`, `dead-ref`, or `drifted` appears

These usually need manual review because they indicate stale or broken semantic metadata.

Typical response:

1. inspect the symbol
2. update or remove outdated tags
3. rerun lint

## Suppression and Policy

Per-category rules can be configured in `.ccg.yaml`.

Example:

```yaml
rules:
  - pattern: "pkg/store/.*"
    category: unannotated
    action: ignore
```

Generated lint state is stored separately from human policy:

- `.ccg.yaml` — manual lint policy
- `.ccg/lint-history.json` — generated occurrence counters
- `.ccg/auto-rules.yaml` — generated warn-only rules

See [CLI Reference](cli-reference.md#lint-policy-vs-generated-state) for the exact rule flow.

## CI and Strict Mode

`ccg lint --strict` is intended for CI or pre-commit enforcement.

Typical uses:

- fail CI on documentation regressions
- block commits when important lint categories exceed policy
- keep generated docs and annotations in sync over time

## See Also

- [CLI Reference](cli-reference.md#lint-categories)
- [Custom Annotations](annotations.md)
- [Development](development.md)
- [Postprocess Failure Policy](postprocess-failure-policy.md)
