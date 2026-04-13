# ccg docs 서브커맨드 설계

**날짜:** 2026-04-13
**상태:** 승인됨

## 개요

`ccg docs [dir] --out <outDir>` 커맨드를 추가한다.
SQLite 그래프(nodes + annotations + edges)를 읽어 파일별 마크다운 문서와 전체 인덱스를 생성한다.

목표 플로우:

```
ccg annotate .   →  코딩 에이전트가 @intent, @domainRule 등 주석 생성
ccg build .      →  Tree-sitter 파싱 → nodes, edges, annotations → SQLite
ccg docs .       →  SQLite 그래프 + 어노테이션 → index.md + 파일별 docs
ccg serve        →  MCP 서버 (index.md 제공 + FTS5 + 그래프 탐색)
```

## 인터페이스

```
ccg docs [dir] [flags]

Flags:
  --out string   출력 디렉터리 (기본값: "./docs")
```

- `dir`을 생략하면 현재 디렉터리(`.`)
- `--out` 디렉터리가 없으면 자동 생성
- serve와의 별도 연동 없음 — 생성된 파일을 프롬프트로 직접 활용

## 아키텍처

### 패키지 구조

```
internal/
  cli/docs.go          — cobra 커맨드 (build.go 패턴 동일)
  docs/
    generator.go       — Generator struct + Run() 메서드
    template.go        — 마크다운 템플릿 정의
```

### 데이터 흐름

```
ccg docs [dir] --out ./docs
        │
        ▼
  docs.Generator{DB: *gorm.DB}
        │
        ├─ nodes 조회: DB.Find(&nodes)  (kind IN file/function/class/type/test)
        ├─ annotations 조회: DB.Preload("Tags").Find(&annotations)
        └─ edges 조회: DB.Where("from_node_id IN ?", nodeIDs).Find(&edges)
        │
        ▼
  파일별 그룹핑: map[filePath][]Node
        │
        ├─ 파일당 → {outDir}/{filePath}.md
        └─ 전체   → {outDir}/index.md
```

`Generator`는 `*gorm.DB`에만 의존한다.
`GraphStore` 인터페이스를 거치지 않는 이유: 어노테이션 `Preload`가 GORM 전용 API이므로.

## 출력 포맷

### 파일별 문서: `{outDir}/{filePath}.md`

```markdown
# internal/cli/build.go

> @index 태그 값 (있을 경우)

## Functions

### newBuildCmd
- **Lines:** 23–230
- **Intent:** 코드 그래프 빌드 커맨드를 생성한다
- **Domain Rules:**
  - `.git`, `vendor`, `node_modules` 디렉터리는 스킵
- **Side Effects:** SQLite DB에 nodes/edges/annotations upsert
- **Params:**
  - `deps *Deps` — 공유 의존성
- **Returns:** `*cobra.Command`
- **Calls:** `deps.Store.UpsertNodes`, `deps.Store.UpsertEdges`

## Types

### Node
- **Lines:** 15–29
- **Intent:** (없으면 생략)
```

**경로 규칙:**
- `filePath`의 디렉터리 구조를 그대로 유지: `internal/cli/build.go` → `{outDir}/internal/cli/build.go.md`
- 중간 디렉터리는 자동 생성 (`os.MkdirAll`)

**규칙:**
- 섹션 순서: Functions → Types → Classes → Tests
- 어노테이션 없는 심볼도 이름/라인 정보만으로 목록에 포함
- `@index` 태그가 파일 노드에 있으면 파일 헤더 아래 blockquote로 표시
- edges 중 `calls`/`imports` 관계만 "Calls" 항목으로 표시
- `@param`, `@return` 태그는 각각 Params/Returns 항목으로 표시

### 전체 인덱스: `{outDir}/index.md`

```markdown
# Code Context Index

Generated: 2026-04-13

## Files

| File | Symbols | Description |
|------|---------|-------------|
| [internal/cli/build.go](internal/cli/build.go.md) | 3 | 디렉터리를 파싱하여 코드 그래프 빌드 |
| [internal/model/node.go](internal/model/node.go.md) | 2 | — |

## All Symbols

| Symbol | Kind | File |
|--------|------|------|
| newBuildCmd | function | internal/cli/build.go |
| Node | type | internal/model/node.go |
```

**규칙:**
- Description 컬럼: 해당 파일 노드의 `@index` 태그 값, 없으면 `—`
- All Symbols: kind별 정렬 (file → class → function → type → test)

## 구현 범위

### In Scope
- `internal/cli/docs.go` — cobra 커맨드
- `internal/docs/generator.go` — 핵심 생성 로직
- `internal/docs/template.go` — 마크다운 템플릿
- `internal/cli/docs_test.go` — CLI 통합 테스트
- `internal/docs/generator_test.go` — 유닛 테스트
- `cmd/ccg/main.go` — `newDocsCmd` 등록

### Out of Scope
- `ccg serve`와의 MCP 연동 (프롬프트로 직접 활용)
- 커스텀 템플릿 파일 지원
- 증분 업데이트 (변경 파일만 재생성)

## 테스트 전략

- `generator_test.go`: in-memory SQLite DB에 fixtures 삽입 → `Run()` 호출 → 생성된 파일 내용 검증
- `docs_test.go`: cobra 커맨드 실행 → `--out` 경로 파일 존재 여부 검증
- TDD: Red → Green → Refactor 순서
