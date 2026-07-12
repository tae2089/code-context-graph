package wiki_test

import (
	"fmt"
	"github.com/tae2089/code-context-graph/internal/app/wiki"
	"testing"
)

// TestSearch_MatchesLabel: query가 label에 매칭되면 결과를 반환한다.
func TestSearch_MatchesLabel(t *testing.T) {
	root := &wiki.TreeNode{
		ID:    "root",
		Label: "Root",
		Children: []*wiki.TreeNode{
			{
				ID:      "community:auth",
				Label:   "MCP Server",
				Summary: "핸들러 레이어",
				Children: []*wiki.TreeNode{
					{ID: "file:handlers.go", Label: "handlers.go", Summary: "MCP 핸들러"},
				},
			},
			{
				ID:      "community:core",
				Label:   "Core Logic",
				Summary: "비즈니스 로직",
			},
		},
	}

	results := wiki.Search(root, "mcp", 10)
	if len(results) != 2 {
		t.Fatalf("expected 2 results (community + file), got %d", len(results))
	}
}

// TestSearch_CaseInsensitive: 검색은 대소문자를 구분하지 않는다.
func TestSearch_CaseInsensitive(t *testing.T) {
	root := &wiki.TreeNode{
		ID:    "root",
		Label: "Root",
		Children: []*wiki.TreeNode{
			{ID: "c1", Label: "Auth Service", Summary: "JWT 인증"},
		},
	}
	results := wiki.Search(root, "AUTH", 10)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
}

// TestSearch_MaxResults: maxResults 파라미터가 결과 수를 제한한다.
func TestSearch_MaxResults(t *testing.T) {
	children := make([]*wiki.TreeNode, 10)
	for i := range children {
		children[i] = &wiki.TreeNode{ID: fmt.Sprintf("c%d", i), Label: "test node", Summary: "test"}
	}
	root := &wiki.TreeNode{ID: "root", Children: children}

	results := wiki.Search(root, "test", 3)
	if len(results) != 3 {
		t.Fatalf("expected 3 results (maxResults), got %d", len(results))
	}
}

// TestSearch_IncludesBreadcrumb: 결과에 Path(breadcrumb)가 포함된다.
func TestSearch_IncludesBreadcrumb(t *testing.T) {
	root := &wiki.TreeNode{
		ID:    "root",
		Label: "Root",
		Children: []*wiki.TreeNode{
			{
				ID:    "community:auth",
				Label: "Auth",
				Children: []*wiki.TreeNode{
					{ID: "file:login.go", Label: "login.go", Summary: "로그인 처리"},
				},
			},
		},
	}
	results := wiki.Search(root, "로그인", 10)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if len(results[0].Path) != 3 {
		t.Fatalf("expected path length 3 (root→auth→file), got %d: %v", len(results[0].Path), results[0].Path)
	}
}

func TestSearch_MatchesSearchText(t *testing.T) {
	root := &wiki.TreeNode{
		ID:    "root",
		Label: "Root",
		Children: []*wiki.TreeNode{
			{ID: "file:policy.go", Label: "policy.go", Summary: "access policy", SearchText: "domainRule 관리자 승인 필요"},
		},
	}
	results := wiki.Search(root, "관리자", 10)
	if len(results) != 1 {
		t.Fatalf("expected search_text result, got %d", len(results))
	}
	if results[0].ID != "file:policy.go" {
		t.Fatalf("result ID = %q", results[0].ID)
	}
}

// TestSearch_NoMatch: 매칭 없으면 빈 슬라이스 반환.
func TestSearch_NoMatch(t *testing.T) {
	root := &wiki.TreeNode{ID: "root", Label: "Root", Children: []*wiki.TreeNode{
		{ID: "c1", Label: "Auth", Summary: "인증"},
	}}
	results := wiki.Search(root, "zzznomatch", 10)
	if len(results) != 0 {
		t.Fatalf("expected 0 results, got %d", len(results))
	}
}

// TestFindNode: FindNode가 재귀적으로 트리에서 노드를 찾는지 검증한다.
func TestFindNode(t *testing.T) {
	root := &wiki.TreeNode{
		ID:    "root",
		Label: "Root",
		Children: []*wiki.TreeNode{
			{
				ID:    "child1",
				Label: "Child 1",
				Children: []*wiki.TreeNode{
					{ID: "grandchild1", Label: "Grandchild 1"},
				},
			},
			{ID: "child2", Label: "Child 2"},
		},
	}

	tests := []struct {
		id      string
		wantNil bool
	}{
		{"root", false},
		{"child1", false},
		{"grandchild1", false},
		{"child2", false},
		{"notfound", true},
	}

	for _, tc := range tests {
		t.Run(tc.id, func(t *testing.T) {
			got := wiki.FindNode(root, tc.id)
			if tc.wantNil && got != nil {
				t.Errorf("FindNode(%q) = %v, want nil", tc.id, got)
			}
			if !tc.wantNil && got == nil {
				t.Errorf("FindNode(%q) = nil, want non-nil", tc.id)
			}
			if got != nil && got.ID != tc.id {
				t.Errorf("FindNode(%q).ID = %q, want %q", tc.id, got.ID, tc.id)
			}
		})
	}
}
