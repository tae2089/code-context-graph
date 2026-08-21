# code-context-graph

A local code analysis tool that parses codebases with Tree-sitter and builds a knowledge graph.

## MCP Server

The ccg MCP server registered in `.mcp.json` provides 18 tools:

- `parse_project`, `build_or_update_graph`, `run_postprocess`
- `get_node`, `search`, `describe`, `query_graph`, `list_graph_stats`, `list_namespaces`, `get_minimal_context`
- `get_impact_radius`, `trace_flow`, `list_cross_refs`
- `detect_changes`, `get_affected_flows`, `list_flows`
- `get_annotation`
- `get_doc_content`

`ccg serve` is the local stdio MCP entry point. Self-hosted HTTP mode is provided by
the separate `ccg-server` binary, which serves `/mcp`, `/health`, `/ready`, `/status`,
and `/webhook`.
Webhooks are enabled in `ccg-server` when allowed repositories are configured with the `--allow-repo` flag.
Per-repository branch filtering: `--allow-repo "org/api:main,develop"` (glob patterns; defaults to main/master when omitted).
Compatible with GitHub (`X-Hub-Signature-256`) and Gitea (`X-Gitea-Signature`, `X-Gitea-Event`).
Push event pipeline: receive push event -> automatic clone/pull -> graph build -> DB persistence.
Graceful shutdown: SIGINT/SIGTERM propagates context cancellation to in-progress clone/build work.

## Agent Skills (7)

| Skill            | Description                                                         |
| ---------------- | ------------------------------------------------------------------- |
| `/ccg`           | Fast read-only discovery and targeted source verification           |
| `/ccg-search-verify` | Absence, completeness, and exhaustive search verification       |
| `/ccg-build`     | Explicit-only graph build, update, migrate, and postprocess         |
| `/ccg-analyze`   | Explicit-only impact, flow tracing, and change-risk analysis        |
| `/ccg-annotate`  | Annotation system: AI annotation workflow and tag reference         |
| `/ccg-docs`      | Documentation: generation, DB-backed discovery, and lint            |
| `/ccg-namespace` | Namespace isolation for multi-project graph data                    |

Skill files are located under `skills/` and are written so coding agents such as Codex and Claude Code
can use them as slash-command style workflows.

Main commands:

- `ccg build [dir]` - build the code graph (supports `--exclude`, `--no-recursive`)
- `ccg serve` - start the local MCP server over stdio
- `ccg-server` - start the self-hosted HTTP MCP/webhook server
- `ccg docs [--out dir]` - generate Markdown documentation and the Wiki compatibility index
- `ccg search <query>` - full-text search (includes annotations)
- `ccg lint [--strict]` - check documentation quality
- `/ccg-annotate annotate [file|dir]` - AI annotation generation workflow

Use `.ccg.yaml` to manage project defaults such as exclude patterns and DB settings.

## Code Search Rules

When looking for code locations, related implementations, call relationships, impact radius, or architecture context,
use ccg MCP tools and Agent Skills first.

- `/ccg` is the fast default for ordinary positive discovery: use at most one `search` call with `limit: 5`, then verify the best candidate in one or two source ranges. Skip namespace, minimal-context, and graph-stat preflights when repository instructions already provide what the query needs.
- Use `/ccg-search-verify` when the user asks whether code does not exist, requests completeness or exhaustive inventory, or when a miss would become a defensible negative claim. It owns freshness, hybrid source checking, and truncation paging.
- CCG `search` answers identifier queries and "why was this built" questions from one index. Use the `/ccg-docs` skill and `get_doc_content` to read a generated doc.
- For exact symbol locations and one direct call relationship, use ccg MCP `query_graph`, `get_node`, or the `/ccg` skill. Use `get_minimal_context` only when the MCP tool contract needed for the task is unavailable; it is not an ordinary search preflight.
- `/ccg-analyze` is explicit-only: use it only when the user names that skill in the current request. A request about impact, flow, callers, or relationships is not permission to load it or expand into its traversal workflow; keep ordinary `/ccg` discovery bounded instead.
- For simple string checks, file existence checks, or cases where the ccg index is missing or stale, use `rg` as a supplement. Report missing or stale graph state without invoking `/ccg-build` automatically.
- `/ccg-build` is explicit-only: use it only when the user names that skill in the current request. A task that needs graph creation, refresh, migration, or postprocessing is not by itself permission to load the skill. Remote ingestion stays Git webhook/sync based; do not treat MCP write tools as source-upload APIs.

## Documentation

See the `guide/` directory for detailed documentation:

- [CLI Reference](guide/cli-reference.md) - all commands, flags, and config files
- [MCP Tools](guide/mcp-tools.md) - 18 MCP tools, Agent Skills, AI-Driven Annotation
- [Annotations](guide/annotations.md) - annotation tags, examples, and search
- [Webhook](guide/webhook.md) - webhook sync, branch filtering, HMAC, graceful shutdown
- [Docker](guide/docker.md) - Docker builds, MCP server, PostgreSQL deployment
- [Development](guide/development.md) - development guide, integration tests, project structure
- [Runtime Layout](guide/runtime-layout.md) - `ccg`, `ccg-server`, and shared `ccg-core` ownership boundaries
- [Architecture](guide/architecture.md) - data flow, components, DB schema

## Development Rules

- TDD: Red -> Green -> Refactor
- Tidy First: separate structural changes from behavioral changes
- Use GORM's model layer for queries. Raw SQL is allowed only where GORM has no
  form for the statement — full-text operators, FTS5 virtual-table DDL and
  writes, schema introspection its migrator cannot do, connection pragmas — and
  only inside `internal/adapters/outbound/searchsql` and `internal/db`.
  Identifiers come from package constants, values are always bound parameters.
  Details and the evidence for each exemption: `guide/development.md` §Raw SQL
- Tests: `CGO_ENABLED=1 go test -tags "fts5" ./... -count=1`
- Integration test: `./scripts/integration-test.sh` (full Gitea + PostgreSQL + ccg Docker pipeline)

## Code Writing Rules

When creating new code or making meaningful behavior changes to existing code, add CCG annotations as well.

Priority:

- Use `// @index ...` when the package/file role should be discoverable.
- Use `// @intent ...` for new public types/functions/methods, MCP handlers, CLI commands, and service methods.
- Use `// @param`, `// @return` when input/output contracts matter.
- Use `// @requires`, `// @ensures` when preconditions or guarantees matter.
- Use `// @sideEffect` when the code mutates external state such as files, DB, network, cache, logs, or processes.
- Use `// @mutates` when the receiver or argument values are modified.
- Use `// @domainRule` for business rules, operational policies, and false-positive/false-negative criteria.
- Use `// @see` when related handlers, services, or models exist.

Annotations must match the code behavior and should not exaggerate the explanation.
Do not force annotations onto simple getters/setters or obvious one-line helpers.

## Completion Checklist

Graph refresh is opt-in. Run `ccg build .` only when the user explicitly invokes
`ccg-build`; otherwise report it as an optional follow-up without executing it:

```bash
ccg build .
```

Generate and lint docs when the existing graph is current. If graph refresh was
not explicitly authorized, report stale-graph dependent verification as skipped.

```bash
ccg docs --out docs
ccg lint
```

If the change modifies behavior or touches DB/search/parser/MCP handlers, also run Go tests:

```bash
CGO_ENABLED=1 go test -tags "fts5" ./... -count=1
```

For documentation-only changes, prioritize regenerating docs with `ccg docs` and running `ccg lint`.
Code tests may be skipped depending on the change scope.

## Skill Routing

- When writing, modifying, or reviewing code, apply `coding-quality-guardrails` as the quality gate.
- When debugging bugs, regressions, flaky behavior, or failing tests, use `diagnosing-bugs` before changing behavior.
- Before implementing new logic with branching, side effects, resource lifecycles, or ordering constraints, use `flow-design` and keep the design note in the task workspace.
- When designing module boundaries, refactoring, or shaping interfaces, use `codebase-design`.
- When aligning terminology or modeling the domain, use `domain-modeling`.
- When a plan is fuzzy, high-impact, or lacks testable acceptance criteria, use `planning-grill` to sharpen scope, acceptance, and failure modes before decomposing it.
- For multi-step or multi-agent work, use `decompose-and-dispatch` to split the work into bounded units. Use `execute-dispatch-unit` only for a clearly assigned unit with scope, dependencies, and verification.
- When preparing context for human or AI code review, use `ready-code-review`; do not use it to perform the review itself.
- To record a session, distill completed work into a replayable recipe, or replay a `recipe.yaml`, use `session-recipe`.

## agent-team Routing

agent-team bundles its own skills; restrict them as follows so methodology stays single-sourced:

- Use only agent-team's CLI operation skills (the `agent-team-*` prefix: run/task/message/inbox/sync/event commands), and load `agent-team-shared` before any command-specific one — it defines the state directory, global flags, and error handling they all assume. Never use its `recipe-*` and `persona-*` skills — the skills routed above own all methodology, even where an excluded skill looks like a closer match (worker checkpoints → `execute-dispatch-unit`'s Ledger Checkpoints; plan sharpening / `recipe-agent-team-planning-grill` → `planning-grill`; decomposition → `decompose-and-dispatch`; architecture → `codebase-design`; terminology → `domain-modeling`).
- When executing an assigned unit, follow `execute-dispatch-unit` for scope, verification, and reporting; its Ledger Checkpoints section defines which `agent-team-*` calls to make.
- When planning, `decompose-and-dispatch` owns decomposition and executor mapping, and its Durable Ledger section defines the run/task registration calls.
- Do not route by the word "recipe": here it means a replayable session recipe (`session-recipe`, `recipe.yaml`); agent-team's `recipe-*` skills are excluded above.
