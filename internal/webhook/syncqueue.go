// @index Deduplicating work queue for webhook-driven repository sync.
package webhook

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"github.com/tae2089/code-context-graph/internal/obs"
)

var ErrSyncQueueFull = errors.New("sync queue full")

// @intent mark sync failures that should stop retry backoff immediately.
type nonRetryableError struct {
	err error
}

// @intent expose the wrapped failure message so non-retryable errors print like the underlying error.
func (e nonRetryableError) Error() string {
	return e.err.Error()
}

// @intent let errors.Is and errors.As reach the underlying cause through the non-retryable wrapper.
func (e nonRetryableError) Unwrap() error {
	return e.err
}

// @intent wrap permanent sync failures so queue retry logic can short-circuit them.
func NonRetryable(err error) error {
	if err == nil {
		return nil
	}
	return nonRetryableError{err: err}
}

// IsNonRetryable reports whether a sync error should bypass queue retries.
// @intent let retry logic stop early when a failure is known to be permanent for the current payload.
func IsNonRetryable(err error) bool {
	var target nonRetryableError
	return errors.As(err, &target)
}

// RetryConfig configures exponential backoff retry for sync handlers.
// @intent tune retry backoff so transient webhook sync failures can recover without overwhelming the remote.
type RetryConfig struct {
	// MaxAttempts is the total number of attempts (1 = no retry). Default: 3.
	MaxAttempts int
	// BaseDelay is the initial backoff duration. Default: 1s.
	BaseDelay time.Duration
	// MaxDelay caps the backoff duration. Default: 30s.
	MaxDelay time.Duration
}

// @intent provide conservative retry defaults for production webhook processing.
func defaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxAttempts: 3,
		BaseDelay:   1 * time.Second,
		MaxDelay:    30 * time.Second,
	}
}

// @intent capture the most recent sync request data per repository while it waits in the queue.
type syncPayload struct {
	ctx          context.Context
	repoFullName string
	cloneURL     string
	branch       string
}

// @intent expose per-repository queue state so operators can inspect backlog and failure hotspots.
type RepoStats struct {
	Repo            string    `json:"repo"`
	Branch          string    `json:"branch"`
	Queued          bool      `json:"queued"`
	Processing      bool      `json:"processing"`
	EnqueuedAt      time.Time `json:"enqueued_at,omitempty"`
	ProcessingAt    time.Time `json:"processing_at,omitempty"`
	LastSuccessTime time.Time `json:"last_success_time,omitempty"`
	LastErrorTime   time.Time `json:"last_error_time,omitempty"`
	LastError       string    `json:"last_error,omitempty"`
}

// @intent cap how many recent repositories are reported in queue stats to bound response size.
const maxRecentRepos = 50

// @intent coordinate deduplicated per-repository sync execution across a worker pool.
type SyncQueue struct {
	ctx             context.Context
	handler         SyncHandlerFunc
	retryConfig     RetryConfig
	maxTrackedRepos int
	mu              sync.Mutex
	queue           []string
	dirty           map[string]bool
	processing      map[string]bool
	payloads        map[string]syncPayload
	queueFullTotal  int64
	failureTotal    int64
	lastError       string
	lastErrorTime   time.Time
	enqueuedAt      map[string]time.Time
	processingAt    map[string]time.Time
	lastSuccessTime time.Time
	repoStats       map[string]*repoStatEntry
	repoStatsOrder  []string
	cond            *sync.Cond
	shutdown        bool
	wg              sync.WaitGroup
}

// @intent retain the latest success and failure outcome for one repository inside the bounded MRU stats map.
type repoStatEntry struct {
	branch          string
	lastSuccessTime time.Time
	lastErrorTime   time.Time
	lastError       string
}

// @intent summarize queue-wide health and recent repository activity for observability.
type SyncQueueStats struct {
	Queued              int           `json:"queued"`
	Dirty               int           `json:"dirty"`
	Processing          int           `json:"processing"`
	TrackedRepos        int           `json:"tracked_repos"`
	MaxTrackedRepos     int           `json:"max_tracked_repos"`
	QueueFullTotal      int64         `json:"queue_full_total"`
	FailureTotal        int64         `json:"failure_total"`
	LastError           string        `json:"last_error,omitempty"`
	LastErrorTime       time.Time     `json:"last_error_time,omitempty"`
	OldestQueuedAge     time.Duration `json:"oldest_queued_age"`
	OldestProcessingAge time.Duration `json:"oldest_processing_age"`
	LastSuccessTime     time.Time     `json:"last_success_time,omitempty"`
	Shutdown            bool          `json:"shutdown"`
	RecentRepos         []RepoStats   `json:"recent_repos,omitempty"`
}

// NewSyncQueue creates a queue with background workers and default lifecycle settings.
// @intent provide the smallest constructor for production webhook dispatch.
func NewSyncQueue(workers int, handler SyncHandlerFunc) *SyncQueue {
	return NewSyncQueueWithContext(context.Background(), workers, handler)
}

// NewSyncQueueWithContext binds queue workers to a parent lifecycle context.
// @intent allow server shutdown to cancel retries and worker waits cleanly.
func NewSyncQueueWithContext(ctx context.Context, workers int, handler SyncHandlerFunc) *SyncQueue {
	return NewSyncQueueWithOptions(ctx, workers, handler, defaultRetryConfig())
}

// @intent configure queue retry policy and memory bounds when constructing a SyncQueue.
type QueueConfig struct {
	RetryConfig
	MaxTrackedRepos int
}

// NewSyncQueueWithOptions applies retry tuning while keeping default queue limits.
// @intent expose backoff customization without forcing every caller to build a full queue config.
func NewSyncQueueWithOptions(ctx context.Context, workers int, handler SyncHandlerFunc, retry RetryConfig) *SyncQueue {
	return NewSyncQueueWithConfig(ctx, workers, handler, QueueConfig{RetryConfig: retry, MaxTrackedRepos: 1024})
}

// NewSyncQueueWithConfig creates a deduplicating repo work queue and starts its workers.
// @intent coalesce bursty webhook pushes per repository while still allowing different repos to sync concurrently.
func NewSyncQueueWithConfig(ctx context.Context, workers int, handler SyncHandlerFunc, cfg QueueConfig) *SyncQueue {
	if ctx == nil {
		ctx = context.Background()
	}
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = 1
	}
	if cfg.MaxTrackedRepos <= 0 {
		cfg.MaxTrackedRepos = 1024
	}
	q := &SyncQueue{
		ctx:             ctx,
		handler:         handler,
		retryConfig:     cfg.RetryConfig,
		maxTrackedRepos: cfg.MaxTrackedRepos,
		dirty:           make(map[string]bool),
		processing:      make(map[string]bool),
		payloads:        make(map[string]syncPayload),
		enqueuedAt:      make(map[string]time.Time),
		processingAt:    make(map[string]time.Time),
		repoStats:       make(map[string]*repoStatEntry),
	}
	q.cond = sync.NewCond(&q.mu)

	q.wg.Add(workers)
	for i := 0; i < workers; i++ {
		go q.worker()
	}

	return q
}

// Add records the latest payload for a repository and enqueues it if needed.
// @intent collapse repeated push events into one queued sync while preserving the newest branch and clone data.
// @mutates SyncQueue.queue, SyncQueue.dirty, SyncQueue.payloads.
func (q *SyncQueue) Add(ctx context.Context, repoFullName, cloneURL, branch string) error {
	if ctx != nil {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
	}
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.shutdown {
		return nil
	}

	if ctx == nil {
		ctx = context.Background()
	}
	if _, exists := q.payloads[repoFullName]; !exists && len(q.payloads) >= q.maxTrackedRepos {
		q.queueFullTotal++
		return ErrSyncQueueFull
	}
	q.payloads[repoFullName] = syncPayload{ctx: ctx, repoFullName: repoFullName, cloneURL: cloneURL, branch: branch}

	now := time.Now()
	if q.dirty[repoFullName] {
		return nil
	}

	q.dirty[repoFullName] = true
	q.enqueuedAt[repoFullName] = now

	if !q.processing[repoFullName] {
		q.queue = append(q.queue, repoFullName)
		q.cond.Signal()
	}
	return nil
}

// Shutdown stops accepting new work and waits for workers to finish or time out.
// @intent give the server a bounded, graceful shutdown path for in-flight webhook sync.
func (q *SyncQueue) Shutdown() {
	q.mu.Lock()
	q.shutdown = true
	q.cond.Broadcast()
	q.mu.Unlock()

	done := make(chan struct{})
	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("sync queue shutdown panicked", "panic", r)
			}
		}()
		q.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(30 * time.Second):
		slog.Error("sync queue shutdown timed out after 30s, abandoning workers")
		q.recordFailure("shutdown", "", errors.New("sync queue shutdown timed out after 30s"))
	}
}

// Stats snapshots queue health and recent repository activity for operators.
// @intent expose enough queue state to diagnose backlog, failures, and hot repositories.
func (q *SyncQueue) Stats() SyncQueueStats {
	q.mu.Lock()
	defer q.mu.Unlock()
	now := time.Now()
	return SyncQueueStats{
		Queued:              len(q.queue),
		Dirty:               len(q.dirty),
		Processing:          len(q.processing),
		TrackedRepos:        len(q.payloads),
		MaxTrackedRepos:     q.maxTrackedRepos,
		QueueFullTotal:      q.queueFullTotal,
		FailureTotal:        q.failureTotal,
		LastError:           q.lastError,
		LastErrorTime:       q.lastErrorTime,
		OldestQueuedAge:     q.oldestAgeLocked(now, q.enqueuedAt),
		OldestProcessingAge: q.oldestAgeLocked(now, q.processingAt),
		LastSuccessTime:     q.lastSuccessTime,
		Shutdown:            q.shutdown,
		RecentRepos:         q.buildRecentReposLocked(),
	}
}

// @intent assemble a bounded, activity-sorted snapshot of repositories recently seen by the queue.
func (q *SyncQueue) buildRecentReposLocked() []RepoStats {
	if len(q.repoStatsOrder) == 0 && len(q.payloads) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(q.repoStatsOrder)+len(q.payloads))
	repos := make([]string, 0, len(q.repoStatsOrder)+len(q.payloads))
	addRepo := func(repo string) {
		if _, ok := seen[repo]; ok || repo == "" {
			return
		}
		seen[repo] = struct{}{}
		repos = append(repos, repo)
	}
	for _, repo := range q.repoStatsOrder {
		addRepo(repo)
	}
	for repo := range q.payloads {
		addRepo(repo)
	}
	for repo := range q.dirty {
		addRepo(repo)
	}
	for repo := range q.processing {
		addRepo(repo)
	}

	result := make([]RepoStats, 0, len(repos))
	for _, repo := range repos {
		result = append(result, q.recentRepoStatsLocked(repo))
	}
	sort.Slice(result, func(i, j int) bool {
		left := recentRepoActivityTime(result[i])
		right := recentRepoActivityTime(result[j])
		if !left.Equal(right) {
			return left.After(right)
		}
		return result[i].Repo < result[j].Repo
	})
	if len(result) > maxRecentRepos {
		result = result[:maxRecentRepos]
	}
	return result
}

// @intent merge queued payload state with historical success and failure data for one repository summary.
func (q *SyncQueue) recentRepoStatsLocked(repo string) RepoStats {
	var entry *repoStatEntry
	if q.repoStats != nil {
		entry = q.repoStats[repo]
	}
	branch := ""
	if entry != nil {
		branch = entry.branch
	}
	if p, ok := q.payloads[repo]; ok && p.branch != "" {
		branch = p.branch
	}
	rs := RepoStats{
		Repo:       repo,
		Branch:     branch,
		Queued:     q.dirty[repo] && !q.processing[repo],
		Processing: q.processing[repo],
	}
	if entry != nil {
		rs.LastSuccessTime = entry.lastSuccessTime
		rs.LastErrorTime = entry.lastErrorTime
		rs.LastError = entry.lastError
	}
	if t, ok := q.enqueuedAt[repo]; ok {
		rs.EnqueuedAt = t
	}
	if t, ok := q.processingAt[repo]; ok {
		rs.ProcessingAt = t
	}
	return rs
}

// @intent derive a comparable activity timestamp for sorting repository summaries.
func recentRepoActivityTime(stats RepoStats) time.Time {
	latest := stats.EnqueuedAt
	if stats.ProcessingAt.After(latest) {
		latest = stats.ProcessingAt
	}
	if stats.LastSuccessTime.After(latest) {
		latest = stats.LastSuccessTime
	}
	if stats.LastErrorTime.After(latest) {
		latest = stats.LastErrorTime
	}
	return latest
}

// @intent surface the oldest outstanding queue age so operators can detect stuck work.
func (q *SyncQueue) oldestAgeLocked(now time.Time, times map[string]time.Time) time.Duration {
	var oldest time.Duration
	for _, started := range times {
		if started.IsZero() {
			continue
		}
		age := now.Sub(started)
		if age > oldest {
			oldest = age
		}
	}
	return oldest
}

// @intent run the main worker loop that drains deduplicated repository work items.
func (q *SyncQueue) worker() {
	defer q.wg.Done()

	for {
		repo, payload, ok := q.get()
		if !ok {
			return
		}
		processCtx, processSpan := obs.StartSpan(payload.ctx, "webhook.sync.queue.process", attribute.String("repo.full_name", repo))

		slog.InfoContext(processCtx, "sync queue processing", append(obs.TraceLogArgs(processCtx), "repo", repo)...)
		payload.ctx = processCtx
		success := q.safeHandle(repo, payload)
		processSpan.End()
		q.done(repo)
		if success {
			q.recordSuccess(repo, payload.branch)
		}
	}
}

// safeHandle runs the sync handler with retries and records terminal failures.
// @intent protect webhook processing from transient git and network errors without retrying permanent failures forever.
func (q *SyncQueue) safeHandle(repo string, payload syncPayload) bool {
	cfg := q.retryConfig
	delay := cfg.BaseDelay

	for attempt := 1; attempt <= cfg.MaxAttempts; attempt++ {
		err := q.tryHandle(repo, payload)
		if err == nil {
			return true
		}
		if IsNonRetryable(err) {
			slog.ErrorContext(payload.ctx, "sync handler failed with non-retryable error", append(obs.TraceLogArgs(payload.ctx), "repo", repo, "error", err)...)
			q.recordFailure(repo, payload.branch, err)
			return false
		}

		if attempt == cfg.MaxAttempts {
			slog.ErrorContext(payload.ctx, "sync handler failed, giving up", append(obs.TraceLogArgs(payload.ctx), "repo", repo, "attempts", attempt, "error", err)...)
			q.recordFailure(repo, payload.branch, err)
			return false
		}

		slog.WarnContext(payload.ctx, "sync handler failed, retrying", append(obs.TraceLogArgs(payload.ctx), "repo", repo, "attempt", attempt, "retryIn", delay, "error", err)...)

		select {
		case <-q.ctx.Done():
			slog.WarnContext(payload.ctx, "sync retry cancelled", append(obs.TraceLogArgs(payload.ctx), "repo", repo, "attempt", attempt)...)
			return false
		case <-payload.ctx.Done():
			slog.WarnContext(payload.ctx, "sync retry cancelled by payload context", append(obs.TraceLogArgs(payload.ctx), "repo", repo, "attempt", attempt)...)
			return false
		case <-time.After(delay):
		}

		delay *= 2
		if delay > cfg.MaxDelay {
			delay = cfg.MaxDelay
		}
	}
	return false
}

// @intent update queue-level and per-repository failure tracking after a terminal sync error.
func (q *SyncQueue) recordFailure(repo string, branch string, err error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.failureTotal++
	q.lastError = fmt.Sprintf("%s: %v", repo, err)
	q.lastErrorTime = time.Now()
	q.upsertRepoStatLocked(repo, branch, func(e *repoStatEntry) {
		e.lastError = err.Error()
		e.lastErrorTime = q.lastErrorTime
	})
}

// @intent update the latest successful sync timestamps after a repository finishes cleanly.
func (q *SyncQueue) recordSuccess(repo string, branch string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.lastSuccessTime = time.Now()
	q.upsertRepoStatLocked(repo, branch, func(e *repoStatEntry) {
		e.lastSuccessTime = q.lastSuccessTime
		e.lastError = ""
	})
}

// @intent maintain a bounded MRU view of repository stats without unbounded growth.
func (q *SyncQueue) upsertRepoStatLocked(repo string, branch string, update func(*repoStatEntry)) {
	entry, exists := q.repoStats[repo]
	if !exists {
		entry = &repoStatEntry{}
		q.repoStats[repo] = entry
		q.repoStatsOrder = append(q.repoStatsOrder, repo)
		if len(q.repoStatsOrder) > maxRecentRepos {
			oldest := q.repoStatsOrder[0]
			q.repoStatsOrder = q.repoStatsOrder[1:]
			delete(q.repoStats, oldest)
		}
	} else {
		for i, r := range q.repoStatsOrder {
			if r == repo {
				q.repoStatsOrder = append(q.repoStatsOrder[:i], q.repoStatsOrder[i+1:]...)
				break
			}
		}
		q.repoStatsOrder = append(q.repoStatsOrder, repo)
	}
	if branch != "" {
		entry.branch = branch
	}
	update(entry)
}

// @intent isolate handler panics and merged cancellation logic around one sync attempt.
func (q *SyncQueue) tryHandle(repo string, payload syncPayload) (err error) {
	ctx, cancel := mergeContexts(q.ctx, payload.ctx)
	ctx, span := obs.StartChildSpan(ctx, "webhook.sync.attempt", attribute.String("repo.full_name", repo), attribute.String("git.branch", payload.branch))
	defer cancel()
	defer span.End()
	defer func() {
		if r := recover(); r != nil {
			slog.ErrorContext(ctx, "sync handler panicked", append(obs.TraceLogArgs(ctx), "repo", repo, "panic", r)...)
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	return q.handler(ctx, payload.repoFullName, payload.cloneURL, payload.branch)
}

// @intent block workers until the next deduplicated repository payload is ready for processing.
func (q *SyncQueue) get() (string, syncPayload, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()

	for len(q.queue) == 0 {
		if q.shutdown {
			return "", syncPayload{}, false
		}
		q.cond.Wait()
	}

	repo := q.queue[0]
	q.queue = q.queue[1:]
	q.processing[repo] = true
	q.processingAt[repo] = time.Now()
	delete(q.dirty, repo)
	delete(q.enqueuedAt, repo)

	payload := q.payloads[repo]
	return repo, payload, true
}

// @intent requeue repositories that changed during processing or release payload state when work is complete.
func (q *SyncQueue) done(repo string) {
	q.mu.Lock()
	defer q.mu.Unlock()

	delete(q.processing, repo)
	delete(q.processingAt, repo)

	if q.dirty[repo] {
		q.queue = append(q.queue, repo)
		if q.enqueuedAt[repo].IsZero() {
			q.enqueuedAt[repo] = time.Now()
		}
		q.cond.Signal()
	} else {
		delete(q.payloads, repo)
	}
}

// @intent cancel a sync attempt when either the queue lifecycle or the payload-specific context is done.
func mergeContexts(queueCtx, payloadCtx context.Context) (context.Context, context.CancelFunc) {
	if payloadCtx == nil {
		payloadCtx = context.Background()
	}
	merged, cancel := context.WithCancel(payloadCtx)
	go func() {
		select {
		case <-queueCtx.Done():
			cancel()
		case <-payloadCtx.Done():
			cancel()
		case <-merged.Done():
		}
	}()
	return merged, cancel
}
