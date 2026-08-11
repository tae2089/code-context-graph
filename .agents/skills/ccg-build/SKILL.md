---
name: ccg-build
description: "Build, update, migrate, and postprocess code-context-graph graphs from source visible to the CCG runtime. Use when a graph is missing or stale, source annotations must be reindexed, a namespace needs Git-backed synchronization, or a scoped graph write needs explicit replacement semantics. Do not use for ordinary read-only search, lookup, or analysis when the existing graph is sufficient; use the ccg skill instead."
disable-model-invocation: true
---

# CCG Build Runtime Adapter

Read the [canonical CCG build skill](../../../skills/ccg-build/SKILL.md)
completely before acting. Resolve its linked resources relative to the canonical
skill directory and follow its ingestion boundary, safety, and completion
instructions for the current task.
