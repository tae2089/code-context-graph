package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/tae2089/code-context-graph/internal/webhook"
)

func TestHandleHealth_OK(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()

	handleHealth(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("expected application/json, got %q", ct)
	}
	body := rec.Body.String()
	if body != `{"status":"ok"}` {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestHandleHealth_MethodNotAllowed(t *testing.T) {
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			req := httptest.NewRequest(method, "/health", nil)
			rec := httptest.NewRecorder()

			handleHealth(rec, req)

			if rec.Code != http.StatusMethodNotAllowed {
				t.Fatalf("expected 405, got %d", rec.Code)
			}
		})
	}
}

func TestReadyHandler_OK(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	rec := httptest.NewRecorder()

	readyHandler(func(r *http.Request) error { return nil }).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("expected application/json, got %q", ct)
	}
	if body := rec.Body.String(); body != `{"status":"ready"}` {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestReadyHandler_Unavailable(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	rec := httptest.NewRecorder()

	readyHandler(func(r *http.Request) error { return errors.New("db down") }).ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("expected application/json, got %q", ct)
	}
	if body := rec.Body.String(); body != `{"status":"not_ready"}` {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestReadyHandler_MethodNotAllowed(t *testing.T) {
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			req := httptest.NewRequest(method, "/ready", nil)
			rec := httptest.NewRecorder()

			readyHandler(func(r *http.Request) error { return nil }).ServeHTTP(rec, req)

			if rec.Code != http.StatusMethodNotAllowed {
				t.Fatalf("expected 405, got %d", rec.Code)
			}
		})
	}
}

func TestStatusHandler_ReportsWebhookQueueFull(t *testing.T) {
	q := webhook.NewSyncQueueWithConfig(nil, 0, func(context.Context, string, string, string) error {
		return nil
	}, webhook.QueueConfig{
		RetryConfig:     webhook.RetryConfig{MaxAttempts: 1, BaseDelay: time.Millisecond, MaxDelay: time.Millisecond},
		MaxTrackedRepos: 1,
	})
	defer q.Shutdown()
	if err := q.Add(nil, "org/one", "url", "main"); err != nil {
		t.Fatalf("Add returned error: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/status", nil)
	rec := httptest.NewRecorder()

	statusHandler(func(r *http.Request) error { return nil }, time.Minute, func() *webhook.SyncQueue { return q }).ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestStatusHandler_ReportsWebhookQueueAges(t *testing.T) {
	q := webhook.NewSyncQueueWithConfig(nil, 0, func(context.Context, string, string, string) error {
		return nil
	}, webhook.QueueConfig{
		RetryConfig:     webhook.RetryConfig{MaxAttempts: 1, BaseDelay: time.Millisecond, MaxDelay: time.Millisecond},
		MaxTrackedRepos: 2,
	})
	defer q.Shutdown()
	if err := q.Add(nil, "org/one", "url", "main"); err != nil {
		t.Fatalf("Add returned error: %v", err)
	}
	time.Sleep(10 * time.Millisecond)

	req := httptest.NewRequest(http.MethodGet, "/status", nil)
	rec := httptest.NewRecorder()

	statusHandler(func(r *http.Request) error { return nil }, time.Minute, func() *webhook.SyncQueue { return q }).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	webhookBody, ok := body["webhook"].(map[string]any)
	if !ok {
		t.Fatalf("missing webhook body: %v", body)
	}
	if got, _ := webhookBody["oldest_queued_age"].(float64); got <= 0 {
		t.Fatalf("oldest_queued_age = %v, want > 0", webhookBody["oldest_queued_age"])
	}
}

func TestReadyHandler_ReportsWebhookQueueDelay(t *testing.T) {
	q := webhook.NewSyncQueueWithConfig(nil, 0, func(context.Context, string, string, string) error {
		return nil
	}, webhook.QueueConfig{
		RetryConfig:     webhook.RetryConfig{MaxAttempts: 1, BaseDelay: time.Millisecond, MaxDelay: time.Millisecond},
		MaxTrackedRepos: 2,
	})
	defer q.Shutdown()
	if err := q.Add(nil, "org/one", "url", "main"); err != nil {
		t.Fatalf("Add returned error: %v", err)
	}
	time.Sleep(10 * time.Millisecond)

	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	rec := httptest.NewRecorder()

	readyHandler(func(r *http.Request) error {
		return webhookBlockingReadyCheck(q, time.Millisecond)
	}).ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestStatusHandler_ReportsUnresolvedWebhookFailure(t *testing.T) {
	done := make(chan struct{})
	q := webhook.NewSyncQueueWithConfig(nil, 1, func(context.Context, string, string, string) error {
		close(done)
		return errors.New("boom")
	}, webhook.QueueConfig{
		RetryConfig:     webhook.RetryConfig{MaxAttempts: 1, BaseDelay: time.Millisecond, MaxDelay: time.Millisecond},
		MaxTrackedRepos: 2,
	})
	defer q.Shutdown()
	if err := q.Add(nil, "org/one", "url", "main"); err != nil {
		t.Fatalf("Add returned error: %v", err)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for failure")
	}
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); {
		if q.Stats().FailureTotal > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	req := httptest.NewRequest(http.MethodGet, "/status", nil)
	rec := httptest.NewRecorder()

	statusHandler(func(r *http.Request) error { return nil }, time.Minute, func() *webhook.SyncQueue { return q }).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if got := body["status"]; got != "degraded" {
		t.Fatalf("expected degraded status, got %v body=%s", got, rec.Body.String())
	}
}

func TestWebhookStatsDegraded_PerRepoUnresolvedFailure(t *testing.T) {
	errTime := time.Now()
	successTime := errTime.Add(2 * time.Minute)

	if !webhookStatsDegraded(webhook.SyncQueueStats{
		LastSuccessTime: successTime,
		RecentRepos: []webhook.RepoStats{{
			Repo:          "org/failrepo",
			LastErrorTime: errTime,
		}},
	}) {
		t.Fatal("expected unresolved repo failure to degrade status")
	}

	if webhookStatsDegraded(webhook.SyncQueueStats{
		LastSuccessTime: successTime,
		RecentRepos: []webhook.RepoStats{{
			Repo:            "org/recovered",
			LastErrorTime:   errTime,
			LastSuccessTime: successTime,
		}},
	}) {
		t.Fatal("expected recovered repo not to degrade status")
	}
}

func TestStatusHandler_DegradedWhenRecentRepoFailureUnresolved(t *testing.T) {
	var mu sync.Mutex
	completed := make([]string, 0, 2)
	q := webhook.NewSyncQueueWithConfig(nil, 1, func(_ context.Context, repoFullName, cloneURL, branch string) error {
		mu.Lock()
		completed = append(completed, repoFullName)
		mu.Unlock()
		if repoFullName == "org/failrepo" {
			return errors.New("boom")
		}
		return nil
	}, webhook.QueueConfig{
		RetryConfig:     webhook.RetryConfig{MaxAttempts: 1, BaseDelay: time.Millisecond, MaxDelay: time.Millisecond},
		MaxTrackedRepos: 10,
	})
	defer q.Shutdown()

	if err := q.Add(nil, "org/failrepo", "url", "feature/fail"); err != nil {
		t.Fatalf("Add failrepo returned error: %v", err)
	}
	if err := q.Add(nil, "org/okrepo", "url", "main"); err != nil {
		t.Fatalf("Add okrepo returned error: %v", err)
	}

	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); {
		mu.Lock()
		done := len(completed)
		mu.Unlock()
		if done == 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	req := httptest.NewRequest(http.MethodGet, "/status", nil)
	rec := httptest.NewRecorder()
	statusHandler(func(r *http.Request) error { return nil }, time.Minute, func() *webhook.SyncQueue { return q }).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if got := body["status"]; got != "degraded" {
		t.Fatalf("expected degraded status, got %v body=%s", got, rec.Body.String())
	}
}

func TestStatusHandler_DBNotReadyTakesPrecedenceOverWebhookDegraded(t *testing.T) {
	done := make(chan struct{})
	q := webhook.NewSyncQueueWithConfig(nil, 1, func(_ context.Context, repoFullName, cloneURL, branch string) error {
		close(done)
		return errors.New("boom")
	}, webhook.QueueConfig{
		RetryConfig:     webhook.RetryConfig{MaxAttempts: 1, BaseDelay: time.Millisecond, MaxDelay: time.Millisecond},
		MaxTrackedRepos: 10,
	})
	defer q.Shutdown()

	if err := q.Add(nil, "org/failrepo", "url", "feature/fail"); err != nil {
		t.Fatalf("Add returned error: %v", err)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for webhook failure")
	}
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); {
		stats := q.Stats()
		if len(stats.RecentRepos) > 0 && webhookStatsDegraded(stats) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	req := httptest.NewRequest(http.MethodGet, "/status", nil)
	rec := httptest.NewRecorder()
	statusHandler(func(r *http.Request) error { return errors.New("db down") }, time.Minute, func() *webhook.SyncQueue { return q }).ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if got := body["status"]; got != "not_ready" {
		t.Fatalf("expected not_ready status, got %v body=%s", got, rec.Body.String())
	}
}

func TestReadyHandler_IgnoresUnresolvedWebhookFailure(t *testing.T) {
	stats := webhook.SyncQueueStats{LastErrorTime: time.Now()}
	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	rec := httptest.NewRecorder()

	readyHandler(func(r *http.Request) error {
		return webhookStatsBlockingReady(stats, time.Minute)
	}).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestStatusHandler_ReportsPerRepoRecentRepos(t *testing.T) {
	done := make(chan struct{})
	q := webhook.NewSyncQueueWithConfig(nil, 1, func(_ context.Context, repoFullName, cloneURL, branch string) error {
		close(done)
		return nil
	}, webhook.QueueConfig{
		RetryConfig:     webhook.RetryConfig{MaxAttempts: 1, BaseDelay: time.Millisecond, MaxDelay: time.Millisecond},
		MaxTrackedRepos: 10,
	})
	defer q.Shutdown()

	if err := q.Add(nil, "org/myrepo", "url", "feature/y"); err != nil {
		t.Fatalf("Add returned error: %v", err)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for handler")
	}

	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); {
		if len(q.Stats().RecentRepos) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	req := httptest.NewRequest(http.MethodGet, "/status", nil)
	rec := httptest.NewRecorder()
	statusHandler(func(r *http.Request) error { return nil }, time.Minute, func() *webhook.SyncQueue { return q }).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	webhookBody, ok := body["webhook"].(map[string]any)
	if !ok {
		t.Fatalf("missing webhook body: %v", body)
	}
	recentRepos, ok := webhookBody["recent_repos"].([]any)
	if !ok || len(recentRepos) == 0 {
		t.Fatalf("recent_repos missing or empty: %v", webhookBody)
	}
	first, ok := recentRepos[0].(map[string]any)
	if !ok {
		t.Fatalf("recent_repos[0] not an object: %v", recentRepos[0])
	}
	if got := first["repo"]; got != "org/myrepo" {
		t.Fatalf("recent_repos[0].repo = %v, want org/myrepo", got)
	}
	if got := first["branch"]; got != "feature/y" {
		t.Fatalf("recent_repos[0].branch = %v, want feature/y", got)
	}
	if _, hasSuccess := first["last_success_time"]; !hasSuccess {
		t.Fatalf("recent_repos[0] missing last_success_time: %v", first)
	}
}

func TestStatusHandler_RecentRepos_FailureHasErrorFields(t *testing.T) {
	done := make(chan struct{})
	q := webhook.NewSyncQueueWithConfig(nil, 1, func(_ context.Context, repoFullName, cloneURL, branch string) error {
		close(done)
		return errors.New("auth failed")
	}, webhook.QueueConfig{
		RetryConfig:     webhook.RetryConfig{MaxAttempts: 1, BaseDelay: time.Millisecond, MaxDelay: time.Millisecond},
		MaxTrackedRepos: 10,
	})
	defer q.Shutdown()

	if err := q.Add(nil, "org/failrepo", "url", "main"); err != nil {
		t.Fatalf("Add returned error: %v", err)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for handler")
	}

	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); {
		if len(q.Stats().RecentRepos) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	req := httptest.NewRequest(http.MethodGet, "/status", nil)
	rec := httptest.NewRecorder()
	statusHandler(func(r *http.Request) error { return nil }, time.Minute, func() *webhook.SyncQueue { return q }).ServeHTTP(rec, req)

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	webhookBody := body["webhook"].(map[string]any)
	recentRepos := webhookBody["recent_repos"].([]any)
	first := recentRepos[0].(map[string]any)

	if got := first["repo"]; got != "org/failrepo" {
		t.Fatalf("repo = %v, want org/failrepo", got)
	}
	if got, _ := first["last_error"].(string); got == "" {
		t.Fatalf("last_error is empty, want non-empty")
	}
	if _, hasErrTime := first["last_error_time"]; !hasErrTime {
		t.Fatalf("last_error_time missing: %v", first)
	}
}

func TestStatusHandler_ExistingAggregateFieldsPreserved(t *testing.T) {
	q := webhook.NewSyncQueueWithConfig(nil, 0, func(context.Context, string, string, string) error {
		return nil
	}, webhook.QueueConfig{
		RetryConfig:     webhook.RetryConfig{MaxAttempts: 1, BaseDelay: time.Millisecond, MaxDelay: time.Millisecond},
		MaxTrackedRepos: 5,
	})
	defer q.Shutdown()

	req := httptest.NewRequest(http.MethodGet, "/status", nil)
	rec := httptest.NewRecorder()
	statusHandler(func(r *http.Request) error { return nil }, time.Minute, func() *webhook.SyncQueue { return q }).ServeHTTP(rec, req)

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	webhookBody, ok := body["webhook"].(map[string]any)
	if !ok {
		t.Fatalf("missing webhook body")
	}
	for _, field := range []string{"queued", "dirty", "processing", "tracked_repos", "max_tracked_repos", "queue_full_total", "failure_total", "oldest_queued_age", "oldest_processing_age", "shutdown"} {
		if _, exists := webhookBody[field]; !exists {
			t.Fatalf("aggregate field %q missing from webhook body: %v", field, webhookBody)
		}
	}
}

type recordingPool struct {
	maxOpen     int
	maxIdle     int
	maxLifetime time.Duration
	maxIdleTime time.Duration
}

func (p *recordingPool) SetMaxOpenConns(v int)              { p.maxOpen = v }
func (p *recordingPool) SetMaxIdleConns(v int)              { p.maxIdle = v }
func (p *recordingPool) SetConnMaxLifetime(v time.Duration) { p.maxLifetime = v }
func (p *recordingPool) SetConnMaxIdleTime(v time.Duration) { p.maxIdleTime = v }

func TestConfigureDBPool_SQLiteUsesSingleConnection(t *testing.T) {
	pool := &recordingPool{}
	configureDBPool(pool, "sqlite")
	if pool.maxOpen != 1 || pool.maxIdle != 1 || pool.maxLifetime != 0 || pool.maxIdleTime != 0 {
		t.Fatalf("unexpected sqlite pool config: %+v", pool)
	}
}

func TestConfigureDBPool_PostgresKeepsDefaultPool(t *testing.T) {
	pool := &recordingPool{}
	configureDBPool(pool, "postgres")
	if pool.maxOpen != 25 || pool.maxIdle != 5 || pool.maxLifetime != 5*time.Minute || pool.maxIdleTime != 5*time.Minute {
		t.Fatalf("unexpected postgres pool config: %+v", pool)
	}
}

func TestValidBearerToken(t *testing.T) {
	if !validBearerToken("Bearer secret", "secret") {
		t.Fatal("expected bearer token to validate")
	}
	if validBearerToken("Bearer wrong", "secret") {
		t.Fatal("expected wrong token to fail")
	}
	if validBearerToken("secret", "secret") {
		t.Fatal("expected missing bearer prefix to fail")
	}
}

func TestIsLoopbackHTTPAddr(t *testing.T) {
	tests := []struct {
		addr string
		want bool
	}{
		{addr: "127.0.0.1:8080", want: true},
		{addr: "localhost:8080", want: true},
		{addr: ":8080", want: true},
		{addr: "0.0.0.0:8080", want: false},
		{addr: "192.168.0.10:8080", want: false},
	}
	for _, tt := range tests {
		if got := isLoopbackHTTPAddr(tt.addr); got != tt.want {
			t.Fatalf("isLoopbackHTTPAddr(%q) = %v, want %v", tt.addr, got, tt.want)
		}
	}
}
