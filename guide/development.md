# Development

## Build

```bash
CGO_ENABLED=1 go build -tags "fts5" -o ccg ./cmd/ccg/
```

## Test

```bash
CGO_ENABLED=1 go test -tags "fts5" ./... -count=1
```

### Eval Test

파서 정확도 평가 (12개 언어 golden corpus 비교):

```bash
# Golden 파일 업데이트 (파서 변경 후)
ccg eval --suite parser --update

# 정확도 비교
ccg eval --suite parser

# JSON 포맷 출력
ccg eval --format json
```

## Integration Test

Gitea push → webhook → ccg clone → build → PostgreSQL → MCP 검증의 full-stack 파이프라인 테스트:

```bash
./scripts/integration-test.sh
```

### What It Does

1. Docker Compose로 3개 컨테이너 시작 (Gitea, PostgreSQL, ccg)
2. Gitea 관리자 유저 및 API 토큰 생성
3. 샘플 Go 코드가 포함된 레포지토리 생성
4. ccg를 가리키는 webhook 등록
5. Gitea에 코드 push (webhook 트리거)
6. ccg가 clone, parse, build 완료 대기
7. MCP 프로토콜로 그래프 데이터 검증 (initialize → tools/call)
8. 모든 컨테이너 정리

### Manual Container Management

```bash
docker compose -f docker-compose.integration.yml up -d --build
docker compose -f docker-compose.integration.yml down -v
```

## Project Structure

```
cmd/ccg/              — CLI 엔트리포인트 (cobra)
internal/
  analysis/           — 분석 엔진 (impact, flows, deadcode, community, ...)
  annotation/         — 어노테이션 파서
  cli/                — CLI 커맨드 정의
  ctxns/              — Context namespace
  docs/               — 문서 생성
  benchmark/          — 토큰 절감 벤치마크 (naive vs graph, recall 측정)
  eval/               — 파서/검색 품질 평가 (golden corpus 기반 P/R/F1, P@K, MRR, nDCG)
  mcp/                — MCP 서버 (29 tools)
  model/              — DB 모델
  parse/treesitter/   — Tree-sitter 파서 (12 languages, Lua/Luau 포함)
  pathutil/           — 경로 유틸리티
  ragindex/           — RAG 인덱스
  service/            — 비즈니스 로직
  store/              — GORM 저장소
  webhook/            — Webhook 핸들러, SyncQueue, RepoFilter
skills/               — Claude Code 스킬 파일
guide/                — 프로젝트 문서
docs/                 — 자동 생성 문서 (ccg docs)
testdata/eval/        — Eval golden corpus (12개 언어 소스 + golden JSON)
scripts/              — 스크립트 (integration test 등)
```

## Token Benchmark

CCG가 LLM에 전달하는 컨텍스트 토큰을 naive 방식 대비 얼마나 줄이는지 측정합니다.

```bash
ccg benchmark token-bench \
  --db-dsn ./ccg.db \
  --corpus testdata/benchmark/queries.yaml \
  --repo /path/to/target-repo \
  --exts .go \
  --limit 30
```

### 측정 방식

| 항목 | 설명 |
|------|------|
| `naive_tokens` | 레포 전체 소스 파일의 토큰 합계 (`len(text)/4` 추정) |
| `graph_tokens` | description → FTS 검색 → 1-hop 확장 후 수집된 노드 토큰 합계 |
| `ratio` | `naive / graph` |
| `recall` | expected_files + expected_symbols 히트율 (0~1) |

### 설계 원칙

- **검색은 description 사용**: `expected_symbols`를 직접 검색 쿼리로 쓰는 것은 oracle cheating. 자연어 description의 ASCII 단어만 추출해 FTS에 사용한다.
- **단어당 limit 자동 조정**: `limitPerTerm = max(3, limit / len(terms))` — 단어 수가 많아도 총 결과 예산을 유지한다.
- **1-hop 확장 포함**: gormstore expander를 통해 검색 노드의 이웃 노드와 어노테이션을 포함한 현실적인 graph_tokens를 측정한다.
- **recall 없는 ratio는 무의미**: recall < 0.5인 쿼리는 graph_tokens가 작더라도 신뢰할 수 없다.

### gin-gonic 기준 측정 결과 (limit=30)

| query | ratio | recall |
|-------|-------|--------|
| router | ~54x | 0.6 |
| context | ~54x | 0.5 |
| middleware | ~79x | 1.0 |
| binding | ~35x | 0.75 |
| recovery | ~46x | 1.0 |

code-review-graph 논문 기준치 49x와 비교해 정직한 측정 기반으로 동등 이상.

## Conventions

- TDD: Red → Green → Refactor
- Tidy First: 구조적 변경과 행위 변경 분리
- GORM 쿼리만 사용 (raw SQL 금지)
- 로깅: `slog`
- CLI: `cobra` framework
- Build flags: `CGO_ENABLED=1 -tags "fts5"`
