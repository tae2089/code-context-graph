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
