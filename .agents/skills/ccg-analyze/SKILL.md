---
name: ccg-analyze
description: "Explicit-only deep analysis of algorithms, feature pipelines, and code relationships with CCG impact radius, bounded flow tracing, callers/callees, git-diff risk, affected stored flows, and cross-namespace references. Use only when the user explicitly names the ccg-analyze skill in the current request. Do not invoke merely because a task asks about a flow, pipeline, impact, caller, or relationship."
disable-model-invocation: true
---

# CCG Analyze Runtime Adapter

Proceed only when the user explicitly names `ccg-analyze` in the current
request. Read the [canonical CCG analyze skill](../../../skills/ccg-analyze/SKILL.md)
completely before acting. Resolve its linked resources relative to the canonical
skill directory and follow its analysis boundary and completion instructions
for the current task.
