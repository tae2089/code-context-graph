package coverage

import (
	"context"
	"fmt"
	"math"
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

func seedNode(t *testing.T, db *gorm.DB, id uint, name string, kind model.NodeKind, file string) {
	t.Helper()
	n := model.Node{
		ID:            id,
		QualifiedName: fmt.Sprintf("%s::%s", file, name),
		Kind:          kind,
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

func seedEdge(t *testing.T, db *gorm.DB, from, to uint, kind model.EdgeKind) {
	t.Helper()
	e := model.Edge{
		FromNodeID:  from,
		ToNodeID:    to,
		Kind:        kind,
		Fingerprint: fmt.Sprintf("%d-%d-%s", from, to, kind),
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

func TestByFile_AllTested(t *testing.T) {
	db := setupDB(t)
	seedNode(t, db, 1, "Foo", model.NodeKindFunction, "a.go")
	seedNode(t, db, 2, "Bar", model.NodeKindFunction, "a.go")
	seedNode(t, db, 10, "TestFoo", model.NodeKindTest, "a_test.go")
	seedNode(t, db, 11, "TestBar", model.NodeKindTest, "a_test.go")
	seedEdge(t, db, 10, 1, model.EdgeKindTestedBy)
	seedEdge(t, db, 11, 2, model.EdgeKindTestedBy)

	svc := New(db)
	got, err := svc.ByFile(context.Background(), "a.go")
	if err != nil {
		t.Fatal(err)
	}
	if got.Total != 2 {
		t.Errorf("expected total=2, got %d", got.Total)
	}
	if got.Tested != 2 {
		t.Errorf("expected tested=2, got %d", got.Tested)
	}
	if got.Ratio != 1.0 {
		t.Errorf("expected ratio=1.0, got %.2f", got.Ratio)
	}
}

func TestByFile_NoneTested(t *testing.T) {
	db := setupDB(t)
	seedNode(t, db, 1, "Foo", model.NodeKindFunction, "a.go")

	svc := New(db)
	got, err := svc.ByFile(context.Background(), "a.go")
	if err != nil {
		t.Fatal(err)
	}
	if got.Ratio != 0.0 {
		t.Errorf("expected ratio=0.0, got %.2f", got.Ratio)
	}
}

func TestByFile_PartialCoverage(t *testing.T) {
	db := setupDB(t)
	seedNode(t, db, 1, "Foo", model.NodeKindFunction, "a.go")
	seedNode(t, db, 2, "Bar", model.NodeKindFunction, "a.go")
	seedNode(t, db, 3, "Baz", model.NodeKindFunction, "a.go")
	seedNode(t, db, 10, "TestFoo", model.NodeKindTest, "a_test.go")
	seedNode(t, db, 11, "TestBar", model.NodeKindTest, "a_test.go")
	seedEdge(t, db, 10, 1, model.EdgeKindTestedBy)
	seedEdge(t, db, 11, 2, model.EdgeKindTestedBy)

	svc := New(db)
	got, err := svc.ByFile(context.Background(), "a.go")
	if err != nil {
		t.Fatal(err)
	}
	if got.Total != 3 {
		t.Errorf("expected total=3, got %d", got.Total)
	}
	if got.Tested != 2 {
		t.Errorf("expected tested=2, got %d", got.Tested)
	}
	expected := 2.0 / 3.0
	if math.Abs(got.Ratio-expected) > 0.001 {
		t.Errorf("expected ratio≈%.3f, got %.3f", expected, got.Ratio)
	}
}

func TestByFile_NoFunctions(t *testing.T) {
	db := setupDB(t)
	seedNode(t, db, 1, "a.go", model.NodeKindFile, "a.go")

	svc := New(db)
	got, err := svc.ByFile(context.Background(), "a.go")
	if err != nil {
		t.Fatal(err)
	}
	if got.Total != 0 {
		t.Errorf("expected total=0, got %d", got.Total)
	}
	if got.Ratio != 0.0 {
		t.Errorf("expected ratio=0.0, got %.2f", got.Ratio)
	}
}

func TestByCommunity_AggregatesFiles(t *testing.T) {
	db := setupDB(t)
	seedNode(t, db, 1, "Foo", model.NodeKindFunction, "a/x.go")
	seedNode(t, db, 2, "Bar", model.NodeKindFunction, "a/y.go")
	seedNode(t, db, 3, "Baz", model.NodeKindFunction, "a/y.go")
	seedNode(t, db, 10, "TestFoo", model.NodeKindTest, "a/x_test.go")
	seedEdge(t, db, 10, 1, model.EdgeKindTestedBy)
	seedCommunity(t, db, 1, "a", 1, 2, 3)

	svc := New(db)
	got, err := svc.ByCommunity(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if got.Total != 3 {
		t.Errorf("expected total=3, got %d", got.Total)
	}
	if got.Tested != 1 {
		t.Errorf("expected tested=1, got %d", got.Tested)
	}
}

func TestByCommunity_InvalidID(t *testing.T) {
	db := setupDB(t)

	svc := New(db)
	_, err := svc.ByCommunity(context.Background(), 999)
	if err == nil {
		t.Fatal("expected error for invalid community ID")
	}
}
