# Architecture

CCG uses a modular hexagonal architecture. Capability-oriented application
packages own their ports; inbound adapters map protocols, outbound adapters
implement persistence/parser/Git/filesystem details, and runtime packages are the
only composition roots.

## Data flow

```text
Source -> Tree-sitter adapter -> ingest application -> graph/search ports
                                              |              |
                                              v              v
                                        graph-GORM      search-SQL

CLI / MCP / HTTP / Wiki / webhook -> application capabilities -> outbound ports
```

## Package ownership

| Layer | Current packages | Responsibility |
| --- | --- | --- |
| Domain | `internal/domain/{graph,annotation,reference}` | Stable graph values, annotation vocabulary, and `ccg://` references |
| Application | `internal/app/{ingest,analyze,search,docs,wiki,reposync}` | Use-case policy and consumer-owned ports |
| Inbound adapters | `internal/adapters/inbound/{cli,mcp,http,webhook,wikihttp}` | Cobra, MCP, HTTP, webhook, and Wiki protocol mapping |
| Outbound adapters | `internal/adapters/outbound/*` | GORM, SQL search, Tree-sitter, Git, filesystem, sync graph, and observability implementations |
| Runtime | `internal/runtime`, `internal/runtime/mcp`, `internal/runtime/remote` | Shared assembly, MCP lifecycle, and remote-only composition |
| Small primitives | `internal/{config,ctx,db,obs,pathspec,safepath}` | Focused cross-capability primitives; no feature-policy dumping ground. Database migrations and their embedded SQL assets live together in `internal/db/migration`. |

## Capability modules

- `app/ingest`: parse, bind annotations, resolve edges, full build, and
  transactional incremental update. `ingest.UnitOfWork` keeps graph and derived
  search changes atomic.
- `app/analyze`: callers/callees, impact radius, flow tracing/rebuild, Git-diff
  risk, statistics, and bounded read models.
- `app/search`: search-document generation, FTS retrieval, structural reranking,
  and retrieval evidence. It also owns identifier tokenization shared by search
  documents, ranking, and query syntax. SQLite uses FTS5; PostgreSQL uses
  tsvector/GIN.
- `app/docs`: deterministic Markdown generation and lint.
- `app/wiki`: CCG's built-in code-exploration tree and compatibility snapshot.
  This is a CCG capability, not the separate future OpenWiki product.
- `app/reposync`: repository admission, branch policy, retry/queue policy, and
  checkout-to-ingest orchestration.

## Dependency rules

1. Domain imports only standard library and other domain packages.
2. Application imports domain and ports owned by the consuming capability.
3. Application imports no adapter, runtime, GORM, Cobra, MCP, or HTTP package.
4. Inbound adapters invoke application contracts and contain no GORM query.
5. Outbound adapters implement application ports and may use GORM, SQL drivers,
   Tree-sitter, go-git, filesystem, or telemetry libraries.
6. Global ports/store packages are forbidden; ports live beside their consumer.
7. Runtime is the only intentionally high-fan-out composition tree.
8. `cmd/*` selects a runtime/adapter and owns process exit only.

`internal/archtest` enforces these rules, legacy-package absence, and local binary
closure with deterministic `go list -json` checks.

## Runtime and transports

Both `ccg serve` (stdio) and `ccg-server` (Streamable HTTP) use the same five
grouped MCP dependency surfaces and expose exactly 18 tools plus four prompts.
The local binary does not link remote HTTP, Wiki, webhook, or remote runtime
packages. See [Runtime Layout](runtime-layout.md).

## Persistence and isolation

Graph records, search documents, communities, and flows carry a namespace.
Application calls propagate it through context; outbound repositories apply the
filters. SQLite and PostgreSQL share GORM-owned graph operations and backend-
specific full-text search adapters. Application and inbound packages never issue
SQL; backend-specific SQL remains encapsulated in outbound adapters and migration code.

The `cross_refs` table is the one deliberate bridge across namespaces: each row
materializes an `@see ccg://` annotation with a symbolic target
(`to_namespace`, `to_path`, `to_symbol`) plus derived state
(`resolved_node_id`, `status`). Rows are rebuilt from annotations after every
build/update of the source namespace and re-resolved when the target namespace
rebuilds, because replace-style syncs regenerate node ids. Cross-namespace
analysis reads go through a dedicated namespace-agnostic reader that merges
resolved refs into traversal as synthetic `cross_ref` edges; regular
single-namespace query paths remain strictly namespace-filtered.

## Reliability and security

- Repository sync verifies HMAC before trust, applies ordered allow rules, bounds
  queue state/retries, validates clone roots, and drains workers on shutdown.
- HTTP enforces body/read/header/idle limits, bearer authentication for sensitive
  endpoints, and bounded graceful shutdown.
- Parser, DB, cache, telemetry, and queue resources each have explicit,
  idempotent lifecycle ownership.
- Wiki/static/docs paths use dedicated containment and symlink-safe policies.

See [ADR-0001](adr/0001-modular-hexagonal-architecture.md) for the decision and
rejected alternatives.
