package policy

import (
	"context"
	"fmt"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/tae2089/code-context-graph/internal/ctxns"
	"github.com/tae2089/code-context-graph/internal/model"
)

func TestEngineResolve_DefaultsToDegradedWithoutHistory(t *testing.T) {
	engine := &Engine{}
	policy, source, err := engine.Resolve(context.Background(), nil, DecisionInput{Tool: ToolBuildOrUpdateGraph})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if policy != PolicyDegraded {
		t.Fatalf("policy = %q, want %q", policy, PolicyDegraded)
	}
	if source != SourceAuto {
		t.Fatalf("source = %q, want %q", source, SourceAuto)
	}
}

func TestEngineResolve_ExplicitOverrideWins(t *testing.T) {
	engine := &Engine{}
	policy, source, err := engine.Resolve(context.Background(), nil, DecisionInput{
		Tool:           ToolBuildOrUpdateGraph,
		ExplicitPolicy: PolicyFailClosed,
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if policy != PolicyFailClosed {
		t.Fatalf("policy = %q, want %q", policy, PolicyFailClosed)
	}
	if source != SourceExplicit {
		t.Fatalf("source = %q, want %q", source, SourceExplicit)
	}
}

func TestEngineResolve_EscalatesAfterThreeConsecutiveFailures(t *testing.T) {
	store := setupPolicyStore(t)
	engine := &Engine{}
	ctx := ctxns.WithNamespace(context.Background(), "svc")

	for i := 0; i < 3; i++ {
		if err := store.RecordRun(ctx, RunRecord{
			Tool:        ToolBuildOrUpdateGraph,
			Policy:      PolicyDegraded,
			Source:      SourceAuto,
			Status:      StatusDegraded,
			FailedSteps: []string{"communities"},
			CreatedAt:   time.Unix(int64(i+1), 0),
		}); err != nil {
			t.Fatalf("record run %d: %v", i, err)
		}
	}

	policy, source, err := engine.Resolve(ctx, store, DecisionInput{Tool: ToolBuildOrUpdateGraph})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if policy != PolicyFailClosed {
		t.Fatalf("policy = %q, want %q", policy, PolicyFailClosed)
	}
	if source != SourceAuto {
		t.Fatalf("source = %q, want %q", source, SourceAuto)
	}
}

func TestStoreRecordRun_UpdatesStateAndResetsAfterSuccess(t *testing.T) {
	store := setupPolicyStore(t)
	ctx := ctxns.WithNamespace(context.Background(), "svc")

	for i := 0; i < 2; i++ {
		if err := store.RecordRun(ctx, RunRecord{
			Tool:        ToolRunPostprocess,
			Policy:      PolicyDegraded,
			Source:      SourceAuto,
			Status:      StatusDegraded,
			FailedSteps: []string{"fts"},
			CreatedAt:   time.Unix(int64(i+1), 0),
		}); err != nil {
			t.Fatalf("record failure %d: %v", i, err)
		}
	}

	count, err := store.ConsecutiveFailures(ctx, ToolRunPostprocess, 3)
	if err != nil {
		t.Fatalf("consecutive failures after failures: %v", err)
	}
	if count != 2 {
		t.Fatalf("count = %d, want 2", count)
	}

	if err := store.RecordRun(ctx, RunRecord{
		Tool:      ToolRunPostprocess,
		Policy:    PolicyDegraded,
		Source:    SourceAuto,
		Status:    StatusOK,
		CreatedAt: time.Unix(3, 0),
	}); err != nil {
		t.Fatalf("record success: %v", err)
	}

	count, err = store.ConsecutiveFailures(ctx, ToolRunPostprocess, 3)
	if err != nil {
		t.Fatalf("consecutive failures after success: %v", err)
	}
	if count != 0 {
		t.Fatalf("count = %d, want 0", count)
	}

	state, err := store.GetState(ctx, ToolRunPostprocess)
	if err != nil {
		t.Fatalf("get state: %v", err)
	}
	if state == nil {
		t.Fatal("expected state to exist")
	}
	if state.Policy != PolicyDegraded {
		t.Fatalf("state policy = %q, want %q", state.Policy, PolicyDegraded)
	}
	if state.Namespace != "svc" {
		t.Fatalf("state namespace = %q, want svc", state.Namespace)
	}
	if state.Tool != ToolRunPostprocess {
		t.Fatalf("state tool = %q, want %q", state.Tool, ToolRunPostprocess)
	}

	var logs []model.PostprocessRunLog
	if err := store.db.Order("created_at asc").Find(&logs).Error; err != nil {
		t.Fatalf("list logs: %v", err)
	}
	if len(logs) != 3 {
		t.Fatalf("logs = %d, want 3", len(logs))
	}
	if logs[0].FailedSteps != `["fts"]` {
		t.Fatalf("first failed_steps = %s, want %s", logs[0].FailedSteps, `["fts"]`)
	}
	if logs[2].FailedSteps != "[]" {
		t.Fatalf("success failed_steps = %s, want []", logs[2].FailedSteps)
	}
}

func TestStoreReset_InsertsResetMarkerAndClearsFailureStreak(t *testing.T) {
	store := setupPolicyStore(t)
	ctx := ctxns.WithNamespace(context.Background(), "svc")

	for i := 0; i < 3; i++ {
		if err := store.RecordRun(ctx, RunRecord{
			Tool:        ToolRunPostprocess,
			Policy:      PolicyFailClosed,
			Source:      SourceAuto,
			Status:      StatusDegraded,
			FailedSteps: []string{"fts"},
			CreatedAt:   time.Unix(int64(i+1), 0),
		}); err != nil {
			t.Fatalf("record failure %d: %v", i, err)
		}
	}

	if err := store.Reset(ctx, ToolRunPostprocess); err != nil {
		t.Fatalf("reset: %v", err)
	}

	count, err := store.ConsecutiveFailures(ctx, ToolRunPostprocess, 10)
	if err != nil {
		t.Fatalf("consecutive failures after reset: %v", err)
	}
	if count != 0 {
		t.Fatalf("count = %d, want 0", count)
	}

	state, err := store.GetState(ctx, ToolRunPostprocess)
	if err != nil {
		t.Fatalf("get state: %v", err)
	}
	if state == nil || state.Policy != PolicyDegraded {
		t.Fatalf("state = %+v, want degraded", state)
	}

	var latest model.PostprocessRunLog
	if err := store.db.Order("created_at desc").Order("id desc").First(&latest).Error; err != nil {
		t.Fatalf("latest run log: %v", err)
	}
	if latest.Source != SourceReset {
		t.Fatalf("latest source = %q, want %q", latest.Source, SourceReset)
	}
	if latest.Status != StatusOK {
		t.Fatalf("latest status = %q, want %q", latest.Status, StatusOK)
	}
	if latest.Policy != PolicyDegraded {
		t.Fatalf("latest policy = %q, want %q", latest.Policy, PolicyDegraded)
	}
}

func TestStoreRecordRun_PrunesOldLogsPerNamespaceAndTool(t *testing.T) {
	store := setupPolicyStore(t)
	store.runLogRetention = 2

	ctxA := ctxns.WithNamespace(context.Background(), "ns-a")
	ctxB := ctxns.WithNamespace(context.Background(), "ns-b")
	for i := 0; i < 4; i++ {
		if err := store.RecordRun(ctxA, RunRecord{
			Tool:      ToolRunPostprocess,
			Policy:    PolicyDegraded,
			Source:    SourceAuto,
			Status:    StatusDegraded,
			CreatedAt: time.Unix(int64(i+1), 0),
		}); err != nil {
			t.Fatalf("record ns-a run %d: %v", i, err)
		}
	}
	if err := store.RecordRun(ctxA, RunRecord{
		Tool:      ToolBuildOrUpdateGraph,
		Policy:    PolicyDegraded,
		Source:    SourceAuto,
		Status:    StatusOK,
		CreatedAt: time.Unix(10, 0),
	}); err != nil {
		t.Fatalf("record ns-a other tool: %v", err)
	}
	if err := store.RecordRun(ctxB, RunRecord{
		Tool:      ToolRunPostprocess,
		Policy:    PolicyDegraded,
		Source:    SourceAuto,
		Status:    StatusOK,
		CreatedAt: time.Unix(11, 0),
	}); err != nil {
		t.Fatalf("record ns-b run: %v", err)
	}

	var kept []model.PostprocessRunLog
	if err := store.db.Where("namespace = ? AND tool = ?", "ns-a", ToolRunPostprocess).Order("created_at asc").Find(&kept).Error; err != nil {
		t.Fatalf("list retained logs: %v", err)
	}
	if len(kept) != 2 {
		t.Fatalf("retained logs = %d, want 2", len(kept))
	}
	if !kept[0].CreatedAt.Equal(time.Unix(3, 0)) || !kept[1].CreatedAt.Equal(time.Unix(4, 0)) {
		t.Fatalf("unexpected retained timestamps: %+v", kept)
	}

	var otherToolCount int64
	if err := store.db.Model(&model.PostprocessRunLog{}).Where("namespace = ? AND tool = ?", "ns-a", ToolBuildOrUpdateGraph).Count(&otherToolCount).Error; err != nil {
		t.Fatalf("count other tool logs: %v", err)
	}
	if otherToolCount != 1 {
		t.Fatalf("other tool logs = %d, want 1", otherToolCount)
	}

	var otherNamespaceCount int64
	if err := store.db.Model(&model.PostprocessRunLog{}).Where("namespace = ? AND tool = ?", "ns-b", ToolRunPostprocess).Count(&otherNamespaceCount).Error; err != nil {
		t.Fatalf("count other namespace logs: %v", err)
	}
	if otherNamespaceCount != 1 {
		t.Fatalf("other namespace logs = %d, want 1", otherNamespaceCount)
	}
}

func TestStoreStatus_SummarizesFailClosedAndRecentFailures(t *testing.T) {
	store := setupPolicyStore(t)
	ctxA := ctxns.WithNamespace(context.Background(), "ns-a")
	ctxB := ctxns.WithNamespace(context.Background(), "ns-b")

	for i := 0; i < 4; i++ {
		if err := store.RecordRun(ctxA, RunRecord{
			Tool:        ToolRunPostprocess,
			Policy:      PolicyFailClosed,
			Source:      SourceAuto,
			Status:      StatusDegraded,
			FailedSteps: []string{"communities"},
			CreatedAt:   time.Unix(int64(i+1), 0),
		}); err != nil {
			t.Fatalf("record ns-a run %d: %v", i, err)
		}
	}
	if err := store.RecordRun(ctxB, RunRecord{
		Tool:        ToolBuildOrUpdateGraph,
		Policy:      PolicyDegraded,
		Source:      SourceAuto,
		Status:      StatusDegraded,
		FailedSteps: []string{"fts"},
		CreatedAt:   time.Unix(10, 0),
	}); err != nil {
		t.Fatalf("record ns-b run: %v", err)
	}

	summary, err := store.Status(context.Background(), StatusOptions{RecentLimit: 3})
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if summary.Status != StatusDegraded {
		t.Fatalf("summary status = %q, want %q", summary.Status, StatusDegraded)
	}
	if len(summary.FailClosed) != 1 {
		t.Fatalf("fail_closed entries = %d, want 1", len(summary.FailClosed))
	}
	if summary.FailClosed[0].Namespace != "ns-a" || summary.FailClosed[0].Tool != ToolRunPostprocess {
		t.Fatalf("unexpected fail_closed entry: %+v", summary.FailClosed[0])
	}
	if summary.FailClosed[0].ConsecutiveFailures != 3 {
		t.Fatalf("consecutive failures = %d, want 3", summary.FailClosed[0].ConsecutiveFailures)
	}
	if len(summary.RecentFailures) != 3 {
		t.Fatalf("recent failures = %d, want 3", len(summary.RecentFailures))
	}
	if summary.RecentFailures[0].Namespace != "ns-b" {
		t.Fatalf("latest recent failure namespace = %q, want ns-b", summary.RecentFailures[0].Namespace)
	}
}

func setupPolicyStore(t *testing.T) *Store {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.PostprocessPolicyState{}, &model.PostprocessRunLog{}); err != nil {
		t.Fatalf("migrate policy tables: %v", err)
	}
	return NewStore(db)
}
