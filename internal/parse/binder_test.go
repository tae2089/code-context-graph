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

	bindings := b.Bind(comments, nodes, "go")
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

	bindings := b.Bind(comments, nodes, "go")
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

	bindings := b.Bind(nil, nodes, "go")
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

	bindings := b.Bind(comments, nodes, "go")
	if len(bindings) != 0 {
		t.Errorf("expected 0 bindings (gap > 1), got %d", len(bindings))
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

	bindings := b.Bind(comments, nodes, "go")
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
// 아래 단위 테스트들은 maxGap=2 제약으로 인해 데코레이터/속성이 2줄 이상 붙은 경우
// 현재 binder가 @intent 주석을 심볼에 바인딩하지 못하는 동작을 재현합니다.
//
// 테스트 이름에 _CurrentlyFails 접미사를 붙여 이 테스트가 "현재 실패하는 동작"을
// 문서화하는 용도임을 명시합니다. 이 테스트들은 현재 통과(바인딩 실패 확인)되어야
// 하며, 추후 수정 후에는 반드시 함께 수정해야 하는 회귀 방지 테스트입니다.

// TestBinder_PythonDecoratorGap_CurrentlyFails 는 Python 데코레이터 2개가 있을 때
// docstring의 EndLine과 def 키워드 StartLine 사이 gap이 maxGap(2)을 초과하므로
// 현재 바인딩이 실패함을 재현합니다.
//
// fixture (decorator_gap.py) 기준:
//   Line 1-4: docstring ("""...""")
//   Line 5:   @app.route('/api/user')
//   Line 6:   @login_required
//   Line 7:   def get_user():   ← tree-sitter StartLine 예상
//
// gap = 7 - 4 = 3 > maxGap(2) → 바인딩 실패
func TestBinder_PythonDecoratorGap_CurrentlyFails(t *testing.T) {
	b := NewBinder()

	// docstring 끝줄=4, 데코레이터 2개 후 def 키워드 줄=7
	// (실제 tree-sitter 측정값은 통합 테스트 TestWalkerBinder_PythonDecorator_CurrentlyFailsBinding 참고)
	comments := []CommentBlock{
		{StartLine: 1, EndLine: 4, Text: `"""
@intent 사용자 조회 엔드포인트
@domainRule 인증 필요
"""`},
	}
	nodes := []model.Node{
		// tree-sitter는 Python에서 decorated_definition 노드가 있을 경우
		// StartLine이 첫 데코레이터 줄로 잡힐 수 있음.
		// 여기서는 의도적으로 def 키워드 줄(7)로 설정하여 gap=3 상황을 재현.
		{Name: "get_user", Kind: model.NodeKindFunction, StartLine: 7, EndLine: 8},
	}

	bindings := b.Bind(comments, nodes, "python")

	// 현재 maxGap=2이므로 gap=3은 바인딩되지 않아야 한다.
	// 이 assertion이 통과하면 → "현재 동작(바인딩 실패)" 이 재현된 것.
	// 추후 수정(effectiveStartLine 도입)으로 바인딩이 성공하면 이 테스트는 실패로 전환됨.
	if len(bindings) != 0 {
		t.Errorf("[회귀감지] Python 데코레이터 gap=3 시나리오: 현재는 바인딩 0개 예상인데 %d개 반환됨. maxGap 확장이 적용된 것인지 확인 필요", len(bindings))
	}
}

// TestBinder_JavaAnnotationGap_CurrentlyFails 는 Java 어노테이션 3개가 있을 때
// Javadoc 끝줄과 class 키워드 StartLine 사이 gap이 maxGap(2)을 초과하므로
// 현재 바인딩이 실패함을 재현합니다.
//
// fixture (AnnotationGap.java) 기준:
//   Line 1-4: /** ... */ Javadoc
//   Line 5:   @Service
//   Line 6:   @Transactional
//   Line 7:   @RequiredArgsConstructor
//   Line 8:   public class UserService {   ← tree-sitter StartLine 예상
//
// gap = 8 - 4 = 4 > maxGap(2) → 바인딩 실패
func TestBinder_JavaAnnotationGap_CurrentlyFails(t *testing.T) {
	b := NewBinder()

	// Javadoc 끝줄=4, 어노테이션 3개 후 class 키워드 줄=8
	comments := []CommentBlock{
		{StartLine: 1, EndLine: 4, Text: `/**
 * @intent 사용자 서비스
 * @domainRule 트랜잭션 필수
 */`},
	}
	nodes := []model.Node{
		{Name: "UserService", Kind: model.NodeKindClass, StartLine: 8, EndLine: 9},
	}

	bindings := b.Bind(comments, nodes, "java")

	if len(bindings) != 0 {
		t.Errorf("[회귀감지] Java 어노테이션 gap=4 시나리오: 현재는 바인딩 0개 예상인데 %d개 반환됨. maxGap 확장이 적용된 것인지 확인 필요", len(bindings))
	}
}

// TestBinder_RustAttributeGap_CurrentlyFails 는 Rust 속성 2개가 있을 때
// doc comment 끝줄과 fn 키워드 StartLine 사이 gap이 maxGap(2)을 초과하므로
// 현재 바인딩이 실패함을 재현합니다.
//
// fixture (attribute_gap.rs) 기준:
//   Line 1: /// @intent 비동기 main 진입점
//   Line 2: /// @sideEffect 런타임 초기화
//   Line 3: #[tokio::main]
//   Line 4: #[allow(dead_code)]
//   Line 5: async fn main() {   ← tree-sitter StartLine 예상
//
// gap = 5 - 2 = 3 > maxGap(2) → 바인딩 실패
func TestBinder_RustAttributeGap_CurrentlyFails(t *testing.T) {
	b := NewBinder()

	// doc comment 끝줄=2, 속성 2개 후 fn 키워드 줄=5
	comments := []CommentBlock{
		{StartLine: 1, EndLine: 2, Text: "/// @intent 비동기 main 진입점\n/// @sideEffect 런타임 초기화"},
	}
	nodes := []model.Node{
		{Name: "main", Kind: model.NodeKindFunction, StartLine: 5, EndLine: 6},
	}

	bindings := b.Bind(comments, nodes, "rust")

	if len(bindings) != 0 {
		t.Errorf("[회귀감지] Rust 속성 gap=3 시나리오: 현재는 바인딩 0개 예상인데 %d개 반환됨. maxGap 확장이 적용된 것인지 확인 필요", len(bindings))
	}
}

// TestBinder_CAttributeGap_CurrentlyFails 는 C __attribute__ 한 줄이 있을 때
// Doxygen 주석 끝줄과 함수 StartLine 사이 gap이 maxGap(2)를 초과하는 경우를 재현합니다.
//
// fixture (attribute_gap.c) 기준:
//   Line 1-3: /** ... */ Doxygen
//   Line 4:   __attribute__((always_inline))
//   Line 5:   static inline int add(int a, int b) {   ← tree-sitter StartLine 예상
//
// gap = 5 - 3 = 2: 이 경우는 gap=2이므로 maxGap(2) 이내. 바인딩이 성공해야 함.
// 그러나 tree-sitter가 function_definition의 StartLine을 __attribute__ 줄(4)로
// 잡는다면 gap = 4 - 3 = 1이 되어 바인딩 성공. 실제 StartLine은 통합 테스트에서 확인.
//
// 이 단위 테스트는 "속성이 1줄" 케이스를 명시적으로 문서화하며,
// gap=3 상황(StartLine=6으로 설정)에서 바인딩 실패를 재현합니다.
func TestBinder_CAttributeGap_CurrentlyFails(t *testing.T) {
	b := NewBinder()

	// Doxygen 끝줄=3, 속성 1줄 후 함수 키워드(static) 줄=5
	// 하지만 tree-sitter가 StartLine=5로 잡는다면 gap=2 → 바인딩 성공
	// 여기서는 더 극단적인 케이스: 속성이 여러 줄인 상황을 모사하여 gap=3
	comments := []CommentBlock{
		{StartLine: 1, EndLine: 3, Text: "/**\n * @intent 항상 인라인되는 덧셈\n */"},
	}
	nodes := []model.Node{
		// gap=3 상황: 속성 2줄 이후 함수 키워드가 6번째 줄에 위치
		{Name: "add", Kind: model.NodeKindFunction, StartLine: 6, EndLine: 8},
	}

	bindings := b.Bind(comments, nodes, "c")

	if len(bindings) != 0 {
		t.Errorf("[회귀감지] C __attribute__ gap=3 시나리오: 현재는 바인딩 0개 예상인데 %d개 반환됨. maxGap 확장이 적용된 것인지 확인 필요", len(bindings))
	}
}
