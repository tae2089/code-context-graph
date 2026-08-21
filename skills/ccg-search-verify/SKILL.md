---
name: ccg-search-verify
description: "Verify read-only code absence and completeness claims with bounded source and code-context-graph evidence. Use when the user asks whether code does not exist, requests exhaustive or complete inventory, or needs a defensible negative result. Do not use for ordinary positive lookup or explanation, deep flow or impact analysis, annotation authoring, documentation generation, or graph writes."
metadata:
  version: 1.0.0
  openclaw:
    category: "code-intelligence"
    domain: "verification"
  requires:
    bins:
      - ccg
  cliHelp: "ccg search --help"
---

# ccg-search-verify — Search Verification

Use this skill only when the answer depends on proving absence, completeness,
or exhaustive coverage. It is read-only and intentionally more expensive than
the ordinary `ccg` fast path.

## Mandatory Verification

Read [`references/search-contract.md`](references/search-contract.md)
completely before searching or making the claim. Follow its source pass, single
CCG query, freshness, and exact paging requirements without substituting an
ordinary fast-search result.

Reuse a namespace and server-visible repository path already established by
the user or repository instructions. Call `list_namespaces` only when the
namespace is genuinely unknown. Call `get_minimal_context` only when the MCP
tool contract needed for the verification is unavailable; it is not a routine
preflight.

## Boundary

- Do not invoke `ccg-analyze`. A deep relationship, flow, or impact workflow
  still requires the user to explicitly name that skill.
- Do not invoke `ccg-build` when freshness is missing or stale. Report the gap;
  graph writes require the user to explicitly name `ccg-build`.
- Do not fall back to the ordinary `ccg` fast path after verification has been
  triggered.

## Completion

State the conclusion at the strength the evidence permits. Report the checked
namespace, freshness evidence or gap, source and CCG query shapes, limit,
truncation state, and every skill or resource path read. Name any skipped check
that prevents an unqualified absence or completeness claim.
