package coupling

import (
	"context"
	"fmt"
	"math"
	"reflect"
	"testing"

	"github.com/tae2089/code-context-graph/internal/model"
	"github.com/tae2089/code-context-graph/internal/paging"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

func setupDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: gormlogger.Discard,
	})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.Node{}, &model.Edge{}, &model.Community{}, &model.CommunityMembership{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func seedNode(t *testing.T, db *gorm.DB, id uint, name, file string) {
	t.Helper()
	n := model.Node{
		ID:            id,
		QualifiedName: fmt.Sprintf("%s::%s", file, name),
		Kind:          model.NodeKindFunction,
		Name:          name,
		FilePath:      file,
		StartLine:     1,
		EndLine:       10,
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

func seedCommunity(t *testing.T, db *gorm.DB, id uint, key string, nodeIDs ...uint) {
	t.Helper()
	c := model.Community{ID: id, Key: key, Label: key, Strategy: "directory"}
	if err := db.Create(&c).Error; err != nil {
		t.Fatalf("seed community: %v", err)
	}
	for _, nid := range nodeIDs {
		m := model.CommunityMembership{CommunityID: id, NodeID: nid}
		if err := db.Create(&m).Error; err != nil {
			t.Fatalf("seed membership: %v", err)
		}
	}
}

func findPair(pairs []CouplingPair, from, to string) *CouplingPair {
	for i := range pairs {
		if pairs[i].FromCommunity == from && pairs[i].ToCommunity == to {
			return &pairs[i]
		}
	}
	return nil
}

func TestAnalyze_TwoCommunities(t *testing.T) {
	db := setupDB(t)
	seedNode(t, db, 1, "A1", "a/a.go")
	seedNode(t, db, 2, "B1", "b/b.go")
	seedCommunity(t, db, 1, "a", 1)
	seedCommunity(t, db, 2, "b", 2)
	for i := range 5 {
		e := model.Edge{
			FromNodeID:  1,
			ToNodeID:    2,
			Kind:        model.EdgeKindCalls,
			Fingerprint: fmt.Sprintf("1-2-%d", i),
		}
		db.Create(&e)
	}

	svc := New(db)
	got, err := svc.Analyze(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	p := findPair(got, "a", "b")
	if p == nil {
		t.Fatal("expected coupling pair a→b")
	}
	if p.EdgeCount != 5 {
		t.Errorf("expected 5 edges, got %d", p.EdgeCount)
	}
}

func TestAnalyze_NoCrossCommunityEdges(t *testing.T) {
	db := setupDB(t)
	seedNode(t, db, 1, "A1", "a/a.go")
	seedNode(t, db, 2, "A2", "a/a2.go")
	seedCommunity(t, db, 1, "a", 1, 2)
	seedEdge(t, db, 1, 2)

	svc := New(db)
	got, err := svc.Analyze(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("expected 0 cross-community pairs, got %d", len(got))
	}
}

func TestAnalyze_Strength(t *testing.T) {
	db := setupDB(t)
	seedNode(t, db, 1, "A1", "a/a.go")
	seedNode(t, db, 2, "B1", "b/b.go")
	seedNode(t, db, 3, "C1", "c/c.go")
	seedCommunity(t, db, 1, "a", 1)
	seedCommunity(t, db, 2, "b", 2)
	seedCommunity(t, db, 3, "c", 3)
	for i := range 10 {
		db.Create(&model.Edge{FromNodeID: 1, ToNodeID: 2, Kind: model.EdgeKindCalls, Fingerprint: fmt.Sprintf("ab-%d", i)})
	}
	for i := range 5 {
		db.Create(&model.Edge{FromNodeID: 1, ToNodeID: 3, Kind: model.EdgeKindCalls, Fingerprint: fmt.Sprintf("ac-%d", i)})
	}

	svc := New(db)
	got, err := svc.Analyze(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ab := findPair(got, "a", "b")
	ac := findPair(got, "a", "c")
	if ab == nil || ac == nil {
		t.Fatal("expected both a→b and a→c pairs")
	}
	if ab.Strength != 1.0 {
		t.Errorf("expected a→b strength=1.0, got %.2f", ab.Strength)
	}
	if math.Abs(ac.Strength-0.5) > 0.001 {
		t.Errorf("expected a→c strength=0.5, got %.2f", ac.Strength)
	}
}

func TestAnalyze_BidirectionalCounting(t *testing.T) {
	db := setupDB(t)
	seedNode(t, db, 1, "A1", "a/a.go")
	seedNode(t, db, 2, "B1", "b/b.go")
	seedCommunity(t, db, 1, "a", 1)
	seedCommunity(t, db, 2, "b", 2)
	for i := range 3 {
		db.Create(&model.Edge{FromNodeID: 1, ToNodeID: 2, Kind: model.EdgeKindCalls, Fingerprint: fmt.Sprintf("ab-%d", i)})
	}
	for i := range 2 {
		db.Create(&model.Edge{FromNodeID: 2, ToNodeID: 1, Kind: model.EdgeKindCalls, Fingerprint: fmt.Sprintf("ba-%d", i)})
	}

	svc := New(db)
	got, err := svc.Analyze(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 pairs (a→b, b→a), got %d", len(got))
	}
	ab := findPair(got, "a", "b")
	ba := findPair(got, "b", "a")
	if ab == nil || ba == nil {
		t.Fatal("expected both a→b and b→a")
	}
	if ab.EdgeCount != 3 {
		t.Errorf("expected a→b count=3, got %d", ab.EdgeCount)
	}
	if ba.EdgeCount != 2 {
		t.Errorf("expected b→a count=2, got %d", ba.EdgeCount)
	}
}

func TestAnalyze_NoCommunities(t *testing.T) {
	db := setupDB(t)

	svc := New(db)
	got, err := svc.Analyze(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("expected 0, got %d", len(got))
	}
}

func seedCrossPair(t *testing.T, db *gorm.DB, fromNode, toNode uint, count int, tag string) {
	t.Helper()
	for i := range count {
		if err := db.Create(&model.Edge{FromNodeID: fromNode, ToNodeID: toNode, Kind: model.EdgeKindCalls, Fingerprint: fmt.Sprintf("%s-%d", tag, i)}).Error; err != nil {
			t.Fatalf("seed edge: %v", err)
		}
	}
}

func TestAnalyzePage_AppliesLimitOffsetAndHasMore(t *testing.T) {
	db := setupDB(t)
	for i := uint(1); i <= 4; i++ {
		seedNode(t, db, i, fmt.Sprintf("N%d", i), fmt.Sprintf("c%d/c.go", i))
		seedCommunity(t, db, i, fmt.Sprintf("c%d", i), i)
	}
	seedCrossPair(t, db, 1, 2, 10, "ab")
	seedCrossPair(t, db, 1, 3, 7, "ac")
	seedCrossPair(t, db, 1, 4, 4, "ad")
	seedCrossPair(t, db, 2, 3, 2, "bc")

	svc := New(db)

	page1, err := svc.AnalyzePage(context.Background(), paging.Request{Limit: 2, Offset: 0})
	if err != nil {
		t.Fatalf("page1: %v", err)
	}
	if len(page1.Items) != 2 {
		t.Fatalf("page1 items=%d, want 2", len(page1.Items))
	}
	if !page1.Pagination.HasMore {
		t.Fatalf("page1 has_more=false, want true")
	}
	if page1.Items[0].Strength < page1.Items[1].Strength {
		t.Fatalf("page1 not sorted by strength desc: %+v", page1.Items)
	}

	page2, err := svc.AnalyzePage(context.Background(), paging.Request{Limit: 2, Offset: 2})
	if err != nil {
		t.Fatalf("page2: %v", err)
	}
	if len(page2.Items) != 2 {
		t.Fatalf("page2 items=%d, want 2", len(page2.Items))
	}
	if page2.Pagination.HasMore {
		t.Fatalf("page2 has_more=true, want false")
	}
}

func TestAnalyzePage_MatchesAnalyzeWindows(t *testing.T) {
	db := setupDB(t)
	for i := uint(1); i <= 5; i++ {
		seedNode(t, db, i, fmt.Sprintf("N%d", i), fmt.Sprintf("c%d/c.go", i))
		seedCommunity(t, db, i, fmt.Sprintf("c%d", i), i)
	}
	seedCrossPair(t, db, 1, 2, 9, "c1-c2")
	seedCrossPair(t, db, 1, 3, 9, "c1-c3")
	seedCrossPair(t, db, 2, 3, 6, "c2-c3")
	seedCrossPair(t, db, 3, 4, 3, "c3-c4")
	seedCrossPair(t, db, 4, 5, 3, "c4-c5")
	seedCrossPair(t, db, 5, 1, 1, "c5-c1")

	svc := New(db)
	all, err := svc.Analyze(context.Background())
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}

	windows := []paging.Request{
		{Limit: 2, Offset: 0},
		{Limit: 2, Offset: 2},
		{Limit: 3, Offset: 3},
	}
	for _, window := range windows {
		page, err := svc.AnalyzePage(context.Background(), window)
		if err != nil {
			t.Fatalf("AnalyzePage(%+v): %v", window, err)
		}
		end := min(window.Offset+window.Limit, len(all))
		want := all[window.Offset:end]
		if !reflect.DeepEqual(page.Items, want) {
			t.Fatalf("AnalyzePage(%+v) items=%+v, want %+v", window, page.Items, want)
		}
	}
}

func TestAnalyzePage_RejectsLimitAboveMax(t *testing.T) {
	db := setupDB(t)
	svc := New(db)
	if _, err := svc.AnalyzePage(context.Background(), paging.Request{Limit: paging.MaxLimit + 1}); err == nil {
		t.Fatal("expected error for over-max limit")
	}
}

func TestAnalyzePage_OffsetBeyondTotalReturnsEmpty(t *testing.T) {
	db := setupDB(t)
	seedNode(t, db, 1, "A1", "a/a.go")
	seedNode(t, db, 2, "B1", "b/b.go")
	seedCommunity(t, db, 1, "a", 1)
	seedCommunity(t, db, 2, "b", 2)
	seedCrossPair(t, db, 1, 2, 3, "ab")

	svc := New(db)
	page, err := svc.AnalyzePage(context.Background(), paging.Request{Limit: 10, Offset: 50})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 0 {
		t.Fatalf("items=%d, want 0", len(page.Items))
	}
	if page.Pagination.HasMore {
		t.Fatal("has_more=true, want false")
	}
}
