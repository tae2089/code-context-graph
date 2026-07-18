// @index Repository checkout-to-graph synchronization application workflow.
package reposync

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// @intent coordinate admitted repository checkout, config loading, graph replacement, and cache invalidation.
type Service struct {
	Checkout            Checkout
	BuildScope          BuildScopeLoader
	Graph               GraphUpdater
	Cache               CacheInvalidator
	Observability       Observability
	AttemptTimeout      time.Duration
	MaxFileBytes        int64
	MaxTotalParsedBytes int64
	FailOnUnreadable    bool
	Logger              *slog.Logger
}

// Sync checks out one admitted repository, updates its graph namespace, and invalidates query cache on success.
// @intent make repository synchronization ordering reusable outside HTTP server composition.
func (s *Service) Sync(ctx context.Context, repoFullName, cloneURL, branch string) error {
	if s.Checkout == nil || s.BuildScope == nil || s.Graph == nil {
		return fmt.Errorf("repository sync dependencies are not configured")
	}
	ns := ExtractNamespace(repoFullName)
	observability := s.Observability
	if observability == nil {
		observability = noopObservability{}
	}
	ctx, end := observability.Start(ctx, "webhook.sync", repoFullName, branch)
	defer end()
	if s.AttemptTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, s.AttemptTimeout)
		defer cancel()
	}
	logger := s.Logger
	if logger == nil {
		logger = slog.Default()
	}
	baseLogArgs := func() []any {
		return append(observability.LogArgs(ctx), "repo", repoFullName, "namespace", ns, "branch", branch)
	}
	logger.InfoContext(ctx, "webhook sync started", baseLogArgs()...)
	repoDir, err := s.Checkout.Sync(ctx, CheckoutRequest{RepoFullName: repoFullName, CloneURL: cloneURL, Namespace: ns, Branch: branch})
	if err != nil {
		logger.ErrorContext(ctx, "webhook clone/pull failed", append(baseLogArgs(), "error", err)...)
		return err
	}
	buildScope, err := s.BuildScope.Load(repoDir)
	if err != nil {
		logger.ErrorContext(ctx, "webhook build scope config invalid", append(baseLogArgs(), "error", err)...)
		return NonRetryable(err)
	}
	stats, err := s.Graph.Update(ctx, GraphRequest{RepoDir: repoDir, Namespace: ns, IncludePaths: buildScope.IncludePaths, ExcludePatterns: buildScope.ExcludePatterns, MaxFileBytes: s.MaxFileBytes, MaxTotalParsedBytes: s.MaxTotalParsedBytes, FailOnUnreadable: s.FailOnUnreadable})
	if err != nil {
		logger.ErrorContext(ctx, "webhook update failed", append(baseLogArgs(), "error", err)...)
		return err
	}
	if s.Cache != nil {
		s.Cache.Invalidate()
	}
	logger.InfoContext(ctx, "webhook sync completed", append(observability.LogArgs(ctx), "repo", repoFullName, "namespace", ns, "added", stats.Added, "modified", stats.Modified, "skipped", stats.Skipped, "deleted", stats.Deleted)...)
	return nil
}
