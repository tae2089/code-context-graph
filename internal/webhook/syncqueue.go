package webhook

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

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
	repoFullName string
	cloneURL     string
}

type SyncQueue struct {
	ctx         context.Context
	handler     SyncHandlerFunc
	retryConfig RetryConfig
	mu          sync.Mutex
	queue       []string
	dirty       map[string]bool
	processing  map[string]bool
	payloads    map[string]syncPayload
	cond        *sync.Cond
	shutdown    bool
	wg          sync.WaitGroup
}

func NewSyncQueue(workers int, handler SyncHandlerFunc) *SyncQueue {
	return NewSyncQueueWithContext(context.Background(), workers, handler)
}

func NewSyncQueueWithContext(ctx context.Context, workers int, handler SyncHandlerFunc) *SyncQueue {
	return NewSyncQueueWithOptions(ctx, workers, handler, defaultRetryConfig())
}

func NewSyncQueueWithOptions(ctx context.Context, workers int, handler SyncHandlerFunc, retry RetryConfig) *SyncQueue {
	if retry.MaxAttempts <= 0 {
		retry.MaxAttempts = 1
	}
	q := &SyncQueue{
		ctx:         ctx,
		handler:     handler,
		retryConfig: retry,
		dirty:       make(map[string]bool),
		processing:  make(map[string]bool),
		payloads:    make(map[string]syncPayload),
	}
	q.cond = sync.NewCond(&q.mu)

	q.wg.Add(workers)
	for i := 0; i < workers; i++ {
		go q.worker()
	}

	return q
}

func (q *SyncQueue) Add(_ context.Context, repoFullName, cloneURL string) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.shutdown {
		return
	}

	q.payloads[repoFullName] = syncPayload{repoFullName: repoFullName, cloneURL: cloneURL}

	if q.dirty[repoFullName] {
		return
	}

	q.dirty[repoFullName] = true

	if !q.processing[repoFullName] {
		q.queue = append(q.queue, repoFullName)
		q.cond.Signal()
	}
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
	}
}

func (q *SyncQueue) worker() {
	defer q.wg.Done()

	for {
		repo, payload, ok := q.get()
		if !ok {
			return
		}

		slog.Info("sync queue processing", "repo", repo)
		q.safeHandle(repo, payload)
		q.done(repo)
	}
}

func (q *SyncQueue) safeHandle(repo string, payload syncPayload) {
	cfg := q.retryConfig
	delay := cfg.BaseDelay

	for attempt := 1; attempt <= cfg.MaxAttempts; attempt++ {
		err := q.tryHandle(repo, payload)
		if err == nil {
			return
		}

		if attempt == cfg.MaxAttempts {
			slog.Error("sync handler failed, giving up", "repo", repo, "attempts", attempt, "error", err)
			return
		}

		slog.Warn("sync handler failed, retrying", "repo", repo, "attempt", attempt, "retryIn", delay, "error", err)

		select {
		case <-q.ctx.Done():
			slog.Warn("sync retry cancelled", "repo", repo, "attempt", attempt)
			return
		case <-time.After(delay):
		}

		delay *= 2
		if delay > cfg.MaxDelay {
			delay = cfg.MaxDelay
		}
	}
}

func (q *SyncQueue) tryHandle(repo string, payload syncPayload) (err error) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("sync handler panicked", "repo", repo, "panic", r)
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	return q.handler(q.ctx, payload.repoFullName, payload.cloneURL)
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
	delete(q.dirty, repo)

	payload := q.payloads[repo]
	return repo, payload, true
}

func (q *SyncQueue) done(repo string) {
	q.mu.Lock()
	defer q.mu.Unlock()

	delete(q.processing, repo)

	if q.dirty[repo] {
		q.queue = append(q.queue, repo)
		q.cond.Signal()
	} else {
		delete(q.payloads, repo)
	}
}
