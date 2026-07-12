package wiki_test

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

	"github.com/tae2089/code-context-graph/internal/adapters/outbound/contentfiles"
	"github.com/tae2089/code-context-graph/internal/adapters/outbound/graphgorm"
	"github.com/tae2089/code-context-graph/internal/app/wiki"
	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

func setupDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := graphgorm.New(db).AutoMigrate(); err != nil {
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

func newWikiBuilder(db *gorm.DB, outDir, indexDir string) *wiki.Builder {
	return &wiki.Builder{Repository: graphgorm.New(db), IndexWriter: contentfiles.NewWikiIndexWriter(indexDir), OutDir: outDir}
}

func TestBuilder_BuildsPackageFileSymbolTree(t *testing.T) {
	db := setupDB(t)
	tmpDir := t.TempDir()

	pkg := createNode(t, db, graph.Node{QualifiedName: "github.com/example/project/internal/core", Kind: graph.NodeKindPackage, Name: "core", FilePath: "internal/core", StartLine: 1, EndLine: 1, Language: "go"})
	file := createNode(t, db, graph.Node{QualifiedName: "internal/core/runtime.go", Kind: graph.NodeKindFile, Name: "internal/core/runtime.go", FilePath: "internal/core/runtime.go", StartLine: 1, EndLine: 40, Language: "go"})
	fn := createNode(t, db, graph.Node{QualifiedName: "core.NewRuntime", Kind: graph.NodeKindFunction, Name: "NewRuntime", FilePath: "internal/core/runtime.go", StartLine: 10, EndLine: 20, Language: "go"})
	createTag(t, db, pkg.ID, graph.TagIndex, "Core runtime package")
	createTag(t, db, file.ID, graph.TagIndex, "Runtime wiring")
	createAnnotation(t, db, fn.ID,
		graph.DocTag{Kind: graph.TagIntent, Value: "construct runtime", Ordinal: 0},
		graph.DocTag{Kind: graph.TagDomainRule, Value: "runtime dependencies are assembled once", Ordinal: 1},
		graph.DocTag{Kind: graph.TagSee, Value: "ccg://auth-svc/internal/auth/token.go#ValidateToken", Ordinal: 2},
	)

	builder := newWikiBuilder(db, filepath.Join(tmpDir, "docs"), filepath.Join(tmpDir, ".ccg"))
	packages, files, err := builder.Build(context.Background())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if packages != 1 || files != 1 {
		t.Fatalf("counts = packages:%d files:%d, want 1/1", packages, files)
	}

	idx, err := contentfiles.LoadWikiIndex(filepath.Join(tmpDir, ".ccg", "wiki-index.json"))
	if err != nil {
		t.Fatalf("LoadIndex: %v", err)
	}
	folder := wiki.FindNode(idx.Root, "folder:internal")
	if folder == nil {
		t.Fatal("expected internal folder")
	}
	pkgNode := wiki.FindNode(folder, "package:internal/core")
	if pkgNode == nil {
		t.Fatal("expected core package")
	}
	if pkgNode.DocPath != "" {
		t.Fatalf("package DocPath = %q, want empty", pkgNode.DocPath)
	}
	fileNode := wiki.FindNode(pkgNode, "file:internal/core/runtime.go")
	if fileNode == nil {
		t.Fatal("expected runtime.go under core package")
	}
	if fileNode.DocPath == "" {
		t.Fatal("expected file doc_path")
	}
	symNode := wiki.FindNode(fileNode, "symbol:core.NewRuntime")
	if symNode == nil {
		t.Fatal("expected NewRuntime symbol under runtime.go")
	}
	if symNode.Kind != string(graph.NodeKindFunction) {
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
	if symNode.Details.Annotation.Tags[1].Kind != graph.TagDomainRule {
		t.Fatalf("second tag kind = %q, want domainRule", symNode.Details.Annotation.Tags[1].Kind)
	}
	if symNode.Details.Annotation.Tags[2].Ref == nil || symNode.Details.Annotation.Tags[2].Ref.Namespace != "auth-svc" {
		t.Fatalf("expected parsed CCG ref on @see tag, got %#v", symNode.Details.Annotation.Tags[2])
	}
}

func TestBuilder_BuildTreeReturnsWikiTreeWithoutWritingIndex(t *testing.T) {
	db := setupDB(t)
	tmpDir := t.TempDir()

	pkg := createNode(t, db, graph.Node{QualifiedName: "github.com/example/project/internal/core", Kind: graph.NodeKindPackage, Name: "core", FilePath: "internal/core", StartLine: 1, EndLine: 1, Language: "go"})
	file := createNode(t, db, graph.Node{QualifiedName: "internal/core/runtime.go", Kind: graph.NodeKindFile, Name: "internal/core/runtime.go", FilePath: "internal/core/runtime.go", StartLine: 1, EndLine: 40, Language: "go"})
	fn := createNode(t, db, graph.Node{QualifiedName: "core.NewRuntime", Kind: graph.NodeKindFunction, Name: "NewRuntime", FilePath: "internal/core/runtime.go", StartLine: 10, EndLine: 20, Language: "go"})
	createTag(t, db, pkg.ID, graph.TagIndex, "Core runtime package")
	createTag(t, db, file.ID, graph.TagIndex, "Runtime wiring")
	createTag(t, db, fn.ID, graph.TagIntent, "construct runtime")

	builder := newWikiBuilder(db, "docs", filepath.Join(tmpDir, ".ccg"))
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
	pkgNode := wiki.FindNode(root, "package:internal/core")
	if pkgNode == nil || pkgNode.Summary != "Core runtime package" {
		t.Fatalf("package node = %#v", pkgNode)
	}
	fileNode := wiki.FindNode(root, "file:internal/core/runtime.go")
	if fileNode == nil || fileNode.DocPath != filepath.Join("docs", "internal/core/runtime.go.md") {
		t.Fatalf("file node = %#v", fileNode)
	}
	symNode := wiki.FindNode(root, "symbol:core.NewRuntime")
	if symNode == nil || symNode.Details == nil || symNode.Details.QualifiedName != "core.NewRuntime" {
		t.Fatalf("symbol node = %#v", symNode)
	}
}

func TestBuilder_BuildTreeScopesAnnotationsAndOrdersTags(t *testing.T) {
	db := setupDB(t)
	alphaNode := createNode(t, db, graph.Node{Namespace: "alpha", QualifiedName: "alpha.Build", Kind: graph.NodeKindFunction, Name: "Build", FilePath: "internal/alpha/build.go", StartLine: 1, EndLine: 10, Language: "go"})
	betaNode := createNode(t, db, graph.Node{Namespace: "beta", QualifiedName: "beta.Build", Kind: graph.NodeKindFunction, Name: "Build", FilePath: "internal/beta/build.go", StartLine: 1, EndLine: 10, Language: "go"})
	createAnnotation(t, db, alphaNode.ID,
		graph.DocTag{Kind: graph.TagSee, Value: "ccg://alpha/internal/alpha/build.go#Build", Ordinal: 2},
		graph.DocTag{Kind: graph.TagIntent, Value: "alpha build intent", Ordinal: 0},
		graph.DocTag{Kind: graph.TagDomainRule, Value: "alpha domain rule", Ordinal: 1},
	)
	createTag(t, db, betaNode.ID, graph.TagIntent, "beta build intent")

	builder := newWikiBuilder(db, "docs", "")
	builder.Namespace = "alpha"
	root, _, _, err := builder.BuildTree(context.Background())
	if err != nil {
		t.Fatalf("BuildTree: %v", err)
	}
	if wiki.FindNode(root, "symbol:beta.Build") != nil {
		t.Fatalf("beta namespace node leaked into alpha tree: %#v", root)
	}
	symNode := wiki.FindNode(root, "symbol:alpha.Build")
	if symNode == nil || symNode.Details == nil || symNode.Details.Annotation == nil {
		t.Fatalf("missing alpha symbol annotation details: %#v", symNode)
	}
	gotKinds := []graph.TagKind{}
	for _, tag := range symNode.Details.Annotation.Tags {
		gotKinds = append(gotKinds, tag.Kind)
	}
	wantKinds := []graph.TagKind{graph.TagIntent, graph.TagDomainRule, graph.TagSee}
	if len(gotKinds) != len(wantKinds) {
		t.Fatalf("tag kinds = %#v, want %#v", gotKinds, wantKinds)
	}
	for i := range wantKinds {
		if gotKinds[i] != wantKinds[i] {
			t.Fatalf("tag kinds = %#v, want %#v", gotKinds, wantKinds)
		}
	}
}

func TestBuilder_BuildSubtreeReturnsBoundedLazyChildren(t *testing.T) {
	db := setupDB(t)
	pkg := createNode(t, db, graph.Node{QualifiedName: "github.com/example/project/internal/core", Kind: graph.NodeKindPackage, Name: "core", FilePath: "internal/core", StartLine: 1, EndLine: 1, Language: "go"})
	file := createNode(t, db, graph.Node{QualifiedName: "internal/core/runtime.go", Kind: graph.NodeKindFile, Name: "internal/core/runtime.go", FilePath: "internal/core/runtime.go", StartLine: 1, EndLine: 40, Language: "go"})
	fn := createNode(t, db, graph.Node{QualifiedName: "core.NewRuntime", Kind: graph.NodeKindFunction, Name: "NewRuntime", FilePath: "internal/core/runtime.go", StartLine: 10, EndLine: 20, Language: "go"})
	createTag(t, db, pkg.ID, graph.TagIndex, "Core runtime package")
	createTag(t, db, file.ID, graph.TagIndex, "Runtime wiring")
	createTag(t, db, fn.ID, graph.TagIntent, "construct runtime")

	builder := newWikiBuilder(db, "docs", "")
	root, err := builder.BuildSubtree(context.Background(), "", 1)
	if err != nil {
		t.Fatalf("BuildSubtree root: %v", err)
	}
	internal := wiki.FindNode(root, "folder:internal")
	if internal == nil || !internal.HasChildren || len(internal.Children) != 0 {
		t.Fatalf("root lazy internal = %#v", internal)
	}

	folder, err := builder.BuildSubtree(context.Background(), "folder:internal", 1)
	if err != nil {
		t.Fatalf("BuildSubtree folder: %v", err)
	}
	core := wiki.FindNode(folder, "package:internal/core")
	if core == nil || core.Summary != "Core runtime package" || !core.HasChildren || len(core.Children) != 0 {
		t.Fatalf("folder lazy package = %#v", core)
	}

	fileNode, err := builder.BuildSubtree(context.Background(), "file:internal/core/runtime.go", 1)
	if err != nil {
		t.Fatalf("BuildSubtree file: %v", err)
	}
	sym := wiki.FindNode(fileNode, "symbol:core.NewRuntime")
	if sym == nil || sym.Details == nil || sym.Details.Annotation == nil {
		t.Fatalf("file lazy symbol = %#v", sym)
	}
}

func TestBuilder_BuildSubtreeDoesNotDuplicateTopLevelFilesUnderRootPackage(t *testing.T) {
	db := setupDB(t)
	createNode(t, db, graph.Node{QualifiedName: "github.com/example/project", Kind: graph.NodeKindPackage, Name: "project", FilePath: ".", StartLine: 1, EndLine: 1, Language: "go"})
	createNode(t, db, graph.Node{QualifiedName: "context.go", Kind: graph.NodeKindFile, Name: "context.go", FilePath: "context.go", StartLine: 1, EndLine: 40, Language: "go"})
	createNode(t, db, graph.Node{QualifiedName: "trace.Context", Kind: graph.NodeKindFunction, Name: "Context", FilePath: "context.go", StartLine: 10, EndLine: 20, Language: "go"})

	builder := newWikiBuilder(db, "docs", "")
	root, err := builder.BuildSubtree(context.Background(), "", 1)
	if err != nil {
		t.Fatalf("BuildSubtree root: %v", err)
	}
	if pkg := wiki.FindNode(root, "package:."); pkg != nil {
		t.Fatalf("root package should be folded into root, got %#v", pkg)
	}
	fileNode := wiki.FindNode(root, "file:context.go")
	if fileNode == nil {
		t.Fatalf("expected top-level file under root: %#v", root.Children)
	}
	if !fileNode.HasChildren || len(fileNode.Children) != 0 {
		t.Fatalf("top-level file lazy state = %#v", fileNode)
	}
}

func TestBuilder_RespectsExclude(t *testing.T) {
	db := setupDB(t)
	tmpDir := t.TempDir()

	createNode(t, db, graph.Node{QualifiedName: "internal/core/runtime.go", Kind: graph.NodeKindFile, Name: "internal/core/runtime.go", FilePath: "internal/core/runtime.go", StartLine: 1, EndLine: 40, Language: "go"})
	createNode(t, db, graph.Node{QualifiedName: "core.NewRuntime", Kind: graph.NodeKindFunction, Name: "NewRuntime", FilePath: "internal/core/runtime.go", StartLine: 10, EndLine: 20, Language: "go"})

	builder := newWikiBuilder(db, filepath.Join(tmpDir, "docs"), filepath.Join(tmpDir, ".ccg"))
	builder.Exclude = []string{"internal/core/.*"}
	_, files, err := builder.Build(context.Background())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if files != 0 {
		t.Fatalf("files = %d, want 0", files)
	}
	idx, err := contentfiles.LoadWikiIndex(filepath.Join(tmpDir, ".ccg", "wiki-index.json"))
	if err != nil {
		t.Fatalf("LoadIndex: %v", err)
	}
	if len(idx.Root.Children) != 0 {
		t.Fatalf("expected empty tree, got %d children", len(idx.Root.Children))
	}
}

func createNode(t *testing.T, db *gorm.DB, node graph.Node) graph.Node {
	t.Helper()
	if err := db.Create(&node).Error; err != nil {
		t.Fatalf("create node: %v", err)
	}
	return node
}

func createTag(t *testing.T, db *gorm.DB, nodeID uint, kind graph.TagKind, value string) {
	t.Helper()
	createAnnotation(t, db, nodeID, graph.DocTag{Kind: kind, Value: value})
}

func createAnnotation(t *testing.T, db *gorm.DB, nodeID uint, tags ...graph.DocTag) {
	t.Helper()
	ann := graph.Annotation{NodeID: nodeID}
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
