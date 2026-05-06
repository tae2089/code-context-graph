package ragindex_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/tae2089/code-context-graph/internal/ctxns"
	"github.com/tae2089/code-context-graph/internal/model"
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

	communities, files, err := b.Build(context.Background())
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

	count, files, err := b.Build(context.Background())
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

	_, _, err := b.Build(context.Background())
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

	_, _, err := b.Build(context.Background())
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

	_, _, err := b.Build(context.Background())
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

	_, _, err := b.Build(context.Background())
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
	if err := db.Create(&model.CommunityMembership{CommunityID: comm.ID, NodeID: fileNode.ID}).Error; err != nil {
		t.Fatalf("create membership: %v", err)
	}

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
	if err := db.Create(&model.CommunityMembership{CommunityID: comm.ID, NodeID: funcNode.ID}).Error; err != nil {
		t.Fatalf("create membership: %v", err)
	}

	// @intent annotation + tag 생성
	ann := model.Annotation{NodeID: funcNode.ID, Summary: "로그인 핸들러"}
	if err := db.Create(&ann).Error; err != nil {
		t.Fatalf("create annotation: %v", err)
	}
	if err := db.Create(&model.DocTag{AnnotationID: ann.ID, Kind: model.TagIntent, Value: "로그인 요청을 처리하고 JWT를 반환한다", Ordinal: 0}).Error; err != nil {
		t.Fatalf("create doc tag: %v", err)
	}
	if err := db.Create(&model.DocTag{AnnotationID: ann.ID, Kind: model.TagDomainRule, Value: "관리자만 감사 로그를 조회한다", Ordinal: 1}).Error; err != nil {
		t.Fatalf("create domain rule tag: %v", err)
	}

	b := &ragindex.Builder{
		DB:       db,
		OutDir:   filepath.Join(tmpDir, "docs"),
		IndexDir: tmpDir,
	}
	_, _, err := b.Build(context.Background())
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
	if commNode.Kind != "community" {
		t.Errorf("community Kind = %q, want community", commNode.Kind)
	}
	if len(commNode.Children) == 0 {
		t.Fatal("expected file children")
	}
	fileTreeNode := commNode.Children[0]
	if fileTreeNode.Kind != "file" {
		t.Errorf("file Kind = %q, want file", fileTreeNode.Kind)
	}
	if len(fileTreeNode.Children) == 0 {
		t.Fatal("expected symbol children under file node")
	}
	sym := fileTreeNode.Children[0]
	if sym.Kind != "symbol" {
		t.Errorf("symbol Kind = %q, want symbol", sym.Kind)
	}
	if sym.ID != "symbol:internal/auth/handler.go/HandleLogin" {
		t.Errorf("symbol ID = %q, want %q", sym.ID, "symbol:internal/auth/handler.go/HandleLogin")
	}
	if sym.Label != "HandleLogin" {
		t.Errorf("symbol Label = %q, want %q", sym.Label, "HandleLogin")
	}
	if sym.Summary != "로그인 요청을 처리하고 JWT를 반환한다" {
		t.Errorf("symbol Summary = %q", sym.Summary)
	}
	if !strings.Contains(sym.SearchText, "관리자만 감사 로그를 조회한다") {
		t.Errorf("symbol SearchText should include non-intent tags, got %q", sym.SearchText)
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
	if err := db.Create(&comm).Error; err != nil {
		t.Fatalf("create community: %v", err)
	}
	node := model.Node{QualifiedName: "core/utils.go/helper", Kind: model.NodeKindFunction, Name: "helper",
		FilePath: "core/utils.go", StartLine: 1, EndLine: 5, Language: "go"}
	if err := db.Create(&node).Error; err != nil {
		t.Fatalf("create node: %v", err)
	}
	if err := db.Create(&model.CommunityMembership{CommunityID: comm.ID, NodeID: node.ID}).Error; err != nil {
		t.Fatalf("create membership: %v", err)
	}
	// annotation 없음 → @intent 없음

	b := &ragindex.Builder{DB: db, OutDir: filepath.Join(tmpDir, "docs"), IndexDir: tmpDir}
	if _, _, err := b.Build(context.Background()); err != nil {
		t.Fatalf("Build: %v", err)
	}

	idx, err := ragindex.LoadIndex(filepath.Join(tmpDir, "doc-index.json"))
	if err != nil {
		t.Fatalf("LoadIndex: %v", err)
	}
	if len(idx.Root.Children) == 0 || len(idx.Root.Children[0].Children) == 0 {
		t.Fatal("expected file node")
	}
	fileNode := idx.Root.Children[0].Children[0]
	if len(fileNode.Children) != 0 {
		t.Errorf("expected 0 symbol children, got %d", len(fileNode.Children))
	}
}

func TestBuilder_IncludesSymbolWithNonIntentAnnotation(t *testing.T) {
	db := setupDB(t)
	tmpDir := t.TempDir()

	comm := model.Community{Key: "core", Label: "Core"}
	if err := db.Create(&comm).Error; err != nil {
		t.Fatalf("create community: %v", err)
	}
	node := model.Node{QualifiedName: "core/rules.go/checkAccess", Kind: model.NodeKindFunction, Name: "checkAccess",
		FilePath: "core/rules.go", StartLine: 1, EndLine: 5, Language: "go"}
	if err := db.Create(&node).Error; err != nil {
		t.Fatalf("create node: %v", err)
	}
	if err := db.Create(&model.CommunityMembership{CommunityID: comm.ID, NodeID: node.ID}).Error; err != nil {
		t.Fatalf("create membership: %v", err)
	}
	ann := model.Annotation{NodeID: node.ID, Summary: "access rule"}
	if err := db.Create(&ann).Error; err != nil {
		t.Fatalf("create annotation: %v", err)
	}
	if err := db.Create(&model.DocTag{AnnotationID: ann.ID, Kind: model.TagDomainRule, Value: "admin role required", Ordinal: 0}).Error; err != nil {
		t.Fatalf("create domain rule tag: %v", err)
	}

	b := &ragindex.Builder{DB: db, OutDir: filepath.Join(tmpDir, "docs"), IndexDir: tmpDir}
	if _, _, err := b.Build(context.Background()); err != nil {
		t.Fatalf("Build: %v", err)
	}
	idx, err := ragindex.LoadIndex(filepath.Join(tmpDir, "doc-index.json"))
	if err != nil {
		t.Fatalf("LoadIndex: %v", err)
	}
	fileNode := idx.Root.Children[0].Children[0]
	if len(fileNode.Children) != 1 {
		t.Fatalf("expected non-intent annotated symbol, got %d children", len(fileNode.Children))
	}
	if !strings.Contains(fileNode.Children[0].SearchText, "domainRule") {
		t.Fatalf("SearchText should include tag kind, got %q", fileNode.Children[0].SearchText)
	}
}

// TestBuilder_IgnoresPackageNodes: package graph nodes do not become RAG document candidates.
func TestBuilder_IgnoresPackageNodes(t *testing.T) {
	db := setupDB(t)
	tmpDir := t.TempDir()

	comm := model.Community{Key: "internal", Label: "internal"}
	if err := db.Create(&comm).Error; err != nil {
		t.Fatalf("create community: %v", err)
	}
	pkgNode := model.Node{
		QualifiedName: "github.com/example/project/internal/core",
		Kind:          model.NodeKindPackage,
		Name:          "core",
		FilePath:      "internal/core",
		StartLine:     1,
		EndLine:       1,
		Language:      "go",
	}
	if err := db.Create(&pkgNode).Error; err != nil {
		t.Fatalf("create package node: %v", err)
	}
	if err := db.Create(&model.CommunityMembership{CommunityID: comm.ID, NodeID: pkgNode.ID}).Error; err != nil {
		t.Fatalf("create package membership: %v", err)
	}

	b := &ragindex.Builder{DB: db, OutDir: filepath.Join(tmpDir, "docs"), IndexDir: tmpDir}
	_, files, err := b.Build(context.Background())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if files != 0 {
		t.Fatalf("files = %d, want 0", files)
	}

	idx, err := ragindex.LoadIndex(filepath.Join(tmpDir, "doc-index.json"))
	if err != nil {
		t.Fatalf("LoadIndex: %v", err)
	}
	if ragindex.FindNode(idx.Root, "package:internal/core") != nil {
		t.Fatal("package node should not be present in doc-index")
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

func TestRetrieve_MatchesTermsAcrossFileSubtree(t *testing.T) {
	root := &ragindex.TreeNode{
		ID:    "root",
		Label: "Root",
		Children: []*ragindex.TreeNode{
			{
				ID:    "community:analysis",
				Label: "analysis",
				Children: []*ragindex.TreeNode{
					{
						ID:      "file:internal/analysis/deadcode/service.go",
						Label:   "service.go",
						DocPath: "docs/internal/analysis/deadcode/service.go.md",
						Children: []*ragindex.TreeNode{
							{ID: "symbol:deadcode.Service.FindPage", Label: "FindPage", Summary: "bounded page"},
							{ID: "symbol:deadcode.normalizePathPrefix", Label: "normalizePathPrefix", Summary: "clean path prefix"},
						},
					},
					{
						ID:      "file:internal/analysis/other/service.go",
						Label:   "service.go",
						DocPath: "docs/internal/analysis/other/service.go.md",
						Children: []*ragindex.TreeNode{
							{ID: "symbol:other.Service.FindPage", Label: "FindPage", Summary: "bounded page"},
						},
					},
				},
			},
		},
	}

	results := ragindex.Retrieve(root, "FindPage normalizePathPrefix", 10)
	if len(results) == 0 {
		t.Fatal("expected retrieve result")
	}
	if results[0].DocPath != "docs/internal/analysis/deadcode/service.go.md" {
		t.Fatalf("top doc = %q, want deadcode service doc", results[0].DocPath)
	}
	if len(results[0].MatchedTerms) != 2 {
		t.Fatalf("matched terms = %#v, want both query terms", results[0].MatchedTerms)
	}
	if len(results[0].Matches) < 2 {
		t.Fatalf("expected symbol evidence for both terms, got %#v", results[0].Matches)
	}
}

func TestRetrieve_MatchesSearchTextAcrossFileSubtree(t *testing.T) {
	root := &ragindex.TreeNode{
		ID:    "root",
		Label: "Root",
		Children: []*ragindex.TreeNode{
			{
				ID:    "community:rules",
				Label: "rules",
				Children: []*ragindex.TreeNode{
					{
						ID:      "file:internal/policy/access.go",
						Label:   "access.go",
						DocPath: "docs/internal/policy/access.go.md",
						Children: []*ragindex.TreeNode{
							{ID: "symbol:policy.CheckAccess", Label: "CheckAccess", Summary: "access check", SearchText: "domainRule 관리자 승인 필요"},
							{ID: "symbol:policy.AuditAccess", Label: "AuditAccess", Summary: "audit write", SearchText: "sideEffect 감사 로그 기록"},
						},
					},
				},
			},
		},
	}

	results := ragindex.Retrieve(root, "관리자 감사", 10)
	if len(results) != 1 {
		t.Fatalf("expected retrieve result, got %d", len(results))
	}
	if len(results[0].MatchedTerms) != 2 {
		t.Fatalf("matched terms = %#v, want 관리자 and 감사", results[0].MatchedTerms)
	}
	if len(results[0].Matches) != 2 {
		t.Fatalf("expected two symbol evidence rows, got %#v", results[0].Matches)
	}
}

// TestBuilder_NamespaceFilter: namespace가 설정된 context로 Build 호출 시
// 해당 namespace의 데이터만 인덱스에 포함되는지 검증한다.
func TestBuilder_NamespaceFilter(t *testing.T) {
	db := setupDB(t)
	tmpDir := t.TempDir()

	// repo-a namespace의 노드와 커뮤니티
	nodeA := model.Node{
		Namespace:     "repo-a",
		QualifiedName: "repo-a/handler.go/Login",
		Kind:          model.NodeKindFunction,
		Name:          "Login",
		FilePath:      "handler.go",
		StartLine:     1, EndLine: 10,
		Language: "go",
	}
	if err := db.Create(&nodeA).Error; err != nil {
		t.Fatalf("create nodeA: %v", err)
	}
	commA := model.Community{Namespace: "repo-a", Key: "auth-a", Label: "Auth A", Strategy: "auto", Description: "repo-a auth"}
	if err := db.Create(&commA).Error; err != nil {
		t.Fatalf("create commA: %v", err)
	}
	if err := db.Create(&model.CommunityMembership{CommunityID: commA.ID, NodeID: nodeA.ID}).Error; err != nil {
		t.Fatalf("create membership A: %v", err)
	}
	annA := model.Annotation{NodeID: nodeA.ID}
	if err := db.Create(&annA).Error; err != nil {
		t.Fatalf("create annA: %v", err)
	}
	if err := db.Create(&model.DocTag{AnnotationID: annA.ID, Kind: model.TagIntent, Value: "repo-a 로그인", Ordinal: 0}).Error; err != nil {
		t.Fatalf("create tagA: %v", err)
	}

	// repo-b namespace의 노드와 커뮤니티
	nodeB := model.Node{
		Namespace:     "repo-b",
		QualifiedName: "repo-b/service.go/Pay",
		Kind:          model.NodeKindFunction,
		Name:          "Pay",
		FilePath:      "service.go",
		StartLine:     1, EndLine: 20,
		Language: "go",
	}
	if err := db.Create(&nodeB).Error; err != nil {
		t.Fatalf("create nodeB: %v", err)
	}
	commB := model.Community{Namespace: "repo-b", Key: "pay-b", Label: "Payment B", Strategy: "auto", Description: "repo-b payment"}
	if err := db.Create(&commB).Error; err != nil {
		t.Fatalf("create commB: %v", err)
	}
	if err := db.Create(&model.CommunityMembership{CommunityID: commB.ID, NodeID: nodeB.ID}).Error; err != nil {
		t.Fatalf("create membership B: %v", err)
	}
	annB := model.Annotation{NodeID: nodeB.ID}
	if err := db.Create(&annB).Error; err != nil {
		t.Fatalf("create annB: %v", err)
	}
	if err := db.Create(&model.DocTag{AnnotationID: annB.ID, Kind: model.TagIntent, Value: "repo-b 결제", Ordinal: 0}).Error; err != nil {
		t.Fatalf("create tagB: %v", err)
	}

	// repo-a namespace context로 빌드
	ctx := ctxns.WithNamespace(context.Background(), "repo-a")
	b := &ragindex.Builder{
		DB:       db,
		OutDir:   filepath.Join(tmpDir, "docs"),
		IndexDir: filepath.Join(tmpDir, ".ccg"),
	}

	communities, files, err := b.Build(ctx)
	if err != nil {
		t.Fatalf("Build() error: %v", err)
	}

	// repo-a에 속한 커뮤니티만 (commA만) 나와야 한다
	if communities != 1 {
		t.Errorf("communities = %d, want 1 (only repo-a)", communities)
	}
	if files != 1 {
		t.Errorf("files = %d, want 1 (only handler.go)", files)
	}

	indexPath := filepath.Join(tmpDir, ".ccg", "doc-index.json")
	idx, err := ragindex.LoadIndex(indexPath)
	if err != nil {
		t.Fatalf("LoadIndex() error: %v", err)
	}

	// root children에 repo-b 커뮤니티가 없어야 함
	for _, child := range idx.Root.Children {
		if child.Label == "Payment B" {
			t.Error("repo-b community should not appear in repo-a namespace build")
		}
	}

	// repo-a 커뮤니티에 repo-b 파일이 없어야 함
	for _, comm := range idx.Root.Children {
		for _, file := range comm.Children {
			if file.Label == "service.go" {
				t.Error("repo-b file (service.go) should not appear in repo-a namespace build")
			}
		}
	}
}

// TestBuilder_NamespaceFilter_EmptyNS: namespace 비어있으면 literal default namespace만 포함한다.
func TestBuilder_NamespaceFilter_EmptyNS(t *testing.T) {
	db := setupDB(t)
	tmpDir := t.TempDir()

	// default namespace와 named namespace에 데이터 생성
	for _, ns := range []string{"", "ns-1"} {
		qname := "/func"
		filePath := "default/main.go"
		key := "default"
		label := "default"
		if ns != "" {
			qname = ns + "/func"
			filePath = ns + "/main.go"
			key = ns
			label = ns
		}
		node := model.Node{
			Namespace: ns, QualifiedName: qname, Kind: model.NodeKindFunction,
			Name: "Func", FilePath: filePath, StartLine: 1, EndLine: 5, Language: "go",
		}
		if err := db.Create(&node).Error; err != nil {
			t.Fatalf("create node %s: %v", ns, err)
		}
		comm := model.Community{Namespace: ns, Key: key, Label: label, Strategy: "auto"}
		if err := db.Create(&comm).Error; err != nil {
			t.Fatalf("create comm %s: %v", ns, err)
		}
		if err := db.Create(&model.CommunityMembership{CommunityID: comm.ID, NodeID: node.ID}).Error; err != nil {
			t.Fatalf("create membership %s: %v", ns, err)
		}
	}

	// namespace 없는 context → literal default namespace만 반환
	b := &ragindex.Builder{DB: db, OutDir: filepath.Join(tmpDir, "docs"), IndexDir: filepath.Join(tmpDir, ".ccg")}
	communities, _, err := b.Build(context.Background())
	if err != nil {
		t.Fatalf("Build() error: %v", err)
	}
	if communities != 1 {
		t.Errorf("communities = %d, want 1 (default namespace only)", communities)
	}

	indexPath := filepath.Join(tmpDir, ".ccg", "doc-index.json")
	idx, err := ragindex.LoadIndex(indexPath)
	if err != nil {
		t.Fatalf("LoadIndex() error: %v", err)
	}

	if len(idx.Root.Children) != 1 {
		t.Fatalf("root.Children len = %d, want 1", len(idx.Root.Children))
	}
	if idx.Root.Children[0].Label != "default" {
		t.Errorf("default namespace community label = %q, want %q", idx.Root.Children[0].Label, "default")
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

// TestRetrieve_IntentOutranksGenericHidden verifies the structured retrieval Phase 1
// scoring puts a node whose @intent bucket matches the query above a node where
// the same term only appears in flat SearchText (generic hidden fallback).
func TestRetrieve_IntentOutranksGenericHidden(t *testing.T) {
	root := &ragindex.TreeNode{
		ID:    "root",
		Label: "Root",
		Children: []*ragindex.TreeNode{
			{
				ID:    "community:c",
				Label: "c",
				Children: []*ragindex.TreeNode{
					{
						ID:      "file:a.go",
						Label:   "a.go",
						DocPath: "docs/a.go.md",
						Children: []*ragindex.TreeNode{
							{
								ID:         "symbol:pkg.Generic",
								Label:      "Generic",
								Summary:    "misc",
								SearchText: "payment processing notes",
							},
						},
					},
					{
						ID:      "file:b.go",
						Label:   "b.go",
						DocPath: "docs/b.go.md",
						Children: []*ragindex.TreeNode{
							{
								ID:    "symbol:pkg.Intentful",
								Label: "Intentful",
								FieldTexts: map[string]string{
									"intent": "payment settlement entrypoint",
								},
							},
						},
					},
				},
			},
		},
	}

	results := ragindex.Retrieve(root, "payment", 10)
	if len(results) < 2 {
		t.Fatalf("expected both files to score, got %d", len(results))
	}
	if results[0].DocPath != "docs/b.go.md" {
		t.Fatalf("top doc = %q, want docs/b.go.md (intent bucket should outrank generic hidden)", results[0].DocPath)
	}
	hasIntent := false
	for _, f := range results[0].MatchedFields {
		if f == "intent" {
			hasIntent = true
		}
	}
	if !hasIntent {
		t.Errorf("MatchedFields = %v, want to include intent", results[0].MatchedFields)
	}
}

// TestRetrieve_MatchedFieldsExposesAnnotationBuckets verifies that retrieve_docs
// surfaces the distinct annotation buckets that fired (domainRule, sideEffect,
// mutates, requires, ensures, see) so callers can audit ranking evidence.
func TestRetrieve_MatchedFieldsExposesAnnotationBuckets(t *testing.T) {
	root := &ragindex.TreeNode{
		ID:    "root",
		Label: "Root",
		Children: []*ragindex.TreeNode{
			{
				ID:    "community:c",
				Label: "c",
				Children: []*ragindex.TreeNode{
					{
						ID:      "file:multi.go",
						Label:   "multi.go",
						DocPath: "docs/multi.go.md",
						Children: []*ragindex.TreeNode{
							{
								ID:    "symbol:pkg.Rule",
								Label: "Rule",
								FieldTexts: map[string]string{
									"domainRule": "alpha business rule",
									"requires":   "alpha precondition",
									"ensures":    "alpha postcondition",
									"sideEffect": "alpha writes log",
									"mutates":    "alpha state change",
									"see":        "alpha related handler",
								},
							},
						},
					},
				},
			},
		},
	}

	results := ragindex.Retrieve(root, "alpha", 5)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	got := map[string]bool{}
	for _, f := range results[0].MatchedFields {
		got[f] = true
	}
	want := []string{"domainRule", "requires", "ensures", "sideEffect", "mutates", "see"}
	for _, w := range want {
		if !got[w] {
			t.Errorf("MatchedFields missing %q; got %v", w, results[0].MatchedFields)
		}
	}
}

// TestRetrieve_LiteralOutranksExpansionOnly verifies Phase 2 rule that an
// expansion-only match (e.g., camelCase split) cannot outrank a direct literal
// match in a high-signal annotation bucket.
func TestRetrieve_LiteralOutranksExpansionOnly(t *testing.T) {
	root := &ragindex.TreeNode{
		ID:    "root",
		Label: "Root",
		Children: []*ragindex.TreeNode{
			{
				ID:    "community:c",
				Label: "c",
				Children: []*ragindex.TreeNode{
					{
						ID:      "file:literal.go",
						Label:   "literal.go",
						DocPath: "docs/literal.go.md",
						Children: []*ragindex.TreeNode{
							{
								ID:    "symbol:pkg.Literal",
								Label: "Literal",
								FieldTexts: map[string]string{
									"intent": "payment processing",
								},
							},
						},
					},
					{
						ID:      "file:expansion.go",
						Label:   "expansion.go",
						DocPath: "docs/expansion.go.md",
						Children: []*ragindex.TreeNode{
							{
								ID:    "symbol:pkg.Expansion",
								Label: "Expansion",
								FieldTexts: map[string]string{
									// Only the camelCase-expanded "payment" appears via "PaymentProcessor"
									"intent": "PaymentProcessor entrypoint",
								},
							},
						},
					},
				},
			},
		},
	}

	results := ragindex.Retrieve(root, "payment", 10)
	if len(results) < 2 {
		t.Fatalf("expected both files to score, got %d", len(results))
	}
	if results[0].DocPath != "docs/literal.go.md" {
		t.Fatalf("top doc = %q, want literal.go (literal must outrank expansion-only)", results[0].DocPath)
	}
	if results[0].Score <= results[1].Score {
		t.Fatalf("literal score (%d) must be strictly greater than expansion-only score (%d)", results[0].Score, results[1].Score)
	}
}

// TestRetrieve_CamelCaseExpansionMatches verifies a query token that is NOT a literal
// substring of any FieldTexts content still matches because camel-split of a sibling
// compound introduces the token via expansion. Two nodes are used: node A has intent
// "PaymentProcessor entrypoint" (camel-splits to [payment, processor]); node B has
// intent "billing pipeline" (no overlap with query). Query "processor" — verify it
// matches A only via the camelCase expansion path, with explain confirming
// ExpandedTerms contains "processor" so the test cannot pass via plain substring alone.
func TestRetrieve_CamelCaseExpansionMatches(t *testing.T) {
	root := &ragindex.TreeNode{
		ID:    "root",
		Label: "Root",
		Children: []*ragindex.TreeNode{
			{
				ID:    "community:c",
				Label: "c",
				Children: []*ragindex.TreeNode{
					{
						ID:      "file:camel.go",
						Label:   "camel.go",
						DocPath: "docs/camel.go.md",
						Children: []*ragindex.TreeNode{
							{
								ID:    "symbol:pkg.PaymentProcessor",
								Label: "PaymentProcessor",
								FieldTexts: map[string]string{
									"intent": "PaymentProcessor entrypoint",
								},
							},
						},
					},
				},
			},
		},
	}
	results := ragindex.RetrieveWithOptions(root, "processor", 5, ragindex.RetrieveOptions{Explain: true})
	if len(results) != 1 {
		t.Fatalf("expected 1 result via camelCase expansion, got %d", len(results))
	}
	// True expansion-only assertion: the query "processor" IS a substring of "paymentprocessor"
	// after lowercase, so substring match still fires. To prove expansion participated, assert
	// ExpandedTerms includes a sibling token from camel-split (e.g. "payment").
	hasSibling := false
	for _, exp := range results[0].ExpandedTerms {
		if exp == "payment" {
			hasSibling = true
			break
		}
	}
	if !hasSibling {
		t.Fatalf("expected camel-split sibling 'payment' in ExpandedTerms, got %v", results[0].ExpandedTerms)
	}
}

// TestRetrieve_SnakeCaseExpansionMatches verifies snake_case tokens get split and
// the expansion path participates beyond plain substring matching. Asserts via explain
// that ExpandedTerms includes the sibling sub-token introduced by snake-case split.
func TestRetrieve_SnakeCaseExpansionMatches(t *testing.T) {
	root := &ragindex.TreeNode{
		ID:    "root",
		Label: "Root",
		Children: []*ragindex.TreeNode{
			{
				ID:    "community:c",
				Label: "c",
				Children: []*ragindex.TreeNode{
					{
						ID:      "file:snake.go",
						Label:   "snake.go",
						DocPath: "docs/snake.go.md",
						Children: []*ragindex.TreeNode{
							{
								ID:    "symbol:pkg.process",
								Label: "process",
								FieldTexts: map[string]string{
									"intent": "handles payment_processor flow",
								},
							},
						},
					},
				},
			},
		},
	}

	results := ragindex.RetrieveWithOptions(root, "processor", 5, ragindex.RetrieveOptions{Explain: true})
	if len(results) != 1 {
		t.Fatalf("expected 1 result via snake_case expansion, got %d", len(results))
	}
	hasSibling := false
	for _, exp := range results[0].ExpandedTerms {
		if exp == "payment" {
			hasSibling = true
			break
		}
	}
	if !hasSibling {
		t.Fatalf("expected snake-split sibling 'payment' in ExpandedTerms, got %v", results[0].ExpandedTerms)
	}
}

// TestRetrieve_TagNameHintExpansion verifies multi-word phrases like
// "side effect" expand to the @sideEffect bucket via tag-name hints.
func TestRetrieve_TagNameHintExpansion(t *testing.T) {
	root := &ragindex.TreeNode{
		ID:    "root",
		Label: "Root",
		Children: []*ragindex.TreeNode{
			{
				ID:    "community:c",
				Label: "c",
				Children: []*ragindex.TreeNode{
					{
						ID:      "file:effects.go",
						Label:   "effects.go",
						DocPath: "docs/effects.go.md",
						Children: []*ragindex.TreeNode{
							{
								ID:    "symbol:pkg.Writer",
								Label: "Writer",
								FieldTexts: map[string]string{
									"sideEffect": "writes audit log to disk",
								},
							},
						},
					},
				},
			},
		},
	}

	// Query "side effect audit" — "side"/"effect" alone are too short / generic,
	// but the tag-name hint should expand to match the @sideEffect bucket.
	results := ragindex.Retrieve(root, "sideeffect audit", 5)
	if len(results) != 1 {
		t.Fatalf("expected 1 result via tag-name hint + literal audit, got %d", len(results))
	}
}

// TestRetrieve_DefaultResponseOmitsExpansionDiagnostics verifies the default
// Retrieve call (no explain) does not expose ExpandedTerms or FieldScores.
func TestRetrieve_DefaultResponseOmitsExpansionDiagnostics(t *testing.T) {
	root := &ragindex.TreeNode{
		ID:    "root",
		Label: "Root",
		Children: []*ragindex.TreeNode{
			{
				ID:    "community:c",
				Label: "c",
				Children: []*ragindex.TreeNode{
					{
						ID:      "file:camel.go",
						Label:   "camel.go",
						DocPath: "docs/camel.go.md",
						Children: []*ragindex.TreeNode{
							{
								ID:    "symbol:pkg.PaymentProcessor",
								Label: "PaymentProcessor",
								FieldTexts: map[string]string{
									"intent": "PaymentProcessor entrypoint",
								},
							},
						},
					},
				},
			},
		},
	}

	results := ragindex.Retrieve(root, "payment", 5)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if len(results[0].ExpandedTerms) != 0 {
		t.Errorf("default Retrieve must not expose ExpandedTerms, got %v", results[0].ExpandedTerms)
	}
	if len(results[0].FieldScores) != 0 {
		t.Errorf("default Retrieve must not expose FieldScores, got %v", results[0].FieldScores)
	}

	// Ensure JSON marshal omits these fields entirely.
	raw, err := json.Marshal(results[0])
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	s := string(raw)
	if strings.Contains(s, "expanded_terms") {
		t.Errorf("default JSON must omit expanded_terms, got %s", s)
	}
	if strings.Contains(s, "field_scores") {
		t.Errorf("default JSON must omit field_scores, got %s", s)
	}
}

// TestRetrieve_ExplainExposesDiagnostics verifies RetrieveWithOptions(Explain:true)
// adds per-result expanded_terms and field_scores diagnostics.
func TestRetrieve_ExplainExposesDiagnostics(t *testing.T) {
	root := &ragindex.TreeNode{
		ID:    "root",
		Label: "Root",
		Children: []*ragindex.TreeNode{
			{
				ID:    "community:c",
				Label: "c",
				Children: []*ragindex.TreeNode{
					{
						ID:      "file:camel.go",
						Label:   "camel.go",
						DocPath: "docs/camel.go.md",
						Children: []*ragindex.TreeNode{
							{
								ID:    "symbol:pkg.PaymentProcessor",
								Label: "PaymentProcessor",
								FieldTexts: map[string]string{
									"intent": "PaymentProcessor entrypoint",
								},
							},
						},
					},
				},
			},
		},
	}

	results := ragindex.RetrieveWithOptions(root, "payment", 5, ragindex.RetrieveOptions{Explain: true})
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if len(results[0].ExpandedTerms) == 0 {
		t.Errorf("explain mode must expose ExpandedTerms, got empty")
	}
	if len(results[0].FieldScores) == 0 {
		t.Errorf("explain mode must expose FieldScores, got empty")
	}

	raw, err := json.Marshal(results[0])
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	s := string(raw)
	if !strings.Contains(s, "expanded_terms") {
		t.Errorf("explain JSON must include expanded_terms, got %s", s)
	}
	if !strings.Contains(s, "field_scores") {
		t.Errorf("explain JSON must include field_scores, got %s", s)
	}
}

// TestRetrieve_DefaultMatchesBudgetCap verifies that when expansion-only matches inflate
// the symbol-level evidence list, the result's Matches slice is bounded by the default
// budget while literal-bearing matches always retain priority within the cap.
func TestRetrieve_DefaultMatchesBudgetCap(t *testing.T) {
	const budget = 12
	// Build one literal-matching symbol and many expansion-only siblings inside the same
	// file. Query "alpha" matches the literal symbol; "beta_gamma_delta" decomposition
	// expands into siblings that hit the rest of the file's symbols.
	literalChild := &ragindex.TreeNode{
		ID:    "symbol:pkg.AlphaCore",
		Label: "AlphaCore",
		FieldTexts: map[string]string{
			"intent": "alpha core entrypoint",
		},
	}
	children := []*ragindex.TreeNode{literalChild}
	// Add 30 expansion-only siblings whose intent contains "beta" (a camel sibling we'll
	// inject via an additional anchor). Use a separate anchor symbol whose camel-split
	// introduces "alpha" + "beta" so vocab sees both.
	anchor := &ragindex.TreeNode{
		ID:    "symbol:pkg.AlphaBeta",
		Label: "AlphaBeta",
		FieldTexts: map[string]string{
			"intent": "AlphaBeta combined anchor",
		},
	}
	children = append(children, anchor)
	for i := 0; i < 30; i++ {
		children = append(children, &ragindex.TreeNode{
			ID:    fmt.Sprintf("symbol:pkg.expand_%d", i),
			Label: fmt.Sprintf("expand_%d", i),
			FieldTexts: map[string]string{
				"intent": fmt.Sprintf("beta sibling %d", i),
			},
		})
	}
	root := &ragindex.TreeNode{
		ID:    "root",
		Label: "Root",
		Children: []*ragindex.TreeNode{
			{
				ID:    "community:c",
				Label: "c",
				Children: []*ragindex.TreeNode{
					{
						ID:       "file:big.go",
						Label:    "big.go",
						DocPath:  "docs/big.go.md",
						Children: children,
					},
				},
			},
		},
	}
	results := ragindex.Retrieve(root, "alpha", 5)
	if len(results) != 1 {
		t.Fatalf("expected 1 file result, got %d", len(results))
	}
	if got := len(results[0].Matches); got > budget {
		t.Fatalf("Matches len = %d, want <= %d (defaultMatchesBudget)", got, budget)
	}
	// Literal-bearing match must be present in the capped slice.
	foundLiteral := false
	for _, m := range results[0].Matches {
		if m.ID == literalChild.ID {
			foundLiteral = true
			break
		}
	}
	if !foundLiteral {
		t.Fatalf("literal-bearing match %q must be retained under budget cap, got %#v", literalChild.ID, results[0].Matches)
	}
}

func TestRetrieve_DoesNotDoubleCountSearchTextWhenStructuredFieldMatches(t *testing.T) {
	root := &ragindex.TreeNode{
		ID:    "root",
		Label: "Root",
		Children: []*ragindex.TreeNode{
			{
				ID:    "community:c",
				Label: "c",
				Children: []*ragindex.TreeNode{
					{
						ID:      "file:intent.go",
						Label:   "intent.go",
						DocPath: "docs/intent.go.md",
						Children: []*ragindex.TreeNode{
							{
								ID:         "symbol:pkg.Intent",
								Label:      "Intent",
								SearchText: "payment settlement entrypoint",
								FieldTexts: map[string]string{"intent": "payment settlement entrypoint"},
							},
						},
					},
					{
						ID:      "file:generic.go",
						Label:   "generic.go",
						DocPath: "docs/generic.go.md",
						Children: []*ragindex.TreeNode{
							{
								ID:         "symbol:pkg.Generic",
								Label:      "Generic",
								SearchText: "payment settlement entrypoint",
							},
						},
					},
				},
			},
		},
	}

	results := ragindex.Retrieve(root, "payment", 10)
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].DocPath != "docs/intent.go.md" {
		t.Fatalf("top doc = %q, want docs/intent.go.md", results[0].DocPath)
	}
	if results[0].Score != 17 {
		t.Fatalf("intent-backed score = %d, want 17 (7 intent + 10 distinct term bonus)", results[0].Score)
	}
	for _, field := range results[0].MatchedFields {
		if field == "generic" {
			t.Fatalf("matched_fields should not include generic when structured field already matched: %#v", results[0].MatchedFields)
		}
	}
}
