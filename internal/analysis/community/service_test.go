package community

import (
	"context"
	"fmt"
	"testing"

	"github.com/imtaebin/code-context-graph/internal/model"
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

func seedNode(t *testing.T, db *gorm.DB, id uint, name string, file string) {
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

func TestRebuild_GroupsByDirectory(t *testing.T) {
	db := setupDB(t)
	seedNode(t, db, 1, "X", "a/x.go")
	seedNode(t, db, 2, "Y", "a/y.go")
	seedNode(t, db, 3, "Z", "b/z.go")

	b := New(db)
	stats, err := b.Rebuild(context.Background(), Config{Depth: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 2 {
		t.Fatalf("expected 2 communities, got %d", len(stats))
	}
}

func TestRebuild_DepthConfig(t *testing.T) {
	db := setupDB(t)
	seedNode(t, db, 1, "X", "a/b/x.go")
	seedNode(t, db, 2, "Y", "a/c/y.go")

	b := New(db)
	stats, err := b.Rebuild(context.Background(), Config{Depth: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 2 {
		t.Fatalf("expected 2 communities (a/b, a/c), got %d", len(stats))
	}
}

func TestRebuild_Depth1(t *testing.T) {
	db := setupDB(t)
	seedNode(t, db, 1, "X", "a/b/x.go")
	seedNode(t, db, 2, "Y", "a/c/y.go")

	b := New(db)
	stats, err := b.Rebuild(context.Background(), Config{Depth: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 1 {
		t.Fatalf("expected 1 community (a), got %d", len(stats))
	}
}

func TestRebuild_CohesionScore(t *testing.T) {
	db := setupDB(t)
	seedNode(t, db, 1, "A1", "a/a1.go")
	seedNode(t, db, 2, "A2", "a/a2.go")
	seedNode(t, db, 3, "A3", "a/a3.go")
	seedNode(t, db, 4, "B1", "b/b1.go")
	seedEdge(t, db, 1, 2) // internal
	seedEdge(t, db, 2, 3) // internal
	seedEdge(t, db, 3, 1) // internal
	seedEdge(t, db, 1, 4) // external

	b := New(db)
	stats, err := b.Rebuild(context.Background(), Config{Depth: 1})
	if err != nil {
		t.Fatal(err)
	}

	var aStat *Stats
	for i := range stats {
		if stats[i].Community.Key == "a" {
			aStat = &stats[i]
			break
		}
	}
	if aStat == nil {
		t.Fatal("community 'a' not found")
	}
	if aStat.InternalEdges != 3 {
		t.Errorf("expected 3 internal edges, got %d", aStat.InternalEdges)
	}
	if aStat.ExternalEdges != 1 {
		t.Errorf("expected 1 external edge, got %d", aStat.ExternalEdges)
	}
	expectedCohesion := 0.75
	if aStat.Cohesion != expectedCohesion {
		t.Errorf("expected cohesion %.2f, got %.2f", expectedCohesion, aStat.Cohesion)
	}
}

func TestRebuild_NoEdges(t *testing.T) {
	db := setupDB(t)
	seedNode(t, db, 1, "A", "a/a.go")

	b := New(db)
	stats, err := b.Rebuild(context.Background(), Config{Depth: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 1 {
		t.Fatalf("expected 1 community, got %d", len(stats))
	}
	if stats[0].Cohesion != 0.0 {
		t.Errorf("expected cohesion 0.0, got %.2f", stats[0].Cohesion)
	}
}

func TestRebuild_ReplacesPrevious(t *testing.T) {
	db := setupDB(t)
	seedNode(t, db, 1, "A", "a/a.go")

	b := New(db)
	_, err := b.Rebuild(context.Background(), Config{Depth: 1})
	if err != nil {
		t.Fatal(err)
	}

	var count1 int64
	db.Model(&model.Community{}).Count(&count1)

	_, err = b.Rebuild(context.Background(), Config{Depth: 1})
	if err != nil {
		t.Fatal(err)
	}

	var count2 int64
	db.Model(&model.Community{}).Count(&count2)

	if count2 != count1 {
		t.Errorf("expected same count after rebuild, got %d then %d", count1, count2)
	}
}

func TestRebuild_MembershipLinks(t *testing.T) {
	db := setupDB(t)
	seedNode(t, db, 1, "X", "a/x.go")
	seedNode(t, db, 2, "Y", "a/y.go")
	seedNode(t, db, 3, "Z", "b/z.go")

	b := New(db)
	_, err := b.Rebuild(context.Background(), Config{Depth: 1})
	if err != nil {
		t.Fatal(err)
	}

	var memberships []model.CommunityMembership
	if err := db.Find(&memberships).Error; err != nil {
		t.Fatal(err)
	}
	if len(memberships) != 3 {
		t.Fatalf("expected 3 memberships, got %d", len(memberships))
	}

	nodeComm := map[uint]uint{}
	for _, m := range memberships {
		if _, exists := nodeComm[m.NodeID]; exists {
			t.Errorf("node %d has multiple memberships", m.NodeID)
		}
		nodeComm[m.NodeID] = m.CommunityID
	}
	if nodeComm[1] != nodeComm[2] {
		t.Error("nodes 1 and 2 should be in the same community")
	}
	if nodeComm[1] == nodeComm[3] {
		t.Error("nodes 1 and 3 should be in different communities")
	}
}
