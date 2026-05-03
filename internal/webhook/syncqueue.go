package webhook

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

var ErrSyncQueueFull = errors.New("sync queue full")

type nonRetryableError struct {
	err error
}

func (e nonRetryableError) Error() string {
	return e.err.Error()
}

func (e nonRetryableError) Unwrap() error {
	return e.err
}

func NonRetryable(err error) error {
	if err == nil {
		return nil
	}
	return nonRetryableError{err: err}
}

func IsNonRetryable(err error) bool {
	var target nonRetryableError
	return errors.As(err, &target)
}

// RetryConfig configures exponential backoff retry for sync handlers.
type RetryConfig struct {
	// MaxAttempts is the total number of attempts (1 = no retry). Default: 3.
	MaxAttempts int
	// BaseDelay is the initial backoff duration. Default: 1s.
	BaseDelay time.Duration
	// MaxDelay caps the backoff duration. Default: 30s.
	MaxDelay time.Duration
}

func defaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxAttempts: 3,
		BaseDelay:   1 * time.Second,
		MaxDelay:    30 * time.Second,
	}
}

type syncPayload struct {
	ctx          context.Context
	repoFullName string
	cloneURL     string
	branch       string
}

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
	cond            *sync.Cond
	shutdown        bool
	wg              sync.WaitGroup
}

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
}

func NewSyncQueue(workers int, handler SyncHandlerFunc) *SyncQueue {
	return NewSyncQueueWithContext(context.Background(), workers, handler)
}

func NewSyncQueueWithContext(ctx context.Context, workers int, handler SyncHandlerFunc) *SyncQueue {
	return NewSyncQueueWithOptions(ctx, workers, handler, defaultRetryConfig())
}

type QueueConfig struct {
	RetryConfig
	MaxTrackedRepos int
}

func NewSyncQueueWithOptions(ctx context.Context, workers int, handler SyncHandlerFunc, retry RetryConfig) *SyncQueue {
	return NewSyncQueueWithConfig(ctx, workers, handler, QueueConfig{RetryConfig: retry, MaxTrackedRepos: 1024})
}

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
	}
	q.cond = sync.NewCond(&q.mu)

	q.wg.Add(workers)
	for i := 0; i < workers; i++ {
		go q.worker()
	}

	return q
}

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
		q.recordFailure("shutdown", errors.New("sync queue shutdown timed out after 30s"))
	}
}

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
	}
}

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

func (q *SyncQueue) worker() {
	defer q.wg.Done()

	for {
		repo, payload, ok := q.get()
		if !ok {
			return
		}

		slog.Info("sync queue processing", "repo", repo)
		success := q.safeHandle(repo, payload)
		q.done(repo)
		if success {
			q.recordSuccess()
		}
	}
}

func (q *SyncQueue) safeHandle(repo string, payload syncPayload) bool {
	cfg := q.retryConfig
	delay := cfg.BaseDelay

	for attempt := 1; attempt <= cfg.MaxAttempts; attempt++ {
		err := q.tryHandle(repo, payload)
		if err == nil {
			return true
		}
		if IsNonRetryable(err) {
			slog.Error("sync handler failed with non-retryable error", "repo", repo, "error", err)
			q.recordFailure(repo, err)
			return false
		}

		if attempt == cfg.MaxAttempts {
			slog.Error("sync handler failed, giving up", "repo", repo, "attempts", attempt, "error", err)
			q.recordFailure(repo, err)
			return false
		}

		slog.Warn("sync handler failed, retrying", "repo", repo, "attempt", attempt, "retryIn", delay, "error", err)

		select {
		case <-q.ctx.Done():
			slog.Warn("sync retry cancelled", "repo", repo, "attempt", attempt)
			return false
		case <-payload.ctx.Done():
			slog.Warn("sync retry cancelled by payload context", "repo", repo, "attempt", attempt)
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

func (q *SyncQueue) recordFailure(repo string, err error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.failureTotal++
	q.lastError = fmt.Sprintf("%s: %v", repo, err)
	q.lastErrorTime = time.Now()
}

func (q *SyncQueue) recordSuccess() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.lastSuccessTime = time.Now()
}

func (q *SyncQueue) tryHandle(repo string, payload syncPayload) (err error) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("sync handler panicked", "repo", repo, "panic", r)
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	ctx, cancel := mergeContexts(q.ctx, payload.ctx)
	defer cancel()
	return q.handler(ctx, payload.repoFullName, payload.cloneURL, payload.branch)
}

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
