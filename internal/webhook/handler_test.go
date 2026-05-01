package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func signPayload(secret, payload []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write(payload)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func makePushEvent(ref, repoFullName, cloneURL string) []byte {
	event := map[string]interface{}{
		"ref": ref,
		"after": "1111111111111111111111111111111111111111",
		"repository": map[string]interface{}{
			"full_name": repoFullName,
			"clone_url": cloneURL,
		},
	}
	data, _ := json.Marshal(event)
	return data
}

func makeDeletedPushEvent(ref, repoFullName, cloneURL string) []byte {
	event := map[string]interface{}{
		"ref":     ref,
		"after":   "0000000000000000000000000000000000000000",
		"deleted": true,
		"repository": map[string]interface{}{
			"full_name": repoFullName,
			"clone_url": cloneURL,
		},
	}
	data, _ := json.Marshal(event)
	return data
}

func TestWebhookHandler_ValidPushEvent(t *testing.T) {
	secret := []byte("test-secret")
	al := NewRepoFilter([]string{"org/*"})

	var mu sync.Mutex
	var syncedRepo, syncedURL, syncedBranch string
	onSync := func(_ context.Context, repo, url, branch string) error {
		mu.Lock()
		defer mu.Unlock()
		syncedRepo = repo
		syncedURL = url
		syncedBranch = branch
		return nil
	}

	h := NewWebhookHandlerWithConfig(WebhookHandlerConfig{Secret: secret, Filter: al, OnSync: onSync, CloneBaseURL: "https://github.com"})

	payload := makePushEvent("refs/heads/main", "org/my-svc", "https://github.com/org/my-svc.git")
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(payload))
	req.Header.Set("X-Hub-Signature-256", signPayload(secret, payload))
	req.Header.Set("X-GitHub-Event", "push")

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusOK)
	}

	mu.Lock()
	defer mu.Unlock()
	if syncedRepo != "org/my-svc" {
		t.Errorf("syncedRepo = %q, want %q", syncedRepo, "org/my-svc")
	}
	if syncedURL != "https://github.com/org/my-svc.git" {
		t.Errorf("syncedURL = %q, want %q", syncedURL, "https://github.com/org/my-svc.git")
	}
	if syncedBranch != "main" {
		t.Errorf("syncedBranch = %q, want %q", syncedBranch, "main")
	}
}

func TestWebhookHandler_InvalidSignature(t *testing.T) {
	secret := []byte("test-secret")
	al := NewRepoFilter([]string{"org/*"})

	h := NewWebhookHandler(secret, al, func(context.Context, string, string, string) error { return nil })

	payload := makePushEvent("refs/heads/main", "org/svc", "https://github.com/org/svc.git")
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(payload))
	req.Header.Set("X-Hub-Signature-256", "sha256=invalid")
	req.Header.Set("X-GitHub-Event", "push")

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusForbidden)
	}
}

func TestWebhookHandler_EmptySecretRejectsPushEvent(t *testing.T) {
	al := NewRepoFilter([]string{"org/*"})

	synced := false
	h := NewWebhookHandler(nil, al, func(context.Context, string, string, string) error { synced = true; return nil })

	payload := makePushEvent("refs/heads/main", "org/svc", "https://github.com/org/svc.git")
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(payload))
	req.Header.Set("X-GitHub-Event", "push")

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusForbidden)
	}
	if synced {
		t.Error("expected sync to NOT be called when secret is empty")
	}
}

func TestWebhookHandler_InsecureModeAcceptsUnsignedPushEvent(t *testing.T) {
	al := NewRepoFilter([]string{"org/*"})

	synced := false
	h := NewWebhookHandlerWithOptions(nil, al, func(context.Context, string, string, string) error { synced = true; return nil }, true)

	payload := makePushEvent("refs/heads/main", "org/svc", "https://github.com/org/svc.git")
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(payload))
	req.Header.Set("X-GitHub-Event", "push")

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	if !synced {
		t.Error("expected sync to be called in insecure mode")
	}
}

func TestWebhookHandler_UsesConfiguredCloneBaseURL(t *testing.T) {
	secret := []byte("test-secret")
	al := NewRepoFilter([]string{"org/*"})

	var syncedURL string
	h := NewWebhookHandlerWithConfig(WebhookHandlerConfig{
		Secret:       secret,
		Filter:       al,
		CloneBaseURL: "https://github.com/base",
		OnSync: func(_ context.Context, _ string, url string, _ string) error {
			syncedURL = url
			return nil
		},
	})

	payload := makePushEvent("refs/heads/main", "org/svc", "https://evil.example/org/svc.git")
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(payload))
	req.Header.Set("X-Hub-Signature-256", signPayload(secret, payload))
	req.Header.Set("X-GitHub-Event", "push")

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	if syncedURL != "https://github.com/base/org/svc.git" {
		t.Fatalf("syncedURL = %q, want %q", syncedURL, "https://github.com/base/org/svc.git")
	}
}

func TestWebhookHandler_RejectsSecureModeWithoutCloneBaseURL(t *testing.T) {
	secret := []byte("test-secret")
	al := NewRepoFilter([]string{"org/*"})

	synced := false
	h := NewWebhookHandler(secret, al, func(context.Context, string, string, string) error { synced = true; return nil })

	payload := makePushEvent("refs/heads/main", "org/svc", "https://github.com/org/svc.git")
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(payload))
	req.Header.Set("X-Hub-Signature-256", signPayload(secret, payload))
	req.Header.Set("X-GitHub-Event", "push")

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusForbidden)
	}
	if synced {
		t.Fatal("expected sync not to be called without configured clone base URL")
	}
}

func TestWebhookHandler_NonMainBranch(t *testing.T) {
	secret := []byte("test-secret")
	al := NewRepoFilter([]string{"org/*"})

	synced := false
	h := NewWebhookHandlerWithConfig(WebhookHandlerConfig{Secret: secret, Filter: al, OnSync: func(context.Context, string, string, string) error { synced = true; return nil }, CloneBaseURL: "https://github.com"})

	payload := makePushEvent("refs/heads/develop", "org/svc", "https://github.com/org/svc.git")
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(payload))
	req.Header.Set("X-Hub-Signature-256", signPayload(secret, payload))
	req.Header.Set("X-GitHub-Event", "push")

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	if synced {
		t.Error("expected sync to NOT be called for non-main branch")
	}
}

func TestWebhookHandler_NonBranchRefSkipsWithoutSync(t *testing.T) {
	secret := []byte("test-secret")
	al := NewRepoFilter([]string{"org/*"})

	synced := false
	h := NewWebhookHandlerWithConfig(WebhookHandlerConfig{Secret: secret, Filter: al, OnSync: func(context.Context, string, string, string) error { synced = true; return nil }, CloneBaseURL: "https://github.com"})

	payload := makePushEvent("refs/tags/v1.0.0", "org/svc", "https://github.com/org/svc.git")
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(payload))
	req.Header.Set("X-Hub-Signature-256", signPayload(secret, payload))
	req.Header.Set("X-GitHub-Event", "push")

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	if synced {
		t.Error("expected sync to NOT be called for tag ref")
	}
}

func TestWebhookHandler_DeletedBranchSkipsWithoutSync(t *testing.T) {
	secret := []byte("test-secret")
	al := NewRepoFilter([]string{"org/*"})

	synced := false
	h := NewWebhookHandlerWithConfig(WebhookHandlerConfig{Secret: secret, Filter: al, OnSync: func(context.Context, string, string, string) error { synced = true; return nil }, CloneBaseURL: "https://github.com"})

	payload := makeDeletedPushEvent("refs/heads/main", "org/svc", "https://github.com/org/svc.git")
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(payload))
	req.Header.Set("X-Hub-Signature-256", signPayload(secret, payload))
	req.Header.Set("X-GitHub-Event", "push")

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	if synced {
		t.Error("expected sync to NOT be called for deleted branch push")
	}
}

func TestWebhookHandler_DisallowedRepo(t *testing.T) {
	secret := []byte("test-secret")
	al := NewRepoFilter([]string{"org/*"})

	synced := false
	h := NewWebhookHandlerWithConfig(WebhookHandlerConfig{Secret: secret, Filter: al, OnSync: func(context.Context, string, string, string) error { synced = true; return nil }, CloneBaseURL: "https://github.com"})

	payload := makePushEvent("refs/heads/main", "other/svc", "https://github.com/other/svc.git")
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(payload))
	req.Header.Set("X-Hub-Signature-256", signPayload(secret, payload))
	req.Header.Set("X-GitHub-Event", "push")

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	if synced {
		t.Error("expected sync to NOT be called for disallowed repo")
	}
}

func TestWebhookHandler_NonPushEvent(t *testing.T) {
	secret := []byte("test-secret")
	al := NewRepoFilter([]string{"org/*"})

	synced := false
	h := NewWebhookHandler(secret, al, func(context.Context, string, string, string) error { synced = true; return nil })

	payload := []byte(`{"action":"opened"}`)
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(payload))
	req.Header.Set("X-Hub-Signature-256", signPayload(secret, payload))
	req.Header.Set("X-GitHub-Event", "issues")

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	if synced {
		t.Error("expected sync to NOT be called for non-push event")
	}
}

func TestExtractNamespace(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"org/pay-svc", "pay-svc"},
		{"org/sub/repo", "sub-repo"},
		{"single", "single"},
	}

	for _, tt := range tests {
		got := ExtractNamespace(tt.input)
		if got != tt.want {
			t.Errorf("ExtractNamespace(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestWebhookHandler_GiteaEventHeader(t *testing.T) {
	secret := []byte("test-secret")
	al := NewRepoFilter([]string{"org/*"})

	var syncedRepo string
	onSync := func(_ context.Context, repo, url, branch string) error {
		syncedRepo = repo
		return nil
	}
	h := NewWebhookHandlerWithConfig(WebhookHandlerConfig{Secret: secret, Filter: al, OnSync: onSync, CloneBaseURL: "https://gitea.local"})

	payload := makePushEvent("refs/heads/main", "org/svc", "https://gitea.local/org/svc.git")
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(payload))
	req.Header.Set("X-Hub-Signature-256", signPayload(secret, payload))
	req.Header.Set("X-Gitea-Event", "push")

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	if syncedRepo != "org/svc" {
		t.Errorf("syncedRepo = %q, want %q", syncedRepo, "org/svc")
	}
}

func signPayloadRaw(secret, payload []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write(payload)
	return hex.EncodeToString(mac.Sum(nil))
}

func TestWebhookHandler_GiteaSignatureHeader(t *testing.T) {
	secret := []byte("test-secret")
	al := NewRepoFilter([]string{"org/*"})

	var syncedRepo string
	onSync := func(_ context.Context, repo, url, branch string) error {
		syncedRepo = repo
		return nil
	}
	h := NewWebhookHandlerWithConfig(WebhookHandlerConfig{Secret: secret, Filter: al, OnSync: onSync, CloneBaseURL: "https://gitea.local"})

	payload := makePushEvent("refs/heads/main", "org/svc", "https://gitea.local/org/svc.git")
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(payload))
	req.Header.Set("X-Gitea-Signature", signPayloadRaw(secret, payload))
	req.Header.Set("X-Gitea-Event", "push")

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	if syncedRepo != "org/svc" {
		t.Errorf("syncedRepo = %q, want %q", syncedRepo, "org/svc")
	}
}

func TestWebhookHandler_SyncContextNotCancelledOnReturn(t *testing.T) {
	secret := []byte("test-secret")
	al := NewRepoFilter([]string{"org/*"})
	ctxDone := make(chan error, 1)

	h := NewWebhookHandlerWithConfig(WebhookHandlerConfig{Secret: secret, Filter: al, CloneBaseURL: "https://github.com", OnSync: func(ctx context.Context, repo, url, branch string) error {
		go func() {
			time.Sleep(20 * time.Millisecond)
			ctxDone <- ctx.Err()
		}()
		return nil
	}})

	payload := makePushEvent("refs/heads/main", "org/svc", "https://github.com/org/svc.git")
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(payload))
	req.Header.Set("X-Hub-Signature-256", signPayload(secret, payload))
	req.Header.Set("X-GitHub-Event", "push")

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	select {
	case err := <-ctxDone:
		if err != nil {
			t.Fatalf("expected detached context after response, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for sync context result")
	}
}

func TestWebhookHandler_ReturnsTooManyRequestsWhenSyncQueueFull(t *testing.T) {
	secret := []byte("test-secret")
	al := NewRepoFilter([]string{"org/*"})
	h := NewWebhookHandlerWithConfig(WebhookHandlerConfig{
		Secret:       secret,
		Filter:       al,
		CloneBaseURL: "https://github.com",
		OnSync: func(context.Context, string, string, string) error {
			return ErrSyncQueueFull
		},
	})

	payload := makePushEvent("refs/heads/main", "org/svc", "https://github.com/org/svc.git")
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(payload))
	req.Header.Set("X-Hub-Signature-256", signPayload(secret, payload))
	req.Header.Set("X-GitHub-Event", "push")

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusTooManyRequests)
	}
}
