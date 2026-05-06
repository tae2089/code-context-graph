package wikiindex_test

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/tae2089/code-context-graph/internal/model"
	"github.com/tae2089/code-context-graph/internal/ragindex"
	"github.com/tae2089/code-context-graph/internal/store/gormstore"
	"github.com/tae2089/code-context-graph/internal/wikiindex"
)

func setupDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := gormstore.New(db).AutoMigrate(); err != nil {
		t.Fatalf("migrate db: %v", err)
	}
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			sqlDB.Close()
		}
	})
	return db
}

func TestBuilder_BuildsPackageFileSymbolTree(t *testing.T) {
	db := setupDB(t)
	tmpDir := t.TempDir()

	pkg := createNode(t, db, model.Node{QualifiedName: "github.com/example/project/internal/core", Kind: model.NodeKindPackage, Name: "core", FilePath: "internal/core", StartLine: 1, EndLine: 1, Language: "go"})
	file := createNode(t, db, model.Node{QualifiedName: "internal/core/runtime.go", Kind: model.NodeKindFile, Name: "internal/core/runtime.go", FilePath: "internal/core/runtime.go", StartLine: 1, EndLine: 40, Language: "go"})
	fn := createNode(t, db, model.Node{QualifiedName: "core.NewRuntime", Kind: model.NodeKindFunction, Name: "NewRuntime", FilePath: "internal/core/runtime.go", StartLine: 10, EndLine: 20, Language: "go"})
	createTag(t, db, pkg.ID, model.TagIndex, "Core runtime package")
	createTag(t, db, file.ID, model.TagIndex, "Runtime wiring")
	createAnnotation(t, db, fn.ID,
		model.DocTag{Kind: model.TagIntent, Value: "construct runtime", Ordinal: 0},
		model.DocTag{Kind: model.TagDomainRule, Value: "runtime dependencies are assembled once", Ordinal: 1},
		model.DocTag{Kind: model.TagSee, Value: "ccg://auth-svc/internal/auth/token.go#ValidateToken", Ordinal: 2},
	)

	builder := &wikiindex.Builder{DB: db, OutDir: filepath.Join(tmpDir, "docs"), IndexDir: filepath.Join(tmpDir, ".ccg")}
	packages, files, err := builder.Build(context.Background())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if packages != 1 || files != 1 {
		t.Fatalf("counts = packages:%d files:%d, want 1/1", packages, files)
	}

	idx, err := ragindex.LoadIndex(filepath.Join(tmpDir, ".ccg", "wiki-index.json"))
	if err != nil {
		t.Fatalf("LoadIndex: %v", err)
	}
	folder := ragindex.FindNode(idx.Root, "folder:internal")
	if folder == nil {
		t.Fatal("expected internal folder")
	}
	pkgNode := ragindex.FindNode(folder, "package:internal/core")
	if pkgNode == nil {
		t.Fatal("expected core package")
	}
	if pkgNode.DocPath != "" {
		t.Fatalf("package DocPath = %q, want empty", pkgNode.DocPath)
	}
	fileNode := ragindex.FindNode(pkgNode, "file:internal/core/runtime.go")
	if fileNode == nil {
		t.Fatal("expected runtime.go under core package")
	}
	if fileNode.DocPath == "" {
		t.Fatal("expected file doc_path")
	}
	symNode := ragindex.FindNode(fileNode, "symbol:core.NewRuntime")
	if symNode == nil {
		t.Fatal("expected NewRuntime symbol under runtime.go")
	}
	if symNode.Kind != string(model.NodeKindFunction) {
		t.Fatalf("symbol Kind = %q, want function", symNode.Kind)
	}
	if symNode.Details == nil {
		t.Fatal("expected symbol details")
	}
	if symNode.Details.QualifiedName != "core.NewRuntime" {
		t.Fatalf("symbol qualified name = %q", symNode.Details.QualifiedName)
	}
	if symNode.Details.StartLine != 10 || symNode.Details.EndLine != 20 {
		t.Fatalf("symbol lines = %d-%d, want 10-20", symNode.Details.StartLine, symNode.Details.EndLine)
	}
	if symNode.Details.Annotation == nil || len(symNode.Details.Annotation.Tags) != 3 {
		t.Fatalf("expected three annotation tags, got %#v", symNode.Details.Annotation)
	}
	if symNode.Details.Annotation.Tags[1].Kind != model.TagDomainRule {
		t.Fatalf("second tag kind = %q, want domainRule", symNode.Details.Annotation.Tags[1].Kind)
	}
	if symNode.Details.Annotation.Tags[2].Ref == nil || symNode.Details.Annotation.Tags[2].Ref.Namespace != "auth-svc" {
		t.Fatalf("expected parsed CCG ref on @see tag, got %#v", symNode.Details.Annotation.Tags[2])
	}
}

func TestBuilder_BuildTreeReturnsWikiTreeWithoutWritingIndex(t *testing.T) {
	db := setupDB(t)
	tmpDir := t.TempDir()

	pkg := createNode(t, db, model.Node{QualifiedName: "github.com/example/project/internal/core", Kind: model.NodeKindPackage, Name: "core", FilePath: "internal/core", StartLine: 1, EndLine: 1, Language: "go"})
	file := createNode(t, db, model.Node{QualifiedName: "internal/core/runtime.go", Kind: model.NodeKindFile, Name: "internal/core/runtime.go", FilePath: "internal/core/runtime.go", StartLine: 1, EndLine: 40, Language: "go"})
	fn := createNode(t, db, model.Node{QualifiedName: "core.NewRuntime", Kind: model.NodeKindFunction, Name: "NewRuntime", FilePath: "internal/core/runtime.go", StartLine: 10, EndLine: 20, Language: "go"})
	createTag(t, db, pkg.ID, model.TagIndex, "Core runtime package")
	createTag(t, db, file.ID, model.TagIndex, "Runtime wiring")
	createTag(t, db, fn.ID, model.TagIntent, "construct runtime")

	builder := &wikiindex.Builder{DB: db, OutDir: "docs", IndexDir: filepath.Join(tmpDir, ".ccg")}
	root, packages, files, err := builder.BuildTree(context.Background())
	if err != nil {
		t.Fatalf("BuildTree: %v", err)
	}
	if packages != 1 || files != 1 {
		t.Fatalf("counts = packages:%d files:%d, want 1/1", packages, files)
	}
	if _, err := os.Stat(filepath.Join(tmpDir, ".ccg", "wiki-index.json")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("BuildTree wrote wiki-index.json or stat failed: %v", err)
	}
	pkgNode := ragindex.FindNode(root, "package:internal/core")
	if pkgNode == nil || pkgNode.Summary != "Core runtime package" {
		t.Fatalf("package node = %#v", pkgNode)
	}
	fileNode := ragindex.FindNode(root, "file:internal/core/runtime.go")
	if fileNode == nil || fileNode.DocPath != filepath.Join("docs", "internal/core/runtime.go.md") {
		t.Fatalf("file node = %#v", fileNode)
	}
	symNode := ragindex.FindNode(root, "symbol:core.NewRuntime")
	if symNode == nil || symNode.Details == nil || symNode.Details.QualifiedName != "core.NewRuntime" {
		t.Fatalf("symbol node = %#v", symNode)
	}
}

func TestBuilder_BuildTreeScopesAnnotationsAndOrdersTags(t *testing.T) {
	db := setupDB(t)
	alphaNode := createNode(t, db, model.Node{Namespace: "alpha", QualifiedName: "alpha.Build", Kind: model.NodeKindFunction, Name: "Build", FilePath: "internal/alpha/build.go", StartLine: 1, EndLine: 10, Language: "go"})
	betaNode := createNode(t, db, model.Node{Namespace: "beta", QualifiedName: "beta.Build", Kind: model.NodeKindFunction, Name: "Build", FilePath: "internal/beta/build.go", StartLine: 1, EndLine: 10, Language: "go"})
	createAnnotation(t, db, alphaNode.ID,
		model.DocTag{Kind: model.TagSee, Value: "ccg://alpha/internal/alpha/build.go#Build", Ordinal: 2},
		model.DocTag{Kind: model.TagIntent, Value: "alpha build intent", Ordinal: 0},
		model.DocTag{Kind: model.TagDomainRule, Value: "alpha domain rule", Ordinal: 1},
	)
	createTag(t, db, betaNode.ID, model.TagIntent, "beta build intent")

	root, _, _, err := (&wikiindex.Builder{DB: db, OutDir: "docs", Namespace: "alpha"}).BuildTree(context.Background())
	if err != nil {
		t.Fatalf("BuildTree: %v", err)
	}
	if ragindex.FindNode(root, "symbol:beta.Build") != nil {
		t.Fatalf("beta namespace node leaked into alpha tree: %#v", root)
	}
	symNode := ragindex.FindNode(root, "symbol:alpha.Build")
	if symNode == nil || symNode.Details == nil || symNode.Details.Annotation == nil {
		t.Fatalf("missing alpha symbol annotation details: %#v", symNode)
	}
	gotKinds := []model.TagKind{}
	for _, tag := range symNode.Details.Annotation.Tags {
		gotKinds = append(gotKinds, tag.Kind)
	}
	wantKinds := []model.TagKind{model.TagIntent, model.TagDomainRule, model.TagSee}
	if len(gotKinds) != len(wantKinds) {
		t.Fatalf("tag kinds = %#v, want %#v", gotKinds, wantKinds)
	}
	for i := range wantKinds {
		if gotKinds[i] != wantKinds[i] {
			t.Fatalf("tag kinds = %#v, want %#v", gotKinds, wantKinds)
		}
	}
}

func TestBuilder_RespectsExclude(t *testing.T) {
	db := setupDB(t)
	tmpDir := t.TempDir()

	createNode(t, db, model.Node{QualifiedName: "internal/core/runtime.go", Kind: model.NodeKindFile, Name: "internal/core/runtime.go", FilePath: "internal/core/runtime.go", StartLine: 1, EndLine: 40, Language: "go"})
	createNode(t, db, model.Node{QualifiedName: "core.NewRuntime", Kind: model.NodeKindFunction, Name: "NewRuntime", FilePath: "internal/core/runtime.go", StartLine: 10, EndLine: 20, Language: "go"})

	builder := &wikiindex.Builder{DB: db, OutDir: filepath.Join(tmpDir, "docs"), IndexDir: filepath.Join(tmpDir, ".ccg"), Exclude: []string{"internal/core/.*"}}
	_, files, err := builder.Build(context.Background())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if files != 0 {
		t.Fatalf("files = %d, want 0", files)
	}
	idx, err := ragindex.LoadIndex(filepath.Join(tmpDir, ".ccg", "wiki-index.json"))
	if err != nil {
		t.Fatalf("LoadIndex: %v", err)
	}
	if len(idx.Root.Children) != 0 {
		t.Fatalf("expected empty tree, got %d children", len(idx.Root.Children))
	}
}

func createNode(t *testing.T, db *gorm.DB, node model.Node) model.Node {
	t.Helper()
	if err := db.Create(&node).Error; err != nil {
		t.Fatalf("create node: %v", err)
	}
	return node
}

func createTag(t *testing.T, db *gorm.DB, nodeID uint, kind model.TagKind, value string) {
	t.Helper()
	createAnnotation(t, db, nodeID, model.DocTag{Kind: kind, Value: value})
}

func createAnnotation(t *testing.T, db *gorm.DB, nodeID uint, tags ...model.DocTag) {
	t.Helper()
	ann := model.Annotation{NodeID: nodeID}
	if err := db.Create(&ann).Error; err != nil {
		t.Fatalf("create annotation: %v", err)
	}
	for i := range tags {
		tags[i].AnnotationID = ann.ID
		if err := db.Create(&tags[i]).Error; err != nil {
			t.Fatalf("create tag: %v", err)
		}
	}
}
