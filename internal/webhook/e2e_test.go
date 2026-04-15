package webhook

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestE2E_WebhookToRAG(t *testing.T) {
	bareDir := initBareRepo(t)
	repoRoot := t.TempDir()
	secret := []byte("e2e-secret")

	allowlist := NewRepoAllowlist([]string{"myorg/*"})

	var mu sync.Mutex
	var clonedNS string
	var cloneErr error
	done := make(chan struct{})

	onSync := func(repoFullName, cloneURL string) {
		defer close(done)
		ns := ExtractNamespace(repoFullName)
		mu.Lock()
		clonedNS = ns
		mu.Unlock()

		err := CloneOrPull(context.Background(), cloneURL, repoRoot, ns, nil)
		mu.Lock()
		cloneErr = err
		mu.Unlock()
	}

	handler := NewWebhookHandler(secret, allowlist, onSync)

	payload := makePushEvent("refs/heads/main", "myorg/pay-svc", bareDir)
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(payload))
	req.Header.Set("X-Hub-Signature-256", signPayload(secret, payload))
	req.Header.Set("X-GitHub-Event", "push")

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("webhook returned %d, want %d", rr.Code, http.StatusOK)
	}

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for sync to complete")
	}

	mu.Lock()
	defer mu.Unlock()

	if cloneErr != nil {
		t.Fatalf("clone/pull failed: %v", cloneErr)
	}

	if clonedNS != "pay-svc" {
		t.Errorf("namespace = %q, want %q", clonedNS, "pay-svc")
	}

	clonedFile := filepath.Join(RepoDir(repoRoot, clonedNS), "hello.txt")
	if _, err := os.Stat(clonedFile); err != nil {
		t.Errorf("expected %s to exist after webhook sync: %v", clonedFile, err)
	}
}

func TestE2E_MultiRepoIsolation(t *testing.T) {
	bareDir1 := initBareRepo(t)
	bareDir2 := initBareRepo(t)

	addFileToBareRepo(t, bareDir2, "repo2-only.txt", "repo2 content")

	repoRoot := t.TempDir()
	secret := []byte("e2e-secret")

	allowlist := NewRepoAllowlist([]string{"myorg/*"})

	var mu sync.Mutex
	var results []struct {
		ns  string
		err error
	}
	var wg sync.WaitGroup

	onSync := func(repoFullName, cloneURL string) {
		defer wg.Done()
		ns := ExtractNamespace(repoFullName)
		err := CloneOrPull(context.Background(), cloneURL, repoRoot, ns, nil)
		mu.Lock()
		results = append(results, struct {
			ns  string
			err error
		}{ns: ns, err: err})
		mu.Unlock()
	}

	handler := NewWebhookHandler(secret, allowlist, onSync)

	wg.Add(1)
	payload1 := makePushEvent("refs/heads/main", "myorg/svc-alpha", bareDir1)
	req1 := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(payload1))
	req1.Header.Set("X-Hub-Signature-256", signPayload(secret, payload1))
	req1.Header.Set("X-GitHub-Event", "push")

	rr1 := httptest.NewRecorder()
	handler.ServeHTTP(rr1, req1)
	if rr1.Code != http.StatusOK {
		t.Fatalf("webhook1 returned %d", rr1.Code)
	}

	wg.Add(1)
	payload2 := makePushEvent("refs/heads/master", "myorg/svc-beta", bareDir2)
	req2 := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(payload2))
	req2.Header.Set("X-Hub-Signature-256", signPayload(secret, payload2))
	req2.Header.Set("X-GitHub-Event", "push")

	rr2 := httptest.NewRecorder()
	handler.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusOK {
		t.Fatalf("webhook2 returned %d", rr2.Code)
	}

	waitDone := make(chan struct{})
	go func() {
		wg.Wait()
		close(waitDone)
	}()
	select {
	case <-waitDone:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for syncs to complete")
	}

	mu.Lock()
	defer mu.Unlock()

	for _, r := range results {
		if r.err != nil {
			t.Fatalf("sync for namespace %q failed: %v", r.ns, r.err)
		}
	}

	if len(results) != 2 {
		t.Fatalf("expected 2 sync results, got %d", len(results))
	}

	alphaDir := RepoDir(repoRoot, "svc-alpha")
	if _, err := os.Stat(filepath.Join(alphaDir, "hello.txt")); err != nil {
		t.Errorf("svc-alpha: expected hello.txt: %v", err)
	}
	if _, err := os.Stat(filepath.Join(alphaDir, "repo2-only.txt")); err == nil {
		t.Error("svc-alpha: repo2-only.txt should NOT exist (isolation violation)")
	}

	betaDir := RepoDir(repoRoot, "svc-beta")
	if _, err := os.Stat(filepath.Join(betaDir, "hello.txt")); err != nil {
		t.Errorf("svc-beta: expected hello.txt: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(betaDir, "repo2-only.txt"))
	if err != nil {
		t.Fatalf("svc-beta: expected repo2-only.txt: %v", err)
	}
	if string(data) != "repo2 content" {
		t.Errorf("svc-beta: repo2-only.txt = %q, want %q", string(data), "repo2 content")
	}
}

func TestE2E_SyncQueueDedup(t *testing.T) {
	bareDir := initBareRepo(t)
	repoRoot := t.TempDir()
	secret := []byte("e2e-secret")

	allowlist := NewRepoAllowlist([]string{"myorg/*"})

	var callCount int32
	var mu sync.Mutex
	done := make(chan struct{}, 1)

	syncHandler := func(repoFullName, cloneURL string) {
		ns := ExtractNamespace(repoFullName)
		_ = CloneOrPull(context.Background(), cloneURL, repoRoot, ns, nil)
		mu.Lock()
		callCount++
		c := callCount
		mu.Unlock()
		if c == 1 {
			select {
			case done <- struct{}{}:
			default:
			}
		}
	}

	q := NewSyncQueue(2, syncHandler)
	defer q.Shutdown()

	handler := NewWebhookHandler(secret, allowlist, q.Add)

	for i := 0; i < 5; i++ {
		payload := makePushEvent("refs/heads/main", "myorg/pay-svc", bareDir)
		req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(payload))
		req.Header.Set("X-Hub-Signature-256", signPayload(secret, payload))
		req.Header.Set("X-GitHub-Event", "push")
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("webhook[%d] returned %d", i, rr.Code)
		}
	}

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for sync")
	}

	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	got := callCount
	mu.Unlock()

	if got > 2 {
		t.Errorf("syncHandler called %d times, want at most 2 (dedup via SyncQueue)", got)
	}

	clonedFile := filepath.Join(RepoDir(repoRoot, "pay-svc"), "hello.txt")
	if _, err := os.Stat(clonedFile); err != nil {
		t.Errorf("expected %s after SyncQueue dedup clone: %v", clonedFile, err)
	}
}

func TestE2E_SyncQueueMultiRepoParallel(t *testing.T) {
	bareDir1 := initBareRepo(t)
	bareDir2 := initBareRepo(t)
	addFileToBareRepo(t, bareDir2, "beta-only.txt", "beta content")

	repoRoot := t.TempDir()
	secret := []byte("e2e-secret")

	allowlist := NewRepoAllowlist([]string{"myorg/*"})

	var mu sync.Mutex
	synced := make(map[string]bool)
	allDone := make(chan struct{})

	syncHandler := func(repoFullName, cloneURL string) {
		ns := ExtractNamespace(repoFullName)
		_ = CloneOrPull(context.Background(), cloneURL, repoRoot, ns, nil)
		mu.Lock()
		synced[ns] = true
		if len(synced) == 2 {
			select {
			case allDone <- struct{}{}:
			default:
			}
		}
		mu.Unlock()
	}

	q := NewSyncQueue(2, syncHandler)
	defer q.Shutdown()

	handler := NewWebhookHandler(secret, allowlist, q.Add)

	payload1 := makePushEvent("refs/heads/main", "myorg/svc-alpha", bareDir1)
	req1 := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(payload1))
	req1.Header.Set("X-Hub-Signature-256", signPayload(secret, payload1))
	req1.Header.Set("X-GitHub-Event", "push")
	rr1 := httptest.NewRecorder()
	handler.ServeHTTP(rr1, req1)

	payload2 := makePushEvent("refs/heads/master", "myorg/svc-beta", bareDir2)
	req2 := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(payload2))
	req2.Header.Set("X-Hub-Signature-256", signPayload(secret, payload2))
	req2.Header.Set("X-GitHub-Event", "push")
	rr2 := httptest.NewRecorder()
	handler.ServeHTTP(rr2, req2)

	select {
	case <-allDone:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for both syncs")
	}

	alphaDir := RepoDir(repoRoot, "svc-alpha")
	if _, err := os.Stat(filepath.Join(alphaDir, "hello.txt")); err != nil {
		t.Errorf("svc-alpha: expected hello.txt: %v", err)
	}
	if _, err := os.Stat(filepath.Join(alphaDir, "beta-only.txt")); err == nil {
		t.Error("svc-alpha: beta-only.txt should NOT exist (isolation violation)")
	}

	betaDir := RepoDir(repoRoot, "svc-beta")
	if _, err := os.Stat(filepath.Join(betaDir, "hello.txt")); err != nil {
		t.Errorf("svc-beta: expected hello.txt: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(betaDir, "beta-only.txt"))
	if err != nil {
		t.Fatalf("svc-beta: expected beta-only.txt: %v", err)
	}
	if string(data) != "beta content" {
		t.Errorf("svc-beta: beta-only.txt = %q, want %q", string(data), "beta content")
	}
}
