package docs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/imtaebin/code-context-graph/internal/model"
	"github.com/imtaebin/code-context-graph/internal/store/gormstore"
)

func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	st := gormstore.New(db)
	if err := st.AutoMigrate(); err != nil {
		t.Fatal(err)
	}
	return db
}

func newGenerator(t *testing.T, db *gorm.DB) (*Generator, string) {
	t.Helper()
	outDir := t.TempDir()
	return &Generator{DB: db, OutDir: outDir}, outDir
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

	fnNode := model.Node{
		QualifiedName: "pkg/foo.go::Bar",
		Kind:          model.NodeKindFunction,
		Name:          "Bar",
		FilePath:      "pkg/foo.go",
		StartLine:     10,
		EndLine:       20,
		Hash:          "h1",
		Language:      "go",
	}
	db.Create(&fnNode)

	ann := model.Annotation{
		NodeID: fnNode.ID,
		Tags: []model.DocTag{
			{Kind: model.TagIntent, Value: "processes bar", Ordinal: 0},
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

	from := model.Node{QualifiedName: "a.go::A", Kind: model.NodeKindFunction, Name: "A", FilePath: "a.go", StartLine: 1, EndLine: 5, Hash: "h1", Language: "go"}
	to := model.Node{QualifiedName: "b.go::B", Kind: model.NodeKindFunction, Name: "B", FilePath: "b.go", StartLine: 1, EndLine: 5, Hash: "h2", Language: "go"}
	db.Create(&from)
	db.Create(&to)

	edge := model.Edge{
		FromNodeID:  from.ID,
		ToNodeID:    to.ID,
		Kind:        model.EdgeKindCalls,
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

	fileNode := model.Node{
		QualifiedName: "internal/foo.go",
		Kind:          model.NodeKindFile,
		Name:          "foo.go",
		FilePath:      "internal/foo.go",
		StartLine:     1, EndLine: 50,
		Hash: "hf", Language: "go",
	}
	db.Create(&fileNode)

	fileAnn := model.Annotation{
		NodeID: fileNode.ID,
		Tags:   []model.DocTag{{Kind: model.TagIndex, Value: "foo 패키지 유틸리티", Ordinal: 0}},
	}
	db.Create(&fileAnn)

	fnNode := model.Node{
		QualifiedName: "internal/foo.go::Bar",
		Kind:          model.NodeKindFunction,
		Name:          "Bar",
		FilePath:      "internal/foo.go",
		StartLine:     10, EndLine: 20,
		Hash: "h1", Language: "go",
	}
	db.Create(&fnNode)

	ann := model.Annotation{
		NodeID:  fnNode.ID,
		Summary: "Bar 함수 요약",
		Context: "레거시 시스템과의 호환성을 위해 존재",
		Tags: []model.DocTag{
			{Kind: model.TagIntent, Value: "bar 요청을 처리한다", Ordinal: 0},
			{Kind: model.TagDomainRule, Value: "입력값은 양수여야 한다", Ordinal: 1},
			{Kind: model.TagSideEffect, Value: "DB에 로그를 기록한다", Ordinal: 2},
			{Kind: model.TagMutates, Value: "users 테이블", Ordinal: 3},
			{Kind: model.TagRequires, Value: "n > 0", Ordinal: 4},
			{Kind: model.TagEnsures, Value: "반환값은 비어 있지 않다", Ordinal: 5},
			{Kind: model.TagParam, Name: "n", Value: "처리할 정수", Ordinal: 6},
			{Kind: model.TagReturn, Value: "처리 결과 문자열", Ordinal: 7},
			{Kind: model.TagSee, Value: "pkg/service.go::Handle", Ordinal: 8},
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

func TestRun_GeneratesIndexMd(t *testing.T) {
	db := newTestDB(t)

	fnNode := model.Node{
		QualifiedName: "pkg/service.go::Handle",
		Kind:          model.NodeKindFunction,
		Name:          "Handle",
		FilePath:      "pkg/service.go",
		StartLine:     5, EndLine: 15,
		Hash: "h3", Language: "go",
	}
	db.Create(&fnNode)

	typeNode := model.Node{
		QualifiedName: "pkg/service.go::Request",
		Kind:          model.NodeKindType,
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
	nodes := []model.Node{
		{ID: 1, Kind: model.NodeKindFunction, Name: "Z", FilePath: "z/z.go", QualifiedName: "z/z.go::Z", StartLine: 1, EndLine: 2, Hash: "h1", Language: "go"},
		{ID: 2, Kind: model.NodeKindFunction, Name: "A", FilePath: "a/a.go", QualifiedName: "a/a.go::A", StartLine: 1, EndLine: 2, Hash: "h2", Language: "go"},
		{ID: 3, Kind: model.NodeKindFunction, Name: "M", FilePath: "m/m.go", QualifiedName: "m/m.go::M", StartLine: 1, EndLine: 2, Hash: "h3", Language: "go"},
	}

	groups := groupByFile(nodes, nil, nil)

	if len(groups) != 3 {
		t.Fatalf("expected 3 groups, got %d", len(groups))
	}
	if groups[0].FilePath != "a/a.go" || groups[1].FilePath != "m/m.go" || groups[2].FilePath != "z/z.go" {
		t.Errorf("expected alphabetical order, got %v, %v, %v", groups[0].FilePath, groups[1].FilePath, groups[2].FilePath)
	}
}
