package service

import (
	"context"
	"errors"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/tae2089/code-context-graph/internal/ctxns"
	"github.com/tae2089/code-context-graph/internal/model"
	"github.com/tae2089/code-context-graph/internal/store"
	"github.com/tae2089/code-context-graph/internal/store/gormstore"
	storesearch "github.com/tae2089/code-context-graph/internal/store/search"
)

type failingGraphStore struct {
	store.GraphStore
	err error
}

func (f failingGraphStore) DeleteGraph(ctx context.Context) error { return f.err }

type nonTransactionalNodeWriter struct {
	store.NodeWriter
}

type purgerSpyBackend struct {
	storesearch.Backend
	calls    []string
	lastDB   *gorm.DB
	purgeErr error
}

func (s *purgerSpyBackend) PurgeNamespace(ctx context.Context, db *gorm.DB) error {
	s.calls = append(s.calls, ctxns.FromContext(ctx))
	s.lastDB = db
	return s.purgeErr
}

func (s *purgerSpyBackend) RebuildNodes(ctx context.Context, db *gorm.DB, nodeIDs []uint) error {
	return nil
}

func newPurgerTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	st := gormstore.New(db)
	if err := st.AutoMigrate(); err != nil {
		t.Fatalf("migrate store: %v", err)
	}
	if err := db.AutoMigrate(
		&model.SearchDocument{},
		&model.Community{}, &model.CommunityMembership{},
		&model.Flow{}, &model.FlowMembership{},
	); err != nil {
		t.Fatalf("migrate extras: %v", err)
	}
	return db
}

func seedTwoNamespaces(t *testing.T, db *gorm.DB) (uint, uint, uint, uint) {
	t.Helper()
	st := gormstore.New(db)
	ctxA := ctxns.WithNamespace(context.Background(), "ns-a")
	ctxB := ctxns.WithNamespace(context.Background(), "ns-b")
	if err := st.UpsertNodes(ctxA, []model.Node{{QualifiedName: "a.F", Kind: model.NodeKindFunction, Name: "F", FilePath: "a.go", StartLine: 1, EndLine: 2, Language: "go"}}); err != nil {
		t.Fatalf("upsert ns-a node: %v", err)
	}
	if err := st.UpsertNodes(ctxB, []model.Node{{QualifiedName: "b.F", Kind: model.NodeKindFunction, Name: "F", FilePath: "b.go", StartLine: 1, EndLine: 2, Language: "go"}}); err != nil {
		t.Fatalf("upsert ns-b node: %v", err)
	}
	nodeA, err := st.GetNode(ctxA, "a.F")
	if err != nil || nodeA == nil {
		t.Fatalf("get ns-a node: %v", err)
	}
	nodeB, err := st.GetNode(ctxB, "b.F")
	if err != nil || nodeB == nil {
		t.Fatalf("get ns-b node: %v", err)
	}

	commA := model.Community{Namespace: "ns-a", Key: "ns-a/core", Label: "ns-a/core", Strategy: "directory"}
	commB := model.Community{Namespace: "ns-b", Key: "ns-b/core", Label: "ns-b/core", Strategy: "directory"}
	if err := db.Create(&commA).Error; err != nil {
		t.Fatalf("create community a: %v", err)
	}
	if err := db.Create(&commB).Error; err != nil {
		t.Fatalf("create community b: %v", err)
	}
	if err := db.Create(&model.CommunityMembership{CommunityID: commA.ID, NodeID: nodeA.ID}).Error; err != nil {
		t.Fatalf("create comm-mem a: %v", err)
	}
	if err := db.Create(&model.CommunityMembership{CommunityID: commB.ID, NodeID: nodeB.ID}).Error; err != nil {
		t.Fatalf("create comm-mem b: %v", err)
	}

	flowA := model.Flow{Namespace: "ns-a", Name: "a-flow"}
	flowB := model.Flow{Namespace: "ns-b", Name: "b-flow"}
	if err := db.Create(&flowA).Error; err != nil {
		t.Fatalf("create flow a: %v", err)
	}
	if err := db.Create(&flowB).Error; err != nil {
		t.Fatalf("create flow b: %v", err)
	}
	if err := db.Create(&model.FlowMembership{Namespace: "ns-a", FlowID: flowA.ID, NodeID: nodeA.ID, Ordinal: 0}).Error; err != nil {
		t.Fatalf("create flow-mem a: %v", err)
	}
	if err := db.Create(&model.FlowMembership{Namespace: "ns-b", FlowID: flowB.ID, NodeID: nodeB.ID, Ordinal: 0}).Error; err != nil {
		t.Fatalf("create flow-mem b: %v", err)
	}
	return nodeA.ID, nodeB.ID, commA.ID, flowA.ID
}

func TestNamespacePurger_RemovesAllNamespaceArtifactsAndScopesToTarget(t *testing.T) {
	db := newPurgerTestDB(t)
	st := gormstore.New(db)
	backend := &purgerSpyBackend{}

	nodeIDA, nodeIDB, commAID, flowAID := seedTwoNamespaces(t, db)

	purger := NewNamespacePurger(st, db, backend)
	ctx := ctxns.WithNamespace(context.Background(), "ns-a")
	if err := purger.Purge(ctx); err != nil {
		t.Fatalf("Purge: %v", err)
	}

	if got, _ := st.GetNodeByID(ctx, nodeIDA); got != nil {
		t.Errorf("expected ns-a node purged, got %+v", got)
	}
	ctxB := ctxns.WithNamespace(context.Background(), "ns-b")
	if got, _ := st.GetNodeByID(ctxB, nodeIDB); got == nil {
		t.Error("expected ns-b node to survive")
	}

	var commACount, commBCount, flowACount, flowBCount int64
	db.Model(&model.Community{}).Where("namespace = ?", "ns-a").Count(&commACount)
	db.Model(&model.Community{}).Where("namespace = ?", "ns-b").Count(&commBCount)
	db.Model(&model.Flow{}).Where("namespace = ?", "ns-a").Count(&flowACount)
	db.Model(&model.Flow{}).Where("namespace = ?", "ns-b").Count(&flowBCount)
	if commACount != 0 {
		t.Errorf("expected ns-a community purged, got %d", commACount)
	}
	if commBCount != 1 {
		t.Errorf("expected ns-b community to survive, got %d", commBCount)
	}
	if flowACount != 0 {
		t.Errorf("expected ns-a flow purged, got %d", flowACount)
	}
	if flowBCount != 1 {
		t.Errorf("expected ns-b flow to survive, got %d", flowBCount)
	}

	var commAMemCount, flowAMemCount int64
	db.Model(&model.CommunityMembership{}).Where("community_id = ?", commAID).Count(&commAMemCount)
	db.Model(&model.FlowMembership{}).Where("flow_id = ?", flowAID).Count(&flowAMemCount)
	if commAMemCount != 0 {
		t.Errorf("expected ns-a community memberships purged, got %d", commAMemCount)
	}
	if flowAMemCount != 0 {
		t.Errorf("expected ns-a flow memberships purged, got %d", flowAMemCount)
	}

	if len(backend.calls) != 1 || backend.calls[0] != "ns-a" {
		t.Errorf("expected exactly one PurgeNamespace call for ns-a, got %v", backend.calls)
	}
	if backend.lastDB == nil || backend.lastDB == db {
		t.Error("expected search backend to receive the inner tx handle, not the outer db")
	}
}

func TestNamespacePurger_PurgesOrphanMembershipsByCommunityAndFlowID(t *testing.T) {
	db := newPurgerTestDB(t)
	st := gormstore.New(db)
	backend := &purgerSpyBackend{}

	commA := model.Community{Namespace: "ns-a", Key: "ns-a/core", Label: "ns-a/core", Strategy: "directory"}
	if err := db.Create(&commA).Error; err != nil {
		t.Fatalf("create community: %v", err)
	}
	flowA := model.Flow{Namespace: "ns-a", Name: "a-flow"}
	if err := db.Create(&flowA).Error; err != nil {
		t.Fatalf("create flow: %v", err)
	}
	if err := db.Create(&model.CommunityMembership{CommunityID: commA.ID, NodeID: 999}).Error; err != nil {
		t.Fatalf("create orphan comm membership: %v", err)
	}
	if err := db.Create(&model.FlowMembership{Namespace: ctxns.DefaultNamespace, FlowID: flowA.ID, NodeID: 888, Ordinal: 0}).Error; err != nil {
		t.Fatalf("create orphan flow membership: %v", err)
	}

	purger := NewNamespacePurger(st, db, backend)
	if err := purger.Purge(ctxns.WithNamespace(context.Background(), "ns-a")); err != nil {
		t.Fatalf("Purge: %v", err)
	}

	var commMem, flowMem int64
	db.Model(&model.CommunityMembership{}).Where("community_id = ?", commA.ID).Count(&commMem)
	db.Model(&model.FlowMembership{}).Where("flow_id = ?", flowA.ID).Count(&flowMem)
	if commMem != 0 {
		t.Errorf("expected orphan community memberships purged, got %d", commMem)
	}
	if flowMem != 0 {
		t.Errorf("expected orphan flow memberships purged, got %d", flowMem)
	}
}

func TestNamespacePurger_RollsBackWhenSearchPurgeFails(t *testing.T) {
	db := newPurgerTestDB(t)
	st := gormstore.New(db)
	backend := &purgerSpyBackend{purgeErr: errors.New("fts boom")}

	nodeIDA, _, commAID, flowAID := seedTwoNamespaces(t, db)

	purger := NewNamespacePurger(st, db, backend)
	err := purger.Purge(ctxns.WithNamespace(context.Background(), "ns-a"))
	if err == nil {
		t.Fatal("expected error when search backend purge fails")
	}

	ctxA := ctxns.WithNamespace(context.Background(), "ns-a")
	if got, getErr := st.GetNodeByID(ctxA, nodeIDA); getErr != nil || got == nil {
		t.Fatalf("expected ns-a node kept after rollback, node=%+v err=%v", got, getErr)
	}

	var commACount, flowACount, commAMemCount, flowAMemCount int64
	db.Model(&model.Community{}).Where("namespace = ?", "ns-a").Count(&commACount)
	db.Model(&model.Flow{}).Where("namespace = ?", "ns-a").Count(&flowACount)
	db.Model(&model.CommunityMembership{}).Where("community_id = ?", commAID).Count(&commAMemCount)
	db.Model(&model.FlowMembership{}).Where("flow_id = ?", flowAID).Count(&flowAMemCount)
	if commACount != 1 {
		t.Errorf("expected ns-a community kept after rollback, got %d", commACount)
	}
	if flowACount != 1 {
		t.Errorf("expected ns-a flow kept after rollback, got %d", flowACount)
	}
	if commAMemCount != 1 {
		t.Errorf("expected ns-a community membership kept after rollback, got %d", commAMemCount)
	}
	if flowAMemCount != 1 {
		t.Errorf("expected ns-a flow membership kept after rollback, got %d", flowAMemCount)
	}
}

func TestNamespacePurger_PropagatesGraphStoreError(t *testing.T) {
	db := newPurgerTestDB(t)
	st := gormstore.New(db)
	backend := &purgerSpyBackend{}

	purger := NewNamespacePurger(failingGraphStore{GraphStore: st, err: errors.New("graph boom")}, db, backend)
	err := purger.Purge(ctxns.WithNamespace(context.Background(), "ns-a"))
	if err == nil {
		t.Fatal("expected error from graph store")
	}
	if len(backend.calls) != 0 {
		t.Errorf("expected backend not invoked when graph delete fails, got %v", backend.calls)
	}
}

func TestNamespacePurger_FailsClosedWhenStoreLacksTransactionalDB(t *testing.T) {
	db := newPurgerTestDB(t)
	st := gormstore.New(db)
	backend := &purgerSpyBackend{}
	nodeIDA, _, _, _ := seedTwoNamespaces(t, db)

	purger := NewNamespacePurger(nonTransactionalNodeWriter{NodeWriter: st}, db, backend)
	err := purger.Purge(ctxns.WithNamespace(context.Background(), "ns-a"))
	if err == nil {
		t.Fatal("expected error when graph store lacks DB transaction handle")
	}
	if len(backend.calls) != 0 {
		t.Errorf("expected backend not invoked without transaction support, got %v", backend.calls)
	}
	ctxA := ctxns.WithNamespace(context.Background(), "ns-a")
	if got, getErr := st.GetNodeByID(ctxA, nodeIDA); getErr != nil || got == nil {
		t.Fatalf("expected ns-a node preserved, node=%+v err=%v", got, getErr)
	}
}

func TestNamespacePurger_NilSearchBackendIsAllowed(t *testing.T) {
	db := newPurgerTestDB(t)
	st := gormstore.New(db)

	purger := NewNamespacePurger(st, db, nil)
	if err := purger.Purge(ctxns.WithNamespace(context.Background(), "ns-a")); err != nil {
		t.Fatalf("Purge with nil backend: %v", err)
	}
}

func TestNamespacePurger_NilDBSkipsServiceTablesWithoutError(t *testing.T) {
	db := newPurgerTestDB(t)
	st := gormstore.New(db)
	purger := NewNamespacePurger(st, nil, nil)
	if err := purger.Purge(ctxns.WithNamespace(context.Background(), "ns-a")); err != nil {
		t.Fatalf("Purge with nil DB: %v", err)
	}
}
