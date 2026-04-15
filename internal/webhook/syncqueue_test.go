package webhook

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSyncQueue_DeduplicatesRapidPushes(t *testing.T) {
	var callCount atomic.Int32
	done := make(chan struct{})

	handler := func(_ context.Context, repoFullName, cloneURL string) {
		callCount.Add(1)
		time.Sleep(50 * time.Millisecond)
		if callCount.Load() == 1 {
			close(done)
		}
	}

	q := NewSyncQueue(2, handler)
	defer q.Shutdown()

	q.Add(context.Background(), "org/svc", "https://github.com/org/svc.git")
	q.Add(context.Background(), "org/svc", "https://github.com/org/svc.git")
	q.Add(context.Background(), "org/svc", "https://github.com/org/svc.git")

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for handler")
	}

	time.Sleep(100 * time.Millisecond)

	got := callCount.Load()
	if got != 1 {
		t.Errorf("handler called %d times, want 1 (dedup failed)", got)
	}
}

func TestSyncQueue_MultiRepoConcurrent(t *testing.T) {
	var mu sync.Mutex
	processing := make(map[string]bool)
	concurrent := false

	var wg sync.WaitGroup
	wg.Add(2)

	handler := func(_ context.Context, repoFullName, cloneURL string) {
		mu.Lock()
		processing[repoFullName] = true
		if len(processing) > 1 {
			concurrent = true
		}
		mu.Unlock()

		time.Sleep(100 * time.Millisecond)

		mu.Lock()
		delete(processing, repoFullName)
		mu.Unlock()
		wg.Done()
	}

	q := NewSyncQueue(2, handler)
	defer q.Shutdown()

	q.Add(context.Background(), "org/alpha", "url-a")
	q.Add(context.Background(), "org/beta", "url-b")

	wg.Wait()

	if !concurrent {
		t.Error("expected different repos to be processed concurrently")
	}
}

func TestSyncQueue_RequeuesOnDirtyDuringProcessing(t *testing.T) {
	var callCount atomic.Int32
	calls := make(chan string, 10)

	handler := func(_ context.Context, repoFullName, cloneURL string) {
		n := callCount.Add(1)
		calls <- cloneURL
		if n == 1 {
			time.Sleep(100 * time.Millisecond)
		}
	}

	q := NewSyncQueue(1, handler)
	defer q.Shutdown()

	q.Add(context.Background(), "org/svc", "url-v1")

	time.Sleep(20 * time.Millisecond)

	q.Add(context.Background(), "org/svc", "url-v2")

	timeout := time.After(5 * time.Second)
	var urls []string
	for {
		select {
		case u := <-calls:
			urls = append(urls, u)
			if len(urls) == 2 {
				goto done
			}
		case <-timeout:
			t.Fatalf("timed out, got %d calls: %v", len(urls), urls)
		}
	}
done:

	if urls[1] != "url-v2" {
		t.Errorf("second call got cloneURL=%q, want %q", urls[1], "url-v2")
	}
}

func TestSyncQueue_ShutdownDrainsWorkers(t *testing.T) {
	var completed atomic.Int32

	handler := func(_ context.Context, repoFullName, cloneURL string) {
		time.Sleep(50 * time.Millisecond)
		completed.Add(1)
	}

	q := NewSyncQueue(2, handler)

	q.Add(context.Background(), "org/a", "url-a")
	q.Add(context.Background(), "org/b", "url-b")

	time.Sleep(10 * time.Millisecond)
	q.Shutdown()

	got := completed.Load()
	if got != 2 {
		t.Errorf("completed=%d after Shutdown, want 2 (drain failed)", got)
	}
}

func TestSyncQueue_PayloadUpdatedToLatest(t *testing.T) {
	calls := make(chan string, 10)

	handler := func(_ context.Context, repoFullName, cloneURL string) {
		calls <- cloneURL
	}

	q := NewSyncQueue(1, handler)
	defer q.Shutdown()

	q.Add(context.Background(), "org/svc", "url-v1")
	q.Add(context.Background(), "org/svc", "url-v2")
	q.Add(context.Background(), "org/svc", "url-v3")

	select {
	case got := <-calls:
		if got != "url-v3" {
			t.Errorf("handler got cloneURL=%q, want %q (latest payload)", got, "url-v3")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out")
	}
}

func TestSyncQueue_ContextCancelStopsHandler(t *testing.T) {
	handlerStarted := make(chan struct{})
	handlerDone := make(chan struct{})
	var handlerErr error

	handler := func(ctx context.Context, repoFullName, cloneURL string) {
		close(handlerStarted)
		select {
		case <-ctx.Done():
			handlerErr = ctx.Err()
		case <-time.After(10 * time.Second):
		}
		close(handlerDone)
	}

	// Given: queue with cancellable parent context
	parentCtx, cancel := context.WithCancel(context.Background())
	q := NewSyncQueueWithContext(parentCtx, 1, handler)

	q.Add(context.Background(), "org/svc", "https://example.com/org/svc.git")

	select {
	case <-handlerStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for handler to start")
	}

	// When: parent context is cancelled
	cancel()

	// Then: handler receives cancellation via its ctx
	select {
	case <-handlerDone:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for handler to respond to context cancel")
	}

	if handlerErr != context.Canceled {
		t.Errorf("handler ctx.Err() = %v, want context.Canceled", handlerErr)
	}

	q.Shutdown()
}

func TestSyncQueue_ContextCancelDrainsQueue(t *testing.T) {
	var callCount atomic.Int32

	handler := func(ctx context.Context, repoFullName, cloneURL string) {
		callCount.Add(1)
		<-ctx.Done()
	}

	// Given: queue with one item processing and one queued
	parentCtx, cancel := context.WithCancel(context.Background())
	q := NewSyncQueueWithContext(parentCtx, 1, handler)

	q.Add(context.Background(), "org/a", "url-a")
	time.Sleep(20 * time.Millisecond)
	q.Add(context.Background(), "org/b", "url-b")

	// When: parent context is cancelled
	cancel()

	// Then: queue drains without hanging
	q.Shutdown()

	got := callCount.Load()
	if got < 1 {
		t.Errorf("expected at least 1 handler call, got %d", got)
	}
}
