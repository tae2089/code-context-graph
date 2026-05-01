package webhook

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/transport"
)

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
