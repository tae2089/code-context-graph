package ragindex_test

import (
	"fmt"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/tae2089/code-context-graph/internal/ragindex"
	"github.com/tae2089/code-context-graph/internal/store/gormstore"
)

// setupDB는 테스트마다 고유한 인메모리 SQLite DB를 생성한다.
func setupDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: logger.Discard,
	})
	if err != nil {
		t.Fatalf("setupDB: open DB: %v", err)
	}
	if err := gormstore.New(db).AutoMigrate(); err != nil {
		t.Fatalf("setupDB: AutoMigrate: %v", err)
	}
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			sqlDB.Close()
		}
	})
	return db
}

// TestPruneTree_Depth1: depth=1이면 root와 직계 자식만 반환, 손자 노드 없음.
func TestPruneTree_Depth1(t *testing.T) {
	root := &ragindex.TreeNode{
		ID:    "root",
		Label: "Root",
		Children: []*ragindex.TreeNode{
			{
				ID:    "c1",
				Label: "Community 1",
				Children: []*ragindex.TreeNode{
					{ID: "f1", Label: "file.go"},
				},
			},
		},
	}

	result := ragindex.PruneTree(root, 1)
	if len(result.Children) != 1 {
		t.Fatalf("expected 1 child, got %d", len(result.Children))
	}
	if len(result.Children[0].Children) != 0 {
		t.Fatalf("expected 0 grandchildren at depth=1, got %d", len(result.Children[0].Children))
	}
	// 원본 트리는 변경되지 않아야 함
	if len(root.Children[0].Children) != 1 {
		t.Fatal("PruneTree must not modify the original tree")
	}
}

// TestPruneTree_NegativeDepth: depth <= 0이면 트리 전체를 반환한다.
func TestPruneTree_NegativeDepth(t *testing.T) {
	root := &ragindex.TreeNode{
		ID: "root",
		Children: []*ragindex.TreeNode{
			{ID: "c1", Children: []*ragindex.TreeNode{{ID: "f1"}}},
		},
	}

	for _, depth := range []int{0, -1} {
		result := ragindex.PruneTree(root, depth)
		if len(result.Children) != 1 {
			t.Fatalf("depth=%d: expected 1 child, got %d", depth, len(result.Children))
		}
		if len(result.Children[0].Children) != 1 {
			t.Fatalf("depth=%d: expected 1 grandchild (unlimited), got %d", depth, len(result.Children[0].Children))
		}
	}
}

// TestPruneTree_NilRoot: nil 입력 → nil 반환.
func TestPruneTree_NilRoot(t *testing.T) {
	result := ragindex.PruneTree(nil, 2)
	if result != nil {
		t.Fatal("expected nil for nil root")
	}
}

// TestSearch_MatchesLabel: query가 label에 매칭되면 결과를 반환한다.
func TestSearch_MatchesLabel(t *testing.T) {
	root := &ragindex.TreeNode{
		ID:    "root",
		Label: "Root",
		Children: []*ragindex.TreeNode{
			{
				ID:      "community:auth",
				Label:   "MCP Server",
				Summary: "핸들러 레이어",
				Children: []*ragindex.TreeNode{
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

	results := ragindex.Search(root, "mcp", 10)
	if len(results) != 2 {
		t.Fatalf("expected 2 results (community + file), got %d", len(results))
	}
}

// TestSearch_CaseInsensitive: 검색은 대소문자를 구분하지 않는다.
func TestSearch_CaseInsensitive(t *testing.T) {
	root := &ragindex.TreeNode{
		ID:    "root",
		Label: "Root",
		Children: []*ragindex.TreeNode{
			{ID: "c1", Label: "Auth Service", Summary: "JWT 인증"},
		},
	}
	results := ragindex.Search(root, "AUTH", 10)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
}

// TestSearch_MaxResults: maxResults 파라미터가 결과 수를 제한한다.
func TestSearch_MaxResults(t *testing.T) {
	children := make([]*ragindex.TreeNode, 10)
	for i := range children {
		children[i] = &ragindex.TreeNode{ID: fmt.Sprintf("c%d", i), Label: "test node", Summary: "test"}
	}
	root := &ragindex.TreeNode{ID: "root", Children: children}

	results := ragindex.Search(root, "test", 3)
	if len(results) != 3 {
		t.Fatalf("expected 3 results (maxResults), got %d", len(results))
	}
}

// TestSearch_IncludesBreadcrumb: 결과에 Path(breadcrumb)가 포함된다.
func TestSearch_IncludesBreadcrumb(t *testing.T) {
	root := &ragindex.TreeNode{
		ID:    "root",
		Label: "Root",
		Children: []*ragindex.TreeNode{
			{
				ID:    "community:auth",
				Label: "Auth",
				Children: []*ragindex.TreeNode{
					{ID: "file:login.go", Label: "login.go", Summary: "로그인 처리"},
				},
			},
		},
	}
	results := ragindex.Search(root, "로그인", 10)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if len(results[0].Path) != 3 {
		t.Fatalf("expected path length 3 (root→auth→file), got %d: %v", len(results[0].Path), results[0].Path)
	}
}

func TestSearch_MatchesSearchText(t *testing.T) {
	root := &ragindex.TreeNode{
		ID:    "root",
		Label: "Root",
		Children: []*ragindex.TreeNode{
			{ID: "file:policy.go", Label: "policy.go", Summary: "access policy", SearchText: "domainRule 관리자 승인 필요"},
		},
	}
	results := ragindex.Search(root, "관리자", 10)
	if len(results) != 1 {
		t.Fatalf("expected search_text result, got %d", len(results))
	}
	if results[0].ID != "file:policy.go" {
		t.Fatalf("result ID = %q", results[0].ID)
	}
}

// TestSearch_NoMatch: 매칭 없으면 빈 슬라이스 반환.
func TestSearch_NoMatch(t *testing.T) {
	root := &ragindex.TreeNode{ID: "root", Label: "Root", Children: []*ragindex.TreeNode{
		{ID: "c1", Label: "Auth", Summary: "인증"},
	}}
	results := ragindex.Search(root, "zzznomatch", 10)
	if len(results) != 0 {
		t.Fatalf("expected 0 results, got %d", len(results))
	}
}

// TestFindNode: FindNode가 재귀적으로 트리에서 노드를 찾는지 검증한다.
func TestFindNode(t *testing.T) {
	root := &ragindex.TreeNode{
		ID:    "root",
		Label: "Root",
		Children: []*ragindex.TreeNode{
			{
				ID:    "child1",
				Label: "Child 1",
				Children: []*ragindex.TreeNode{
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
			got := ragindex.FindNode(root, tc.id)
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
