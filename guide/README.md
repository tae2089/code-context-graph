# Guide

Documentation index for code-context-graph.

For LLM-agent workflows, start natural-language code exploration with
`find_by_intent`: ask why something was built, then walk from the `node_id` it
returns, or read a generated doc with `get_doc_content`. Treat it as an
evidence-driven narrowing layer, not a Top1 search engine: use the small
file-level answer to choose the shortest route into graph tools.

The browser Wiki is served by `ccg-server` when `--wiki-dir` points at built
React assets. It prefers the graph database for presentation, uses
`wiki-index.json` as a compatibility tree snapshot, and uses `/wiki/api/graph`
for the visual graph tab. Runtime documentation search uses DB-backed graph and
annotation evidence. Use the Wiki when a human developer needs to browse docs,
inspect annotation-rich symbol cards, collect Context Tray Markdown, or visually
explore graph edges.

| Document | Description |
|----------|-------------|
| [CLI Reference](cli-reference.md) | Full CLI commands, options, and configuration file (`.ccg.yaml`) |
| [Lint](lint.md) | Detailed `ccg lint` category reference, interpretation guide, and CI usage |
| [MCP Tools](mcp-tools.md) | 19 MCP tools, agent skills, evidence-first routing, AI-driven annotation |
| [Annotations](annotations.md) | Custom annotation system — tags, examples, and search quality |
| [Webhook](webhook.md) | GitHub / Gitea webhook sync, branch filtering, graceful shutdown |
| [Docker](docker.md) | Docker image build, MCP server setup, Wiki UI deployment, PostgreSQL integration |
| [Operations](operations.md) | Deployment profiles, database choice, readiness, webhook operations, troubleshooting |
| [Runtime Layout](runtime-layout.md) | `ccg`, `ccg-server`, Wiki serving, and shared `ccg-core` ownership boundaries |
| [Architecture](architecture.md) | System architecture, data flow, DB schema |
| [Development](development.md) | Build, test, integration test (Gitea + PostgreSQL) |
| [Large Build Performance](build-performance.md) | Measured full-build bottlenecks, bounded concurrency, and import-file indexing |
| [Namespace Migration](namespace-migration.md) | Default namespace change and migration guide |
| [CLAUDE.md Guide](claude-md-guide.md) | CLAUDE.md template for projects using CCG |
