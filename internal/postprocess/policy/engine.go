package policy

import (
	"context"
	"encoding/json"
	"sort"
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
	SourceReset    = "reset"

	StatusOK       = "ok"
	StatusDegraded = "degraded"

	ToolBuildOrUpdateGraph = "build_or_update_graph"
	ToolRunPostprocess     = "run_postprocess"

	DefaultRunLogRetention = 200
	DefaultStatusLimit     = 5
)

type StatusOptions struct {
	Namespace   string
	Tool        string
	RecentLimit int
}

type StateSnapshot struct {
	Namespace           string    `json:"namespace"`
	Tool                string    `json:"tool"`
	Policy              string    `json:"policy"`
	UpdatedAt           time.Time `json:"updated_at"`
	ConsecutiveFailures int       `json:"consecutive_failures"`
}

type RunSnapshot struct {
	Namespace    string    `json:"namespace"`
	Tool         string    `json:"tool"`
	Policy       string    `json:"policy"`
	Source       string    `json:"source"`
	Status       string    `json:"status"`
	FailedSteps  []string  `json:"failed_steps"`
	SkippedSteps []string  `json:"skipped_steps"`
	CreatedAt    time.Time `json:"created_at"`
}

type StatusSummary struct {
	Status         string          `json:"status"`
	FailClosed     []StateSnapshot `json:"fail_closed,omitempty"`
	RecentFailures []RunSnapshot   `json:"recent_failures,omitempty"`
}

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
	db              *gorm.DB
	runLogRetention int
}

func NewStore(db *gorm.DB) *Store {
	return &Store{db: db, runLogRetention: DefaultRunLogRetention}
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
		if err := s.pruneRunLogs(tx, ns, record.Tool); err != nil {
			return err
		}
		return nil
	})
}

func (s *Store) Reset(ctx context.Context, tool string) error {
	if !ValidTool(tool) {
		return trace.New("invalid postprocess tool")
	}
	return s.RecordRun(ctx, RunRecord{
		Tool:      tool,
		Policy:    PolicyDegraded,
		Source:    SourceReset,
		Status:    StatusOK,
		CreatedAt: time.Now().UTC(),
	})
}

func (s *Store) Status(ctx context.Context, opts StatusOptions) (*StatusSummary, error) {
	limit := opts.RecentLimit
	if limit <= 0 {
		limit = DefaultStatusLimit
	}
	states, err := s.listStates(ctx, opts.Namespace, opts.Tool)
	if err != nil {
		return nil, err
	}
	summary := &StatusSummary{Status: StatusOK}
	recentFailures := make([]RunSnapshot, 0, limit)
	for _, state := range states {
		count, err := s.consecutiveFailuresScoped(ctx, state.Namespace, state.Tool, limit)
		if err != nil {
			return nil, err
		}
		snapshot := StateSnapshot{
			Namespace:           state.Namespace,
			Tool:                state.Tool,
			Policy:              state.Policy,
			UpdatedAt:           state.UpdatedAt,
			ConsecutiveFailures: count,
		}
		if state.Policy == PolicyFailClosed {
			summary.FailClosed = append(summary.FailClosed, snapshot)
		}
		if count == 0 {
			continue
		}
		runs, err := s.listLatestFailedRuns(ctx, state.Namespace, state.Tool, count)
		if err != nil {
			return nil, err
		}
		for _, run := range runs {
			recentFailures = append(recentFailures, run)
		}
	}
	sort.Slice(summary.FailClosed, func(i, j int) bool {
		return summary.FailClosed[i].UpdatedAt.After(summary.FailClosed[j].UpdatedAt)
	})
	sort.Slice(recentFailures, func(i, j int) bool {
		if recentFailures[i].CreatedAt.Equal(recentFailures[j].CreatedAt) {
			if recentFailures[i].Namespace == recentFailures[j].Namespace {
				return recentFailures[i].Tool < recentFailures[j].Tool
			}
			return recentFailures[i].Namespace < recentFailures[j].Namespace
		}
		return recentFailures[i].CreatedAt.After(recentFailures[j].CreatedAt)
	})
	if len(recentFailures) > limit {
		recentFailures = recentFailures[:limit]
	}
	summary.RecentFailures = recentFailures
	if len(summary.FailClosed) > 0 || len(summary.RecentFailures) > 0 {
		summary.Status = StatusDegraded
	}
	return summary, nil
}

func (s *Store) ConsecutiveFailures(ctx context.Context, tool string, limit int) (int, error) {
	if limit <= 0 {
		return 0, nil
	}
	ns := ctxns.FromContext(ctx)
	return s.consecutiveFailuresScoped(ctx, ns, tool, limit)
}

func ValidTool(tool string) bool {
	switch tool {
	case ToolBuildOrUpdateGraph, ToolRunPostprocess:
		return true
	default:
		return false
	}
}

func (s *Store) consecutiveFailuresScoped(ctx context.Context, namespace, tool string, limit int) (int, error) {
	var logs []model.PostprocessRunLog
	if err := s.db.WithContext(ctx).
		Where("namespace = ? AND tool = ?", namespace, tool).
		Order("created_at desc").
		Order("id desc").
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

func (s *Store) listStates(ctx context.Context, namespace, tool string) ([]model.PostprocessPolicyState, error) {
	query := s.db.WithContext(ctx).Model(&model.PostprocessPolicyState{})
	if namespace != "" {
		query = query.Where("namespace = ?", namespace)
	}
	if tool != "" {
		query = query.Where("tool = ?", tool)
	}
	var states []model.PostprocessPolicyState
	if err := query.Order("updated_at desc").Find(&states).Error; err != nil {
		return nil, trace.Wrap(err, "list postprocess policy states")
	}
	return states, nil
}

func (s *Store) listLatestFailedRuns(ctx context.Context, namespace, tool string, limit int) ([]RunSnapshot, error) {
	if limit <= 0 {
		return nil, nil
	}
	var logs []model.PostprocessRunLog
	if err := s.db.WithContext(ctx).
		Where("namespace = ? AND tool = ? AND status <> ?", namespace, tool, StatusOK).
		Order("created_at desc").
		Order("id desc").
		Limit(limit).
		Find(&logs).Error; err != nil {
		return nil, trace.Wrap(err, "list latest failed postprocess runs")
	}
	runs := make([]RunSnapshot, 0, len(logs))
	for _, log := range logs {
		failed, err := unmarshalStringSlice(log.FailedSteps)
		if err != nil {
			return nil, err
		}
		skipped, err := unmarshalStringSlice(log.SkippedSteps)
		if err != nil {
			return nil, err
		}
		runs = append(runs, RunSnapshot{
			Namespace:    log.Namespace,
			Tool:         log.Tool,
			Policy:       log.Policy,
			Source:       log.Source,
			Status:       log.Status,
			FailedSteps:  failed,
			SkippedSteps: skipped,
			CreatedAt:    log.CreatedAt,
		})
	}
	return runs, nil
}

func (s *Store) pruneRunLogs(tx *gorm.DB, namespace, tool string) error {
	if s.runLogRetention <= 0 {
		return nil
	}
	for {
		var staleIDs []uint
		if err := tx.WithContext(context.Background()).
			Model(&model.PostprocessRunLog{}).
			Where("namespace = ? AND tool = ?", namespace, tool).
			Order("created_at desc").
			Order("id desc").
			Offset(s.runLogRetention).
			Limit(100).
			Pluck("id", &staleIDs).Error; err != nil {
			return trace.Wrap(err, "list stale postprocess run logs")
		}
		if len(staleIDs) == 0 {
			return nil
		}
		if err := tx.Where("id IN ?", staleIDs).Delete(&model.PostprocessRunLog{}).Error; err != nil {
			return trace.Wrap(err, "delete stale postprocess run logs")
		}
	}
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

func unmarshalStringSlice(raw string) ([]string, error) {
	if raw == "" {
		return nil, nil
	}
	var values []string
	if err := json.Unmarshal([]byte(raw), &values); err != nil {
		return nil, trace.Wrap(err, "unmarshal string slice")
	}
	return values, nil
}
