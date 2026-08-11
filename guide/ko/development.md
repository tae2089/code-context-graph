# 개발 (Development)

[English](../development.md)

## 빌드 (Build)

```bash
CGO_ENABLED=1 go build -tags "fts5" -o ccg ./cmd/ccg/
CGO_ENABLED=1 go build -tags "fts5" -o ccg-server ./cmd/ccg-server/
```

Makefile 단축 명령어:

```bash
make build        # stripped ccg 및 ccg-server 바이너리 빌드 (make release와 동일)
make release      # 버전/커밋/날짜 정보가 포함된 stripped 빌드
make build-debug  # 버전/커밋/날짜 정보가 포함된 unstripped ccg 및 ccg-server 바이너리 빌드
make wiki-db      # 로컬 Wiki DB 마이그레이션 및 WIKI_REPO 그래프 빌드
make wiki-run     # Wiki UI 빌드, 그래프 빌드, DB-backed Wiki API로 ccg-server 실행
make wiki-run-indexed # Wiki UI 빌드, 그래프/문서/index 생성 후 ccg-server 실행
```

`make wiki-run`은 기본값으로 `127.0.0.1:8080`과 `ccg.db`를 사용합니다.
`WIKI_ADDR`, `WIKI_DB`, `WIKI_REPO`, 필요 시 `WIKI_TOKEN`으로 값을 바꿀 수 있습니다:

```bash
WIKI_ADDR=127.0.0.1:18080 WIKI_TOKEN=dev-token make wiki-run
```

## 테스트 (Test)

```bash
make test
```

`make test`는 Go 테스트 스위트와 Docker 통합 테스트용 경량 셸 헬퍼 테스트를 모두 실행합니다.

### 전체 실행 결과 읽기

전체 실행은 수천 줄을 출력하므로 실패만 걸러 보고 싶어집니다. 그런데 거르는 기준을 잘못 잡으면 실패는 이름만 남고 내용이 사라집니다:

```bash
# 이렇게 하지 마세요. `--- FAIL: TestX` 아래에 메시지가 하나도 안 남고, 실행은 이미 끝나 있습니다.
go test -tags fts5 ./... -count=1 | grep -E '^(FAIL|---)'
```

무엇이 깨졌는지 말해 주는 줄은 `--- FAIL` 아래에 들여쓰기된 줄인데, 저 필터는 그걸 전부 버립니다. 로그를 통째로 남기고 나중에 찾으세요:

```bash
go test -tags fts5 ./... -count=1 2>&1 | tee /tmp/ccg-test.log
grep -B2 -A20 '^--- FAIL' /tmp/ccg-test.log
```

실행 중에 꼭 걸러야 한다면 들여쓰기된 줄도 함께 남깁니다:

```bash
go test -tags fts5 ./... -count=1 2>&1 | grep -E '^(FAIL|--- FAIL|ok )|^[[:space:]]'
```

한 번 실패하고 다시는 재현되지 않는 테스트에서 특히 중요합니다. #78은 간헐적 실패가 남긴 유일한 메시지를 이렇게 잃었고, 아무리 다시 돌려도 그 메시지는 돌아오지 않았습니다.

## PostgreSQL 테스트

`postgres` 빌드 태그가 붙은 테스트는 실제 서버가 필요합니다. `TEST_POSTGRES_DSN`으로 서버를 지정한 뒤 실행합니다:

```bash
docker run -d --name ccg-test-pg -p 5432:5432 \
  -e POSTGRES_PASSWORD=postgres -e POSTGRES_DB=ccg_test postgres:16

TEST_POSTGRES_DSN="host=localhost user=postgres password=postgres dbname=ccg_test port=5432 sslmode=disable" \
  CGO_ENABLED=1 go test -tags "fts5,postgres" ./... -count=1
```

이 명령에서 주의할 점 두 가지:

- 읽히는 환경 변수는 `TEST_POSTGRES_DSN` 하나뿐입니다. 값이 없거나 이름을 잘못 쓰면 모든 postgres 테스트가 `t.Skip`을 호출하고, 실제로는 아무것도 실행하지 않은 채 패키지마다 `ok`만 출력합니다. CI처럼 `REQUIRE_POSTGRES=1`을 설정하면 이 조용한 통과가 실패로 바뀝니다.
- `-p 1`은 쓰지 않습니다. 각 테스트가 `internal/db/dbtest`를 통해 자기 스키마를 만들고 `search_path`를 거기로 맞추므로 패키지가 병렬로 돌아갑니다. 패키지를 함께 돌릴 때만 실패하는 테스트는 자기 스키마 밖을 건드리고 있다는 뜻이며, `-p 1`은 그것을 고치는 대신 감춥니다.

postgres 테스트를 새로 쓸 때는 직접 커넥션을 열지 말고 헬퍼 두 개 중 하나를 씁니다:

```go
db := dbtest.OpenIsolatedPostgres(t)   // 이 테스트 전용 스키마로 범위가 잡힌 *gorm.DB
dsn := dbtest.IsolatedPostgresDSN(t)   // 같은 스키마를 DSN으로. 풀을 직접 여는 코드용
```

둘 다 스키마를 만들고, 테스트가 끝나면 지우고, 서버가 없으면 테스트를 건너뜁니다. 설계상 알아둘 점:

- 스키마는 `SET search_path` 문이 아니라 DSN에 담겨 전달됩니다. `SET`은 커넥션 하나에만 적용되는데 풀은 여러 개를 열고, 나머지는 계속 `public`에 씁니다.
- DSN에 `search_path`를 직접 써넣지 말고, 테스트에서 `public`을 이름으로 쓰지 마세요. 스키마를 명시한 이름이나 `'public'`으로 필터한 카탈로그 쿼리는 다른 테스트의 테이블을 읽습니다.
- 카탈로그 쿼리에는 스키마 조건이 필요합니다. `SELECT count(*) FROM pg_indexes WHERE indexname = 'x'`는 병렬로 도는 모든 테스트의 사본을 셉니다. `current_schema()`로 필터하거나 `migration.PostgresIndexExists`를 쓰세요.
- 익스텐션은 스키마가 아니라 데이터베이스에 속하므로, `pg_trgm`은 전용 스키마에 두고 각 테스트의 `search_path` 뒤에 붙입니다. 이것이 `public` 밖에서도 `gin_trgm_ops` 인덱스가 동작하게 하는 부분입니다.
- 강제 종료된 테스트가 남긴 스키마는 다음 실행에서 생성 시각 기준 정리(sweep)로 지워집니다. 기준 시간은 어떤 테스트보다도 긴 시간 단위라서, 사용 중인 스키마가 지워지는 일은 없습니다.

## Integration Test

풀스택 파이프라인 테스트: Gitea push → 명시적 `ccg migrate` → 웹훅 → ccg 복제 → 빌드 → PostgreSQL → MCP 검증:

```bash
./scripts/integration-test.sh
```

경량 셸 헬퍼 테스트는 Docker를 시작하지 않고 통합 테스트용 헬퍼들을 테스트합니다:

```bash
make test-integration-helpers
```

### 테스트 수행 과정

1. Docker Compose를 통해 3개의 컨테이너(Gitea, PostgreSQL, ccg) 시작
2. 런타임 서비스 시작 전 ccg 컨테이너에서 `ccg migrate` 실행
3. Gitea 관리자 계정 및 API 토큰 생성
4. 샘플 Go 코드가 포함된 저장소 생성
5. ccg를 가리키는 웹훅 등록
6. Gitea에 코드 push (웹훅 트리거)
7. ccg가 복제, 파싱 및 빌드를 완료할 때까지 대기
8. MCP 프로토콜을 통해 그래프 데이터 검증 (초기화 → 도구 호출)
9. 실패 시 디버그 아티팩트(artifact) 캡처
10. 별도의 요청이 없는 한 모든 컨테이너 정리

### 통합 테스트 실패 디버깅

통합 테스트 도구는 실패 시 Docker 진단 정보를 기록합니다. 로컬 디버깅을 위해 다음 환경 변수들을 사용할 수 있습니다:

| 변수 | 기본값 | 설명 |
|----------|---------|-------------|
| `ARTIFACT_DIR` | `artifacts/integration-<timestamp>` | `compose-ps.txt`, `compose.log` 및 서비스별 로그 저장 디렉토리 |
| `KEEP_CONTAINERS` | `0` | 실행 후 `docker compose down -v`를 생략하려면 `1`로 설정 |
| `DUMP_ON_SUCCESS` | `0` | 성공 시에도 아티팩트를 캡처하려면 `1`로 설정 |
| `WEBHOOK_WAIT_SECONDS` | `60` | 저장소당 웹훅/빌드 최대 대기 시간 |
| `CCG_E2E_ALLOW_MCP_LOG_FALLBACK` | `0` | 로컬 디버깅용: MCP 초기화 실패 시 로그 기반 웹훅 체크를 허용하려면 `1`로 설정. 기본값은 MCP 검증이 필수이므로 실패 처리됨. |

예시:

```bash
KEEP_CONTAINERS=1 ARTIFACT_DIR=/tmp/ccg-e2e ./scripts/integration-test.sh
DUMP_ON_SUCCESS=1 ./scripts/integration-test.sh
```

웹훅 대기 시 대상 워크스페이스의 MCP 관측 가능 그래프 통계를 우선적으로 확인하며, MCP가 준비되지 않았거나 데이터를 아직 보여주지 않는 경우에만 ccg 로그로 폴백합니다.
MCP 초기화 및 도구 응답은 엄격하게 체크됩니다: 잘못된 형식의 JSON, 최상위 JSON-RPC 에러, `result.isError=true`, 내용 검증을 위한 `result.content[0].text` 누락 등은 통합 테스트 실패로 처리됩니다. MCP를 초기화할 수 없는 실행은 위에서 언급한 로컬 디버그 재정의가 설정되지 않는 한 성공으로 보고되지 않으며, 해당 재정의 설정 시 MCP 도구 검증은 건너뜁니다.

### 수동 컨테이너 관리

```bash
make container-artifacts
CONTAINER_ARCH="$(go env GOARCH)" docker compose -f docker-compose.integration.yml up -d --build
docker compose -f docker-compose.integration.yml down -v
```

## 프로젝트 구조 (Project Structure)

```
cmd/ccg/              — 로컬 CLI 진입점 (cobra, stdio MCP)
cmd/ccg-server/       — 셀프호스트 HTTP MCP/웹훅 서버 진입점
internal/
  analysis/           — 분석 엔진 (impact, flows, changes, incremental update)
  annotation/         — 어노테이션 파서
  cli/                — CLI 명령어 정의
  core/               — parser, DB, store, search, sync 공용 런타임 wiring
  ctx/                — 요청 컨텍스트 값 (namespace 격리)
  docs/               — 문서 생성 로직
  mcpruntime/         — 공용 MCP runtime assembly, stdio runner, cache, telemetry
  mcp/                — MCP 서버 (18개 도구)
  wikiserver/         — ccg-server Wiki 정적 파일 서빙 및 viewer API
  wikiindex/          — Wiki 표시용 인덱스 생성기 (`wiki-index.json`)
  model/              — DB 모델
  parse/treesitter/   — Tree-sitter 파서 (Lua/Luau 포함 12개 언어)
  pathspec/           — 순수 include/exclude 및 경로 문자열 매칭
  ragindex/           — 공용 Wiki tree 및 문서 검색 DTO/helper
  server/             — HTTP MCP 서버, health/status 엔드포인트, 웹훅 런타임
  service/            — 비즈니스 로직
  store/              — GORM 저장소
  webhook/            — 웹훅 핸들러, SyncQueue, RepoFilter
skills/               — 에이전트 스킬 파일
guide/                — 프로젝트 문서
docs/                 — 자동 생성된 문서 (ccg docs)
scripts/              — 스크립트 (통합 테스트 등)
```

React/Tailwind Wiki UI는 `web/wiki`에 있으며 `web/wiki/dist`로 빌드됩니다.
dist 디렉터리는 git에서 제외하고 release에서 별도 asset으로 패키징합니다:

```bash
make wiki-build
```

## Skill 계약 (Skill Contract)

`skills/` 아래의 모든 프로젝트 로컬 skill은 다음을 frontmatter에 선언합니다:

- 실제 trigger가 포함된 `name`과 `description`
- semantic `metadata.version`
- `metadata.openclaw.category`와 `domain`
- `metadata.requires` 아래의 필수 binary와 선행 skill
- 직접 대응하는 CLI help가 있을 때만 `metadata.cliHelp`

세부 variant는 `SKILL.md`에서 직접 연결한 `references/` 파일에 두고, 핵심
`SKILL.md` 지침은 특정 host에 종속되지 않게 유지합니다. metadata, dependency,
직접 reference link, 제거된 command drift는 다음 명령으로 검증합니다:

```bash
go test ./internal/adapters/inbound/cli -run TestProjectSkills -count=1
```

## 컨벤션 (Conventions)

- TDD: Red → Green → Refactor
- Tidy First: 구조적 변경과 행동 변경의 분리
- GORM 쿼리만 사용 (Raw SQL 사용 금지)
- 로깅: `slog`
- CLI: `cobra` 프레임워크
- 빌드 플래그: `CGO_ENABLED=1 -tags "fts5"`
