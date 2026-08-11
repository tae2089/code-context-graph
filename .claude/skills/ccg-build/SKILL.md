---
name: ccg-build
description: "Explicit-only graph ingestion for building, updating, migrating, and postprocessing code-context-graph graphs from source visible to the CCG runtime. Use only when the user explicitly names the ccg-build skill in the current request. Do not invoke merely because a graph is missing or stale, annotations need reindexing, or another workflow would benefit from a refresh."
disable-model-invocation: true
---

# CCG Build Runtime Adapter

Proceed only when the user explicitly names `ccg-build` in the current request.
Read the [canonical CCG build skill](../../../skills/ccg-build/SKILL.md)
completely before acting. Resolve its linked resources relative to the canonical
skill directory and follow its ingestion boundary, safety, and completion
instructions for the current task.
