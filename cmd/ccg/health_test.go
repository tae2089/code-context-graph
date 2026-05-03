package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
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
