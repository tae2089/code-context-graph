package docs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/tae2089/code-context-graph/internal/adapters/outbound/contentfiles"
	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	if err := migrateDocsTestDB(db); err != nil {
		t.Fatal(err)
	}
	return db
}

func newGenerator(t *testing.T, db *gorm.DB) (*Generator, string) {
	t.Helper()
	outDir := t.TempDir()
	return &Generator{Repository: testRepository{db: db}, Files: contentfiles.NewRoot(outDir), OutDir: outDir}, outDir
}

func TestRun_EmptyDB(t *testing.T) {
	db := newTestDB(t)
	gen, _ := newGenerator(t, db)
	if err := gen.Run(); err != nil {
		t.Fatalf("unexpected error on empty DB: %v", err)
	}
}

func TestLoadNodes_ReturnsNodesWithAnnotations(t *testing.T) {
	db := newTestDB(t)

	fnNode := graph.Node{
		QualifiedName: "pkg/foo.go::Bar",
		Kind:          graph.NodeKindFunction,
		Name:          "Bar",
		FilePath:      "pkg/foo.go",
		StartLine:     10,
		EndLine:       20,
		Hash:          "h1",
		Language:      "go",
	}
	db.Create(&fnNode)

	ann := graph.Annotation{
		NodeID: fnNode.ID,
		Tags: []graph.DocTag{
			{Kind: graph.TagIntent, Value: "processes bar", Ordinal: 0},
		},
	}
	db.Create(&ann)

	gen, _ := newGenerator(t, db)
	nodes, annByID, err := gen.loadNodes()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(nodes))
	}
	a, ok := annByID[fnNode.ID]
	if !ok {
		t.Fatal("expected annotation for node")
	}
	if len(a.Tags) != 1 || a.Tags[0].Value != "processes bar" {
		t.Fatalf("unexpected tags: %+v", a.Tags)
	}
}

func TestLoadEdges_ReturnsCallsAndImports(t *testing.T) {
	db := newTestDB(t)

	from := graph.Node{QualifiedName: "a.go::A", Kind: graph.NodeKindFunction, Name: "A", FilePath: "a.go", StartLine: 1, EndLine: 5, Hash: "h1", Language: "go"}
	to := graph.Node{QualifiedName: "b.go::B", Kind: graph.NodeKindFunction, Name: "B", FilePath: "b.go", StartLine: 1, EndLine: 5, Hash: "h2", Language: "go"}
	db.Create(&from)
	db.Create(&to)

	edge := graph.Edge{
		FromNodeID:  from.ID,
		ToNodeID:    to.ID,
		Kind:        graph.EdgeKindCalls,
		FilePath:    "a.go",
		Line:        3,
		Fingerprint: "fp1",
	}
	db.Create(&edge)

	gen, _ := newGenerator(t, db)
	edgesByFromID, err := gen.loadEdges([]uint{from.ID, to.ID})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	edges, ok := edgesByFromID[from.ID]
	if !ok || len(edges) != 1 {
		t.Fatalf("expected 1 edge from A, got %v", edgesByFromID)
	}
	if edges[0].ToNode.Name != "B" {
		t.Fatalf("expected ToNode.Name=B, got %s", edges[0].ToNode.Name)
	}
}

func TestRun_GeneratesFileDoc(t *testing.T) {
	db := newTestDB(t)

	fileNode := graph.Node{
		QualifiedName: "internal/foo.go",
		Kind:          graph.NodeKindFile,
		Name:          "foo.go",
		FilePath:      "internal/foo.go",
		StartLine:     1, EndLine: 50,
		Hash: "hf", Language: "go",
	}
	db.Create(&fileNode)

	fileAnn := graph.Annotation{
		NodeID: fileNode.ID,
		Tags:   []graph.DocTag{{Kind: graph.TagIndex, Value: "foo 패키지 유틸리티", Ordinal: 0}},
	}
	db.Create(&fileAnn)

	fnNode := graph.Node{
		QualifiedName: "internal/foo.go::Bar",
		Kind:          graph.NodeKindFunction,
		Name:          "Bar",
		FilePath:      "internal/foo.go",
		StartLine:     10, EndLine: 20,
		Hash: "h1", Language: "go",
	}
	db.Create(&fnNode)

	ann := graph.Annotation{
		NodeID:  fnNode.ID,
		Summary: "Bar 함수 요약",
		Context: "레거시 시스템과의 호환성을 위해 존재",
		Tags: []graph.DocTag{
			{Kind: graph.TagIntent, Value: "bar 요청을 처리한다", Ordinal: 0},
			{Kind: graph.TagDomainRule, Value: "입력값은 양수여야 한다", Ordinal: 1},
			{Kind: graph.TagSideEffect, Value: "DB에 로그를 기록한다", Ordinal: 2},
			{Kind: graph.TagMutates, Value: "users 테이블", Ordinal: 3},
			{Kind: graph.TagRequires, Value: "n > 0", Ordinal: 4},
			{Kind: graph.TagEnsures, Value: "반환값은 비어 있지 않다", Ordinal: 5},
			{Kind: graph.TagParam, Name: "n", Value: "처리할 정수", Ordinal: 6},
			{Kind: graph.TagReturn, Value: "처리 결과 문자열", Ordinal: 7},
			{Kind: graph.TagSee, Value: "pkg/service.go::Handle", Ordinal: 8},
		},
	}
	db.Create(&ann)

	gen, outDir := newGenerator(t, db)
	if err := gen.Run(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	docPath := filepath.Join(outDir, "internal", "foo.go.md")
	content, err := os.ReadFile(docPath)
	if err != nil {
		t.Fatalf("expected file doc at %s: %v", docPath, err)
	}

	got := string(content)
	for _, want := range []string{
		"# internal/foo.go",
		"> foo 패키지 유틸리티",
		"## Functions",
		"### Bar",
		"**Lines:** 10–20",
		"Bar 함수 요약",
		"> 레거시 시스템과의 호환성을 위해 존재",
		"**Intent:** bar 요청을 처리한다",
		"**Domain Rules:**",
		"입력값은 양수여야 한다",
		"**Side Effects:** DB에 로그를 기록한다",
		"**Mutates:** users 테이블",
		"**Requires:**",
		"n > 0",
		"**Ensures:**",
		"반환값은 비어 있지 않다",
		"`n` — 처리할 정수",
		"**Returns:** 처리 결과 문자열",
		"**See:** pkg/service.go::Handle",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("expected %q in file doc, got:\n%s", want, got)
		}
	}

	// 빈 섹션(Classes, Types, Tests)이 출력되지 않아야 함
	for _, absent := range []string{"## Classes", "## Types", "## Tests"} {
		if strings.Contains(got, absent) {
			t.Errorf("expected %q to be absent in file doc, got:\n%s", absent, got)
		}
	}
}

func TestRun_RejectsFilePathOutsideOutDir(t *testing.T) {
	db := newTestDB(t)
	badNode := graph.Node{
		QualifiedName: "evil.Escape",
		Kind:          graph.NodeKindFunction,
		Name:          "Escape",
		FilePath:      "../escape.go",
		StartLine:     1,
		EndLine:       5,
		Hash:          "h1",
		Language:      "go",
	}
	if err := db.Create(&badNode).Error; err != nil {
		t.Fatalf("seed node: %v", err)
	}

	gen, outDir := newGenerator(t, db)
	err := gen.Run()
	if err == nil {
		t.Fatal("expected Run to reject a file path outside OutDir")
	}
	if _, statErr := os.Stat(filepath.Join(filepath.Dir(outDir), "escape.go.md")); !os.IsNotExist(statErr) {
		t.Fatalf("expected no file outside OutDir, stat err=%v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(outDir, ".ccg-docs-manifest.json")); !os.IsNotExist(statErr) {
		t.Fatalf("expected no manifest after invalid path, stat err=%v", statErr)
	}
}

func TestRun_RejectsAbsoluteFilePath(t *testing.T) {
	db := newTestDB(t)
	badNode := graph.Node{
		QualifiedName: "evil.Abs",
		Kind:          graph.NodeKindFunction,
		Name:          "Abs",
		FilePath:      filepath.Join(t.TempDir(), "abs.go"),
		StartLine:     1,
		EndLine:       5,
		Hash:          "h1",
		Language:      "go",
	}
	if err := db.Create(&badNode).Error; err != nil {
		t.Fatalf("seed node: %v", err)
	}

	gen, _ := newGenerator(t, db)
	if err := gen.Run(); err == nil {
		t.Fatal("expected Run to reject an absolute file path")
	}
}

func TestRun_RejectsSymlinkDirectoryEscape(t *testing.T) {
	db := newTestDB(t)
	node := graph.Node{
		QualifiedName: "pkg.Escape",
		Kind:          graph.NodeKindFunction,
		Name:          "Escape",
		FilePath:      "pkg/escape.go",
		StartLine:     1,
		EndLine:       5,
		Hash:          "h1",
		Language:      "go",
	}
	if err := db.Create(&node).Error; err != nil {
		t.Fatalf("seed node: %v", err)
	}

	gen, outDir := newGenerator(t, db)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(outDir, "pkg")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	if err := gen.Run(); err == nil {
		t.Fatal("expected Run to reject an output path through symlink")
	}
	if _, statErr := os.Stat(filepath.Join(outside, "escape.go.md")); !os.IsNotExist(statErr) {
		t.Fatalf("expected no file through symlink, stat err=%v", statErr)
	}
}

func TestRun_GeneratesIndexMd(t *testing.T) {
	db := newTestDB(t)

	fnNode := graph.Node{
		QualifiedName: "pkg/service.go::Handle",
		Kind:          graph.NodeKindFunction,
		Name:          "Handle",
		FilePath:      "pkg/service.go",
		StartLine:     5, EndLine: 15,
		Hash: "h3", Language: "go",
	}
	db.Create(&fnNode)

	typeNode := graph.Node{
		QualifiedName: "pkg/service.go::Request",
		Kind:          graph.NodeKindType,
		Name:          "Request",
		FilePath:      "pkg/service.go",
		StartLine:     1, EndLine: 4,
		Hash: "h4", Language: "go",
	}
	db.Create(&typeNode)

	gen, outDir := newGenerator(t, db)
	if err := gen.Run(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(outDir, "index.md"))
	if err != nil {
		t.Fatalf("expected index.md: %v", err)
	}

	got := string(content)
	for _, want := range []string{
		"# Code Context Index",
		"pkg/service.go",
		"| 2 |",
		// All Symbols 앵커 링크 형식 확인
		"[Handle](pkg/service.go.md#handle)",
		"[Request](pkg/service.go.md#request)",
		"function",
		"type",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("expected %q in index.md, got:\n%s", want, got)
		}
	}
}

func TestGroupByFile_SortsByFilePath(t *testing.T) {
	nodes := []graph.Node{
		{ID: 1, Kind: graph.NodeKindFunction, Name: "Z", FilePath: "z/z.go", QualifiedName: "z/z.go::Z", StartLine: 1, EndLine: 2, Hash: "h1", Language: "go"},
		{ID: 2, Kind: graph.NodeKindFunction, Name: "A", FilePath: "a/a.go", QualifiedName: "a/a.go::A", StartLine: 1, EndLine: 2, Hash: "h2", Language: "go"},
		{ID: 3, Kind: graph.NodeKindFunction, Name: "M", FilePath: "m/m.go", QualifiedName: "m/m.go::M", StartLine: 1, EndLine: 2, Hash: "h3", Language: "go"},
	}

	groups := groupByFile(nodes, nil, nil)

	if len(groups) != 3 {
		t.Fatalf("expected 3 groups, got %d", len(groups))
	}
	if groups[0].FilePath != "a/a.go" || groups[1].FilePath != "m/m.go" || groups[2].FilePath != "z/z.go" {
		t.Errorf("expected alphabetical order, got %v, %v, %v", groups[0].FilePath, groups[1].FilePath, groups[2].FilePath)
	}
}

func TestRun_ExcludePatterns(t *testing.T) {
	db := newTestDB(t)

	nodes := []graph.Node{
		{QualifiedName: "internal/foo.go::Foo", Kind: graph.NodeKindFunction, Name: "Foo", FilePath: "internal/foo.go", StartLine: 1, EndLine: 5, Hash: "h1", Language: "go"},
		{QualifiedName: "vendor/lib.go::Lib", Kind: graph.NodeKindFunction, Name: "Lib", FilePath: "vendor/lib.go", StartLine: 1, EndLine: 5, Hash: "h2", Language: "go"},
		{QualifiedName: "internal/foo_test.go::TestFoo", Kind: graph.NodeKindTest, Name: "TestFoo", FilePath: "internal/foo_test.go", StartLine: 1, EndLine: 5, Hash: "h3", Language: "go"},
	}
	for i := range nodes {
		db.Create(&nodes[i])
	}

	gen, outDir := newGenerator(t, db)
	gen.Exclude = []string{"vendor", "*_test.go"}

	if err := gen.Run(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(outDir, "index.md"))
	if err != nil {
		t.Fatalf("expected index.md: %v", err)
	}
	got := string(content)

	if !strings.Contains(got, "Foo") {
		t.Errorf("expected Foo in index.md")
	}
	if strings.Contains(got, "Lib") {
		t.Errorf("vendor/lib.go::Lib should be excluded")
	}
	if strings.Contains(got, "TestFoo") {
		t.Errorf("*_test.go matched file should be excluded")
	}
}

func TestRun_RespectsNamespace(t *testing.T) {
	db := newTestDB(t)
	nodes := []graph.Node{
		{Namespace: "alpha", QualifiedName: "alpha.Foo", Kind: graph.NodeKindFunction, Name: "Foo", FilePath: "alpha/foo.go", StartLine: 1, EndLine: 5, Hash: "h1", Language: "go"},
		{Namespace: "beta", QualifiedName: "beta.Bar", Kind: graph.NodeKindFunction, Name: "Bar", FilePath: "beta/bar.go", StartLine: 1, EndLine: 5, Hash: "h2", Language: "go"},
	}
	for i := range nodes {
		db.Create(&nodes[i])
	}

	gen, outDir := newGenerator(t, db)
	gen.Namespace = "alpha"
	if err := gen.Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(outDir, "index.md"))
	if err != nil {
		t.Fatal(err)
	}
	got := string(content)
	if !strings.Contains(got, "Foo") {
		t.Fatalf("expected alpha symbol in index, got:\n%s", got)
	}
	if strings.Contains(got, "Bar") {
		t.Fatalf("beta symbol leaked into alpha docs:\n%s", got)
	}
}

func TestRun_PruneDeletesOnlyGeneratorManagedStaleDocs(t *testing.T) {
	db := newTestDB(t)
	oldNode := graph.Node{QualifiedName: "pkg.Old", Kind: graph.NodeKindFunction, Name: "Old", FilePath: "pkg/old.go", StartLine: 1, EndLine: 5, Hash: "h1", Language: "go"}
	db.Create(&oldNode)

	gen, outDir := newGenerator(t, db)
	gen.Prune = true
	if err := gen.Run(); err != nil {
		t.Fatalf("initial Run: %v", err)
	}
	userDoc := filepath.Join(outDir, "pkg", "user.go.md")
	if err := os.WriteFile(userDoc, []byte("# user doc\n"), 0644); err != nil {
		t.Fatal(err)
	}

	db.Delete(&oldNode)
	newNode := graph.Node{QualifiedName: "pkg.New", Kind: graph.NodeKindFunction, Name: "New", FilePath: "pkg/new.go", StartLine: 1, EndLine: 5, Hash: "h2", Language: "go"}
	db.Create(&newNode)
	if err := gen.Run(); err != nil {
		t.Fatalf("second Run: %v", err)
	}

	if _, err := os.Stat(filepath.Join(outDir, "pkg", "old.go.md")); !os.IsNotExist(err) {
		t.Fatalf("managed stale doc should be pruned, stat err=%v", err)
	}
	if _, err := os.Stat(userDoc); err != nil {
		t.Fatalf("user doc must not be pruned: %v", err)
	}
}

func TestRun_MalformedManifestFailsBeforeMutatingOutputs(t *testing.T) {
	db := newTestDB(t)
	node := graph.Node{QualifiedName: "pkg.New", Kind: graph.NodeKindFunction, Name: "New", FilePath: "pkg/new.go", StartLine: 1, EndLine: 5, Hash: "h1", Language: "go"}
	if err := db.Create(&node).Error; err != nil {
		t.Fatalf("create node: %v", err)
	}

	gen, outDir := newGenerator(t, db)
	gen.Prune = true
	manifestPath := filepath.Join(outDir, ".ccg-docs-manifest.json")
	malformed := []byte("{not-json")
	if err := os.WriteFile(manifestPath, malformed, 0o644); err != nil {
		t.Fatalf("write malformed manifest: %v", err)
	}
	indexPath := filepath.Join(outDir, "index.md")
	indexBefore := []byte("operator-owned sentinel\n")
	if err := os.WriteFile(indexPath, indexBefore, 0o644); err != nil {
		t.Fatalf("write sentinel index: %v", err)
	}

	if err := gen.Run(); err == nil {
		t.Fatal("Run() = nil, want malformed manifest error")
	}
	manifestAfter, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read manifest after failed run: %v", err)
	}
	if string(manifestAfter) != string(malformed) {
		t.Fatalf("manifest changed after failed load: %q", manifestAfter)
	}
	indexAfter, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatalf("read index after failed run: %v", err)
	}
	if string(indexAfter) != string(indexBefore) {
		t.Fatalf("index changed after failed manifest load: %q", indexAfter)
	}
	if _, err := os.Stat(filepath.Join(outDir, "pkg", "new.go.md")); !os.IsNotExist(err) {
		t.Fatalf("file doc written after failed manifest load: %v", err)
	}
}
