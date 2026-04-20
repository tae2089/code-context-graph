# Task: 주석-심볼 바인딩 견고성 (데코레이터/어노테이션/속성/매크로 사이에 있을 때)

## 문제 정의

ccg는 소스 주석에서 DoodlinDoc 태그(`@intent`, `@domainRule` 등)를 뽑아 심볼 노드에 바인딩함.
심볼 위에 데코레이터·어노테이션·속성·매크로가 끼어있을 때 **주석→심볼 바인딩이 깨지는가**가 핵심 관심사.

## 실측 데이터 (2026-04-20)

Red 테스트로 4개 언어 각각 fixture 파싱한 결과 (`internal/parse/treesitter/binding_gap_integration_test.go`):

| 언어 | 심볼 StartLine 위치 | 바인딩 결과 | 원인 |
|------|---------------------|-------------|------|
| **Python** `#` 주석 + `@app.route`·`@login_required` + `def get_user` | `def` 줄 (5) | ❌ 실패 | gap=3 > maxGap(2). `function_definition` StartLine이 데코레이터 제외 |
| **Python** `"""docstring"""` + 데코레이터 + `def` | `def` 줄 | ❌ 실패 | tree-sitter가 `"""..."""`를 `comment` 노드로 인식하지 않음 — 아예 주석 수집에서 누락 |
| **Java** `/** @intent */` + `@Service @Transactional @RequiredArgsConstructor` + `class` | **첫 어노테이션 줄 (5)** | ✅ 성공 | `class_declaration`이 어노테이션을 자식으로 포함해 StartLine이 어노테이션 줄로 잡힘 (gap=1) |
| **Rust** `/// @intent` + `#[tokio::main]` + `#[allow(...)]` + `fn main` | `fn` 줄 (5) | ⚠ 부분 실패 | gap=2로 maxGap 이내, 바인딩은 됨. 그러나 `normalizer.go`에 `rust` 케이스 누락 → `///` 접두사 미제거 → `@intent` 태그 파싱 실패 |
| **C** `/** @intent */` + `__attribute__((...))` + `static inline int add` | **`__attribute__` 줄 (4)** | ✅ 성공 | `function_definition`이 `__attribute__`를 포함 (gap=1) |

**결론**: 원래 진단한 "maxGap 때문에 데코레이터 건너뜀"은 **Python에만** 해당. 언어마다 tree-sitter 노드가 메타 표식을 포함하는지가 달라서 **단일 처방 불가**. 언어별 실측 기반 개별 조치 필요.

또한 Python에서 **2번째 더 큰 문제** 발견: `"""docstring"""`은 comment 노드가 아니라 string 노드 → ccg 주석 수집기가 아예 못 잡음.

---

## 실행 계획 (우선순위 + TDD)

### P0 — 실측으로 확정된 실제 버그

- [x] Red 테스트 작성 (Python/Java/Rust/C)
  - 파일: `internal/parse/binder_test.go`, `internal/parse/treesitter/binding_gap_integration_test.go`
  - Fixture: `testdata/binding_gap/{python,java,rust,c}/`
  - 현재 모두 실패 상태 기록 완료

- [x] **P0-1. Python `function_definition` StartLine** — 완료
  - 기대: `def` 줄이 아닌 **첫 데코레이터 줄**을 심볼 StartLine으로 (Java/C와 동일 동작)
  - Green 후보 1: `tags.scm` (Python)에서 `decorated_definition`을 함수 매칭으로 교체
  - Green 후보 2: walker에서 `function_definition`의 부모가 `decorated_definition`이면 부모 StartLine 사용
  - Refactor: `class_definition`도 동일 처리 (`@dataclass` 등)
  - 영향 범위: Python golden.json 재생성 필요

- [x] Python docstring 2차 실측 (6 fixture + `python_docstring_variants_test.go`)
  - `'''`·`"""`·한 줄·`r/f/b` prefix 모두 동일한 `string` 노드 → prefix 분기 불필요
  - gap이 **구조적으로 음수** — EndLine 가짜 설정 우회 불가
  - walker + binder 둘 다 손대야 함

- [x] **P0-2. Python 함수/클래스 docstring 수집** (구조 + 행위, 2 커밋) — 완료
  - 단계 1 구조: `CommentBlock`에 `IsDocstring bool`·`OwnerStartLine int` 추가, walker에 `collectDocstrings` (조건: `expression_statement.namedChild` 유일 `string` + 부모가 `block`이면 `OwnerStartLine = 부모의 부모.StartLine`)
  - 단계 2 행위: walker가 `mergeCommentBlocks`로 병합해 반환, binder에 `if cb.IsDocstring { if OwnerStartLine == node.StartLine → 바인딩 }` 경로 추가
  - Red 테스트: `TestPythonDocstring_FuncDouble`, `FuncSingle`, `OneLine`, `Class` 4개 모두 Green

- [x] **P0-4. Python 모듈 docstring → File 노드 바인딩** (결정 D) — 완료
  - `collectDocstrings`에서 부모가 `module`이면 `OwnerStartLine=0`
  - binder의 `NodeKindFile` 경로: 모듈 docstring(`IsDocstring=true && OwnerStartLine==0`) 또는 첫 일반 comment 분기 (첫 매치 후 break)
  - Red 테스트: `TestPythonDocstring_Module` Green

- [x] **P0-3. Rust normalizer `///` 접두사 제거** — 완료
  - `internal/annotation/normalizer.go:23-108`의 `stripLinePrefix` 언어별 분기에 `rust` 케이스 누락
  - Green: `rust` 케이스 추가 — `///` (doc) 와 `//` (일반) 둘 다 제거
  - 추가 고려: `//!` (inner doc), `/** ... */`, `/*! ... */` 처리
  - Red 테스트: rust fixture에서 `ann.Tags`에 `@intent`가 들어있어야 통과

### P1 — 실측 결과 (2026-04-20 밤)

Red fixture + 통합 테스트 작성: `testdata/binding_gap/{typescript,kotlin,php,cpp,go}/`,
`internal/parse/treesitter/binding_gap_p1_test.go` + `p1_ast_probe_test.go`.

| 언어 | 심볼 StartLine | @intent 바인딩 | 원인 |
|------|---------------|---------------|------|
| **TypeScript** `@Injectable()` + `@Component(...)` + `export class` | `class` 줄 (4) | ❌ 실패 | `export_statement` 밑에 `decorator + class_declaration`이 형제. class_declaration.StartLine이 class 키워드 줄. Python decorated_definition 패턴과 유사 |
| **Kotlin** `@Composable @JvmStatic` + `fun` | `@Composable` 줄 (2) | ❌ 실패 | comment 노드 타입이 `multiline_comment` — walker `collectComments`가 `comment/line_comment/block_comment`만 인식. 주석 수집 자체가 누락됨 |
| **PHP** `#[Route(...)]` + `#[IsGranted(...)]` + `function` | `#[Route]` 줄 (3) | ❌ 실패 | StartLine 정상, gap=1 OK. 그러나 normalizer에 `php` 케이스 부재 → `/**` 블록 delimiter 미제거 → `@intent` 태그 파싱 실패 |
| **C++** `[[nodiscard]] [[deprecated]]` + `inline int divide` | `[[nodiscard]]` 줄 (2) | ✅ 성공 | C와 동일 — `function_definition`이 `[[...]]` 속성 포함. gap=1 |
| **Go** `// @intent` + `//go:generate` + `type Pill int` | `type Pill` 줄 (5) | ⚠ 부분 성공 | `//@intent`와 `//go:generate`가 동일 CommentBlock으로 병합. gap=1 OK, `@intent` 바인딩 성공. 단 태그 Value에 `go:generate stringer -type=Pill` 섞여 들어감 — 부가 오염 |

### P1-fix — 실측으로 확정된 3개 버그 + 1개 부작용

- [x] **P1-3. PHP normalizer 케이스 추가** — 완료
  - `stripBlockDelimiters` C-family case에 `"php"` 추가, `/**`, `/*` 처리
  - `stripLinePrefix`에 전용 `case "php"` 추가 — `//`, `#`, `* ` 모두 처리 (PHP는 C-style과 shell-style 주석 모두 지원)
  - Red 테스트: `TestWalkerBinder_PHP_Attributes_P1Measurement` + `TestNormalize_PhpDocComment` 5 케이스 Green

- [x] **P1-2. Kotlin `multiline_comment` 노드 수집** — 완료
  - `walker.go:695`의 `collectComments`에 `multiline_comment` 조건 추가 (한 줄)
  - AST probe 결과: Kotlin `/** ... */`는 `multiline_comment` 노드 타입으로 단일 노드
  - Red 테스트: `TestWalkerBinder_Kotlin_Annotations_P1Measurement` Green

- [x] **P1-1. TypeScript `export_statement + decorator + class_declaration` StartLine 보정** — 완료
  - `queries/typescript/tags.scm`에 `(export_statement (class_declaration ...)) @definition.class` wrapper 쿼리 추가
  - Python `decorated_definition` 처리 때 추가한 `nameIndex` dedup(walker.go:343)이 그대로 동작해 더 작은 StartLine(첫 데코레이터 줄) 보존
  - Red 테스트: `TestWalkerBinder_TypeScript_Decorators_P1Measurement` Green
  - ⚠ 남은 범위: non-export `@Component class Foo` 케이스(데코레이터가 class_declaration과 sibling, export_statement 밖). 현재 fixture 없음 — 필요 시 P2에서 walker 부모 탐색으로 처리

- [x] **P2-1. Go `//go:generate` 같은 디렉티브가 `@intent` 주석에 섞이는 문제** — 완료
  - 선택: 옵션 2 — normalizer Go 분기에서 `//go:<word>` 시작 줄 제거
  - 구현: `internal/annotation/normalizer.go`에 `isGoDirective(line)` 헬퍼 추가. `Normalize()` 라인 루프에서 `language == "go" && isGoDirective(line)`이면 `continue`로 건너뜀
  - 디렉티브 판별: `//go:` 뒤에 알파벳 또는 `_` 1자 이상. `// go:` (공백 포함)는 일반 주석으로 보존
  - Red 테스트:
    - `TestNormalize_GoDirectiveSkip` 4 케이스 (unit) — `go:generate`, `go:noinline`, 중간 낀 디렉티브, 공백 있는 `// go:` 보존
    - `TestWalkerBinder_Go_DirectivePollution` 2 케이스 (통합) — `go:generate/type`, `go:noinline/func`
  - Green: 전 회귀 통과

### P2 — DoodlinDoc 태그 별칭 매핑 (별도 작업) — 완료 (2026-04-20 밤 V)

이 작업은 "바인딩 견고성"과 독립. 파서 레벨에서 JSDoc/YARD 호환성 확대.

- [x] **P2-a. JSDoc `@returns` → `@return` 별칭** — 완료
  - `knownTags["returns"] = TagReturn` 매핑. Ordinal은 @return과 공유 카운터 사용
  - 테스트: `TestParse_ReturnsAlias`, `TestParse_ReturnAndReturnsAlias_SharedOrdinal`
  - 커밋: `eade3f0`

- [x] **P2-b. JSDoc `@typedef`, `@throws` 처리 정책 결정** — 완료
  - 정책: 새 TagKind 2개 추가 — `TagThrows`, `TagTypedef`
    - `@throws ExceptionType description` → Kind=TagThrows, Name=ExceptionType, Value=description (@param과 동일 규칙)
    - `@exception` = `@throws` Javadoc 공식 alias
    - `@typedef {Type} Name desc` → Kind=TagTypedef, Value=원문 보존 (구조 복잡, 검색/표시용)
  - 변경: `internal/model/annotation.go` 상수 추가 + parser knownTags + parseTagLine param-like 분기 확장
  - 테스트: `TestParse_Throws_WithType`, `TestParse_Throws_TypeOnly`, `TestParse_Exception_AliasOfThrows`, `TestParse_Typedef`
  - 커밋: `0591560`

- [x] **P2-c. YARD `@param [Type] name` 타입 부분 파싱** — 완료
  - 구조 변경: `model.DocTag`에 `Type string` 필드 추가 (별도 커밋 `0ff2511`)
  - 행위 변경:
    - `extractTypePrefix()` 헬퍼 — `[...]` / `{...}` balance 기반 분리, 중첩 지원
    - `parseTagLine`에서 param/return/throws는 value 앞의 type prefix를 먼저 추출 → `DocTag.Type`
    - 기존 `@param name desc` (type 없음) 하위호환 유지
  - 지원 문법: YARD `[String]`, `[Array<String>]`; JSDoc `{string}`, `{string|number}`
  - 테스트: 7개 (param 5 + return 2)
  - 커밋: `622eef5`

### P3 — 테스트·문서 정비

- [ ] Kotlin `testdata/eval/kotlin/Sample.kt`의 golden.json 배포 (누락 상태)
- [ ] Cross-language 통합 테스트 (모든 지원 언어에서 `@intent` 바인딩이 동일하게 동작하는지)

### P4 — 코드 리뷰 후속 조치 (2026-04-20 밤 IV)

- [x] **P4-1. indexer.go `IsDocstring`/`OwnerStartLine` 필드 전파 누락** — 완료
  - 리뷰에서 HIGH로 지적: P0-2 docstring 바인딩이 프로덕션 경로(`internal/service/indexer.go`)에서 미동작
  - Fix: `internal/service/indexer.go:127-131`의 인라인 변환 루프에 두 필드 추가 → 이후 구조적으로 `toBinderComments(...)` 헬퍼로 추출
  - 재발 방지: `internal/service/indexer_test.go` 4 케이스 (basic, docstring, non-docstring, empty)
  - 커밋: `97dfb3b` (fix) / `efda056` (refactor) / `72bac2c` (test)

- [x] **P4-2. `nameIndex` dedup이 동명 메서드를 오병합** — 완료
  - 리뷰에서 HIGH로 지적: `walker.go:343`의 nameIndex가 `name` 단독 키라서 같은 이름의 다른 클래스 메서드가 서로 소실
  - 실측: Python `Alpha.save`/`Beta.save` 와 TS `Alpha.render`/`Beta.render` 모두 하나만 수집됨 (Red 확인)
  - Fix: `rangesOverlap()` 헬퍼 추가 후 nameIndex dedup 분기에 overlap 가드 — 범위가 겹치지 않으면 별개 심볼로 새 노드 추가
  - Red→Green: `TestWalker_NameIndexDedup_{Python,TypeScript}_DupMethods`
  - 커밋: `7b0a7b8` (test) / `9e21c05` (fix)

---

## 설계 결정 (합의 필요)

### 결정 A. Python `function_definition` StartLine 방식
- 옵션 1: `tags.scm`에서 `decorated_definition` 매칭 (tree-sitter 쿼리 변경, 가장 깨끗)
- 옵션 2: walker에서 부모 노드 탐색 후 StartLine 조정 (쿼리 변경 없음, 로직 추가)
- **추천**: 옵션 1 — golden.json 재생성 부담은 있으나 Java/C 동작과 일관됨

### 결정 B. Python docstring 수집 전략
- 옵션 1: walker 주석 수집기에 Python 전용 분기 추가 (`function_definition` 첫 자식이 docstring인 경우)
- 옵션 2: binder에 "심볼 아래 방향" 바인딩 규칙 추가 (Python 한정)
- **추천**: 옵션 1 — 주석 수집 단계에서 docstring을 `CommentBlock`으로 승격시키면 binder는 기존 로직 그대로 (gap<1 조건 때문에 아래 방향 바인딩은 안 됨 → binder 규칙도 살짝 수정 필요할 수 있음)
- ⚠ binder `gap < 1` 조건은 Python 전용 규칙 추가 시 재검토 필요

### 결정 C. Rust `///` 주석 종류 처리 범위
- `///`, `//!`, `/** */`, `/*! */` 전부 지원할지, `///`·`//`만 우선 지원할지
- **추천**: 이번 P0에서는 `///`·`//`만 처리, inner doc(`//!`)은 P1

---

## 실행 규칙

- TDD: 각 항목 Red → Green → Refactor 엄수 (Red는 이미 작성 완료)
- Tidy First: 구조적 변경(쿼리 분리, normalizer 리팩터)과 행위 변경은 별도 커밋
- 테스트: `CGO_ENABLED=1 go test -tags "fts5" ./... -count=1`
- 한 번에 한 항목만 In Progress
- Python docstring 이슈(P0-2)는 설계 결정 B 합의 후 착수
