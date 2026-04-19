package webhook

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

type syncPayload struct {
	repoFullName string
	cloneURL     string
}

type SyncQueue struct {
	ctx        context.Context
	handler    SyncFunc
	mu         sync.Mutex
	queue      []string
	dirty      map[string]bool
	processing map[string]bool
	payloads   map[string]syncPayload
	cond       *sync.Cond
	shutdown   bool
	wg         sync.WaitGroup
}

func NewSyncQueue(workers int, handler SyncFunc) *SyncQueue {
	return NewSyncQueueWithContext(context.Background(), workers, handler)
}

func NewSyncQueueWithContext(ctx context.Context, workers int, handler SyncFunc) *SyncQueue {
	q := &SyncQueue{
		ctx:        ctx,
		handler:    handler,
		dirty:      make(map[string]bool),
		processing: make(map[string]bool),
		payloads:   make(map[string]syncPayload),
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
	defer func() {
		if r := recover(); r != nil {
			slog.Error("sync handler panicked", "repo", repo, "panic", r)
		}
	}()
	q.handler(q.ctx, payload.repoFullName, payload.cloneURL)
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
