package webhook

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSyncQueue_DeduplicatesRapidPushes(t *testing.T) {
	var callCount atomic.Int32
	done := make(chan struct{})

	handler := func(_ context.Context, repoFullName, cloneURL, branch string) error {
		callCount.Add(1)
		time.Sleep(50 * time.Millisecond)
		if callCount.Load() == 1 {
			close(done)
		}
		return nil
	}

	q := NewSyncQueue(2, handler)
	defer q.Shutdown()

	q.Add(context.Background(), "org/svc", "https://github.com/org/svc.git", "main")
	q.Add(context.Background(), "org/svc", "https://github.com/org/svc.git", "main")
	q.Add(context.Background(), "org/svc", "https://github.com/org/svc.git", "main")

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

	handler := func(_ context.Context, repoFullName, cloneURL, branch string) error {
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
		return nil
	}

	q := NewSyncQueue(2, handler)
	defer q.Shutdown()

	q.Add(context.Background(), "org/alpha", "url-a", "main")
	q.Add(context.Background(), "org/beta", "url-b", "main")

	wg.Wait()

	if !concurrent {
		t.Error("expected different repos to be processed concurrently")
	}
}

func TestSyncQueue_RequeuesOnDirtyDuringProcessing(t *testing.T) {
	var callCount atomic.Int32
	calls := make(chan string, 10)

	handler := func(_ context.Context, repoFullName, cloneURL, branch string) error {
		n := callCount.Add(1)
		calls <- cloneURL
		if n == 1 {
			time.Sleep(100 * time.Millisecond)
		}
		return nil
	}

	q := NewSyncQueue(1, handler)
	defer q.Shutdown()

	q.Add(context.Background(), "org/svc", "url-v1", "main")

	time.Sleep(20 * time.Millisecond)

	q.Add(context.Background(), "org/svc", "url-v2", "develop")

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

	handler := func(_ context.Context, repoFullName, cloneURL, branch string) error {
		time.Sleep(50 * time.Millisecond)
		completed.Add(1)
		return nil
	}

	q := NewSyncQueue(2, handler)

	q.Add(context.Background(), "org/a", "url-a", "main")
	q.Add(context.Background(), "org/b", "url-b", "main")

	time.Sleep(10 * time.Millisecond)
	q.Shutdown()

	got := completed.Load()
	if got != 2 {
		t.Errorf("completed=%d after Shutdown, want 2 (drain failed)", got)
	}
}

func TestSyncQueue_PayloadUpdatedToLatest(t *testing.T) {
	calls := make(chan string, 10)

	handler := func(_ context.Context, repoFullName, cloneURL, branch string) error {
		calls <- cloneURL
		return nil
	}

	q := NewSyncQueue(1, handler)
	defer q.Shutdown()

	q.Add(context.Background(), "org/svc", "url-v1", "main")
	q.Add(context.Background(), "org/svc", "url-v2", "main")
	q.Add(context.Background(), "org/svc", "url-v3", "release")

	select {
	case got := <-calls:
		if got != "url-v3" {
			t.Errorf("handler got cloneURL=%q, want %q (latest payload)", got, "url-v3")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out")
	}
}

func TestSyncQueue_PassesLatestBranch(t *testing.T) {
	calls := make(chan string, 10)

	handler := func(_ context.Context, repoFullName, cloneURL, branch string) error {
		calls <- branch
		return nil
	}

	q := NewSyncQueue(1, handler)
	defer q.Shutdown()

	q.Add(context.Background(), "org/svc", "url-v1", "main")
	q.Add(context.Background(), "org/svc", "url-v2", "feature/harden")

	select {
	case got := <-calls:
		if got != "feature/harden" {
			t.Errorf("handler got branch=%q, want %q (latest payload)", got, "feature/harden")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out")
	}
}

func TestSyncQueue_ContextCancelStopsHandler(t *testing.T) {
	handlerStarted := make(chan struct{})
	handlerDone := make(chan struct{})
	var handlerErr error

	handler := func(ctx context.Context, repoFullName, cloneURL, branch string) error {
		close(handlerStarted)
		select {
		case <-ctx.Done():
			handlerErr = ctx.Err()
		case <-time.After(10 * time.Second):
		}
		close(handlerDone)
		return handlerErr
	}

	parentCtx, cancel := context.WithCancel(context.Background())
	q := NewSyncQueueWithContext(parentCtx, 1, handler)

	q.Add(context.Background(), "org/svc", "https://example.com/org/svc.git", "main")

	select {
	case <-handlerStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for handler to start")
	}

	cancel()

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

	handler := func(ctx context.Context, repoFullName, cloneURL, branch string) error {
		callCount.Add(1)
		<-ctx.Done()
		return ctx.Err()
	}

	parentCtx, cancel := context.WithCancel(context.Background())
	q := NewSyncQueueWithContext(parentCtx, 1, handler)

	q.Add(context.Background(), "org/a", "url-a", "main")
	time.Sleep(20 * time.Millisecond)
	q.Add(context.Background(), "org/b", "url-b", "main")

	cancel()

	q.Shutdown()

	got := callCount.Load()
	if got < 1 {
		t.Errorf("expected at least 1 handler call, got %d", got)
	}
}

func TestSyncQueue_RetriesOnHandlerError(t *testing.T) {
	var callCount atomic.Int32
	errOnce := errors.New("transient error")

	handler := func(_ context.Context, repoFullName, cloneURL, branch string) error {
		n := callCount.Add(1)
		if n < 3 {
			return errOnce
		}
		return nil
	}

	q := NewSyncQueueWithOptions(context.Background(), 1, handler, RetryConfig{
		MaxAttempts: 3,
		BaseDelay:   1 * time.Millisecond,
		MaxDelay:    10 * time.Millisecond,
	})
	defer q.Shutdown()

	q.Add(context.Background(), "org/svc", "url", "main")

	time.Sleep(200 * time.Millisecond)

	got := callCount.Load()
	if got != 3 {
		t.Errorf("handler called %d times, want 3 (2 failures + 1 success)", got)
	}
}

func TestSyncQueue_GivesUpAfterMaxAttempts(t *testing.T) {
	var callCount atomic.Int32
	alwaysFail := errors.New("always fails")

	handler := func(_ context.Context, repoFullName, cloneURL, branch string) error {
		callCount.Add(1)
		return alwaysFail
	}

	q := NewSyncQueueWithOptions(context.Background(), 1, handler, RetryConfig{
		MaxAttempts: 3,
		BaseDelay:   1 * time.Millisecond,
		MaxDelay:    10 * time.Millisecond,
	})
	defer q.Shutdown()

	q.Add(context.Background(), "org/svc", "url", "main")

	time.Sleep(200 * time.Millisecond)

	got := callCount.Load()
	if got != 3 {
		t.Errorf("handler called %d times, want exactly 3 (MaxAttempts)", got)
	}
}

func TestSyncQueue_NonRetryableErrorSkipsRetries(t *testing.T) {
	var callCount atomic.Int32

	handler := func(_ context.Context, repoFullName, cloneURL, branch string) error {
		callCount.Add(1)
		return NonRetryable(errors.New("invalid repo config"))
	}

	q := NewSyncQueueWithOptions(context.Background(), 1, handler, RetryConfig{
		MaxAttempts: 5,
		BaseDelay:   1 * time.Millisecond,
		MaxDelay:    10 * time.Millisecond,
	})
	defer q.Shutdown()

	q.Add(context.Background(), "org/svc", "url", "main")

	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); {
		stats := q.Stats()
		if stats.FailureTotal == 1 {
			if got := callCount.Load(); got != 1 {
				t.Fatalf("handler called %d times, want 1", got)
			}
			if stats.LastError == "" {
				t.Fatalf("expected last error to be recorded: %+v", stats)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("non-retryable failure stats were not recorded: %+v", q.Stats())
}

func TestSyncQueue_RetryCancelledOnContextDone(t *testing.T) {
	var callCount atomic.Int32
	alwaysFail := errors.New("always fails")

	handler := func(_ context.Context, repoFullName, cloneURL, branch string) error {
		callCount.Add(1)
		return alwaysFail
	}

	ctx, cancel := context.WithCancel(context.Background())
	q := NewSyncQueueWithOptions(ctx, 1, handler, RetryConfig{
		MaxAttempts: 10,
		BaseDelay:   50 * time.Millisecond,
		MaxDelay:    100 * time.Millisecond,
	})
	defer q.Shutdown()

	q.Add(context.Background(), "org/svc", "url", "main")

	time.Sleep(80 * time.Millisecond)
	cancel()

	time.Sleep(200 * time.Millisecond)

	got := callCount.Load()
	if got >= 10 {
		t.Errorf("handler called %d times — retry was not cancelled by context", got)
	}
}

func TestSyncQueue_AddHonorsPerCallContextCancel(t *testing.T) {
	var callCount atomic.Int32
	handler := func(_ context.Context, repoFullName, cloneURL, branch string) error {
		callCount.Add(1)
		return nil
	}

	q := NewSyncQueue(1, handler)
	defer q.Shutdown()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	q.Add(ctx, "org/svc", "url", "main")

	time.Sleep(100 * time.Millisecond)
	if got := callCount.Load(); got != 0 {
		t.Fatalf("expected cancelled Add not to enqueue work, got %d calls", got)
	}
}

func TestSyncQueue_AddPerCallTimeoutPropagates(t *testing.T) {
	handlerDone := make(chan error, 1)
	handler := func(ctx context.Context, repoFullName, cloneURL, branch string) error {
		<-ctx.Done()
		handlerDone <- ctx.Err()
		return ctx.Err()
	}

	q := NewSyncQueue(1, handler)
	defer q.Shutdown()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	q.Add(ctx, "org/svc", "url", "main")

	select {
	case err := <-handlerDone:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("expected deadline exceeded, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for handler")
	}
}

func TestNewSyncQueueWithOptions_NilContextDefaultsToBackground(t *testing.T) {
	handlerDone := make(chan error, 1)
	handler := func(ctx context.Context, repoFullName, cloneURL, branch string) error {
		select {
		case <-ctx.Done():
			handlerDone <- ctx.Err()
		case <-time.After(50 * time.Millisecond):
			handlerDone <- nil
		}
		return nil
	}

	q := NewSyncQueueWithOptions(nil, 1, handler, RetryConfig{MaxAttempts: 1, BaseDelay: time.Millisecond, MaxDelay: time.Millisecond})
	defer q.Shutdown()

	q.Add(context.Background(), "org/svc", "url", "main")

	select {
	case err := <-handlerDone:
		if err != nil {
			t.Fatalf("expected background fallback context, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for handler")
	}
}

func TestSyncQueue_AddRejectsWhenMaxTrackedReposExceeded(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	handler := func(_ context.Context, repoFullName, cloneURL, branch string) error {
		select {
		case started <- struct{}{}:
		default:
		}
		<-release
		return nil
	}

	q := NewSyncQueueWithConfig(context.Background(), 1, handler, QueueConfig{
		RetryConfig:     RetryConfig{MaxAttempts: 1, BaseDelay: time.Millisecond, MaxDelay: time.Millisecond},
		MaxTrackedRepos: 1,
	})
	defer q.Shutdown()

	if err := q.Add(context.Background(), "org/one", "url-one", "main"); err != nil {
		t.Fatalf("first Add returned error: %v", err)
	}

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for first repo to start")
	}

	if err := q.Add(context.Background(), "org/two", "url-two", "main"); !errors.Is(err, ErrSyncQueueFull) {
		t.Fatalf("second Add error = %v, want %v", err, ErrSyncQueueFull)
	}

	close(release)
}

func TestSyncQueue_AddAllowsExistingRepoUpdateWhenAtCapacity(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	urls := make(chan string, 2)
	handler := func(_ context.Context, repoFullName, cloneURL, branch string) error {
		urls <- cloneURL
		select {
		case started <- struct{}{}:
		default:
		}
		<-release
		return nil
	}

	q := NewSyncQueueWithConfig(context.Background(), 1, handler, QueueConfig{
		RetryConfig:     RetryConfig{MaxAttempts: 1, BaseDelay: time.Millisecond, MaxDelay: time.Millisecond},
		MaxTrackedRepos: 1,
	})
	defer q.Shutdown()

	if err := q.Add(context.Background(), "org/one", "url-one", "main"); err != nil {
		t.Fatalf("first Add returned error: %v", err)
	}

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for first repo to start")
	}

	if err := q.Add(context.Background(), "org/one", "url-two", "develop"); err != nil {
		t.Fatalf("existing repo update returned error: %v", err)
	}

	close(release)

	select {
	case got := <-urls:
		if got != "url-one" {
			t.Fatalf("first handler url = %q, want %q", got, "url-one")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for first handler call")
	}
	select {
	case got := <-urls:
		if got != "url-two" {
			t.Fatalf("second handler url = %q, want %q", got, "url-two")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for second handler call")
	}
}

func TestSyncQueueStats_ReportsQueueFull(t *testing.T) {
	q := NewSyncQueueWithConfig(context.Background(), 0, func(context.Context, string, string, string) error {
		return nil
	}, QueueConfig{
		RetryConfig:     RetryConfig{MaxAttempts: 1, BaseDelay: time.Millisecond, MaxDelay: time.Millisecond},
		MaxTrackedRepos: 1,
	})
	defer q.Shutdown()

	if err := q.Add(context.Background(), "org/one", "url-one", "main"); err != nil {
		t.Fatalf("first Add returned error: %v", err)
	}
	if err := q.Add(context.Background(), "org/two", "url-two", "main"); !errors.Is(err, ErrSyncQueueFull) {
		t.Fatalf("second Add error = %v, want ErrSyncQueueFull", err)
	}

	stats := q.Stats()
	if stats.Queued != 1 || stats.TrackedRepos != 1 || stats.MaxTrackedRepos != 1 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
	if stats.QueueFullTotal != 1 {
		t.Fatalf("QueueFullTotal = %d, want 1", stats.QueueFullTotal)
	}
}

func TestSyncQueueStats_ReportsFinalFailure(t *testing.T) {
	done := make(chan struct{})
	q := NewSyncQueueWithConfig(context.Background(), 1, func(context.Context, string, string, string) error {
		close(done)
		return errors.New("boom")
	}, QueueConfig{
		RetryConfig:     RetryConfig{MaxAttempts: 1, BaseDelay: time.Millisecond, MaxDelay: time.Millisecond},
		MaxTrackedRepos: 1,
	})
	defer q.Shutdown()

	if err := q.Add(context.Background(), "org/fail", "url", "main"); err != nil {
		t.Fatalf("Add returned error: %v", err)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for handler")
	}

	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); {
		stats := q.Stats()
		if stats.FailureTotal == 1 {
			if stats.LastError == "" || stats.LastErrorTime.IsZero() {
				t.Fatalf("failure stats missing details: %+v", stats)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("failure stats were not recorded: %+v", q.Stats())
}

func TestSyncQueueStats_RecentRepos_RecordsSuccessAndBranch(t *testing.T) {
	done := make(chan struct{})
	handler := func(_ context.Context, repoFullName, cloneURL, branch string) error {
		close(done)
		return nil
	}

	q := NewSyncQueueWithConfig(context.Background(), 1, handler, QueueConfig{
		RetryConfig:     RetryConfig{MaxAttempts: 1, BaseDelay: time.Millisecond, MaxDelay: time.Millisecond},
		MaxTrackedRepos: 10,
	})
	defer q.Shutdown()

	q.Add(context.Background(), "org/myrepo", "url", "feature/x")

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for handler")
	}

	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); {
		stats := q.Stats()
		if len(stats.RecentRepos) > 0 {
			r := stats.RecentRepos[0]
			if r.Repo != "org/myrepo" {
				t.Fatalf("repo = %q, want %q", r.Repo, "org/myrepo")
			}
			if r.Branch != "feature/x" {
				t.Fatalf("branch = %q, want %q", r.Branch, "feature/x")
			}
			if r.LastSuccessTime.IsZero() {
				t.Fatalf("LastSuccessTime is zero, want non-zero")
			}
			if r.LastErrorTime != (time.Time{}) && !r.LastErrorTime.IsZero() {
				t.Fatalf("LastErrorTime should be zero for success, got %v", r.LastErrorTime)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("RecentRepos not populated: %+v", q.Stats())
}

func TestSyncQueueStats_RecentRepos_RecordsFailureAndError(t *testing.T) {
	done := make(chan struct{})
	handler := func(_ context.Context, repoFullName, cloneURL, branch string) error {
		close(done)
		return errors.New("clone failed: permission denied")
	}

	q := NewSyncQueueWithConfig(context.Background(), 1, handler, QueueConfig{
		RetryConfig:     RetryConfig{MaxAttempts: 1, BaseDelay: time.Millisecond, MaxDelay: time.Millisecond},
		MaxTrackedRepos: 10,
	})
	defer q.Shutdown()

	q.Add(context.Background(), "org/failrepo", "url", "main")

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for handler")
	}

	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); {
		stats := q.Stats()
		if len(stats.RecentRepos) > 0 {
			r := stats.RecentRepos[0]
			if r.LastError == "" || r.LastErrorTime.IsZero() {
				time.Sleep(10 * time.Millisecond)
				continue
			}
			if r.Repo != "org/failrepo" {
				t.Fatalf("repo = %q, want %q", r.Repo, "org/failrepo")
			}
			if r.Branch != "main" {
				t.Fatalf("branch = %q, want %q", r.Branch, "main")
			}
			if r.LastError == "" {
				t.Fatalf("LastError is empty, want non-empty")
			}
			if r.LastErrorTime.IsZero() {
				t.Fatalf("LastErrorTime is zero, want non-zero")
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("RecentRepos not populated after failure: %+v", q.Stats())
}

func TestSyncQueueStats_RecentRepos_OrderedByMostRecent(t *testing.T) {
	var mu sync.Mutex
	order := make([]string, 0)
	release := make(chan struct{})

	handler := func(_ context.Context, repoFullName, cloneURL, branch string) error {
		<-release
		mu.Lock()
		order = append(order, repoFullName)
		mu.Unlock()
		return nil
	}

	q := NewSyncQueueWithConfig(context.Background(), 3, handler, QueueConfig{
		RetryConfig:     RetryConfig{MaxAttempts: 1, BaseDelay: time.Millisecond, MaxDelay: time.Millisecond},
		MaxTrackedRepos: 10,
	})
	defer q.Shutdown()

	q.Add(context.Background(), "org/alpha", "url-a", "main")
	q.Add(context.Background(), "org/beta", "url-b", "main")
	q.Add(context.Background(), "org/gamma", "url-c", "main")

	time.Sleep(20 * time.Millisecond)
	close(release)

	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); {
		mu.Lock()
		n := len(order)
		mu.Unlock()
		if n == 3 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); {
		stats := q.Stats()
		if len(stats.RecentRepos) == 3 {
			seen := make(map[string]bool)
			for _, r := range stats.RecentRepos {
				if seen[r.Repo] {
					t.Fatalf("duplicate repo in RecentRepos: %v", r.Repo)
				}
				seen[r.Repo] = true
			}
			if !seen["org/alpha"] || !seen["org/beta"] || !seen["org/gamma"] {
				t.Fatalf("missing repos in RecentRepos: %+v", stats.RecentRepos)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("RecentRepos not fully populated: %+v", q.Stats())
}

func TestSyncQueueStats_RecentRepos_CappedAtMaxRecentRepos(t *testing.T) {
	done := make(chan struct{})
	var once sync.Once

	handler := func(_ context.Context, repoFullName, cloneURL, branch string) error {
		once.Do(func() { close(done) })
		return nil
	}

	q := NewSyncQueueWithConfig(context.Background(), 5, handler, QueueConfig{
		RetryConfig:     RetryConfig{MaxAttempts: 1, BaseDelay: time.Millisecond, MaxDelay: time.Millisecond},
		MaxTrackedRepos: 100,
	})
	defer q.Shutdown()

	for i := 0; i < 60; i++ {
		repo := "org/repo" + string(rune('A'+i%26)) + string(rune('0'+i/26))
		q.Add(context.Background(), repo, "url", "main")
	}

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for handler")
	}

	time.Sleep(200 * time.Millisecond)

	stats := q.Stats()
	if len(stats.RecentRepos) > 50 {
		t.Fatalf("RecentRepos len = %d, want ≤ 50 (cap exceeded)", len(stats.RecentRepos))
	}
}

func TestSyncQueueStats_RecentRepos_ShowsQueuedAndProcessingState(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once

	handler := func(_ context.Context, repoFullName, cloneURL, branch string) error {
		once.Do(func() { close(started) })
		<-release
		return nil
	}

	q := NewSyncQueueWithConfig(context.Background(), 1, handler, QueueConfig{
		RetryConfig:     RetryConfig{MaxAttempts: 1, BaseDelay: time.Millisecond, MaxDelay: time.Millisecond},
		MaxTrackedRepos: 10,
	})
	defer q.Shutdown()

	q.Add(context.Background(), "org/processing", "url", "main")
	q.Add(context.Background(), "org/queued", "url2", "develop")

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for handler to start")
	}

	stats := q.Stats()
	repoMap := make(map[string]RepoStats)
	for _, r := range stats.RecentRepos {
		repoMap[r.Repo] = r
	}

	if proc, ok := repoMap["org/processing"]; ok {
		if !proc.Processing {
			t.Fatalf("org/processing should have Processing=true, got %+v", proc)
		}
	}
	if queued, ok := repoMap["org/queued"]; ok {
		if !queued.Queued {
			t.Fatalf("org/queued should have Queued=true, got %+v", queued)
		}
	}

	close(release)
}

func TestSyncQueueStats_RecentRepos_IncludesQueuedAndProcessingBeforeFirstOutcome(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once

	handler := func(_ context.Context, repoFullName, cloneURL, branch string) error {
		once.Do(func() { close(started) })
		<-release
		return nil
	}

	q := NewSyncQueueWithConfig(context.Background(), 1, handler, QueueConfig{
		RetryConfig:     RetryConfig{MaxAttempts: 1, BaseDelay: time.Millisecond, MaxDelay: time.Millisecond},
		MaxTrackedRepos: 10,
	})
	defer q.Shutdown()

	if err := q.Add(context.Background(), "org/processing", "url", "feature/processing"); err != nil {
		t.Fatalf("Add processing returned error: %v", err)
	}
	if err := q.Add(context.Background(), "org/queued", "url2", "feature/queued"); err != nil {
		t.Fatalf("Add queued returned error: %v", err)
	}

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for handler to start")
	}

	stats := q.Stats()
	if len(stats.RecentRepos) < 2 {
		t.Fatalf("expected queued and processing repos in RecentRepos, got %+v", stats.RecentRepos)
	}
	repoMap := make(map[string]RepoStats)
	for _, r := range stats.RecentRepos {
		repoMap[r.Repo] = r
	}

	proc, ok := repoMap["org/processing"]
	if !ok {
		t.Fatalf("processing repo missing from RecentRepos: %+v", stats.RecentRepos)
	}
	if !proc.Processing {
		t.Fatalf("processing repo should have Processing=true, got %+v", proc)
	}
	if proc.Branch != "feature/processing" {
		t.Fatalf("processing repo branch = %q, want %q", proc.Branch, "feature/processing")
	}

	queued, ok := repoMap["org/queued"]
	if !ok {
		t.Fatalf("queued repo missing from RecentRepos: %+v", stats.RecentRepos)
	}
	if !queued.Queued {
		t.Fatalf("queued repo should have Queued=true, got %+v", queued)
	}
	if queued.Branch != "feature/queued" {
		t.Fatalf("queued repo branch = %q, want %q", queued.Branch, "feature/queued")
	}

	close(release)
}

func TestSyncQueueStats_ReportsAgesAndLastSuccess(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{})
	q := NewSyncQueueWithConfig(context.Background(), 1, func(context.Context, string, string, string) error {
		close(started)
		<-release
		return nil
	}, QueueConfig{
		RetryConfig:     RetryConfig{MaxAttempts: 1, BaseDelay: time.Millisecond, MaxDelay: time.Millisecond},
		MaxTrackedRepos: 2,
	})
	defer q.Shutdown()

	if err := q.Add(context.Background(), "org/one", "url-one", "main"); err != nil {
		t.Fatalf("Add org/one returned error: %v", err)
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for handler")
	}
	if err := q.Add(context.Background(), "org/two", "url-two", "main"); err != nil {
		t.Fatalf("Add org/two returned error: %v", err)
	}
	time.Sleep(20 * time.Millisecond)

	stats := q.Stats()
	if stats.OldestProcessingAge <= 0 {
		t.Fatalf("OldestProcessingAge = %v, want > 0", stats.OldestProcessingAge)
	}
	if stats.OldestQueuedAge <= 0 {
		t.Fatalf("OldestQueuedAge = %v, want > 0", stats.OldestQueuedAge)
	}
	if !stats.LastSuccessTime.IsZero() {
		t.Fatalf("LastSuccessTime before success = %v, want zero", stats.LastSuccessTime)
	}

	close(release)
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); {
		if !q.Stats().LastSuccessTime.IsZero() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("LastSuccessTime was not recorded: %+v", q.Stats())
}
