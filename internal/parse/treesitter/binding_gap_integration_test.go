// 데코레이터/어노테이션/속성 gap으로 인한 @intent 바인딩 실패를 재현하는 통합 테스트.
//
// 배경: binder.go의 maxGap=2 제약으로 인해, 심볼 선언부 위에 데코레이터/어노테이션/속성이
// 여러 줄 붙어 있으면 @intent docstring/주석이 심볼 노드에 바인딩되지 않는다.
//
// 이 파일의 테스트는 각 언어별로:
//   1. 실제 tree-sitter로 fixture를 파싱
//   2. 심볼 노드의 실제 StartLine을 t.Logf로 출력
//   3. Binder.Bind 결과를 검증하여 현재 실패 동작을 명시적으로 기록
//
// 테스트 이름에 _CurrentlyFailsBinding 접미사를 붙여 현재 상태를 명시합니다.
package treesitter

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tae2089/code-context-graph/internal/annotation"
	"github.com/tae2089/code-context-graph/internal/model"
	"github.com/tae2089/code-context-graph/internal/parse"
)

// fixtureDir은 binding_gap fixture 디렉토리의 절대 경로를 반환합니다.
func fixtureDir(lang string) string {
	// 이 파일은 internal/parse/treesitter/ 에 위치.
	// testdata는 프로젝트 루트 기준 testdata/binding_gap/<lang>/ 에 위치.
	// runtime에서 절대 경로를 사용합니다.
	wd, err := os.Getwd()
	if err != nil {
		panic("getwd failed: " + err.Error())
	}
	// internal/parse/treesitter → 루트로 3단계 올라감
	root := filepath.Join(wd, "..", "..", "..")
	return filepath.Join(root, "testdata", "binding_gap", lang)
}

// readFixture는 fixture 파일 내용을 읽어 반환합니다.
func readFixture(t *testing.T, lang, filename string) []byte {
	t.Helper()
	path := filepath.Join(fixtureDir(lang), filename)
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("fixture 파일 읽기 실패 (%s): %v", path, err)
	}
	return content
}

// binderFromWalkerComments는 Walker의 CommentBlock을 parse.CommentBlock으로 변환합니다.
// (treesitter.CommentBlock과 parse.CommentBlock은 별도 타입이므로 변환이 필요함)
func binderFromWalkerComments(wcs []CommentBlock) []parse.CommentBlock {
	result := make([]parse.CommentBlock, len(wcs))
	for i, wc := range wcs {
		result[i] = parse.CommentBlock{
			StartLine:      wc.StartLine,
			EndLine:        wc.EndLine,
			Text:           wc.Text,
			IsDocstring:    wc.IsDocstring,
			OwnerStartLine: wc.OwnerStartLine,
		}
	}
	return result
}

// logNodeInfo는 노드의 상세 정보를 t.Logf로 출력합니다.
// 이후 effectiveStartLine 설계의 핵심 데이터를 수집하는 용도입니다.
func logNodeInfo(t *testing.T, nodes []model.Node) {
	t.Helper()
	for _, n := range nodes {
		if n.Kind == model.NodeKindFile {
			continue
		}
		t.Logf("  노드: Kind=%-10s Name=%-20s StartLine=%d EndLine=%d",
			n.Kind, n.Name, n.StartLine, n.EndLine)
	}
}

// logCommentInfo는 주석 블록의 상세 정보를 t.Logf로 출력합니다.
func logCommentInfo(t *testing.T, comments []CommentBlock) {
	t.Helper()
	for i, c := range comments {
		preview := c.Text
		if len(preview) > 60 {
			preview = preview[:60] + "..."
		}
		preview = strings.ReplaceAll(preview, "\n", "\\n")
		t.Logf("  주석블록[%d]: StartLine=%d EndLine=%d Text=%q",
			i, c.StartLine, c.EndLine, preview)
	}
}

// TestWalkerBinder_PythonDecorator_CurrentlyFailsBinding 은
// Python 데코레이터 2개 + docstring 시나리오에서 tree-sitter의 실제 StartLine을 측정하고
// Binder.Bind 결과가 현재 실패(바인딩 없음)함을 검증합니다.
//
// 핵심 질문: Python에서 decorated_definition 노드의 StartLine은
//   - def 키워드 줄인가?
//   - 첫 번째 데코레이터(@app.route) 줄인가?
func TestWalkerBinder_PythonDecorator_CurrentlyFailsBinding(t *testing.T) {
	content := readFixture(t, "python", "decorator_gap.py")
	t.Logf("fixture 내용:\n%s", string(content))

	w := NewWalker(PythonSpec)
	nodes, _, walkerComments, err := w.ParseWithComments(context.Background(), "decorator_gap.py", content)
	if err != nil {
		t.Fatalf("파싱 실패: %v", err)
	}

	t.Log("=== 파싱된 노드 ===")
	logNodeInfo(t, nodes)

	t.Log("=== 파싱된 주석 블록 ===")
	logCommentInfo(t, walkerComments)

	// get_user 함수 노드를 찾아 실제 StartLine 기록
	var getUserNode *model.Node
	for i := range nodes {
		if nodes[i].Name == "get_user" {
			getUserNode = &nodes[i]
			break
		}
	}

	if getUserNode == nil {
		t.Fatal("get_user 노드를 찾지 못함 — Python 파서가 decorated_definition을 별도 처리하는지 확인 필요")
	}

	t.Logf("=== get_user 실측 StartLine: %d ===", getUserNode.StartLine)
	t.Logf("(이 값이 def 키워드 줄인지, 첫 데코레이터 줄인지를 확인하세요)")

	// Binder 바인딩 시도
	b := parse.NewBinder()
	binderComments := binderFromWalkerComments(walkerComments)
	bindings := b.Bind(binderComments, nodes, "python")

	// get_user에 대한 바인딩이 있는지 확인
	var getUserBinding *parse.Binding
	for i := range bindings {
		if bindings[i].Node.Name == "get_user" {
			getUserBinding = &bindings[i]
			break
		}
	}

	if getUserBinding != nil {
		t.Logf("[예상 외 성공] get_user에 바인딩 성공: Annotation=%+v", getUserBinding.Annotation)
		t.Logf("  → Python이 decorated_definition으로 StartLine을 첫 데코레이터 줄로 잡아 gap이 줄었을 가능성")
	} else {
		t.Logf("[현재 동작 확인] get_user에 바인딩 없음 (gap 초과)")
	}

	// === Red 테스트: 기대 동작 (현재 실패) ===
	// get_user 노드에 @intent 어노테이션이 바인딩되어야 한다.
	// 현재 maxGap=2 제약으로 이 테스트는 실패한다.
	if getUserBinding == nil {
		t.Errorf("[Red] get_user에 @intent 바인딩이 없음. 기대: @intent='사용자 조회 엔드포인트' 바인딩 성공")
	} else {
		hasIntent := false
		for _, tag := range getUserBinding.Annotation.Tags {
			if tag.Kind == "intent" {
				hasIntent = true
				t.Logf("  @intent 태그값: %s", tag.Value)
				break
			}
		}
		if !hasIntent {
			t.Errorf("[Red] get_user 바인딩은 있지만 @intent 태그가 없음. Tags=%+v", getUserBinding.Annotation.Tags)
		}
	}
}

// TestWalkerBinder_JavaAnnotation_CurrentlyFailsBinding 은
// Java 어노테이션 3개 + Javadoc 시나리오에서 tree-sitter의 실제 StartLine을 측정하고
// Binder.Bind 결과가 현재 실패(바인딩 없음)함을 검증합니다.
//
// 핵심 질문: Java에서 class_declaration 노드의 StartLine은
//   - public class 키워드 줄인가?
//   - 첫 번째 어노테이션(@Service) 줄인가?
func TestWalkerBinder_JavaAnnotation_CurrentlyFailsBinding(t *testing.T) {
	content := readFixture(t, "java", "AnnotationGap.java")
	t.Logf("fixture 내용:\n%s", string(content))

	w := NewWalker(JavaSpec)
	nodes, _, walkerComments, err := w.ParseWithComments(context.Background(), "AnnotationGap.java", content)
	if err != nil {
		t.Fatalf("파싱 실패: %v", err)
	}

	t.Log("=== 파싱된 노드 ===")
	logNodeInfo(t, nodes)

	t.Log("=== 파싱된 주석 블록 ===")
	logCommentInfo(t, walkerComments)

	// UserService 클래스 노드를 찾아 실제 StartLine 기록
	var userServiceNode *model.Node
	for i := range nodes {
		if nodes[i].Name == "UserService" {
			userServiceNode = &nodes[i]
			break
		}
	}

	if userServiceNode == nil {
		t.Fatal("UserService 노드를 찾지 못함")
	}

	t.Logf("=== UserService 실측 StartLine: %d ===", userServiceNode.StartLine)
	t.Logf("(이 값이 'public class' 키워드 줄인지, 첫 어노테이션 줄인지를 확인하세요)")

	// Binder 바인딩 시도
	b := parse.NewBinder()
	binderComments := binderFromWalkerComments(walkerComments)
	bindings := b.Bind(binderComments, nodes, "java")

	var userServiceBinding *parse.Binding
	for i := range bindings {
		if bindings[i].Node.Name == "UserService" {
			userServiceBinding = &bindings[i]
			break
		}
	}

	if userServiceBinding != nil {
		t.Logf("[예상 외 성공] UserService에 바인딩 성공: Annotation=%+v", userServiceBinding.Annotation)
		t.Logf("  → Java가 어노테이션 포함 StartLine으로 gap이 줄었을 가능성")
	} else {
		t.Logf("[현재 동작 확인] UserService에 바인딩 없음 (gap 초과)")
	}

	// === Red 테스트: 기대 동작 (현재 실패) ===
	if userServiceBinding == nil {
		t.Errorf("[Red] UserService에 @intent 바인딩이 없음. 기대: @intent='사용자 서비스' 바인딩 성공")
	} else {
		hasIntent := false
		for _, tag := range userServiceBinding.Annotation.Tags {
			if tag.Kind == "intent" {
				hasIntent = true
				t.Logf("  @intent 태그값: %s", tag.Value)
				break
			}
		}
		if !hasIntent {
			t.Errorf("[Red] UserService 바인딩은 있지만 @intent 태그가 없음. Tags=%+v", userServiceBinding.Annotation.Tags)
		}
	}
}

// TestWalkerBinder_RustAttribute_CurrentlyFailsBinding 은
// Rust 속성 2개 + doc comment 시나리오에서 tree-sitter의 실제 StartLine을 측정하고
// Binder.Bind 결과가 현재 실패(바인딩 없음)함을 검증합니다.
//
// 핵심 질문: Rust에서 function_item 노드의 StartLine은
//   - async fn 키워드 줄인가?
//   - 첫 번째 #[...] 속성 줄인가?
func TestWalkerBinder_RustAttribute_CurrentlyFailsBinding(t *testing.T) {
	content := readFixture(t, "rust", "attribute_gap.rs")
	t.Logf("fixture 내용:\n%s", string(content))

	w := NewWalker(RustSpec)
	nodes, _, walkerComments, err := w.ParseWithComments(context.Background(), "attribute_gap.rs", content)
	if err != nil {
		t.Fatalf("파싱 실패: %v", err)
	}

	t.Log("=== 파싱된 노드 ===")
	logNodeInfo(t, nodes)

	t.Log("=== 파싱된 주석 블록 ===")
	logCommentInfo(t, walkerComments)

	// main 함수 노드를 찾아 실제 StartLine 기록
	var mainNode *model.Node
	for i := range nodes {
		if nodes[i].Name == "main" && nodes[i].Kind == model.NodeKindFunction {
			mainNode = &nodes[i]
			break
		}
	}

	if mainNode == nil {
		t.Fatal("main 노드를 찾지 못함")
	}

	t.Logf("=== main 실측 StartLine: %d ===", mainNode.StartLine)
	t.Logf("(이 값이 'async fn' 키워드 줄인지, 첫 #[...] 속성 줄인지를 확인하세요)")

	// Binder 바인딩 시도
	b := parse.NewBinder()
	binderComments := binderFromWalkerComments(walkerComments)
	bindings := b.Bind(binderComments, nodes, "rust")

	var mainBinding *parse.Binding
	for i := range bindings {
		if bindings[i].Node.Name == "main" {
			mainBinding = &bindings[i]
			break
		}
	}

	if mainBinding != nil {
		t.Logf("[예상 외 성공] main에 바인딩 성공: Annotation=%+v", mainBinding.Annotation)
		t.Logf("  → Rust function_item의 StartLine이 속성 포함 줄로 잡혀 gap이 줄었을 가능성")
	} else {
		t.Logf("[현재 동작 확인] main에 바인딩 없음 (gap 초과)")
	}

	// === Red 테스트: 기대 동작 (현재 실패) ===
	if mainBinding == nil {
		t.Errorf("[Red] main에 @intent 바인딩이 없음. 기대: @intent='비동기 main 진입점' 바인딩 성공")
	} else {
		hasIntent := false
		for _, tag := range mainBinding.Annotation.Tags {
			if tag.Kind == "intent" {
				hasIntent = true
				t.Logf("  @intent 태그값: %s", tag.Value)
				break
			}
		}
		if !hasIntent {
			t.Errorf("[Red] main 바인딩은 있지만 @intent 태그가 없음. Tags=%+v", mainBinding.Annotation.Tags)
		}
	}
}

// TestWalkerBinder_CAttribute_CurrentlyFailsBinding 은
// C __attribute__ + Doxygen 주석 시나리오에서 tree-sitter의 실제 StartLine을 측정하고
// Binder.Bind 결과를 검증합니다.
//
// 핵심 질문: C에서 function_definition 노드의 StartLine은
//   - static inline int 키워드 줄(5)인가?
//   - __attribute__ 줄(4)인가?
//
// Doxygen 끝줄=3, __attribute__ 줄=4, static inline 줄=5
// - StartLine=4: gap = 4-3 = 1 → 바인딩 성공 (maxGap 이내)
// - StartLine=5: gap = 5-3 = 2 → 바인딩 성공 (maxGap 이내)
// 위 fixture는 속성이 1줄이라 gap이 maxGap 이내일 수 있음.
// 이 테스트는 실제 tree-sitter 동작을 측정하는 것이 주 목적이며,
// C는 1줄 속성으로 gap=2 이내가 될 수 있어 바인딩 성공 가능성도 있음.
func TestWalkerBinder_CAttribute_CurrentlyFailsBinding(t *testing.T) {
	content := readFixture(t, "c", "attribute_gap.c")
	t.Logf("fixture 내용:\n%s", string(content))

	w := NewWalker(CSpec)
	nodes, _, walkerComments, err := w.ParseWithComments(context.Background(), "attribute_gap.c", content)
	if err != nil {
		t.Fatalf("파싱 실패: %v", err)
	}

	t.Log("=== 파싱된 노드 ===")
	logNodeInfo(t, nodes)

	t.Log("=== 파싱된 주석 블록 ===")
	logCommentInfo(t, walkerComments)

	// add 함수 노드를 찾아 실제 StartLine 기록
	var addNode *model.Node
	for i := range nodes {
		if nodes[i].Name == "add" && nodes[i].Kind == model.NodeKindFunction {
			addNode = &nodes[i]
			break
		}
	}

	if addNode == nil {
		t.Fatal("add 노드를 찾지 못함 — C 파서가 __attribute__ 포함 함수를 파싱하는지 확인 필요")
	}

	t.Logf("=== add 실측 StartLine: %d ===", addNode.StartLine)
	t.Logf("(이 값이 'static inline int' 키워드 줄인지, '__attribute__' 줄인지를 확인하세요)")

	// 주석과 노드 사이의 실제 gap 계산
	for _, c := range walkerComments {
		gap := addNode.StartLine - c.EndLine
		t.Logf("  주석 EndLine=%d → add StartLine=%d, gap=%d (maxGap=2)", c.EndLine, addNode.StartLine, gap)
	}

	// Binder 바인딩 시도
	b := parse.NewBinder()
	binderComments := binderFromWalkerComments(walkerComments)
	bindings := b.Bind(binderComments, nodes, "c")

	var addBinding *parse.Binding
	for i := range bindings {
		if bindings[i].Node.Name == "add" {
			addBinding = &bindings[i]
			break
		}
	}

	if addBinding != nil {
		t.Logf("[현재 동작] add에 바인딩 성공: Annotation=%+v", addBinding.Annotation)
		t.Logf("  → C의 경우 __attribute__ 1줄만 있어 gap이 maxGap(2) 이내일 수 있음")
	} else {
		t.Logf("[현재 동작] add에 바인딩 없음 (gap 초과)")
	}

	// === Red 테스트: 기대 동작 ===
	// C fixture는 속성이 1줄이라 경계 케이스. 바인딩이 성공해야 함.
	if addBinding == nil {
		t.Errorf("[Red] add에 @intent 바인딩이 없음. 기대: @intent='항상 인라인되는 덧셈' 바인딩 성공")
	} else {
		hasIntent := false
		for _, tag := range addBinding.Annotation.Tags {
			if tag.Kind == "intent" {
				hasIntent = true
				t.Logf("  @intent 태그값: %s", tag.Value)
				break
			}
		}
		if !hasIntent {
			t.Errorf("[Red] add 바인딩은 있지만 @intent 태그가 없음. Tags=%+v", addBinding.Annotation.Tags)
		}
	}
}

// TestWalkerBinder_PythonDecoratorHashComment_CurrentlyFailsBinding 은
// Python에서 """ docstring이 tree-sitter에서 comment로 잡히지 않는 문제를 보완하여
// # 라인 주석 + 데코레이터 2개 시나리오로 바인딩 실패를 검증합니다.
//
// fixture (decorator_gap_comment.py) 기준:
//   Line 1: # @intent 사용자 조회 엔드포인트
//   Line 2: # @domainRule 인증 필요
//   Line 3: @app.route('/api/user')
//   Line 4: @login_required
//   Line 5: def get_user():   ← tree-sitter StartLine
//
// 주석EndLine=2, get_user StartLine=5, gap=3 > maxGap(2) → 바인딩 실패 예상
// (단, Python이 decorated_definition으로 StartLine을 데코레이터 첫 줄=3으로 잡으면
//  gap=3-2=1이 되어 바인딩 성공 가능성도 있음)
func TestWalkerBinder_PythonDecoratorHashComment_CurrentlyFailsBinding(t *testing.T) {
	content := readFixture(t, "python", "decorator_gap_comment.py")
	t.Logf("fixture 내용:\n%s", string(content))

	w := NewWalker(PythonSpec)
	nodes, _, walkerComments, err := w.ParseWithComments(context.Background(), "decorator_gap_comment.py", content)
	if err != nil {
		t.Fatalf("파싱 실패: %v", err)
	}

	t.Log("=== 파싱된 노드 ===")
	logNodeInfo(t, nodes)

	t.Log("=== 파싱된 주석 블록 ===")
	logCommentInfo(t, walkerComments)

	// get_user 함수 노드를 찾아 실제 StartLine 기록
	var getUserNode *model.Node
	for i := range nodes {
		if nodes[i].Name == "get_user" {
			getUserNode = &nodes[i]
			break
		}
	}

	if getUserNode == nil {
		t.Fatal("get_user 노드를 찾지 못함")
	}

	t.Logf("=== get_user 실측 StartLine: %d ===", getUserNode.StartLine)
	t.Logf("(이 값이 def 키워드 줄인지, 첫 데코레이터 줄인지를 확인하세요)")

	// 주석과 노드 사이의 실제 gap 계산
	for _, c := range walkerComments {
		gap := getUserNode.StartLine - c.EndLine
		withinMax := gap >= 1 && gap <= 2
		status := "FAIL (gap>maxGap)"
		if withinMax {
			status = "OK (gap<=maxGap)"
		}
		t.Logf("  주석EndLine=%d → get_user.StartLine=%d, gap=%d → %s",
			c.EndLine, getUserNode.StartLine, gap, status)
	}

	// Binder 바인딩 시도
	b := parse.NewBinder()
	binderComments := binderFromWalkerComments(walkerComments)
	bindings := b.Bind(binderComments, nodes, "python")

	var getUserBinding *parse.Binding
	for i := range bindings {
		if bindings[i].Node.Name == "get_user" {
			getUserBinding = &bindings[i]
			break
		}
	}

	if getUserBinding != nil {
		t.Logf("[예상 외 성공] get_user에 바인딩 성공: Annotation=%+v", getUserBinding.Annotation)
		t.Logf("  → Python decorated_definition의 StartLine이 데코레이터 첫 줄로 잡혔을 가능성")
	} else {
		t.Logf("[현재 동작 확인] get_user에 바인딩 없음 (gap 초과)")
	}

	// === Red 테스트: 기대 동작 (현재 실패) ===
	if getUserBinding == nil {
		t.Errorf("[Red] get_user에 @intent 바인딩이 없음. 기대: @intent='사용자 조회 엔드포인트' 바인딩 성공")
	} else {
		hasIntent := false
		for _, tag := range getUserBinding.Annotation.Tags {
			if tag.Kind == "intent" {
				hasIntent = true
				t.Logf("  @intent 태그값: %s", tag.Value)
				break
			}
		}
		if !hasIntent {
			t.Errorf("[Red] get_user 바인딩은 있지만 @intent 태그가 없음. Tags=%+v", getUserBinding.Annotation.Tags)
		}
	}
}

// TestWalkerBinder_GapDiagnosis_AllLanguages 는 4개 언어의 실측 StartLine과 gap을
// 한 테이블로 출력하여 진단 가설 검증을 용이하게 합니다.
// 이 테스트는 항상 통과하며, 진단 데이터만 수집합니다.
func TestWalkerBinder_GapDiagnosis_AllLanguages(t *testing.T) {
	type langCase struct {
		spec      *LangSpec
		lang      string
		filename  string
		symName   string
		symKind   model.NodeKind
		fixtureExt string
	}

	cases := []langCase{
		{PythonSpec, "python", "decorator_gap.py", "get_user", model.NodeKindFunction, "py"},
		{JavaSpec, "java", "AnnotationGap.java", "UserService", model.NodeKindClass, "java"},
		{RustSpec, "rust", "attribute_gap.rs", "main", model.NodeKindFunction, "rs"},
		{CSpec, "c", "attribute_gap.c", "add", model.NodeKindFunction, "c"},
	}

	t.Log("=================================================================")
	t.Log("언어별 실측 StartLine 및 Binder gap 진단 결과")
	t.Log("=================================================================")

	for _, tc := range cases {
		t.Run(tc.lang, func(t *testing.T) {
			content := readFixture(t, tc.lang, tc.filename)

			w := NewWalker(tc.spec)
			nodes, _, walkerComments, err := w.ParseWithComments(context.Background(), tc.filename, content)
			if err != nil {
				t.Fatalf("[%s] 파싱 실패: %v", tc.lang, err)
			}

			// 대상 심볼 노드 찾기
			var targetNode *model.Node
			for i := range nodes {
				if nodes[i].Name == tc.symName && nodes[i].Kind == tc.symKind {
					targetNode = &nodes[i]
					break
				}
			}

			if targetNode == nil {
				t.Logf("[%s] %s 노드를 찾지 못함", tc.lang, tc.symName)
				return
			}

			t.Logf("[%s] %s.StartLine=%d", tc.lang, tc.symName, targetNode.StartLine)

			// 각 주석과의 gap 계산
			for _, c := range walkerComments {
				gap := targetNode.StartLine - c.EndLine
				withinMax := gap >= 1 && gap <= 2
				status := "FAIL (gap>maxGap)"
				if withinMax {
					status = "OK (gap<=maxGap)"
				}
				t.Logf("[%s] 주석EndLine=%d → %s.StartLine=%d, gap=%d → %s",
					tc.lang, c.EndLine, tc.symName, targetNode.StartLine, gap, status)
			}

			// 실제 Binder 결과
			b := parse.NewBinder()
			binderComments := binderFromWalkerComments(walkerComments)
			bindings := b.Bind(binderComments, nodes, tc.lang)

			bound := false
			for _, binding := range bindings {
				if binding.Node.Name == tc.symName {
					bound = true
					hasIntent := false
					for _, tag := range binding.Annotation.Tags {
						if tag.Kind == "intent" {
							hasIntent = true
							t.Logf("[%s] %s → @intent 바인딩 성공: %s", tc.lang, tc.symName, tag.Value)
						}
					}
					if !hasIntent {
						t.Logf("[%s] %s → 바인딩 있지만 @intent 태그 없음", tc.lang, tc.symName)
					}
					break
				}
			}

			if !bound {
				t.Logf("[%s] %s → 바인딩 없음 (gap 초과 또는 주석 없음)", tc.lang, tc.symName)
			}
		})
	}
}

// annotation 패키지를 사용 가능하게 하기 위한 blank import 확인용 변수
// (컴파일러가 미사용 임포트로 오류를 낼 경우를 방지)
var _ = annotation.NewNormalizer
