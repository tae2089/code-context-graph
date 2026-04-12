package largefunc

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
	if err := db.AutoMigrate(&model.Node{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func seedNode(t *testing.T, db *gorm.DB, id uint, name string, kind model.NodeKind, startLine, endLine int) {
	t.Helper()
	n := model.Node{
		ID:            id,
		QualifiedName: fmt.Sprintf("pkg::%s", name),
		Kind:          kind,
		Name:          name,
		FilePath:      "pkg.go",
		StartLine:     startLine,
		EndLine:       endLine,
		Language:      "go",
	}
	if err := db.Create(&n).Error; err != nil {
		t.Fatalf("seed node: %v", err)
	}
}

func TestFind_AboveThreshold(t *testing.T) {
	db := setupDB(t)
	seedNode(t, db, 1, "BigFunc", model.NodeKindFunction, 1, 50)

	svc := New(db)
	got, err := svc.Find(context.Background(), 30)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1, got %d", len(got))
	}
	if got[0].Name != "BigFunc" {
		t.Errorf("expected BigFunc, got %s", got[0].Name)
	}
}

func TestFind_BelowThreshold(t *testing.T) {
	db := setupDB(t)
	seedNode(t, db, 1, "SmallFunc", model.NodeKindFunction, 1, 10)

	svc := New(db)
	got, err := svc.Find(context.Background(), 30)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("expected 0, got %d", len(got))
	}
}

func TestFind_ExactThreshold(t *testing.T) {
	db := setupDB(t)
	seedNode(t, db, 1, "ExactFunc", model.NodeKindFunction, 1, 30)

	svc := New(db)
	got, err := svc.Find(context.Background(), 30)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("expected 0 (exact threshold not included), got %d", len(got))
	}
}

func TestFind_OnlyFunctionKinds(t *testing.T) {
	db := setupDB(t)
	seedNode(t, db, 1, "BigClass", model.NodeKindClass, 1, 100)
	seedNode(t, db, 2, "BigType", model.NodeKindType, 1, 100)
	seedNode(t, db, 3, "BigFunc", model.NodeKindFunction, 1, 100)
	seedNode(t, db, 4, "BigTest", model.NodeKindTest, 1, 100)

	svc := New(db)
	got, err := svc.Find(context.Background(), 30)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 (function + test only), got %d", len(got))
	}
}

func TestFind_OrderByLineCount(t *testing.T) {
	db := setupDB(t)
	seedNode(t, db, 1, "Medium", model.NodeKindFunction, 1, 50)
	seedNode(t, db, 2, "Large", model.NodeKindFunction, 1, 100)
	seedNode(t, db, 3, "Small", model.NodeKindFunction, 1, 40)

	svc := New(db)
	got, err := svc.Find(context.Background(), 30)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3, got %d", len(got))
	}
	if got[0].Name != "Large" {
		t.Errorf("expected first=Large, got %s", got[0].Name)
	}
	if got[1].Name != "Medium" {
		t.Errorf("expected second=Medium, got %s", got[1].Name)
	}
	if got[2].Name != "Small" {
		t.Errorf("expected third=Small, got %s", got[2].Name)
	}
}
