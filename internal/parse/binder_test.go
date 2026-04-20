package parse

import (
	"testing"

	"github.com/tae2089/code-context-graph/internal/model"
)

func TestBinder_FunctionWithPrecedingComment(t *testing.T) {
	b := NewBinder()
	comments := []CommentBlock{
		{StartLine: 1, EndLine: 2, Text: "사용자 인증을 수행한다\n@param username ID"},
	}
	nodes := []model.Node{
		{Name: "AuthenticateUser", Kind: model.NodeKindFunction, StartLine: 3, EndLine: 10},
	}

	bindings := b.Bind(comments, nodes, "go", nil)
	if len(bindings) != 1 {
		t.Fatalf("expected 1 binding, got %d", len(bindings))
	}
	if bindings[0].Node.Name != "AuthenticateUser" {
		t.Errorf("Node.Name = %q", bindings[0].Node.Name)
	}
	if bindings[0].Annotation.Summary != "사용자 인증을 수행한다" {
		t.Errorf("Summary = %q", bindings[0].Annotation.Summary)
	}
}

func TestBinder_ClassWithPrecedingComment(t *testing.T) {
	b := NewBinder()
	comments := []CommentBlock{
		{StartLine: 5, EndLine: 5, Text: "유저 서비스 클래스"},
	}
	nodes := []model.Node{
		{Name: "UserService", Kind: model.NodeKindClass, StartLine: 6, EndLine: 20},
	}

	bindings := b.Bind(comments, nodes, "go", nil)
	if len(bindings) != 1 {
		t.Fatalf("expected 1 binding, got %d", len(bindings))
	}
	if bindings[0].Node.Name != "UserService" {
		t.Errorf("Node.Name = %q", bindings[0].Node.Name)
	}
}

func TestBinder_NoComment(t *testing.T) {
	b := NewBinder()
	nodes := []model.Node{
		{Name: "Foo", Kind: model.NodeKindFunction, StartLine: 5, EndLine: 10},
	}

	bindings := b.Bind(nil, nodes, "go", nil)
	if len(bindings) != 0 {
		t.Errorf("expected 0 bindings, got %d", len(bindings))
	}
}

func TestBinder_CommentNotAdjacent(t *testing.T) {
	b := NewBinder()
	comments := []CommentBlock{
		{StartLine: 1, EndLine: 1, Text: "멀리 떨어진 주석"},
	}
	nodes := []model.Node{
		{Name: "Foo", Kind: model.NodeKindFunction, StartLine: 5, EndLine: 10},
	}

	// sourceLines nil, gap=4 → 보수적 폴백으로 바인딩 안 됨
	bindings := b.Bind(comments, nodes, "go", nil)
	if len(bindings) != 0 {
		t.Errorf("expected 0 bindings (gap=4, nil sourceLines), got %d", len(bindings))
	}
}

func TestBinder_MultipleDeclarations(t *testing.T) {
	b := NewBinder()
	comments := []CommentBlock{
		{StartLine: 1, EndLine: 1, Text: "첫 번째 함수"},
		{StartLine: 6, EndLine: 6, Text: "두 번째 함수"},
	}
	nodes := []model.Node{
		{Name: "FuncA", Kind: model.NodeKindFunction, StartLine: 2, EndLine: 4},
		{Name: "FuncB", Kind: model.NodeKindFunction, StartLine: 7, EndLine: 9},
	}

	bindings := b.Bind(comments, nodes, "go", nil)
	if len(bindings) != 2 {
		t.Fatalf("expected 2 bindings, got %d", len(bindings))
	}
	if bindings[0].Node.Name != "FuncA" {
		t.Errorf("first binding Node.Name = %q", bindings[0].Node.Name)
	}
	if bindings[1].Node.Name != "FuncB" {
		t.Errorf("second binding Node.Name = %q", bindings[1].Node.Name)
	}
}

// --- 데코레이터/어노테이션 gap 회귀 테스트 ---
//
// 아래 단위 테스트들은 maxGap=3 경계를 검증합니다.
//
// maxGap=3 으로 변경되어 gap=3 (데코레이터/속성 최대 2줄) 은 이제 바인딩 성공합니다.
// gap=4 이상은 여전히 바인딩되지 않습니다.
//
// 역사: maxGap=2 시절 gap=3 은 실패했으므로 `_CurrentlyFails` 이름이 붙어 있었으나,
// walker line_comment 정규화 + maxGap=3 상향으로 이제 성공 케이스가 됩니다.

// TestBinder_PythonDecoratorGap_BindsWithinMaxGap 는 Python 데코레이터 2개가 있을 때
// docstring의 EndLine과 def 키워드 StartLine 사이 gap=3 이 maxGap(3) 이내이므로
// 바인딩이 성공함을 검증합니다.
//
// fixture 기준:
//   Line 1-4: docstring ("""...""")
//   Line 5:   @app.route('/api/user')
//   Line 6:   @login_required
//   Line 7:   def get_user():   ← tree-sitter StartLine 예상
//
// gap = 7 - 4 = 3 ≤ maxGap(3) → 바인딩 성공
func TestBinder_PythonDecoratorGap_BindsWithinMaxGap(t *testing.T) {
	b := NewBinder()

	// 데코레이터가 있는 Python 함수: 주석 끝줄=4, 함수 StartLine=7
	comments := []CommentBlock{
		{StartLine: 1, EndLine: 4, Text: `"""
@intent 사용자 조회 엔드포인트
@domainRule 인증 필요
"""`},
	}
	nodes := []model.Node{
		{Name: "get_user", Kind: model.NodeKindFunction, StartLine: 7, EndLine: 8},
	}

	sourceLines := []string{
		`"""`,
		`@intent 사용자 조회 엔드포인트`,
		`@domainRule 인증 필요`,
		`"""`,
		`@login_required`,
		`@require_role("admin")`,
		`def get_user():`,
		`    pass`,
	}

	bindings := b.Bind(comments, nodes, "python", sourceLines)

	if len(bindings) != 1 {
		t.Errorf("Python 데코레이터 gap=3: 바인딩 1개 예상인데 %d개 반환됨", len(bindings))
	}
}

// TestBinder_JavaAnnotationGap_PassthroughBinds 는 Java 어노테이션 3개가 주석과 선언 사이에
// 있을 때, Look-Between 방식으로 passthrough되어 바인딩이 성공함을 검증합니다.
func TestBinder_JavaAnnotationGap_PassthroughBinds(t *testing.T) {
	b := NewBinder()

	comments := []CommentBlock{
		{StartLine: 1, EndLine: 4, Text: `/**
 * @intent 사용자 서비스
 * @domainRule 트랜잭션 필수
 */`},
	}
	nodes := []model.Node{
		{Name: "UserService", Kind: model.NodeKindClass, StartLine: 8, EndLine: 9},
	}

	sourceLines := []string{
		`/**`,
		` * @intent 사용자 서비스`,
		` * @domainRule 트랜잭션 필수`,
		` */`,
		`@Service`,
		`@Transactional`,
		`@RequiredArgsConstructor`,
		`public class UserService {`,
		`}`,
	}

	bindings := b.Bind(comments, nodes, "java", sourceLines)

	if len(bindings) != 1 {
		t.Errorf("Java 어노테이션 passthrough: 바인딩 1개 예상인데 %d개 반환됨", len(bindings))
	}
}

// TestBinder_RustAttributeGap_BindsWithinMaxGap 는 Rust 속성 2개가 있을 때
// doc comment 끝줄과 fn 키워드 StartLine 사이 gap=3 이 maxGap(3) 이내이므로
// 바인딩이 성공함을 검증합니다.
//
// fixture (attribute_gap.rs) 기준:
//   Line 1: /// @intent 비동기 main 진입점
//   Line 2: /// @sideEffect 런타임 초기화
//   Line 3: #[tokio::main]
//   Line 4: #[allow(dead_code)]
//   Line 5: async fn main() {   ← tree-sitter StartLine
//
// gap = 5 - 2 = 3 ≤ maxGap(3) → 바인딩 성공
func TestBinder_RustAttributeGap_BindsWithinMaxGap(t *testing.T) {
	b := NewBinder()

	comments := []CommentBlock{
		{StartLine: 1, EndLine: 2, Text: "/// @intent 비동기 main 진입점\n/// @sideEffect 런타임 초기화"},
	}
	nodes := []model.Node{
		{Name: "main", Kind: model.NodeKindFunction, StartLine: 5, EndLine: 6},
	}

	sourceLines := []string{
		`/// @intent 비동기 main 진입점`,
		`/// @sideEffect 런타임 초기화`,
		`#[tokio::main]`,
		`#[allow(dead_code)]`,
		`async fn main() {`,
		`}`,
	}

	bindings := b.Bind(comments, nodes, "rust", sourceLines)

	if len(bindings) != 1 {
		t.Errorf("Rust 속성 gap=3: 바인딩 1개 예상인데 %d개 반환됨", len(bindings))
	}
}

// TestBinder_CAttributeGap_BindsWithinMaxGap 는 C __attribute__ 속성 2줄이 있을 때
// Doxygen 주석 끝줄과 함수 StartLine 사이 gap=3 이 maxGap(3) 이내이므로
// 바인딩이 성공함을 검증합니다.
//
// fixture (attribute_gap.c) 기준:
//   Line 1-3: /** ... */ Doxygen
//   Line 4:   __attribute__((always_inline))
//   Line 5:   __attribute__((nonnull))
//   Line 6:   static inline int add(int a, int b) {   ← tree-sitter StartLine 예상
//
// gap = 6 - 3 = 3 ≤ maxGap(3) → 바인딩 성공
func TestBinder_CAttributeGap_BindsWithinMaxGap(t *testing.T) {
	b := NewBinder()

	comments := []CommentBlock{
		{StartLine: 1, EndLine: 3, Text: "/**\n * @intent 항상 인라인되는 덧셈\n */"},
	}
	nodes := []model.Node{
		{Name: "add", Kind: model.NodeKindFunction, StartLine: 6, EndLine: 8},
	}

	sourceLines := []string{
		`/**`,
		` * @intent 항상 인라인되는 덧셈`,
		` */`,
		`__attribute__((always_inline))`,
		`__attribute__((nonnull))`,
		`static inline int add(int a, int b) {`,
		`  return a + b;`,
		`}`,
	}

	bindings := b.Bind(comments, nodes, "c", sourceLines)

	if len(bindings) != 1 {
		t.Errorf("C __attribute__ gap=3: 바인딩 1개 예상인데 %d개 반환됨", len(bindings))
	}
}

// TestBinder_GapWithCodeBetween_NoBinding 는 주석과 선언 사이에 실제 코드가 있으면
// 바인딩이 차단됨을 검증합니다.
func TestBinder_GapWithCodeBetween_NoBinding(t *testing.T) {
	b := NewBinder()

	comments := []CommentBlock{
		{StartLine: 1, EndLine: 3, Text: "/**\n * @intent 갭 초과 테스트\n */"},
	}
	nodes := []model.Node{
		{Name: "BigClass", Kind: model.NodeKindClass, StartLine: 8, EndLine: 20},
	}

	sourceLines := []string{
		`/**`,
		` * @intent 갭 초과 테스트`,
		` */`,
		`int x = 42;`,
		`doSomething();`,
		``,
		``,
		`public class BigClass {`,
		`}`,
		``, ``, ``, ``, ``, ``, ``, ``, ``, ``, ``,
	}

	bindings := b.Bind(comments, nodes, "java", sourceLines)

	if len(bindings) != 0 {
		t.Errorf("사이에 코드가 있으면 바인딩 0개 예상인데 %d개 반환됨", len(bindings))
	}
}


// =============================================================================
// Look-Between 동적 바인딩 테스트
// =============================================================================

// TestBinder_LookBetween_BlankLinesOnly_Binds 는 주석과 선언 사이에 빈 줄만 있으면
// gap 크기에 관계없이 바인딩이 성공함을 검증합니다.
//
// fixture:
//
//	Line 1: // @intent 빈 줄 사이 바인딩
//	Line 2: (빈 줄)
//	Line 3: (빈 줄)
//	Line 4: (빈 줄)
//	Line 5: func MyFunc() {   ← gap = 5 - 1 = 4
//
// 사이 구간(line 2~4)이 전부 빈 줄 → 바인딩 성공
func TestBinder_LookBetween_BlankLinesOnly_Binds(t *testing.T) {
	b := NewBinder()

	comments := []CommentBlock{
		{StartLine: 1, EndLine: 1, Text: "@intent 빈 줄 사이 바인딩"},
	}
	nodes := []model.Node{
		{Name: "MyFunc", Kind: model.NodeKindFunction, StartLine: 5, EndLine: 10},
	}
	sourceLines := []string{
		"// @intent 빈 줄 사이 바인딩", // line 1
		"",                           // line 2
		"",                           // line 3
		"",                           // line 4
		"func MyFunc() {",            // line 5
	}

	bindings := b.Bind(comments, nodes, "go", sourceLines)

	if len(bindings) != 1 {
		t.Fatalf("빈 줄만 있는 gap=4: 바인딩 1개 예상인데 %d개 반환됨", len(bindings))
	}
	if bindings[0].Node.Name != "MyFunc" {
		t.Errorf("Node.Name = %q, want MyFunc", bindings[0].Node.Name)
	}
}

// TestBinder_LookBetween_CodeBetween_NoBinding 은 주석과 선언 사이에 코드가 있으면
// 바인딩이 발생하지 않음을 검증합니다.
//
// fixture:
//
//	Line 1: // @intent 이 주석은 바인딩 안 됨
//	Line 2: var x = 42
//	Line 3: func MyFunc() {   ← gap = 3 - 1 = 2
//
// 사이 구간(line 2)에 코드가 있음 → 바인딩 실패
func TestBinder_LookBetween_CodeBetween_NoBinding(t *testing.T) {
	b := NewBinder()

	comments := []CommentBlock{
		{StartLine: 1, EndLine: 1, Text: "@intent 이 주석은 바인딩 안 됨"},
	}
	nodes := []model.Node{
		{Name: "MyFunc", Kind: model.NodeKindFunction, StartLine: 3, EndLine: 10},
	}
	sourceLines := []string{
		"// @intent 이 주석은 바인딩 안 됨", // line 1
		"var x = 42",                    // line 2 - 코드!
		"func MyFunc() {",               // line 3
	}

	bindings := b.Bind(comments, nodes, "go", sourceLines)

	if len(bindings) != 0 {
		t.Errorf("코드가 사이에 있는 경우: 바인딩 0개 예상인데 %d개 반환됨", len(bindings))
	}
}

// TestBinder_LookBetween_DecoratorsOnly_Binds 는 주석과 선언 사이에 데코레이터/어노테이션만
// 있으면 바인딩이 성공함을 검증합니다 (데코레이터는 선언의 일부이므로 바인딩 허용).
//
// fixture:
//
//	Line 1-3: """@intent 사용자 조회"""
//	Line 4: @app.route('/api/user')
//	Line 5: @login_required
//	Line 6: def get_user():   ← gap = 6 - 3 = 3
//
// 사이 구간(line 4~5)이 데코레이터 → 빈 줄이 아니지만 코드임
// Look-Between에서는 "코드가 있으면 바인딩하지 않음"이 아니라
// "빈 줄만 있으면 바인딩"이 원칙이므로, gap=1인 경우만 무조건 바인딩.
// gap>1이면서 사이에 비-공백 라인이 있으면 바인딩 안 됨.
// BUT: 실제로 Python은 decorated_definition으로 StartLine이 데코레이터를 포함하므로
// gap=1이 됨. 이 테스트는 gap=1인 인접 케이스를 확인.
func TestBinder_LookBetween_Adjacent_Gap1_Binds(t *testing.T) {
	b := NewBinder()

	comments := []CommentBlock{
		{StartLine: 1, EndLine: 2, Text: "@intent 인접 바인딩"},
	}
	nodes := []model.Node{
		{Name: "MyFunc", Kind: model.NodeKindFunction, StartLine: 3, EndLine: 10},
	}
	sourceLines := []string{
		"// @intent 인접 바인딩", // line 1
		"// (continued)",      // line 2
		"func MyFunc() {",     // line 3
	}

	bindings := b.Bind(comments, nodes, "go", sourceLines)

	if len(bindings) != 1 {
		t.Fatalf("gap=1 인접: 바인딩 1개 예상인데 %d개 반환됨", len(bindings))
	}
}

// TestBinder_LookBetween_NilSourceLines_FallsBackToGap1 은 sourceLines가 nil이면
// gap=1인 경우만 바인딩하는 보수적 폴백 동작을 검증합니다.
func TestBinder_LookBetween_NilSourceLines_FallsBackToGap1(t *testing.T) {
	b := NewBinder()

	comments := []CommentBlock{
		{StartLine: 1, EndLine: 1, Text: "@intent 폴백 테스트"},
	}
	nodes := []model.Node{
		{Name: "MyFunc", Kind: model.NodeKindFunction, StartLine: 3, EndLine: 10},
	}

	// sourceLines nil → gap=2이므로 사이를 확인 불가 → 바인딩 안 됨
	bindings := b.Bind(comments, nodes, "go", nil)

	if len(bindings) != 0 {
		t.Errorf("nil sourceLines, gap=2: 바인딩 0개 예상인데 %d개 반환됨", len(bindings))
	}
}

// TestBinder_LookBetween_WhitespaceOnlyLines_Binds 는 탭/스페이스만 있는 줄도
// "빈 줄"로 취급하여 바인딩이 성공함을 검증합니다.
func TestBinder_LookBetween_WhitespaceOnlyLines_Binds(t *testing.T) {
	b := NewBinder()

	comments := []CommentBlock{
		{StartLine: 1, EndLine: 1, Text: "@intent 공백문자만 있는 줄"},
	}
	nodes := []model.Node{
		{Name: "MyFunc", Kind: model.NodeKindFunction, StartLine: 4, EndLine: 10},
	}
	sourceLines := []string{
		"// @intent 공백문자만 있는 줄", // line 1
		"   ",                       // line 2 - spaces only
		"\t\t",                      // line 3 - tabs only
		"func MyFunc() {",           // line 4
	}

	bindings := b.Bind(comments, nodes, "go", sourceLines)

	if len(bindings) != 1 {
		t.Fatalf("whitespace-only lines gap=3: 바인딩 1개 예상인데 %d개 반환됨", len(bindings))
	}
}

// =============================================================================
// Look-Between Passthrough 테스트 — 데코레이터/주석/속성이 사이에 있어도 바인딩
// =============================================================================

// TestBinder_Passthrough_PythonDecorators_Binds 는 CCG 어노테이션과 함수 사이에
// Python 데코레이터(@)만 있으면 바인딩이 성공함을 검증합니다.
//
// fixture:
//
//	Line 1: # @intent 사용자 조회 엔드포인트
//	Line 2: @app.route('/api/user')
//	Line 3: @login_required
//	Line 4: def get_user():   ← gap = 4 - 1 = 3
//
// 사이 구간(line 2~3)이 데코레이터 → passthrough → 바인딩 성공
func TestBinder_Passthrough_PythonDecorators_Binds(t *testing.T) {
	b := NewBinder()

	comments := []CommentBlock{
		{StartLine: 1, EndLine: 1, Text: "@intent 사용자 조회 엔드포인트"},
	}
	nodes := []model.Node{
		{Name: "get_user", Kind: model.NodeKindFunction, StartLine: 4, EndLine: 8},
	}
	sourceLines := []string{
		"# @intent 사용자 조회 엔드포인트",  // line 1
		"@app.route('/api/user')",        // line 2 - decorator
		"@login_required",                // line 3 - decorator
		"def get_user():",                // line 4
	}

	bindings := b.Bind(comments, nodes, "python", sourceLines)

	if len(bindings) != 1 {
		t.Fatalf("Python 데코레이터 passthrough: 바인딩 1개 예상인데 %d개 반환됨", len(bindings))
	}
}

// TestBinder_Passthrough_JavaAnnotations_Binds 는 CCG 어노테이션과 클래스 사이에
// Java 어노테이션(@Service 등)만 있으면 바인딩이 성공함을 검증합니다.
//
// fixture:
//
//	Line 1-3: /** @intent 사용자 서비스 */
//	Line 4: @Service
//	Line 5: @Transactional
//	Line 6: @RequiredArgsConstructor
//	Line 7: public class UserService {   ← gap = 7 - 3 = 4
//
// 사이 구간(line 4~6)이 어노테이션 → passthrough → 바인딩 성공
func TestBinder_Passthrough_JavaAnnotations_Binds(t *testing.T) {
	b := NewBinder()

	comments := []CommentBlock{
		{StartLine: 1, EndLine: 3, Text: "/**\n * @intent 사용자 서비스\n */"},
	}
	nodes := []model.Node{
		{Name: "UserService", Kind: model.NodeKindClass, StartLine: 7, EndLine: 20},
	}
	sourceLines := []string{
		"/**",                             // line 1
		" * @intent 사용자 서비스",             // line 2
		" */",                             // line 3
		"@Service",                        // line 4 - annotation
		"@Transactional",                  // line 5 - annotation
		"@RequiredArgsConstructor",        // line 6 - annotation
		"public class UserService {",      // line 7
	}

	bindings := b.Bind(comments, nodes, "java", sourceLines)

	if len(bindings) != 1 {
		t.Fatalf("Java 어노테이션 passthrough: 바인딩 1개 예상인데 %d개 반환됨", len(bindings))
	}
}

// TestBinder_Passthrough_RustAttributes_Binds 는 CCG 어노테이션과 함수 사이에
// Rust 속성(#[...])만 있으면 바인딩이 성공함을 검증합니다.
//
// fixture:
//
//	Line 1: /// @intent 비동기 main 진입점
//	Line 2: /// @sideEffect 런타임 초기화
//	Line 3: #[tokio::main]
//	Line 4: #[allow(dead_code)]
//	Line 5: async fn main() {   ← gap = 5 - 2 = 3
//
// 사이 구간(line 3~4)이 Rust 속성 → passthrough → 바인딩 성공
func TestBinder_Passthrough_RustAttributes_Binds(t *testing.T) {
	b := NewBinder()

	comments := []CommentBlock{
		{StartLine: 1, EndLine: 2, Text: "/// @intent 비동기 main 진입점\n/// @sideEffect 런타임 초기화"},
	}
	nodes := []model.Node{
		{Name: "main", Kind: model.NodeKindFunction, StartLine: 5, EndLine: 10},
	}
	sourceLines := []string{
		"/// @intent 비동기 main 진입점",      // line 1
		"/// @sideEffect 런타임 초기화",       // line 2
		"#[tokio::main]",                   // line 3 - Rust attribute
		"#[allow(dead_code)]",              // line 4 - Rust attribute
		"async fn main() {",               // line 5
	}

	bindings := b.Bind(comments, nodes, "rust", sourceLines)

	if len(bindings) != 1 {
		t.Fatalf("Rust 속성 passthrough: 바인딩 1개 예상인데 %d개 반환됨", len(bindings))
	}
}

// TestBinder_Passthrough_CAttributes_Binds 는 CCG 어노테이션과 함수 사이에
// C __attribute__ 또는 [[...]] 속성만 있으면 바인딩이 성공함을 검증합니다.
//
// fixture:
//
//	Line 1-3: /** @intent 항상 인라인되는 덧셈 */
//	Line 4: __attribute__((always_inline))
//	Line 5: [[nodiscard]]
//	Line 6: static inline int add(int a, int b) {   ← gap = 6 - 3 = 3
//
// 사이 구간(line 4~5)이 C 속성 → passthrough → 바인딩 성공
func TestBinder_Passthrough_CAttributes_Binds(t *testing.T) {
	b := NewBinder()

	comments := []CommentBlock{
		{StartLine: 1, EndLine: 3, Text: "/**\n * @intent 항상 인라인되는 덧셈\n */"},
	}
	nodes := []model.Node{
		{Name: "add", Kind: model.NodeKindFunction, StartLine: 6, EndLine: 10},
	}
	sourceLines := []string{
		"/**",                                        // line 1
		" * @intent 항상 인라인되는 덧셈",                  // line 2
		" */",                                        // line 3
		"__attribute__((always_inline))",             // line 4 - C attribute
		"[[nodiscard]]",                              // line 5 - C++17 attribute
		"static inline int add(int a, int b) {",     // line 6
	}

	bindings := b.Bind(comments, nodes, "c", sourceLines)

	if len(bindings) != 1 {
		t.Fatalf("C 속성 passthrough: 바인딩 1개 예상인데 %d개 반환됨", len(bindings))
	}
}

// TestBinder_Passthrough_OtherComments_Binds 는 CCG 어노테이션과 함수 사이에
// 다른 일반 주석(// 또는 #)만 있으면 바인딩이 성공함을 검증합니다.
//
// fixture:
//
//	Line 1: // @intent 주석 사이 바인딩
//	Line 2: // TODO: 나중에 리팩토링
//	Line 3: // NOTE: 이 함수는 deprecated 예정
//	Line 4: func MyFunc() {   ← gap = 4 - 1 = 3
//
// 사이 구간(line 2~3)이 일반 주석 → passthrough → 바인딩 성공
func TestBinder_Passthrough_OtherComments_Binds(t *testing.T) {
	b := NewBinder()

	comments := []CommentBlock{
		{StartLine: 1, EndLine: 1, Text: "@intent 주석 사이 바인딩"},
	}
	nodes := []model.Node{
		{Name: "MyFunc", Kind: model.NodeKindFunction, StartLine: 4, EndLine: 10},
	}
	sourceLines := []string{
		"// @intent 주석 사이 바인딩",            // line 1
		"// TODO: 나중에 리팩토링",              // line 2 - comment
		"// NOTE: 이 함수는 deprecated 예정", // line 3 - comment
		"func MyFunc() {",                  // line 4
	}

	bindings := b.Bind(comments, nodes, "go", sourceLines)

	if len(bindings) != 1 {
		t.Fatalf("일반 주석 passthrough: 바인딩 1개 예상인데 %d개 반환됨", len(bindings))
	}
}

// TestBinder_Passthrough_Mixed_Binds 는 CCG 어노테이션과 함수 사이에
// 주석 + 데코레이터 + 빈 줄이 혼합되어 있어도 바인딩이 성공함을 검증합니다.
//
// fixture:
//
//	Line 1: # @intent 혼합 passthrough 테스트
//	Line 2: # type: ignore
//	Line 3: (빈 줄)
//	Line 4: @app.route('/test')
//	Line 5: @requires_auth
//	Line 6: def handler():   ← gap = 6 - 1 = 5
//
// 사이 구간(line 2~5): 주석, 빈 줄, 데코레이터 혼합 → 전부 passthrough → 바인딩 성공
func TestBinder_Passthrough_Mixed_Binds(t *testing.T) {
	b := NewBinder()

	comments := []CommentBlock{
		{StartLine: 1, EndLine: 1, Text: "@intent 혼합 passthrough 테스트"},
	}
	nodes := []model.Node{
		{Name: "handler", Kind: model.NodeKindFunction, StartLine: 6, EndLine: 10},
	}
	sourceLines := []string{
		"# @intent 혼합 passthrough 테스트", // line 1
		"# type: ignore",                // line 2 - comment
		"",                              // line 3 - blank
		"@app.route('/test')",           // line 4 - decorator
		"@requires_auth",                // line 5 - decorator
		"def handler():",                // line 6
	}

	bindings := b.Bind(comments, nodes, "python", sourceLines)

	if len(bindings) != 1 {
		t.Fatalf("혼합 passthrough: 바인딩 1개 예상인데 %d개 반환됨", len(bindings))
	}
}

// TestBinder_Passthrough_DecoratorPlusCode_NoBinding 은 CCG 어노테이션과 함수 사이에
// 데코레이터가 있더라도 실제 코드(변수 선언 등)가 섞여 있으면 바인딩이 실패함을 검증합니다.
//
// fixture:
//
//	Line 1: # @intent 이건 바인딩 안 됨
//	Line 2: @decorator
//	Line 3: x = 42             ← 실제 코드!
//	Line 4: def handler():   ← gap = 4 - 1 = 3
//
// 사이 구간(line 2~3): 데코레이터 + 코드 혼합 → 코드 존재 → 바인딩 실패
func TestBinder_Passthrough_DecoratorPlusCode_NoBinding(t *testing.T) {
	b := NewBinder()

	comments := []CommentBlock{
		{StartLine: 1, EndLine: 1, Text: "@intent 이건 바인딩 안 됨"},
	}
	nodes := []model.Node{
		{Name: "handler", Kind: model.NodeKindFunction, StartLine: 4, EndLine: 10},
	}
	sourceLines := []string{
		"# @intent 이건 바인딩 안 됨", // line 1
		"@decorator",            // line 2 - decorator (passthrough)
		"x = 42",                // line 3 - actual code!
		"def handler():",        // line 4
	}

	bindings := b.Bind(comments, nodes, "python", sourceLines)

	if len(bindings) != 0 {
		t.Errorf("데코레이터+코드 혼합: 바인딩 0개 예상인데 %d개 반환됨", len(bindings))
	}
}
