// Python docstring 2차 실측 테스트.
//
// 목적: tree-sitter-python이 다양한 docstring 변형(함수/클래스/모듈, """/''', 한 줄, prefix)을
// 어떤 AST 노드 구조로 파싱하는지 정확히 측정하고,
// 현재 collectComments 로직이 이들을 CommentBlock으로 수집하는지 검증한다.
//
// 구현 수정 금지: walker.go, binder.go, normalizer.go 는 건드리지 않는다.
// 오직 fixture + 실측 테스트만 추가한다.
package treesitter

import (
	"context"
	"fmt"
	"strings"
	"testing"

	sitter "github.com/smacker/go-tree-sitter"

	"github.com/tae2089/code-context-graph/internal/model"
	"github.com/tae2089/code-context-graph/internal/parse"
)

// ─────────────────────────────────────────────────────────────
// AST 탐색 헬퍼
// ─────────────────────────────────────────────────────────────

// dumpAST는 AST 노드를 재귀적으로 탐색하여 t.Logf로 출력한다.
// maxDepth를 초과하면 탐색을 멈춘다.
func dumpAST(t *testing.T, node *sitter.Node, content []byte, depth, maxDepth int) {
	t.Helper()
	if node == nil || depth > maxDepth {
		return
	}
	indent := strings.Repeat("  ", depth)
	startRow := node.StartPoint().Row + 1
	endRow := node.EndPoint().Row + 1
	startCol := node.StartPoint().Column
	nodeText := node.Content(content)
	if len(nodeText) > 50 {
		nodeText = nodeText[:50] + "..."
	}
	nodeText = strings.ReplaceAll(nodeText, "\n", "\\n")

	fieldName := ""
	if node.IsNamed() {
		t.Logf("%s[named] type=%-30s line=%d-%d col=%d  %s%q",
			indent, node.Type(), startRow, endRow, startCol, fieldName, nodeText)
	} else {
		t.Logf("%s[anon]  type=%-30s line=%d-%d col=%d  %s%q",
			indent, node.Type(), startRow, endRow, startCol, fieldName, nodeText)
	}

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child != nil {
			dumpAST(t, child, content, depth+1, maxDepth)
		}
	}
}

// findDocstringNodes는 AST를 재귀 탐색하여 docstring으로 의심되는 노드를 수집한다.
// 판단 기준: expression_statement의 유일한 named child가 string 계열 노드인 경우.
type docstringInfo struct {
	exprStmtNode *sitter.Node // expression_statement 노드
	stringNode   *sitter.Node // string / string_content 노드
	parentType   string       // expression_statement의 부모 타입
	parentChain  []string     // 루트부터의 노드 타입 체인
}

func findDocstringNodes(root *sitter.Node) []docstringInfo {
	var results []docstringInfo
	collectDocstringNodes(root, nil, &results)
	return results
}

func collectDocstringNodes(node *sitter.Node, ancestors []*sitter.Node, results *[]docstringInfo) {
	if node == nil {
		return
	}

	// expression_statement 를 발견하면 그 안에 string 노드만 있는지 확인
	if node.Type() == "expression_statement" {
		namedChildren := make([]*sitter.Node, 0)
		for i := 0; i < int(node.NamedChildCount()); i++ {
			ch := node.NamedChild(i)
			if ch != nil {
				namedChildren = append(namedChildren, ch)
			}
		}
		if len(namedChildren) == 1 {
			ch := namedChildren[0]
			chType := ch.Type()
			// string, concatenated_string, f-string(formatted_string) 등을 대상으로 한다
			if chType == "string" || chType == "concatenated_string" ||
				strings.Contains(chType, "string") {
				// 부모 체인 구성
				chain := make([]string, len(ancestors)+1)
				for i, a := range ancestors {
					chain[i] = a.Type()
				}
				chain[len(ancestors)] = node.Type()

				parentType := ""
				if len(ancestors) > 0 {
					parentType = ancestors[len(ancestors)-1].Type()
				}

				*results = append(*results, docstringInfo{
					exprStmtNode: node,
					stringNode:   ch,
					parentType:   parentType,
					parentChain:  chain,
				})
			}
		}
	}

	newAncestors := append(ancestors, node)
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child != nil {
			collectDocstringNodes(child, newAncestors, results)
		}
	}
}

// parseRawTree는 tree-sitter Python 파서로 직접 AST를 반환한다.
func parseRawTree(t *testing.T, content []byte) *sitter.Node {
	t.Helper()
	w := NewWalker(PythonSpec)
	if w.parser == nil {
		t.Fatal("Python 파서 초기화 실패")
	}
	// parser를 직접 호출하려면 lock이 필요하나, 테스트 내에서는 단독 사용이므로 안전.
	tree, err := w.parser.ParseCtx(context.Background(), nil, content)
	if err != nil {
		t.Fatalf("tree-sitter 파싱 실패: %v", err)
	}
	// tree는 테스트 종료 후 GC에 맡긴다 (Close 타이밍을 defer로 처리)
	t.Cleanup(func() { tree.Close() })
	return tree.RootNode()
}

// stringNodeDetail은 string 노드의 상세 정보를 분석한다.
type stringNodeDetail struct {
	nodeType        string // "string", "string_content", 기타
	startLine       int
	endLine         int
	isOneLiner      bool
	prefixToken     string // "r", "f", "b", "rb" 등 (없으면 "")
	quoteStyle      string // `"""` or `'''` or `"` or `'`
	rawContent      string // 노드 Content의 첫 80자
}

func analyzeStringNode(node *sitter.Node, content []byte) stringNodeDetail {
	detail := stringNodeDetail{
		nodeType:  node.Type(),
		startLine: int(node.StartPoint().Row) + 1,
		endLine:   int(node.EndPoint().Row) + 1,
	}
	detail.isOneLiner = detail.startLine == detail.endLine

	raw := node.Content(content)
	if len(raw) > 80 {
		detail.rawContent = raw[:80] + "..."
	} else {
		detail.rawContent = raw
	}

	// prefix 및 따옴표 스타일 추론 (노드 텍스트 기반)
	stripped := raw
	lower := strings.ToLower(stripped)
	for _, p := range []string{"rb", "br", "rf", "fr", "r", "f", "b", "u"} {
		if strings.HasPrefix(lower, p) {
			detail.prefixToken = p
			stripped = stripped[len(p):]
			break
		}
	}
	if strings.HasPrefix(stripped, `"""`) {
		detail.quoteStyle = `"""`
	} else if strings.HasPrefix(stripped, `'''`) {
		detail.quoteStyle = `'''`
	} else if strings.HasPrefix(stripped, `"`) {
		detail.quoteStyle = `"`
	} else if strings.HasPrefix(stripped, `'`) {
		detail.quoteStyle = `'`
	}

	// tree-sitter 자식 노드에서 string_start / prefix 확인
	for i := 0; i < int(node.ChildCount()); i++ {
		ch := node.Child(i)
		if ch == nil {
			continue
		}
		if ch.Type() == "string_start" {
			startTok := ch.Content(content)
			// string_start 토큰에서 prefix를 재추론
			lower2 := strings.ToLower(startTok)
			for _, p := range []string{"rb", "br", "rf", "fr", "r", "f", "b", "u"} {
				if strings.HasPrefix(lower2, p) {
					detail.prefixToken = p
					break
				}
			}
			if detail.quoteStyle == "" {
				rest := startTok[len(detail.prefixToken):]
				if strings.HasPrefix(rest, `"""`) {
					detail.quoteStyle = `"""`
				} else if strings.HasPrefix(rest, `'''`) {
					detail.quoteStyle = `'''`
				}
			}
		}
	}

	return detail
}

// ─────────────────────────────────────────────────────────────
// 결과 집계 구조체
// ─────────────────────────────────────────────────────────────

type docstringMeasurement struct {
	fixture           string
	nodeType          string   // string 노드 타입
	parentChain       []string // 부모 체인 (루트 방향)
	startLine         int
	endLine           int
	prefixToken       string
	quoteStyle        string
	isOneLiner        bool
	inCommentBlock    bool   // 현재 CommentBlock에 포함되는가
	intentBound       bool   // @intent 바인딩 성공 여부
	bindingNote       string // 추가 메모
}

func (m docstringMeasurement) parentChainStr() string {
	return strings.Join(m.parentChain, " > ")
}

// ─────────────────────────────────────────────────────────────
// 공통 실측 함수
// ─────────────────────────────────────────────────────────────

// measureDocstringFixture 는 하나의 Python fixture 파일에 대해:
//  1. ParseWithComments를 실행하여 CommentBlock과 노드를 얻고,
//  2. 직접 AST를 탐색하여 docstring 노드의 구조를 측정한다.
//
// t.Logf로 모든 중간 결과를 기록하고 docstringMeasurement를 반환한다.
func measureDocstringFixture(
	t *testing.T,
	filename string,
	targetSymName string,    // 바인딩 대상 심볼 이름 ("" 이면 file 노드)
	targetSymKind model.NodeKind,
) docstringMeasurement {
	t.Helper()

	content := readFixture(t, "python", filename)
	t.Logf("─── fixture: %s ───", filename)
	t.Logf("내용:\n%s", string(content))

	// 1) Walker.ParseWithComments로 노드/주석 수집
	w := NewWalker(PythonSpec)
	nodes, _, walkerComments, err := w.ParseWithComments(context.Background(), filename, content)
	if err != nil {
		t.Fatalf("파싱 실패: %v", err)
	}

	t.Log("--- 파싱된 노드 ---")
	logNodeInfo(t, nodes)

	t.Log("--- 파싱된 CommentBlock ---")
	logCommentInfo(t, walkerComments)

	// 2) 직접 AST 탐색으로 docstring 노드 측정
	root := parseRawTree(t, content)
	t.Log("--- 전체 AST (depth≤5) ---")
	dumpAST(t, root, content, 0, 5)

	docstringNodes := findDocstringNodes(root)
	t.Logf("--- expression_statement>string 후보 노드 수: %d ---", len(docstringNodes))

	m := docstringMeasurement{fixture: filename}

	for idx, di := range docstringNodes {
		detail := analyzeStringNode(di.stringNode, content)
		t.Logf("[docstring후보 #%d]", idx)
		t.Logf("  expression_statement: line=%d-%d",
			di.exprStmtNode.StartPoint().Row+1,
			di.exprStmtNode.EndPoint().Row+1)
		t.Logf("  string 노드타입:   %s", detail.nodeType)
		t.Logf("  부모 체인:         %s", strings.Join(di.parentChain, " > "))
		t.Logf("  부모 타입:         %s", di.parentType)
		t.Logf("  StartLine-EndLine: %d-%d (oneliner=%v)", detail.startLine, detail.endLine, detail.isOneLiner)
		t.Logf("  prefix:            %q", detail.prefixToken)
		t.Logf("  quoteStyle:        %q", detail.quoteStyle)
		t.Logf("  raw(80):           %q", detail.rawContent)

		// 자식 노드 상세 (string_start, string_content, string_end)
		for i := 0; i < int(di.stringNode.ChildCount()); i++ {
			ch := di.stringNode.Child(i)
			if ch == nil {
				continue
			}
			chText := ch.Content(content)
			if len(chText) > 40 {
				chText = chText[:40] + "..."
			}
			t.Logf("    string child[%d]: type=%-20s %q", i, ch.Type(), chText)
		}

		// 첫 번째 docstring만 결과에 기록 (테이블 1행 = 1 fixture)
		if idx == 0 {
			m.nodeType = detail.nodeType
			m.parentChain = di.parentChain
			m.startLine = detail.startLine
			m.endLine = detail.endLine
			m.prefixToken = detail.prefixToken
			m.quoteStyle = detail.quoteStyle
			m.isOneLiner = detail.isOneLiner
		}
	}

	if len(docstringNodes) == 0 {
		t.Logf("  [주의] expression_statement>string 노드가 발견되지 않음")
	}

	// 3) CommentBlock에 포함 여부 확인
	// docstring이 walkerComments에 어느 CommentBlock으로라도 들어오는지 확인
	m.inCommentBlock = false
	if len(docstringNodes) > 0 {
		dsStart := m.startLine
		dsEnd := m.endLine
		for _, cb := range walkerComments {
			if cb.StartLine <= dsStart && dsEnd <= cb.EndLine {
				m.inCommentBlock = true
				t.Logf("  [CommentBlock 포함] StartLine=%d EndLine=%d Text=%q",
					cb.StartLine, cb.EndLine, cb.Text[:min(len(cb.Text), 60)])
				break
			}
		}
	}
	if !m.inCommentBlock {
		t.Logf("  [CommentBlock 미포함] → collectComments가 이 docstring을 수집하지 못함")
	}

	// 4) Binder 바인딩 결과 확인
	b := parse.NewBinder()
	binderComments := binderFromWalkerComments(walkerComments)
	bindings := b.Bind(binderComments, nodes, "python", strings.Split(string(content), "\n"))

	for _, binding := range bindings {
		var matchName bool
		if targetSymName == "" {
			matchName = binding.Node.Kind == model.NodeKindFile
		} else {
			matchName = binding.Node.Name == targetSymName && binding.Node.Kind == targetSymKind
		}
		if !matchName {
			continue
		}
		for _, tag := range binding.Annotation.Tags {
			if tag.Kind == "intent" {
				m.intentBound = true
				m.bindingNote = fmt.Sprintf("@intent=%q", tag.Value)
				t.Logf("  [바인딩 성공] %s → @intent=%q", binding.Node.Name, tag.Value)
				break
			}
		}
	}
	if !m.intentBound {
		t.Logf("  [바인딩 없음] %s 심볼에 @intent 바인딩 없음", targetSymName)
	}

	return m
}

// ─────────────────────────────────────────────────────────────
// 개별 fixture 테스트
// ─────────────────────────────────────────────────────────────

// TestPythonDocstring_FuncDouble 은 함수 본문 내 """...""" docstring을 실측한다.
//
// 검증 포인트:
//   - string 노드가 function_definition > block > expression_statement 하위에 있는가
//   - collectComments가 이를 수집하는가 (예상: 미수집, comment 노드 타입이 아니므로)
//   - @intent 바인딩이 성공하는가 (예상: 실패)
func TestPythonDocstring_FuncDouble(t *testing.T) {
	m := measureDocstringFixture(t, "docstring_func_double.py", "get_user", model.NodeKindFunction)

	t.Logf("=== 실측 결과 요약 ===")
	t.Logf("  nodeType:       %s", m.nodeType)
	t.Logf("  parentChain:    %s", m.parentChainStr())
	t.Logf("  line:           %d-%d (oneliner=%v)", m.startLine, m.endLine, m.isOneLiner)
	t.Logf("  prefix:         %q", m.prefixToken)
	t.Logf("  quoteStyle:     %q", m.quoteStyle)
	t.Logf("  inCommentBlock: %v", m.inCommentBlock)
	t.Logf("  intentBound:    %v (%s)", m.intentBound, m.bindingNote)

	// [Red] 현재 동작 기록: collectComments는 docstring을 수집하지 않으므로 바인딩 불가
	if m.intentBound {
		t.Logf("[예상 외 성공] get_user @intent 바인딩 성공 → 원인 분석 필요")
	} else {
		t.Errorf("[Red] get_user @intent 바인딩 없음 — docstring이 CommentBlock으로 승격되지 않음")
	}
}

// TestPythonDocstring_FuncSingle 은 함수 본문 내 '''...''' docstring을 실측한다.
//
// 검증 포인트:
//   - """와 '''가 동일한 tree-sitter 노드 타입("string")으로 처리되는가
func TestPythonDocstring_FuncSingle(t *testing.T) {
	m := measureDocstringFixture(t, "docstring_func_single.py", "get_user", model.NodeKindFunction)

	t.Logf("=== 실측 결과 요약 ===")
	t.Logf("  nodeType:       %s", m.nodeType)
	t.Logf("  parentChain:    %s", m.parentChainStr())
	t.Logf("  line:           %d-%d (oneliner=%v)", m.startLine, m.endLine, m.isOneLiner)
	t.Logf("  prefix:         %q", m.prefixToken)
	t.Logf("  quoteStyle:     %q", m.quoteStyle)
	t.Logf("  inCommentBlock: %v", m.inCommentBlock)
	t.Logf("  intentBound:    %v (%s)", m.intentBound, m.bindingNote)

	if m.intentBound {
		t.Logf("[예상 외 성공] get_user @intent 바인딩 성공")
	} else {
		t.Errorf("[Red] get_user @intent 바인딩 없음 — '''...''' docstring이 CommentBlock으로 승격되지 않음")
	}
}

// TestPythonDocstring_OneLine 은 함수 본문 내 한 줄 """...""" docstring을 실측한다.
//
// 검증 포인트:
//   - StartLine == EndLine 인가
//   - binder의 gap < 1 조건과 별도로, body 내부 docstring이라 gap 자체가 음수인가
func TestPythonDocstring_OneLine(t *testing.T) {
	m := measureDocstringFixture(t, "docstring_oneline.py", "add", model.NodeKindFunction)

	t.Logf("=== 실측 결과 요약 ===")
	t.Logf("  nodeType:       %s", m.nodeType)
	t.Logf("  parentChain:    %s", m.parentChainStr())
	t.Logf("  line:           %d-%d (oneliner=%v)", m.startLine, m.endLine, m.isOneLiner)
	t.Logf("  prefix:         %q", m.prefixToken)
	t.Logf("  quoteStyle:     %q", m.quoteStyle)
	t.Logf("  inCommentBlock: %v", m.inCommentBlock)
	t.Logf("  intentBound:    %v (%s)", m.intentBound, m.bindingNote)

	if m.intentBound {
		t.Logf("[예상 외 성공] add @intent 바인딩 성공")
	} else {
		t.Errorf("[Red] add @intent 바인딩 없음 — 한 줄 docstring이 CommentBlock으로 승격되지 않음")
	}
}

// TestPythonDocstring_Class 는 클래스 본문 내 """...""" docstring을 실측한다.
//
// 검증 포인트:
//   - 부모 체인이 class_definition > block > expression_statement 구조인가
//   - @dataclass 데코레이터가 있을 때 클래스 StartLine은?
func TestPythonDocstring_Class(t *testing.T) {
	m := measureDocstringFixture(t, "docstring_class.py", "User", model.NodeKindClass)

	t.Logf("=== 실측 결과 요약 ===")
	t.Logf("  nodeType:       %s", m.nodeType)
	t.Logf("  parentChain:    %s", m.parentChainStr())
	t.Logf("  line:           %d-%d (oneliner=%v)", m.startLine, m.endLine, m.isOneLiner)
	t.Logf("  prefix:         %q", m.prefixToken)
	t.Logf("  quoteStyle:     %q", m.quoteStyle)
	t.Logf("  inCommentBlock: %v", m.inCommentBlock)
	t.Logf("  intentBound:    %v (%s)", m.intentBound, m.bindingNote)

	if m.intentBound {
		t.Logf("[예상 외 성공] User @intent 바인딩 성공")
	} else {
		t.Errorf("[Red] User @intent 바인딩 없음 — 클래스 docstring이 CommentBlock으로 승격되지 않음")
	}
}

// TestPythonDocstring_Module 은 모듈 레벨 """...""" docstring을 실측한다.
//
// 검증 포인트:
//   - 부모 체인이 module > expression_statement 구조인가 (함수/클래스의 body > block 없음)
//   - binder가 file 노드에 첫 번째 CommentBlock을 바인딩하는 로직과 다름에 주의
//   - collectComments가 이를 수집하는가
func TestPythonDocstring_Module(t *testing.T) {
	// 모듈 docstring은 file 노드에 바인딩되어야 하므로 targetSymName = "" (file 노드 처리)
	m := measureDocstringFixture(t, "docstring_module.py", "", model.NodeKindFile)

	t.Logf("=== 실측 결과 요약 ===")
	t.Logf("  nodeType:       %s", m.nodeType)
	t.Logf("  parentChain:    %s", m.parentChainStr())
	t.Logf("  line:           %d-%d (oneliner=%v)", m.startLine, m.endLine, m.isOneLiner)
	t.Logf("  prefix:         %q", m.prefixToken)
	t.Logf("  quoteStyle:     %q", m.quoteStyle)
	t.Logf("  inCommentBlock: %v", m.inCommentBlock)
	t.Logf("  intentBound:    %v (%s)", m.intentBound, m.bindingNote)

	if m.intentBound {
		t.Logf("[예상 외 성공] file 노드 @intent 바인딩 성공")
	} else {
		t.Errorf("[Red] file 노드 @intent 바인딩 없음 — 모듈 docstring이 CommentBlock으로 승격되지 않음")
	}
}

// TestPythonDocstring_Prefix 는 r"""...""" 및 f"""...""" prefix 변형 docstring을 실측한다.
//
// 검증 포인트:
//   - prefix가 붙은 string이 여전히 "string" 노드 타입인가, 아니면 "string"과 다른가
//   - string_start 자식 토큰에서 prefix가 어떻게 표현되는가
//   - f-string은 Python 스펙상 docstring이 아니지만 tree-sitter는 어떻게 파싱하는가
func TestPythonDocstring_Prefix(t *testing.T) {
	content := readFixture(t, "python", "docstring_prefix.py")
	t.Logf("fixture 내용:\n%s", string(content))

	// 직접 AST 탐색
	root := parseRawTree(t, content)
	t.Log("--- 전체 AST (depth≤6) ---")
	dumpAST(t, root, content, 0, 6)

	// expression_statement > string 후보 수집
	docstringNodes := findDocstringNodes(root)
	t.Logf("--- expression_statement>string 후보 노드 수: %d ---", len(docstringNodes))

	var rStrDetail, fStrDetail *stringNodeDetail

	for idx, di := range docstringNodes {
		detail := analyzeStringNode(di.stringNode, content)
		t.Logf("[docstring후보 #%d]", idx)
		t.Logf("  string 노드타입:   %s", detail.nodeType)
		t.Logf("  부모 체인:         %s", strings.Join(di.parentChain, " > "))
		t.Logf("  StartLine-EndLine: %d-%d", detail.startLine, detail.endLine)
		t.Logf("  prefix:            %q", detail.prefixToken)
		t.Logf("  quoteStyle:        %q", detail.quoteStyle)
		t.Logf("  raw(80):           %q", detail.rawContent)

		for i := 0; i < int(di.stringNode.ChildCount()); i++ {
			ch := di.stringNode.Child(i)
			if ch == nil {
				continue
			}
			chText := ch.Content(content)
			if len(chText) > 40 {
				chText = chText[:40] + "..."
			}
			t.Logf("    string child[%d]: type=%-20s %q", i, ch.Type(), chText)
		}

		d := detail
		switch idx {
		case 0:
			rStrDetail = &d
		case 1:
			fStrDetail = &d
		}
	}

	// CommentBlock 수집 결과
	w := NewWalker(PythonSpec)
	_, _, walkerComments, err := w.ParseWithComments(context.Background(), "docstring_prefix.py", content)
	if err != nil {
		t.Fatalf("파싱 실패: %v", err)
	}
	t.Log("--- 파싱된 CommentBlock ---")
	logCommentInfo(t, walkerComments)

	t.Logf("=== r\"\"\"...\"\"\" 실측 결과 ===")
	if rStrDetail != nil {
		t.Logf("  nodeType:    %s", rStrDetail.nodeType)
		t.Logf("  prefix:      %q", rStrDetail.prefixToken)
		t.Logf("  quoteStyle:  %q", rStrDetail.quoteStyle)
		t.Logf("  line:        %d-%d", rStrDetail.startLine, rStrDetail.endLine)
	} else {
		t.Logf("  [주의] r\"\"\"...\"\"\" 노드 미발견")
	}

	t.Logf("=== f\"\"\"...\"\"\" 실측 결과 ===")
	if fStrDetail != nil {
		t.Logf("  nodeType:    %s", fStrDetail.nodeType)
		t.Logf("  prefix:      %q", fStrDetail.prefixToken)
		t.Logf("  quoteStyle:  %q", fStrDetail.quoteStyle)
		t.Logf("  line:        %d-%d", fStrDetail.startLine, fStrDetail.endLine)
	} else {
		t.Logf("  [주의] f\"\"\"...\"\"\" 노드 미발견 (별도 노드 타입일 수 있음)")
	}
}

// ─────────────────────────────────────────────────────────────
// 종합 결과 테스트 — 5+1 fixture 전체 실측 테이블
// ─────────────────────────────────────────────────────────────

// TestPythonDocstring_AllVariants_Summary 는 6개 fixture를 한 번에 실행하여
// 결과를 표 형태로 출력하는 종합 진단 테스트다.
// 이 테스트 자체는 항상 Pass (진단 전용).
func TestPythonDocstring_AllVariants_Summary(t *testing.T) {
	type fixtureCase struct {
		filename   string
		symName    string
		symKind    model.NodeKind
		label      string
	}

	cases := []fixtureCase{
		{"docstring_func_double.py", "get_user", model.NodeKindFunction, "func+\"\"\""},
		{"docstring_func_single.py", "get_user", model.NodeKindFunction, "func+'''"},
		{"docstring_oneline.py", "add", model.NodeKindFunction, "oneliner"},
		{"docstring_class.py", "User", model.NodeKindClass, "class"},
		{"docstring_module.py", "", model.NodeKindFile, "module"},
	}

	type row struct {
		label         string
		nodeType      string
		parentChain   string
		lines         string
		prefix        string
		quoteStyle    string
		inComment     bool
		intentBound   bool
	}
	var rows []row

	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			content := readFixture(t, "python", tc.filename)
			root := parseRawTree(t, content)
			docstringNodes := findDocstringNodes(root)

			w := NewWalker(PythonSpec)
			nodes, _, walkerComments, err := w.ParseWithComments(context.Background(), tc.filename, content)
			if err != nil {
				t.Fatalf("파싱 실패: %v", err)
			}

			r := row{label: tc.label}

			if len(docstringNodes) > 0 {
				di := docstringNodes[0]
				detail := analyzeStringNode(di.stringNode, content)
				r.nodeType = detail.nodeType
				r.parentChain = strings.Join(di.parentChain, " > ")
				r.lines = fmt.Sprintf("%d-%d", detail.startLine, detail.endLine)
				r.prefix = detail.prefixToken
				r.quoteStyle = detail.quoteStyle

				// CommentBlock 포함 여부
				for _, cb := range walkerComments {
					if cb.StartLine <= detail.startLine && detail.endLine <= cb.EndLine {
						r.inComment = true
						break
					}
				}
			} else {
				r.nodeType = "(not found)"
				r.parentChain = "(not found)"
				r.lines = "-"
			}

		// 바인딩 확인
		b := parse.NewBinder()
		binderComments := binderFromWalkerComments(walkerComments)
		bindings := b.Bind(binderComments, nodes, "python", strings.Split(string(content), "\n"))
			for _, binding := range bindings {
				var match bool
				if tc.symName == "" {
					match = binding.Node.Kind == model.NodeKindFile
				} else {
					match = binding.Node.Name == tc.symName && binding.Node.Kind == tc.symKind
				}
				if !match {
					continue
				}
				for _, tag := range binding.Annotation.Tags {
					if tag.Kind == "intent" {
						r.intentBound = true
						break
					}
				}
			}

			rows = append(rows, r)

			t.Logf("[%s] nodeType=%s parentChain=%s lines=%s prefix=%q inComment=%v intentBound=%v",
				tc.label, r.nodeType, r.parentChain, r.lines, r.prefix, r.inComment, r.intentBound)
		})
	}

	// prefix 변형은 별도 측정
	t.Run("prefix_variants", func(t *testing.T) {
		content := readFixture(t, "python", "docstring_prefix.py")
		root := parseRawTree(t, content)
		docstringNodes := findDocstringNodes(root)

		t.Logf("prefix fixture: docstring 후보 노드 수=%d", len(docstringNodes))
		for idx, di := range docstringNodes {
			detail := analyzeStringNode(di.stringNode, content)
			t.Logf("[prefix #%d] nodeType=%s prefix=%q quoteStyle=%q parentChain=%s",
				idx, detail.nodeType, detail.prefixToken, detail.quoteStyle,
				strings.Join(di.parentChain, " > "))
		}
	})

	// ── 최종 집계 표 출력 ──
	t.Log("==============================================================================")
	t.Log("fixture            | nodeType | parentChain(끝 2단계)         | lines | prefix | inComment | intentBound")
	t.Log("──────────────────────────────────────────────────────────────────────────────")
	for _, r := range rows {
		// parentChain에서 마지막 2단계만 표시
		parts := strings.Split(r.parentChain, " > ")
		chain2 := r.parentChain
		if len(parts) >= 2 {
			chain2 = strings.Join(parts[len(parts)-2:], " > ")
		}
		prefix := r.prefix
		if prefix == "" {
			prefix = "(none)"
		}
		t.Logf("%-18s | %-8s | %-29s | %-5s | %-6s | %-9v | %v",
			r.label, r.nodeType, chain2, r.lines, prefix, r.inComment, r.intentBound)
	}
	t.Log("==============================================================================")
}

// ─────────────────────────────────────────────────────────────
// gap 동작 상세 분석 테스트
// ─────────────────────────────────────────────────────────────

// TestPythonDocstring_GapAnalysis 는 docstring을 가상으로 CommentBlock에 주입했을 때
// binder.Bind의 gap 계산이 어떻게 되는지 시뮬레이션한다.
// (구현 수정 없이, 단순히 gap 계산 로직을 이해하기 위한 측정)
func TestPythonDocstring_GapAnalysis(t *testing.T) {
	type gapCase struct {
		name            string
		filename        string
		symName         string
		symKind         model.NodeKind
		// docstring이 CommentBlock으로 주입될 때 예상 StartLine, EndLine
		fakeCommentStart int
		fakeCommentEnd   int
	}

	cases := []gapCase{
		// docstring_func_double.py:
		//   Line 1: @app.route
		//   Line 2: @login_required
		//   Line 3: def get_user():
		//   Line 4:     """   ← docstring StartLine
		//   Line 7:     """   ← docstring EndLine
		// get_user StartLine은 tree-sitter 실측 후 결정됨.
		// docstring이 함수 body 내부에 있으므로 get_user.StartLine보다 항상 크다.
		// → gap = get_user.StartLine - docstring.EndLine < 0 → 바인딩 불가능.
		{
			name:             "func_double(body내부→gap음수예상)",
			filename:         "docstring_func_double.py",
			symName:          "get_user",
			symKind:          model.NodeKindFunction,
			fakeCommentStart: 4,
			fakeCommentEnd:   7,
		},
		// docstring_oneline.py:
		//   Line 1: def add(a, b):
		//   Line 2:     """@intent ..."""  ← docstring StartLine == EndLine == 2
		// add StartLine = 1 (def 키워드 줄)
		// gap = 1 - 2 = -1 < 0 → 바인딩 불가능.
		{
			name:             "oneliner(body내부→gap음수예상)",
			filename:         "docstring_oneline.py",
			symName:          "add",
			symKind:          model.NodeKindFunction,
			fakeCommentStart: 2,
			fakeCommentEnd:   2,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			content := readFixture(t, "python", tc.filename)
			w := NewWalker(PythonSpec)
			nodes, _, _, err := w.ParseWithComments(context.Background(), tc.filename, content)
			if err != nil {
				t.Fatalf("파싱 실패: %v", err)
			}

			// 대상 심볼 찾기
			var targetNode *model.Node
			for i := range nodes {
				if nodes[i].Name == tc.symName && nodes[i].Kind == tc.symKind {
					targetNode = &nodes[i]
					break
				}
			}
			if targetNode == nil {
				t.Fatalf("%s 노드 미발견", tc.symName)
			}

			t.Logf("[%s] %s.StartLine=%d", tc.filename, tc.symName, targetNode.StartLine)
			t.Logf("[%s] 가상 CommentBlock StartLine=%d EndLine=%d",
				tc.filename, tc.fakeCommentStart, tc.fakeCommentEnd)

			gap := targetNode.StartLine - tc.fakeCommentEnd
			t.Logf("[%s] gap = %d.StartLine(%d) - fakeCommentEnd(%d) = %d",
				tc.filename, targetNode.StartLine, targetNode.StartLine, tc.fakeCommentEnd, gap)

			const maxGapConst = 2
			if gap < 1 {
				t.Logf("[%s] gap=%d < 1 → binder가 skip (docstring이 심볼 body 내부에 위치)", tc.filename, gap)
				t.Logf("  결론: 함수 body docstring을 주석 기반 gap으로 바인딩하는 것은 구조적으로 불가능.")
				t.Logf("  walker 확장 시 docstring의 StartLine 대신 '귀속 심볼의 StartLine'으로 특수 처리 필요.")
			} else if gap > maxGapConst {
				t.Logf("[%s] gap=%d > maxGap(%d) → binder가 skip", tc.filename, gap, maxGapConst)
			} else {
				t.Logf("[%s] gap=%d → binder 조건 충족 (바인딩 가능)", tc.filename, gap)
			}
		})
	}
}
