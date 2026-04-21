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

---

## 2026-04-20 (밤 IV) — P4: 코드 리뷰 후속 조치 (HIGH 2건)

### 촉발
독립 리뷰 에이전트에 P0+P1 전체 범위(10 commits) 재검토 요청 → 리뷰어가 Blocker 0, **HIGH 2건** 발견.

### HIGH #1 — indexer 필드 전파 누락 (P4-1)
- 증상: `internal/service/indexer.go:127-131`의 walker→binder 변환 루프에 `IsDocstring`/`OwnerStartLine` 미복사. P0-2/P0-4에서 추가한 Python docstring 바인딩이 **프로덕션 경로에선 전혀 실행 안 됨**. 통합 테스트는 `binderFromWalkerComments` 헬퍼로 우회해서 이 버그를 못 잡았음.
- Fix 흐름:
  1. `97dfb3b` — 즉시 필드 2개 추가 (행위 fix)
  2. `efda056` — 인라인 변환 루프를 `toBinderComments()` 헬퍼로 추출 (구조 리팩터, 행위 무변경)
  3. `72bac2c` — `indexer_test.go` 4 케이스 (basic/docstring/non-docstring/empty) — 재발 방지 단위 테스트
- 교훈: **production 경로와 test helper 경로가 다른 변환 코드를 각각 갖고 있으면 필드 추가 시 반드시 양쪽 동기화 체크**. 구조 리팩터로 변환 로직을 한 곳으로 모으면 재발 원천 차단.

### HIGH #2 — nameIndex dedup 동명 메서드 오병합 (P4-2)
- 증상: `walker.go:343`의 `nameIndex`가 `name` 단독 키. 같은 이름의 메서드가 서로 다른 클래스 본문에 있으면 두 번째 매칭이 첫 번째를 덮어써 한 쪽이 소실.
- 실측 (Red):
  - Python `Alpha.save`/`Beta.save` fixture — `save` 이름 노드 1개만 수집됨 (Alpha만 살아남음)
  - TS `Alpha.render`/`Beta.render` fixture — `render` 이름 노드 1개만 수집됨
- Fix: `rangesOverlap(aStart, aEnd, bStart, bEnd) bool` 헬퍼 추가 후 nameIndex dedup 분기에 가드 삽입. 범위가 겹칠 때만 같은 심볼의 중복 매칭으로 간주해 dedup하고, 겹치지 않으면 else 분기에서 별도 노드로 등록.
- Green: `TestWalker_NameIndexDedup_{Python,TypeScript}_DupMethods` 2 케이스 통과 (save/render 각각 2개 노드, StartLine 분리)
- 커밋: `7b0a7b8` (test) / `9e21c05` (fix)
- 교훈: **nameIndex 같은 느슨한 dedup은 "어떤 조건에서 같은 심볼인가"를 명시적으로 정의해야 한다**. decorated_definition + function_definition 래퍼는 범위가 겹치는데, 동명 메서드는 범위가 겹치지 않는다는 걸 구분 기준으로 삼으면 된다. key에 scope를 붙이지 않고도 overlap 체크만으로 해결.

### 리뷰어 피드백 반영 총괄
- HIGH 2건 모두 fix + 재발 방지 테스트
- Medium 1건 (`go:build`/`go:embed` 케이스) — P2-1 단위 테스트에 추가 (`6b57405`)
- Rust block doc, TS export default 등 low/nit는 현재 fixture 없음 — 추후 실측 시 처리

### 교훈 (리뷰 자체에 대해)
- **"동작한다"와 "테스트가 통과한다"는 다르다**. 테스트 헬퍼가 production 변환 코드를 우회하면 치명적 버그가 숨을 수 있다 — 이게 HIGH #1의 정체.
- 리뷰어가 `diff`만 보지 않고 **호출 체인 전체**를 추적해야 테스트 경로 분기를 잡아낼 수 있다. 리뷰 프롬프트에 "production 경로와 테스트 경로를 분리해서 확인"을 명시하는 게 효과적.

---

## 2026-04-20 (밤 V) — P2: JSDoc/YARD 호환 파서 확장

### 촉발
"task 진행 안된것들 확인" 후 사용자 지시로 P2 세 항목 순차 진행. 바인딩 견고성 파이프라인과는 독립된 파서 레벨 호환성 개선.

### P2-a — @returns JSDoc alias
- 변경: `knownTags["returns"] = model.TagReturn`. Ordinal 카운터는 `kind` 기준이라 @return과 자동 공유
- 테스트: `TestParse_ReturnsAlias` + 혼용 시 ordinal 공유 검증 `TestParse_ReturnAndReturnsAlias_SharedOrdinal`
- 커밋: `eade3f0`

### P2-b — @throws / @typedef 정책 결정
- **결정**: 새 TagKind 2개(`TagThrows`, `TagTypedef`) 도입. unknown 태그 warning으로 드롭하지 말고 1급 시민으로 저장.
- `@throws ExceptionType description` → Kind=TagThrows, Name=ExceptionType, Value=description (param과 동일 규칙)
- `@exception` = `@throws` Javadoc 공식 alias — knownTags에 중복 매핑
- `@typedef {Type} Name description` → Kind=TagTypedef, Value=전체 보존. JSDoc 전용 구조가 param/throws와 달라 세분화 대신 원문 유지가 실용적.
- 변경 범위: model 상수 2개 + parser knownTags 3엔트리 + parseTagLine param-like 분기에 `kind == TagThrows` 추가
- 테스트: 4개 (throws with type / type only / exception alias / typedef)
- 커밋: `0591560`

### P2-c — YARD/JSDoc 타입 prefix 파싱
- **구조 변경 먼저**(Tidy First): `model.DocTag`에 `Type string` 컬럼 추가. GORM AutoMigrate가 기존 테이블에 nullable 컬럼 자동 추가. 커밋 `0ff2511` — 단독으로는 행위 변화 없음 확인 후 분리 커밋.
- **행위 변경**: `extractTypePrefix(value) (typeStr, rest, ok)` 헬퍼 신설. `[...]`/`{...}` balance 기반으로 중첩 허용(`[Hash<Symbol, [String, Integer]>]` 같은 YARD 문법, `{string|number}` 같은 JSDoc union type).
  - `parseTagLine`에서 kind가 param/return/throws면 value 맨앞 type prefix 먼저 추출 → `tag.Type` 설정 후 남은 문자열에 기존 name/value 로직 적용
  - 타입 없는 기존 문법 `@param name desc`는 `extractTypePrefix`의 첫 바이트 체크에서 `ok=false` 반환 → 기존 경로 그대로 동작 (하위호환)
- 테스트: 7개
  - param: YARD `[String]`, JSDoc `{string}`, union `{string|number}`, generic `[Array<String>]`, plain (type 없음)
  - return: YARD `[String]`, JSDoc `{boolean}` (with @returns alias), plain
- 커밋: `622eef5`

### 결과
- 전 패키지 회귀 통과 (annotation 29 tests, 전체 pkg 27개 ok)
- DocTag.Type 활용은 추후 검색/표시 UI에서 쓸 수 있도록 저장만 준비 (이번 PR은 파서 단계까지)

### 교훈
- **정책 결정이 필요한 항목은 "옵션 분기 + 추천안 + 근거"를 먼저 task.md에 적고 그대로 실행**. P2-b의 "처리 정책 결정"을 새 TagKind로 낙찰시키는 이유(unknown warning 노이즈 + 검색 가능)를 커밋 메시지와 task.md 양쪽에 남기면 나중에 다시 논의할 필요 없음.
- **호환성 개선은 하위호환 테스트를 "plain 경로 여전히 동작" 형태로 명시적으로 고정**. P2-c `TestParse_Param_PlainStillWorks` / `TestParse_Return_PlainStillWorks`가 바로 이 역할. 미래에 extractTypePrefix가 리팩터되어도 이 두 테스트가 regression 안전망.
- **Tidy First 실천 포인트**: P2-c에서 `DocTag.Type` 추가(구조)와 `extractTypePrefix` 로직(행위)을 두 커밋으로 분리. 구조 커밋에서 전 테스트 통과를 확인해 "행위 무변경"을 증명한 뒤 행위 커밋을 쌓음. 나중에 bisect 시 원인 분리가 쉬움.

### 리뷰 대응 (밤 V-2)

독립 리뷰 결과 **Blocker 1 + Important 5 + Minor 4** 지적. 주요 대응:

- **Blocker #1 (MCP 노출 누락)** — `internal/mcp/handler_query.go:165-170`의 tag 직렬화에 `"type"` 키 부재. DocTag.Type은 DB에 저장되지만 get_annotation 응답에 실리지 않아 외부 사용 경로 차단. `TestHandler_GetAnnotation_ExposesDocTagTypeField` Red 테스트 추가 → "type" 키 추가로 Green. 커밋 `6dde17b`.

- **Important #2 (사일런트 드롭)** — `@param [Type]` / `@throws [IOException]` 처럼 type만 있고 name이 없을 때 기존 코드가 `return nil, ""`로 조용히 드롭. 디버깅 어려움. `return nil, tagName` 으로 변경해 warning slice에 싣고, 호출자가 unknown 태그 메시지와 동일하게 받아볼 수 있게 함. 커밋 `e77c39c`.

- **Important #5 (컬럼 크기 부족)** — `DocTag.Type`을 `size:128`에서 `type:text`로 완화. TypeScript/JSDoc 복합 타입 수백 바이트 대비. 구조 변경 단독 커밋 `f19478c`로 분리.

- **Important #6 (멀티라인 상호작용 미검증)** — 현재 동작을 명시적으로 테스트로 고정: 첫 줄에 type만 있고 name이 continuation에 있으면 드롭 + warning (파서 설계상 continuation은 Value로만 붙음), 정상 케이스(type+name 첫 줄, description continuation)는 정상 동작. 테스트 2건으로 계약 박음.

- **Minor #7/#8/#10** — extractTypePrefix 주석에 "mixed bracket nesting 비지원" 명시, 중첩 대괄호 `[Hash<Symbol, [String, Integer]>]` 실측 테스트 추가, typedef는 extractTypePrefix를 거치지 않는다는 명시적 assertion 추가.

### 교훈 2
- **외부 노출 경로를 리뷰 시 함께 체크해야 한다**. 파싱/저장 로직을 추가했을 때 "DB까지 갔는가"가 아니라 "외부 소비자(MCP/CLI/HTTP)에게 전달되는가"를 확인해야 완결. 리뷰가 이걸 잡아내서 Blocker로 격상시킨 게 결정적.
- **사일런트 드롭은 항상 의심**. 코드에서 `return nil, ""` 같은 형태로 데이터가 버려지는 곳을 찾으면 거의 항상 warning 경로를 추가하는 게 맞다. 특히 사용자 입력(주석 텍스트)을 처리하는 파서는 "왜 이 태그가 안 잡히지?" 의 답을 로그로 줄 수 있어야 한다.

## 밤 VI — P3 테스트 정비 (2026-04-20)

### P3-1. Kotlin golden.json 배포 확인
- task.md에 "누락"으로 기록되어 있었지만 실측 결과 `testdata/eval/kotlin/Sample.kt.golden.json`은 이미 존재(커밋 `ccc95f8`).
- `ccg eval --suite parser` 실행 → Kotlin 100%/100%/F1=1.0 (Node/Edge) Green 확인.
- task.md 항목을 "완료 + 실측 결과" 형태로 재기술.

### P3-2. Cross-language @intent 바인딩 계약 테스트
- 신규 파일: `internal/parse/treesitter/binding_gap_cross_language_test.go`
- 12개 지원 언어(go/python/typescript/java/c/rust/cpp/javascript/ruby/kotlin/php/lua) 각각에 대해 최소 케이스 "심볼 위 한 줄 @intent 주석 → binding에 @intent 태그 존재"를 table-driven으로 검증.
- 결과: **11 PASS + 1 SKIP**
  - PASS: Go/Python/TypeScript/Java/C/Rust/C++/JavaScript/Ruby/Kotlin/PHP
  - SKIP: Lua — tree-sitter-lua의 `comment`/`function_statement` 노드가 선행 공백/주석을 흡수해 gap이 항상 0으로 계산됨. walker 레벨 보정 필요 (별도 이슈로 분리).
- Rust 관찰: `///` line_comment가 trailing newline까지 포함해 EndLine이 다음 줄로 확장 → 주석과 선언 사이에 빈 줄을 둬야 gap>=1을 만족. fixture 소스에 주석과 함께 명시.
- 커밋 대상: test 파일 1개 + task.md 업데이트.

### 교훈 3
- **언어별 tree-sitter AST 특성은 단일 계약 테스트에서 drift로 드러난다**. Go/Java 같이 단순한 라인 계산을 기대한 케이스가 Rust/Lua에서 각자 다른 이유로 실패 — 각 grammar의 comment/statement boundary 흡수 규칙을 모른 채 "그냥 되겠지"로 코드를 짜면 회귀가 잠재된다. Cross-lang 계약 테스트는 이런 흡수 규칙의 차이를 수면으로 끌어올린다.
- **SKIP은 실패의 회피가 아니라 명시적 문서화 수단**. Lua 바인딩이 안 되는 건 walker 버그지만 스코프 밖이므로, 테스트를 지우지 말고 skipReason으로 원인/해결 방향을 박아 두면 미래의 엔지니어(또는 과거의 나)가 바로 원인을 짚어낼 수 있다. "테스트가 왜 없지?" 보다 "SKIP 사유에 뭐라고 적혀 있지?"가 훨씬 빠르다.

### 리뷰 대응 (밤 VI-2)

독립 리뷰: **Blocker 2 + Important 4 + Minor 4** 지적. 그중 MEDIUM 이상을 단일 커밋 `5af7446`로 정리.

- **Blocker #1 (Rust blank-line workaround)** — `///` 뒤에 빈 줄을 둔 Green 케이스는 유지하되, 빈 줄 없는 자연스러운 Rust 주석 패턴을 `expectBound=false` **Red 케이스**로 추가(`Rust_DocComment_Function_NoBlankLine`). walker의 line_comment trailing-newline quirk를 회피하지 않고 "현재 상태로 고정". 구현이 고쳐져 우연히 바인딩되면 Red 위반으로 실패 → 승격 강제.
  - walker.go를 전역 수정하려 했으나 merge된 comment block의 EndLine까지 줄여 `TestWalkerBinder_RustAttribute_CurrentlyFailsBinding`의 gap=2 계약을 깨뜨림. 전역 보정 대신 테스트 범위에서 명시적 Red로 전환.
- **Blocker #2 (Lua SKIP 부정확)** — `t.Skip`을 제거하고 `expectBound=false` Red로 승격. skipReason 설명도 "선행 newline 포함/선행 공백 흡수"라는 정확한 quirk 쌍으로 교정.
- **Important #1 (Kind 미검증)** — `expectedKind model.NodeKind` 필드 추가. 파싱/바인딩 양쪽에서 Name과 Kind를 동시에 매칭. 동명 심볼 false-positive 방지.
- **Important #3 (진단 불명확)** — 파싱 단계(Phase 1)와 바인딩 단계(Phase 2)를 분리. "심볼 파싱 실패(파서/LangSpec 회귀)"와 "심볼은 파싱됐지만 바인딩 누락(binder/walker 회귀)"의 실패 메시지가 달라 원인 분리가 빠름.
- **Important #4 (PHP `<?php` 필요성)** — 인라인 주석으로 "태그 없으면 text 노드 취급되어 함수가 심볼로 안 뜬다"는 이유 명시.
- **Minor #1 (lang 필드 중복)** — `tc.lang` 제거하고 `tc.spec.Name`으로 통일. 진실의 단일 원천.

테스트: 13개 subtest(11 Green + 2 Red) 전부 통과. 전체 parse 스위트 green.

### 교훈 4
- **전역 보정이 기존 Red 테스트를 깨면, 테스트 스코프에서 명시적 Red로 전환하는 게 더 낫다**. walker.collectComments의 trailing-newline 보정은 single line_comment만을 고쳐야 하는데 merge된 block까지 같이 줄여 gap=2 계약을 위반함. "코드 수정 전에 기존 Red 테스트가 무엇을 고정하고 있는지" 확인하고, 수정 스코프가 겹치면 테스트 레벨 표현(expectBound=false + redReason)로 일단 "현상 고정"한 뒤 나중에 walker 보정과 함께 승격.
- **SKIP보다 Red 계약이 회귀 탐지력이 높다**. Skip은 "그냥 안 돈다"로 눈에 안 띄지만, Red 계약(expectBound=false)은 구현이 우연히 고쳐져도 즉시 실패 → 엔지니어가 Green 승격을 안 하고 방치하는 실수를 잡는다.
- **Kind + Name 동시 매칭으로 false-positive를 선제 차단**. 심볼 이름만으로 매칭하면 다른 종류(변수, 타입)가 동명이면 엉뚱한 binding을 잡을 수 있음. LangSpec/grammar가 변경될 때의 예기치 않은 동작을 리뷰어 1회 검토로 끝내지 않고 테스트 구조에 박아 두는 게 안전.

---

## 2026-04-21 — Python prefix docstring `doc_tags` 회귀

### Red
- `testdata/binding_gap/python/docstring_prefix.py`를 `r/f/b/rb/fr/u` prefix 케이스까지 확장
- `internal/annotation/normalizer_test.go`에 prefix normalize Red 테스트 추가
- `internal/parse/treesitter/python_docstring_prefix_binding_test.go`에 실제 walker→binder 경로 바인딩 Red 테스트 추가
- 실패 확인:
  - normalizer가 `r"""..."""` 등 prefix를 제거하지 못함
  - binder 결과에서 `foo` 함수의 `@intent`가 비어 있음

### Green 설계
- 원인: docstring 수집은 정상이나 Python `stripBlockDelimiters()`가 따옴표 앞 prefix를 모름
- 수정: normalizer에 `stripPythonStringPrefix()` 추가 후, delimiter 제거 전에 파싱용 문자열만 prefix 제거
- 범위 최소화: walker/binder/원본 CommentBlock.Text 불변

---

## 2026-04-21 — incremental rebuild stale node 정리

### 배경
- 사용자 요청: `internal/service/indexer.go` incremental rebuild 경로에서 `DeleteNodesByFile`을 호출해 stale node를 정리하도록 TDD로 구현.

### 컨텍스트 확인
- `internal/store/store.go`에 `DeleteNodesByFile(ctx, filePath string) error` 계약이 이미 존재함 확인.
- `internal/store/gormstore/gormstore.go` 실측 결과 구현도 이미 존재.
  - namespace/file_path 기준 node id 조회
  - 관련 edge, doc_tags, annotation, node cascade 삭제
- 실제 누락 지점은 `GraphService.Build()`의 파일별 트랜잭션이었음. 현재는 `UpsertNodes`만 수행해 삭제된 선언이 영구 잔존.

### Red
- `internal/service/indexer_test.go`에 `TestBuild_IncrementalRebuild_RemovesStaleNodesBeforeUpsert` 추가.
- 시나리오:
  1. `sample.go`에 `Keep`, `Remove` 2개 함수로 1차 빌드
  2. 같은 파일을 `Keep`만 남기도록 축소 후 2차 빌드
  3. `GetNodesByFile("sample.go")`의 function 이름이 `Keep`만 남아야 함
- 실제 Red 결과: 2차 빌드 후도 `got=[Keep Remove]` → stale node 재현 성공.

### Green
- `internal/service/indexer.go` 파일 처리 트랜잭션 시작 직후 `txStore.DeleteNodesByFile(ctx, relPath)` 호출 추가.
- 그 다음 `UpsertNodes(ctx, nodes)` 실행하도록 순서 변경.
- 이유: 파일 재빌드는 merge가 아니라 replace semantics여야 하므로 이전 파일 노드를 먼저 제거해야 함.

### 결과
- 신규 Red 테스트 Green 전환 확인.
- 이번 변경으로 gormstore 구현 추가는 불필요했음. 기존 구현 재사용으로 해결.

### 교훈
- `Upsert`만으로는 절대 "삭제"를 표현할 수 없다. incremental rebuild가 사실상 file-level replace이면 delete-first가 계약이어야 한다.
- 저장소 계층에 이미 올바른 primitive(`DeleteNodesByFile`)가 있어도 orchestration 계층에서 호출하지 않으면 기능은 없는 것과 같다. 이런 버그는 단위 테스트보다 시나리오 기반 서비스 테스트가 더 잘 잡는다.
