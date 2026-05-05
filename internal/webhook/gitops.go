// @index Repository locking and clone-or-pull operations for webhook sync.
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
	"go.opentelemetry.io/otel/attribute"

	"github.com/tae2089/code-context-graph/internal/obs"
)

// @intent signal that a per-repository lock could not be acquired within the configured wait window.
var ErrRepoLockTimeout = errors.New("repo lock timeout")

// @intent treat repository lock files older than this threshold as abandoned by a previous process.
const repoLockStaleAfter = 30 * time.Minute

// @intent keep repository-scoped git operations serialized across concurrent webhook deliveries.
type RepoLocker struct {
	timeout time.Duration
	mu      sync.Mutex
	locks   map[string]chan struct{}
}

// @intent persist enough lock provenance to detect and clean up stale repository lock files safely.
type repoLockMetadata struct {
	Repo      string    `json:"repo"`
	PID       int       `json:"pid"`
	Hostname  string    `json:"hostname"`
	CreatedAt time.Time `json:"created_at"`
}

// NewRepoLocker creates a per-repository lock coordinator with a bounded wait time.
// @intent serialize concurrent webhook sync for the same repo so git operations do not corrupt the working tree.
// @param timeout bounds how long callers wait before ErrRepoLockTimeout is returned.
// @ensures non-positive timeout falls back to 30 seconds.
func NewRepoLocker(timeout time.Duration) *RepoLocker {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &RepoLocker{timeout: timeout, locks: make(map[string]chan struct{})}
}

// WithLock runs a sync operation while holding both in-process and filesystem repo locks.
// @intent coordinate webhook workers across goroutines and processes before touching a repository checkout.
// @sideEffect creates and removes filesystem lock files under the repo root.
// @param lockRoot is the filesystem root under which lock files are created.
// @param repoFullName is the repository key used for in-memory and filesystem lock scoping.
// @ensures fn runs at most once concurrently per repository across cooperating workers/processes.
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

// @intent gate same-process sync attempts for a repository before filesystem locking is attempted.
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

// @intent coordinate repository sync across processes by creating an exclusive lock file under the repo root.
// @sideEffect creates and removes repository lock files on disk.
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
			slog.WarnContext(ctx, "removed stale repo lock", append(obs.TraceLogArgs(ctx), "repo", repoFullName, "lock", lockFile, "age", age)...)
			continue
		}

		select {
		case <-ctx.Done():
			return nil, nil, fmt.Errorf("%w: %s", ErrRepoLockTimeout, repoFullName)
		case <-ticker.C:
		}
	}
}

// @intent write lock ownership metadata so stale lock cleanup can be diagnosed from the filesystem.
// @sideEffect writes JSON metadata and fsyncs the lock file.
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

// @intent discard abandoned repository lock files after the stale timeout elapses.
// @sideEffect may remove an on-disk lock file.
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

// @intent convert repository names into stable lock-safe filenames.
func lockFileName(repoFullName string) string {
	replacer := strings.NewReplacer("/", "-", "\\", "-", ":", "-")
	return replacer.Replace(repoFullName) + ".lock"
}

// RepoDir maps a namespace to the checkout directory used for repository sync.
// @intent keep workspace naming stable across clone, pull, and downstream build steps.
// @return returns the repository checkout path rooted under repoRoot.
func RepoDir(repoRoot, namespace string) string {
	return filepath.Join(repoRoot, namespace)
}

// CloneOrPull syncs the repository namespace to the default remote branch.
// @intent give webhook handlers a branch-agnostic entry point for standard repo refresh.
// @param auth is the git transport auth method to use when the remote requires credentials.
// @sideEffect may clone or hard-reset the target repository checkout on disk.
func CloneOrPull(ctx context.Context, repoURL, repoRoot, namespace string, auth transport.AuthMethod) error {
	return CloneOrPullBranch(ctx, repoURL, repoRoot, namespace, "", auth)
}

// CloneOrPullBranch ensures the namespace checkout exists and matches the requested branch head.
// @intent reuse the same repo sync path for first clone and subsequent updates.
// @sideEffect creates or hard-resets the namespace checkout on disk.
// @param branch is optional; when empty the repository's current HEAD branch is used during sync.
// @ensures the checkout at RepoDir(repoRoot, namespace) matches the requested remote branch head on success.
func CloneOrPullBranch(ctx context.Context, repoURL, repoRoot, namespace, branch string, auth transport.AuthMethod) error {
	dest := RepoDir(repoRoot, namespace)

	repo, err := git.PlainOpen(dest)
	if err == git.ErrRepositoryNotExists {
		if _, statErr := os.Stat(dest); statErr == nil {
			return fmt.Errorf("repo path exists but is not a git repository: %s", dest)
		} else if !os.IsNotExist(statErr) {
			return fmt.Errorf("stat repo path %s: %w", dest, statErr)
		}
		return cloneRepo(ctx, repoURL, repoRoot, dest, namespace, branch, auth)
	}
	if err != nil {
		return fmt.Errorf("open repo %s: %w", dest, err)
	}

	return syncRepoBranch(ctx, repo, branch, auth)
}

// CloneOrPullBranchLocked wraps branch sync with repository locking.
// @intent prevent overlapping webhook deliveries from cloning or resetting the same checkout simultaneously.
// @ensures branch sync runs under repository locking even when caller passes a nil locker.
func CloneOrPullBranchLocked(ctx context.Context, locker *RepoLocker, repoURL, repoRoot, repoFullName, namespace, branch string, auth transport.AuthMethod) error {
	if locker == nil {
		locker = NewRepoLocker(30 * time.Second)
	}
	return locker.WithLock(ctx, repoRoot, repoFullName, func(ctx context.Context) error {
		return CloneOrPullBranch(ctx, repoURL, repoRoot, namespace, branch, auth)
	})
}

// @intent log clone URLs without leaking embedded credentials.
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

// @intent perform the first namespace clone via a temp directory so partially cloned repos are never promoted.
// @sideEffect creates temporary directories and renames the completed clone into place.
func cloneRepo(ctx context.Context, repoURL, repoRoot, dest, namespace, branch string, auth transport.AuthMethod) error {
	ctx, span := obs.StartChildSpan(ctx, "git.clone", attribute.String("repo.namespace", namespace), attribute.String("git.branch", branch))
	defer span.End()
	slog.InfoContext(ctx, "cloning repository", append(obs.TraceLogArgs(ctx), "url", sanitizeURL(repoURL), "dest", dest)...)

	opts := &git.CloneOptions{
		URL:   repoURL,
		Depth: 2,
	}
	if branch != "" {
		opts.ReferenceName = plumbing.NewBranchReferenceName(branch)
		opts.SingleBranch = true
	}
	if auth != nil {
		opts.Auth = auth
	}

	tmpRoot := filepath.Join(repoRoot, ".tmp")
	if err := os.MkdirAll(tmpRoot, 0755); err != nil {
		return fmt.Errorf("create clone temp root: %w", err)
	}
	tmpDir, err := os.MkdirTemp(tmpRoot, lockFileName(namespace)+"-*")
	if err != nil {
		return fmt.Errorf("create clone temp dir: %w", err)
	}
	cleanupTemp := true
	defer func() {
		if cleanupTemp {
			_ = os.RemoveAll(tmpDir)
		}
	}()

	_, err = git.PlainCloneContext(ctx, tmpDir, false, opts)
	if err != nil {
		return fmt.Errorf("clone %s: %w", sanitizeURL(repoURL), err)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return fmt.Errorf("create repo parent: %w", err)
	}
	if err := os.Rename(tmpDir, dest); err != nil {
		return fmt.Errorf("promote cloned repo: %w", err)
	}
	cleanupTemp = false
	return nil
}

// @intent hard-reset an existing checkout to the latest remote branch head for deterministic rebuilds.
// @sideEffect fetches from origin and rewrites the local worktree state.
func syncRepoBranch(ctx context.Context, repo *git.Repository, branch string, auth transport.AuthMethod) error {
	ctx, span := obs.StartChildSpan(ctx, "git.sync", attribute.String("git.branch", branch))
	defer span.End()
	slog.InfoContext(ctx, "syncing repository to remote branch", append(obs.TraceLogArgs(ctx), "branch", branch)...)

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

// @intent build fetch options that keep sync traffic branch-scoped and shallow when possible.
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
