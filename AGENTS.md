# code-context-graph

A local code analysis tool that parses codebases with Tree-sitter and builds a knowledge graph.

## MCP Server

The ccg MCP server registered in `.mcp.json` provides 33 tools:

- `parse_project`, `build_or_update_graph`, `run_postprocess`
- `get_postprocess_policy`, `reset_postprocess_policy`
- `get_node`, `search`, `query_graph`, `list_graph_stats`, `get_minimal_context`
- `get_impact_radius`, `trace_flow`
- `find_large_functions`, `find_dead_code`
- `detect_changes`, `get_affected_flows`, `list_flows`
- `list_communities`, `get_community`, `get_architecture_overview`
- `get_annotation`
- `build_rag_index`, `get_rag_tree`, `get_doc_content`, `search_docs`, `retrieve_docs`
- `upload_file`, `upload_files`, `list_namespaces`, `list_files`, `delete_file`, `delete_namespace`

`ccg serve` is the local stdio MCP entry point. Self-hosted HTTP mode is provided by
the separate `ccg-server` binary, which serves `/mcp`, `/health`, `/ready`, `/status`,
and `/webhook`.
Webhooks are enabled in `ccg-server` when allowed repositories are configured with the `--allow-repo` flag.
Per-repository branch filtering: `--allow-repo "org/api:main,develop"` (glob patterns; defaults to main/master when omitted).
Compatible with GitHub (`X-Hub-Signature-256`) and Gitea (`X-Gitea-Signature`, `X-Gitea-Event`).
Push event pipeline: receive push event -> automatic clone/pull -> graph build -> DB persistence.
Graceful shutdown: SIGINT/SIGTERM propagates context cancellation to in-progress clone/build work.

## Agent Skills (5)

| Skill            | Description                                                                                 |
| ---------------- | ------------------------------------------------------------------------------------------- |
| `/ccg`           | Core build and search: parsing, graph build, query, search                                   |
| `/ccg-analyze`   | Code analysis: impact radius, flow tracing, dead code, architecture                          |
| `/ccg-annotate`  | Annotation system: AI annotation workflow and tag reference                                  |
| `/ccg-docs`      | Documentation: doc generation, RAG indexing, lint                                            |
| `/ccg-namespace` | Namespace file management: upload, list, delete                                              |

Skill files are located under `skills/` and are written so coding agents such as Codex and Claude Code
can use them as slash-command style workflows.

Main commands:

- `ccg build [dir]` - build the code graph (supports `--exclude`, `--no-recursive`)
- `ccg serve` - start the local MCP server over stdio
- `ccg-server` - start the self-hosted HTTP MCP/webhook server
- `ccg docs [--out dir]` - generate Markdown documentation and the default RAG index
- `ccg rag-index [--out dir]` - build a RAG index from precomputed communities and generated docs
- `ccg search <query>` - full-text search (includes annotations)
- `ccg lint [--strict]` - check documentation quality
- `/ccg-annotate annotate [file|dir]` - AI annotation generation workflow
- `ccg example [language]`, `ccg tags` - annotation examples and tag reference

Use `.ccg.yaml` to manage project defaults such as exclude patterns and DB settings.

## Code Search Rules

When looking for code locations, related implementations, call relationships, impact radius, or architecture context,
use ccg MCP tools and Agent Skills first.

- For natural-language code understanding, module exploration, and architecture context, prefer the `/ccg-docs` skill and `retrieve_docs`, `get_rag_tree`, `get_doc_content`.
- For exact symbol locations, call relationships, and graph metadata, use ccg MCP `query_graph`, `get_node`, `get_minimal_context`, or the `/ccg` skill.
- For annotation/keyword-based candidate search, use ccg MCP `search` or `ccg search` as a supplement.
- For impact radius, flows, dead code, and structural analysis, prefer the `/ccg-analyze` skill and related MCP tools (`get_impact_radius`, `trace_flow`, `find_dead_code`, `get_architecture_overview`).
- For simple string checks, file existence checks, or cases where the ccg index is missing or stale, use `rg` as a supplement. If needed, refresh the graph with `ccg build .` or `ccg update .`.

## Documentation

See the `guide/` directory for detailed documentation:

- [CLI Reference](guide/cli-reference.md) - all commands, flags, and config files
- [MCP Tools](guide/mcp-tools.md) - 33 MCP tools, Agent Skills, AI-Driven Annotation
- [Annotations](guide/annotations.md) - annotation tags, examples, and search
- [Webhook](guide/webhook.md) - webhook sync, branch filtering, HMAC, graceful shutdown
- [Docker](guide/docker.md) - Docker builds, MCP server, PostgreSQL deployment
- [Development](guide/development.md) - development guide, integration tests, project structure
- [Runtime Layout](guide/runtime-layout.md) - `ccg`, `ccg-server`, and shared `ccg-core` ownership boundaries
- [Architecture](guide/architecture.md) - data flow, components, DB schema

## Development Rules

- TDD: Red -> Green -> Refactor
- Tidy First: separate structural changes from behavioral changes
- Use GORM queries only (no raw SQL)
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

After completing code generation or code modifications, run the following by default:

```bash
ccg build .
ccg docs --out docs
ccg lint
```

If the change modifies behavior or touches DB/search/parser/MCP handlers, also run Go tests:

```bash
CGO_ENABLED=1 go test -tags "fts5" ./... -count=1
```

For documentation-only changes, prioritize regenerating docs with `ccg docs` and running `ccg lint`.
Code tests may be skipped depending on the change scope.
