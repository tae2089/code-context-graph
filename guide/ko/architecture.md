# 아키텍처

CCG는 모듈형 헥사고날 아키텍처를 사용합니다. 기능 중심 application 패키지가
자신이 소비하는 port를 소유하고, inbound adapter는 프로토콜을 변환하며,
outbound adapter는 DB/parser/Git/filesystem 구현을 제공합니다. 모든 조립은
runtime 패키지 트리에서만 수행합니다.

## 데이터 흐름

```text
Source -> Tree-sitter adapter -> ingest application -> graph/search ports
                                              |              |
                                              v              v
                                        graph-GORM      search-SQL

CLI / MCP / HTTP / Wiki / webhook -> application capability -> outbound port
```

## 패키지 소유권

| 계층 | 현재 패키지 | 책임 |
| --- | --- | --- |
| Domain | `internal/domain/{graph,annotation,reference}` | 안정적인 graph 값, annotation 어휘, `ccg://` reference |
| Application | `internal/app/{ingest,analyze,search,docs,wiki,reposync}` | use case 정책과 consumer-owned port |
| Inbound adapter | `internal/adapters/inbound/{cli,mcp,http,webhook,wikihttp}` | Cobra, MCP, HTTP, webhook, Wiki 프로토콜 매핑 |
| Outbound adapter | `internal/adapters/outbound/*` | GORM, SQL 검색, Tree-sitter, Git, 파일, sync graph, observability 구현 |
| Runtime | `internal/runtime`, `internal/runtime/mcp`, `internal/runtime/remote` | 공용 조립, MCP lifecycle, 원격 전용 조립 |
| 소형 primitive | `internal/{config,ctx,db,obs,pathspec,safepath}` | 기능 정책을 담지 않는 제한된 공용 primitive. DB migration과 embedded SQL 자산은 `internal/db/migration`이 함께 소유합니다. |

## 기능 모듈

- `app/ingest`: parse, annotation bind, edge resolve, 전체 build, transaction 기반
  incremental update. `ingest.UnitOfWork`가 graph와 파생 search 변경을 원자적으로 묶습니다.
- `app/analyze`: callers/callees, impact radius, flow trace/rebuild, Git diff risk,
  통계와 bounded read model.
- `app/search`: search document 생성, FTS retrieval, 구조 rerank, retrieval evidence,
  그리고 document/rank/query syntax가 공유하는 identifier tokenization을 소유합니다.
  SQLite는 FTS5, PostgreSQL은 tsvector/GIN을 사용합니다.
- `app/docs`: 결정적 Markdown 생성 및 lint.
- `app/wiki`: CCG 고유의 코드 탐색 tree와 compatibility snapshot. 별도 제품인
  향후 OpenWiki와 합치거나 추출하지 않습니다.
- `app/reposync`: repository admission, branch 정책, retry/queue, checkout부터
  ingest까지의 orchestration.

## 의존성 규칙

1. Domain은 표준 라이브러리와 다른 domain 패키지만 import합니다.
2. Application은 domain과 해당 consumer가 소유한 port만 import합니다.
3. Application은 adapter, runtime, GORM, Cobra, MCP, HTTP를 import하지 않습니다.
4. Inbound adapter는 application contract를 호출하며 GORM query를 포함하지 않습니다.
5. Outbound adapter는 application port를 구현하고 GORM, DB driver, Tree-sitter,
   go-git, filesystem, telemetry 라이브러리를 사용할 수 있습니다.
6. 전역 ports/store 패키지는 금지하며 port는 consumer 옆에 둡니다.
7. Runtime만 의도적으로 fan-out이 큰 조립 트리입니다.
8. `cmd/*`는 runtime/adapter를 선택하고 process exit만 소유합니다.

`internal/archtest`가 `go list -json`으로 이 규칙, 과거 패키지 부재, 로컬
바이너리 의존성 폐쇄를 검증합니다.

## 런타임과 전송 계층

`ccg serve`(stdio)와 `ccg-server`(Streamable HTTP)는 동일한 다섯 MCP 의존성
그룹을 사용하며 정확히 17개 tool과 4개 prompt를 노출합니다. 로컬 바이너리는
원격 HTTP, Wiki, webhook, remote runtime 패키지를 링크하지 않습니다.
자세한 내용은 [런타임 레이아웃](runtime-layout.md)을 참고하세요.

## 영속성과 격리

Graph record, search document, community, flow는 namespace를 가집니다.
Application 호출은 context로 namespace를 전달하고 outbound repository가 필터를
적용합니다. SQLite/PostgreSQL graph 연산은 GORM을 공유하고 full-text search는
DB별 adapter를 사용합니다. Application과 inbound package는 SQL을 실행하지 않으며,
DB별 SQL은 outbound adapter와 migration code 내부에만 캡슐화합니다.

## 신뢰성과 보안

- Repository sync는 trust 전에 HMAC을 검증하고 ordered allow rule, bounded
  queue/retry, clone root 검증, 종료 drain을 적용합니다.
- HTTP는 body/read/header/idle 제한, 민감 endpoint bearer 인증, bounded graceful
  shutdown을 적용합니다.
- Parser, DB, cache, telemetry, queue는 각각 명시적이고 idempotent한 lifecycle owner가 있습니다.
- Wiki/static/docs 경로는 목적별 containment 및 symlink-safe 정책을 사용합니다.

결정과 기각한 대안은 [ADR-0001](../adr/0001-modular-hexagonal-architecture.md)을 참고하세요.
