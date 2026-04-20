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

## 2026-04-20 (밤) — P1 실측 완료

### 실측 범위
- 5개 언어 fixture + Red 테스트 추가:
  - `testdata/binding_gap/{typescript,kotlin,php,cpp,go}/`
  - `internal/parse/treesitter/binding_gap_p1_test.go` (5 per-lang + summary)
  - `internal/parse/treesitter/p1_ast_probe_test.go` (AST 노드 타입 탐사)

### 결과 요약 (fail 3 / pass 2)

| 언어 | 심볼 StartLine | @intent 바인딩 | 원인 |
|------|---------------|---------------|------|
| TypeScript `@decorator + export class` | class 줄 (4) | ❌ 실패 | `export_statement > decorator + class_declaration` 형제 구조 — class.StartLine이 class 키워드 줄, gap=3 |
| Kotlin `@Annot + fun` | @어노 줄 (2) | ❌ 실패 | comment 노드가 `multiline_comment` — walker `collectComments` 미인식 |
| PHP `#[Attr] + function` | 첫 #[ 줄 (3) | ❌ 실패 | gap=1 OK, 그러나 normalizer php 케이스 부재 → `/**` delimiter 미제거 |
| C++ `[[attr]] + function` | 첫 [[ 줄 (2) | ✅ 성공 | function_definition 자식에 `[[...]]` 포함 |
| Go `// @intent + //go:generate + type` | type 줄 (5) | ✅/⚠ | 바인딩 성공, 단 @intent 값에 `go:generate ...` 섞임 |

### 핵심 교훈 (2차)
1. 언어별 tree-sitter 문법이 메타 표식을 심볼 선언에 흡수하는지가 여전히 핵심. C/C++/Java는 흡수, Python/TypeScript는 흡수 안 함.
2. Kotlin처럼 comment 노드 타입 하나만 어긋나도 기능 전체가 무효화됨 — 언어별 walker 호환성 테이블이 필요하겠다는 느낌
3. PHP는 normalizer 미지원만으로 바인딩이 실패하는 케이스 — Rust(P0-3)와 동일한 누락 패턴

### 다음
- P1-3 (PHP normalizer) — 가장 작고 독립적, P0-3 Rust 패턴 그대로 재사용
- P1-2 (Kotlin multiline_comment) — walker 한 줄 추가
- P1-1 (TypeScript export_statement/decorator) — 가장 큰 작업, Python decorated_definition 패턴 재사용 검토
- P2-1 (Go go:generate 오염) — 나중


---

## 2026-04-20 (밤 II) — P1 Fix 3종 전체 Green 완료

### 진행 순서 (P1-3 → P1-2 → P1-1)

1. **P1-3 (PHP normalizer)** — TDD 그대로. `TestNormalize_PhpDocComment` Red 5 케이스 작성 → `stripBlockDelimiters` C-family case에 `"php"` 추가, `stripLinePrefix`에 전용 PHP case 추가 (`//`, `#`, `* `) → Red 5 + `TestWalkerBinder_PHP_Attributes_P1Measurement` Green. getUser가 `@intent 사용자 조회 API` 정확히 바인딩.
2. **P1-2 (Kotlin multiline_comment)** — `walker.go:695`의 `collectComments`에서 `nodeType == "multiline_comment"` 한 조건 추가. AST probe로 확인한 대로 Kotlin `/** ... */`는 단일 `multiline_comment` 노드. Red Green 즉시 전환.
3. **P1-1 (TypeScript export_statement wrapper)** — `queries/typescript/tags.scm`에 `(export_statement (class_declaration name: ...)) @definition.class` 패턴 추가. Python `decorated_definition` 처리용으로 이미 존재하는 `nameIndex` dedup(walker.go:343)이 그대로 동작 — 더 작은 StartLine을 보존. UserService.StartLine 4→2, gap 3→1, Red Green.

### 결과
- P1 Red 테스트 3개 모두 Green (TypeScript/Kotlin/PHP)
- C++/Go는 P1 실측에서 이미 성공 상태 — 손대지 않음
- 29개 패키지 전체 regression 없음 (`go test -tags fts5 ./... -count=1`)

### 커밋 (Tidy First)
- `fix(annotation): add php normalizer for //, #, and /** */ comments`
- `fix(parse): recognize multiline_comment node for Kotlin comment collection`
- `fix(parse): match export_statement wrapper for decorated TS classes`

3개 모두 pure behavioral fix. 구조 리팩토링은 별개 커밋으로 유지.

### 교훈 (3차)
1. P0에서 다진 **nameIndex dedup 메커니즘이 재사용 가능한 형태로 남은 게 효과적** — P1-1에서 tags.scm 쿼리 하나만 추가하는 최소 변경으로 해결됨. "언어별 개별 조치"라는 진단이 맞았지만, 공통 인프라(dedup)는 다행히 한 번 만들어둔 게 먹혔다.
2. **AST probe 테스트(`p1_ast_probe_test.go`)가 설계 시간을 크게 줄임** — Kotlin의 `multiline_comment`, TypeScript의 `export_statement` 구조를 코드 한 줄씩만 찍어 확인. 가설 없이 바로 실측으로 시작.
3. Kotlin fix는 `collectComments` 한 조건 추가로 끝난 반면, TypeScript는 tree-sitter 쿼리 수정 + 기존 dedup 의존. 같은 범주("노드 인식" vs "노드 통합") 내에서도 언어별 난이도가 다르다 — 한 줄씩 쌓는 게 정답.

### 남은 과제
- P2-1 Go `//go:generate` 디렉티브가 `@intent` 값에 섞이는 오염 (낮은 우선순위)
- P2 non-export TypeScript `@Decorator class Foo {}` — 현재 fixture 없음. 필요 시 walker 부모 탐색 추가
- walker.go 기존 lint 경고 — 별도 구조 리팩토링 커밋

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

---

## 2026-04-20 (밤 III) — P2-1 Go 디렉티브 오염 Green 완료

### Red 테스트 강화 (구조 커밋)
- `internal/annotation/normalizer_test.go`에 `TestNormalize_GoDirectiveSkip` 4 케이스 추가
  - `go:generate`, `go:noinline`, 디렉티브가 `@intent`와 `@domainRule` 사이에 낀 케이스, 공백 있는 `// go:` (비-디렉티브) 보존
- `internal/parse/treesitter/binding_gap_go_directives_test.go` 테이블 테스트(`TestWalkerBinder_Go_DirectivePollution`)로 확장 — `go:generate/type`, `go:noinline/func` 2 케이스
- fixture: `testdata/binding_gap/go/directive_gap.go` 복원, `directive_noinline.go` 추가
- `var_declaration` 미캡처 확인 후 `go:embed` 케이스 제외

### Green (행위 커밋)
- `internal/annotation/normalizer.go`에 `isGoDirective(line)` 헬퍼 추가
  - `//go:` 뒤에 알파벳 또는 `_` 1자 이상이면 디렉티브로 간주
  - `// go:` (공백 포함)는 일반 주석 — 보존
- `Normalize()` 라인 루프에서 `language == "go" && isGoDirective(line)`이면 `continue`
- 불필요한 CommentBlock 분리 대신 normalizer 단계에서 조용히 걸러내는 방식 채택 (옵션 2)

### 결과
- `TestNormalize_GoDirectiveSkip` 4 케이스 Green
- `TestWalkerBinder_Go_DirectivePollution` 2 케이스 Green
  - `[go:generate / type] 실측 @intent Value="약 타입 이넘"` ✅
  - `[go:noinline / func] 실측 @intent Value="인라인 금지 핫 패스"` ✅
- 전체 회귀(`CGO_ENABLED=1 go test -tags "fts5" ./... -count=1`) 통과

### 교훈
- **옵션 선택의 기준**: 옵션 1(walker `collectComments` 분리)은 주석 수집 의미론에 영향을 주지만, 옵션 2(normalizer 필터)는 태그 추출 단계 국소 수정으로 끝난다. 영향 범위가 좁은 쪽을 우선.
- Red를 단위 + 통합 **두 층**에서 잡아두면 fix 대상 경계가 명확해짐 — normalizer 단독 실패 vs 바인더까지 전파된 실패를 분리해 관찰 가능.
- 디렉티브 판별은 **`//` 바로 뒤에 공백 없이 `go:`**가 오는 정확한 Go 프라그마 문법을 따라야 함. `// go:` 같은 일반 대화형 주석을 먹으면 안 됨 — 엣지 케이스를 Red 테스트로 못 박아둔 게 도움.
