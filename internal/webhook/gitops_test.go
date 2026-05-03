package webhook

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func initBareRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	bareDir := filepath.Join(dir, "bare.git")

	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=t@t")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, out)
		}
	}

	run("init", "--bare", bareDir)

	workDir := filepath.Join(dir, "work")
	run("clone", bareDir, workDir)

	if err := os.WriteFile(filepath.Join(workDir, "hello.txt"), []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}

	runIn := func(d string, args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = d
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=t@t")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v in %s failed: %v\n%s", args, d, err, out)
		}
	}

	runIn(workDir, "add", ".")
	runIn(workDir, "commit", "-m", "initial")
	runIn(workDir, "push")

	return bareDir
}

func addFileToBareRepo(t *testing.T, bareDir, fileName, content string) {
	t.Helper()
	dir := t.TempDir()
	workDir := filepath.Join(dir, "work")

	runIn := func(d string, args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = d
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=t@t")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v in %s failed: %v\n%s", args, d, err, out)
		}
	}

	cmd := exec.Command("git", "clone", bareDir, workDir)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("clone failed: %v\n%s", err, out)
	}

	if err := os.WriteFile(filepath.Join(workDir, fileName), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	runIn(workDir, "add", ".")
	runIn(workDir, "commit", "-m", "add "+fileName)
	runIn(workDir, "push")
}

func addBranchFileToBareRepo(t *testing.T, bareDir, branch, fileName, content string) {
	t.Helper()
	dir := t.TempDir()
	workDir := filepath.Join(dir, "work")

	runIn := func(d string, args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = d
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=t@t")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v in %s failed: %v\n%s", args, d, err, out)
		}
	}

	cmd := exec.Command("git", "clone", bareDir, workDir)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("clone failed: %v\n%s", err, out)
	}

	runIn(workDir, "checkout", "-b", branch)
	if err := os.WriteFile(filepath.Join(workDir, fileName), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	runIn(workDir, "add", ".")
	runIn(workDir, "commit", "-m", "add "+fileName)
	runIn(workDir, "push", "origin", branch)
}

func TestGitOps_RepoDir(t *testing.T) {
	got := RepoDir("/var/repos", "acme/pay-svc")
	want := filepath.Join("/var/repos", "acme/pay-svc")
	if got != want {
		t.Errorf("RepoDir = %q, want %q", got, want)
	}
}

func TestGitOps_CloneRepo(t *testing.T) {
	bareDir := initBareRepo(t)
	destRoot := t.TempDir()
	ns := "test-ns"

	err := CloneOrPull(context.Background(), bareDir, destRoot, ns, nil)
	if err != nil {
		t.Fatalf("CloneOrPull (clone) failed: %v", err)
	}

	dest := RepoDir(destRoot, ns)
	if _, err := os.Stat(filepath.Join(dest, "hello.txt")); err != nil {
		t.Errorf("expected hello.txt to exist after clone: %v", err)
	}
}

func TestGitOps_CloneRepo_KeepsParentCommitForHEADTildeOne(t *testing.T) {
	bareDir := initBareRepo(t)
	addFileToBareRepo(t, bareDir, "second.txt", "second")
	destRoot := t.TempDir()
	ns := "test-ns"

	if err := CloneOrPullBranch(context.Background(), bareDir, destRoot, ns, "main", nil); err != nil {
		t.Fatalf("CloneOrPullBranch failed: %v", err)
	}

	dest := RepoDir(destRoot, ns)
	cmd := exec.Command("git", "rev-parse", "HEAD~1")
	cmd.Dir = dest
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("expected HEAD~1 to exist in cloned webhook repo: %v\n%s", err, out)
	}
}

func TestGitOps_CloneFailurePreservesExistingNonRepoDir(t *testing.T) {
	destRoot := t.TempDir()
	ns := "test-ns"
	dest := RepoDir(destRoot, ns)
	if err := os.MkdirAll(dest, 0755); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(dest, "keep.txt")
	if err := os.WriteFile(sentinel, []byte("keep"), 0644); err != nil {
		t.Fatal(err)
	}

	err := CloneOrPullBranch(context.Background(), "file:///does/not/exist", destRoot, ns, "main", nil)
	if err == nil {
		t.Fatal("expected CloneOrPullBranch to fail")
	}
	data, readErr := os.ReadFile(sentinel)
	if readErr != nil {
		t.Fatalf("existing file should remain after clone failure: %v", readErr)
	}
	if string(data) != "keep" {
		t.Fatalf("existing file content = %q, want keep", string(data))
	}
}

func TestGitOps_CloneFailureRemovesOnlyTempDir(t *testing.T) {
	destRoot := t.TempDir()
	ns := "test-ns"

	err := CloneOrPullBranch(context.Background(), "file:///does/not/exist", destRoot, ns, "main", nil)
	if err == nil {
		t.Fatal("expected CloneOrPullBranch to fail")
	}
	if _, statErr := os.Stat(RepoDir(destRoot, ns)); !os.IsNotExist(statErr) {
		t.Fatalf("dest should not be created on clone failure, stat err=%v", statErr)
	}
	entries, readErr := os.ReadDir(filepath.Join(destRoot, ".tmp"))
	if readErr != nil {
		t.Fatalf("read temp root: %v", readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("expected clone temp dir cleanup, found %d entries", len(entries))
	}
}

func TestGitOps_PullUpdates(t *testing.T) {
	bareDir := initBareRepo(t)
	destRoot := t.TempDir()
	ns := "test-ns"

	err := CloneOrPull(context.Background(), bareDir, destRoot, ns, nil)
	if err != nil {
		t.Fatalf("initial clone failed: %v", err)
	}

	addFileToBareRepo(t, bareDir, "new.txt", "new content")

	err = CloneOrPull(context.Background(), bareDir, destRoot, ns, nil)
	if err != nil {
		t.Fatalf("CloneOrPull (pull) failed: %v", err)
	}

	dest := RepoDir(destRoot, ns)
	data, err := os.ReadFile(filepath.Join(dest, "new.txt"))
	if err != nil {
		t.Fatalf("expected new.txt to exist after pull: %v", err)
	}
	if string(data) != "new content" {
		t.Errorf("new.txt content = %q, want %q", string(data), "new content")
	}
}

func TestGitOps_CloneOrPullBranchSyncsRequestedBranch(t *testing.T) {
	bareDir := initBareRepo(t)
	addBranchFileToBareRepo(t, bareDir, "feature/harden", "feature.txt", "branch content")
	destRoot := t.TempDir()
	ns := "test-ns"

	if err := CloneOrPullBranch(context.Background(), bareDir, destRoot, ns, "feature/harden", nil); err != nil {
		t.Fatalf("CloneOrPullBranch failed: %v", err)
	}

	dest := RepoDir(destRoot, ns)
	data, err := os.ReadFile(filepath.Join(dest, "feature.txt"))
	if err != nil {
		t.Fatalf("expected feature.txt from requested branch: %v", err)
	}
	if string(data) != "branch content" {
		t.Fatalf("feature.txt = %q, want %q", string(data), "branch content")
	}
}

func TestGitOps_CloneOrPullBranchCleansWorktreeDrift(t *testing.T) {
	bareDir := initBareRepo(t)
	destRoot := t.TempDir()
	ns := "test-ns"

	if err := CloneOrPullBranch(context.Background(), bareDir, destRoot, ns, "main", nil); err != nil {
		t.Fatalf("initial clone failed: %v", err)
	}
	dest := RepoDir(destRoot, ns)
	if err := os.WriteFile(filepath.Join(dest, "hello.txt"), []byte("local drift"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dest, "untracked.txt"), []byte("remove me"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := CloneOrPullBranch(context.Background(), bareDir, destRoot, ns, "main", nil); err != nil {
		t.Fatalf("resync failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dest, "hello.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello" {
		t.Fatalf("hello.txt = %q, want remote content", string(data))
	}
	if _, err := os.Stat(filepath.Join(dest, "untracked.txt")); !os.IsNotExist(err) {
		t.Fatalf("untracked drift should be removed, stat err=%v", err)
	}
}

func TestRepoLocker_SerializesSameRepoInProcess(t *testing.T) {
	lockRoot := t.TempDir()
	locker := NewRepoLocker(2 * time.Second)

	entered := make(chan struct{})
	release := make(chan struct{})
	var concurrent int32
	var maxConcurrent int32

	go func() {
		err := locker.WithLock(context.Background(), lockRoot, "org/svc", func(context.Context) error {
			current := atomic.AddInt32(&concurrent, 1)
			atomic.StoreInt32(&maxConcurrent, current)
			close(entered)
			<-release
			atomic.AddInt32(&concurrent, -1)
			return nil
		})
		if err != nil {
			t.Errorf("first lock failed: %v", err)
		}
	}()
	<-entered

	secondDone := make(chan error, 1)
	go func() {
		secondDone <- locker.WithLock(context.Background(), lockRoot, "org/svc", func(context.Context) error {
			current := atomic.AddInt32(&concurrent, 1)
			for {
				prev := atomic.LoadInt32(&maxConcurrent)
				if current <= prev || atomic.CompareAndSwapInt32(&maxConcurrent, prev, current) {
					break
				}
			}
			atomic.AddInt32(&concurrent, -1)
			return nil
		})
	}()

	select {
	case err := <-secondDone:
		t.Fatalf("second lock finished before first released: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)

	select {
	case err := <-secondDone:
		if err != nil {
			t.Fatalf("second lock failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for second lock")
	}
	if maxConcurrent != 1 {
		t.Fatalf("same repo operations overlapped, maxConcurrent=%d", maxConcurrent)
	}
}

func TestRepoLocker_TimesOutWhenFilesystemLockHeld(t *testing.T) {
	lockRoot := t.TempDir()
	locker := NewRepoLocker(30 * time.Millisecond)

	lockFile := filepath.Join(lockRoot, ".locks", "org-svc.lock")
	if err := os.MkdirAll(filepath.Dir(lockFile), 0755); err != nil {
		t.Fatal(err)
	}
	held, err := os.OpenFile(lockFile, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	defer os.Remove(lockFile)

	err = locker.WithLock(context.Background(), lockRoot, "org/svc", func(context.Context) error {
		t.Fatal("callback should not run while filesystem lock is held")
		return nil
	})
	if !errors.Is(err, ErrRepoLockTimeout) {
		t.Fatalf("error = %v, want ErrRepoLockTimeout", err)
	}
}

func TestRepoLocker_RemovesStaleFilesystemLock(t *testing.T) {
	lockRoot := t.TempDir()
	locker := NewRepoLocker(2 * time.Second)

	lockFile := filepath.Join(lockRoot, ".locks", "org-svc.lock")
	if err := os.MkdirAll(filepath.Dir(lockFile), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lockFile, []byte(`{"repo":"org/svc"}`), 0600); err != nil {
		t.Fatal(err)
	}
	staleTime := time.Now().Add(-(repoLockStaleAfter + time.Minute))
	if err := os.Chtimes(lockFile, staleTime, staleTime); err != nil {
		t.Fatal(err)
	}

	entered := make(chan struct{})
	err := locker.WithLock(context.Background(), lockRoot, "org/svc", func(context.Context) error {
		if _, statErr := os.Stat(lockFile); statErr != nil {
			t.Fatalf("expected replacement lock file during callback: %v", statErr)
		}
		close(entered)
		return nil
	})
	if err != nil {
		t.Fatalf("WithLock: %v", err)
	}
	select {
	case <-entered:
	default:
		t.Fatal("callback did not run")
	}
	if _, err := os.Stat(lockFile); !os.IsNotExist(err) {
		t.Fatalf("expected lock file removed after callback, stat err=%v", err)
	}
}

func TestRepoLocker_RemovesMalformedStaleFilesystemLock(t *testing.T) {
	lockRoot := t.TempDir()
	locker := NewRepoLocker(2 * time.Second)

	lockFile := filepath.Join(lockRoot, ".locks", "org-svc.lock")
	if err := os.MkdirAll(filepath.Dir(lockFile), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lockFile, []byte(`not-json`), 0600); err != nil {
		t.Fatal(err)
	}
	staleTime := time.Now().Add(-(repoLockStaleAfter + time.Minute))
	if err := os.Chtimes(lockFile, staleTime, staleTime); err != nil {
		t.Fatal(err)
	}

	called := false
	if err := locker.WithLock(context.Background(), lockRoot, "org/svc", func(context.Context) error {
		called = true
		return nil
	}); err != nil {
		t.Fatalf("WithLock: %v", err)
	}
	if !called {
		t.Fatal("expected callback to run after malformed stale lock removal")
	}
}

func TestRepoLocker_DoesNotRemoveFreshMalformedFilesystemLock(t *testing.T) {
	lockRoot := t.TempDir()
	locker := NewRepoLocker(30 * time.Millisecond)

	lockFile := filepath.Join(lockRoot, ".locks", "org-svc.lock")
	if err := os.MkdirAll(filepath.Dir(lockFile), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lockFile, []byte(`not-json`), 0600); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(lockFile)

	err := locker.WithLock(context.Background(), lockRoot, "org/svc", func(context.Context) error {
		t.Fatal("callback should not run while fresh filesystem lock is held")
		return nil
	})
	if !errors.Is(err, ErrRepoLockTimeout) {
		t.Fatalf("error = %v, want ErrRepoLockTimeout", err)
	}
	if _, err := os.Stat(lockFile); err != nil {
		t.Fatalf("fresh malformed lock should remain, stat err=%v", err)
	}
}

func TestRepoLocker_AllowsDifferentReposInParallel(t *testing.T) {
	lockRoot := t.TempDir()
	locker := NewRepoLocker(2 * time.Second)
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	var wg sync.WaitGroup

	for _, repo := range []string{"org/a", "org/b"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := locker.WithLock(context.Background(), lockRoot, repo, func(context.Context) error {
				started <- struct{}{}
				<-release
				return nil
			})
			if err != nil {
				t.Errorf("lock %s failed: %v", repo, err)
			}
		}()
	}

	for i := 0; i < 2; i++ {
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			t.Fatal("different repo lock did not start in parallel")
		}
	}
	close(release)
	wg.Wait()
}
