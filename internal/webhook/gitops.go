package webhook

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/transport"
)

func RepoDir(repoRoot, namespace string) string {
	return filepath.Join(repoRoot, namespace)
}

func CloneOrPull(ctx context.Context, repoURL, repoRoot, namespace string, auth transport.AuthMethod) error {
	dest := RepoDir(repoRoot, namespace)

	repo, err := git.PlainOpen(dest)
	if err == git.ErrRepositoryNotExists {
		return cloneRepo(ctx, repoURL, dest, auth)
	}
	if err != nil {
		return fmt.Errorf("open repo %s: %w", dest, err)
	}

	return pullRepo(ctx, repo, auth)
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

func cloneRepo(ctx context.Context, repoURL, dest string, auth transport.AuthMethod) error {
	slog.Info("cloning repository", "url", sanitizeURL(repoURL), "dest", dest)

	opts := &git.CloneOptions{
		URL:   repoURL,
		Depth: 1,
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

func pullRepo(ctx context.Context, repo *git.Repository, auth transport.AuthMethod) error {
	slog.Info("pulling repository updates")

	wt, err := repo.Worktree()
	if err != nil {
		return fmt.Errorf("worktree: %w", err)
	}

	opts := &git.PullOptions{}
	if auth != nil {
		opts.Auth = auth
	}

	err = wt.PullContext(ctx, opts)
	if err == git.NoErrAlreadyUpToDate {
		slog.Info("repository already up-to-date")
		return nil
	}
	if err != nil {
		return fmt.Errorf("pull: %w", err)
	}
	return nil
}
