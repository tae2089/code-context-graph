package docs_test

import (
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
