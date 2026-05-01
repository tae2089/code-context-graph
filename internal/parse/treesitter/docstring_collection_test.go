// Python docstring 수집 단위 테스트.
//
// 목적: walker.collectDocstrings가 다양한 Python docstring 변형을
// CommentBlock(IsDocstring=true)으로 올바르게 수집하는지 검증한다.
//
// 이 파일은 결정 B+D 단계 1 (구조적 변경) 검증 전용이다.
// binder 동작(바인딩 결과)은 검증하지 않는다 — 그것은 단계 2 범위다.
// ParseWithComments 반환값도 검증하지 않는다 — 단계 1에서는 collectDocstrings를
// 직접 호출하여 수집 로직만 확인한다.
package treesitter

import (
	"context"
	"testing"
)

// collectDocstringsFromContent 는 테스트 헬퍼: Python 소스를 파싱하여
// collectDocstrings 결과를 반환한다.
func collectDocstringsFromContent(t *testing.T, content []byte) []CommentBlock {
	t.Helper()
	w := NewWalker(PythonSpec)
	if w.parser == nil {
		t.Fatal("Python 파서 초기화 실패")
	}

	tree, err := w.parser.ParseCtx(context.Background(), nil, content)
	if err != nil {
		t.Fatalf("tree-sitter 파싱 실패: %v", err)
	}
	defer tree.Close()

	root := tree.RootNode()
	// nodes는 OwnerStartLine 결정에 사용하지 않아도 되므로 빈 슬라이스 전달
	return w.collectDocstrings(root, content, nil)
}

// TestWalker_CollectDocstrings_FuncDouble 은 """...""" 함수 docstring이
// IsDocstring=true로 수집되는지 검증한다.
func TestWalker_CollectDocstrings_FuncDouble(t *testing.T) {
	content := readFixture(t, "python", "docstring_func_double.py")
	docstrings := collectDocstringsFromContent(t, content)

	t.Log("--- 수집된 docstring CommentBlock ---")
	for i, cb := range docstrings {
		t.Logf("  [%d] StartLine=%d EndLine=%d IsDocstring=%v OwnerStartLine=%d Text=%q",
			i, cb.StartLine, cb.EndLine, cb.IsDocstring, cb.OwnerStartLine,
			truncate(cb.Text, 60))
	}

	if len(docstrings) == 0 {
		t.Fatalf("[구조 검증 실패] docstring이 수집되지 않음")
	}

	cb := docstrings[0]
	if !cb.IsDocstring {
		t.Errorf("IsDocstring = false, want true")
	}
	// get_user는 @app.route+@login_required+def로 감싸진 decorated_definition
	// fixture: line1=@app.route, line2=@login_required, line3=def get_user()
	// decorated_definition.StartLine = 1
	if cb.OwnerStartLine == 0 {
		t.Errorf("함수 docstring의 OwnerStartLine=0, want >0")
	}
	t.Logf("[확인] IsDocstring=true, OwnerStartLine=%d", cb.OwnerStartLine)
}

// TestWalker_CollectDocstrings_AllFixtures 는 5개 fixture와 prefix 변형을
// 테이블 드리븐 테스트로 검증한다.
func TestWalker_CollectDocstrings_AllFixtures(t *testing.T) {
	type fixtureCase struct {
		label          string
		filename       string
		wantCount      int  // 수집되어야 할 docstring 수 (최소)
		wantModuleDs   bool // 모듈 docstring (OwnerStartLine=0)인가
		wantOwnerAbove bool // OwnerStartLine > 0 인가 (함수/클래스)
	}

	cases := []fixtureCase{
		{
			label:          "func_double(\"\"\")",
			filename:       "docstring_func_double.py",
			wantCount:      1,
			wantModuleDs:   false,
			wantOwnerAbove: true,
		},
		{
			label:          "func_single(''')",
			filename:       "docstring_func_single.py",
			wantCount:      1,
			wantModuleDs:   false,
			wantOwnerAbove: true,
		},
		{
			label:          "oneliner",
			filename:       "docstring_oneline.py",
			wantCount:      1,
			wantModuleDs:   false,
			wantOwnerAbove: true,
		},
		{
			label:          "class",
			filename:       "docstring_class.py",
			wantCount:      1,
			wantModuleDs:   false,
			wantOwnerAbove: true,
		},
		{
			label:          "module",
			filename:       "docstring_module.py",
			wantCount:      1,
			wantModuleDs:   true,
			wantOwnerAbove: false,
		},
		{
			label:          "prefix(plain/r/u only)",
			filename:       "docstring_prefix.py",
			wantCount:      2, // r/u prefix만 docstring으로 인정
			wantModuleDs:   false,
			wantOwnerAbove: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			content := readFixture(t, "python", tc.filename)
			docstrings := collectDocstringsFromContent(t, content)

			t.Logf("수집된 docstring 수: %d", len(docstrings))
			for i, cb := range docstrings {
				t.Logf("  [%d] StartLine=%d EndLine=%d IsDocstring=%v OwnerStartLine=%d",
					i, cb.StartLine, cb.EndLine, cb.IsDocstring, cb.OwnerStartLine)
			}

			if len(docstrings) < tc.wantCount {
				t.Errorf("[%s] docstring 수=%d, want >=%d", tc.label, len(docstrings), tc.wantCount)
				return
			}

			// 모든 블록이 IsDocstring=true여야 함
			for i, cb := range docstrings {
				if !cb.IsDocstring {
					t.Errorf("[%s] docstrings[%d].IsDocstring=false, want true", tc.label, i)
				}
			}

			// 첫 번째 docstring 검증
			first := docstrings[0]
			if tc.wantModuleDs {
				if first.OwnerStartLine != 0 {
					t.Errorf("[%s] 모듈 docstring의 OwnerStartLine=%d, want 0",
						tc.label, first.OwnerStartLine)
				} else {
					t.Logf("[%s] 모듈 docstring 확인: OwnerStartLine=0", tc.label)
				}
			}
			if tc.wantOwnerAbove {
				if first.OwnerStartLine <= 0 {
					t.Errorf("[%s] 함수/클래스 docstring의 OwnerStartLine=%d, want >0",
						tc.label, first.OwnerStartLine)
				} else {
					t.Logf("[%s] 함수/클래스 docstring 확인: OwnerStartLine=%d",
						tc.label, first.OwnerStartLine)
				}
			}

			t.Logf("[%s] docstring StartLine=%d EndLine=%d IsDocstring=%v",
				tc.label, first.StartLine, first.EndLine, first.IsDocstring)
		})
	}
}

// TestWalker_CollectDocstrings_OrderedByStartLine 은 mergeCommentBlocks가
// StartLine 오름차순으로 병합하는지 검증한다.
// (직접 merge 함수를 테스트)
func TestWalker_CollectDocstrings_OrderedByStartLine(t *testing.T) {
	// 모듈 docstring + 함수 2개가 있는 소스를 직접 작성
	src := []byte(`"""
@intent 모듈 docstring
"""

def foo():
    """@intent foo 함수."""
    pass

def bar():
    """@intent bar 함수."""
    pass
`)
	docstrings := collectDocstringsFromContent(t, src)

	t.Logf("수집된 docstring 수: %d", len(docstrings))
	for i, cb := range docstrings {
		t.Logf("  [%d] StartLine=%d EndLine=%d IsDocstring=%v OwnerStartLine=%d",
			i, cb.StartLine, cb.EndLine, cb.IsDocstring, cb.OwnerStartLine)
	}

	// 3개의 docstring이 모두 수집되었는지 확인
	if len(docstrings) < 3 {
		t.Errorf("3개의 docstring을 기대했지만 %d개만 수집됨", len(docstrings))
		return
	}

	// StartLine 오름차순 정렬 검증
	for i := 1; i < len(docstrings); i++ {
		if docstrings[i].StartLine < docstrings[i-1].StartLine {
			t.Errorf("StartLine 오름차순 위반: docstrings[%d].StartLine=%d < docstrings[%d].StartLine=%d",
				i, docstrings[i].StartLine, i-1, docstrings[i-1].StartLine)
		}
	}

	// mergeCommentBlocks도 직접 검증
	comments := []CommentBlock{
		{StartLine: 2, EndLine: 2, Text: "# comment at line 2"},
		{StartLine: 8, EndLine: 8, Text: "# comment at line 8"},
	}
	fakeDocstrings := []CommentBlock{
		{StartLine: 1, EndLine: 1, IsDocstring: true, OwnerStartLine: 0},
		{StartLine: 5, EndLine: 5, IsDocstring: true, OwnerStartLine: 4},
		{StartLine: 10, EndLine: 10, IsDocstring: true, OwnerStartLine: 9},
	}
	merged := mergeCommentBlocks(comments, fakeDocstrings)
	t.Logf("mergeCommentBlocks 결과 (총 %d개):", len(merged))
	for i, cb := range merged {
		t.Logf("  [%d] StartLine=%d IsDocstring=%v", i, cb.StartLine, cb.IsDocstring)
	}
	for i := 1; i < len(merged); i++ {
		if merged[i].StartLine < merged[i-1].StartLine {
			t.Errorf("merge 결과 StartLine 오름차순 위반: [%d].StartLine=%d < [%d].StartLine=%d",
				i, merged[i].StartLine, i-1, merged[i-1].StartLine)
		}
	}
}

// TestWalker_CollectDocstrings_NonPython 은 Python이 아닌 언어에서
// collectDocstrings가 호출되지 않음을 확인한다.
// ParseWithComments에서 python 분기만 collectDocstrings를 호출하는지 확인한다.
func TestWalker_CollectDocstrings_NonPython(t *testing.T) {
	src := []byte(`package main

func hello() {
}
`)
	w := NewWalker(GoSpec)
	_, _, comments, err := w.ParseWithComments(context.Background(), "main.go", src)
	if err != nil {
		t.Fatalf("파싱 실패: %v", err)
	}

	for _, cb := range comments {
		if cb.IsDocstring {
			t.Errorf("Go 파일에서 IsDocstring=true 블록 발견 — Python 전용이어야 함")
		}
	}
	t.Logf("Go 파일: IsDocstring=true 블록 없음 확인 (CommentBlock 수: %d)", len(comments))
}

// TestWalker_CollectDocstrings_SecondStringNotDocstring 은
// 함수 body 내 두 번째 string이 docstring으로 수집되지 않는지 검증한다.
func TestWalker_CollectDocstrings_SecondStringNotDocstring(t *testing.T) {
	src := []byte(`def foo():
    """@intent 첫 번째 docstring."""
    x = "두 번째 string — 변수 할당 내부라 expression_statement가 아님"
    return x
`)
	docstrings := collectDocstringsFromContent(t, src)

	for _, cb := range docstrings {
		t.Logf("docstring: StartLine=%d EndLine=%d Text=%q",
			cb.StartLine, cb.EndLine, truncate(cb.Text, 60))
	}

	// 첫 번째 string만 docstring이어야 함
	if len(docstrings) != 1 {
		t.Errorf("docstring 수 = %d, want 1 (첫 번째만)", len(docstrings))
	}
}

// truncate는 문자열이 maxLen보다 길면 잘라낸다.
func truncate(s string, maxLen int) string {
	if len(s) > maxLen {
		return s[:maxLen] + "..."
	}
	return s
}
