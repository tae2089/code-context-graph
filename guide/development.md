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

## Conventions

- TDD: Red → Green → Refactor
- Tidy First: 구조적 변경과 행위 변경 분리
- GORM 쿼리만 사용 (raw SQL 금지)
- 로깅: `slog`
- CLI: `cobra` framework
- Build flags: `CGO_ENABLED=1 -tags "fts5"`
