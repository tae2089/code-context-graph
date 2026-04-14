package ragindex_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/imtaebin/code-context-graph/internal/model"
	"github.com/imtaebin/code-context-graph/internal/ragindex"
	"github.com/imtaebin/code-context-graph/internal/store/gormstore"
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

// TestBuilder_EmptyDB: 빈 DB에서 Build() 호출 시 0 communities, 0 files,
// doc-index.json이 생성되고 version=1, root != nil, root.Children 비어있음을 검증한다.
func TestBuilder_EmptyDB(t *testing.T) {
	db := setupDB(t)
	tmpDir := t.TempDir()
	indexDir := filepath.Join(tmpDir, ".ccg")

	b := &ragindex.Builder{
		DB:       db,
		OutDir:   filepath.Join(tmpDir, "docs"),
		IndexDir: indexDir,
	}

	communities, files, err := b.Build()
	if err != nil {
		t.Fatalf("Build() error: %v", err)
	}
	if communities != 0 {
		t.Errorf("communities = %d, want 0", communities)
	}
	if files != 0 {
		t.Errorf("files = %d, want 0", files)
	}

	indexPath := filepath.Join(indexDir, "doc-index.json")
	idx, err := ragindex.LoadIndex(indexPath)
	if err != nil {
		t.Fatalf("LoadIndex() error: %v", err)
	}
	if idx.Version != 1 {
		t.Errorf("version = %d, want 1", idx.Version)
	}
	if idx.Root == nil {
		t.Fatal("root is nil")
	}
	if len(idx.Root.Children) != 0 {
		t.Errorf("root.Children len = %d, want 0", len(idx.Root.Children))
	}
}

// TestBuilder_WithCommunities: 멤버 없는 커뮤니티 3개 생성 후 Build() →
// communities=3, root.Children 길이=3 검증.
func TestBuilder_WithCommunities(t *testing.T) {
	db := setupDB(t)
	tmpDir := t.TempDir()

	communities := []model.Community{
		{Key: "c1", Label: "Community One", Strategy: "auto"},
		{Key: "c2", Label: "Community Two", Strategy: "auto"},
		{Key: "c3", Label: "Community Three", Strategy: "auto"},
	}
	if err := db.Create(&communities).Error; err != nil {
		t.Fatalf("create communities: %v", err)
	}

	b := &ragindex.Builder{
		DB:       db,
		OutDir:   filepath.Join(tmpDir, "docs"),
		IndexDir: filepath.Join(tmpDir, ".ccg"),
	}

	count, files, err := b.Build()
	if err != nil {
		t.Fatalf("Build() error: %v", err)
	}
	if count != 3 {
		t.Errorf("communities = %d, want 3", count)
	}
	if files != 0 {
		t.Errorf("files = %d, want 0", files)
	}

	indexPath := filepath.Join(tmpDir, ".ccg", "doc-index.json")
	idx, err := ragindex.LoadIndex(indexPath)
	if err != nil {
		t.Fatalf("LoadIndex() error: %v", err)
	}
	if len(idx.Root.Children) != 3 {
		t.Errorf("root.Children len = %d, want 3", len(idx.Root.Children))
	}
}

// TestBuilder_FileSummary_IndexTag: node + annotation + @index 태그 + membership →
// file 노드 summary = index tag Value 검증.
func TestBuilder_FileSummary_IndexTag(t *testing.T) {
	db := setupDB(t)
	tmpDir := t.TempDir()

	community := model.Community{Key: "c1", Label: "Community One", Strategy: "auto"}
	if err := db.Create(&community).Error; err != nil {
		t.Fatalf("create community: %v", err)
	}

	node := model.Node{
		QualifiedName: "pkg.MyFunc",
		Kind:          model.NodeKindFunction,
		Name:          "MyFunc",
		FilePath:      "internal/pkg/myfunc.go",
		StartLine:     1,
		EndLine:       10,
		Language:      "go",
	}
	if err := db.Create(&node).Error; err != nil {
		t.Fatalf("create node: %v", err)
	}

	ann := model.Annotation{
		NodeID: node.ID,
		Tags: []model.DocTag{
			{Kind: model.TagIndex, Value: "MyFunc 파일의 인덱스 설명", Ordinal: 0},
		},
	}
	if err := db.Create(&ann).Error; err != nil {
		t.Fatalf("create annotation: %v", err)
	}

	membership := model.CommunityMembership{
		CommunityID: community.ID,
		NodeID:      node.ID,
	}
	if err := db.Create(&membership).Error; err != nil {
		t.Fatalf("create membership: %v", err)
	}

	b := &ragindex.Builder{
		DB:       db,
		OutDir:   filepath.Join(tmpDir, "docs"),
		IndexDir: filepath.Join(tmpDir, ".ccg"),
	}

	_, _, err := b.Build()
	if err != nil {
		t.Fatalf("Build() error: %v", err)
	}

	indexPath := filepath.Join(tmpDir, ".ccg", "doc-index.json")
	idx, err := ragindex.LoadIndex(indexPath)
	if err != nil {
		t.Fatalf("LoadIndex() error: %v", err)
	}

	if len(idx.Root.Children) == 0 {
		t.Fatal("root has no children")
	}
	communityNode := idx.Root.Children[0]
	if len(communityNode.Children) == 0 {
		t.Fatal("community node has no file children")
	}
	fileNode := communityNode.Children[0]
	want := "MyFunc 파일의 인덱스 설명"
	if fileNode.Summary != want {
		t.Errorf("file summary = %q, want %q", fileNode.Summary, want)
	}
}

// TestBuilder_FileSummary_Fallback: @index 태그 없고 @intent 태그만 있는 경우 →
// file 노드 summary = intent tag Value 검증.
func TestBuilder_FileSummary_Fallback(t *testing.T) {
	db := setupDB(t)
	tmpDir := t.TempDir()

	community := model.Community{Key: "c1", Label: "Community One", Strategy: "auto"}
	if err := db.Create(&community).Error; err != nil {
		t.Fatalf("create community: %v", err)
	}

	node := model.Node{
		QualifiedName: "pkg.MyFunc",
		Kind:          model.NodeKindFunction,
		Name:          "MyFunc",
		FilePath:      "internal/pkg/myfunc.go",
		StartLine:     1,
		EndLine:       10,
		Language:      "go",
	}
	if err := db.Create(&node).Error; err != nil {
		t.Fatalf("create node: %v", err)
	}

	ann := model.Annotation{
		NodeID: node.ID,
		Tags: []model.DocTag{
			{Kind: model.TagIntent, Value: "MyFunc의 의도 설명", Ordinal: 0},
		},
	}
	if err := db.Create(&ann).Error; err != nil {
		t.Fatalf("create annotation: %v", err)
	}

	membership := model.CommunityMembership{
		CommunityID: community.ID,
		NodeID:      node.ID,
	}
	if err := db.Create(&membership).Error; err != nil {
		t.Fatalf("create membership: %v", err)
	}

	b := &ragindex.Builder{
		DB:       db,
		OutDir:   filepath.Join(tmpDir, "docs"),
		IndexDir: filepath.Join(tmpDir, ".ccg"),
	}

	_, _, err := b.Build()
	if err != nil {
		t.Fatalf("Build() error: %v", err)
	}

	indexPath := filepath.Join(tmpDir, ".ccg", "doc-index.json")
	idx, err := ragindex.LoadIndex(indexPath)
	if err != nil {
		t.Fatalf("LoadIndex() error: %v", err)
	}

	if len(idx.Root.Children) == 0 {
		t.Fatal("root has no children")
	}
	communityNode := idx.Root.Children[0]
	if len(communityNode.Children) == 0 {
		t.Fatal("community node has no file children")
	}
	fileNode := communityNode.Children[0]
	want := "MyFunc의 의도 설명"
	if fileNode.Summary != want {
		t.Errorf("file summary = %q, want %q", fileNode.Summary, want)
	}
}

// TestBuilder_WritesJSON: Build() 후 doc-index.json에 version, built_at, root 필드 존재 검증.
func TestBuilder_WritesJSON(t *testing.T) {
	db := setupDB(t)
	tmpDir := t.TempDir()

	b := &ragindex.Builder{
		DB:       db,
		OutDir:   filepath.Join(tmpDir, "docs"),
		IndexDir: filepath.Join(tmpDir, ".ccg"),
	}

	_, _, err := b.Build()
	if err != nil {
		t.Fatalf("Build() error: %v", err)
	}

	indexPath := filepath.Join(tmpDir, ".ccg", "doc-index.json")
	raw, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}

	for _, field := range []string{"version", "built_at", "root"} {
		if _, ok := m[field]; !ok {
			t.Errorf("doc-index.json missing field %q", field)
		}
	}
}

// TestBuilder_ProjectDesc: ProjectDesc 설정 시 root.Summary에 반영됨을 검증한다.
func TestBuilder_ProjectDesc(t *testing.T) {
	db := setupDB(t)
	tmpDir := t.TempDir()

	b := &ragindex.Builder{
		DB:          db,
		OutDir:      filepath.Join(tmpDir, "docs"),
		IndexDir:    filepath.Join(tmpDir, ".ccg"),
		ProjectDesc: "테스트 프로젝트 설명",
	}

	_, _, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	idxPath := filepath.Join(tmpDir, ".ccg", "doc-index.json")
	idx, err := ragindex.LoadIndex(idxPath)
	if err != nil {
		t.Fatalf("LoadIndex: %v", err)
	}

	if idx.Root.Summary != "테스트 프로젝트 설명" {
		t.Errorf("root.Summary = %q, want %q", idx.Root.Summary, "테스트 프로젝트 설명")
	}
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

// TestBuilder_SymbolNodes: @intent 태그를 가진 노드가 file 하위에 symbol 노드로 나타남을 검증한다.
func TestBuilder_SymbolNodes(t *testing.T) {
	db := setupDB(t)
	tmpDir := t.TempDir()

	// community 생성
	comm := model.Community{Key: "auth", Label: "Auth Service", Description: "인증"}
	if err := db.Create(&comm).Error; err != nil {
		t.Fatalf("create community: %v", err)
	}

	// file 노드 생성 (community 멤버)
	fileNode := model.Node{
		QualifiedName: "internal/auth/handler.go",
		Kind:          model.NodeKindFile,
		Name:          "handler.go",
		FilePath:      "internal/auth/handler.go",
		StartLine:     1, EndLine: 100,
		Language: "go",
	}
	if err := db.Create(&fileNode).Error; err != nil {
		t.Fatalf("create file node: %v", err)
	}
	db.Create(&model.CommunityMembership{CommunityID: comm.ID, NodeID: fileNode.ID})

	// function 노드 (같은 파일, community 멤버, @intent 태그 있음)
	funcNode := model.Node{
		QualifiedName: "internal/auth/handler.go/HandleLogin",
		Kind:          model.NodeKindFunction,
		Name:          "HandleLogin",
		FilePath:      "internal/auth/handler.go",
		StartLine:     10, EndLine: 30,
		Language: "go",
	}
	if err := db.Create(&funcNode).Error; err != nil {
		t.Fatalf("create func node: %v", err)
	}
	db.Create(&model.CommunityMembership{CommunityID: comm.ID, NodeID: funcNode.ID})

	// @intent annotation + tag 생성
	ann := model.Annotation{NodeID: funcNode.ID, Summary: "로그인 핸들러"}
	db.Create(&ann)
	db.Create(&model.DocTag{AnnotationID: ann.ID, Kind: model.TagIntent, Value: "로그인 요청을 처리하고 JWT를 반환한다", Ordinal: 0})

	b := &ragindex.Builder{
		DB:       db,
		OutDir:   filepath.Join(tmpDir, "docs"),
		IndexDir: tmpDir,
	}
	_, _, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	idx, err := ragindex.LoadIndex(filepath.Join(tmpDir, "doc-index.json"))
	if err != nil {
		t.Fatalf("LoadIndex: %v", err)
	}

	// root → community → file → symbol 계층 확인
	if len(idx.Root.Children) == 0 {
		t.Fatal("expected community children")
	}
	commNode := idx.Root.Children[0]
	if len(commNode.Children) == 0 {
		t.Fatal("expected file children")
	}
	fileTreeNode := commNode.Children[0]
	if len(fileTreeNode.Children) == 0 {
		t.Fatal("expected symbol children under file node")
	}
	sym := fileTreeNode.Children[0]
	if sym.ID != "symbol:internal/auth/handler.go/HandleLogin" {
		t.Errorf("symbol ID = %q, want %q", sym.ID, "symbol:internal/auth/handler.go/HandleLogin")
	}
	if sym.Label != "HandleLogin" {
		t.Errorf("symbol Label = %q, want %q", sym.Label, "HandleLogin")
	}
	if sym.Summary != "로그인 요청을 처리하고 JWT를 반환한다" {
		t.Errorf("symbol Summary = %q", sym.Summary)
	}
	if sym.DocPath != "" {
		t.Errorf("symbol DocPath should be empty, got %q", sym.DocPath)
	}
}

// TestBuilder_NoSymbolsWithoutIntent: @intent 태그 없는 노드는 symbol 노드로 추가되지 않는다.
func TestBuilder_NoSymbolsWithoutIntent(t *testing.T) {
	db := setupDB(t)
	tmpDir := t.TempDir()

	comm := model.Community{Key: "core", Label: "Core"}
	db.Create(&comm)
	node := model.Node{QualifiedName: "core/utils.go/helper", Kind: model.NodeKindFunction, Name: "helper",
		FilePath: "core/utils.go", StartLine: 1, EndLine: 5, Language: "go"}
	db.Create(&node)
	db.Create(&model.CommunityMembership{CommunityID: comm.ID, NodeID: node.ID})
	// annotation 없음 → @intent 없음

	b := &ragindex.Builder{DB: db, OutDir: filepath.Join(tmpDir, "docs"), IndexDir: tmpDir}
	if _, _, err := b.Build(); err != nil {
		t.Fatalf("Build: %v", err)
	}

	idx, _ := ragindex.LoadIndex(filepath.Join(tmpDir, "doc-index.json"))
	if len(idx.Root.Children) == 0 || len(idx.Root.Children[0].Children) == 0 {
		t.Fatal("expected file node")
	}
	fileNode := idx.Root.Children[0].Children[0]
	if len(fileNode.Children) != 0 {
		t.Errorf("expected 0 symbol children, got %d", len(fileNode.Children))
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
