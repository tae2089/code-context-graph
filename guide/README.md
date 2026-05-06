# Guide

Documentation index for code-context-graph.

For LLM-agent workflows, start natural-language code exploration from generated
docs and the RAG index. Use graph/search tools after the relevant module,
document, or symbol candidate is identified.

The browser Wiki is served by `ccg-server` when `--wiki-dir` points at built
React assets. It prefers the graph database for presentation, uses
`wiki-index.json` and `doc-index.json` as compatibility snapshots, and uses
`/wiki/api/graph` for the visual graph tab. Runtime retrieve mode prefers the
database when it is configured and queryable. Use it when a human developer
needs to browse docs, inspect annotation-rich symbol cards, collect Context Tray
Markdown, or visually explore graph edges.

| Document | Description |
|----------|-------------|
| [CLI Reference](cli-reference.md) | Full CLI commands, options, and configuration file (`.ccg.yaml`) |
| [Eval](eval.md) | Parser/search quality evaluation, golden corpus, and metrics |
| [Lint](lint.md) | Detailed `ccg lint` category reference, interpretation guide, and CI usage |
| [MCP Tools](mcp-tools.md) | 33 MCP tools, agent skills, RAG-first routing, AI-driven annotation |
| [Annotations](annotations.md) | Custom annotation system — tags, examples, search/RAG quality |
| [Webhook](webhook.md) | GitHub / Gitea webhook sync, branch filtering, graceful shutdown |
| [Docker](docker.md) | Docker image build, MCP server setup, Wiki UI deployment, PostgreSQL integration |
| [Operations](operations.md) | Deployment profiles, database choice, readiness, webhook operations, troubleshooting |
| [Postprocess Failure Policy](postprocess-failure-policy.md) | Status rules, failure causes, and automatic degraded/fail_closed policy for build and postprocess tools |
| [Runtime Layout](runtime-layout.md) | `ccg`, `ccg-server`, Wiki serving, and shared `ccg-core` ownership boundaries |
| [Architecture](architecture.md) | System architecture, data flow, DB schema |
| [Development](development.md) | Build, test, integration test (Gitea + PostgreSQL) |
| [Namespace Migration](namespace-migration.md) | Default namespace change and migration guide |
| [CLAUDE.md Guide](claude-md-guide.md) | CLAUDE.md template for projects using CCG |
