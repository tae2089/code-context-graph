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
)

func signPayload(secret, payload []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write(payload)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func makePushEvent(ref, repoFullName, cloneURL string) []byte {
	event := map[string]interface{}{
		"ref": ref,
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
	var syncedRepo, syncedURL string
	onSync := func(_ context.Context, repo, url string) {
	mu.Lock()
		defer mu.Unlock()
		syncedRepo = repo
		syncedURL = url
}

	h := NewWebhookHandler(secret, al, onSync)

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
}

func TestWebhookHandler_InvalidSignature(t *testing.T) {
	secret := []byte("test-secret")
	al := NewRepoFilter([]string{"org/*"})

	h := NewWebhookHandler(secret, al, func(context.Context, string, string) {})

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

func TestWebhookHandler_NonMainBranch(t *testing.T) {
	secret := []byte("test-secret")
	al := NewRepoFilter([]string{"org/*"})

	synced := false
	h := NewWebhookHandler(secret, al, func(context.Context, string, string) { synced = true })

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

func TestWebhookHandler_DisallowedRepo(t *testing.T) {
	secret := []byte("test-secret")
	al := NewRepoFilter([]string{"org/*"})

	synced := false
	h := NewWebhookHandler(secret, al, func(context.Context, string, string) { synced = true })

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
	h := NewWebhookHandler(secret, al, func(context.Context, string, string) { synced = true })

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
	onSync := func(_ context.Context, repo, url string) {
	syncedRepo = repo
}
	h := NewWebhookHandler(secret, al, onSync)

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
	onSync := func(_ context.Context, repo, url string) {
	syncedRepo = repo
}
	h := NewWebhookHandler(secret, al, onSync)

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
