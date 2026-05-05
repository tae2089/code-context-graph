package changes

import (
	"context"
	"fmt"
	"testing"

	"github.com/tae2089/code-context-graph/internal/ctxns"
	"github.com/tae2089/code-context-graph/internal/model"
	"github.com/tae2089/code-context-graph/internal/paging"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

type mockGit struct {
	files            []string
	hunks            []Hunk
	changedFilesCall int
	diffHunksCall    int
}

func (m *mockGit) ChangedFiles(_ context.Context, _, _ string) ([]string, error) {
	m.changedFilesCall++
	return m.files, nil
}

func (m *mockGit) DiffHunks(_ context.Context, _, _ string, _ []string) ([]Hunk, error) {
	m.diffHunksCall++
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

func TestAnalyzePage_AppliesLimitOffsetAndHasMore(t *testing.T) {
	db := setupDB(t)
	for i := 1; i <= 5; i++ {
		seedNode(t, db, uint(i), fmt.Sprintf("Fn%d", i), fmt.Sprintf("f%d.go", i), 1, 50)
	}

	hunks := []Hunk{}
	files := []string{}
	for i := 1; i <= 5; i++ {
		file := fmt.Sprintf("f%d.go", i)
		files = append(files, file)
		hunks = append(hunks, Hunk{FilePath: file, StartLine: 10, EndLine: 20})
	}
	for i := 1; i <= 5; i++ {
		for j := 0; j < i; j++ {
			seedEdge(t, db, uint(i), uint(100+j))
		}
	}

	svc := New(db, &mockGit{files: files, hunks: hunks})

	page1, err := svc.AnalyzePage(context.Background(), ".", "main", paging.Request{Limit: 2, Offset: 0})
	if err != nil {
		t.Fatalf("page1: %v", err)
	}
	if len(page1.Items) != 2 {
		t.Fatalf("page1 items = %d, want 2", len(page1.Items))
	}
	if !page1.Pagination.HasMore {
		t.Fatalf("page1 has_more = false, want true")
	}
	if page1.Items[0].RiskScore < page1.Items[1].RiskScore {
		t.Fatalf("page1 not sorted by risk desc: %v", page1.Items)
	}

	page2, err := svc.AnalyzePage(context.Background(), ".", "main", paging.Request{Limit: 2, Offset: 2})
	if err != nil {
		t.Fatalf("page2: %v", err)
	}
	if len(page2.Items) != 2 {
		t.Fatalf("page2 items = %d, want 2", len(page2.Items))
	}
	if !page2.Pagination.HasMore {
		t.Fatalf("page2 has_more = false, want true")
	}

	page3, err := svc.AnalyzePage(context.Background(), ".", "main", paging.Request{Limit: 2, Offset: 4})
	if err != nil {
		t.Fatalf("page3: %v", err)
	}
	if len(page3.Items) != 1 {
		t.Fatalf("page3 items = %d, want 1", len(page3.Items))
	}
	if page3.Pagination.HasMore {
		t.Fatalf("page3 has_more = true, want false")
	}
}

func TestAnalyzePage_PreservesAnalyzeOrderingForPagedConsumers(t *testing.T) {
	db := setupDB(t)
	for i := 1; i <= 4; i++ {
		seedNode(t, db, uint(i), fmt.Sprintf("Fn%d", i), fmt.Sprintf("f%d.go", i), 1, 50)
	}
	for i := 1; i <= 4; i++ {
		for j := 0; j < i; j++ {
			seedEdge(t, db, uint(i), uint(100+j))
		}
	}

	files := []string{"f1.go", "f2.go", "f3.go", "f4.go"}
	hunks := []Hunk{
		{FilePath: "f1.go", StartLine: 10, EndLine: 20},
		{FilePath: "f2.go", StartLine: 10, EndLine: 20},
		{FilePath: "f3.go", StartLine: 10, EndLine: 20},
		{FilePath: "f4.go", StartLine: 10, EndLine: 20},
	}
	svc := New(db, &mockGit{files: files, hunks: hunks})

	legacy, err := svc.Analyze(context.Background(), ".", "main")
	if err != nil {
		t.Fatal(err)
	}
	page, err := svc.AnalyzePage(context.Background(), ".", "main", paging.Request{Limit: 2, Offset: 1})
	if err != nil {
		t.Fatal(err)
	}

	if len(page.Items) != 2 {
		t.Fatalf("items = %d, want 2", len(page.Items))
	}
	for i, item := range page.Items {
		want := legacy[i+1]
		if item.Node.ID != want.Node.ID || item.RiskScore != want.RiskScore || item.HunkCount != want.HunkCount {
			t.Fatalf("page item %d = %+v, want legacy slice item %+v", i, item, want)
		}
	}
}

func TestAnalyzePage_MatchesAnalyzeAcrossRepresentativeWindows(t *testing.T) {
	db := setupDB(t)
	for i := 1; i <= 8; i++ {
		seedNode(t, db, uint(i), fmt.Sprintf("Fn%d", i), fmt.Sprintf("f%d.go", i), 1, 50)
	}
	for i := 1; i <= 8; i++ {
		for j := 0; j < i; j++ {
			seedEdge(t, db, uint(i), uint(100+j))
		}
	}

	files := make([]string, 0, 8)
	hunks := make([]Hunk, 0, 8)
	for i := 1; i <= 8; i++ {
		file := fmt.Sprintf("f%d.go", i)
		files = append(files, file)
		hunks = append(hunks, Hunk{FilePath: file, StartLine: 10, EndLine: 20})
	}
	svc := New(db, &mockGit{files: files, hunks: hunks})

	legacy, err := svc.Analyze(context.Background(), ".", "main")
	if err != nil {
		t.Fatal(err)
	}

	for _, req := range []paging.Request{{Limit: 1, Offset: 0}, {Limit: 3, Offset: 0}, {Limit: 3, Offset: 2}, {Limit: 2, Offset: 5}} {
		page, err := svc.AnalyzePage(context.Background(), ".", "main", req)
		if err != nil {
			t.Fatalf("AnalyzePage(%+v): %v", req, err)
		}
		end := req.Offset + req.Limit
		if end > len(legacy) {
			end = len(legacy)
		}
		want := legacy[req.Offset:end]
		if len(page.Items) != len(want) {
			t.Fatalf("AnalyzePage(%+v) items=%d, want %d", req, len(page.Items), len(want))
		}
		for i := range want {
			if page.Items[i].Node.ID != want[i].Node.ID || page.Items[i].RiskScore != want[i].RiskScore || page.Items[i].HunkCount != want[i].HunkCount {
				t.Fatalf("AnalyzePage(%+v) item %d = %+v, want %+v", req, i, page.Items[i], want[i])
			}
		}
		wantHasMore := len(legacy) > req.Offset+req.Limit
		if page.Pagination.HasMore != wantHasMore {
			t.Fatalf("AnalyzePage(%+v) has_more=%v, want %v", req, page.Pagination.HasMore, wantHasMore)
		}
	}
}

func TestChangedNodeIDs_ReturnsUniqueChangedNodesWithoutRiskScoring(t *testing.T) {
	db := setupDB(t)
	seedNode(t, db, 1, "Foo", "a.go", 10, 30)
	seedNode(t, db, 2, "Bar", "a.go", 40, 60)
	seedNode(t, db, 3, "Baz", "b.go", 10, 30)
	seedEdge(t, db, 1, 100)
	seedEdge(t, db, 1, 101)

	git := &mockGit{
		files: []string{"a.go", "b.go"},
		hunks: []Hunk{
			{FilePath: "a.go", StartLine: 12, EndLine: 15},
			{FilePath: "a.go", StartLine: 20, EndLine: 25},
			{FilePath: "b.go", StartLine: 12, EndLine: 15},
		},
	}
	svc := New(db, git)

	ids, err := svc.ChangedNodeIDs(context.Background(), ".", "main")
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 {
		t.Fatalf("ids = %v, want two changed nodes", ids)
	}
	if ids[0] != 1 || ids[1] != 3 {
		t.Fatalf("ids = %v, want [1 3] in deterministic source order", ids)
	}
	if git.changedFilesCall != 1 || git.diffHunksCall != 1 {
		t.Fatalf("git calls = changed:%d hunks:%d, want one diff scan", git.changedFilesCall, git.diffHunksCall)
	}
}

func TestAnalyzePage_RejectsLimitAboveMax(t *testing.T) {
	db := setupDB(t)
	svc := New(db, &mockGit{})
	if _, err := svc.AnalyzePage(context.Background(), ".", "main", paging.Request{Limit: paging.MaxLimit + 1}); err == nil {
		t.Fatal("expected error for over-max limit")
	}
}

func TestAnalyzePage_OffsetBeyondTotalReturnsEmpty(t *testing.T) {
	db := setupDB(t)
	seedNode(t, db, 1, "Foo", "a.go", 10, 30)
	svc := New(db, &mockGit{
		files: []string{"a.go"},
		hunks: []Hunk{{FilePath: "a.go", StartLine: 12, EndLine: 15}},
	})
	page, err := svc.AnalyzePage(context.Background(), ".", "main", paging.Request{Limit: 10, Offset: 50})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 0 {
		t.Fatalf("items = %d, want 0", len(page.Items))
	}
	if page.Pagination.HasMore {
		t.Fatal("has_more = true, want false")
	}
}
