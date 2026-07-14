package webhook

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/tae2089/code-context-graph/internal/adapters/outbound/gitrepo"
	"github.com/tae2089/code-context-graph/internal/app/reposync"
)

func initBareRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	bareDir := filepath.Join(dir, "bare.git")
	runTestGit(t, dir, "init", "--bare", "--initial-branch=main", bareDir)
	workDir := filepath.Join(dir, "work")
	runTestGit(t, dir, "clone", bareDir, workDir)
	if err := os.WriteFile(filepath.Join(workDir, "hello.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	runTestGit(t, workDir, "add", ".")
	runTestGit(t, workDir, "commit", "-m", "initial")
	runTestGit(t, workDir, "push")
	return bareDir
}

func addFileToBareRepo(t *testing.T, bareDir, fileName, content string) {
	t.Helper()
	workDir := filepath.Join(t.TempDir(), "work")
	runTestGit(t, filepath.Dir(workDir), "clone", bareDir, workDir)
	if err := os.WriteFile(filepath.Join(workDir, fileName), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	runTestGit(t, workDir, "add", ".")
	runTestGit(t, workDir, "commit", "-m", "add "+fileName)
	runTestGit(t, workDir, "push")
}

func runTestGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v in %s failed: %v\n%s", args, dir, err, out)
	}
}

func TestE2E_WebhookToRAG(t *testing.T) {
	bareDir := initBareRepo(t)
	repoRoot := t.TempDir()
	secret := []byte("e2e-secret")

	allowlist := reposync.NewRepoFilter([]string{"myorg/*"})

	var mu sync.Mutex
	var clonedNS string
	var cloneErr error
	done := make(chan struct{})

	onSync := func(_ context.Context, repoFullName, cloneURL, branch string) error {
		defer close(done)
		ns := reposync.ExtractNamespace(repoFullName)
		mu.Lock()
		clonedNS = ns
		mu.Unlock()

		err := gitrepo.CloneOrPullBranch(context.Background(), cloneURL, repoRoot, ns, branch, nil)
		mu.Lock()
		cloneErr = err
		mu.Unlock()
		return nil
	}

	handler := NewWebhookHandlerWithOptions(secret, allowlist, onSync, true)

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

	clonedFile := filepath.Join(gitrepo.RepoDir(repoRoot, clonedNS), "hello.txt")
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

	allowlist := reposync.NewRepoFilter([]string{"myorg/*"})

	var mu sync.Mutex
	var results []struct {
		ns  string
		err error
	}
	var wg sync.WaitGroup

	onSync := func(_ context.Context, repoFullName, cloneURL, branch string) error {
		defer wg.Done()
		ns := reposync.ExtractNamespace(repoFullName)
		err := gitrepo.CloneOrPullBranch(context.Background(), cloneURL, repoRoot, ns, branch, nil)
		mu.Lock()
		results = append(results, struct {
			ns  string
			err error
		}{ns: ns, err: err})
		mu.Unlock()
		return nil
	}

	handler := NewWebhookHandlerWithOptions(secret, allowlist, onSync, true)

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
	payload2 := makePushEvent("refs/heads/main", "myorg/svc-beta", bareDir2)
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

	alphaDir := gitrepo.RepoDir(repoRoot, "svc-alpha")
	if _, err := os.Stat(filepath.Join(alphaDir, "hello.txt")); err != nil {
		t.Errorf("svc-alpha: expected hello.txt: %v", err)
	}
	if _, err := os.Stat(filepath.Join(alphaDir, "repo2-only.txt")); err == nil {
		t.Error("svc-alpha: repo2-only.txt should NOT exist (isolation violation)")
	}

	betaDir := gitrepo.RepoDir(repoRoot, "svc-beta")
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

	allowlist := reposync.NewRepoFilter([]string{"myorg/*"})

	var callCount int32
	var mu sync.Mutex
	done := make(chan struct{}, 1)

	syncHandler := func(_ context.Context, repoFullName, cloneURL, branch string) error {
		ns := reposync.ExtractNamespace(repoFullName)
		_ = gitrepo.CloneOrPullBranch(context.Background(), cloneURL, repoRoot, ns, branch, nil)
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
		return nil
	}

	q := reposync.NewSyncQueue(2, syncHandler)
	defer q.Shutdown()

	handler := NewWebhookHandlerWithOptions(secret, allowlist, q.Add, true)

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

	clonedFile := filepath.Join(gitrepo.RepoDir(repoRoot, "pay-svc"), "hello.txt")
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

	allowlist := reposync.NewRepoFilter([]string{"myorg/*"})

	var mu sync.Mutex
	synced := make(map[string]bool)
	allDone := make(chan struct{})

	syncHandler := func(_ context.Context, repoFullName, cloneURL, branch string) error {
		ns := reposync.ExtractNamespace(repoFullName)
		_ = gitrepo.CloneOrPullBranch(context.Background(), cloneURL, repoRoot, ns, branch, nil)
		mu.Lock()
		synced[ns] = true
		if len(synced) == 2 {
			select {
			case allDone <- struct{}{}:
			default:
			}
		}
		mu.Unlock()
		return nil
	}

	q := reposync.NewSyncQueue(2, syncHandler)
	defer q.Shutdown()

	handler := NewWebhookHandlerWithOptions(secret, allowlist, q.Add, true)

	payload1 := makePushEvent("refs/heads/main", "myorg/svc-alpha", bareDir1)
	req1 := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(payload1))
	req1.Header.Set("X-Hub-Signature-256", signPayload(secret, payload1))
	req1.Header.Set("X-GitHub-Event", "push")
	rr1 := httptest.NewRecorder()
	handler.ServeHTTP(rr1, req1)

	payload2 := makePushEvent("refs/heads/main", "myorg/svc-beta", bareDir2)
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

	alphaDir := gitrepo.RepoDir(repoRoot, "svc-alpha")
	if _, err := os.Stat(filepath.Join(alphaDir, "hello.txt")); err != nil {
		t.Errorf("svc-alpha: expected hello.txt: %v", err)
	}
	if _, err := os.Stat(filepath.Join(alphaDir, "beta-only.txt")); err == nil {
		t.Error("svc-alpha: beta-only.txt should NOT exist (isolation violation)")
	}

	betaDir := gitrepo.RepoDir(repoRoot, "svc-beta")
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
