package parse

import (
	"testing"

	"github.com/imtaebin/code-context-graph/internal/model"
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
