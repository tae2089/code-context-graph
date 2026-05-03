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
