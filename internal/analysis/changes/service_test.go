package changes

import (
	"context"
	"fmt"
	"testing"

	"github.com/imtaebin/code-context-graph/internal/ctxns"
	"github.com/imtaebin/code-context-graph/internal/model"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

type mockGit struct {
	files []string
	hunks []Hunk
}

func (m *mockGit) ChangedFiles(_ context.Context, _, _ string) ([]string, error) {
	return m.files, nil
}

func (m *mockGit) DiffHunks(_ context.Context, _, _ string, _ []string) ([]Hunk, error) {
	return m.hunks, nil
}

func setupDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: gormlogger.Discard,
	})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.Node{}, &model.Edge{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func seedNode(t *testing.T, db *gorm.DB, id uint, name string, file string, start, end int) {
	t.Helper()
	seedNodeNS(t, db, id, name, file, start, end, "")
}

func seedNodeNS(t *testing.T, db *gorm.DB, id uint, name string, file string, start, end int, ns string) {
	t.Helper()
	n := model.Node{
		ID:            id,
		QualifiedName: fmt.Sprintf("%s::%s", file, name),
		Namespace:     ns,
		Kind:          model.NodeKindFunction,
		Name:          name,
		FilePath:      file,
		StartLine:     start,
		EndLine:       end,
		Language:      "go",
	}
	if err := db.Create(&n).Error; err != nil {
		t.Fatalf("seed node: %v", err)
	}
}

func seedEdge(t *testing.T, db *gorm.DB, from, to uint) {
	t.Helper()
	e := model.Edge{
		FromNodeID:  from,
		ToNodeID:    to,
		Kind:        model.EdgeKindCalls,
		Fingerprint: fmt.Sprintf("%d-%d", from, to),
	}
	if err := db.Create(&e).Error; err != nil {
		t.Fatalf("seed edge: %v", err)
	}
}

func TestAnalyze_ChangedFunction(t *testing.T) {
	db := setupDB(t)
	seedNode(t, db, 1, "Foo", "a.go", 10, 30)

	git := &mockGit{
		files: []string{"a.go"},
		hunks: []Hunk{{FilePath: "a.go", StartLine: 15, EndLine: 20}},
	}
	svc := New(db, git)
	got, err := svc.Analyze(context.Background(), ".", "main")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 risk entry, got %d", len(got))
	}
	if got[0].Node.Name != "Foo" {
		t.Errorf("expected Foo, got %s", got[0].Node.Name)
	}
}

func TestAnalyze_NoOverlap(t *testing.T) {
	db := setupDB(t)
	seedNode(t, db, 1, "Foo", "a.go", 10, 30)

	git := &mockGit{
		files: []string{"a.go"},
		hunks: []Hunk{{FilePath: "a.go", StartLine: 1, EndLine: 5}},
	}
	svc := New(db, git)
	got, err := svc.Analyze(context.Background(), ".", "main")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("expected 0, got %d", len(got))
	}
}

func TestAnalyze_MultipleHunks(t *testing.T) {
	db := setupDB(t)
	seedNode(t, db, 1, "Foo", "a.go", 10, 50)

	git := &mockGit{
		files: []string{"a.go"},
		hunks: []Hunk{
			{FilePath: "a.go", StartLine: 12, EndLine: 15},
			{FilePath: "a.go", StartLine: 20, EndLine: 25},
			{FilePath: "a.go", StartLine: 40, EndLine: 45},
		},
	}
	svc := New(db, git)
	got, err := svc.Analyze(context.Background(), ".", "main")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1, got %d", len(got))
	}
	if got[0].HunkCount != 3 {
		t.Errorf("expected HunkCount=3, got %d", got[0].HunkCount)
	}
}

func TestAnalyze_RiskScoreCalculation(t *testing.T) {
	db := setupDB(t)
	seedNode(t, db, 1, "Foo", "a.go", 10, 50)
	seedEdge(t, db, 1, 100)
	seedEdge(t, db, 1, 101)
	seedEdge(t, db, 1, 102)

	git := &mockGit{
		files: []string{"a.go"},
		hunks: []Hunk{
			{FilePath: "a.go", StartLine: 12, EndLine: 15},
			{FilePath: "a.go", StartLine: 20, EndLine: 25},
		},
	}
	svc := New(db, git)
	got, err := svc.Analyze(context.Background(), ".", "main")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1, got %d", len(got))
	}
	// RiskScore = HunkCount * (outgoing edges + 1) = 2 * (3 + 1) = 8.0
	if got[0].RiskScore != 8.0 {
		t.Errorf("expected RiskScore=8.0, got %.1f", got[0].RiskScore)
	}
}

func TestAnalyze_EmptyDiff(t *testing.T) {
	db := setupDB(t)

	git := &mockGit{files: nil, hunks: nil}
	svc := New(db, git)
	got, err := svc.Analyze(context.Background(), ".", "main")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("expected 0, got %d", len(got))
	}
}

func TestAnalyze_RespectsNamespace(t *testing.T) {
	db := setupDB(t)
	seedNodeNS(t, db, 1, "FooA", "a.go", 10, 30, "ns-a")
	seedNodeNS(t, db, 2, "FooB", "a.go", 10, 30, "ns-b")

	git := &mockGit{
		files: []string{"a.go"},
		hunks: []Hunk{{FilePath: "a.go", StartLine: 15, EndLine: 20}},
	}
	svc := New(db, git)

	ctxA := ctxns.WithNamespace(context.Background(), "ns-a")
	got, err := svc.Analyze(ctxA, ".", "main")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 risk entry for ns-a, got %d", len(got))
	}
	if got[0].Node.Name != "FooA" {
		t.Errorf("expected FooA, got %s", got[0].Node.Name)
	}
}
