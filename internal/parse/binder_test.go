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

	comments := []CommentBlock{
		{StartLine: 1, EndLine: 4, Text: `"""
@intent 사용자 조회 엔드포인트
@domainRule 인증 필요
"""`},
	}
	nodes := []model.Node{
		{Name: "get_user", Kind: model.NodeKindFunction, StartLine: 7, EndLine: 8},
	}

	bindings := b.Bind(comments, nodes, "python")

	if len(bindings) != 1 {
		t.Errorf("Python 데코레이터 gap=3: 바인딩 1개 예상인데 %d개 반환됨", len(bindings))
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

	bindings := b.Bind(comments, nodes, "rust")

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
	b.MaxGap = 3

	comments := []CommentBlock{
		{StartLine: 1, EndLine: 3, Text: "/**\n * @intent 항상 인라인되는 덧셈\n */"},
	}
	nodes := []model.Node{
		// gap=3 상황: 속성 2줄 이후 함수 키워드가 6번째 줄에 위치
		{Name: "add", Kind: model.NodeKindFunction, StartLine: 6, EndLine: 8},
	}

	bindings := b.Bind(comments, nodes, "c")

	if len(bindings) != 1 {
		t.Errorf("C __attribute__ gap=3: 바인딩 1개 예상인데 %d개 반환됨", len(bindings))
	}
}

// TestBinder_GapExceedsMax 는 gap이 MaxGap을 초과할 때 바인딩이 발생하지 않음을 검증합니다.
//
// fixture:
//   Line 1-3: /** ... */ 주석
//   Line 4-7: 어노테이션/속성 4줄
//   Line 8:   class 선언   ← gap = 8 - 3 = 5 > MaxGap(3)
//
// gap=5, MaxGap=3 → 바인딩 없음
func TestBinder_GapExceedsMax(t *testing.T) {
	b := NewBinder()
	b.MaxGap = 3

	comments := []CommentBlock{
		{StartLine: 1, EndLine: 3, Text: "/**\n * @intent 갭 초과 테스트\n */"},
	}
	nodes := []model.Node{
		{Name: "BigClass", Kind: model.NodeKindClass, StartLine: 8, EndLine: 20},
	}

	bindings := b.Bind(comments, nodes, "java")

	if len(bindings) != 0 {
		t.Errorf("gap=5 > MaxGap=3: 바인딩 0개 예상인데 %d개 반환됨", len(bindings))
	}
}

// TestNewBinderFromConfig 는 NewBinderFromConfig 가 MaxGap 을 올바르게 설정함을 검증합니다.
func TestNewBinderFromConfig(t *testing.T) {
	tests := []struct {
		name       string
		inputGap   int
		wantMaxGap int
	}{
		{"양수 값 적용", 5, 5},
		{"0 입력 시 기본값 사용", 0, defaultMaxGap},
		{"음수 입력 시 기본값 사용", -1, defaultMaxGap},
		{"기본값과 동일", defaultMaxGap, defaultMaxGap},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b := NewBinderFromConfig(tc.inputGap)
			if b.MaxGap != tc.wantMaxGap {
				t.Errorf("MaxGap = %d, want %d", b.MaxGap, tc.wantMaxGap)
			}
		})
	}
}

// TestNewBinderFromConfig_BindsWithCustomMaxGap 는 MaxGap=5 로 설정한 Binder 가
// gap=5 짜리 comment-to-node 바인딩을 성공시킴을 검증합니다.
func TestNewBinderFromConfig_BindsWithCustomMaxGap(t *testing.T) {
	b := NewBinderFromConfig(5)

	comments := []CommentBlock{
		// EndLine=2, StartLine=8 → gap=6 > 5 → 바인딩 안 됨
		// EndLine=2, StartLine=7 → gap=5 ≤ 5 → 바인딩 됨
		{StartLine: 1, EndLine: 2, Text: "@intent 커스텀 갭 테스트"},
	}
	nodes := []model.Node{
		{Name: "MyFunc", Kind: model.NodeKindFunction, StartLine: 7, EndLine: 10},
	}

	bindings := b.Bind(comments, nodes, "go")

	if len(bindings) != 1 {
		t.Errorf("MaxGap=5, gap=5: 바인딩 1개 예상인데 %d개 반환됨", len(bindings))
	}
}
