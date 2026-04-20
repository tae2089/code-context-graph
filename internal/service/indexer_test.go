// toBinderComments가 Walker의 CommentBlock 메타 필드를
// Binder의 CommentBlock로 누락 없이 옮기는지 검증하는 재발 방지 테스트.
//
// 배경: P0-2에서 추가된 IsDocstring/OwnerStartLine 필드가 초기 indexer 변환
// 루프에서 누락되어 Python docstring 바인딩이 프로덕션 경로에서 동작하지
// 않던 문제가 있었다 (code review에서 발견, 97dfb3b 에서 수정).
//
// 이 테스트는 Walker↔Binder 타입이 분기 진화할 경우 동일한 실수가
// 재발하지 않도록 변환 함수 단위로 필드 전파를 고정한다.
package service

import (
	"testing"

	"github.com/tae2089/code-context-graph/internal/parse/treesitter"
)

func TestToBinderComments_PreservesBasicFields(t *testing.T) {
	in := []treesitter.CommentBlock{
		{StartLine: 3, EndLine: 4, Text: "// hello"},
	}
	got := toBinderComments(in)
	if len(got) != 1 {
		t.Fatalf("len mismatch: got=%d want=1", len(got))
	}
	if got[0].StartLine != 3 || got[0].EndLine != 4 || got[0].Text != "// hello" {
		t.Errorf("basic fields lost: %+v", got[0])
	}
}

func TestToBinderComments_PreservesDocstringFields(t *testing.T) {
	in := []treesitter.CommentBlock{
		{
			StartLine:      5,
			EndLine:        7,
			Text:           `"""module docstring"""`,
			IsDocstring:    true,
			OwnerStartLine: 0,
		},
		{
			StartLine:      10,
			EndLine:        12,
			Text:           `"""func docstring"""`,
			IsDocstring:    true,
			OwnerStartLine: 9,
		},
	}
	got := toBinderComments(in)
	if len(got) != 2 {
		t.Fatalf("len mismatch: got=%d want=2", len(got))
	}

	if !got[0].IsDocstring || got[0].OwnerStartLine != 0 {
		t.Errorf("module docstring fields lost: IsDocstring=%v OwnerStartLine=%d",
			got[0].IsDocstring, got[0].OwnerStartLine)
	}
	if !got[1].IsDocstring || got[1].OwnerStartLine != 9 {
		t.Errorf("func docstring fields lost: IsDocstring=%v OwnerStartLine=%d",
			got[1].IsDocstring, got[1].OwnerStartLine)
	}
}

func TestToBinderComments_NonDocstringKeepsDefaults(t *testing.T) {
	in := []treesitter.CommentBlock{
		{StartLine: 1, EndLine: 1, Text: "# plain", IsDocstring: false, OwnerStartLine: 0},
	}
	got := toBinderComments(in)
	if got[0].IsDocstring || got[0].OwnerStartLine != 0 {
		t.Errorf("non-docstring contaminated: %+v", got[0])
	}
}

func TestToBinderComments_EmptyInput(t *testing.T) {
	got := toBinderComments(nil)
	if got == nil {
		t.Error("nil input should return empty (non-nil) slice for consistency")
	}
	if len(got) != 0 {
		t.Errorf("expected empty slice, got len=%d", len(got))
	}
}
