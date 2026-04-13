package docs_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/imtaebin/code-context-graph/internal/docs"
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

func newGenerator(t *testing.T, db *gorm.DB) (*docs.Generator, string) {
	t.Helper()
	outDir := t.TempDir()
	return &docs.Generator{DB: db, OutDir: outDir}, outDir
}

func TestRun_EmptyDB(t *testing.T) {
	db := newTestDB(t)
	gen, _ := newGenerator(t, db)
	if err := gen.Run(); err != nil {
		t.Fatalf("unexpected error on empty DB: %v", err)
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
	edgesByFromID, err := gen.LoadEdges([]uint{from.ID, to.ID})
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
		NodeID: fnNode.ID,
		Tags: []model.DocTag{
			{Kind: model.TagIntent, Value: "bar 요청을 처리한다", Ordinal: 0},
			{Kind: model.TagDomainRule, Value: "입력값은 양수여야 한다", Ordinal: 1},
			{Kind: model.TagParam, Name: "n", Value: "처리할 정수", Ordinal: 2},
			{Kind: model.TagReturn, Value: "처리 결과 문자열", Ordinal: 3},
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
		"**Intent:** bar 요청을 처리한다",
		"**Domain Rules:**",
		"입력값은 양수여야 한다",
		"`n` — 처리할 정수",
		"**Returns:** 처리 결과 문자열",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("expected %q in file doc, got:\n%s", want, got)
		}
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
	nodes, annByID, err := gen.LoadNodes()
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
