# Lint v2: 8-Category Lint + Twice Rule

## Goal

ccg lint를 4개 카테고리에서 8개로 확장하고, Twice Rule(같은 경고 2회 연속 → .ccg.yaml에 자동 규칙 추가)을 도입한다.

## Architecture

```
ccg lint
  ├── LintReport (8 categories)
  │     ├── orphan        — doc 있는데 소스 없음 (기존)
  │     ├── missing       — 소스 있는데 doc 없음 (기존)
  │     ├── stale         — doc가 소스보다 오래됨 (기존)
  │     ├── unannotated   — 어노테이션 자체가 없음 (기존)
  │     ├── contradiction — 코드 hash 변경 + @param 존재 (신규)
  │     ├── dead-ref      — @see 타겟이 DB에 없음 (신규)
  │     ├── incomplete    — 어노테이션 있지만 @intent 누락 (신규)
  │     └── drift         — Node.UpdatedAt > Annotation.UpdatedAt (신규)
  │
  ├── Twice Rule
  │     ├── .ccg/lint-history.json  — 이전 실행 결과 스냅샷
  │     └── .ccg.yaml rules 섹션   — 자동 생성된 규칙
  │
  └── --strict 모드
        └── rules에서 action: ignore인 항목은 카운트 제외
```

## Data Model

### LintReport 확장

```go
type LintReport struct {
    // 기존
    Orphans     []string
    Missing     []string
    Stale       []string
    Unannotated []string

    // 신규
    Contradictions []Contradiction
    DeadRefs       []DeadRef
    Incomplete     []string  // qualified names
    Drifted        []string  // qualified names

    // Twice Rule
    TwiceRuleTriggered []string // "category:qualified_name"
}

type Contradiction struct {
    QualifiedName string
    Detail        string
}

type DeadRef struct {
    QualifiedName string
    SeeTarget     string
}
```

### 탐지 로직

| 카테고리 | 데이터 소스 | 로직 |
|---------|-----------|------|
| contradiction | Node + Annotation + DocTag(param) | 어노테이션에 @param 태그가 있고 Node.UpdatedAt > Annotation.UpdatedAt → 시그니처 변경 가능성 경고 |
| dead-ref | DocTag(see) + Node DB lookup | @see 값으로 GetNode() 조회, 없으면 dead ref |
| incomplete | Annotation + DocTag | annotation 존재하지만 TagIntent가 하나도 없는 심볼 |
| drift | Node.UpdatedAt vs Annotation.UpdatedAt | Node.UpdatedAt > Annotation.UpdatedAt (코드가 어노테이션 이후 변경됨) |

**contradiction 간단 버전**: 1차에서는 "Node hash 변경 + @param 존재"만 감지한다. 정밀 버전(Tree-sitter로 파라미터명 추출 → @param과 1:1 비교)은 추후 구현한다.

## Twice Rule

### 동작 흐름

1. `ccg lint` 실행 → 현재 LintReport 산출
2. `.ccg/lint-history.json` 읽기 (없으면 빈 상태)
3. 현재 결과의 각 항목을 `category:qualified_name` 키로 매핑
4. 이전 히스토리와 비교:
   - 양쪽 모두 존재 → count++
   - 현재에만 존재 → count = 1 (신규)
   - 이전에만 존재 → 삭제 (해결됨)
5. count >= 2인 항목 → `.ccg.yaml`의 `rules` 섹션에 자동 추가
6. `.ccg/lint-history.json` 갱신

### lint-history.json 형식

```json
{
  "timestamp": "2026-04-14T10:00:00Z",
  "entries": {
    "incomplete:pkg/service.go::Handle": 1,
    "unannotated:pkg/util.go::Helper": 2
  }
}
```

### .ccg.yaml rules 형식

```yaml
rules:
  - pattern: "pkg/util.go::Helper"
    category: unannotated
    action: warn
    auto: true
    created: "2026-04-14"
```

- `warn` (기본, Twice Rule 자동 생성 시 항상 이 값)
- `ignore` — 사용자가 수동 변경. lint 리포트에서 제외. --strict 카운트에서도 제외.
- `error` — 사용자가 수동 변경. --strict 모드에서 반드시 실패.

### --strict 동작

`.ccg.yaml` rules에서 `action: ignore`인 항목은 total count에서 제외. 그 외 모든 카테고리의 합 > 0이면 exit 1.

## CLI 출력 포맷

```
$ ccg lint --out docs/

Orphan docs (1):
  internal/deleted.go

Missing docs (2):
  internal/new_handler.go
  internal/new_service.go

Stale docs (1):
  internal/auth/login.go

Unannotated symbols (3):
  internal/auth/login.go::Login
  internal/payment/pay.go::Pay
  pkg/service.go::Config

Contradictions (1):
  internal/auth/login.go::Login — @param exists but node hash changed since annotation

Dead refs (1):
  internal/payment/pay.go::Pay — @see pkg/old.go::Removed (not found)

Incomplete annotations (2):
  internal/util/helper.go::Parse — missing @intent
  internal/util/helper.go::Format — missing @intent

Drifted annotations (1):
  internal/auth/session.go::Validate — node updated 2026-04-13, annotation from 2026-04-01

Twice Rule triggered (1):
  unannotated:pkg/service.go::Config → added to .ccg.yaml rules (warn)

Summary: 1 orphan, 2 missing, 1 stale, 3 unannotated, 1 contradiction, 1 dead-ref, 2 incomplete, 1 drifted
```

## 파일 구조

| 파일 | 변경 |
|------|------|
| `internal/docs/lint.go` | LintReport 확장 + 4개 신규 탐지 로직 |
| `internal/docs/lint_test.go` | 신규 카테고리 테스트 (contradiction, dead-ref, incomplete, drift) |
| `internal/docs/history.go` (신규) | lint-history.json CRUD + Twice Rule 비교 + .ccg.yaml rules 자동 추가 |
| `internal/docs/history_test.go` (신규) | Twice Rule 트리거/해제/히스토리 파일 관리 테스트 |
| `internal/cli/lint.go` | 신규 카테고리 출력 + Twice Rule 출력 + rules ignore 처리 |
| `internal/cli/lint_test.go` | CLI 통합 테스트 |

## 테스트 매트릭스

| 테스트 | 검증 내용 |
|--------|----------|
| `TestLint_DetectsContradiction` | @param 있는 노드의 UpdatedAt > Annotation.UpdatedAt → contradiction |
| `TestLint_NoContradiction_WhenFresh` | @param 있지만 annotation이 더 최신 → 정상 |
| `TestLint_DetectsDeadRef` | @see 타겟 qualified name이 DB에 없음 → dead-ref |
| `TestLint_ValidRef_NotDeadRef` | @see 타겟이 DB에 있음 → 정상 |
| `TestLint_DetectsIncomplete` | annotation 있지만 TagIntent 없음 → incomplete |
| `TestLint_CompleteAnnotation_NotIncomplete` | TagIntent 있음 → 정상 |
| `TestLint_DetectsDrift` | Node.UpdatedAt > Annotation.UpdatedAt → drift |
| `TestLint_NoDrift_WhenAnnotationFresh` | Annotation이 Node보다 최신 → 정상 |
| `TestHistory_FirstRun_CountOne` | 첫 실행 시 모든 항목 count = 1 |
| `TestHistory_SecondRun_TwiceRule` | 같은 항목 2회 연속 → TwiceRuleTriggered |
| `TestHistory_ResolvedItem_Removed` | 이전에 있던 항목 해결 → count 삭제 |
| `TestHistory_WritesYamlRule` | count >= 2 → .ccg.yaml rules에 추가 |
| `TestHistory_Idempotent_NoDoubleRule` | 이미 rules에 있는 항목 → 중복 추가 안 함 |
| `TestLintCLI_IgnoreRule_StrictMode` | rules에 ignore → --strict에서 카운트 제외 |
| `TestLintCLI_ErrorRule_StrictMode` | rules에 error → --strict에서 카운트 포함 |

## 미구현 (추후)

- **contradiction 정밀 버전**: Tree-sitter로 함수 파라미터명 추출 → @param name과 1:1 비교
- **--fix 플래그**: stale → docs 재생성, orphan → 파일 삭제, dead-ref → @see 제거
- **severity level**: 카테고리별 기본 severity (info/warn/error) 설정
