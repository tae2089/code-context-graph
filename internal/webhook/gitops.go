package webhook

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/transport"
)

var ErrRepoLockTimeout = errors.New("repo lock timeout")

const repoLockStaleAfter = 30 * time.Minute

type RepoLocker struct {
	timeout time.Duration
	mu      sync.Mutex
	locks   map[string]chan struct{}
}

type repoLockMetadata struct {
	Repo      string    `json:"repo"`
	PID       int       `json:"pid"`
	Hostname  string    `json:"hostname"`
	CreatedAt time.Time `json:"created_at"`
}

func NewRepoLocker(timeout time.Duration) *RepoLocker {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &RepoLocker{timeout: timeout, locks: make(map[string]chan struct{})}
}

func (l *RepoLocker) WithLock(ctx context.Context, lockRoot, repoFullName string, fn func(context.Context) error) error {
	if ctx == nil {
		ctx = context.Background()
	}
	acquireCtx, cancel := context.WithTimeout(ctx, l.timeout)
	defer cancel()

	localLock, releaseLocal, err := l.acquireLocal(acquireCtx, repoFullName)
	if err != nil {
		return err
	}
	defer releaseLocal(localLock)

	lockFile, releaseFile, err := acquireFilesystemLock(acquireCtx, lockRoot, repoFullName)
	if err != nil {
		return err
	}
	defer releaseFile(lockFile)

	return fn(ctx)
}

func (l *RepoLocker) acquireLocal(ctx context.Context, repoFullName string) (chan struct{}, func(chan struct{}), error) {
	l.mu.Lock()
	lock := l.locks[repoFullName]
	if lock == nil {
		lock = make(chan struct{}, 1)
		lock <- struct{}{}
		l.locks[repoFullName] = lock
	}
	l.mu.Unlock()

	select {
	case <-lock:
		return lock, func(lock chan struct{}) { lock <- struct{}{} }, nil
	case <-ctx.Done():
		return nil, nil, fmt.Errorf("%w: %s", ErrRepoLockTimeout, repoFullName)
	}
}

func acquireFilesystemLock(ctx context.Context, lockRoot, repoFullName string) (*os.File, func(*os.File), error) {
	lockFile := filepath.Join(lockRoot, ".locks", lockFileName(repoFullName))
	if err := os.MkdirAll(filepath.Dir(lockFile), 0755); err != nil {
		return nil, nil, fmt.Errorf("create lock directory: %w", err)
	}

	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	for {
		file, err := os.OpenFile(lockFile, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
		if err == nil {
			if err := writeRepoLockMetadata(file, repoFullName); err != nil {
				name := file.Name()
				_ = file.Close()
				_ = os.Remove(name)
				return nil, nil, fmt.Errorf("write repo lock metadata: %w", err)
			}
			return file, func(file *os.File) {
				name := file.Name()
				_ = file.Close()
				_ = os.Remove(name)
			}, nil
		}
		if !os.IsExist(err) {
			return nil, nil, fmt.Errorf("create repo lock: %w", err)
		}
		if removed, age := removeStaleFilesystemLock(lockFile); removed {
			slog.Warn("removed stale repo lock", "repo", repoFullName, "lock", lockFile, "age", age)
			continue
		}

		select {
		case <-ctx.Done():
			return nil, nil, fmt.Errorf("%w: %s", ErrRepoLockTimeout, repoFullName)
		case <-ticker.C:
		}
	}
}

func writeRepoLockMetadata(file *os.File, repoFullName string) error {
	hostname, err := os.Hostname()
	if err != nil {
		hostname = "unknown"
	}
	meta := repoLockMetadata{
		Repo:      repoFullName,
		PID:       os.Getpid(),
		Hostname:  hostname,
		CreatedAt: time.Now().UTC(),
	}
	if err := json.NewEncoder(file).Encode(meta); err != nil {
		return err
	}
	return file.Sync()
}

func removeStaleFilesystemLock(lockFile string) (bool, time.Duration) {
	info, err := os.Stat(lockFile)
	if err != nil {
		return false, 0
	}
	age := time.Since(info.ModTime())
	if age < repoLockStaleAfter {
		return false, age
	}
	if err := os.Remove(lockFile); err != nil {
		return false, age
	}
	return true, age
}

func lockFileName(repoFullName string) string {
	replacer := strings.NewReplacer("/", "-", "\\", "-", ":", "-")
	return replacer.Replace(repoFullName) + ".lock"
}

func RepoDir(repoRoot, namespace string) string {
	return filepath.Join(repoRoot, namespace)
}

func CloneOrPull(ctx context.Context, repoURL, repoRoot, namespace string, auth transport.AuthMethod) error {
	return CloneOrPullBranch(ctx, repoURL, repoRoot, namespace, "", auth)
}

func CloneOrPullBranch(ctx context.Context, repoURL, repoRoot, namespace, branch string, auth transport.AuthMethod) error {
	dest := RepoDir(repoRoot, namespace)

	repo, err := git.PlainOpen(dest)
	if err == git.ErrRepositoryNotExists {
		return cloneRepo(ctx, repoURL, dest, branch, auth)
	}
	if err != nil {
		return fmt.Errorf("open repo %s: %w", dest, err)
	}

	return syncRepoBranch(ctx, repo, branch, auth)
}

func CloneOrPullBranchLocked(ctx context.Context, locker *RepoLocker, repoURL, repoRoot, repoFullName, namespace, branch string, auth transport.AuthMethod) error {
	if locker == nil {
		locker = NewRepoLocker(30 * time.Second)
	}
	return locker.WithLock(ctx, repoRoot, repoFullName, func(ctx context.Context) error {
		return CloneOrPullBranch(ctx, repoURL, repoRoot, namespace, branch, auth)
	})
}

func sanitizeURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "***"
	}
	if u.User != nil {
		u.User = url.User("***")
	}
	return u.String()
}

func cloneRepo(ctx context.Context, repoURL, dest, branch string, auth transport.AuthMethod) error {
	slog.Info("cloning repository", "url", sanitizeURL(repoURL), "dest", dest)

	opts := &git.CloneOptions{
		URL:   repoURL,
		Depth: 1,
	}
	if branch != "" {
		opts.ReferenceName = plumbing.NewBranchReferenceName(branch)
		opts.SingleBranch = true
	}
	if auth != nil {
		opts.Auth = auth
	}

	_, err := git.PlainCloneContext(ctx, dest, false, opts)
	if err != nil {
		os.RemoveAll(dest)
		return fmt.Errorf("clone %s: %w", sanitizeURL(repoURL), err)
	}
	return nil
}

func syncRepoBranch(ctx context.Context, repo *git.Repository, branch string, auth transport.AuthMethod) error {
	slog.Info("syncing repository to remote branch", "branch", branch)

	wt, err := repo.Worktree()
	if err != nil {
		return fmt.Errorf("worktree: %w", err)
	}

	remoteName := "origin"
	if err := repo.FetchContext(ctx, fetchOptions(remoteName, branch, auth)); err != nil && err != git.NoErrAlreadyUpToDate {
		return fmt.Errorf("fetch: %w", err)
	}

	if branch == "" {
		head, err := repo.Head()
		if err != nil {
			return fmt.Errorf("head: %w", err)
		}
		branch = head.Name().Short()
	}

	remoteRef := plumbing.NewRemoteReferenceName(remoteName, branch)
	ref, err := repo.Reference(remoteRef, true)
	if err != nil {
		return fmt.Errorf("remote branch %s: %w", branch, err)
	}

	branchRef := plumbing.NewBranchReferenceName(branch)
	if err := wt.Checkout(&git.CheckoutOptions{Branch: branchRef, Force: true}); err != nil {
		if err := wt.Checkout(&git.CheckoutOptions{Branch: branchRef, Hash: ref.Hash(), Create: true, Force: true}); err != nil {
			return fmt.Errorf("checkout %s: %w", branch, err)
		}
	}

	if err := wt.Reset(&git.ResetOptions{Commit: ref.Hash(), Mode: git.HardReset}); err != nil {
		return fmt.Errorf("reset %s: %w", branch, err)
	}
	if err := wt.Clean(&git.CleanOptions{Dir: true}); err != nil {
		return fmt.Errorf("clean worktree: %w", err)
	}
	return nil
}

func fetchOptions(remoteName, branch string, auth transport.AuthMethod) *git.FetchOptions {
	opts := &git.FetchOptions{RemoteName: remoteName, Depth: 1}
	if branch != "" {
		branchRef := plumbing.NewBranchReferenceName(branch)
		remoteRef := plumbing.NewRemoteReferenceName(remoteName, branch)
		opts.RefSpecs = []config.RefSpec{config.RefSpec("+" + branchRef.String() + ":" + remoteRef.String())}
	}
	if auth != nil {
		opts.Auth = auth
	}
	return opts
}
