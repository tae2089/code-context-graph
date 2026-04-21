# Implementation: 주석-심볼 바인딩 견고성

`task.md` P0 착수 전 설계 결정. 실측(`binding_gap_integration_test.go`) 결과 기반.

---

## 실측 요약

| 언어 | 현재 바인딩 | 핵심 메커니즘 |
|------|-------------|--------------|
| Java | ✅ 동작 | `class_declaration` 노드가 어노테이션 자식으로 포함 → StartLine이 어노테이션 줄 |
| C | ✅ 동작 | `function_definition`이 `__attribute__` 포함 → StartLine이 속성 줄 |
| Rust | ⚠ gap OK / normalizer 버그 | `///` 접두사 미제거 |
| Python | ❌ 실패 | `function_definition` StartLine=`def` 줄, 데코레이터 미포함 + docstring은 comment 노드 아님 |

**핵심 인사이트**: "심볼 StartLine이 메타 표식을 포함하는가"가 tree-sitter 문법마다 다름. 단일 `effectiveStartLine()` 함수로 통일하기보다 **언어마다 이미 동작하는 것은 그대로 두고 깨진 곳만 손본다.**

---

## 결정 A. Python `function_definition` StartLine 보정

### 옵션

| # | 방식 | 파급 |
|---|------|------|
| 1 | `tags.scm` (Python)에서 `(decorated_definition (function_definition ...))` 를 함수 심볼로 캡처 | Python golden.json 전부 재생성, 쿼리 한 곳 변경 |
| 2 | walker에서 `function_definition`의 부모 조사 → `decorated_definition`이면 부모 StartLine 사용 | 쿼리 유지, walker 로직 확장. 다른 언어에도 재활용 가능 |
| 3 | 심볼 그대로 두고 binder의 `maxGap`을 언어별로 변경 (Python만 10~20) | 오탐 크게 증가 (무관한 주석 오바인딩) — 기각 |

### 추천: 옵션 1

- Java/C가 이미 tree-sitter 문법상 "declaration wrapper"가 메타를 포함함. Python의 `decorated_definition`도 같은 역할을 하므로 **같은 레이어(쿼리)에서** 통일
- 옵션 2는 walker가 노드 계층을 자의적으로 해석하게 돼서 "언어별 StartLine 조정 정책"이라는 암시적 개념이 추가됨 — Tidy First에 어긋남
- 영향: `testdata/eval/python/*/golden.json` 재생성. `ccg build` 결과의 Python 함수 StartLine이 1~N줄 앞으로 이동. downstream(`find_large_functions`의 line count, `detect_changes`의 diff)이 자연스럽게 함께 교정됨 — 오히려 긍정적

### 구현 스케치

```scheme
;; internal/parse/treesitter/queries/python/tags.scm
(decorated_definition
  definition: (function_definition
    name: (identifier) @name)) @definition.function

(function_definition
  name: (identifier) @name) @definition.function   ; 데코레이터 없는 경우 fallback
```

`class_definition`도 동일 패턴으로 추가. Red 테스트는 기존 `TestWalkerBinder_PythonDecoratorHashComment_CurrentlyFailsBinding`이 Green으로 전환되면 통과.

---

## 결정 B. Python docstring 수집 (2차 실측 반영)

### 2차 실측 결과

`testdata/binding_gap/python/docstring_*.py` 6개 fixture + `python_docstring_variants_test.go` 실측:

1. `'''`, `"""`, 한 줄, `r"""`, `f"""`, `b"""` 모두 **동일한 `string` 노드 타입** — 따옴표/prefix 분기 불필요
2. 함수·클래스 docstring: `(function_definition > block > expression_statement > string)` / `(class_definition > block > expression_statement > string)` 체인
3. 모듈 docstring: `(module > expression_statement > string)` 체인
4. **가장 중요**: docstring의 StartLine > 심볼 StartLine — gap이 **구조적으로 음수**. 현재 binder의 `gap < 1` 조건은 "주석이 심볼 위" 전제. docstring은 심볼 body 내부라 원리적으로 맞지 않음 → **EndLine 가짜 설정(옵션 3) 불가**
5. 따라서 **walker + binder 둘 다** 확장 필요

### 재정의된 옵션

| # | 방식 | 파급 |
|---|------|------|
| ~~3~~ | ~~EndLine을 심볼 StartLine-1로 가짜 설정~~ | **폐기** — gap이 음수임을 확인했고, 모든 downstream 도구가 `CommentBlock` 위치를 4줄 이상 왜곡된 값으로 보게 됨 |
| 1 | `CommentBlock`에 `IsDocstring bool` + `OwnerStartLine int` 필드 추가. walker에서 docstring 수집, binder에서 docstring 전용 경로 | Tidy First 준수 가능 (walker 확장 / binder 확장 2-커밋 분리). 소스 위치 정확 보존 |
| 2 | `CommentBlock` 그대로 두고 별도 `DocstringBlock` 타입 신설 | 타입 분리는 깔끔하나 walker/binder 반환 시그니처 2개로 늘어나 유지비 증가 |

### 추천: 옵션 1

**단계 1 (구조적 커밋)** — `CommentBlock` 확장 + `collectDocstrings`
```go
type CommentBlock struct {
    StartLine      int
    EndLine        int
    Text           string
    IsDocstring    bool  // 신규
    OwnerStartLine int   // 신규 — 소속 심볼의 StartLine (모듈 docstring은 0 또는 -1)
}
```
walker에 `collectDocstrings` 추가. 조건:
- `expression_statement`의 **유일한 named child**가 `string`
- 부모가 `block` → `OwnerStartLine = 부모(function/class)의 StartLine`
- 부모가 `module` → `IsDocstring=true`, 모듈 레벨 표시자 (파일 노드 바인딩용)

기존 `collectComments` 경로는 변경 없음. 하위 호환.

**단계 2 (행위적 커밋)** — binder에 docstring 경로 추가
```go
for _, cb := range comments {
    if cb.IsDocstring {
        // 심볼 StartLine과 OwnerStartLine 일치로 바인딩
        if cb.OwnerStartLine == node.StartLine { ... }
        continue
    }
    // 기존 gap 로직
}
```
모듈 docstring은 `NodeKindFile` 노드에 바인딩 (결정 D 참고).

### 왜 이게 맞는가

- **소스 위치 보존**: RAG 인덱싱·MCP `get_annotation`·wiki 생성 등에서 주석 원본 줄 번호 정확
- **Tidy First**: 구조 변경과 행위 변경 두 커밋 분리
- **확장성**: `IsDocstring`/`OwnerStartLine` 는 다른 "심볼 내부 메타 주석" 케이스(예: Ruby `# @!...` YARD 블록이 메서드 내부에 있는 패턴) 에도 재사용 가능

---

## 결정 D. 모듈 docstring → 파일 노드 바인딩 (결정 B 확장)

### 배경
`docstring_module.py` 실측 결과: 파일 최상단 `"""..."""`는 `(module > expression_statement > string)` 구조. 현재 binder는 `NodeKindFile`에 대해 "첫 comment block"을 바인딩하지만 (`binder.go:51-64`), Python 모듈 docstring은 그게 comment 노드가 아니라서 누락.

### 옵션
- 1. 결정 B의 `collectDocstrings`가 모듈 docstring을 `IsDocstring=true, OwnerStartLine=0`으로 수집 → binder의 NodeKindFile 경로가 이를 인식하도록 조건 추가
- 2. 별도 `ModuleDocstring` 경로

### 추천: 옵션 1 — 결정 B와 동일 메커니즘 재사용

binder의 NodeKindFile 처리에 `if first.IsDocstring && first.OwnerStartLine == 0` 케이스 추가.

### 다른 언어 영향

모듈 docstring 개념은 Python만 있음 (Go/Rust/Java는 모듈 레벨 주석이 별도 `//`, `///`, `/** */` — 이미 comment로 잡힘). Python만 처리하면 됨.

---

## 결정 C. Rust normalizer `///` 처리 범위

### 옵션

| # | 방식 | 범위 |
|---|------|------|
| 1 | `stripLinePrefix`에 `rust` 케이스 추가, `///` 와 `//`만 처리 | 최소 변경. 일반 doc comment 커버 |
| 2 | `///`, `//!`, `/** */`, `/*! */` 전부 처리 | 완전하지만 이번 범위 초과 |

### 추천: 옵션 1 (이번 P0)

`//!` (inner doc)은 파일/모듈 레벨 문서라 심볼 바인딩과는 연관이 적음. 추가 필요 시 P1.

### 구현 스케치

```go
// internal/annotation/normalizer.go:stripLinePrefix
case "rust":
    line = strings.TrimPrefix(line, "///")
    line = strings.TrimPrefix(line, "//")
```

기존 C/Go 분기 옆에 추가. 테스트는 `TestWalkerBinder_RustAttribute_CurrentlyFailsBinding`을 Green으로 전환하는 것으로 검증.

---

## 실행 순서

Tidy First + TDD:

1. **결정 C (Rust normalizer) 먼저** — 가장 작고 독립적, 영향 반경 최소
   - Red: 이미 작성됨
   - Green: `normalizer.go`에 rust 케이스 추가
   - Refactor: 언어별 `stripLinePrefix` 맵으로 분리 (옵션)

2. **결정 A (Python decorated_definition)**
   - 구조적 변경: `tags.scm` 쿼리 수정, golden.json 재생성
   - 행위 변경: Red → Green (기존 Red 테스트가 자동으로 Green)
   - 두 커밋 분리

3. **결정 B + D (Python docstring + 모듈 docstring)** — 결정 A 이후
   - 단계 1 (구조): `CommentBlock`에 `IsDocstring`/`OwnerStartLine` 필드 추가 + walker에 `collectDocstrings` 추가 — 행위 변경 없음, 모든 기존 테스트 그린 유지
   - 단계 2 (행위): binder에 docstring 경로 + NodeKindFile의 모듈 docstring 경로 추가 — Red 테스트 5개(`TestPythonDocstring_*`) Green 전환
   - 각각 별도 커밋

4. **P1 언어별 실측 추가** — Red 테스트만 먼저 추가해서 현재 상태 확정 후 판단

---

## 참고 파일

- `internal/parse/binder.go:39` — `maxGap = 2`
- `internal/parse/binder.go:46-83` — `Bind()` 로직
- `internal/parse/binder_test.go` — 단위 테스트
- `internal/parse/treesitter/binding_gap_integration_test.go` — 통합 테스트 (실측 결과 기록)
- `internal/annotation/normalizer.go:23-108` — 언어별 접두사 제거
- `internal/parse/treesitter/queries/python/tags.scm` — Python 쿼리
- `internal/parse/treesitter/queries/rust/tags.scm` — Rust 쿼리
- `testdata/binding_gap/{python,java,rust,c}/` — P0 fixture

---

## 2026-04-21 — Python prefix docstring `doc_tags` 누락 수정

### 문제 재정의

Python docstring 수집 자체는 이미 동작하지만, `r"""..."""`, `f"""..."""`, `b"""..."""`, `rb"""..."""` 같은 prefix가 붙은 문자열은 `Normalizer.stripBlockDelimiters()`가 앞의 prefix를 제거하지 못해 `@intent`가 문자열 시작에 오지 않는다. 그 결과 annotation parser가 `@tag`로 인식하지 못하고 `doc_tags`가 비게 된다.

### 최소 수정 전략

- walker / binder는 유지
- Python normalizer에서만 **파싱용 텍스트**에 한해 string prefix를 제거
- prefix 제거 후 기존 triple-quote delimiter 제거 로직을 재사용
- 원본 source text(`CommentBlock.Text`)는 변경하지 않음

### 지원 prefix 범위

- 단일: `r`, `f`, `b`, `u`
- 조합: `rb`, `br`, `rf`, `fr`, `ur`, `ru`

### TDD

1. `internal/annotation/normalizer_test.go`
   - prefix별 normalize 결과가 `@intent ...` 로 시작하는지 검증
2. `internal/parse/treesitter/python_docstring_prefix_binding_test.go`
   - 실제 walker → binder 경로에서 각 함수의 `@intent` 바인딩 검증

---

## 2026-04-21 — incremental rebuild stale node 정리 설계

### 문제

`internal/service/indexer.go`의 파일 처리 루프는 같은 파일을 재빌드할 때 새로 파싱된 노드만 `UpsertNodes` 한다.
이 경로는 **사라진 선언**을 별도로 지우지 않으므로, 예를 들어 `Remove()` 함수가 파일에서 삭제되어도 이전 빌드의 node가 DB에 남는다.

### 요구 동작

- 파일 단위 incremental rebuild 시 저장 트랜잭션 안에서 기존 파일 노드를 먼저 제거한다.
- 제거 범위는 해당 파일의 node뿐 아니라 연결 edge / annotation / doc_tags까지 포함해야 한다.
- 이후 현재 파싱 결과를 `UpsertNodes` 해서 최신 상태만 남긴다.

### 설계

1. `gormstore.DeleteNodesByFile(ctx, filePath)` 재사용
   - 구현 실측 결과 이미 존재
   - filePath+namespace 기준으로 node id를 모은 뒤 edge, doc_tags, annotation, node 순으로 cascade delete

2. `GraphService.Build()`의 파일별 트랜잭션 순서 변경
   - 기존: `UpsertNodes` → annotation binding
   - 변경: `DeleteNodesByFile` → `UpsertNodes` → annotation binding

3. TDD 계약
   - 첫 빌드: `sample.go`에 `Keep`, `Remove`
   - 두 번째 빌드: 동일 파일에서 `Remove` 삭제
   - 기대: `GetNodesByFile("sample.go")` 결과의 function 이름은 `Keep`만 존재

### 이유

- `UpsertNodes`는 conflict key가 같은 노드만 갱신하므로, **삭제된 심볼**은 절대 없어지지 않는다.
- 파일 단위 재파싱은 "해당 파일의 선언 집합을 전부 다시 계산"하는 작업이므로 replace semantics가 맞다.
- 삭제를 같은 트랜잭션 안에서 수행하면 stale 상태가 중간에 노출되지 않는다.

---

## 2026-04-21 — 코드리뷰 후속 설계 수정

### 1. Build/parse의 include_paths 의미 재정의

기존 `Build()`/`walkAndParse()`는 include_paths가 있을 때 **선택된 파일만 추가 파싱**했지만,
DB에는 이전 빌드의 비선택 파일 상태가 남아 있었다. 이는 기존 CLI/MCP 테스트 계약
(`include_paths 밖 노드는 존재하면 안 됨`)과도 맞지 않는다.

이번 수정에서는 의미를 다음처럼 고정한다.

- `Build()` / `walkAndParse()`는 항상 **replace semantics**
- `include_paths`는 "무엇을 남길 것인가"를 제한하는 필터
- 따라서 빌드 시작 시 현재 namespace 그래프를 먼저 비우고,
  이번 실행에서 관측된 파일만 다시 적재한다.

이 방식은 코드리뷰에서 지적된 cross-file edge 유실 문제를 edge 보존으로 우회하지 않고,
**노드/엣지/annotation 전체를 입력 집합 기준 최종 상태로 수렴**시킨다.

### 2. unreadable / parse failure 정책

기존에는 파일 읽기/파싱이 실패하면 해당 파일만 skip하고 이전 그래프 상태를 남겼다.
하지만 replace semantics 기준에서는 이것이 stale data다.

선택한 정책:

- build start → graph reset
- 이후 unreadable/parse failure 파일은 단순히 재적재되지 않음
- 결과적으로 이전 상태는 제거되고, 현재 정상적으로 읽힌 파일만 그래프에 남음

즉, "마지막 정상 상태 유지"가 아니라 **"현재 관측 가능한 상태만 유지"** 정책이다.

### 3. Python docstring prefix 범위 축소

이전 수정은 `f`, `b`, `rb`, `fr`까지 모두 docstring처럼 수집/정규화했다.
하지만 이는 Python 런타임 의미의 문서화 문자열보다 범위가 넓다.

선택한 정책:

- 허용: plain triple-quoted string, `r`, `u`
- 비허용: `f`, `b`, `rb`, `fr` 등 실행/bytes 성격 literal

적용 지점:

1. `walker.collectDocstrings()` 단계에서 비허용 prefix는 docstring으로 수집하지 않음
2. `normalizer.stripPythonDocstringDelimiters()`도 동일한 허용 집합만 처리

이중 방어로 수집/정규화 의미를 일치시킨다.
