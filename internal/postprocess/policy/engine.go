package policy

import (
	"context"
	"encoding/json"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/tae2089/code-context-graph/internal/ctxns"
	"github.com/tae2089/code-context-graph/internal/model"
	"github.com/tae2089/trace"
)

const (
	PolicyDegraded   = "degraded"
	PolicyFailClosed = "fail_closed"

	SourceAuto     = "auto"
	SourceExplicit = "explicit"

	StatusOK       = "ok"
	StatusDegraded = "degraded"

	ToolBuildOrUpdateGraph = "build_or_update_graph"
	ToolRunPostprocess     = "run_postprocess"
)

type DecisionInput struct {
	Tool           string
	ExplicitPolicy string
}

type RunRecord struct {
	Tool         string
	Policy       string
	Source       string
	Status       string
	FailedSteps  []string
	SkippedSteps []string
	CreatedAt    time.Time
}

type Engine struct{}

type Store struct {
	db *gorm.DB
}

func NewStore(db *gorm.DB) *Store {
	return &Store{db: db}
}

func (e *Engine) Resolve(ctx context.Context, store *Store, input DecisionInput) (string, string, error) {
	if input.ExplicitPolicy != "" {
		return input.ExplicitPolicy, SourceExplicit, nil
	}
	if store == nil {
		return PolicyDegraded, SourceAuto, nil
	}
	count, err := store.ConsecutiveFailures(ctx, input.Tool, 3)
	if err != nil {
		return "", "", err
	}
	if count >= 3 {
		return PolicyFailClosed, SourceAuto, nil
	}
	return PolicyDegraded, SourceAuto, nil
}

func (s *Store) GetState(ctx context.Context, tool string) (*model.PostprocessPolicyState, error) {
	var state model.PostprocessPolicyState
	ns := ctxns.FromContext(ctx)
	err := s.db.WithContext(ctx).Where("namespace = ? AND tool = ?", ns, tool).First(&state).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, trace.Wrap(err, "get postprocess policy state")
	}
	return &state, nil
}

func (s *Store) RecordRun(ctx context.Context, record RunRecord) error {
	ns := ctxns.FromContext(ctx)
	createdAt := record.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	failedJSON, err := marshalStringSlice(record.FailedSteps)
	if err != nil {
		return err
	}
	skippedJSON, err := marshalStringSlice(record.SkippedSteps)
	if err != nil {
		return err
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		log := &model.PostprocessRunLog{
			Namespace:    ns,
			Tool:         record.Tool,
			Policy:       record.Policy,
			Source:       record.Source,
			Status:       record.Status,
			FailedSteps:  failedJSON,
			SkippedSteps: skippedJSON,
			CreatedAt:    createdAt,
		}
		if err := tx.Create(log).Error; err != nil {
			return trace.Wrap(err, "create postprocess run log")
		}
		state := &model.PostprocessPolicyState{
			Namespace: ns,
			Tool:      record.Tool,
			Policy:    record.Policy,
			UpdatedAt: createdAt,
		}
		if err := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "namespace"}, {Name: "tool"}},
			DoUpdates: clause.AssignmentColumns([]string{"policy", "updated_at"}),
		}).Create(state).Error; err != nil {
			return trace.Wrap(err, "upsert postprocess policy state")
		}
		return nil
	})
}

func (s *Store) ConsecutiveFailures(ctx context.Context, tool string, limit int) (int, error) {
	if limit <= 0 {
		return 0, nil
	}
	ns := ctxns.FromContext(ctx)
	var logs []model.PostprocessRunLog
	if err := s.db.WithContext(ctx).
		Where("namespace = ? AND tool = ?", ns, tool).
		Order("created_at desc").
		Limit(limit).
		Find(&logs).Error; err != nil {
		return 0, trace.Wrap(err, "list postprocess run logs")
	}
	count := 0
	for _, log := range logs {
		if log.Status == StatusOK {
			break
		}
		count++
	}
	return count, nil
}

func marshalStringSlice(values []string) (string, error) {
	if len(values) == 0 {
		return "[]", nil
	}
	raw, err := json.Marshal(values)
	if err != nil {
		return "", trace.Wrap(err, "marshal string slice")
	}
	return string(raw), nil
}
