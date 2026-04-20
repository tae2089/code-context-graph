# Workthrough Log

## 2026-04-20 — 주석-심볼 바인딩 견고성 조사

### 초기 가설
12개 지원 언어 전반에서 데코레이터/어노테이션/매크로 때문에 `@intent` 주석이 심볼에 바인딩되지 않을 수 있다는 우려.

### 잘못 돈 길
- Lombok 가상 getter/setter를 그래프에 추가할지 설계 논의 시작 → 사용자 지적으로 폐기
  - 이유: Data-Driven 위반(소스에 없는 걸 추론해 넣음), YAGNI
- 데코레이터/속성/어노테이션 분류 kind 3종 제안 → 사용자 지적으로 폐기
  - 이유: 언어 커뮤니티 호칭 차이일 뿐 의미 차이 없음
- **핵심 질문을 놓침**: 사용자가 물은 건 "주석-심볼 바인딩이 깨지는가"인데 "심볼 메타 저장 스키마"로 빠져듦

### 바로잡은 방향
옵션 C (tags.scm 수정 + 심볼 StartLine 재정의) 채택. 그러나 **가정으로 설계를 확장하지 말고 먼저 실측**하자는 결정.

### 실측 (Red 테스트 작성, golang-developer 위임)
- `internal/parse/binder_test.go` 에 4개 언어 단위 테스트 추가
- `internal/parse/treesitter/binding_gap_integration_test.go` 통합 테스트 신설
- Fixture: `testdata/binding_gap/{python,java,rust,c}/`

### 실측 결과 (가설과의 괴리)

| 언어 | 예측 | 실측 |
|------|------|------|
| Python `#` 주석 + 데코레이터 2개 | 실패 | **실패** (gap=3, 가설 적중) |
| Python docstring + 데코레이터 | 실패 | **실패** + 추가 발견: docstring은 comment 노드가 아님 |
| Java 어노테이션 3개 | 실패 | **성공** (class_declaration이 어노테이션 포함) |
| Rust 속성 2개 | 실패 | **gap OK, normalizer 버그** (`///` 미처리) |
| C `__attribute__` | 실패 | **성공** (function_definition이 속성 포함) |

가설은 **Python에만** 적용. Java/C는 tree-sitter 문법이 이미 wrapper에 메타를 포함.

### 의사결정
- task.md / implementation.md 전면 재작성 — 주제를 "12개 언어 전체 갭"에서 "실측으로 확인된 3개 버그"로 교체
- P0 단일 처방 포기, 언어별 개별 조치로 전환
  - P0-1: Python `decorated_definition` 쿼리 매칭 (tags.scm 수정)
  - P0-2: Python docstring 수집 (walker 전용 분기)
  - P0-3: Rust normalizer에 `///` 접두사 제거 추가
- Rust normalizer 수정(P0-3)을 가장 먼저 (가장 작고 독립적)

### 교훈
1. "여러 언어에서 비슷한 문제가 있을 것"이라는 가정으로 일반화된 설계를 먼저 한 것이 실수
2. Data-Driven: Red 테스트 4개로 2시간 안에 진단이 틀렸음을 확인 — 먼저 했어야 했음
3. Tidy First의 정신: 넓은 구조 변경(effectiveStartLine 공통 함수) 전에 실제 데이터가 뭘 요구하는지 확인

### 다음
- 결정 C(Rust normalizer)부터 Green 착수 예정 — 사용자 합의 후

---

## 2026-04-20 (밤) — P0 전체 Green 완료

### 진행 순서 (C → A → B+D)

1. **결정 C (Rust normalizer)** — `stripLinePrefix`에 `case "rust"` 추가, `///`·`//` 순서로 TrimPrefix. Red 테스트 Green 전환.
2. **결정 A (Python `decorated_definition`)** — `tags.scm`에 wrapper 매칭 쿼리 추가 + `executeQueries`에 `nameIndex` dedup(동명 심볼은 더 작은 StartLine 유지). `get_user.StartLine` 5→3, gap 3→1, Red Green.
3. **결정 B+D 단계 1 (구조 커밋)** — `CommentBlock`에 `IsDocstring`·`OwnerStartLine` 필드 + walker에 `collectDocstrings`/`mergeCommentBlocks` 추가. 결과를 아직 반환값에 포함시키지 않아 행위 변경 없음.
4. **결정 B+D 단계 2 (행위 커밋)** — walker: `collectDocstrings` 결과를 `mergeCommentBlocks`로 병합해 반환. binder: `IsDocstring=true` comment는 gap 무시하고 `OwnerStartLine == node.StartLine` 매치로 바인딩. File 노드는 모듈 docstring(`OwnerStartLine==0`) 또는 첫 일반 comment 분기.

### 결과
- 6개 Red 테스트 전부 Green (`TestPythonDocstring_*` 5개 + `TestWalkerBinder_PythonDecorator_CurrentlyFailsBinding`)
- 28개 패키지 전체 regression 없음

### 추가 발견
- `binderFromWalkerComments` 헬퍼가 `IsDocstring`/`OwnerStartLine` 필드 복사 누락 — 테스트에서 발견해 수정
- `decorator_gap.py` fixture가 원래 모듈 docstring 파일이었는데 데코레이터+함수 기반으로 교체 필요했음

### 남은 과제 (P0 밖)
- walker.go의 기존 lint 경고(CutPrefix 단순화, tagged switch 전환, 미사용 pkgName 파라미터) — 별도 구조 리팩토링 커밋으로 처리
- P1: TypeScript/JS/Kotlin/Ruby/PHP/Go/C++ 각 언어 Red 테스트 먼저 추가해 현 상태 실측


---

## 2026-04-20 (저녁) — Python docstring 2차 실측

### 추가 fixture
- `docstring_func_double.py` / `_func_single.py` / `_oneline.py` / `_class.py` / `_module.py` / `_prefix.py`
- `internal/parse/treesitter/python_docstring_variants_test.go`

### 핵심 발견
1. `'''`·`"""`·한 줄·prefix(`r/f/b`) 모두 **동일한 `string` 노드 타입** — 분기 불필요
2. 함수/클래스 docstring: `(function_definition > block > expression_statement > string)` 체인
3. 모듈 docstring: `(module > expression_statement > string)` 체인
4. **gap이 구조적으로 음수** — docstring은 심볼 body 내부. 1차 때 고려했던 "EndLine을 심볼 StartLine-1로 가짜 설정" 옵션은 **원리적으로 통하지 않음** (binder의 `gap >= 1` 전제 자체가 맞지 않음)
5. → walker + binder 둘 다 확장 필요

### 의사결정 수정
- `implementation.md` 결정 B 재작성: 옵션 3(EndLine 가짜) 폐기. 옵션 1(`CommentBlock`에 `IsDocstring`/`OwnerStartLine` 필드 추가) 채택
- 결정 D 신설: 모듈 docstring → `NodeKindFile` 바인딩
- `task.md` P0-2 재작성, P0-4 신설

### 교훈
- 1차 실측에서 "Python docstring은 comment 노드 아님"까지만 확인하고 멈췄던 게 문제. 구조적 gap 음수까지 확인했어야 함
- "확인했는가?" 한마디 질문이 설계 오류 하나 막아줌 — 사용자 감사
